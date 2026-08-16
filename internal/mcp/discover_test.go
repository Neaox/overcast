package mcp

// discover_test.go — how a client finds out what this server speaks.
//
// `server/discover` is the 2026-07-28 replacement for what `initialize` used to
// tell a client, and servers "MUST implement it". It is also the era probe: a
// client that does not know whether it is talking to a handshake-based server
// or a stateless one sends this first, and the shape of the answer decides.
//
// That makes one property load-bearing above all others, and it is the first
// test below: **it must be answerable with no handshake and no `_meta`**. A
// client probing a server it knows nothing about cannot be expected to have
// completed a lifecycle it may not need, nor to declare a protocol version when
// asking which versions exist. A discover that required either would answer the
// question only for clients that already knew.
//
// See docs/plans/mcp-2026-07-28-migration.md, phase 2.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The probe case: a bare request, no handshake, no `_meta`, no headers.
func TestDiscover_answersWithNoHandshakeAndNoMeta(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
	}, nil)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var envelope struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("server/discover was rejected: %v — this is the request a client "+
			"sends when it does not yet know what the server is, so it cannot "+
			"require a handshake or a declared version", envelope.Error)
	}

	// One entry, and it is the modern one. A server that lists a revision it
	// cannot serve invites a client to send a request it will then refuse:
	// 2025-11-25 means `initialize`, a session and a GET stream, and phase 4
	// removed all three. The list is the only thing a client has to choose from,
	// so it has to be the truth about what this server answers.
	versions, _ := envelope.Result["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != ProtocolVersion {
		t.Errorf("supportedVersions = %v, want exactly [%q] — anything else offers a "+
			"client an era this server no longer implements", versions, ProtocolVersion)
	}

	if _, ok := envelope.Result["capabilities"]; !ok {
		t.Error("result has no capabilities — that is half of what initialize used to report")
	}
}

// The result is cacheable, and says so. `DiscoverResult` carries the
// `ttlMs`/`cacheScope` pair, which is how a client knows it need not re-probe a
// server it has already asked.
func TestDiscover_resultIsCacheable(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
	}, nil)
	defer resp.Body.Close() //nolint:errcheck

	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ttl, ok := envelope.Result["ttlMs"].(float64)
	if !ok || ttl <= 0 {
		t.Errorf("ttlMs = %v, want a positive duration", envelope.Result["ttlMs"])
	}
	if scope, _ := envelope.Result["cacheScope"].(string); scope == "" {
		t.Error("cacheScope is empty")
	}
}

// Every result in this revision carries a resultType, and a plain answer is
// "complete". Clients are told to treat an absent one as complete for
// backwards compatibility, but this server is answering a 2026-07-28 method and
// should say so rather than rely on that fallback.
func TestDiscover_resultTypeIsComplete(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
	}, nil)
	defer resp.Body.Close() //nolint:errcheck

	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := envelope.Result["resultType"]; got != resultTypeComplete {
		t.Errorf("resultType = %v, want %q", got, resultTypeComplete)
	}
}

// The server identifies itself in `_meta`, which is where 2026-07-28 puts what
// `initialize` used to return as `serverInfo`.
func TestDiscover_reportsServerInfoInMeta(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
	}, nil)
	defer resp.Body.Close() //nolint:errcheck

	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	meta, _ := envelope.Result["_meta"].(map[string]any)
	if meta == nil {
		t.Fatalf("result has no _meta: %v", envelope.Result)
	}
	info, _ := meta[metaServerInfo].(map[string]any)
	if info == nil {
		t.Fatalf("_meta has no %s: %v", metaServerInfo, meta)
	}
	if name, _ := info["name"].(string); name == "" {
		t.Error("serverInfo.name is empty")
	}
}

// A modern client will send `_meta` on discover like on anything else, and the
// normal validation applies — so a version this server cannot speak is still
// refused, even on the method whose job is to report versions. The client is
// not left guessing: -32022 carries the supported list, which is the same
// information discover would have given it.
func TestDiscover_stillValidatesADeclaredVersion(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	meta := metaBlock()
	meta[metaProtocolVersion] = "1900-01-01"
	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
		"params": map[string]any{"_meta": meta},
	}, nil)
	defer resp.Body.Close() //nolint:errcheck

	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("a discover declaring an unsupported version was accepted")
	}
	if code, _ := envelope.Error["code"].(float64); int(code) != RPCUnsupportedProtocolVersion {
		t.Errorf("code = %v, want %d", envelope.Error["code"], RPCUnsupportedProtocolVersion)
	}
}
