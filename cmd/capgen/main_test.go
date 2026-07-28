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
