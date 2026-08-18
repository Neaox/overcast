package mcp

// discover_test.go — how a client finds out what this server speaks.
//
// `server/discover` is the 2026-07-28 replacement for what `initialize` used to
// tell a client, and servers "MUST implement it". It is also the era probe: a
// client that does not know whether it is talking to a handshake-based server
// or a stateless one sends this first, and the shape of the answer decides.
//
// What it is *not* is exempt from the metadata every other request carries. The
// revision marks `protocolVersion` and `clientCapabilities` required on every
// request and gives `DiscoverRequest` the same `RequestParams` as everything
// else, so a bare discover is malformed and refused — the first test below. A
// client that does not know which version to name learns it from -32022's
// `supported` list, which carries the same information discover would have.
//
// See docs/plans/mcp-2026-07-28-migration.md, phase 2.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Discover is not exempt from the per-request metadata every other method
// carries, and a bare one is refused like any other malformed request.
//
// This test used to assert the opposite — that a `_meta`-less discover is
// answered — on the reasoning that a probe cannot be expected to know a version.
// The revision does not agree: `protocolVersion` and `clientCapabilities` are
// required on every request, `DiscoverRequest` takes the same `RequestParams` as
// everything else, and no exemption is granted anywhere. The old test passed
// only because `mcpPost` put the `_meta` back on the way out (#1035), so it
// never sent the request it described. Sent raw here, deliberately.
func TestDiscover_bareRequestIsRefusedLikeAnyOther(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPostRaw(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": discoverMethod,
	}, nil)
	defer resp.Body.Close() //nolint:errcheck

	// "On HTTP, the response status MUST be 400 Bad Request." This is the half
	// that was wrong: the refusal carried the right code inside a 200, so an
	// intermediary reading status alone saw a success.
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 — a request missing a required _meta field is malformed",
			resp.StatusCode)
	}
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("a discover carrying no protocol version was served")
	}
	if code, _ := envelope.Error["code"].(float64); int(code) != RPCInvalidParams {
		t.Errorf("code = %v, want %d (InvalidParams)", envelope.Error["code"], RPCInvalidParams)
	}

	// The refusal has to say what to retry with, and say it truthfully: one
	// entry, the modern one. Listing a revision this server cannot serve invites
	// a client to send a request it will then be refused for.
	data, _ := envelope.Error["data"].(map[string]any)
	supported, _ := data["supported"].([]any)
	if len(supported) != 1 || supported[0] != ProtocolVersion {
		t.Errorf("supported = %v, want exactly [%q]", supported, ProtocolVersion)
	}
}

// The conforming case: a discover that declares itself properly gets the answer.
func TestDiscover_answersAConformingRequest(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": discoverMethod,
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
		t.Fatalf("server/discover was rejected: %v", envelope.Error)
	}

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
