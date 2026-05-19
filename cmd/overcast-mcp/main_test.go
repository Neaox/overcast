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

func initMsg(version string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}
}

func TestMain_ServeStdio_InitializeRoundTrip(t *testing.T) {
	srv := intmcp.NewServer(nil, nil)
	in := bytes.NewReader(mcpLine(t, initMsg(intmcp.ProtocolVersion)))
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
	if result["protocolVersion"] != intmcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", result["protocolVersion"], intmcp.ProtocolVersion)
	}
}
