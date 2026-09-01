//go:build !slim

package main

// cmd_mcp_test.go — `overcast mcp`. Covers the cobra wiring this file adds
// over internal/mcp: flag defaults matching the formerly-standalone
// overcast-mcp binary, that --workspace actually reaches the repo provider,
// the stdio transport round-tripping a request through cmd.InOrStdin /
// cmd.OutOrStdout (not os.Stdin/os.Stdout, so it's testable), and the HTTP
// handler's routes. The full net.Listen+http.Serve path in runMCP is
// exercised manually (see the task's smoke-test step), not here — binding a
// real listener from a unit test buys little and risks port flakiness.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	intmcp "github.com/overcast-sh/overcast/internal/mcp"
	"github.com/overcast-sh/overcast/internal/mcp/providers"
)

func TestMCPCmd_FlagDefaults(t *testing.T) {
	cmd := newMCPCmd()

	tests := []struct {
		name string
		want string
	}{
		{"workspace", "."},
		{"listen", "127.0.0.1:7778"},
		{"stdio", "false"},
	}
	for _, tt := range tests {
		f := cmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Fatalf("flag %q not registered", tt.name)
		}
		if f.DefValue != tt.want {
			t.Errorf("flag %q default = %q, want %q", tt.name, f.DefValue, tt.want)
		}
	}
}

// discoverLine renders a `server/discover` request the way a stdio client
// would send it: one JSON-RPC line carrying the 2026-07-28 `_meta` block
// every request needs. It is the natural round trip to check a fresh server
// answers, since there is no separate handshake method any more.
func discoverLine(t *testing.T) []byte {
	t.Helper()
	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "server/discover",
		"params": map[string]any{
			"_meta": intmcp.NewRequestMeta("cmd_mcp_test", "1.0"),
		},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal discover line: %v", err)
	}
	return append(b, '\n')
}

// TestMCPCmd_Stdio_DiscoverRoundTrip runs `overcast mcp --stdio` end to end
// through the cobra command (flag parsing, server construction, ServeStdio
// wiring) via cmd.SetIn/SetOut, and checks a `server/discover` request gets
// a well-formed, error-free JSON-RPC response naming this build's protocol
// version. Closing the input (EOF) is what ends ServeStdio and lets
// cmd.Execute() return — no signal or timeout needed.
func TestMCPCmd_Stdio_DiscoverRoundTrip(t *testing.T) {
	cmd := newMCPCmd()
	cmd.SetArgs([]string{"--stdio", "--workspace", t.TempDir()})
	cmd.SetIn(bytes.NewReader(discoverLine(t)))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("overcast mcp --stdio: %v (stderr: %s)", err, errOut.String())
	}

	line := bytes.TrimSpace(out.Bytes())
	if len(line) == 0 {
		t.Fatalf("expected a JSON-RPC response line on stdout, got none (stderr: %s)", errOut.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v; raw=%q", err, string(line))
	}
	if resp["error"] != nil {
		t.Fatalf("unexpected error response: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", resp["result"])
	}
	versions, _ := result["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != intmcp.ProtocolVersion {
		t.Fatalf("supportedVersions = %v, want [%q]", versions, intmcp.ProtocolVersion)
	}
}

// TestMCPCmd_Stdio_WorkspaceFlagReachesProvider pins --workspace to actually
// select the repo the workspace tools run against: it points --workspace at
// an empty temp directory (not this repo) and checks a repo-aware tool
// (repo_service_files, which lists Overcast's own services) comes back
// empty for it — proof the flag reached providers.NewRepoProvider rather
// than the command silently defaulting to "." or the process's cwd.
func TestMCPCmd_Stdio_WorkspaceFlagReachesProvider(t *testing.T) {
	empty := t.TempDir()
	cmd := newMCPCmd()
	toolsCallLine := func() []byte {
		msg := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "repo_service_files",
				"arguments": map[string]any{"service": "s3"},
				"_meta":     intmcp.NewRequestMeta("cmd_mcp_test", "1.0"),
			},
		}
		b, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal tools/call line: %v", err)
		}
		return append(b, '\n')
	}
	cmd.SetArgs([]string{"--stdio", "--workspace", empty})
	cmd.SetIn(bytes.NewReader(toolsCallLine()))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("overcast mcp --stdio: %v (stderr: %s)", err, errOut.String())
	}

	// The important thing here isn't what repo_service_files says about an
	// empty directory (that's internal/mcp/providers' contract, tested
	// there) — it's that the response is not an error and not a "tool not
	// found", which is what a server built without a workspace at all, or
	// with the wrong one, would look like from this end. `error` carries
	// `omitempty`, so its absence on a successful response is unambiguous.
	line := bytes.TrimSpace(out.Bytes())
	if len(line) == 0 {
		t.Fatalf("expected a JSON-RPC response line on stdout, got none (stderr: %s)", errOut.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v; raw=%q", err, string(line))
	}
	if resp["error"] != nil {
		t.Fatalf("repo_service_files against an empty --workspace returned an error: %v", resp["error"])
	}
}

// TestMCPHandler_Routes checks the HTTP surface runMCP builds: the health
// check local tooling polls before dialing, and that the MCP endpoint
// itself is mounted at /mcp/ and answers a request. Built directly via
// newMCPHandler + httptest rather than through runMCP's net.Listen, so this
// needs no real port.
func TestMCPHandler_Routes(t *testing.T) {
	server := intmcp.NewServer(nil, providers.NewRepoProvider(t.TempDir()))
	srv := httptest.NewServer(newMCPHandler(server))
	defer srv.Close()

	t.Run("health", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/_overcast/health")
		if err != nil {
			t.Fatalf("GET /_overcast/health: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
	})

	t.Run("mcp endpoint mounted", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(discoverLine(t)))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// The HTTP binding requires a conforming client to mirror its
		// JSON-RPC method into this header — see internal/mcp/standard_headers.go.
		req.Header.Set("Mcp-Method", "server/discover")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /mcp/: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 200 (body: %s)", resp.StatusCode, body)
		}
	})

	t.Run("unknown path", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/nope")
		if err != nil {
			t.Fatalf("GET /nope: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

// TestMCPCmd_Long_MentionsSlimExclusion keeps the discoverability promise in
// cmd_mcp.go's doc comment honest: someone reading `overcast mcp --help` on
// a non-slim build should still be told this command is not everywhere.
func TestMCPCmd_Long_MentionsSlimExclusion(t *testing.T) {
	cmd := newMCPCmd()
	if !strings.Contains(cmd.Long, "slim") {
		t.Errorf("Long help does not mention slim-build availability:\n%s", cmd.Long)
	}
}
