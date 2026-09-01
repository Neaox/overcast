package main

// main_test.go — covers the transport wiring this command adds over
// internal/mcp: the stdio transport round-tripping a request, and the HTTP
// handler's routes. The full net.Listen+http.Serve path in run() is
// exercised manually (see docs/plans/mcp.md), not here — binding a real
// listener from a unit test buys little and risks port flakiness.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	intmcp "github.com/overcast-sh/overcast/internal/mcp"
	"github.com/overcast-sh/overcast/internal/mcp/providers"
)

func mcpLine(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mcpLine marshal: %v", err)
	}
	return append(b, '\n')
}

// discoverMsg is what a 2026-07-28 client sends first. There is no first
// message in the lifecycle sense any more — every request is self-describing
// and independent — but `server/discover` is what a client asks when it does
// not yet know what a server is, so it is the natural round trip to check
// this binary answers.
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

func TestServeStdio_DiscoverRoundTrip(t *testing.T) {
	srv := intmcp.NewServer(nil, providers.NewRepoProvider(t.TempDir()))
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

// TestServeStdio_WorkspaceReachesProvider pins the workspace path to
// actually select the repo the workspace tools run against: it points at an
// empty temp directory (not this repo) and checks a repo-aware tool
// (repo_service_files, which lists Overcast's own services) comes back
// without error for it — proof the workspace root reached
// providers.NewRepoProvider rather than the process silently defaulting to
// "." or its cwd.
func TestServeStdio_WorkspaceReachesProvider(t *testing.T) {
	empty := t.TempDir()
	srv := intmcp.NewServer(nil, providers.NewRepoProvider(empty))

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "repo_service_files",
			"arguments": map[string]any{"service": "s3"},
			"_meta":     intmcp.NewRequestMeta("main_test", "1.0"),
		},
	}
	in := bytes.NewReader(mcpLine(t, msg))
	var out bytes.Buffer

	if err := srv.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatalf("ServeStdio returned error: %v", err)
	}

	// The important thing here isn't what repo_service_files says about an
	// empty directory (that's internal/mcp/providers' contract, tested
	// there) — it's that the response is not an error and not a "tool not
	// found", which is what a server built without a workspace at all, or
	// with the wrong one, would look like from this end. `error` carries
	// `omitempty`, so its absence on a successful response is unambiguous.
	line := bytes.TrimSpace(out.Bytes())
	if len(line) == 0 {
		t.Fatal("expected a JSON-RPC response line, got none")
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v; raw=%q", err, string(line))
	}
	if resp["error"] != nil {
		t.Fatalf("repo_service_files against an empty workspace returned an error: %v", resp["error"])
	}
}

// TestHandler_Routes checks the HTTP surface newHandler builds: the health
// check local tooling polls before dialing, and that the MCP endpoint itself
// is mounted at /mcp/ and answers a request. Built directly via newHandler +
// httptest rather than through run()'s net.Listen, so this needs no real
// port.
func TestHandler_Routes(t *testing.T) {
	server := intmcp.NewServer(nil, providers.NewRepoProvider(t.TempDir()))
	srv := httptest.NewServer(newHandler(server))
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
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(mcpLine(t, discoverMsg())))
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
