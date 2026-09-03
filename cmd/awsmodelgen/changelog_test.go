package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// operationAt builds an inventory operation with the fields the diff reads.
func operationAt(service, name, protocol, method, uri string) inventoryOperation {
	return inventoryOperation{
		Service:      service,
		ServiceShape: service + "Service",
		SDKID:        service,
		APIVersion:   "2020-01-01",
		Name:         name,
		Protocol:     protocol,
		Protocols:    []string{protocol},
		HTTPMethod:   method,
		URI:          uri,
	}
}

func inventoryOf(revision string, ops ...inventoryOperation) modelInventory {
	inventory := modelInventory{Revision: revision, Operations: ops}
	for range ops {
		inventory.Coverage.NonS3Operations++
		inventory.Coverage.ClaimableOperations++
		inventory.Coverage.RESTBindings++
	}
	return inventory
}

func TestChangelogFragment_isEmptyForAnUnchangedCorpus(t *testing.T) {
	one := inventoryOf("aaa", operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"))
	two := inventoryOf("bbb", operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"))

	// The revision moved but nothing in it did: this is the case the caller
	// waives the gate for, and a fragment here would be the reflex entry.
	if got := changelogFragment(diffInventories(one, two), "2026-08-21"); got != "" {
		t.Errorf("changelogFragment() = %q, want empty for an inert refresh", got)
	}
}

func TestChangelogFragment_recordsAddedOperationsAsAdded(t *testing.T) {
	baseline := inventoryOf("aaa", operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"))
	current := inventoryOf("bbb",
		operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"),
		operationAt("acm", "ListCertificateDomainValidations", "RESTJSON", "GET", "/validations"),
		operationAt("agent-registry", "ListRegistries", "RESTJSON", "GET", "/registries"),
	)

	got := changelogFragment(diffInventories(baseline, current), "2026-08-21")
	if !strings.HasPrefix(got, "+ [router] 2 operations newly modeled by AWS are recognised") {
		t.Errorf("changelogFragment() = %q, want it to open with the Added count", got)
	}
	if !strings.Contains(got, "`agent-registry`") {
		t.Errorf("changelogFragment() = %q, want the new service named", got)
	}
	// One new service, so the singular has to survive the count helper.
	if !strings.Contains(got, "spanning 1 service new to the corpus (`agent-registry`)") {
		t.Errorf("changelogFragment() = %q, want a singular service clause", got)
	}
}

func TestChangelogFragment_foldsRemovalsAndTraitChangesIntoOneChangedEntry(t *testing.T) {
	baseline := inventoryOf("aaa",
		operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"),
		operationAt("acm", "Retired", "RESTJSON", "GET", "/retired"),
	)
	current := inventoryOf("bbb",
		operationAt("acm", "ListCertificates", "RESTXML", "GET", "/certificates"),
	)

	got := changelogFragment(diffInventories(baseline, current), "2026-08-21")
	if strings.Contains(got, "\n- ") || strings.HasPrefix(got, "- ") {
		t.Errorf("changelogFragment() = %q, want no Removed entry: an upstream retirement is a Changed, and `-` would mark it breaking", got)
	}
	if !strings.HasPrefix(got, "~ [router] the pinned AWS API models moved to 2026-08-21.") {
		t.Errorf("changelogFragment() = %q, want a single Changed entry dated from the model date", got)
	}
	if !strings.Contains(got, "1 operation AWS retired") || !strings.Contains(got, "1 operation changed protocol traits") {
		t.Errorf("changelogFragment() = %q, want both categories named", got)
	}
	if !strings.Contains(got, "retired from its models is no longer claimed and 1 operation changed") {
		t.Errorf("changelogFragment() = %q, want the two clauses joined with 'and'", got)
	}
}

// TestChangelogFragment_explainsOnlyTheCategoriesThatMoved pins the rule that
// makes the entry trustworthy: the trailing explanation is assembled from the
// categories present, so it never describes a mechanism this refresh did not
// touch. An entry that over-explains is not merely wordy — it is inaccurate
// about a diff the reader cannot check without leaving the changelog.
func TestChangelogFragment_explainsOnlyTheCategoriesThatMoved(t *testing.T) {
	baseline := inventoryOf("aaa", operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"))
	current := inventoryOf("bbb", operationAt("acm", "ListCertificates", "RESTXML", "GET", "/certificates"))

	got := changelogFragment(diffInventories(baseline, current), "2026-08-21")
	if !strings.Contains(got, "A trait change moves the error envelope") {
		t.Errorf("changelogFragment() = %q, want the trait effect explained", got)
	}
	for _, absent := range []string{"credential-scope check", "S3 fallback", "which request shape"} {
		if strings.Contains(got, absent) {
			t.Errorf("changelogFragment() = %q, want no mention of %q: no such change is in this diff", got, absent)
		}
	}
}

func TestChangelogFragment_countsServicesRatherThanNamingManyOfThem(t *testing.T) {
	baseline := inventoryOf("aaa", operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"))
	current := modelInventory{Revision: "bbb", Operations: baseline.Operations}
	for _, name := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		current.Operations = append(current.Operations, operationAt(name, "List", "RESTJSON", "GET", "/"+name))
	}

	got := changelogFragment(diffInventories(baseline, current), "2026-08-21")
	if !strings.Contains(got, "spanning 7 services new to the corpus.") {
		t.Errorf("changelogFragment() = %q, want the services counted once past the naming cutoff", got)
	}
	if strings.Contains(got, "`seven`") {
		t.Errorf("changelogFragment() = %q, want no service list past the cutoff", got)
	}
}

func TestChangelogFragment_isDeterministic(t *testing.T) {
	baseline := inventoryOf("aaa", operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"))
	current := inventoryOf("bbb",
		operationAt("acm", "ListCertificates", "RESTXML", "GET", "/certificates"),
		operationAt("zeta", "B", "RESTJSON", "GET", "/b"),
		operationAt("alpha", "A", "RESTJSON", "GET", "/a"),
	)

	first := changelogFragment(diffInventories(baseline, current), "2026-08-21")
	for range 8 {
		if got := changelogFragment(diffInventories(baseline, current), "2026-08-21"); got != first {
			t.Fatalf("changelogFragment() is not deterministic:\n first: %q\nlater: %q", first, got)
		}
	}
	if !strings.Contains(first, "`alpha`, `zeta`") {
		t.Errorf("changelogFragment() = %q, want services in sorted order", first)
	}
}

// workingPython returns an interpreter that actually runs, or skips.
//
// Being on PATH is not enough on Windows: "python3" there is usually the
// Microsoft Store's install stub, which resolves, executes, and exits 9009
// without ever being Python. Only running it says which one this is.
func workingPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := exec.Command(path, "--version").Run(); err == nil {
			return path
		}
	}
	t.Skip("no working python on PATH; the linter runs in CI's Changelog fragments job")
	return ""
}

// TestChangelogFragment_passesTheFragmentLinter is the one that matters: a
// generated fragment that the gate rejects would fail the refresh PR it exists
// to unblock, and the linter — not this file's idea of the grammar — is the
// authority on the entry format and the unmarked-breaking-word rule.
func TestChangelogFragment_passesTheFragmentLinter(t *testing.T) {
	python := workingPython(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	baseline := inventoryOf("aaa",
		operationAt("acm", "ListCertificates", "RESTJSON", "GET", "/certificates"),
		operationAt("acm", "Retired", "RESTJSON", "GET", "/retired"),
	)
	current := inventoryOf("bbb",
		operationAt("acm", "ListCertificates", "RESTXML", "GET", "/certificates"),
		operationAt("acm", "ListCertificateDomainValidations", "RESTJSON", "GET", "/validations"),
		operationAt("agent-registry", "ListRegistries", "RESTJSON", "GET", "/registries"),
	)
	fragment := changelogFragment(diffInventories(baseline, current), "2026-08-21")
	if fragment == "" {
		t.Fatal("changelogFragment() is empty; this diff has both an Added and a Changed entry")
	}

	// Lint it where the real ones live, so the linter reads it exactly as it
	// would on the refresh PR, then take it straight back out.
	path := filepath.Join(repoRoot, ".changelog", "29991231-awsmodelgen-lint-fixture.md")
	if err := os.WriteFile(path, []byte(fragment), 0o644); err != nil {
		t.Fatalf("write fixture fragment: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	cmd := exec.Command(python, filepath.Join("scripts", "changelog.py"), "check")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scripts/changelog.py check rejected the generated fragment: %v\nfragment:\n%s\noutput:\n%s", err, fragment, out)
	}
}

// TestChangelogFragment_staysInsideTheDetailLineCap covers the case the fixture
// diff above cannot reach: every non-additive category moving at once, which is
// what a large model refresh looks like. The effects sentence is then four
// clauses long, and scripts/changelog.py caps a detail line at 200 characters —
// so the generator has to fold it rather than emit one paragraph.
func TestChangelogFragment_staysInsideTheDetailLineCap(t *testing.T) {
	// Given: the effects sentence for all four categories.
	all := joinEffects([]string{
		"an unclaimed operation goes back to the S3 fallback",
		"a trait change moves the error envelope",
		"a binding change moves which request shape reaches it",
		"a shared binding skips the credential-scope check, having no single owner",
	})

	// When: it is packed into detail lines.
	lines := packDetail(all)

	// Then: every line fits, and nothing was dropped.
	joined := ""
	for _, line := range lines {
		if len(line)+2 > detailLineMax {
			t.Errorf("detail line is %d chars (cap %d with its indent): %q", len(line)+2, detailLineMax, line)
		}
		joined += line
	}
	for _, clause := range []string{"S3 fallback", "error envelope", "request shape", "credential-scope check"} {
		if !strings.Contains(joined, clause) {
			t.Errorf("packing lost %q; the counts an entry exists to report must survive", clause)
		}
	}
}
