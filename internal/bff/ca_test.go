package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCACert_proxiesPEMFromEmulator(t *testing.T) {
	const pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	var gotPath string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write([]byte(pem))
	}))
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_overcast/ca.pem" {
		t.Fatalf("expected emulator path /_overcast/ca.pem, got %q", gotPath)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-pem-file" {
		t.Errorf("Content-Type = %q, want application/x-pem-file", got)
	}
	if rec.Body.String() != pem {
		t.Errorf("body = %q, want the served PEM", rec.Body.String())
	}
}

func TestCACert_forwardsNotFound(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.NotFoundHandler())
	defer restore()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
