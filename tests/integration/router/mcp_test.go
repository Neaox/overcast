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

// Replaces TestRuntimeMCPInitialize_returnsToolsCapability.
//
// The capabilities block it asserted on used to arrive in the `initialize`
// result; 2026-07-28 removes the handshake and `server/discover` is where a
// client reads the same thing. The subject is unchanged — a whole emulator,
// reached over HTTP at its real path, reports that it serves tools — and only
// the method that asks has moved.
func TestRuntimeMCPDiscover_returnsToolsCapability(t *testing.T) {
	srv := helpers.NewTestServer(t)

	payload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params": map[string]any{
			"_meta": mcp.NewRequestMeta("router-test", "1.0"),
		},
	})

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_overcast/mcp/", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The two headers the revision requires a request to mirror its body into.
	req.Header.Set("MCP-Protocol-Version", mcp.ProtocolVersion)
	req.Header.Set("Mcp-Method", "server/discover")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var body map[string]any
	helpers.DecodeJSON(t, resp, &body)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T (error: %v)", body["result"], body["error"])
	}
	versions, _ := result["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != mcp.ProtocolVersion {
		t.Fatalf("supportedVersions = %v, want [%q]", versions, mcp.ProtocolVersion)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T", result["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatal("capabilities.tools must be present")
	}
}
