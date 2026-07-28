package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGenerateManifest_extractsServiceOperationsAndProtocols(t *testing.T) {
	// Given: a minimal official-style Smithy JSON AST with JSON and REST services.
	modelsDir := t.TempDir()
	writeModel(t, filepath.Join(modelsDir, "queue.json"), `{
  "smithy":"2.0",
  "shapes":{
    "example.queue#Queue_20260101":{"type":"service","version":"2026-01-01","operations":[{"target":"example.queue#CreateQueue"}],"traits":{"aws.api#service":{"sdkId":"Queue","endpointPrefix":"queue"},"aws.protocols#awsJson1_0":{},"smithy.protocols#rpcv2Cbor":{}}},
    "example.queue#CreateQueue":{"type":"operation"}
  }
}`)
	writeModel(t, filepath.Join(modelsDir, "widget.json"), `{
  "smithy":"2.0",
  "shapes":{
    "example.widget#Widget":{"type":"service","version":"2026-02-02","operations":[{"target":"example.widget#GetWidget"}],"traits":{"aws.api#service":{"sdkId":"Widget","endpointPrefix":"widget"},"aws.protocols#restJson1":{}}},
    "example.widget#GetWidget":{"type":"operation","traits":{"smithy.api#http":{"method":"GET","uri":"/widgets/{id}"}}}
  }
}`)
	writeModel(t, filepath.Join(modelsDir, "resource.json"), `{
  "smithy":"2.0",
  "shapes":{
    "example.resource#ResourceService":{"type":"service","version":"2026-03-03","resources":[{"target":"example.resource#Widget"}],"traits":{"aws.api#service":{"sdkId":"Resource Service","endpointPrefix":"resource"},"aws.protocols#restJson1":{}}},
    "example.resource#Widget":{"type":"resource","operations":[{"target":"example.resource#GetWidget"}]},
    "example.resource#GetWidget":{"type":"operation","traits":{"smithy.api#http":{"method":"GET","uri":"/widgets/{id}"}}}
  }
}`)

	// When: the generator reads the model directory.
	got, err := generateManifest(modelsDir, "test-revision")
	if err != nil {
		t.Fatal(err)
	}

	// Then: it emits deterministic operation ownership metadata without model I/O at runtime.
	for _, want := range []string{
		`SourceRevision = "test-revision"`,
		`{Service: "queue", SDKID: "Queue", APIVersion: "2026-01-01", Name: "CreateQueue", Protocol: ProtocolAWSJSON10, Protocols: ProtocolsAWSJSON10 | ProtocolsRPCV2CBOR, TargetPrefix: "Queue_20260101.", HTTPMethod: "", URI: ""}`,
		`{Service: "widget", SDKID: "Widget", APIVersion: "2026-02-02", Name: "GetWidget", Protocol: ProtocolRESTJSON, Protocols: ProtocolsRESTJSON, TargetPrefix: "", HTTPMethod: "GET", URI: "/widgets/{id}"}`,
		`{Service: "resource-service", SDKID: "Resource Service", APIVersion: "2026-03-03", Name: "GetWidget", Protocol: ProtocolRESTJSON, Protocols: ProtocolsRESTJSON, TargetPrefix: "", HTTPMethod: "GET", URI: "/widgets/{id}"}`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("generated manifest missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateManifest_rejectsEmptyModels(t *testing.T) {
	if _, err := generateManifest(t.TempDir(), "test-revision"); err == nil {
		t.Fatal("generateManifest() unexpectedly succeeded")
	}
}

func TestTargetPrefixForService_usesKnownLegacyOverride(t *testing.T) {
	protocols := []string{"AWSJSON11"}
	if got, want := targetPrefixForService("com.amazonaws.cloudtrail#CloudTrail_20131101", protocols), "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."; got != want {
		t.Errorf("targetPrefixForService() = %q, want %q", got, want)
	}
}

func TestTargetPrefixForService_ignoresNonJSONProtocols(t *testing.T) {
	if got := targetPrefixForService("example.widget#Widget", []string{"RESTJSON"}); got != "" {
		t.Errorf("targetPrefixForService() = %q, want empty", got)
	}
}

func TestModelProtocol_hasStablePrecedence(t *testing.T) {
	// Given: a service that carries more than one recognized protocol trait.
	traits := map[string]json.RawMessage{
		"aws.protocols#restJson1":  {},
		"aws.protocols#awsJson1_1": {},
	}

	// Then: the canonical protocol is selected deterministically.
	if got, want := modelProtocol(traits), "AWSJSON11"; got != want {
		t.Errorf("modelProtocol() = %q, want %q", got, want)
	}
}

func TestModelProtocol_recognizesSmithyRPCV2Protocols(t *testing.T) {
	for trait, want := range map[string]string{
		"smithy.protocols#rpcv2Cbor": "RPCV2CBOR",
		"smithy.protocols#rpcv2Json": "RPCV2JSON",
	} {
		if got := modelProtocol(map[string]json.RawMessage{trait: {}}); got != want {
			t.Errorf("modelProtocol(%q) = %q, want %q", trait, got, want)
		}
	}
}

func TestModelProtocols_preservesAdditiveTraits(t *testing.T) {
	traits := map[string]json.RawMessage{
		"aws.protocols#awsJson1_1":   {},
		"smithy.protocols#rpcv2Cbor": {},
	}

	if got, want := modelProtocols(traits), []string{"AWSJSON11", "RPCV2CBOR"}; !slices.Equal(got, want) {
		t.Errorf("modelProtocols() = %v, want %v", got, want)
	}
}

func writeModel(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
