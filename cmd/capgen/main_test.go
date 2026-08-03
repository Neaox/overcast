//go:build dev

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckCapabilitiesInManifest_allowsDocOnlyAndRejectsUnknown(t *testing.T) {
	// Given: one modeled capability, one documented synthetic row, and one typo.
	caps := []CapabilityDecl{
		{Service: "secretsmanager", Operation: "ListSecrets"},
		{Service: "cognito", Operation: "ListUsers"},
		{Service: "apigateway", Operation: "CreateV2Api"},
		{Service: "cloudfront", Operation: "DeleteFieldLevelEncryption"},
		{Service: "appsync", Operation: "ExecuteGraphQL"},
		{Service: "secretsmanager", Operation: "ConsoleOnly", DocOnly: true},
		{Service: "secretsmanager", Operation: "NotAnAWSOperation"},
	}

	// When: capgen validates the declarations against the generated corpus.
	violations := checkCapabilitiesInManifest(caps)

	// Then: only the undeclared AWS operation is rejected.
	if violations != 1 {
		t.Errorf("checkCapabilitiesInManifest() = %d violations, want 1", violations)
	}
}

func TestCheckServiceKeysInManifest_requiresKeyOrAliasResolution(t *testing.T) {
	// Given: service keys that resolve directly, via an alias, and not at all.
	services := []string{"sqs", "stepfunctions", "cloudwatch-logs"}
	caps := []CapabilityDecl{
		{Service: "waf", Operation: "CreateWebACL"},
		{Service: "not-an-aws-service", Operation: "DoThing"},
	}

	// When: capgen validates every key against the generated corpus.
	violations := checkServiceKeysInManifest(services, caps)

	// Then: only the key no manifest identity resolves to is rejected.
	if violations != 1 {
		t.Errorf("checkServiceKeysInManifest() = %d violations, want 1", violations)
	}
}

func TestCheckCompatRegistryServiceKeys_requiresCapabilityServiceKeys(t *testing.T) {
	// Given: compat groups keyed by a capability service, the exempt IaC tool
	// grouping, and a service no capability table declares.
	root := t.TempDir()
	writeCompatRegistry(t, root, `{
	  "version": 1,
	  "groups": [
	    {"service": "sqs", "name": "sqs-crud", "tests": [{"name": "SendMessage"}]},
	    {"service": "cognito", "name": "cognito-pools", "tests": [{"name": "ListUsers"}]},
	    {"service": "cdk", "name": "cdk-lifecycle", "tests": [{"name": "Deploy"}]},
	    {"service": "sqsx", "name": "sqsx-typo", "tests": [{"name": "SendMessage"}]}
	  ]
	}`)
	caps := []CapabilityDecl{
		{Service: "sqs", Operation: "SendMessage"},
		{Service: "cognito", Operation: "ListUsers"},
	}

	// When: capgen validates the compat registry against capability keys.
	violations := checkCompatRegistryServiceKeys(root, caps)

	// Then: only the undeclared service key is rejected.
	if violations != 1 {
		t.Errorf("checkCompatRegistryServiceKeys() = %d violations, want 1", violations)
	}
}

func TestCheckCompatRegistryServiceKeys_absentRegistryIsNotAViolation(t *testing.T) {
	// Given: a workspace with no compat registry (capgen runs outside it too).

	// When: the check runs.
	violations := checkCompatRegistryServiceKeys(t.TempDir(), nil)

	// Then: a missing registry is silent rather than a spurious failure.
	if violations != 0 {
		t.Errorf("checkCompatRegistryServiceKeys() = %d violations, want 0", violations)
	}
}

func TestParseHandlerOps_detectsTypedOperationRegistry(t *testing.T) {
	// Given: a REST-routed service whose only operation registration is the
	// typed registry built by typedOps().
	svcDir := t.TempDir()
	writeGoFile(t, svcDir, "typed_ops.go", `package widget

import "github.com/Neaox/overcast/internal/protocol/op"

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateWidget": op.NewTyped[createWidgetRequest, createWidgetResponse]("CreateWidget", s.createWidgetTyped),
		"ListWidgets":  op.NewTypedAny[listWidgetsRequest]("ListWidgets", s.listWidgetsTyped),
	}
}
`)

	// When: capgen parses the package.
	ops, comprehensive, err := parseHandlerOps(svcDir)
	if err != nil {
		t.Fatalf("parseHandlerOps() error = %v", err)
	}

	// Then: both typed operations are detected, but the registry does not claim
	// comprehensive detection — REST-routed services register only a subset here.
	if got := opNames(ops); len(got) != 2 || got[0] != "CreateWidget" || got[1] != "ListWidgets" {
		t.Errorf("parseHandlerOps() ops = %v, want [CreateWidget ListWidgets]", got)
	}
	if comprehensive {
		t.Error("parseHandlerOps() comprehensive = true, want false for a typed-registry-only service")
	}
}

func TestParseHandlerOps_typedRegistryDoesNotWeakenHandlerFuncMap(t *testing.T) {
	// Given: a service that dispatches through map[string]http.HandlerFunc and
	// registers an extra operation only in the typed registry.
	svcDir := t.TempDir()
	writeGoFile(t, svcDir, "handler.go", `package widget

import "net/http"

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateWidget": h.CreateWidget,
		"ArchiveWidget": h.ArchiveWidget,
	}
}

func (h *Handler) ArchiveWidget(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
`)
	writeGoFile(t, svcDir, "typed_ops.go", `package widget

import "github.com/Neaox/overcast/internal/protocol/op"

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateWidget": op.NewTyped[createWidgetRequest, createWidgetResponse]("CreateWidget", h.createWidgetTyped),
		"ListWidgets":  op.NewTyped[listWidgetsRequest, listWidgetsResponse]("ListWidgets", h.listWidgetsTyped),
	}
}
`)

	// When: capgen parses the package.
	ops, comprehensive, err := parseHandlerOps(svcDir)
	if err != nil {
		t.Fatalf("parseHandlerOps() error = %v", err)
	}

	// Then: the typed-only operation joins the set and the HandlerFunc map still
	// makes detection comprehensive, so ORPHANs remain violations.
	if got := opNames(ops); len(got) != 3 {
		t.Errorf("parseHandlerOps() ops = %v, want 3 operations", got)
	}
	if !comprehensive {
		t.Error("parseHandlerOps() comprehensive = false, want true when a HandlerFunc map is present")
	}
}

func TestParseHandlerOps_typedRegistryEntryDelegatingToAStubIsAStub(t *testing.T) {
	// Given: a typed registry that wraps a raw handler method returning 501 —
	// the op.NewRaw shape services use to expose an existing HandlerFunc.
	svcDir := t.TempDir()
	writeGoFile(t, svcDir, "typed_ops.go", `package widget

import "github.com/Neaox/overcast/internal/protocol/op"

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"ArchiveWidget": op.NewRaw("ArchiveWidget", h.ArchiveWidget),
	}
}

func (h *Handler) ArchiveWidget(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
`)

	// When: capgen parses the package.
	ops, _, err := parseHandlerOps(svcDir)
	if err != nil {
		t.Fatalf("parseHandlerOps() error = %v", err)
	}

	// Then: the operation is reported as a stub, so a Supported declaration is
	// caught by the WRONG_STATUS check.
	if len(ops) != 1 {
		t.Fatalf("parseHandlerOps() ops = %v, want 1 operation", opNames(ops))
	}
	if !ops[0].IsStub {
		t.Error("parseHandlerOps() IsStub = false, want true for an operation delegating to a 501 method")
	}
}

func opNames(ops []Operation) []string {
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		names = append(names, op.Name)
	}
	return names
}

func writeGoFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCompatRegistry(t *testing.T, root, contents string) {
	t.Helper()
	dir := filepath.Join(root, "compat", "suites")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "registry.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
