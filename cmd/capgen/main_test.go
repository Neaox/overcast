//go:build dev

package main

import "testing"

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
