package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	intmcp "github.com/Neaox/overcast/internal/mcp"
)

func mcpLine(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mcpLine marshal: %v", err)
	}
	return append(b, '\n')
}

// discoverMsg is what a 2026-07-28 client sends first.
//
// There is no first message in the lifecycle sense any more — every request is
// self-describing and independent — but `server/discover` is what a client asks
// when it does not yet know what a server is, so it is the natural round trip
// to check this binary answers.
func discoverMsg() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params": map[string]any{
			"_meta": intmcp.NewRequestMeta("test-client", "1.0"),
		},
	}
}

// Replaces TestMain_ServeStdio_InitializeRoundTrip.
//
// Its subject was the handshake, which 2026-07-28 removes. What it was worth
// keeping for is the end of the path rather than the method at the end of it:
// a line into ServeStdio produces a well-formed JSON-RPC answer out, through
// the same synthesised-request dispatch every stdio message takes.
func TestMain_ServeStdio_DiscoverRoundTrip(t *testing.T) {
	srv := intmcp.NewServer(nil, nil)
	in := bytes.NewReader(mcpLine(t, discoverMsg()))
	var out bytes.Buffer

	if err := srv.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatalf("ServeStdio returned error: %v", err)
	}

	line := bytes.TrimSpace(out.Bytes())
	if len(line) == 0 {
		t.Fatal("expected a JSON response line")
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v; raw=%q", err, string(line))
	}
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp["result"])
	}
	versions, _ := result["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != intmcp.ProtocolVersion {
		t.Fatalf("supportedVersions = %v, want [%q]", versions, intmcp.ProtocolVersion)
	}
}
