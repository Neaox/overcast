package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// TestCACertHandler_404WithoutCA verifies the endpoint reports "no CA" when
// the daemon has never minted one (TLS off).
func TestCACertHandler_404WithoutCA(t *testing.T) {
	// Given: a data dir with no CA
	handler := caCertHandler(&config.Config{DataDir: t.TempDir()})
	req := httptest.NewRequest(http.MethodGet, "/_overcast/ca.pem", nil)
	rec := httptest.NewRecorder()

	// When
	handler(rec, req)

	// Then
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "OVERCAST_TLS") {
		t.Errorf("404 body should point at OVERCAST_TLS, got %q", rec.Body.String())
	}
}

// TestCACertHandler_servesPublicCertOnly verifies the endpoint serves the CA
// certificate as PEM — and never anything resembling key material.
func TestCACertHandler_servesPublicCertOnly(t *testing.T) {
	// Given: a minted CA under the data dir
	dataDir := t.TempDir()
	ca, err := trust.LoadOrCreateCA(trust.DirFor(dataDir))
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	handler := caCertHandler(&config.Config{DataDir: dataDir})
	req := httptest.NewRequest(http.MethodGet, "/_overcast/ca.pem", nil)
	rec := httptest.NewRecorder()

	// When
	handler(rec, req)

	// Then: 200, PEM content type, and exactly the CA certificate
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/x-pem-file" {
		t.Errorf("Content-Type = %q, want application/x-pem-file", got)
	}
	if rec.Body.String() != string(ca.CertPEM) {
		t.Error("served body does not match the CA certificate PEM")
	}
	cert, err := trust.ParseCertificatePEM(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("served body does not parse as a certificate: %v", err)
	}
	if !cert.IsCA {
		t.Error("served certificate is not a CA certificate")
	}
	if strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatal("served body contains key material")
	}
}

// TestCACertHandler_refusesCorruptFile verifies a corrupt on-disk file is
// never served into anyone's trust store.
func TestCACertHandler_refusesCorruptFile(t *testing.T) {
	// Given: garbage where the CA certificate should be
	dataDir := t.TempDir()
	caDir := trust.DirFor(dataDir)
	if err := os.MkdirAll(caDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(caDir, trust.CACertFile), []byte("not a pem"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	handler := caCertHandler(&config.Config{DataDir: dataDir})
	rec := httptest.NewRecorder()

	// When
	handler(rec, httptest.NewRequest(http.MethodGet, "/_overcast/ca.pem", nil))

	// Then
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
