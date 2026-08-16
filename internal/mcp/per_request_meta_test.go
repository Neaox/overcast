package mcp

// per_request_meta_test.go — a request that carries its own protocol metadata
// needs no handshake.
//
// Revision 2026-07-28 makes MCP stateless: "There is no negotiation handshake.
// Every request carries its protocol version, and the server accepts or rejects
// each request independently." Version, client identity and client capabilities
// travel in `_meta` on every request instead of being negotiated once by
// `initialize` and remembered for the connection.
//
// This landed in phase 1 as an additive change, alongside a handshake that
// still worked for requests carrying no `_meta`. Phase 4 removed that handshake,
// so what was once "one of two ways in" is now the only one, and these tests
// changed with it: the case that used to prove the legacy path still worked now
// proves there is no longer a path for such a request to take. See
// docs/plans/mcp-2026-07-28-migration.md.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// metaBlock is the `_meta` block a 2026-07-28 client puts on every request.
func metaBlock() map[string]any {
	return map[string]any{
		metaProtocolVersion:    ProtocolVersion,
		metaClientInfo:         map[string]any{"name": "router-test", "version": "1.0"},
		metaClientCapabilities: map[string]any{},
	}
}

// postModern sends one JSON-RPC request carrying `_meta`, with no handshake
// before it and no MCP-Protocol-Version header unless one is given.
func postModern(t *testing.T, srv *httptest.Server, method string, params map[string]any, headers map[string]string) *http.Response {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if _, ok := params["_meta"]; !ok {
		params["_meta"] = metaBlock()
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// A conforming modern client mirrors its method into a header, and the
	// server requires it — see standard_headers.go. Set by default so these
	// tests model a compliant client; a caller that wants to test the header
	// rules themselves overrides it.
	req.Header.Set("Mcp-Method", method)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", method, err)
	}
	return resp
}

// decodeRPC reads a JSON-RPC envelope off a response.
func decodeRPC(t *testing.T, resp *http.Response) (result map[string]any, rpcErr map[string]any) {
	t.Helper()
	var envelope struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope.Result, envelope.Error
}

// The headline behaviour: no initialize, no notifications/initialized, and the
// request still works because it says who it is and what version it speaks.
func TestPerRequestMeta_servesARequestWithNoHandshake(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := postModern(t, srv, "tools/list", nil, nil)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	result, rpcErr := decodeRPC(t, resp)
	if rpcErr != nil {
		t.Fatalf("tools/list with per-request _meta was rejected: %v — a self-describing "+
			"request has nothing left for a lifecycle gate to check", rpcErr)
	}
	if _, ok := result["tools"]; !ok {
		t.Errorf("result has no tools key: %v", result)
	}
}

// The other half of the rule, and the half phase 4 changed the meaning of.
//
// A request carrying no `_meta` used to be a 2025-11-25 request, governed by the
// handshake; this test proved the handshake still governed it. There is no
// handshake now, so the same request has nothing to fall back to and is refused
// — which is what "modern-only" means at the wire. The revision says as much
// about the client on the other end: "legacy clients have no fall-forward
// mechanism", so there is no answer that would help one.
func TestPerRequestMeta_aRequestThatDescribesItselfIsTheOnlyKindServed(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST tools/list: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	_, rpcErr := decodeRPC(t, resp)
	if rpcErr == nil {
		t.Fatal("a request carrying no _meta was served — there is no era left for it " +
			"to belong to, so it has no version, no identity and no capabilities " +
			"the server could act on")
	}
}

// An unsupported version is refused with the error the revision defines for it,
// carrying what the server does support so the client can retry.
func TestPerRequestMeta_unsupportedVersionIsRefusedWithSupportedList(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	meta := metaBlock()
	meta[metaProtocolVersion] = "1900-01-01"
	resp := postModern(t, srv, "tools/list", map[string]any{"_meta": meta}, nil)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_, rpcErr := decodeRPC(t, resp)
	if rpcErr == nil {
		t.Fatal("an unsupported protocol version was accepted")
	}
	if code, _ := rpcErr["code"].(float64); int(code) != RPCUnsupportedProtocolVersion {
		t.Errorf("code = %v, want %d (UnsupportedProtocolVersion)", rpcErr["code"], RPCUnsupportedProtocolVersion)
	}
	data, _ := rpcErr["data"].(map[string]any)
	if data == nil {
		t.Fatalf("error carries no data: %v", rpcErr)
	}
	if data["requested"] != "1900-01-01" {
		t.Errorf("data.requested = %v, want the version that was asked for", data["requested"])
	}
	supported, _ := data["supported"].([]any)
	if len(supported) == 0 {
		t.Error("data.supported is empty — the client has nothing to retry with")
	}
}

// The version in the header and the version in the body are two statements of
// the same fact, and the revision requires them to agree.
func TestPerRequestMeta_headerMustMatchTheBody(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := postModern(t, srv, "tools/list", nil, map[string]string{
		"MCP-Protocol-Version": "2025-11-25",
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_, rpcErr := decodeRPC(t, resp)
	if rpcErr == nil {
		t.Fatal("a request whose header and _meta disagreed about the protocol version was served")
	}
	if code, _ := rpcErr["code"].(float64); int(code) != RPCHeaderMismatch {
		t.Errorf("code = %v, want %d (HeaderMismatch)", rpcErr["code"], RPCHeaderMismatch)
	}
}

// A header that agrees is fine, and is what a conforming client sends.
func TestPerRequestMeta_matchingHeaderIsAccepted(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := postModern(t, srv, "tools/list", nil, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if _, rpcErr := decodeRPC(t, resp); rpcErr != nil {
		t.Fatalf("a request whose header matched its _meta was rejected: %v", rpcErr)
	}
}

// `_meta` that names a version the server speaks but omits the capabilities the
// revision requires is malformed, and malformed params are -32602.
func TestPerRequestMeta_missingClientCapabilitiesIsInvalidParams(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	meta := metaBlock()
	delete(meta, metaClientCapabilities)
	resp := postModern(t, srv, "tools/list", map[string]any{"_meta": meta}, nil)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_, rpcErr := decodeRPC(t, resp)
	if rpcErr == nil {
		t.Fatal("a modern request without clientCapabilities was served")
	}
	if code, _ := rpcErr["code"].(float64); int(code) != RPCInvalidParams {
		t.Errorf("code = %v, want %d (InvalidParams)", rpcErr["code"], RPCInvalidParams)
	}
}
