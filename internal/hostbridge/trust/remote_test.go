package trust

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveCA returns an httptest handler serving pem at CAPemPath.
func serveCA(pem []byte) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(CAPemPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(pem) //nolint:errcheck
	})
	return mux
}

func TestFetchRemoteCA_validCA(t *testing.T) {
	// Given: a daemon serving its CA certificate
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	srv := httptest.NewServer(serveCA(ca.CertPEM))
	t.Cleanup(srv.Close)

	// When: the CA is fetched
	pem, cert, err := FetchRemoteCA(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchRemoteCA: %v", err)
	}

	// Then: the fetched certificate is the daemon's CA
	if string(pem) != string(ca.CertPEM) {
		t.Error("fetched PEM does not match the served CA")
	}
	if cert.SerialNumber.Cmp(ca.Cert.SerialNumber) != 0 {
		t.Error("parsed certificate is not the served CA")
	}
}

func TestFetchRemoteCA_httpsDaemonViaHTTPEndpoint(t *testing.T) {
	// Given: a TLS-serving daemon (the OVERCAST_TLS=auto case), but the user
	// passed the plain-http endpoint (the one printed in docs everywhere)
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	srv := httptest.NewTLSServer(serveCA(ca.CertPEM))
	t.Cleanup(srv.Close)
	httpURL := strings.Replace(srv.URL, "https://", "http://", 1)

	// When: the CA is fetched via the http:// spelling
	pem, _, err := FetchRemoteCA(context.Background(), httpURL)

	// Then: the fetch auto-negotiates to https (unverified — the payload is
	// validated instead) and succeeds
	if err != nil {
		t.Fatalf("FetchRemoteCA against a TLS daemon via http endpoint: %v", err)
	}
	if string(pem) != string(ca.CertPEM) {
		t.Error("fetched PEM does not match the served CA")
	}
}

func TestFetchRemoteCA_rejectsNonCA(t *testing.T) {
	// Given: a server handing out a leaf (server) certificate, not a CA
	dir := t.TempDir()
	if _, _, err := ServerCertificate(dir, []string{"localhost"}); err != nil {
		t.Fatalf("ServerCertificate: %v", err)
	}
	leafPEM, err := os.ReadFile(filepath.Join(dir, leafCertFile))
	if err != nil {
		t.Fatalf("read leaf: %v", err)
	}
	srv := httptest.NewServer(serveCA(leafPEM))
	t.Cleanup(srv.Close)

	// When + Then: the fetch refuses to hand back a non-CA certificate
	if _, _, err := FetchRemoteCA(context.Background(), srv.URL); err == nil {
		t.Error("expected an error for a non-CA certificate, got nil")
	}
}

func TestFetchRemoteCA_rejectsGarbage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>not a certificate</html>")) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	if _, _, err := FetchRemoteCA(context.Background(), srv.URL); err == nil {
		t.Error("expected an error for a non-PEM response, got nil")
	}
}

func TestFetchRemoteCA_404MeansNoTLS(t *testing.T) {
	// Given: a daemon that is not serving TLS (no CA minted → 404)
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	// When + Then: the error explains the daemon has no CA
	_, _, err := FetchRemoteCA(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "OVERCAST_TLS") {
		t.Errorf("404 error should point at OVERCAST_TLS, got: %v", err)
	}
}

func TestLoopbackEndpoint(t *testing.T) {
	cases := []struct {
		endpoint string
		want     bool
	}{
		{"http://localhost:4566", true},
		{"http://127.0.0.1:4566", true},
		{"https://[::1]:4566", true},
		{"http://LOCALHOST:4566", true},
		{"http://localhost.overcast.sh:4566", false}, // resolves to 127.0.0.1, but only via DNS we did not do
		{"http://build-host:4566", false},
		{"http://192.168.1.50:4566", false},
	}
	for _, tc := range cases {
		t.Run(tc.endpoint, func(t *testing.T) {
			got, err := LoopbackEndpoint(tc.endpoint)
			if err != nil {
				t.Fatalf("LoopbackEndpoint: %v", err)
			}
			if got != tc.want {
				t.Errorf("LoopbackEndpoint(%q) = %v, want %v", tc.endpoint, got, tc.want)
			}
		})
	}
}

func TestRemoteDirFor_deterministicAndDistinct(t *testing.T) {
	dataDir := t.TempDir()

	a1 := RemoteDirFor(dataDir, "http://localhost:4566")
	a2 := RemoteDirFor(dataDir, "http://localhost:4566")
	b := RemoteDirFor(dataDir, "http://localhost:4580")

	if a1 != a2 {
		t.Errorf("RemoteDirFor is not deterministic: %q != %q", a1, a2)
	}
	if a1 == b {
		t.Error("RemoteDirFor collides for different endpoints")
	}
	// Never the local CA directory — a fetched CA must not be mistaken for a
	// locally-minted one.
	if a1 == DirFor(dataDir) || b == DirFor(dataDir) {
		t.Error("RemoteDirFor must not collide with the local CA directory")
	}
	// Scheme must not matter: http vs https name the same daemon.
	if RemoteDirFor(dataDir, "https://localhost:4566") != a1 {
		t.Error("RemoteDirFor should be scheme-agnostic")
	}
}

func TestSaveRemoteCA_roundTripsThroughTrustStore(t *testing.T) {
	// Given: a fetched CA PEM
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	dataDir := t.TempDir()
	dir := RemoteDirFor(dataDir, "http://localhost:4566")

	// When: it is cached locally
	if err := SaveRemoteCA(dir, ca.CertPEM); err != nil {
		t.Fatalf("SaveRemoteCA: %v", err)
	}

	// Then: the trust backends can load it as an anchor (certificate only —
	// there is no key, and there must never be one)
	cert, _, absent, err := loadTrustAnchor(dir)
	if err != nil || absent {
		t.Fatalf("loadTrustAnchor(remote dir) = absent=%v err=%v, want a certificate", absent, err)
	}
	if cert.SerialNumber.Cmp(ca.Cert.SerialNumber) != 0 {
		t.Error("cached anchor is not the fetched CA")
	}
	if _, err := os.Stat(filepath.Join(dir, caKeyFile)); !os.IsNotExist(err) {
		t.Error("a remote CA cache must never contain a private key file")
	}
}
