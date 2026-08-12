//go:build !slim

// The runtime MCP endpoint only exists in non-slim builds:
// internal/router/mcp_routes.go carries //go:build !slim, and its slim twin
// makes registerMCPRoutes a deliberate no-op. Under -tags slim there is no
// /_overcast/mcp route, so these requests correctly fall through to the S3 catch-all
// and return a 501 NotImplemented. Guarding the file rather than the assertion
// mirrors internal/router/mcp_routes_test.go, which guards its subject the
// same way.
package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/internal/mcp"
	"github.com/Neaox/overcast/tests/helpers"
)

func TestRuntimeMCPInitialize_returnsToolsCapability(t *testing.T) {
	srv := helpers.NewTestServer(t)

	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "router-test", "version": "1.0"},
		},
	})

	resp, err := http.Post(srv.URL+"/_overcast/mcp/", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var body map[string]any
	helpers.DecodeJSON(t, resp, &body)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", body["result"])
	}
	if result["protocolVersion"] != mcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", result["protocolVersion"], mcp.ProtocolVersion)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T", result["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatal("capabilities.tools must be present")
	}
}
