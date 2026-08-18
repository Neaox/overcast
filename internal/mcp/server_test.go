package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestHTTPServer(t *testing.T, providers ...ToolProvider) *httptest.Server {
	t.Helper()
	srv := NewServer(nil, providers...)
	return httptest.NewServer(srv.Handler())
}

// newTestHTTPServerPair creates a Server and its httptest.Server together so tests
// can call server methods (e.g. emitResourceUpdated) while driving it over HTTP.
func newTestHTTPServerPair(t *testing.T, providers ...ToolProvider) (*Server, *httptest.Server) {
	t.Helper()
	srv := NewServer(nil, providers...)
	return srv, httptest.NewServer(srv.Handler())
}

// mcpPost sends one JSON-RPC message, making it a conforming 2026-07-28 request
// on the way out.
//
// Every request now has to carry its own protocol version in `_meta` and mirror
// its method into Mcp-Method — there is no handshake to establish either once,
// which is exactly what removing the handshake means. Doing it here rather than
// at each of the hundred-odd call sites keeps those tests about their own
// subject: a test of prompts/list pagination should not have to restate the
// protocol to ask a question about pagination.
//
// Anything the caller set is kept. That matters for the tests whose subject *is*
// this metadata — a deliberately mismatched header or an unsupported version has
// to survive being sent.
func mcpPost(t *testing.T, srv *httptest.Server, body any, headers map[string]string) *http.Response {
	t.Helper()
	body, headers = asModernRequest(body, headers)
	return mcpPostRaw(t, srv, body, headers)
}

// mcpPostRaw sends exactly what it is given — no `_meta`, no mirrored headers,
// nothing filled in.
//
// Use it only where the absence *is* the subject. `mcpPost` makes every request
// conforming on the way out, which is right for the hundred-odd tests whose
// subject is something else, and wrong for the handful asserting what happens to
// a request that declares nothing: those got their `_meta` back from the helper
// and passed while testing the opposite of their name (#1035).
func mcpPostRaw(t *testing.T, srv *httptest.Server, body any, headers map[string]string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/: %v", err)
	}
	return resp
}

// asModernRequest fills in the protocol metadata a request needs, without
// overwriting anything already there.
func asModernRequest(body any, headers map[string]string) (any, map[string]string) {
	msg, ok := body.(map[string]any)
	if !ok || msg["id"] == nil {
		return body, headers // notifications and hand-built bodies are left alone
	}
	method, _ := msg["method"].(string)

	out := make(map[string]any, len(msg))
	for k, v := range msg {
		out[k] = v
	}
	// Only where params is a map, or absent. A test that deliberately sends
	// params of the wrong shape is testing what the server does with it, and
	// replacing them with a well-formed map would quietly delete its subject.
	params, isMap := out["params"].(map[string]any)
	if params == nil && out["params"] != nil && !isMap {
		return out, headersWith(headers, method, nil)
	}
	merged := make(map[string]any, len(params)+1)
	for k, v := range params {
		merged[k] = v
	}
	// Merged key by key, not wholesale. A test that sets `_meta` for a
	// progressToken still needs the protocol version alongside it, and a test
	// whose subject *is* the protocol version must keep the one it chose.
	meta, _ := merged["_meta"].(map[string]any)
	withMeta := metaBlock()
	for k, v := range meta {
		withMeta[k] = v
	}
	merged["_meta"] = withMeta
	out["params"] = merged

	return out, headersWith(headers, method, merged)
}

// headersWith adds the headers a modern request mirrors, leaving alone anything
// the caller set deliberately.
func headersWith(headers map[string]string, method string, params map[string]any) map[string]string {
	hdrs := make(map[string]string, len(headers)+3)
	for k, v := range headers {
		hdrs[k] = v
	}
	if _, present := hdrs["Mcp-Method"]; !present {
		hdrs["Mcp-Method"] = method
	}
	if _, present := hdrs["MCP-Protocol-Version"]; !present {
		hdrs["MCP-Protocol-Version"] = ProtocolVersion
	}
	if name, needed := mirroredName(method, params); needed {
		if _, present := hdrs["Mcp-Name"]; !present {
			hdrs["Mcp-Name"] = name
		}
	}
	return hdrs
}

// mirroredName returns the value Mcp-Name must carry for the three methods that
// name what they act on.
func mirroredName(method string, params map[string]any) (string, bool) {
	if params == nil {
		return "", false
	}
	field := ""
	switch method {
	case "tools/call", "prompts/get":
		field = "name"
	case "resources/read":
		field = "uri"
	default:
		return "", false
	}
	value, ok := params[field].(string)
	return value, ok && value != ""
}

func decodeBodyMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func TestUniquePrefixMatches_SortsDedupesAndMatchesCaseInsensitively(t *testing.T) {
	got := uniquePrefixMatches([]string{
		"beta",
		"Alpha",
		"alpha",
		"Beta",
		"Alpha",
		"",
	}, "a")
	want := []any{"Alpha", "alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniquePrefixMatches() = %#v, want %#v", got, want)
	}
}

func TestServer_HTTPAuthToken_RejectsMissingOrInvalidBearerToken(t *testing.T) {
	mcpSrv := NewServer(nil)
	mcpSrv.SetBearerAuthToken("secret-token")
	srv := httptest.NewServer(mcpSrv.Handler())
	defer srv.Close()

	baseReqBody := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}

	missingAuthResp := mcpPost(t, srv, baseReqBody, nil)
	defer missingAuthResp.Body.Close()
	if missingAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing auth status = %d, want 401", missingAuthResp.StatusCode)
	}
	if got := missingAuthResp.Header.Get("WWW-Authenticate"); !strings.Contains(strings.ToLower(got), "bearer") {
		t.Fatalf("WWW-Authenticate = %q, want bearer challenge", got)
	}

	badAuthResp := mcpPost(t, srv, baseReqBody, map[string]string{"Authorization": "Bearer wrong"})
	defer badAuthResp.Body.Close()
	if badAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad auth status = %d, want 401", badAuthResp.StatusCode)
	}

	okResp := mcpPost(t, srv, baseReqBody, map[string]string{"Authorization": "Bearer secret-token"})
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("valid auth status = %d, want 200", okResp.StatusCode)
	}
}

func TestServer_Notification_ToolsListChanged_NoBody(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/tools/list_changed",
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) > 0 {
		t.Fatalf("notification response body must be empty, got %q", string(body))
	}
}

func TestServer_Notification_ResourcesListChanged_NoBody(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/resources/list_changed",
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) > 0 {
		t.Fatalf("notification response body must be empty, got %q", string(body))
	}
}

func TestServer_Notification_ResourcesUpdated_NoBody(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/resources/updated",
		"params":  map[string]any{"uri": "oc://demo/item"},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) > 0 {
		t.Fatalf("notification response body must be empty, got %q", string(body))
	}
}

func TestServer_Notification_PromptsListChanged_NoBody(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/prompts/list_changed",
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) > 0 {
		t.Fatalf("notification response body must be empty, got %q", string(body))
	}
}

func TestServer_Notification_Progress_NoBody(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/progress",
		"params":  map[string]any{"progressToken": "p1", "progress": 0.5, "total": 1},
	}, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) > 0 {
		t.Fatalf("notification response body must be empty, got %q", string(body))
	}
}

func TestServer_Cancellation_CancelsInFlightRequestAndSuppressesResponse(t *testing.T) {
	started := make(chan struct{}, 1)
	provider := &staticProvider{
		tools: []Tool{{Name: "block", Description: "block", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	callRespCh := make(chan *http.Response, 1)
	go func() {
		callRespCh <- mcpPost(t, srv, map[string]any{
			"jsonrpc": "2.0",
			"id":      10,
			"method":  "tools/call",
			"params":  map[string]any{"name": "block", "arguments": map[string]any{}},
		}, nil)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	cancelResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params":  map[string]any{"requestId": 10, "reason": "user requested cancel"},
	}, nil)
	if cancelResp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel notification status = %d, want 204", cancelResp.StatusCode)
	}
	_ = cancelResp.Body.Close()

	select {
	case callResp := <-callRespCh:
		defer callResp.Body.Close()
		if callResp.StatusCode != http.StatusNoContent {
			t.Fatalf("cancelled call status = %d, want 204", callResp.StatusCode)
		}
		b, _ := io.ReadAll(callResp.Body)
		if len(bytes.TrimSpace(b)) != 0 {
			t.Fatalf("cancelled call body must be empty, got %q", string(b))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled call response")
	}
}

func TestServer_ProgressToken_InvalidTypeRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      20,
		"method":  "tools/list",
		"params":  map[string]any{"_meta": map[string]any{"progressToken": true}},
	}, nil)
	body := decodeBodyMap(t, resp)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != float64(RPCInvalidParams) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidParams)
	}
}

func TestServer_ProgressToken_DuplicateActiveTokenRejected(t *testing.T) {
	started := make(chan struct{}, 1)
	provider := &staticProvider{
		tools: []Tool{{Name: "block", Description: "block", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	firstRespCh := make(chan *http.Response, 1)
	go func() {
		firstRespCh <- mcpPost(t, srv, map[string]any{
			"jsonrpc": "2.0",
			"id":      30,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "block",
				"arguments": map[string]any{},
				"_meta":     map[string]any{"progressToken": "dup-token"},
			},
		}, nil)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first call to start")
	}

	dupResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      31,
		"method":  "tools/list",
		"params":  map[string]any{"_meta": map[string]any{"progressToken": "dup-token"}},
	}, nil)
	dupBody := decodeBodyMap(t, dupResp)
	dupErr := dupBody["error"].(map[string]any)
	if dupErr["code"] != float64(RPCInvalidParams) {
		t.Fatalf("error.code = %v, want %d", dupErr["code"], RPCInvalidParams)
	}

	cancelResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params":  map[string]any{"requestId": 30},
	}, nil)
	_ = cancelResp.Body.Close()

	select {
	case firstResp := <-firstRespCh:
		_ = firstResp.Body.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first call cancellation completion")
	}
}

func TestServer_ToolsList_ReturnsArrayAfterLifecycleHandshake(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)

	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("unexpected error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	tools := result["tools"]
	if tools == nil {
		t.Fatal("result.tools must not be null")
	}
	if _, ok := tools.([]any); !ok {
		t.Fatalf("result.tools type = %T, want []any", tools)
	}
}

func TestServer_ToolsList_PaginatesWithCursor(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{
			{Name: "alpha", Description: "a", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "beta", Description: "b", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{"limit": 1},
	}, nil)
	firstBody := decodeBodyMap(t, first)
	if firstBody["error"] != nil {
		t.Fatalf("unexpected first-page error: %v", firstBody["error"])
	}
	firstResult := firstBody["result"].(map[string]any)
	firstTools := firstResult["tools"].([]any)
	if len(firstTools) != 1 {
		t.Fatalf("first page tool count = %d, want 1", len(firstTools))
	}
	if firstResult["nextCursor"] != "1" {
		t.Fatalf("first page nextCursor = %v, want 1", firstResult["nextCursor"])
	}

	second := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{"cursor": "1", "limit": 1},
	}, nil)
	secondBody := decodeBodyMap(t, second)
	if secondBody["error"] != nil {
		t.Fatalf("unexpected second-page error: %v", secondBody["error"])
	}
	secondResult := secondBody["result"].(map[string]any)
	secondTools := secondResult["tools"].([]any)
	if len(secondTools) != 1 {
		t.Fatalf("second page tool count = %d, want 1", len(secondTools))
	}
	if _, ok := secondResult["nextCursor"]; ok {
		t.Fatalf("second page nextCursor = %v, want omitted", secondResult["nextCursor"])
	}
}

func TestServer_ToolsList_AutoPopulatesToolTitle(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "echo_tool", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	first := tools[0].(map[string]any)
	if first["title"] != "Echo Tool" {
		t.Fatalf("tool.title = %v, want %q", first["title"], "Echo Tool")
	}
}

func TestServer_ToolsList_AutoPopulatesReadOnlyAnnotations(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "repo_echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	first := tools[0].(map[string]any)
	annotations, ok := first["annotations"].(map[string]any)
	if !ok {
		t.Fatalf("tool.annotations type = %T", first["annotations"])
	}
	if annotations["readOnlyHint"] != true {
		t.Fatalf("tool.annotations.readOnlyHint = %v, want true", annotations["readOnlyHint"])
	}
}

func TestServer_ToolsList_AutoPopulatesExecutionMetadata(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "runtime_echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	first := tools[0].(map[string]any)
	execution, ok := first["execution"].(map[string]any)
	if !ok {
		t.Fatalf("tool.execution type = %T", first["execution"])
	}
	if execution["readOnlyHint"] != true {
		t.Fatalf("tool.execution.readOnlyHint = %v, want true", execution["readOnlyHint"])
	}
	if execution["destructiveHint"] != false {
		t.Fatalf("tool.execution.destructiveHint = %v, want false", execution["destructiveHint"])
	}
	if execution["idempotentHint"] != true {
		t.Fatalf("tool.execution.idempotentHint = %v, want true", execution["idempotentHint"])
	}
	if execution["openWorldHint"] != false {
		t.Fatalf("tool.execution.openWorldHint = %v, want false", execution["openWorldHint"])
	}
	if execution["mutationClass"] != "read" {
		t.Fatalf("tool.execution.mutationClass = %v, want read", execution["mutationClass"])
	}
	if execution["effectScope"] != "local_runtime" {
		t.Fatalf("tool.execution.effectScope = %v, want local_runtime", execution["effectScope"])
	}
	if execution["reversibility"] != "not_applicable" {
		t.Fatalf("tool.execution.reversibility = %v, want not_applicable", execution["reversibility"])
	}
}

func TestServer_ToolsList_AutoPopulatesOutputSchemaForRepoRuntimeTools(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "runtime_echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	first := tools[0].(map[string]any)
	if first["outputSchema"] != true {
		t.Fatalf("tool.outputSchema = %v, want true", first["outputSchema"])
	}
}

func TestServer_ToolsList_AutoPopulatesIconsForRepoRuntimeTools(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "repo_echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	first := tools[0].(map[string]any)
	icons, ok := first["icons"].([]any)
	if !ok || len(icons) == 0 {
		t.Fatalf("tool.icons = %T %#v, want non-empty []any", first["icons"], first["icons"])
	}
	icon, ok := icons[0].(map[string]any)
	if !ok {
		t.Fatalf("tool.icons[0] type = %T", icons[0])
	}
	if src, _ := icon["src"].(string); !strings.HasPrefix(src, "data:image/svg+xml;utf8,") {
		t.Fatalf("icon.src = %q, want svg data URL", src)
	}
	if mimeType, _ := icon["mimeType"].(string); mimeType != "image/svg+xml" {
		t.Fatalf("icon.mimeType = %q, want image/svg+xml", mimeType)
	}
}

func TestServer_ToolsList_PreservesExplicitIcons(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{
			Name:        "repo_echo",
			Description: "echo",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Icons:       []map[string]any{{"src": "https://example.test/icon.svg", "mimeType": "image/svg+xml"}},
		}},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	first := tools[0].(map[string]any)
	icons := first["icons"].([]any)
	icon := icons[0].(map[string]any)
	if icon["src"] != "https://example.test/icon.svg" {
		t.Fatalf("icon.src = %v, want explicit icon", icon["src"])
	}
}

func TestServer_ToolsList_PreservesExplicitExecutionMetadata(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{
			Name:        "runtime_echo",
			Description: "echo",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			Execution: map[string]any{
				"readOnlyHint":    false,
				"destructiveHint": true,
				"idempotentHint":  false,
				"openWorldHint":   true,
			},
		}},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	tools := result["tools"].([]any)
	first := tools[0].(map[string]any)
	execution := first["execution"].(map[string]any)
	if execution["readOnlyHint"] != false {
		t.Fatalf("tool.execution.readOnlyHint = %v, want false", execution["readOnlyHint"])
	}
	if execution["destructiveHint"] != true {
		t.Fatalf("tool.execution.destructiveHint = %v, want true", execution["destructiveHint"])
	}
	if execution["idempotentHint"] != false {
		t.Fatalf("tool.execution.idempotentHint = %v, want false", execution["idempotentHint"])
	}
	if execution["openWorldHint"] != true {
		t.Fatalf("tool.execution.openWorldHint = %v, want true", execution["openWorldHint"])
	}
	if execution["mutationClass"] != "write" {
		t.Fatalf("tool.execution.mutationClass = %v, want write", execution["mutationClass"])
	}
	if execution["effectScope"] != "external" {
		t.Fatalf("tool.execution.effectScope = %v, want external", execution["effectScope"])
	}
	if execution["reversibility"] != "destructive" {
		t.Fatalf("tool.execution.reversibility = %v, want destructive", execution["reversibility"])
	}
}

func TestServer_ToolsCall_DispatchesToHandlerAfterLifecycleHandshake(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]string{"ok": "yes"}, nil
		},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "echo", "arguments": map[string]any{}},
	}, nil)

	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("unexpected error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content = %v, want non-empty array", result["content"])
	}
	if _, ok := result["structuredContent"]; !ok {
		t.Fatalf("structuredContent missing in tools/call result: %v", result)
	}
}

func TestServer_ToolsCall_StringResultUsesPlainTextContent(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "plain text", nil
		},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "echo", "arguments": map[string]any{}},
	}, nil)

	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("unexpected error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	if first["text"] != "plain text" {
		t.Fatalf("content[0].text = %v, want %q", first["text"], "plain text")
	}
	if result["structuredContent"] != "plain text" {
		t.Fatalf("structuredContent = %v, want %q", result["structuredContent"], "plain text")
	}
}

func TestServer_ToolsCall_ExplicitToolResultPassesThrough(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "echo", Description: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return ToolResult{
				Content:           TextContent("summary"),
				StructuredContent: map[string]any{"ok": "yes"},
			}, nil
		},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "echo", "arguments": map[string]any{}},
	}, nil)

	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("unexpected error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	content := result["content"].([]any)
	first := content[0].(map[string]any)
	if first["text"] != "summary" {
		t.Fatalf("content[0].text = %v, want %q", first["text"], "summary")
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["ok"] != "yes" {
		t.Fatalf("structuredContent.ok = %v, want %q", structured["ok"], "yes")
	}
}

func TestServer_ToolsCall_HandlerErrorReturnsToolErrorResult(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "boom", Description: "boom", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, io.EOF
		},
	}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "boom", "arguments": map[string]any{}},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("expected tools/call handler failure as tool result, got rpc error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("result.isError = %v, want true", result["isError"])
	}
}

func TestServer_UnknownMethod_ReturnsMethodNotFoundAfterLifecycleHandshake(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "completions/complete",
	}, nil)

	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCMethodNotFound) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCMethodNotFound)
	}
}

func TestServer_UnsupportedOptionalMethods_ReturnMethodNotFound(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	methods := []string{
		"tasks/list",
		"tasks/get",
		"tasks/cancel",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			resp := mcpPost(t, srv, map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  method,
			}, nil)

			body := decodeBodyMap(t, resp)
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error object, got %v", body)
			}
			if errObj["code"] != float64(RPCMethodNotFound) {
				t.Fatalf("error.code = %v, want %d", errObj["code"], RPCMethodNotFound)
			}
		})
	}
}

func TestServer_UnsupportedDeferredOptionalMethods_ReturnMethodNotFound(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	methods := []string{
		"tasks/list",
		"tasks/get",
		"tasks/cancel",
		"roots/list",
		"sampling/createMessage",
		"elicitation/create",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			resp := mcpPost(t, srv, map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  method,
			}, nil)

			body := decodeBodyMap(t, resp)
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("expected error object, got %v", body)
			}
			if errObj["code"] != float64(RPCMethodNotFound) {
				t.Fatalf("error.code = %v, want %d", errObj["code"], RPCMethodNotFound)
			}
		})
	}
}

func TestServer_ResourcesMethods_DelegateToResourceProvider(t *testing.T) {
	provider := &staticResourceProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      101,
		"method":  "resources/list",
	}, nil)
	listBody := decodeBodyMap(t, listResp)
	if listBody["error"] != nil {
		t.Fatalf("resources/list returned error: %v", listBody["error"])
	}
	listResult := listBody["result"].(map[string]any)
	resources := listResult["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("unexpected resources/list payload: %#v", listResult)
	}

	templatesResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      102,
		"method":  "resources/templates/list",
	}, nil)
	templatesBody := decodeBodyMap(t, templatesResp)
	if templatesBody["error"] != nil {
		t.Fatalf("resources/templates/list returned error: %v", templatesBody["error"])
	}
	templatesResult := templatesBody["result"].(map[string]any)
	templates := templatesResult["resourceTemplates"].([]any)
	if len(templates) != 1 {
		t.Fatalf("unexpected resources/templates/list payload: %#v", templatesResult)
	}

	readResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      103,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "oc://demo/item"},
	}, nil)
	readBody := decodeBodyMap(t, readResp)
	if readBody["error"] != nil {
		t.Fatalf("resources/read returned error: %v", readBody["error"])
	}
	readResult := readBody["result"].(map[string]any)
	contents := readResult["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("unexpected resources/read payload: %#v", readResult)
	}
}

func TestServer_ResourcesList_PaginatesWithCursor(t *testing.T) {
	provider := &pagedResourceProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      120,
		"method":  "resources/list",
		"params":  map[string]any{"limit": 1},
	}, nil)
	firstBody := decodeBodyMap(t, first)
	if firstBody["error"] != nil {
		t.Fatalf("unexpected first-page error: %v", firstBody["error"])
	}
	firstResult := firstBody["result"].(map[string]any)
	firstResources := firstResult["resources"].([]any)
	if len(firstResources) != 1 {
		t.Fatalf("first page resources count = %d, want 1", len(firstResources))
	}
	if firstResult["nextCursor"] != "1" {
		t.Fatalf("first page nextCursor = %v, want 1", firstResult["nextCursor"])
	}

	second := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      121,
		"method":  "resources/list",
		"params":  map[string]any{"cursor": "1", "limit": 1},
	}, nil)
	secondBody := decodeBodyMap(t, second)
	if secondBody["error"] != nil {
		t.Fatalf("unexpected second-page error: %v", secondBody["error"])
	}
	secondResult := secondBody["result"].(map[string]any)
	secondResources := secondResult["resources"].([]any)
	if len(secondResources) != 1 {
		t.Fatalf("second page resources count = %d, want 1", len(secondResources))
	}
	if _, ok := secondResult["nextCursor"]; ok {
		t.Fatalf("second page nextCursor = %v, want omitted", secondResult["nextCursor"])
	}
}

func TestServer_ResourcesList_ReturnsInternalErrorWhenProviderFails(t *testing.T) {
	failing := &failingResourcesProvider{}
	good := &staticResourceProvider{}
	srv := newTestHTTPServer(t, failing, good)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      124,
		"method":  "resources/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCInternalError) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInternalError)
	}
}

func TestServer_ResourcesList_WithNoProvidersReturnsEmptyResources(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      127,
		"method":  "resources/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("resources/list returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	resources, ok := result["resources"].([]any)
	if !ok {
		t.Fatalf("resources/list result type = %T, want []any", result["resources"])
	}
	if len(resources) != 0 {
		t.Fatalf("resources/list count = %d, want 0", len(resources))
	}
}

func TestServer_ResourceTemplatesList_PaginatesWithCursor(t *testing.T) {
	provider := &pagedResourceProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      122,
		"method":  "resources/templates/list",
		"params":  map[string]any{"limit": 1},
	}, nil)
	firstBody := decodeBodyMap(t, first)
	if firstBody["error"] != nil {
		t.Fatalf("unexpected first-page error: %v", firstBody["error"])
	}
	firstResult := firstBody["result"].(map[string]any)
	firstTemplates := firstResult["resourceTemplates"].([]any)
	if len(firstTemplates) != 1 {
		t.Fatalf("first page template count = %d, want 1", len(firstTemplates))
	}
	if firstResult["nextCursor"] != "1" {
		t.Fatalf("first page nextCursor = %v, want 1", firstResult["nextCursor"])
	}

	second := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      123,
		"method":  "resources/templates/list",
		"params":  map[string]any{"cursor": "1", "limit": 1},
	}, nil)
	secondBody := decodeBodyMap(t, second)
	if secondBody["error"] != nil {
		t.Fatalf("unexpected second-page error: %v", secondBody["error"])
	}
	secondResult := secondBody["result"].(map[string]any)
	secondTemplates := secondResult["resourceTemplates"].([]any)
	if len(secondTemplates) != 1 {
		t.Fatalf("second page template count = %d, want 1", len(secondTemplates))
	}
	if _, ok := secondResult["nextCursor"]; ok {
		t.Fatalf("second page nextCursor = %v, want omitted", secondResult["nextCursor"])
	}
}

func TestServer_ResourceTemplatesList_WithNoProvidersReturnsEmptyTemplates(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      125,
		"method":  "resources/templates/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("resources/templates/list returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	templates, ok := result["resourceTemplates"].([]any)
	if !ok {
		t.Fatalf("resources/templates/list result type = %T, want []any", result["resourceTemplates"])
	}
	if len(templates) != 0 {
		t.Fatalf("resources/templates/list template count = %d, want 0", len(templates))
	}
}

func TestServer_ResourceTemplatesList_ReturnsInternalErrorWhenProviderFails(t *testing.T) {
	failing := &failingResourceTemplatesProvider{}
	good := &staticResourceProvider{}
	srv := newTestHTTPServer(t, failing, good)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      126,
		"method":  "resources/templates/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCInternalError) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInternalError)
	}
}

func TestServer_PromptsList_ReturnsExamplePrompt(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      200,
		"method":  "prompts/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("prompts/list returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	prompts := result["prompts"].([]any)
	if len(prompts) < 2 {
		t.Fatalf("prompt count = %d, want at least 2", len(prompts))
	}
	names := make([]string, 0, len(prompts))
	for _, item := range prompts {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	if !slices.Contains(names, "example") {
		t.Fatalf("prompt names = %#v, want example present", names)
	}
	if !slices.Contains(names, "validate_next_step") {
		t.Fatalf("prompt names = %#v, want validate_next_step present", names)
	}
}

func TestServer_PromptsList_PaginatesWithCursor(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      204,
		"method":  "prompts/list",
		"params":  map[string]any{"limit": 1},
	}, nil)
	firstBody := decodeBodyMap(t, first)
	if firstBody["error"] != nil {
		t.Fatalf("unexpected first-page error: %v", firstBody["error"])
	}
	firstResult := firstBody["result"].(map[string]any)
	firstPrompts := firstResult["prompts"].([]any)
	if len(firstPrompts) != 1 {
		t.Fatalf("first page prompt count = %d, want 1", len(firstPrompts))
	}
	if firstResult["nextCursor"] != "1" {
		t.Fatalf("first page nextCursor = %v, want 1", firstResult["nextCursor"])
	}

	second := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      205,
		"method":  "prompts/list",
		"params":  map[string]any{"cursor": "1", "limit": 1},
	}, nil)
	secondBody := decodeBodyMap(t, second)
	if secondBody["error"] != nil {
		t.Fatalf("unexpected second-page error: %v", secondBody["error"])
	}
	secondResult := secondBody["result"].(map[string]any)
	secondPrompts := secondResult["prompts"].([]any)
	if len(secondPrompts) != 1 {
		t.Fatalf("second page prompt count = %d, want 1", len(secondPrompts))
	}
	if _, ok := secondResult["nextCursor"]; ok {
		t.Fatalf("second page nextCursor = %v, want omitted", secondResult["nextCursor"])
	}
}

func TestServer_PromptsGet_ReturnsExamplePromptMessages(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      201,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "example"},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("prompts/get returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	messages := result["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	first := content[0].(map[string]any)
	if !strings.Contains(first["text"].(string), "Overcast MCP") {
		t.Fatalf("prompt text = %q, want Overcast MCP guidance", first["text"])
	}
}

func TestServer_PromptsGet_ReturnsValidateNextStepPromptMessages(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      206,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "validate_next_step"},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("prompts/get returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	messages := result["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	first := content[0].(map[string]any)
	if !strings.Contains(first["text"].(string), "validation") {
		t.Fatalf("prompt text = %q, want validation guidance", first["text"])
	}
}

func TestServer_PromptsListEntriesAreResolvableViaPromptsGet(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      214,
		"method":  "prompts/list",
	}, nil)
	listBody := decodeBodyMap(t, listResp)
	if listBody["error"] != nil {
		t.Fatalf("prompts/list returned error: %v", listBody["error"])
	}
	listResult := listBody["result"].(map[string]any)
	prompts := listResult["prompts"].([]any)
	if len(prompts) == 0 {
		t.Fatal("prompts/list returned no prompts")
	}

	for i, item := range prompts {
		prompt := item.(map[string]any)
		name := prompt["name"].(string)
		getResp := mcpPost(t, srv, map[string]any{
			"jsonrpc": "2.0",
			"id":      215 + i,
			"method":  "prompts/get",
			"params":  map[string]any{"name": name},
		}, nil)
		getBody := decodeBodyMap(t, getResp)
		if getBody["error"] != nil {
			t.Fatalf("prompts/get for %q returned error: %v", name, getBody["error"])
		}
		getResult := getBody["result"].(map[string]any)
		messages := getResult["messages"].([]any)
		if len(messages) == 0 {
			t.Fatalf("prompts/get for %q returned no messages", name)
		}
	}
}

func TestServer_CompletionComplete_PromptSuggestionsMatchPromptsListPrefix(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	const prefix = "val"
	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      216,
		"method":  "prompts/list",
	}, nil)
	listBody := decodeBodyMap(t, listResp)
	if listBody["error"] != nil {
		t.Fatalf("prompts/list returned error: %v", listBody["error"])
	}
	listResult := listBody["result"].(map[string]any)
	prompts := listResult["prompts"].([]any)
	expected := make([]any, 0, len(prompts))
	for _, item := range prompts {
		name := item.(map[string]any)["name"].(string)
		if strings.HasPrefix(name, prefix) {
			expected = append(expected, name)
		}
	}

	completionResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      217,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": prefix},
		},
	}, nil)
	completionBody := decodeBodyMap(t, completionResp)
	if completionBody["error"] != nil {
		t.Fatalf("completion/complete returned error: %v", completionBody["error"])
	}
	completionResult := completionBody["result"].(map[string]any)
	completion := completionResult["completion"].(map[string]any)
	values := completion["values"].([]any)

	if !reflect.DeepEqual(values, expected) {
		t.Fatalf("completion values = %#v, expected from prompts/list = %#v", values, expected)
	}
}

func TestServer_PromptsList_IncludesPromptProviderEntries(t *testing.T) {
	provider := &staticPromptProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      220,
		"method":  "prompts/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("prompts/list returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	prompts := result["prompts"].([]any)
	names := make([]string, 0, len(prompts))
	for _, item := range prompts {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	if !slices.Contains(names, "dynamic_prompt") {
		t.Fatalf("prompt names = %#v, want dynamic_prompt present", names)
	}
}

func TestServer_PromptsList_DedupesPromptNamesAcrossDefaultAndProviders(t *testing.T) {
	provider := &duplicatePromptProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      226,
		"method":  "prompts/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("prompts/list returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	prompts := result["prompts"].([]any)

	countByName := map[string]int{}
	for _, item := range prompts {
		name := item.(map[string]any)["name"].(string)
		countByName[name]++
	}
	if countByName["example"] != 1 {
		t.Fatalf("example count = %d, want 1; prompts=%#v", countByName["example"], prompts)
	}
}

func TestServer_PromptsGet_ResolvesPromptProviderEntry(t *testing.T) {
	provider := &staticPromptProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      221,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "dynamic_prompt"},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("prompts/get returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	messages := result["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(messages))
	}
	message := messages[0].(map[string]any)
	content := message["content"].([]any)
	first := content[0].(map[string]any)
	if !strings.Contains(first["text"].(string), "dynamic prompt provider") {
		t.Fatalf("prompt text = %q, want dynamic prompt provider guidance", first["text"])
	}
}

func TestServer_PromptsList_ReturnsInternalErrorWhenPromptProviderFails(t *testing.T) {
	failing := &failingPromptProvider{}
	srv := newTestHTTPServer(t, failing)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      223,
		"method":  "prompts/list",
	}, nil)
	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCInternalError) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInternalError)
	}
}

func TestServer_PromptsGet_ReturnsInternalErrorWhenPromptProviderFails(t *testing.T) {
	failing := &failingPromptProvider{}
	srv := newTestHTTPServer(t, failing)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      224,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "dynamic_prompt"},
	}, nil)
	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCInternalError) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInternalError)
	}
}

func TestServer_CompletionComplete_PromptSuggestionsIncludePromptProviderEntries(t *testing.T) {
	provider := &staticPromptProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      222,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "dynamic"},
		},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("completion/complete returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]any)
	if len(values) != 1 || values[0] != "dynamic_prompt" {
		t.Fatalf("completion values = %#v, want [dynamic_prompt]", values)
	}

	titleResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2221,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "title", "value": "Dynamic"},
		},
	}, nil)
	titleBody := decodeBodyMap(t, titleResp)
	if titleBody["error"] != nil {
		t.Fatalf("completion/complete title returned error: %v", titleBody["error"])
	}
	titleResult := titleBody["result"].(map[string]any)
	titleCompletion := titleResult["completion"].(map[string]any)
	titleValues := titleCompletion["values"].([]any)
	if len(titleValues) != 1 || titleValues[0] != "Dynamic Prompt" {
		t.Fatalf("title completion values = %#v, want [Dynamic Prompt]", titleValues)
	}

	titleLowerResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2223,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "title", "value": "dynamic"},
		},
	}, nil)
	titleLowerBody := decodeBodyMap(t, titleLowerResp)
	if titleLowerBody["error"] != nil {
		t.Fatalf("completion/complete title lowercase returned error: %v", titleLowerBody["error"])
	}
	titleLowerResult := titleLowerBody["result"].(map[string]any)
	titleLowerCompletion := titleLowerResult["completion"].(map[string]any)
	titleLowerValues := titleLowerCompletion["values"].([]any)
	if len(titleLowerValues) != 1 || titleLowerValues[0] != "Dynamic Prompt" {
		t.Fatalf("lowercase title completion values = %#v, want [Dynamic Prompt]", titleLowerValues)
	}

	descriptionResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2222,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "description", "value": "Prompt provided dynamically"},
		},
	}, nil)
	descriptionBody := decodeBodyMap(t, descriptionResp)
	if descriptionBody["error"] != nil {
		t.Fatalf("completion/complete description returned error: %v", descriptionBody["error"])
	}
	descriptionResult := descriptionBody["result"].(map[string]any)
	descriptionCompletion := descriptionResult["completion"].(map[string]any)
	descriptionValues := descriptionCompletion["values"].([]any)
	if len(descriptionValues) != 1 || descriptionValues[0] != "Prompt provided dynamically by a provider." {
		t.Fatalf("description completion values = %#v, want [Prompt provided dynamically by a provider.]", descriptionValues)
	}

	fieldResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2224,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "group", "value": "dynamic"},
		},
	}, nil)
	fieldBody := decodeBodyMap(t, fieldResp)
	if fieldBody["error"] != nil {
		t.Fatalf("completion/complete custom prompt field returned error: %v", fieldBody["error"])
	}
	fieldValues := fieldBody["result"].(map[string]any)["completion"].(map[string]any)["values"].([]any)
	if len(fieldValues) != 1 || fieldValues[0] != "dynamic-group" {
		t.Fatalf("custom prompt field completion values = %#v, want [dynamic-group]", fieldValues)
	}
}

func TestServer_CompletionComplete_ToleratesPromptProviderErrors(t *testing.T) {
	failing := &failingPromptProvider{}
	srv := newTestHTTPServer(t, failing)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      225,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "ex"},
		},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("completion/complete returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]any)
	if len(values) != 1 || values[0] != "example" {
		t.Fatalf("prompt values with failing provider = %#v, want [example]", values)
	}
}

func TestServer_CompletionComplete_SuggestsPromptNamesAndResourceTemplates(t *testing.T) {
	provider := &staticResourceProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	promptResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      202,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "ex"},
		},
	}, nil)
	promptBody := decodeBodyMap(t, promptResp)
	if promptBody["error"] != nil {
		t.Fatalf("completion/complete prompt returned error: %v", promptBody["error"])
	}
	promptResult := promptBody["result"].(map[string]any)
	promptCompletion := promptResult["completion"].(map[string]any)
	promptValues := promptCompletion["values"].([]any)
	if len(promptValues) != 1 || promptValues[0] != "example" {
		t.Fatalf("prompt completion values = %#v, want [example]", promptValues)
	}
	if promptCompletion["hasMore"] != false {
		t.Fatalf("prompt completion hasMore = %v, want false", promptCompletion["hasMore"])
	}

	promptDescriptionResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2021,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "description", "value": "Example baseline prompt"},
		},
	}, nil)
	promptDescriptionBody := decodeBodyMap(t, promptDescriptionResp)
	if promptDescriptionBody["error"] != nil {
		t.Fatalf("completion/complete prompt description returned error: %v", promptDescriptionBody["error"])
	}
	promptDescriptionResult := promptDescriptionBody["result"].(map[string]any)
	promptDescriptionCompletion := promptDescriptionResult["completion"].(map[string]any)
	promptDescriptionValues := promptDescriptionCompletion["values"].([]any)
	if len(promptDescriptionValues) != 1 || promptDescriptionValues[0] != "Example baseline prompt for MCP prompt discovery and completion tests." {
		t.Fatalf("prompt description completion values = %#v, want [Example baseline prompt for MCP prompt discovery and completion tests.]", promptDescriptionValues)
	}

	templateResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      203,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": "oc://demo"},
		},
	}, nil)
	templateBody := decodeBodyMap(t, templateResp)
	if templateBody["error"] != nil {
		t.Fatalf("completion/complete template returned error: %v", templateBody["error"])
	}
	templateResult := templateBody["result"].(map[string]any)
	templateCompletion := templateResult["completion"].(map[string]any)
	templateValues := templateCompletion["values"].([]any)
	if len(templateValues) != 1 || templateValues[0] != "oc://demo/{id}" {
		t.Fatalf("template completion values = %#v, want [oc://demo/{id}]", templateValues)
	}

	templateNameResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      205,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "name", "value": "Demo"},
		},
	}, nil)
	templateNameBody := decodeBodyMap(t, templateNameResp)
	if templateNameBody["error"] != nil {
		t.Fatalf("completion/complete template name returned error: %v", templateNameBody["error"])
	}
	templateNameResult := templateNameBody["result"].(map[string]any)
	templateNameCompletion := templateNameResult["completion"].(map[string]any)
	templateNameValues := templateNameCompletion["values"].([]any)
	if len(templateNameValues) != 1 || templateNameValues[0] != "Demo Template" {
		t.Fatalf("template name completion values = %#v, want [Demo Template]", templateNameValues)
	}

	templateDescriptionResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2051,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "description", "value": "Demo resource template"},
		},
	}, nil)
	templateDescriptionBody := decodeBodyMap(t, templateDescriptionResp)
	if templateDescriptionBody["error"] != nil {
		t.Fatalf("completion/complete template description returned error: %v", templateDescriptionBody["error"])
	}
	templateDescriptionResult := templateDescriptionBody["result"].(map[string]any)
	templateDescriptionCompletion := templateDescriptionResult["completion"].(map[string]any)
	templateDescriptionValues := templateDescriptionCompletion["values"].([]any)
	if len(templateDescriptionValues) != 1 || templateDescriptionValues[0] != "Demo resource template exposed by the static test provider." {
		t.Fatalf("template description completion values = %#v, want [Demo resource template exposed by the static test provider.]", templateDescriptionValues)
	}

	templateMimeTypeResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      208,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "mimeType", "value": "application/"},
		},
	}, nil)
	templateMimeTypeBody := decodeBodyMap(t, templateMimeTypeResp)
	if templateMimeTypeBody["error"] != nil {
		t.Fatalf("completion/complete template mimeType returned error: %v", templateMimeTypeBody["error"])
	}
	templateMimeTypeResult := templateMimeTypeBody["result"].(map[string]any)
	templateMimeTypeCompletion := templateMimeTypeResult["completion"].(map[string]any)
	templateMimeTypeValues := templateMimeTypeCompletion["values"].([]any)
	if len(templateMimeTypeValues) != 1 || templateMimeTypeValues[0] != "application/json" {
		t.Fatalf("template mimeType completion values = %#v, want [application/json]", templateMimeTypeValues)
	}

	resourceFieldResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2082,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resource"},
			"argument": map[string]any{"name": "category", "value": "demo"},
		},
	}, nil)
	resourceFieldBody := decodeBodyMap(t, resourceFieldResp)
	if resourceFieldBody["error"] != nil {
		t.Fatalf("completion/complete resource field returned error: %v", resourceFieldBody["error"])
	}
	resourceFieldValues := resourceFieldBody["result"].(map[string]any)["completion"].(map[string]any)["values"].([]any)
	if len(resourceFieldValues) != 1 || resourceFieldValues[0] != "demo-resource" {
		t.Fatalf("resource field completion values = %#v, want [demo-resource]", resourceFieldValues)
	}

	templateFieldResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2083,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "category", "value": "demo"},
		},
	}, nil)
	templateFieldBody := decodeBodyMap(t, templateFieldResp)
	if templateFieldBody["error"] != nil {
		t.Fatalf("completion/complete template field returned error: %v", templateFieldBody["error"])
	}
	templateFieldValues := templateFieldBody["result"].(map[string]any)["completion"].(map[string]any)["values"].([]any)
	if len(templateFieldValues) != 1 || templateFieldValues[0] != "demo-template" {
		t.Fatalf("template field completion values = %#v, want [demo-template]", templateFieldValues)
	}

	templateMimeTypeUpperResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2081,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "mimeType", "value": "APPLICATION/"},
		},
	}, nil)
	templateMimeTypeUpperBody := decodeBodyMap(t, templateMimeTypeUpperResp)
	if templateMimeTypeUpperBody["error"] != nil {
		t.Fatalf("completion/complete template mimeType uppercase returned error: %v", templateMimeTypeUpperBody["error"])
	}
	templateMimeTypeUpperResult := templateMimeTypeUpperBody["result"].(map[string]any)
	templateMimeTypeUpperCompletion := templateMimeTypeUpperResult["completion"].(map[string]any)
	templateMimeTypeUpperValues := templateMimeTypeUpperCompletion["values"].([]any)
	if len(templateMimeTypeUpperValues) != 1 || templateMimeTypeUpperValues[0] != "application/json" {
		t.Fatalf("uppercase template mimeType completion values = %#v, want [application/json]", templateMimeTypeUpperValues)
	}

	resourceResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      204,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resource"},
			"argument": map[string]any{"name": "uri", "value": "oc://demo/"},
		},
	}, nil)
	resourceBody := decodeBodyMap(t, resourceResp)
	if resourceBody["error"] != nil {
		t.Fatalf("completion/complete resource returned error: %v", resourceBody["error"])
	}
	resourceResult := resourceBody["result"].(map[string]any)
	resourceCompletion := resourceResult["completion"].(map[string]any)
	resourceValues := resourceCompletion["values"].([]any)
	if len(resourceValues) != 1 || resourceValues[0] != "oc://demo/item" {
		t.Fatalf("resource completion values = %#v, want [oc://demo/item]", resourceValues)
	}

	resourceNameResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      206,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resource"},
			"argument": map[string]any{"name": "name", "value": "Demo"},
		},
	}, nil)
	resourceNameBody := decodeBodyMap(t, resourceNameResp)
	if resourceNameBody["error"] != nil {
		t.Fatalf("completion/complete resource name returned error: %v", resourceNameBody["error"])
	}
	resourceNameResult := resourceNameBody["result"].(map[string]any)
	resourceNameCompletion := resourceNameResult["completion"].(map[string]any)
	resourceNameValues := resourceNameCompletion["values"].([]any)
	if len(resourceNameValues) != 1 || resourceNameValues[0] != "Demo Item" {
		t.Fatalf("resource name completion values = %#v, want [Demo Item]", resourceNameValues)
	}

	resourceNameLowerResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2062,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resource"},
			"argument": map[string]any{"name": "name", "value": "demo"},
		},
	}, nil)
	resourceNameLowerBody := decodeBodyMap(t, resourceNameLowerResp)
	if resourceNameLowerBody["error"] != nil {
		t.Fatalf("completion/complete resource name lowercase returned error: %v", resourceNameLowerBody["error"])
	}
	resourceNameLowerResult := resourceNameLowerBody["result"].(map[string]any)
	resourceNameLowerCompletion := resourceNameLowerResult["completion"].(map[string]any)
	resourceNameLowerValues := resourceNameLowerCompletion["values"].([]any)
	if len(resourceNameLowerValues) != 1 || resourceNameLowerValues[0] != "Demo Item" {
		t.Fatalf("lowercase resource name completion values = %#v, want [Demo Item]", resourceNameLowerValues)
	}

	resourceDescriptionResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2061,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resource"},
			"argument": map[string]any{"name": "description", "value": "Demo resource exposed"},
		},
	}, nil)
	resourceDescriptionBody := decodeBodyMap(t, resourceDescriptionResp)
	if resourceDescriptionBody["error"] != nil {
		t.Fatalf("completion/complete resource description returned error: %v", resourceDescriptionBody["error"])
	}
	resourceDescriptionResult := resourceDescriptionBody["result"].(map[string]any)
	resourceDescriptionCompletion := resourceDescriptionResult["completion"].(map[string]any)
	resourceDescriptionValues := resourceDescriptionCompletion["values"].([]any)
	if len(resourceDescriptionValues) != 1 || resourceDescriptionValues[0] != "Demo resource exposed by the static test provider." {
		t.Fatalf("resource description completion values = %#v, want [Demo resource exposed by the static test provider.]", resourceDescriptionValues)
	}

	resourceMimeTypeResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      207,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resource"},
			"argument": map[string]any{"name": "mimeType", "value": "application/"},
		},
	}, nil)
	resourceMimeTypeBody := decodeBodyMap(t, resourceMimeTypeResp)
	if resourceMimeTypeBody["error"] != nil {
		t.Fatalf("completion/complete resource mimeType returned error: %v", resourceMimeTypeBody["error"])
	}
	resourceMimeTypeResult := resourceMimeTypeBody["result"].(map[string]any)
	resourceMimeTypeCompletion := resourceMimeTypeResult["completion"].(map[string]any)
	resourceMimeTypeValues := resourceMimeTypeCompletion["values"].([]any)
	if len(resourceMimeTypeValues) != 1 || resourceMimeTypeValues[0] != "application/json" {
		t.Fatalf("resource mimeType completion values = %#v, want [application/json]", resourceMimeTypeValues)
	}
}

func TestServer_CompletionComplete_InvalidParamsRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      207,
		"method":  "completion/complete",
		"params":  []any{"not-an-object"},
	}, nil)
	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCInvalidParams) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidParams)
	}
}

func TestServer_CompletionComplete_DefaultFallbackSuggestsAllPromptNames(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      208,
		"method":  "completion/complete",
		"params": map[string]any{
			"argument": map[string]any{"name": "name", "value": ""},
		},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("completion/complete fallback returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]any)

	got := make([]string, 0, len(values))
	for _, value := range values {
		got = append(got, value.(string))
	}
	if !slices.Contains(got, "example") {
		t.Fatalf("fallback completion values = %#v, want example", got)
	}
	if !slices.Contains(got, "validate_next_step") {
		t.Fatalf("fallback completion values = %#v, want validate_next_step", got)
	}
}

func TestServer_CompletionComplete_DefaultAndPromptRefAreConsistent(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	defaultResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      209,
		"method":  "completion/complete",
		"params": map[string]any{
			"argument": map[string]any{"name": "name", "value": "val"},
		},
	}, nil)
	defaultBody := decodeBodyMap(t, defaultResp)
	if defaultBody["error"] != nil {
		t.Fatalf("completion/complete default returned error: %v", defaultBody["error"])
	}
	defaultResult := defaultBody["result"].(map[string]any)
	defaultCompletion := defaultResult["completion"].(map[string]any)
	defaultValues := defaultCompletion["values"].([]any)

	promptResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      210,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "val"},
		},
	}, nil)
	promptBody := decodeBodyMap(t, promptResp)
	if promptBody["error"] != nil {
		t.Fatalf("completion/complete prompt returned error: %v", promptBody["error"])
	}
	promptResult := promptBody["result"].(map[string]any)
	promptCompletion := promptResult["completion"].(map[string]any)
	promptValues := promptCompletion["values"].([]any)

	if !reflect.DeepEqual(defaultValues, promptValues) {
		t.Fatalf("default values = %#v, prompt values = %#v; want equal", defaultValues, promptValues)
	}
}

func TestServer_CompletionComplete_DedupesResourceTemplateSuggestions(t *testing.T) {
	providerA := &staticResourceProvider{}
	providerB := &staticResourceProvider{}
	srv := newTestHTTPServer(t, providerA, providerB)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      211,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": "oc://demo"},
		},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("completion/complete template returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]any)
	if len(values) != 1 || values[0] != "oc://demo/{id}" {
		t.Fatalf("deduped template values = %#v, want [oc://demo/{id}]", values)
	}
}

func TestServer_CompletionComplete_TemplateSuggestionsMatchResourcesTemplatesListPrefix(t *testing.T) {
	providerA := &staticResourceProvider{}
	providerB := &staticResourceProvider{}
	srv := newTestHTTPServer(t, providerA, providerB)
	defer srv.Close()

	const prefix = "oc://demo"
	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      218,
		"method":  "resources/templates/list",
	}, nil)
	listBody := decodeBodyMap(t, listResp)
	if listBody["error"] != nil {
		t.Fatalf("resources/templates/list returned error: %v", listBody["error"])
	}
	listResult := listBody["result"].(map[string]any)
	resourceTemplates := listResult["resourceTemplates"].([]any)

	expected := make([]any, 0, len(resourceTemplates))
	seen := map[string]struct{}{}
	for _, item := range resourceTemplates {
		entry := item.(map[string]any)
		uriTemplate, _ := entry["uriTemplate"].(string)
		if !strings.HasPrefix(uriTemplate, prefix) {
			continue
		}
		if _, ok := seen[uriTemplate]; ok {
			continue
		}
		seen[uriTemplate] = struct{}{}
		expected = append(expected, uriTemplate)
	}

	completionResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      219,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": prefix},
		},
	}, nil)
	completionBody := decodeBodyMap(t, completionResp)
	if completionBody["error"] != nil {
		t.Fatalf("completion/complete returned error: %v", completionBody["error"])
	}
	completionResult := completionBody["result"].(map[string]any)
	completion := completionResult["completion"].(map[string]any)
	values := completion["values"].([]any)

	if !reflect.DeepEqual(values, expected) {
		t.Fatalf("completion values = %#v, expected from resources/templates/list = %#v", values, expected)
	}
}

func TestServer_CompletionComplete_ToleratesTemplateProviderErrors(t *testing.T) {
	failing := &failingResourceTemplatesProvider{}
	good := &staticResourceProvider{}
	srv := newTestHTTPServer(t, failing, good)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      220,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": "oc://demo"},
		},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("completion/complete returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]any)
	if len(values) != 1 || values[0] != "oc://demo/{id}" {
		t.Fatalf("template values with failing provider = %#v, want [oc://demo/{id}]", values)
	}
}

func TestServer_CompletionComplete_TrimmedPrefixMatchesPromptNames(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      212,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "  ex  "},
		},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("completion/complete prompt returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]any)
	if len(values) != 1 || values[0] != "example" {
		t.Fatalf("trimmed prompt completion values = %#v, want [example]", values)
	}
}

func TestServer_CompletionComplete_TrimmedPrefixMatchesResourceTemplates(t *testing.T) {
	provider := &staticResourceProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      213,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": "  oc://demo  "},
		},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("completion/complete template returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]any)
	if len(values) != 1 || values[0] != "oc://demo/{id}" {
		t.Fatalf("trimmed template completion values = %#v, want [oc://demo/{id}]", values)
	}
}

func TestServer_ResourcesRead_ReturnsInvalidParamsWhenResourceMissing(t *testing.T) {
	provider := &staticResourceProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      104,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "oc://missing"},
	}, nil)
	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCInvalidParams) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidParams)
	}
}

func TestServer_ResourcesRead_WithNoProvidersReturnsEmptyContents(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      106,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "oc://anything"},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("resources/read returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	contents, ok := result["contents"].([]any)
	if !ok {
		t.Fatalf("resources/read result contents type = %T, want []any", result["contents"])
	}
	if len(contents) != 0 {
		t.Fatalf("resources/read contents length = %d, want 0", len(contents))
	}
}

func TestServer_ResourcesRead_ToleratesProviderReadErrorsWhenAnotherProviderMatches(t *testing.T) {
	failing := &failingResourcesProvider{}
	good := &staticResourceProvider{}
	srv := newTestHTTPServer(t, failing, good)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      105,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "oc://demo/item"},
	}, nil)
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("resources/read returned error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	contents := result["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("unexpected resources/read payload: %#v", result)
	}
}

func TestServer_StreamableHTTP_PostSSEResponseMode(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "tools/list",
		"params":  map[string]any{"_meta": metaBlock()},
	})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	req.Header.Set("Mcp-Method", "tools/list")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/ in SSE mode: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(ct), "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyText := string(bodyBytes)
	if !strings.Contains(bodyText, "data:") || !strings.Contains(bodyText, `"method"`) && !strings.Contains(bodyText, `"result"`) {
		t.Fatalf("unexpected SSE response body: %q", bodyText)
	}
}

func TestServer_StreamableHTTP_InvalidOriginRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "ping",
	})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/ with origin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// staticProvider is a minimal ToolProvider for use in tests.
type staticProvider struct {
	tools   []Tool
	handler func(context.Context, json.RawMessage) (any, error)
}

type staticResourceProvider struct{}

type staticPromptProvider struct{}

type duplicatePromptProvider struct{}

type pagedResourceProvider struct{}

type mutableRuntimeResourceProvider struct {
	resources []map[string]any
}

type nonDestructiveRuntimeResourceProvider struct {
	resources []map[string]any
}

type emitterAwareProvider struct {
	emitter        func()
	updatedEmitter func(string)
	promptEmitter  func()
}

type failingResourceTemplatesProvider struct{}

type failingResourcesProvider struct{}

type failingPromptProvider struct{}

func (p *staticResourceProvider) Tools() []Tool { return nil }

func (p *emitterAwareProvider) Tools() []Tool { return nil }

func (p *emitterAwareProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *emitterAwareProvider) SetResourceListChangedEmitter(cb func()) {
	p.emitter = cb
}

func (p *emitterAwareProvider) SetResourceUpdatedEmitter(cb func(string)) {
	p.updatedEmitter = cb
}

func (p *emitterAwareProvider) SetPromptListChangedEmitter(cb func()) {
	p.promptEmitter = cb
}

func (p *staticResourceProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *staticResourceProvider) ListResources(context.Context) ([]map[string]any, error) {
	return []map[string]any{{
		"uri":         "oc://demo/item",
		"name":        "Demo Item",
		"description": "Demo resource exposed by the static test provider.",
		"mimeType":    "application/json",
		"category":    "demo-resource",
	}}, nil
}

func (p *staticResourceProvider) ReadResource(_ context.Context, uri string) ([]map[string]any, error) {
	if uri != "oc://demo/item" {
		return nil, errors.New("not found")
	}
	return []map[string]any{{
		"uri":      uri,
		"mimeType": "application/json",
		"text":     `{"name":"demo"}`,
	}}, nil
}

func (p *staticResourceProvider) ListResourceTemplates(context.Context) ([]map[string]any, error) {
	return []map[string]any{{
		"uriTemplate": "oc://demo/{id}",
		"name":        "Demo Template",
		"description": "Demo resource template exposed by the static test provider.",
		"mimeType":    "application/json",
		"category":    "demo-template",
	}}, nil
}

func (p *staticPromptProvider) Tools() []Tool { return nil }

func (p *duplicatePromptProvider) Tools() []Tool { return nil }

func (p *staticPromptProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *duplicatePromptProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *staticPromptProvider) ListPrompts(context.Context) ([]map[string]any, error) {
	return []map[string]any{{
		"name":        "dynamic_prompt",
		"title":       "Dynamic Prompt",
		"description": "Prompt provided dynamically by a provider.",
		"group":       "dynamic-group",
	}}, nil
}

func (p *duplicatePromptProvider) ListPrompts(context.Context) ([]map[string]any, error) {
	return []map[string]any{{
		"name":        "example",
		"title":       "Duplicate Example",
		"description": "Duplicate prompt name from provider.",
	}}, nil
}

func (p *staticPromptProvider) GetPrompt(_ context.Context, name string) ([]map[string]any, bool, error) {
	if name != "dynamic_prompt" {
		return nil, false, nil
	}
	return []map[string]any{{
		"role": "user",
		"content": []map[string]any{{
			"type": "text",
			"text": "This prompt came from a dynamic prompt provider.",
		}},
	}}, true, nil
}

func (p *duplicatePromptProvider) GetPrompt(context.Context, string) ([]map[string]any, bool, error) {
	return nil, false, nil
}

func (p *failingPromptProvider) Tools() []Tool { return nil }

func (p *failingPromptProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *failingPromptProvider) ListPrompts(context.Context) ([]map[string]any, error) {
	return nil, errors.New("prompt source unavailable")
}

func (p *failingPromptProvider) GetPrompt(context.Context, string) ([]map[string]any, bool, error) {
	return nil, false, errors.New("prompt source unavailable")
}

func (p *pagedResourceProvider) Tools() []Tool { return nil }

func (p *pagedResourceProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *pagedResourceProvider) ListResources(context.Context) ([]map[string]any, error) {
	return []map[string]any{
		{
			"uri":      "oc://demo/one",
			"name":     "Demo One",
			"mimeType": "application/json",
		},
		{
			"uri":      "oc://demo/two",
			"name":     "Demo Two",
			"mimeType": "application/json",
		},
	}, nil
}

func (p *pagedResourceProvider) ReadResource(_ context.Context, uri string) ([]map[string]any, error) {
	switch uri {
	case "oc://demo/one", "oc://demo/two":
		return []map[string]any{{
			"uri":      uri,
			"mimeType": "application/json",
			"text":     `{"name":"demo"}`,
		}}, nil
	default:
		return nil, errors.New("not found")
	}
}

func (p *pagedResourceProvider) ListResourceTemplates(context.Context) ([]map[string]any, error) {
	return []map[string]any{
		{
			"uriTemplate": "oc://demo/{id}",
			"name":        "Demo Template",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "oc://paged/{id}",
			"name":        "Paged Template",
			"mimeType":    "application/json",
		},
	}, nil
}

func (p *mutableRuntimeResourceProvider) Tools() []Tool {
	return []Tool{{
		Name:        "runtime_mutate_demo",
		Description: "mutate demo runtime inventory",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execution: map[string]any{
			"readOnlyHint":    false,
			"destructiveHint": true,
			"idempotentHint":  false,
			"openWorldHint":   false,
		},
	}}
}

func (p *mutableRuntimeResourceProvider) Handler(name string) (HandlerFunc, bool) {
	if name != "runtime_mutate_demo" {
		return nil, false
	}
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		p.resources = append(p.resources, map[string]any{
			"uri":  fmt.Sprintf("oc://demo/%d", len(p.resources)+1),
			"name": fmt.Sprintf("Demo %d", len(p.resources)+1),
		})
		return map[string]any{"ok": true}, nil
	}, true
}

func (p *nonDestructiveRuntimeResourceProvider) Tools() []Tool {
	return []Tool{{
		Name:        "runtime_update_demo",
		Description: "update demo runtime inventory",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execution: map[string]any{
			"readOnlyHint":    false,
			"destructiveHint": false,
			"idempotentHint":  true,
			"openWorldHint":   false,
		},
	}}
}

func (p *nonDestructiveRuntimeResourceProvider) Handler(name string) (HandlerFunc, bool) {
	if name != "runtime_update_demo" {
		return nil, false
	}
	return func(_ context.Context, _ json.RawMessage) (any, error) {
		p.resources = append(p.resources, map[string]any{
			"uri":  fmt.Sprintf("oc://demo/%d", len(p.resources)+1),
			"name": fmt.Sprintf("Demo %d", len(p.resources)+1),
		})
		return map[string]any{"ok": true}, nil
	}, true
}

func (p *nonDestructiveRuntimeResourceProvider) ListResources(context.Context) ([]map[string]any, error) {
	return p.resources, nil
}

func (p *nonDestructiveRuntimeResourceProvider) ReadResource(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (p *nonDestructiveRuntimeResourceProvider) ListResourceTemplates(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (p *mutableRuntimeResourceProvider) ListResources(context.Context) ([]map[string]any, error) {
	return append([]map[string]any(nil), p.resources...), nil
}

func (p *mutableRuntimeResourceProvider) ReadResource(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (p *mutableRuntimeResourceProvider) ListResourceTemplates(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (p *failingResourceTemplatesProvider) Tools() []Tool { return nil }

func (p *failingResourceTemplatesProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *failingResourceTemplatesProvider) ListResources(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (p *failingResourceTemplatesProvider) ReadResource(context.Context, string) ([]map[string]any, error) {
	return nil, errors.New("not found")
}

func (p *failingResourceTemplatesProvider) ListResourceTemplates(context.Context) ([]map[string]any, error) {
	return nil, errors.New("template source unavailable")
}

func (p *failingResourcesProvider) Tools() []Tool { return nil }

func (p *failingResourcesProvider) Handler(string) (HandlerFunc, bool) { return nil, false }

func (p *failingResourcesProvider) ListResources(context.Context) ([]map[string]any, error) {
	return nil, errors.New("resource source unavailable")
}

func (p *failingResourcesProvider) ReadResource(context.Context, string) ([]map[string]any, error) {
	return nil, errors.New("resource source unavailable")
}

func (p *failingResourcesProvider) ListResourceTemplates(context.Context) ([]map[string]any, error) {
	return nil, nil
}

func (p *staticProvider) Tools() []Tool { return p.tools }

func (p *staticProvider) Handler(name string) (HandlerFunc, bool) {
	for _, tool := range p.tools {
		if tool.Name == name && p.handler != nil {
			h := p.handler
			return func(ctx context.Context, params json.RawMessage) (any, error) {
				return h(ctx, params)
			}, true
		}
	}
	return nil, false
}

// --- ServeStdio tests ---

func newStdioServer(t *testing.T, providers ...ToolProvider) *Server {
	t.Helper()
	return NewServer(nil, providers...)
}

// stdioLine renders one message as a stdio client would send it: newline
// delimited, and carrying the protocol metadata every 2026-07-28 request needs.
// Notifications are left alone — they have no id, and the rules are written for
// requests.
func stdioLine(t *testing.T, msg map[string]any) []byte {
	t.Helper()
	if msg["id"] != nil {
		params, _ := msg["params"].(map[string]any)
		merged := make(map[string]any, len(params)+1)
		for k, v := range params {
			merged[k] = v
		}
		meta, _ := merged["_meta"].(map[string]any)
		withMeta := metaBlock()
		for k, v := range meta {
			withMeta[k] = v
		}
		merged["_meta"] = withMeta
		msg["params"] = merged
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal stdio message: %v", err)
	}
	return append(b, '\n')
}

// stdioListenLine opens a `subscriptions/listen` stream over stdio, asking for
// every broadcast type.
//
// This is how a stdio client receives notifications now, and it is what these
// tests used to get for free. The loop used to copy a server-wide broadcast onto
// stdout; that broadcast is gone with the GET stream, and a client says what it
// wants instead. The stream shares the one descriptor with every response, which
// is why each notification on it carries the subscription id.
func stdioListenLine(t *testing.T) []byte {
	t.Helper()
	return stdioLine(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "stdio-listen",
		"method":  subscriptionsListenMethod,
		"params": map[string]any{
			"notifications": map[string]any{
				"toolsListChanged":     true,
				"promptsListChanged":   true,
				"resourcesListChanged": true,
			},
		},
	})
}

func TestServer_ServeStdio_ExitsGracefullyOnEOF(t *testing.T) {
	// Given: an empty stdin (no bytes)
	srv := newStdioServer(t)
	in := bytes.NewReader(nil)
	out := &strings.Builder{}

	// When/Then: exits cleanly with nil
	if err := srv.ServeStdio(context.Background(), in, out); err != nil {
		t.Fatalf("ServeStdio on EOF returned error: %v", err)
	}
}

func TestServer_ServeStdio_ExitsGracefullyOnUnexpectedEOF(t *testing.T) {
	// Given: a partial message with no terminating newline
	srv := newStdioServer(t)
	partial := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	in := bytes.NewReader(partial)
	out := &strings.Builder{}

	// When/Then: exits cleanly with nil, produces no output
	if err := srv.ServeStdio(context.Background(), in, out); err != nil {
		t.Fatalf("ServeStdio on unexpected EOF returned error: %v", err)
	}
	if out.Len() > 0 {
		t.Fatalf("expected no output on mid-frame stdin close, got: %q", out.String())
	}
}

func TestServer_ServeStdio_NotificationProducesNoOutput(t *testing.T) {
	tests := []struct {
		name   string
		method string
		params map[string]any
	}{
		{name: "initialized", method: "notifications/initialized"},
		{name: "tools-list-changed", method: "notifications/tools/list_changed"},
		{name: "resources-list-changed", method: "notifications/resources/list_changed"},
		{name: "resources-updated", method: "notifications/resources/updated", params: map[string]any{"uri": "oc://demo/item"}},
		{name: "prompts-list-changed", method: "notifications/prompts/list_changed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A notification (no ID) must not produce a response line.
			srv := newStdioServer(t)
			notif, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"method":  tt.method,
				"params":  tt.params,
			})
			in := bytes.NewReader(append(notif, '\n'))
			out := &strings.Builder{}

			if err := srv.ServeStdio(context.Background(), in, out); err != nil {
				t.Fatalf("ServeStdio returned error: %v", err)
			}
			if out.Len() > 0 {
				t.Fatalf("notification must produce no output, got: %q", out.String())
			}
		})
	}
}

func TestServer_ServeStdio_CancellationEmitsCancelledNotification(t *testing.T) {
	started := make(chan struct{}, 1)
	provider := &staticProvider{
		tools: []Tool{{Name: "block", Description: "block", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	srv := newStdioServer(t, provider)

	inReader, inWriter := io.Pipe()
	defer inWriter.Close()
	out := &strings.Builder{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, out)
	}()

	// No `subscriptions/listen` here, deliberately. `notifications/cancelled` is
	// request scoped: it flows on the response stream of the request it cancels,
	// which `ServeStdio` gives every stdio request, and `subscriptionFilter.wants`
	// keeps it off listen streams by kind. Opening one would only obscure which
	// transport carried it.
	_, _ = inWriter.Write(stdioLine(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      12,
		"method":  "tools/call",
		"params":  map[string]any{"name": "block", "arguments": map[string]any{}},
	}))

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	cancelMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params":  map[string]any{"requestId": 12, "reason": "user requested cancel"},
	})
	_, _ = inWriter.Write(append(cancelMsg, '\n'))
	_ = inWriter.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var sawNotification bool
	for _, line := range lines {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg["method"] != "notifications/cancelled" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		if params["requestId"] != float64(12) {
			t.Fatalf("requestId = %v, want 12", params["requestId"])
		}
		if params["reason"] != "user requested cancel" {
			t.Fatalf("reason = %v, want user requested cancel", params["reason"])
		}
		sawNotification = true
	}
	if !sawNotification {
		t.Fatalf("did not find notifications/cancelled output in:\n%s", out.String())
	}
}

func TestServer_ServeStdio_EmitToolsListChangedNotification(t *testing.T) {
	srv := newStdioServer(t)

	inReader, inWriter := io.Pipe()
	defer inWriter.Close()
	outReader, outWriter := io.Pipe()
	defer outReader.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, outWriter)
	}()

	inWriter.Write(stdioListenLine(t))

	lineCh := make(chan string, 16)
	errReadCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errReadCh <- scanErr
		}
	}()

	initSeen := false
	initDeadline := time.After(2 * time.Second)
	for !initSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == notificationsAcknowledged {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for the listen stream's acknowledgement on stdio")
		}
	}

	srv.emitToolsListChanged()
	defer inWriter.Close()  //nolint:errcheck
	defer outWriter.Close() //nolint:errcheck

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == "notifications/tools/list_changed" {
				return
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-deadline:
			t.Fatal("did not find notifications/tools/list_changed output on stdio")
		}
	}
}

func TestServer_ServeStdio_RegisterProvider_EmitsToolsListChangedNotification(t *testing.T) {
	srv := newStdioServer(t)

	inReader, inWriter := io.Pipe()
	defer inWriter.Close()
	outReader, outWriter := io.Pipe()
	defer outReader.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, outWriter)
	}()

	inWriter.Write(stdioListenLine(t))

	lineCh := make(chan string, 16)
	errReadCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errReadCh <- scanErr
		}
	}()

	initSeen := false
	initDeadline := time.After(2 * time.Second)
	for !initSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == notificationsAcknowledged {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for the listen stream's acknowledgement on stdio")
		}
	}

	ping, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "ping",
	})
	_, _ = inWriter.Write(append(ping, '\n'))

	pingSeen := false
	pingDeadline := time.After(2 * time.Second)
	for !pingSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["id"] == float64(2) {
				pingSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-pingDeadline:
			t.Fatal("timed out waiting for ping response on stdio")
		}
	}

	srv.registerProvider(&staticProvider{
		tools: []Tool{{
			Name:        "dynamic_stdio_tool",
			Description: "dynamic tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	})
	defer inWriter.Close()  //nolint:errcheck
	defer outWriter.Close() //nolint:errcheck

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == "notifications/tools/list_changed" {
				return
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-deadline:
			t.Fatal("did not find notifications/tools/list_changed output on stdio")
		}
	}
}

func TestServer_ServeStdio_RegisterProvider_EmitsResourcesListChangedNotification(t *testing.T) {
	srv := newStdioServer(t)

	inReader, inWriter := io.Pipe()
	defer inWriter.Close()
	outReader, outWriter := io.Pipe()
	defer outReader.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, outWriter)
	}()

	inWriter.Write(stdioListenLine(t))

	lineCh := make(chan string, 16)
	errReadCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errReadCh <- scanErr
		}
	}()

	initSeen := false
	initDeadline := time.After(2 * time.Second)
	for !initSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == notificationsAcknowledged {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for the listen stream's acknowledgement on stdio")
		}
	}

	ping, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "ping",
	})
	_, _ = inWriter.Write(append(ping, '\n'))

	pingSeen := false
	pingDeadline := time.After(2 * time.Second)
	for !pingSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["id"] == float64(2) {
				pingSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-pingDeadline:
			t.Fatal("timed out waiting for ping response on stdio")
		}
	}

	srv.registerProvider(&staticResourceProvider{})
	defer inWriter.Close()  //nolint:errcheck
	defer outWriter.Close() //nolint:errcheck

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == "notifications/resources/list_changed" {
				return
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-deadline:
			t.Fatal("did not find notifications/resources/list_changed output on stdio")
		}
	}
}

func TestServer_ServeStdio_RegisterProvider_EmitsPromptsListChangedNotification(t *testing.T) {
	srv := newStdioServer(t)

	inReader, inWriter := io.Pipe()
	defer inWriter.Close()
	outReader, outWriter := io.Pipe()
	defer outReader.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, outWriter)
	}()

	inWriter.Write(stdioListenLine(t))

	lineCh := make(chan string, 16)
	errReadCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errReadCh <- scanErr
		}
	}()

	initSeen := false
	initDeadline := time.After(2 * time.Second)
	for !initSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == notificationsAcknowledged {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for the listen stream's acknowledgement on stdio")
		}
	}

	ping, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "ping",
	})
	_, _ = inWriter.Write(append(ping, '\n'))

	pingSeen := false
	pingDeadline := time.After(2 * time.Second)
	for !pingSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["id"] == float64(2) {
				pingSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-pingDeadline:
			t.Fatal("timed out waiting for ping response on stdio")
		}
	}

	srv.registerProvider(&staticPromptProvider{})
	defer inWriter.Close()  //nolint:errcheck
	defer outWriter.Close() //nolint:errcheck

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == "notifications/prompts/list_changed" {
				return
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-deadline:
			t.Fatal("did not find notifications/prompts/list_changed output on stdio")
		}
	}
}

func TestServer_ToolsCall_RequestTimeoutReturnsToolError(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "slow-timeout", Description: "slow", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	mcpSrv, srv := newTestHTTPServerPair(t, provider)
	mcpSrv.requestTimeout = 20 * time.Millisecond
	defer srv.Close()

	start := time.Now()
	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      90,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "slow-timeout",
			"arguments": map[string]any{},
		},
	}, nil)
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("expected tool error result, got protocol error: %v", body["error"])
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", body["result"])
	}
	if result["isError"] != true {
		t.Fatalf("isError = %v, want true", result["isError"])
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent type = %T", result["structuredContent"])
	}
	errText, _ := structured["error"].(string)
	if !strings.Contains(errText, "deadline") {
		t.Fatalf("expected deadline error text, got %q", errText)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout policy not enforced; elapsed = %s", elapsed)
	}
}

func TestServer_ServeStdio_EmitResourcesListChangedNotification(t *testing.T) {
	srv := newStdioServer(t)

	inReader, inWriter := io.Pipe()
	defer inWriter.Close()
	outReader, outWriter := io.Pipe()
	defer outReader.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, outWriter)
	}()

	inWriter.Write(stdioListenLine(t))

	lineCh := make(chan string, 16)
	errReadCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errReadCh <- scanErr
		}
	}()

	initSeen := false
	initDeadline := time.After(2 * time.Second)
	for !initSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == notificationsAcknowledged {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for the listen stream's acknowledgement on stdio")
		}
	}

	srv.emitResourceListChanged()
	defer inWriter.Close()  //nolint:errcheck
	defer outWriter.Close() //nolint:errcheck

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == "notifications/resources/list_changed" {
				return
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-deadline:
			t.Fatal("did not find notifications/resources/list_changed output on stdio")
		}
	}
}

func TestServer_ServeStdio_RuntimeMutationTool_EmitsResourcesListChangedNotification(t *testing.T) {
	srv := newStdioServer(t, &mutableRuntimeResourceProvider{})

	inReader, inWriter := io.Pipe()
	defer inWriter.Close() //nolint:errcheck
	outReader, outWriter := io.Pipe()
	defer outReader.Close() //nolint:errcheck
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, outWriter)
	}()

	_, _ = inWriter.Write(stdioListenLine(t))

	lineCh := make(chan string, 16)
	errReadCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errReadCh <- scanErr
		}
	}()

	// The listen request is dispatched on a worker, so the subscription is not
	// registered the moment the line is written. Wait for its acknowledgement
	// before triggering the mutation, or the emit can beat the subscription onto
	// the stream and the notification is dropped with nobody listening.
	initSeen := false
	initDeadline := time.After(2 * time.Second)
	for !initSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == notificationsAcknowledged {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for the listen stream's acknowledgement on stdio")
		}
	}

	_, _ = inWriter.Write(stdioLine(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      62,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_mutate_demo",
			"arguments": map[string]any{},
		},
	}))

	// Read both out before closing stdin. EOF cancels the loop context, which
	// ends the listen stream, and a notification already in flight loses the race
	// against that cancellation in `serveSubscription`'s select.
	var sawCallResponse, sawNotification bool
	deadline := time.After(2 * time.Second)
	for !sawCallResponse || !sawNotification {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["id"] == float64(62) {
				sawCallResponse = true
			}
			if msg["method"] == "notifications/resources/list_changed" {
				sawNotification = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-deadline:
			t.Fatalf("timed out on stdio; saw tools/call response = %t, saw notifications/resources/list_changed = %t", sawCallResponse, sawNotification)
		}
	}

	_ = inWriter.Close()
	defer outWriter.Close() //nolint:errcheck
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}
}

func TestServer_ServeStdio_EmitPromptsListChangedNotification(t *testing.T) {
	srv := newStdioServer(t)

	inReader, inWriter := io.Pipe()
	defer inWriter.Close()
	outReader, outWriter := io.Pipe()
	defer outReader.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, outWriter)
	}()

	inWriter.Write(stdioListenLine(t))

	lineCh := make(chan string, 16)
	errReadCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errReadCh <- scanErr
		}
	}()

	initSeen := false
	initDeadline := time.After(2 * time.Second)
	for !initSeen {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == notificationsAcknowledged {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for the listen stream's acknowledgement on stdio")
		}
	}

	srv.emitPromptsListChanged()
	defer inWriter.Close()  //nolint:errcheck
	defer outWriter.Close() //nolint:errcheck

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			if msg["method"] == "notifications/prompts/list_changed" {
				return
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-deadline:
			t.Fatal("did not find notifications/prompts/list_changed output on stdio")
		}
	}
}
