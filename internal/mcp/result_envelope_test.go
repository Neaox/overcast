package mcp

// result_envelope_test.go — what 2026-07-28 requires of every result, and of
// the five that are cacheable.
//
// Two separate obligations, both easy to satisfy at one site and impossible to
// keep satisfied at fifty:
//
//   - "The `result` MUST include a `resultType` field to indicate the type of
//     the result." Every result, not just the new ones. Clients "MUST treat an
//     absent resultType as complete" when talking to older servers, but that is
//     a compatibility allowance for them, not a licence for us.
//   - `tools/list`, `prompts/list`, `resources/list`, `resources/read` and
//     `resources/templates/list` return a CacheableResult, which carries
//     `ttlMs` and `cacheScope`.
//
// The tests below drive real methods rather than the helpers, because the thing
// worth protecting is that a result written by any handler comes out stamped —
// not that a particular function stamps it.

import (
	"net/http/httptest"
	"testing"
)

// newEnvelopeServer is a server past the handshake, so these tests exercise the
// ordinary result path rather than the lifecycle.
func newEnvelopeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newTestHTTPServer(t)
	t.Cleanup(srv.Close)
	return srv
}

// resultOf drives one method through the legacy path and returns its result.
func resultOf(t *testing.T, srv *httptest.Server, method string, params map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	resp := mcpPost(t, srv, body, nil)
	defer resp.Body.Close() //nolint:errcheck

	decoded := decodeBodyMap(t, resp)
	if decoded["error"] != nil {
		t.Fatalf("%s returned error: %v", method, decoded["error"])
	}
	result, ok := decoded["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s result type = %T, want an object", method, decoded["result"])
	}
	return result
}

// Every map-shaped result carries a resultType, whichever method produced it.
func TestResultEnvelope_everyResultDeclaresItsType(t *testing.T) {
	srv := newEnvelopeServer(t)

	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{method: "tools/list"},
		{method: "prompts/list"},
		{method: "resources/list"},
		{method: "resources/templates/list"},
		{method: "resources/read", params: map[string]any{"uri": "file:///workspace/README.md"}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			result := resultOf(t, srv, tc.method, tc.params)
			if got := result["resultType"]; got != resultTypeComplete {
				t.Errorf("resultType = %v, want %q", got, resultTypeComplete)
			}
		})
	}
}

// The five cacheable results say how long they may be reused, and by whom.
func TestResultEnvelope_cacheableResultsCarryTheirDirective(t *testing.T) {
	srv := newEnvelopeServer(t)

	for _, tc := range []struct {
		method string
		params map[string]any
	}{
		{method: "tools/list"},
		{method: "prompts/list"},
		{method: "resources/list"},
		{method: "resources/templates/list"},
		{method: "resources/read", params: map[string]any{"uri": "file:///workspace/README.md"}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			result := resultOf(t, srv, tc.method, tc.params)
			ttl, ok := result["ttlMs"].(float64)
			if !ok || ttl <= 0 {
				t.Errorf("ttlMs = %v, want a positive duration", result["ttlMs"])
			}
			// Public because none of these varies by caller — the spec requires
			// a list "MUST NOT vary per-connection", and nothing here varies by
			// credential either.
			if scope := result["cacheScope"]; scope != cacheScopePublic {
				t.Errorf("cacheScope = %v, want %q", scope, cacheScopePublic)
			}
		})
	}
}

// A result that has already declared itself is left alone. Nothing returns a
// non-complete type yet — MRTR's `input_required` is the first that will — so
// this guards the stamping rule rather than a live path.
func TestResultEnvelope_doesNotOverwriteADeclaredType(t *testing.T) {
	stamped, ok := stampResult(map[string]any{"resultType": "input_required"}).(map[string]any)
	if !ok {
		t.Fatal("stampResult did not return a map")
	}
	if got := stamped["resultType"]; got != "input_required" {
		t.Errorf("resultType = %v, want the value the caller set", got)
	}
}

// Non-map results pass through untouched. These are the legacy handshake
// results, which belong to a revision that has no resultType.
func TestResultEnvelope_leavesTypedResultsAlone(t *testing.T) {
	// Compared by type rather than by value: InitializeResult carries maps, so
	// == on it panics at runtime rather than reporting inequality.
	original := InitializeResult{ProtocolVersion: ProtocolVersion}
	got := stampResult(original)
	if _, becameAMap := got.(map[string]any); becameAMap {
		t.Errorf("a typed result was rewritten into a stamped map: %v", got)
	}
	if typed, ok := got.(InitializeResult); !ok || typed.ProtocolVersion != ProtocolVersion {
		t.Errorf("a typed result did not pass through unchanged: %#v", got)
	}
}
