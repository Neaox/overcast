package mcp

// standard_headers_test.go — the mirrored headers, and the two places the rule
// deliberately stops.
//
// A modern request over HTTP must repeat its method, and the thing it names, in
// headers an intermediary can read without parsing the body. The server's job
// is to check they agree: a header saying one thing and a body saying another
// means a load balancer and a server are handling different requests, which is
// the failure the rule exists to prevent.
//
// The two exemptions are as load-bearing as the rule. A legacy request predates
// the requirement, and stdio has no envelope to mirror into — headers are a
// property of the Streamable HTTP binding, not of the protocol.

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
)

func modernHeaders(method, name string) map[string]string {
	headers := map[string]string{
		"MCP-Protocol-Version": ModernProtocolVersion,
		"Mcp-Method":           method,
	}
	if name != "" {
		headers["Mcp-Name"] = name
	}
	return headers
}

func modernParams(extra map[string]any) map[string]any {
	params := map[string]any{"_meta": modernMeta()}
	for k, v := range extra {
		params[k] = v
	}
	return params
}

// The happy path: headers present and agreeing.
func TestStandardHeaders_acceptedWhenTheyAgree(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": modernParams(nil),
	}, modernHeaders("tools/list", ""))
	defer resp.Body.Close() //nolint:errcheck

	if _, rpcErr := decodeRPC(t, resp); rpcErr != nil {
		t.Fatalf("a request whose headers agreed with its body was rejected: %v", rpcErr)
	}
}

// A modern request that omits the method header is refused: the header is
// "REQUIRED for compliance", and an intermediary that cannot see the method
// cannot do the routing the mirroring exists for.
func TestStandardHeaders_missingMethodHeaderIsRefused(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": modernParams(nil),
		// Explicitly empty rather than absent: mcpPost fills in the mirrored
		// headers a conforming request needs, and this test's subject is what
		// happens without one.
	}, map[string]string{"MCP-Protocol-Version": ModernProtocolVersion, "Mcp-Method": ""})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_, rpcErr := decodeRPC(t, resp)
	if rpcErr == nil {
		t.Fatal("a modern request with no Mcp-Method header was served")
	}
	if code, _ := rpcErr["code"].(float64); int(code) != RPCHeaderMismatch {
		t.Errorf("code = %v, want %d", rpcErr["code"], RPCHeaderMismatch)
	}
}

// The case the rule is really about: header and body disagreeing.
func TestStandardHeaders_methodDisagreeingWithTheBodyIsRefused(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": modernParams(nil),
	}, modernHeaders("prompts/list", ""))
	defer resp.Body.Close() //nolint:errcheck

	_, rpcErr := decodeRPC(t, resp)
	if rpcErr == nil {
		t.Fatal("a request whose Mcp-Method contradicted its body was served — an " +
			"intermediary routing on that header would have sent it somewhere else")
	}
	if code, _ := rpcErr["code"].(float64); int(code) != RPCHeaderMismatch {
		t.Errorf("code = %v, want %d", rpcErr["code"], RPCHeaderMismatch)
	}
}

// Mcp-Name mirrors the thing a request names, on the three methods that name
// one, and must agree too.
func TestStandardHeaders_nameDisagreeingWithTheBodyIsRefused(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "resources/read",
		"params": modernParams(map[string]any{"uri": "file:///workspace/README.md"}),
	}, modernHeaders("resources/read", "file:///workspace/OTHER.md"))
	defer resp.Body.Close() //nolint:errcheck

	_, rpcErr := decodeRPC(t, resp)
	if rpcErr == nil {
		t.Fatal("a request whose Mcp-Name contradicted its body was served")
	}
	if code, _ := rpcErr["code"].(float64); int(code) != RPCHeaderMismatch {
		t.Errorf("code = %v, want %d", rpcErr["code"], RPCHeaderMismatch)
	}
}

// A name that cannot be sent as plain ASCII travels base64-wrapped, and the
// server "MUST decode" it before comparing — otherwise a perfectly correct
// request reads as a mismatch.
func TestStandardHeaders_base64EncodedNameIsDecodedBeforeComparing(t *testing.T) {
	const uri = "file:///workspace/世界.md"
	encoded := base64SentinelPrefix + base64.StdEncoding.EncodeToString([]byte(uri)) + base64SentinelSuffix

	if got := decodeHeaderValue(encoded); got != uri {
		t.Fatalf("decodeHeaderValue(%q) = %q, want %q", encoded, got, uri)
	}
	// A value that only looks encoded is left alone rather than turned into a
	// different error about encoding.
	if got := decodeHeaderValue(base64SentinelPrefix + "not-base64!!" + base64SentinelSuffix); got == uri {
		t.Error("an undecodable sentinel value was treated as decoded")
	}
}

// A legacy request carries none of these headers and must not be held to a rule
// its revision does not have.
func TestStandardHeaders_legacyRequestIsExempt(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}, nil)
	defer resp.Body.Close() //nolint:errcheck

	if _, rpcErr := decodeRPC(t, resp); rpcErr != nil {
		t.Fatalf("a 2025-11-25 request was refused for missing headers its revision "+
			"never defined: %v", rpcErr)
	}
}

// Stdio has no envelope to mirror into. The marker is what keeps a rule written
// for the HTTP binding from rejecting every stdio request, since ServeStdio
// pushes a synthesised request through the same handler.
func TestStandardHeaders_stdioIsExempt(t *testing.T) {
	req, err := http.NewRequestWithContext(withStdioTransport(context.Background()),
		http.MethodPost, "/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	rpcReq := jsonRPCRequest{JSONRPC: "2.0", Method: "tools/list"}
	meta := requestMeta{modern: true, protocolVersion: ModernProtocolVersion}

	if got := validateStandardHeaders(req, rpcReq, meta); got != nil {
		t.Fatalf("a stdio request was held to the HTTP binding's header rule: %v", got)
	}

	// And the same request over HTTP is not exempt — proving the marker is what
	// makes the difference, not something incidental about the request.
	plain, err := http.NewRequest(http.MethodPost, "/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if got := validateStandardHeaders(plain, rpcReq, meta); got == nil {
		t.Fatal("an HTTP request with no Mcp-Method header was accepted")
	}
}
