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
	if got := rec.Body.String(); got != "{\"error\":\"DebugDisabled\",\"message\":\"OVERCAST_DEBUG must be enabled to inspect raw state.\"}\n" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func testStaticFS() fstest.MapFS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><head></head><body></body></html>")}}
}
