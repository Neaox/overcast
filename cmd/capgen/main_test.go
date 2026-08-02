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
