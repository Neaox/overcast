package bff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestDebugState_proxiesStateFromEmulator(t *testing.T) {
	// Given: a fake emulator with the raw state debug endpoint enabled.
	var gotPath string
	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]string{"sqs/queues": {"queue-a"}})
	}))
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the browser requests raw state through the shipped Go BFF.
	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the BFF returns the emulator JSON instead of falling through to the SPA.
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
	// Given: a fake emulator with a namespace-specific raw state endpoint.
	var gotPath string
	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"queue-a": "{\"name\":\"queue-a\"}"})
	}))
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the browser requests one encoded raw-state namespace.
	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state/sqs%2Fqueues", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the BFF preserves the namespace segment when proxying to the emulator.
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
	// Given: a fake emulator returns a selected raw JSON state value.
	var gotPath string
	var gotQuery string
	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"layer"}`))
	}))
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the browser opens one raw state value through the shipped Go BFF.
	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state/lambda%3Alayers?key=us-east-1%2Fdeps%3A0000000001", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the BFF forwards the selected key query and streams the raw response.
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
	// Given: a fake emulator where OVERCAST_DEBUG is disabled.
	emulator := httptest.NewServer(http.NotFoundHandler())
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the browser requests raw state through the BFF.
	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/state", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the BFF matches the dev BFF JSON error instead of returning index.html.
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
	// Given: a fake emulator with the storage diagnostics/advisories endpoint enabled.
	var gotPath string
	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stores":[{"mode":"hybrid","journalMode":"wal"}],"advisories":[]}`))
	}))
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the web UI's Metrics & Health page requests storage diagnostics
	// through the shipped Go BFF.
	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/metrics", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the BFF proxies straight through to /_debug/metrics.
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

// TestDebugMetrics_disabledReturnsJSONError mirrors
// TestDebugState_disabledReturnsJSONError for the metrics/advisories proxy —
// the Metrics & Health page needs the same recognizable "DebugDisabled" error
// the raw-state debugger already surfaces, not a bare "HTTP 404".
func TestDebugMetrics_disabledReturnsJSONError(t *testing.T) {
	// Given: a fake emulator where OVERCAST_DEBUG is disabled.
	emulator := httptest.NewServer(http.NotFoundHandler())
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the browser requests storage diagnostics through the BFF.
	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/debug/metrics", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the BFF returns the same DebugDisabled JSON error shape.
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
