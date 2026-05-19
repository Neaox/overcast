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
	srv := NewServer(nil, nil, providers...)
	return httptest.NewServer(srv.Handler())
}

// newTestHTTPServerPair creates a Server and its httptest.Server together so tests
// can call server methods (e.g. emitResourceUpdated) while driving it over HTTP.
func newTestHTTPServerPair(t *testing.T, providers ...ToolProvider) (*Server, *httptest.Server) {
	t.Helper()
	srv := NewServer(nil, nil, providers...)
	return srv, httptest.NewServer(srv.Handler())
}

func mcpPost(t *testing.T, srv *httptest.Server, body any, headers map[string]string) *http.Response {
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

func decodeBodyMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func mcpDelete(t *testing.T, srv *httptest.Server, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /mcp/: %v", err)
	}
	return resp
}

func requireLifecycleReady(t *testing.T, srv *httptest.Server) {
	t.Helper()

	initResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", initResp.StatusCode)
	}
	_ = decodeBodyMap(t, initResp)

	notifyResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}, nil)
	defer notifyResp.Body.Close()
	if notifyResp.StatusCode != http.StatusNoContent {
		t.Fatalf("initialized notification status = %d, want 204", notifyResp.StatusCode)
	}
}

func operationHeaders() map[string]string {
	return map[string]string{"MCP-Protocol-Version": ProtocolVersion}
}

func TestServer_Initialize_ReturnsRequiredFields(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBodyMap(t, resp)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", body["result"])
	}
	if result["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", result["protocolVersion"], ProtocolVersion)
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T", result["capabilities"])
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatal("capabilities.tools must be present")
	}
	for _, key := range []string{"resources", "prompts", "completions", "logging"} {
		if _, ok := caps[key]; !ok {
			t.Fatalf("capabilities.%s must be present", key)
		}
	}
	if _, ok := caps["tasks"]; ok {
		t.Fatal("capabilities.tasks must remain unadvertised")
	}
	info, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo type = %T", result["serverInfo"])
	}
	if info["name"] == "" || info["version"] == "" {
		t.Fatal("serverInfo.name and serverInfo.version must be non-empty")
	}
}

func TestServer_Initialize_AdvertisesListChangedCapabilities(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeBodyMap(t, resp)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", body["result"])
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T", result["capabilities"])
	}

	// Verify tools.listChanged is advertised
	toolsCap, ok := caps["tools"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities.tools type = %T", caps["tools"])
	}
	if toolsCap["listChanged"] != true {
		t.Fatalf("tools.listChanged = %v, want true", toolsCap["listChanged"])
	}

	// Verify resources.listChanged is advertised
	resourcesCap, ok := caps["resources"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities.resources type = %T", caps["resources"])
	}
	if resourcesCap["listChanged"] != true {
		t.Fatalf("resources.listChanged = %v, want true", resourcesCap["listChanged"])
	}
	if resourcesCap["subscribe"] != true {
		t.Fatalf("resources.subscribe = %v, want true", resourcesCap["subscribe"])
	}

	// Verify prompts.listChanged is advertised
	promptsCap, ok := caps["prompts"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities.prompts type = %T", caps["prompts"])
	}
	if promptsCap["listChanged"] != true {
		t.Fatalf("prompts.listChanged = %v, want true", promptsCap["listChanged"])
	}
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

func TestServer_Initialize_UnsupportedVersion_ReturnsServerVersion(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "9999-12-31",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)

	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("unexpected error: %v", body["error"])
	}
	result := body["result"].(map[string]any)
	if result["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", result["protocolVersion"], ProtocolVersion)
	}
}

func TestServer_Initialize_RequiresProtocolVersionParam(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"capabilities": map[string]any{}},
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

func TestServer_Operation_RejectsBeforeInitialize(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      7,
		"method":  "tools/list",
	}, operationHeaders())

	body := decodeBodyMap(t, resp)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != float64(RPCInvalidRequest) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidRequest)
	}
}

func TestServer_HTTPAuthToken_RejectsMissingOrInvalidBearerToken(t *testing.T) {
	mcpSrv := NewServer(nil, nil)
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

func TestServer_SetNotificationReplayLimit_TrimsAndClearsReplayBuffer(t *testing.T) {
	mcpSrv := NewServer(nil, nil)
	mcpSrv.SetNotificationReplayLimit(2)

	mcpSrv.emitNotification("notifications/tools/list_changed", map[string]any{})
	mcpSrv.emitNotification("notifications/resources/list_changed", map[string]any{})
	mcpSrv.emitNotification("notifications/prompts/list_changed", map[string]any{})

	mcpSrv.mu.RLock()
	if len(mcpSrv.notificationReplay) != 2 {
		mcpSrv.mu.RUnlock()
		t.Fatalf("replay len = %d, want 2", len(mcpSrv.notificationReplay))
	}
	firstID := mcpSrv.notificationReplay[0].id
	lastID := mcpSrv.notificationReplay[1].id
	mcpSrv.mu.RUnlock()

	if firstID != "2" || lastID != "3" {
		t.Fatalf("replay ids = [%s, %s], want [2, 3]", firstID, lastID)
	}

	mcpSrv.SetNotificationReplayLimit(0)
	mcpSrv.mu.RLock()
	defer mcpSrv.mu.RUnlock()
	if len(mcpSrv.notificationReplay) != 0 {
		t.Fatalf("replay len after disable = %d, want 0", len(mcpSrv.notificationReplay))
	}
}

func TestServer_Operation_RejectsBeforeInitializedNotification(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	initResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)
	_ = decodeBodyMap(t, initResp)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}, operationHeaders())

	body := decodeBodyMap(t, resp)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != float64(RPCInvalidRequest) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidRequest)
	}
}

func TestServer_Operation_AllowsMissingProtocolVersionHeader(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}, nil)

	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("unexpected error: %v", body["error"])
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %T", body["result"])
	}
	if _, ok := result["tools"].([]any); !ok {
		t.Fatalf("result.tools type = %T, want []any", result["tools"])
	}
}

func TestServer_Operation_RejectsUnsupportedProtocolVersionHeader(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}, map[string]string{"MCP-Protocol-Version": "2023-01-01"})

	body := decodeBodyMap(t, resp)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != float64(RPCInvalidParams) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidParams)
	}
	data, ok := errObj["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected error.data map, got %T", errObj["data"])
	}
	if data["requested"] != "2023-01-01" {
		t.Fatalf("error.data.requested = %v, want 2023-01-01", data["requested"])
	}
}

func TestServer_Ping_ReturnsEmptyResult(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "ping",
	}, nil)

	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("unexpected ping error: %v", body["error"])
	}
	if body["result"] == nil {
		t.Fatal("ping result must be present")
	}
}

func TestServer_Notification_Initialized_NoBody(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
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
	requireLifecycleReady(t, srv)

	callRespCh := make(chan *http.Response, 1)
	go func() {
		callRespCh <- mcpPost(t, srv, map[string]any{
			"jsonrpc": "2.0",
			"id":      10,
			"method":  "tools/call",
			"params":  map[string]any{"name": "block", "arguments": map[string]any{}},
		}, operationHeaders())
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

func TestServer_Cancellation_EmitsCancelledNotificationOnSSE(t *testing.T) {
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
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseResp.Body.Close()

	callRespCh := make(chan *http.Response, 1)
	go func() {
		callRespCh <- mcpPost(t, srv, map[string]any{
			"jsonrpc": "2.0",
			"id":      11,
			"method":  "tools/call",
			"params":  map[string]any{"name": "block", "arguments": map[string]any{}},
		}, operationHeaders())
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to start")
	}

	cancelResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/cancelled",
		"params":  map[string]any{"requestId": 11, "reason": "user requested cancel"},
	}, nil)
	_ = cancelResp.Body.Close()

	select {
	case callResp := <-callRespCh:
		_ = callResp.Body.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled call response")
	}

	scanner := bufio.NewScanner(sseResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}
		if msg["method"] != "notifications/cancelled" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		if params["requestId"] != float64(11) {
			t.Fatalf("requestId = %v, want 11", params["requestId"])
		}
		if params["reason"] != "user requested cancel" {
			t.Fatalf("reason = %v, want user requested cancel", params["reason"])
		}
		return
	}
	t.Fatal("did not receive notifications/cancelled on SSE")
}

func TestServer_ProgressToken_InvalidTypeRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      20,
		"method":  "tools/list",
		"params":  map[string]any{"_meta": map[string]any{"progressToken": true}},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

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
		}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())

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
	requireLifecycleReady(t, srv)

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{"limit": 1},
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "echo", "arguments": map[string]any{}},
	}, operationHeaders())

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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "echo", "arguments": map[string]any{}},
	}, operationHeaders())

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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "echo", "arguments": map[string]any{}},
	}, operationHeaders())

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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "boom", "arguments": map[string]any{}},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "completions/complete",
	}, operationHeaders())

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
	requireLifecycleReady(t, srv)

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
			}, operationHeaders())

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

func TestServer_Initialize_CapabilitySet_ExcludesOnlyTasks(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)

	body := decodeBodyMap(t, resp)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", body["result"])
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T", result["capabilities"])
	}
	var keys []string
	for k := range caps {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, required := range []string{"tools", "resources", "prompts", "completions", "logging"} {
		if !slices.Contains(keys, required) {
			t.Fatalf("capability keys = %v, missing %s", keys, required)
		}
	}
	if slices.Contains(keys, "tasks") {
		t.Fatalf("capability keys = %v, tasks must not be advertised", keys)
	}
}

func TestServer_Initialize_CapabilitySet_DoesNotAdvertiseDeferredClientFeatures(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)

	body := decodeBodyMap(t, resp)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", body["result"])
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities type = %T", result["capabilities"])
	}

	for _, denied := range []string{"tasks", "roots", "sampling", "elicitation"} {
		if _, exists := caps[denied]; exists {
			t.Fatalf("capabilities must not advertise %q before it is implemented", denied)
		}
	}
}

func TestServer_UnsupportedDeferredOptionalMethods_ReturnMethodNotFound(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

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
			}, operationHeaders())

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

func TestServer_ImplementedOptionalMethods_SucceedAfterLifecycleHandshake(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "resources/list"},
		{"jsonrpc": "2.0", "id": 2, "method": "resources/templates/list"},
		{"jsonrpc": "2.0", "id": 3, "method": "resources/read", "params": map[string]any{"uri": "memory://example"}},
		{"jsonrpc": "2.0", "id": 4, "method": "prompts/list"},
		{"jsonrpc": "2.0", "id": 5, "method": "prompts/get", "params": map[string]any{"name": "example"}},
		{"jsonrpc": "2.0", "id": 6, "method": "completion/complete"},
		{"jsonrpc": "2.0", "id": 7, "method": "logging/setLevel", "params": map[string]any{"level": "info"}},
	}

	for _, req := range requests {
		resp := mcpPost(t, srv, req, operationHeaders())
		body := decodeBodyMap(t, resp)
		if body["error"] != nil {
			t.Fatalf("method %v returned unexpected error: %v", req["method"], body["error"])
		}
	}
}

func TestServer_ResourcesMethods_DelegateToResourceProvider(t *testing.T) {
	provider := &staticResourceProvider{}
	srv := newTestHTTPServer(t, provider)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      101,
		"method":  "resources/list",
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      120,
		"method":  "resources/list",
		"params":  map[string]any{"limit": 1},
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      124,
		"method":  "resources/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      127,
		"method":  "resources/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      122,
		"method":  "resources/templates/list",
		"params":  map[string]any{"limit": 1},
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      125,
		"method":  "resources/templates/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      126,
		"method":  "resources/templates/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      200,
		"method":  "prompts/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	first := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      204,
		"method":  "prompts/list",
		"params":  map[string]any{"limit": 1},
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      201,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "example"},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      206,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "validate_next_step"},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      214,
		"method":  "prompts/list",
	}, operationHeaders())
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
		}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	const prefix = "val"
	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      216,
		"method":  "prompts/list",
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      220,
		"method":  "prompts/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      226,
		"method":  "prompts/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      221,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "dynamic_prompt"},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      223,
		"method":  "prompts/list",
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      224,
		"method":  "prompts/get",
		"params":  map[string]any{"name": "dynamic_prompt"},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      222,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "dynamic"},
		},
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      225,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "ex"},
		},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	promptResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      202,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "ex"},
		},
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      207,
		"method":  "completion/complete",
		"params":  []any{"not-an-object"},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      208,
		"method":  "completion/complete",
		"params": map[string]any{
			"argument": map[string]any{"name": "name", "value": ""},
		},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	defaultResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      209,
		"method":  "completion/complete",
		"params": map[string]any{
			"argument": map[string]any{"name": "name", "value": "val"},
		},
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      211,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": "oc://demo"},
		},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	const prefix = "oc://demo"
	listResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      218,
		"method":  "resources/templates/list",
	}, operationHeaders())
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
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      220,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": "oc://demo"},
		},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      212,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/prompt"},
			"argument": map[string]any{"name": "name", "value": "  ex  "},
		},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      213,
		"method":  "completion/complete",
		"params": map[string]any{
			"ref":      map[string]any{"type": "ref/resourceTemplate"},
			"argument": map[string]any{"name": "uri", "value": "  oc://demo  "},
		},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      104,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "oc://missing"},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      106,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "oc://anything"},
	}, operationHeaders())
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
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      105,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "oc://demo/item"},
	}, operationHeaders())
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

func TestServer_LoggingSetLevel_EmitsMessageNotificationOnSSE(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/ SSE: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	lineCh := make(chan string, 8)
	errCh := make(chan error, 1)
	go func() {
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				errCh <- readErr
				return
			}
			lineCh <- line
		}
	}()

	select {
	case line := <-lineCh:
		if !strings.HasPrefix(line, ": connected") {
			t.Fatalf("unexpected SSE prelude: %q", line)
		}
	case err := <-errCh:
		t.Fatalf("reading SSE prelude: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE prelude")
	}

	resp2 := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "debug"},
	}, operationHeaders())
	body := decodeBodyMap(t, resp2)
	if body["error"] != nil {
		t.Fatalf("logging/setLevel error: %v", body["error"])
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			var msg map[string]any
			if err := json.Unmarshal([]byte(payload), &msg); err != nil {
				t.Fatalf("invalid SSE notification payload: %v; raw=%q", err, payload)
			}
			if msg["method"] != "notifications/message" {
				continue
			}
			params := msg["params"].(map[string]any)
			if params["level"] != "debug" {
				t.Fatalf("notification level = %v, want debug", params["level"])
			}
			if !strings.Contains(params["data"].(string), "logging level set to debug") {
				t.Fatalf("notification data = %v, want logging level message", params["data"])
			}
			if params["logger"] != "overcast" {
				t.Fatalf("notification logger = %v, want overcast", params["logger"])
			}
			return
		case err := <-errCh:
			t.Fatalf("reading SSE notification: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for notifications/message on SSE stream")
		}
	}
}

// TestServer_LoggingNotification_IncludesLoggerField verifies that every
// notifications/message payload includes the "logger" field identifying the
// emitting component, as required for full RFC 5424 / MCP-spec conformance.
func TestServer_LoggingNotification_IncludesLoggerField(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/ SSE: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	lineCh := make(chan string, 8)
	errCh := make(chan error, 1)
	go func() {
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				errCh <- readErr
				return
			}
			lineCh <- line
		}
	}()

	// drain the prelude
	select {
	case <-lineCh:
	case err := <-errCh:
		t.Fatalf("SSE prelude: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE prelude")
	}

	resp2 := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      301,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "notice"},
	}, operationHeaders())
	if body := decodeBodyMap(t, resp2); body["error"] != nil {
		t.Fatalf("logging/setLevel error: %v", body["error"])
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case line := <-lineCh:
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			var msg map[string]any
			if err := json.Unmarshal([]byte(payload), &msg); err != nil {
				t.Fatalf("invalid SSE payload: %v", err)
			}
			if msg["method"] != "notifications/message" {
				continue
			}
			params, ok := msg["params"].(map[string]any)
			if !ok {
				t.Fatalf("params is not an object: %T", msg["params"])
			}
			// level must be one of the 8 RFC 5424 names (normalized, no aliases)
			level, _ := params["level"].(string)
			if _, validErr := normalizeLoggingLevel(level); validErr != nil {
				t.Fatalf("notifications/message level %q is not a valid RFC 5424 level name", level)
			}
			// logger must identify the emitting component
			logger, _ := params["logger"].(string)
			if logger == "" {
				t.Fatal("notifications/message missing logger field")
			}
			// data must be present
			if params["data"] == nil {
				t.Fatal("notifications/message missing data field")
			}
			return
		case err := <-errCh:
			t.Fatalf("reading SSE notification: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for notifications/message")
		}
	}
}

func TestServer_LoggingSetLevel_InvalidLevelRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      207,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "verbose"},
	}, operationHeaders())
	body := decodeBodyMap(t, resp)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", body)
	}
	if errObj["code"] != float64(RPCInvalidParams) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidParams)
	}
	if errObj["message"] != "invalid logging level" {
		t.Fatalf("error.message = %v, want invalid logging level", errObj["message"])
	}
}

func TestServer_LoggingSetLevel_WarnAliasNormalizesToWarning(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseResp.Body.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      208,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "warn"},
	}, operationHeaders())
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("logging/setLevel error: %v", body["error"])
	}

	scanner := bufio.NewScanner(sseResp.Body)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for notifications/message on SSE stream")
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("reading SSE notification: %v", err)
			}
			continue
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		var msg map[string]any
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			continue
		}
		if msg["method"] != "notifications/message" {
			continue
		}
		params := msg["params"].(map[string]any)
		if params["level"] != "warning" {
			t.Fatalf("notification level = %v, want warning", params["level"])
		}
		return
	}
}

func TestServer_StreamableHTTP_PostSSEResponseMode(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "ping",
	})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
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

func TestServer_StreamableHTTP_GetRequiresSSEAccept(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406", resp.StatusCode)
	}
}

func TestServer_StreamableHTTP_GetRejectsUnknownSession(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Session-Id", "missing-session")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/ with unknown session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_SSECompatibility_InvalidOriginRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/sse with origin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestServer_SSECompatibility_RejectsUnknownSession(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("MCP-Session-Id", "missing-session")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/sse with unknown session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_SSECompatibility_DeprecationHeadersPresent(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/sse: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Deprecation"); got != "true" {
		t.Fatalf("Deprecation = %q, want true", got)
	}
	if got := resp.Header.Get("Link"); !strings.Contains(got, "/mcp/") || !strings.Contains(got, `rel="successor-version"`) {
		t.Fatalf("Link = %q, want successor-version /mcp/ link", got)
	}
	if got := resp.Header.Get("Warning"); !strings.Contains(strings.ToLower(got), "deprecated endpoint") {
		t.Fatalf("Warning = %q, want deprecation warning", got)
	}
}

func TestServer_StreamableHTTP_Get_NoDeprecationHeaders(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := strings.TrimSpace(resp.Header.Get("Deprecation")); got != "" {
		t.Fatalf("Deprecation = %q, want empty", got)
	}
}

func TestServer_StreamableHTTP_GetInvalidOriginRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/ with origin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestServer_SessionDelete_RequiresSessionID(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	delResp := mcpDelete(t, srv, nil)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete status = %d, want 400", delResp.StatusCode)
	}
}

func TestServer_SessionDelete_UnknownSessionIsIdempotent(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	delResp := mcpDelete(t, srv, map[string]string{"MCP-Session-Id": "missing-session"})
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}
}

func TestServer_StreamableHTTP_LastEventIDReplaysMissedNotifications(t *testing.T) {
	_, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	// First SSE connection to capture an initial event ID.
	req1, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req1.Header.Set("Accept", "text/event-stream")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("GET /mcp/: %v", err)
	}
	scanner1 := bufio.NewScanner(resp1.Body)

	setDebug := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      700,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "debug"},
	}, operationHeaders())
	_ = decodeBodyMap(t, setDebug)

	firstEventID := ""
	deadline := time.After(2 * time.Second)
outer1:
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first SSE event id")
		default:
			if !scanner1.Scan() {
				continue
			}
			line := scanner1.Text()
			if strings.HasPrefix(line, "id:") {
				firstEventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
			if strings.HasPrefix(line, "data:") && firstEventID != "" {
				break outer1
			}
		}
	}
	if firstEventID == "" {
		t.Fatal("expected first SSE event id")
	}
	_ = resp1.Body.Close()

	setInfo := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      701,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "info"},
	}, operationHeaders())
	_ = decodeBodyMap(t, setInfo)

	// Reconnect and ask for replay after firstEventID.
	req2, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req2.Header.Set("Accept", "text/event-stream")
	req2.Header.Set("Last-Event-ID", firstEventID)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /mcp/ replay: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}

	scanner2 := bufio.NewScanner(resp2.Body)
	replayedID := ""
	replayedData := ""
	deadline = time.After(2 * time.Second)
outer2:
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for replayed SSE event")
		default:
			if !scanner2.Scan() {
				continue
			}
			line := scanner2.Text()
			if strings.HasPrefix(line, "id:") {
				replayedID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
			if strings.HasPrefix(line, "data:") {
				replayedData = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if replayedID != "" {
					break outer2
				}
			}
		}
	}
	if replayedID == "" || replayedID == firstEventID {
		t.Fatalf("expected replayed event id newer than %q, got %q", firstEventID, replayedID)
	}
	var msg map[string]any
	if err := json.Unmarshal([]byte(replayedData), &msg); err != nil {
		t.Fatalf("invalid replayed payload: %v (%q)", err, replayedData)
	}
	if msg["method"] != "notifications/message" {
		t.Fatalf("replayed method = %v, want notifications/message", msg["method"])
	}
}

func TestServer_StreamableHTTP_LastEventIDInvalidRejected(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", "not-a-number")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/ with invalid Last-Event-ID: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_StreamableHTTP_LastEventIDStaleRejected(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	mcpSrv.notificationReplayLimit = 1
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req1, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req1.Header.Set("Accept", "text/event-stream")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("GET /mcp/: %v", err)
	}
	scanner1 := bufio.NewScanner(resp1.Body)

	setDebug := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      710,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "debug"},
	}, operationHeaders())
	_ = decodeBodyMap(t, setDebug)

	staleID := ""
	deadline := time.After(2 * time.Second)
	for staleID == "" {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for stale replay id")
		default:
			if !scanner1.Scan() {
				continue
			}
			line := scanner1.Text()
			if strings.HasPrefix(line, "id:") {
				staleID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
		}
	}
	_ = resp1.Body.Close()

	setInfo := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      711,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "info"},
	}, operationHeaders())
	_ = decodeBodyMap(t, setInfo)

	req2, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req2.Header.Set("Accept", "text/event-stream")
	req2.Header.Set("Last-Event-ID", staleID)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /mcp/ stale replay: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp2.StatusCode)
	}
}

func TestServer_SessionDelete_InvalidatesSession(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	initResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)
	sid := initResp.Header.Get("MCP-Session-Id")
	if strings.TrimSpace(sid) == "" {
		t.Fatal("initialize must return MCP-Session-Id header")
	}
	_ = decodeBodyMap(t, initResp)

	notifyResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}, map[string]string{"MCP-Session-Id": sid})
	notifyResp.Body.Close()

	delResp := mcpDelete(t, srv, map[string]string{"MCP-Session-Id": sid})
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}, map[string]string{"MCP-Session-Id": sid, "MCP-Protocol-Version": ProtocolVersion})
	body := decodeBodyMap(t, resp)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != float64(RPCInvalidParams) {
		t.Fatalf("error.code = %v, want %d", errObj["code"], RPCInvalidParams)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Session-Id", sid)
	getResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /mcp/ with deleted session: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("streamable get status = %d, want 400", getResp.StatusCode)
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
	return NewServer(nil, nil, providers...)
}

func stdioLifecycleLine(t *testing.T, version string) []byte {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	})
	return append(b, '\n')
}

func stdioInitializedLine(t *testing.T) []byte {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	return append(b, '\n')
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

func TestServer_ServeStdio_InitializeResponseIsNewlineDelimitedJSON(t *testing.T) {
	srv := newStdioServer(t)
	in := bytes.NewReader(stdioLifecycleLine(t, ProtocolVersion))
	out := &strings.Builder{}

	if err := srv.ServeStdio(context.Background(), in, out); err != nil {
		t.Fatalf("ServeStdio returned error: %v", err)
	}
	line := strings.TrimSpace(out.String())
	if line == "" {
		t.Fatal("expected a JSON response line")
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v; raw=%q", err, line)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T; body=%q", resp["result"], line)
	}
	if result["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", result["protocolVersion"], ProtocolVersion)
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

func TestServer_ServeStdio_FullLifecycleThenToolsList(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "probe", Description: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	srv := newStdioServer(t, provider)

	var input bytes.Buffer
	input.Write(stdioLifecycleLine(t, ProtocolVersion))
	input.Write(stdioInitializedLine(t))
	toolsListMsg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	input.Write(append(toolsListMsg, '\n'))

	out := &strings.Builder{}
	if err := srv.ServeStdio(context.Background(), &input, out); err != nil {
		t.Fatalf("ServeStdio returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines (initialize + tools/list), got %d:\n%s", len(lines), out.String())
	}

	// Find the tools/list response by id (response order is not guaranteed).
	var toolsResp map[string]any
	for _, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response line is not valid JSON: %v; raw=%q", err, line)
		}
		if m["id"] == float64(2) {
			toolsResp = m
		}
	}
	if toolsResp == nil {
		t.Fatalf("did not find tools/list response (id=2) in output:\n%s", out.String())
	}
	result, ok := toolsResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result type = %T", toolsResp["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools type = %T", result["tools"])
	}
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}
}

func TestServer_ServeStdio_LoggingSetLevelEmitsMessageNotification(t *testing.T) {
	srv := newStdioServer(t)

	var input bytes.Buffer
	input.Write(stdioLifecycleLine(t, ProtocolVersion))
	input.Write(stdioInitializedLine(t))
	setLevelMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "logging/setLevel",
		"params":  map[string]any{"level": "debug"},
	})
	input.Write(append(setLevelMsg, '\n'))

	out := &strings.Builder{}
	if err := srv.ServeStdio(context.Background(), &input, out); err != nil {
		t.Fatalf("ServeStdio returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines (initialize + logging/setLevel + notification), got %d:\n%s", len(lines), out.String())
	}

	var sawResponse bool
	var sawNotification bool
	for _, line := range lines {
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("output line is not valid JSON: %v; raw=%q", err, line)
		}
		if msg["id"] == float64(2) {
			sawResponse = true
		}
		if msg["method"] == "notifications/message" {
			params := msg["params"].(map[string]any)
			if params["level"] != "debug" {
				t.Fatalf("notification level = %v, want debug", params["level"])
			}
			sawNotification = true
		}
	}
	if !sawResponse {
		t.Fatal("did not find logging/setLevel response")
	}
	if !sawNotification {
		t.Fatal("did not find notifications/message output")
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

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))
	_, _ = inWriter.Write(stdioInitializedLine(t))
	callMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      12,
		"method":  "tools/call",
		"params":  map[string]any{"name": "block", "arguments": map[string]any{}},
	})
	_, _ = inWriter.Write(append(callMsg, '\n'))

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

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))

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
			if msg["id"] == float64(1) {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for initialize response on stdio")
		}
	}

	srv.emitToolsListChanged()
	_ = inWriter.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}
	_ = outWriter.Close()

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

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))

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
			if msg["id"] == float64(1) {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for initialize response on stdio")
		}
	}

	_, _ = inWriter.Write(stdioInitializedLine(t))
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
	_ = inWriter.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}
	_ = outWriter.Close()

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

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))

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
			if msg["id"] == float64(1) {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for initialize response on stdio")
		}
	}

	_, _ = inWriter.Write(stdioInitializedLine(t))
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
	_ = inWriter.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}
	_ = outWriter.Close()

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

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))

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
			if msg["id"] == float64(1) {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for initialize response on stdio")
		}
	}

	_, _ = inWriter.Write(stdioInitializedLine(t))
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
	_ = inWriter.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}
	_ = outWriter.Close()

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

func TestServer_ResourcesSubscribe_ReturnsEmpty(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	for _, method := range []string{"resources/subscribe", "resources/unsubscribe"} {
		t.Run(method, func(t *testing.T) {
			resp := mcpPost(t, srv, map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  method,
				"params":  map[string]any{"uri": "file:///workspace/README.md"},
			}, operationHeaders())

			body := decodeBodyMap(t, resp)
			if body["error"] != nil {
				t.Fatalf("%s returned error: %v", method, body["error"])
			}
			result, ok := body["result"].(map[string]any)
			if !ok {
				t.Fatalf("%s result type = %T", method, body["result"])
			}
			if len(result) != 0 {
				t.Fatalf("%s result = %v, want empty", method, result)
			}
		})
	}
}

func TestServer_ResourcesSubscribe_EmitsResourceUpdatedOnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	// Open SSE connection
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	// Subscribe to a resource URI
	const testURI = "file:///workspace/README.md"
	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "resources/subscribe",
		"params":  map[string]any{"uri": testURI},
	}, operationHeaders())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe status = %d", resp.StatusCode)
	}

	// Trigger a resource update
	mcpSrv.emitResourceUpdated(testURI)

	// Read SSE stream and find the notification
	scanner := bufio.NewScanner(sseresp.Body)
	var sawNotification bool
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}
		if msg["method"] == "notifications/resources/updated" {
			params, _ := msg["params"].(map[string]any)
			if params["uri"] == testURI {
				sawNotification = true
				break
			}
		}
	}
	if !sawNotification {
		t.Fatal("did not receive notifications/resources/updated on SSE")
	}
}

func TestServer_ResourcesSubscribe_TrimsURIForSubscriptionKeys(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	// Open SSE connection
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	// Subscribe with surrounding whitespace. Updates should still match the trimmed URI.
	const canonicalURI = "file:///workspace/README.md"
	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "resources/subscribe",
		"params":  map[string]any{"uri": "  " + canonicalURI + "  "},
	}, operationHeaders())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe status = %d", resp.StatusCode)
	}

	mcpSrv.emitResourceUpdated(canonicalURI)

	lineCh := make(chan string, 8)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(sseresp.Body)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
		}
	}()

	deadline := time.After(500 * time.Millisecond)
	var sawNotification bool
	timedOut := false
	for !sawNotification && !timedOut {
		select {
		case err := <-errCh:
			t.Fatalf("reading SSE notification: %v", err)
		case line := <-lineCh:
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] == "notifications/resources/updated" {
				params, _ := msg["params"].(map[string]any)
				if params["uri"] == canonicalURI {
					sawNotification = true
					break
				}
			}
		case <-deadline:
			timedOut = true
		}
	}
	if !sawNotification {
		t.Fatal("did not receive notifications/resources/updated for trimmed URI")
	}
}

func TestServer_ResourcesSubscribe_NoEmitWhenNotSubscribed(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	// Open SSE connection (no subscription made)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	// Emit for a URI nobody subscribed to — should produce no notification
	mcpSrv.emitResourceUpdated("file:///workspace/README.md")

	// Verify no notifications/resources/updated arrives (short timeout)
	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(sseresp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				ch <- line
				return
			}
		}
	}()
	select {
	case line := <-ch:
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			if msg["method"] == "notifications/resources/updated" {
				t.Fatal("received unexpected notifications/resources/updated")
			}
		}
	case <-time.After(100 * time.Millisecond):
		// expected: no notification
	}
}

func TestServer_ResourcesSubscribe_UnsubscribeStopsResourceUpdatedOnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	const canonicalURI = "file:///workspace/README.md"
	subscribeResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "resources/subscribe",
		"params":  map[string]any{"uri": "  " + canonicalURI + "  "},
	}, operationHeaders())
	if subscribeResp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe status = %d", subscribeResp.StatusCode)
	}

	unsubscribeResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "resources/unsubscribe",
		"params":  map[string]any{"uri": canonicalURI},
	}, operationHeaders())
	if unsubscribeResp.StatusCode != http.StatusOK {
		t.Fatalf("unsubscribe status = %d", unsubscribeResp.StatusCode)
	}

	mcpSrv.emitResourceUpdated(canonicalURI)

	ch := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(sseresp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				ch <- line
				return
			}
		}
	}()

	select {
	case line := <-ch:
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err == nil {
			if msg["method"] == "notifications/resources/updated" {
				t.Fatal("received notifications/resources/updated after unsubscribe")
			}
		}
	case <-time.After(100 * time.Millisecond):
		// expected: no resource updated notification
	}
}

func TestServer_ResourcesSubscribe_ReferenceCountRequiresMatchingUnsubscribes(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	const testURI = "file:///workspace/README.md"
	for _, id := range []int{7, 8} {
		resp := mcpPost(t, srv, map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "resources/subscribe",
			"params":  map[string]any{"uri": testURI},
		}, operationHeaders())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("subscribe status = %d", resp.StatusCode)
		}
	}

	firstUnsub := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      9,
		"method":  "resources/unsubscribe",
		"params":  map[string]any{"uri": testURI},
	}, operationHeaders())
	if firstUnsub.StatusCode != http.StatusOK {
		t.Fatalf("first unsubscribe status = %d", firstUnsub.StatusCode)
	}

	mcpSrv.emitResourceUpdated(testURI)

	lineCh := make(chan string, 8)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(sseresp.Body)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errCh <- scanErr
		}
	}()

	deadline := time.After(500 * time.Millisecond)
	sawFirstNotification := false
	for !sawFirstNotification {
		select {
		case scanErr := <-errCh:
			t.Fatalf("reading SSE after first unsubscribe: %v", scanErr)
		case line := <-lineCh:
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/resources/updated" {
				continue
			}
			params, _ := msg["params"].(map[string]any)
			if params["uri"] == testURI {
				sawFirstNotification = true
			}
		case <-deadline:
			t.Fatal("did not receive notifications/resources/updated after first unsubscribe")
		}
	}

	secondUnsub := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "resources/unsubscribe",
		"params":  map[string]any{"uri": testURI},
	}, operationHeaders())
	if secondUnsub.StatusCode != http.StatusOK {
		t.Fatalf("second unsubscribe status = %d", secondUnsub.StatusCode)
	}

	mcpSrv.emitResourceUpdated(testURI)

	select {
	case scanErr := <-errCh:
		t.Fatalf("reading SSE after second unsubscribe: %v", scanErr)
	case line := <-lineCh:
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err == nil && msg["method"] == "notifications/resources/updated" {
				t.Fatal("received notifications/resources/updated after second unsubscribe")
			}
		}
	case <-time.After(100 * time.Millisecond):
		// expected: no notification after refcount reaches zero
	}

	extraUnsub := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      11,
		"method":  "resources/unsubscribe",
		"params":  map[string]any{"uri": testURI},
	}, operationHeaders())
	extraBody := decodeBodyMap(t, extraUnsub)
	if extraBody["error"] != nil {
		t.Fatalf("unexpected error on extra unsubscribe: %v", extraBody["error"])
	}
}

func TestServer_RuntimeMutationTool_EmitsResourcesListChangedOnSSE(t *testing.T) {
	provider := &mutableRuntimeResourceProvider{}
	_, srv := newTestHTTPServerPair(t, provider)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseResp.Body.Close()

	callResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      61,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_mutate_demo",
			"arguments": map[string]any{},
		},
	}, operationHeaders())
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call status = %d", callResp.StatusCode)
	}
	callBody := decodeBodyMap(t, callResp)
	if callBody["error"] != nil {
		t.Fatalf("tools/call error = %v", callBody["error"])
	}

	scanner := bufio.NewScanner(sseResp.Body)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for notifications/resources/list_changed")
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("reading SSE stream: %v", err)
			}
			continue
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}
		if msg["method"] == "notifications/resources/list_changed" {
			return
		}
	}
}

func TestServer_RuntimeNonDestructiveMutationTool_EmitsResourcesListChangedOnSSE(t *testing.T) {
	provider := &nonDestructiveRuntimeResourceProvider{}
	_, srv := newTestHTTPServerPair(t, provider)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseResp.Body.Close()

	callResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      62,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_update_demo",
			"arguments": map[string]any{},
		},
	}, operationHeaders())
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call status = %d", callResp.StatusCode)
	}
	callBody := decodeBodyMap(t, callResp)
	if callBody["error"] != nil {
		t.Fatalf("tools/call error = %v", callBody["error"])
	}

	scanner := bufio.NewScanner(sseResp.Body)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for notifications/resources/list_changed")
		default:
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("reading SSE stream: %v", err)
			}
			continue
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}
		if msg["method"] == "notifications/resources/list_changed" {
			return
		}
	}
}

func TestServer_ResourcesSubscribe_EmitResourceUpdatedTrimsURI(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	const canonicalURI = "file:///workspace/README.md"
	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "resources/subscribe",
		"params":  map[string]any{"uri": canonicalURI},
	}, operationHeaders())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe status = %d", resp.StatusCode)
	}

	mcpSrv.emitResourceUpdated("  " + canonicalURI + "  ")

	scanner := bufio.NewScanner(sseresp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("did not receive notifications/resources/updated for trimmed emit URI")
		default:
			if !scanner.Scan() {
				continue
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/resources/updated" {
				continue
			}
			params, _ := msg["params"].(map[string]any)
			if params["uri"] != canonicalURI {
				t.Fatalf("notification uri = %v, want %s", params["uri"], canonicalURI)
			}
			return
		}
	}
}

func TestServer_EmitResourceListChanged_OnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	mcpSrv.emitResourceListChanged()

	scanner := bufio.NewScanner(sseresp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("did not receive notifications/resources/list_changed")
		default:
			if !scanner.Scan() {
				continue
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/resources/list_changed" {
				continue
			}
			return
		}
	}
}

func TestServer_EmitPromptsListChanged_OnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	mcpSrv.emitPromptsListChanged()

	scanner := bufio.NewScanner(sseresp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("did not receive notifications/prompts/list_changed")
		default:
			if !scanner.Scan() {
				continue
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/prompts/list_changed" {
				continue
			}
			return
		}
	}
}

func TestServer_EmitToolsListChanged_OnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	mcpSrv.emitToolsListChanged()

	scanner := bufio.NewScanner(sseresp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("did not receive notifications/tools/list_changed")
		default:
			if !scanner.Scan() {
				continue
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/tools/list_changed" {
				continue
			}
			return
		}
	}
}

func TestServer_RegisterProvider_EmitsToolsListChanged_OnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	mcpSrv.registerProvider(&staticProvider{
		tools: []Tool{{
			Name:        "dynamic_test_tool",
			Description: "dynamic tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	})

	scanner := bufio.NewScanner(sseresp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("did not receive notifications/tools/list_changed")
		default:
			if !scanner.Scan() {
				continue
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/tools/list_changed" {
				continue
			}
			return
		}
	}
}

func TestServer_RegisterProvider_EmitsResourcesListChanged_OnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	mcpSrv.registerProvider(&staticResourceProvider{})

	scanner := bufio.NewScanner(sseresp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("did not receive notifications/resources/list_changed")
		default:
			if !scanner.Scan() {
				continue
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/resources/list_changed" {
				continue
			}
			return
		}
	}
}

func TestServer_RegisterProvider_EmitsPromptsListChanged_OnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	mcpSrv.registerProvider(&staticPromptProvider{})

	scanner := bufio.NewScanner(sseresp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("did not receive notifications/prompts/list_changed")
		default:
			if !scanner.Scan() {
				continue
			}
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err != nil {
				continue
			}
			if msg["method"] != "notifications/prompts/list_changed" {
				continue
			}
			return
		}
	}
}

func TestServer_RegisterProvider_DoesNotEmitListChangedBeforeLifecycleReady(t *testing.T) {
	mcpSrv := NewServer(nil, nil)
	notifications := make(chan []byte, 4)
	mcpSrv.subscribeNotifications(notifications)
	defer mcpSrv.unsubscribeNotifications(notifications)

	mcpSrv.registerProvider(&staticProvider{
		tools: []Tool{{
			Name:        "dynamic_test_tool",
			Description: "dynamic tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	})
	mcpSrv.registerProvider(&staticResourceProvider{})
	mcpSrv.registerProvider(&staticPromptProvider{})

	select {
	case payload := <-notifications:
		t.Fatalf("unexpected notification before lifecycle ready: %s", string(payload))
	case <-time.After(100 * time.Millisecond):
		return
	}
}

func TestServer_RegisterProvider_WiresResourceListChangedEmitter(t *testing.T) {
	mcpSrv := NewServer(nil, nil)
	notifications := make(chan []byte, 2)
	mcpSrv.subscribeNotifications(notifications)
	defer mcpSrv.unsubscribeNotifications(notifications)

	provider := &emitterAwareProvider{}
	mcpSrv.registerProvider(provider)

	if provider.emitter == nil {
		t.Fatal("expected resource list changed emitter callback to be wired")
	}

	provider.emitter()
	select {
	case payload := <-notifications:
		if !bytes.Contains(payload, []byte("notifications/resources/list_changed")) {
			t.Fatalf("unexpected notification payload: %s", string(payload))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for resources/list_changed notification")
	}
}

func TestServer_RegisterProvider_WiresResourceUpdatedEmitter(t *testing.T) {
	mcpSrv := NewServer(nil, nil)
	notifications := make(chan []byte, 2)
	mcpSrv.subscribeNotifications(notifications)
	defer mcpSrv.unsubscribeNotifications(notifications)

	provider := &emitterAwareProvider{}
	mcpSrv.registerProvider(provider)

	if provider.updatedEmitter == nil {
		t.Fatal("expected resource updated emitter callback to be wired")
	}

	const testURI = "oc://demo/item"
	mcpSrv.mu.Lock()
	mcpSrv.resourceSubscriptions[testURI] = 1
	mcpSrv.mu.Unlock()
	provider.updatedEmitter(testURI)
	select {
	case payload := <-notifications:
		if !bytes.Contains(payload, []byte("notifications/resources/updated")) {
			t.Fatalf("unexpected notification payload: %s", string(payload))
		}
		if !bytes.Contains(payload, []byte(testURI)) {
			t.Fatalf("expected updated uri in payload: %s", string(payload))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for resources/updated notification")
	}
}

func TestServer_RegisterProvider_WiresPromptListChangedEmitter(t *testing.T) {
	mcpSrv := NewServer(nil, nil)
	notifications := make(chan []byte, 2)
	mcpSrv.subscribeNotifications(notifications)
	defer mcpSrv.unsubscribeNotifications(notifications)

	provider := &emitterAwareProvider{}
	mcpSrv.registerProvider(provider)

	if provider.promptEmitter == nil {
		t.Fatal("expected prompt list changed emitter callback to be wired")
	}

	provider.promptEmitter()
	select {
	case payload := <-notifications:
		if !bytes.Contains(payload, []byte("notifications/prompts/list_changed")) {
			t.Fatalf("unexpected notification payload: %s", string(payload))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for prompts/list_changed notification")
	}
}

func TestServer_ResourceCapability_AdvertisesSubscribe(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "1.0"},
		},
	}, nil)

	body := decodeBodyMap(t, resp)
	result := body["result"].(map[string]any)
	caps := result["capabilities"].(map[string]any)
	resourcesCap, ok := caps["resources"].(map[string]any)
	if !ok {
		t.Fatalf("resources capability missing or wrong type: %T", caps["resources"])
	}
	if resourcesCap["subscribe"] != true {
		t.Fatalf("resources.subscribe = %v, want true", resourcesCap["subscribe"])
	}
}

func TestServer_EmitPromptsListChanged_SendsNotificationOnSSE(t *testing.T) {
	mcpSrv, srv := newTestHTTPServerPair(t)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	// Open SSE connection
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	// Emit prompts list changed
	mcpSrv.emitPromptsListChanged()

	// Read SSE stream
	scanner := bufio.NewScanner(sseresp.Body)
	var sawNotification bool
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}
		if msg["method"] == "notifications/prompts/list_changed" {
			sawNotification = true
			break
		}
	}
	if !sawNotification {
		t.Fatal("did not receive notifications/prompts/list_changed on SSE")
	}
}

func TestServer_ToolsCall_EmitsProgressNotification(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "slow-tool", Description: "slow", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "done", nil
		},
	}
	mcpSrv, srv := newTestHTTPServerPair(t, provider)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	// Open SSE to catch progress notifications
	sseReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()
	_ = mcpSrv // ensure mcpSrv is in scope (server is wired to the httptest.Server)

	// Call the tool with a progress token
	callResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      50,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "slow-tool",
			"arguments": map[string]any{},
			"_meta":     map[string]any{"progressToken": "prog-1"},
		},
	}, operationHeaders())
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call status = %d", callResp.StatusCode)
	}
	callBody := decodeBodyMap(t, callResp)
	if callBody["error"] != nil {
		t.Fatalf("tools/call returned error: %v", callBody["error"])
	}

	// Read SSE stream looking for two progress notifications (0 and 1)
	scanner := bufio.NewScanner(sseresp.Body)
	var progressValues []float64
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			continue
		}
		if msg["method"] != "notifications/progress" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		if params["progressToken"] != "prog-1" {
			continue
		}
		progressValues = append(progressValues, params["progress"].(float64))
		if len(progressValues) >= 2 {
			break
		}
	}
	if len(progressValues) < 2 {
		t.Fatalf("expected 2 progress notifications, got %d: %v", len(progressValues), progressValues)
	}
	if progressValues[0] != 0 || progressValues[1] != 1 {
		t.Fatalf("progress values = %v, want [0, 1]", progressValues)
	}
}

func TestServer_ToolsCall_NoProgressWhenNoToken(t *testing.T) {
	provider := &staticProvider{
		tools: []Tool{{Name: "noop", Description: "noop", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "ok", nil
		},
	}
	_, srv := newTestHTTPServerPair(t, provider)
	defer srv.Close()
	requireLifecycleReady(t, srv)

	// Open SSE
	sseReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	sseresp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseresp.Body.Close()

	// Call without progress token
	callResp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      51,
		"method":  "tools/call",
		"params":  map[string]any{"name": "noop", "arguments": map[string]any{}},
	}, operationHeaders())
	if callResp.StatusCode != http.StatusOK {
		t.Fatalf("tools/call status = %d", callResp.StatusCode)
	}
	_ = decodeBodyMap(t, callResp)

	// Ensure no notifications/progress arrives within a short window
	notifCh := make(chan map[string]any, 4)
	go func() {
		scanner := bufio.NewScanner(sseresp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var msg map[string]any
			if err := json.Unmarshal([]byte(data), &msg); err == nil {
				notifCh <- msg
			}
		}
	}()
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case msg := <-notifCh:
			if msg["method"] == "notifications/progress" {
				t.Fatalf("unexpected notifications/progress received: %v", msg)
			}
		case <-timeout:
			return // no spurious progress
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
	requireLifecycleReady(t, srv)

	start := time.Now()
	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0",
		"id":      90,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "slow-timeout",
			"arguments": map[string]any{},
		},
	}, operationHeaders())
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

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))

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
			if msg["id"] == float64(1) {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for initialize response on stdio")
		}
	}

	srv.emitResourceListChanged()
	_ = inWriter.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}
	_ = outWriter.Close()

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
	defer inWriter.Close()
	out := &strings.Builder{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ServeStdio(context.Background(), inReader, out)
	}()

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))
	_, _ = inWriter.Write(stdioInitializedLine(t))
	callMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      62,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "runtime_mutate_demo",
			"arguments": map[string]any{},
		},
	})
	_, _ = inWriter.Write(append(callMsg, '\n'))
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
	var sawCallResponse bool
	var sawNotification bool
	for _, line := range lines {
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
	}
	if !sawCallResponse {
		t.Fatalf("did not find tools/call response in:\n%s", out.String())
	}
	if !sawNotification {
		t.Fatalf("did not find notifications/resources/list_changed output in:\n%s", out.String())
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

	_, _ = inWriter.Write(stdioLifecycleLine(t, ProtocolVersion))

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
			if msg["id"] == float64(1) {
				initSeen = true
			}
		case readErr := <-errReadCh:
			t.Fatalf("reading stdio output: %v", readErr)
		case <-initDeadline:
			t.Fatal("timed out waiting for initialize response on stdio")
		}
	}

	srv.emitPromptsListChanged()
	_ = inWriter.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeStdio returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ServeStdio to finish")
	}
	_ = outWriter.Close()

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
