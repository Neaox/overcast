package bff

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSettingsHTTPSStatus_proxiesDaemonStatus verifies GET /api/settings/https
// passes /_overcast/tls/status through verbatim.
func TestSettingsHTTPSStatus_proxiesDaemonStatus(t *testing.T) {
	const body = `{"mode":"off","caExists":false,"trustStore":"not_installed","inContainer":false}`
	var gotPath string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/settings/https", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_overcast/tls/status" {
		t.Fatalf("expected emulator path /_overcast/tls/status, got %q", gotPath)
	}
	if strings.TrimSpace(rec.Body.String()) != body {
		t.Errorf("body = %q, want the daemon response verbatim", rec.Body.String())
	}
}

// TestSettingsHTTPSEnable_forwardsOriginAndStatus verifies the setup proxy
// POSTs upstream, forwards the browser Origin (the daemon's cross-origin
// guard must judge the real page origin), and passes the daemon's verdict —
// including a 403 — back unmodified.
func TestSettingsHTTPSEnable_forwardsOriginAndStatus(t *testing.T) {
	var gotMethod, gotPath, gotOrigin string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotOrigin = r.Method, r.URL.EscapedPath(), r.Header.Get("Origin")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"ForeignOrigin"}`)) //nolint:errcheck
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/https/enable", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotMethod != http.MethodPost || gotPath != "/_overcast/tls/setup" {
		t.Fatalf("upstream call = %s %s, want POST /_overcast/tls/setup", gotMethod, gotPath)
	}
	if gotOrigin != "https://evil.example.com" {
		t.Errorf("forwarded Origin = %q, want the browser's origin", gotOrigin)
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want the daemon's 403 passed through", rec.Code)
	}
}

// TestSettingsHTTPSEnable_successPassesThrough verifies the happy path body
// reaches the browser unchanged.
func TestSettingsHTTPSEnable_successPassesThrough(t *testing.T) {
	const body = `{"ok":true,"caReady":true,"trustInstalled":true,"restartRequired":true}`
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/https/enable", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != body {
		t.Errorf("body = %q, want the daemon response verbatim", rec.Body.String())
	}
}
