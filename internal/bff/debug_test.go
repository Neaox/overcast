package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestDebugState_proxiesStateFromEmulator(t *testing.T) {
	var gotPath string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]string{"sqs/queues": {"queue-a"}})
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content-type, got %q", got)
	}
	if gotPath != "/_debug/state" {
		t.Fatalf("expected emulator path /_debug/state, got %q", gotPath)
	}
	if got := rec.Body.String(); got != "{\"sqs/queues\":[\"queue-a\"]}\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestDebugStateNamespace_proxiesNamespaceFromEmulator(t *testing.T) {
	var gotPath string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"queue-a": `{"name":"queue-a"}`})
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state/sqs%2Fqueues", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_debug/state/sqs%2Fqueues" {
		t.Fatalf("expected encoded emulator namespace path, got %q", gotPath)
	}
	if got := rec.Body.String(); got != "{\"queue-a\":\"{\\\"name\\\":\\\"queue-a\\\"}\"}\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestDebugStateNamespaceKey_proxiesRawValueFromEmulator(t *testing.T) {
	var gotPath string
	var gotQuery string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"layer"}`))
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state/lambda%3Alayers?key=us-east-1%2Fdeps%3A0000000001", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_debug/state/lambda%3Alayers" {
		t.Fatalf("expected encoded emulator namespace path, got %q", gotPath)
	}
	if gotQuery != "key=us-east-1%2Fdeps%3A0000000001" {
		t.Fatalf("expected raw key query forwarded, got %q", gotQuery)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content-type, got %q", got)
	}
	if got := rec.Body.String(); got != `{"name":"layer"}` {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestDebugState_disabledReturnsJSONError(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.NotFoundHandler())
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content-type, got %q", got)
	}
	if got := rec.Body.String(); got != "{\"error\":\"DebugDisabled\",\"message\":\"OVERCAST_DEBUG must be enabled to inspect raw state or storage diagnostics.\"}\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestDebugMetrics_proxiesMetricsFromEmulator(t *testing.T) {
	var gotPath string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stores":[{"mode":"hybrid","journalMode":"wal"}],"advisories":[]}`))
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_debug/metrics" {
		t.Fatalf("expected emulator path /_debug/metrics, got %q", gotPath)
	}
	if got := rec.Body.String(); got != `{"stores":[{"mode":"hybrid","journalMode":"wal"}],"advisories":[]}` {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestDebugMetrics_disabledReturnsJSONError(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.NotFoundHandler())
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content-type, got %q", got)
	}
	if got := rec.Body.String(); got != "{\"error\":\"DebugDisabled\",\"message\":\"OVERCAST_DEBUG must be enabled to inspect raw state or storage diagnostics.\"}\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func testStaticFS() fstest.MapFS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><head></head><body></body></html>")}}
}
