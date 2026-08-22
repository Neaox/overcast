//go:build dev

package main

import "testing"

// TestDeclaredWireStrings_resolvesLiteralsAndConstants pins the reader every
// check in wirefacts.go depends on.
//
// The failure mode a source-reading gate is least likely to notice about itself
// is finding nothing: a reader that silently returns no strings makes every
// check pass while proving nothing, and it looks exactly like a clean run. This
// asserts both spellings the service packages actually use — a string literal
// returned directly, and a package-level constant returned by name — and that a
// service with no such method is reported as absent rather than as empty.
func TestDeclaredWireStrings_resolvesLiteralsAndConstants(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "service.go", `package svc

const apiPrefix = "/2021-01-01"

type Service struct{}

func (s *Service) TargetPrefix() string { return "Widgets_20130101." }

func (s *Service) PathPrefixes() []string {
	return []string{apiPrefix, "/widgets"}
}
`)

	prefix, found, err := declaredWireStrings(dir, "TargetPrefix")
	if err != nil {
		t.Fatalf("declaredWireStrings(TargetPrefix): %v", err)
	}
	if !found || len(prefix) != 1 || prefix[0] != "Widgets_20130101." {
		t.Errorf("declaredWireStrings(TargetPrefix) = %v, found=%v; want [Widgets_20130101.] found", prefix, found)
	}

	paths, found, err := declaredWireStrings(dir, "PathPrefixes")
	if err != nil {
		t.Fatalf("declaredWireStrings(PathPrefixes): %v", err)
	}
	// Sorted, so the constant and the literal are compared by value.
	if !found || len(paths) != 2 || paths[0] != "/2021-01-01" || paths[1] != "/widgets" {
		t.Errorf("declaredWireStrings(PathPrefixes) = %v, found=%v; want [/2021-01-01 /widgets] found", paths, found)
	}

	if _, found, _ := declaredWireStrings(dir, "SigningName"); found {
		t.Error("declaredWireStrings(SigningName) reported found for a method the package does not declare")
	}
}

// TestDeclaredWireStrings_readsTheRealServicePackages is the guard against the
// whole gate passing on an empty read.
//
// checkPathPrefixesAgainstTheModel has no ledger — nothing in the tree violates
// it — so a reader that stopped resolving anything would look identical to a
// clean run. Two live packages are asserted by value: EKS, whose PathPrefixes
// are literals, and OpenSearch, whose single prefix is a constant. Both are
// facts the pinned model also states, which is the point of the check.
func TestDeclaredWireStrings_readsTheRealServicePackages(t *testing.T) {
	root, err := findWorkspaceRoot(".")
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	eks, found, err := declaredWireStrings(serviceSourceDir(root, "eks"), "PathPrefixes")
	if err != nil || !found {
		t.Fatalf("declaredWireStrings(eks PathPrefixes) found=%v err=%v", found, err)
	}
	if len(eks) == 0 || eks[0] != "/access-policies" {
		t.Errorf("eks PathPrefixes = %v, want the four literal paths it declares", eks)
	}

	opensearch, found, err := declaredWireStrings(serviceSourceDir(root, "opensearch"), "PathPrefixes")
	if err != nil || !found {
		t.Fatalf("declaredWireStrings(opensearch PathPrefixes) found=%v err=%v", found, err)
	}
	if len(opensearch) != 1 || opensearch[0] != "/2021-01-01" {
		t.Errorf("opensearch PathPrefixes = %v, want [/2021-01-01] resolved from the apiPrefix constant", opensearch)
	}

	prefix, found, err := declaredWireStrings(serviceSourceDir(root, "cloudwatch-logs"), "TargetPrefix")
	if err != nil || !found {
		t.Fatalf("declaredWireStrings(cloudwatch-logs TargetPrefix) found=%v err=%v", found, err)
	}
	if len(prefix) != 1 || prefix[0] != "Logs_20140328." {
		t.Errorf("cloudwatch-logs TargetPrefix = %v, want [Logs_20140328.] — and serviceSourceDir must resolve the sub-service directory", prefix)
	}
}

// TestPrefixIsModeled_wantsASubtreeNotAStringPrefix guards the comparison the
// path-prefix check rests on.
//
// A claimed prefix owns a subtree, so a modeled URI has to continue with "/" or
// with the query the template pins. Plain strings.HasPrefix would let
// "/2021-01-0" satisfy itself against "/2021-01-01/domain" and quietly accept a
// truncated claim.
func TestPrefixIsModeled_wantsASubtreeNotAStringPrefix(t *testing.T) {
	uris := []string{"/2021-01-01/domain", "/2021-01-01/tags?arn", "/clusters"}

	for _, prefix := range []string{"/2021-01-01", "/clusters"} {
		if !prefixIsModeled(prefix, uris) {
			t.Errorf("prefixIsModeled(%q) = false, want true", prefix)
		}
	}
	for _, prefix := range []string{"/2021-01-0", "/cluster", "/_opensearch", "/clustersx"} {
		if prefixIsModeled(prefix, uris) {
			t.Errorf("prefixIsModeled(%q) = true, want false — a prefix claims a subtree, not a string", prefix)
		}
	}
}

// TestCheckDocOnlyRowsNameRealOperations_holdsOperationShapedNamesOnly asserts
// the filter that lets DocOnly keep documenting things that are not operations.
func TestCheckDocOnlyRowsNameRealOperations_holdsOperationShapedNamesOnly(t *testing.T) {
	// Given: DocOnly rows in the three shapes that exist in the tree — a real
	// operation, prose, and an operation-shaped name AWS does not model for
	// this service.
	caps := []CapabilityDecl{
		{Service: "secretsmanager", Operation: "ListSecrets", DocOnly: true},
		{Service: "sns", Operation: "SMS publish", DocOnly: true},
		{Service: "cloudformation", Operation: "Fn::ImportValue", DocOnly: true},
		{Service: "secretsmanager", Operation: "RotateSecretsQuickly", DocOnly: true},
	}

	// When: the check runs over a partial set, so the ledger's own entries are
	// not judged stale for being absent.
	violations := checkDocOnlyRowsNameRealOperations(caps, false)

	// Then: only the operation-shaped unknown is a finding. ListSecrets is a
	// real operation, and the other two are plainly not operation names.
	if violations != 1 {
		t.Errorf("checkDocOnlyRowsNameRealOperations() = %d violations, want 1 (only RotateSecretsQuickly)", violations)
	}

	// And: over the whole set, every ledger entry is reported as stale, which is
	// the ratchet's second direction working.
	if got, want := checkDocOnlyRowsNameRealOperations(caps, true), len(docOnlyRowsOutsideTheModel)+1; got != want {
		t.Errorf("checkDocOnlyRowsNameRealOperations(complete) = %d violations, want %d (%d stale ledger rows + the unknown name)",
			got, want, len(docOnlyRowsOutsideTheModel))
	}
}
