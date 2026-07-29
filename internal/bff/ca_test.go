package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCACert_proxiesPEMFromEmulator verifies /api/ca.pem passes the daemon's
// CA certificate through verbatim, PEM content type included, so the UI
// origin can also hand out the CA.
func TestCACert_proxiesPEMFromEmulator(t *testing.T) {
	// Given: a fake emulator serving its CA certificate
	const pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	var gotPath string
	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write([]byte(pem)) //nolint:errcheck
	}))
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	// When: the browser requests the CA through the BFF
	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Then: the PEM comes back verbatim from /_overcast/ca.pem
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

// TestCACert_forwardsNotFound verifies a daemon without a CA (TLS off)
// surfaces as 404 through the proxy rather than a masked error.
func TestCACert_forwardsNotFound(t *testing.T) {
	emulator := httptest.NewServer(http.NotFoundHandler())
	defer emulator.Close()

	origClient := bffHTTPClient
	bffHTTPClient = emulator.Client()
	defer func() { bffHTTPClient = origClient }()

	handler := NewHandler(testStaticFS(), nil, UIConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/ca.pem", nil)
	req.Header.Set(endpointHeader, emulator.URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
