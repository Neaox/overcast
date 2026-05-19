//go:build !slim

package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/mcp"
	"github.com/Neaox/overcast/internal/state"
)

func newMCPRouterTestServer(t *testing.T, mutateCfg ...func(*config.Config)) *httptest.Server {
	t.Helper()

	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",
		Services: map[string]bool{
			"s3":       true,
			"sqs":      true,
			"dynamodb": true,
			"sns":      true,
			"lambda":   true,
		},
		ShutdownTimeout: 0,
		SigV4Validate:   false,
		Debug:           false,
	}
	for _, mutate := range mutateCfg {
		if mutate != nil {
			mutate(cfg)
		}
	}

	store := state.NewMemoryStore()
	handler, preShutdown, cleanup, _ := New(cfg, store, zap.NewNop(), clock.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		preShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cleanup(ctx)
		srv.Close()
	})
	return srv
}

func TestRuntimeMCPRoutes_RemoteExposureRequiresBearerToken(t *testing.T) {
	srv := newMCPRouterTestServer(t, func(cfg *config.Config) {
		cfg.MCPRemoteExposure = true
		cfg.MCPAuthToken = "test-token"
		cfg.MCPReplayLimit = 32
	})

	initBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "router-test", "version": "1.0"},
		},
	})

	unauthReq, err := http.NewRequest(http.MethodPost, srv.URL+"/_mcp", strings.NewReader(string(initBody)))
	if err != nil {
		t.Fatalf("new unauth request: %v", err)
	}
	unauthReq.Header.Set("Content-Type", "application/json")
	unauthResp, err := http.DefaultClient.Do(unauthReq)
	if err != nil {
		t.Fatalf("POST /_mcp unauthenticated: %v", err)
	}
	defer unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", unauthResp.StatusCode)
	}
	if got := unauthResp.Header.Get("WWW-Authenticate"); !strings.Contains(strings.ToLower(got), "bearer") {
		t.Fatalf("WWW-Authenticate = %q, want bearer challenge", got)
	}

	authReq, err := http.NewRequest(http.MethodPost, srv.URL+"/_mcp", strings.NewReader(string(initBody)))
	if err != nil {
		t.Fatalf("new auth request: %v", err)
	}
	authReq.Header.Set("Content-Type", "application/json")
	authReq.Header.Set("Authorization", "Bearer test-token")
	authResp, err := http.DefaultClient.Do(authReq)
	if err != nil {
		t.Fatalf("POST /_mcp authenticated: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("auth status = %d, want 200", authResp.StatusCode)
	}
}

func TestRuntimeMCPRoutes_InitializeAndSessionLifecycle(t *testing.T) {
	srv := newMCPRouterTestServer(t)

	initBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "router-test", "version": "1.0"},
		},
	})
	initReq, err := http.NewRequest(http.MethodPost, srv.URL+"/_mcp", strings.NewReader(string(initBody)))
	if err != nil {
		t.Fatalf("new initialize request: %v", err)
	}
	initReq.Header.Set("Content-Type", "application/json")
	initResp, err := http.DefaultClient.Do(initReq)
	if err != nil {
		t.Fatalf("POST /_mcp initialize: %v", err)
	}
	defer initResp.Body.Close()
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", initResp.StatusCode)
	}
	sid := strings.TrimSpace(initResp.Header.Get("MCP-Session-Id"))
	if sid == "" {
		t.Fatal("initialize response missing MCP-Session-Id")
	}

	delReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/_mcp", nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	delReq.Header.Set("MCP-Session-Id", sid)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /_mcp: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}

	getReq, err := http.NewRequest(http.MethodGet, srv.URL+"/_mcp", nil)
	if err != nil {
		t.Fatalf("new streamable GET request: %v", err)
	}
	getReq.Header.Set("Accept", "text/event-stream")
	getReq.Header.Set("MCP-Session-Id", sid)
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET /_mcp with deleted session: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("streamable GET status = %d, want 400", getResp.StatusCode)
	}
}

func TestRuntimeMCPRoutes_StreamableGetRequiresAcceptHeader(t *testing.T) {
	srv := newMCPRouterTestServer(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/_mcp", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /_mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406", resp.StatusCode)
	}
}

func TestRuntimeMCPRoutes_StreamableGetAndLegacySSE_Connect(t *testing.T) {
	srv := newMCPRouterTestServer(t)

	tests := []struct {
		name string
		path string
	}{
		{name: "streamable-get", path: "/_mcp"},
		{name: "legacy-sse", path: "/_mcp/sse"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Accept", "text/event-stream")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", tt.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(got), "text/event-stream") {
				t.Fatalf("Content-Type = %q, want text/event-stream", got)
			}
			buf := make([]byte, 64)
			n, readErr := resp.Body.Read(buf)
			if readErr != nil && readErr != io.EOF {
				t.Fatalf("read SSE prelude: %v", readErr)
			}
			if !strings.Contains(string(buf[:n]), ": connected") {
				t.Fatalf("SSE prelude = %q, want : connected", string(buf[:n]))
			}
		})
	}
}

func TestRuntimeMCPRoutes_RejectForbiddenOrigin(t *testing.T) {
	srv := newMCPRouterTestServer(t)

	postReq, err := http.NewRequest(http.MethodPost, srv.URL+"/_mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("Origin", "https://evil.example")
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST /_mcp with forbidden origin: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST status = %d, want 403", postResp.StatusCode)
	}

	getReq, err := http.NewRequest(http.MethodGet, srv.URL+"/_mcp", nil)
	if err != nil {
		t.Fatalf("new GET request: %v", err)
	}
	getReq.Header.Set("Accept", "text/event-stream")
	getReq.Header.Set("Origin", "https://evil.example")
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET /_mcp with forbidden origin: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET status = %d, want 403", getResp.StatusCode)
	}

	delReq, err := http.NewRequest(http.MethodDelete, srv.URL+"/_mcp", nil)
	if err != nil {
		t.Fatalf("new DELETE request: %v", err)
	}
	delReq.Header.Set("Origin", "https://evil.example")
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE /_mcp with forbidden origin: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusForbidden {
		t.Fatalf("DELETE status = %d, want 403", delResp.StatusCode)
	}
}
