package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateManifest_extractsServiceOperationsAndProtocols(t *testing.T) {
	// Given: a minimal official-style Smithy JSON AST with JSON and REST services.
	modelsDir := t.TempDir()
	writeModel(t, filepath.Join(modelsDir, "queue.json"), `{
  "smithy":"2.0",
  "shapes":{
    "example.queue#Queue":{"type":"service","version":"2026-01-01","operations":[{"target":"example.queue#CreateQueue"}],"traits":{"aws.api#service":{"sdkId":"Queue","endpointPrefix":"queue"},"aws.protocols#awsJson1_0":{}}},
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

	// When: the generator reads the model directory.
	got, err := generateManifest(modelsDir, "test-revision")
	if err != nil {
		t.Fatal(err)
	}

	// Then: it emits deterministic operation ownership metadata without model I/O at runtime.
	for _, want := range []string{
		`SourceRevision = "test-revision"`,
		`{Service: "queue", SDKID: "Queue", APIVersion: "2026-01-01", Name: "CreateQueue", Protocol: ProtocolAWSJSON10, TargetPrefix: "queue", HTTPMethod: "", URI: ""}`,
		`{Service: "widget", SDKID: "Widget", APIVersion: "2026-02-02", Name: "GetWidget", Protocol: ProtocolRESTJSON, TargetPrefix: "widget", HTTPMethod: "GET", URI: "/widgets/{id}"}`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("generated manifest missing %q:\n%s", want, got)
		}
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

func writeModel(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
