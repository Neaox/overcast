package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/hostbridge/trust"
)

// loopbackCert mints a leaf from a throwaway CA — the same call the serve path
// makes in auto mode.
func loopbackCert(t *testing.T) tls.Certificate {
	t.Helper()
	cert, _, err := trust.ServerCertificate(trust.DirFor(t.TempDir()), []string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatalf("mint certificate: %v", err)
	}
	return cert
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// The API and the web UI are two http.Servers over one set of TLS material.
// They must not share the *tls.Config value: http.Server.ServeTLS calls
// http2ConfigureServer, which appends "h2"/"http/1.1" to srv.TLSConfig
// .NextProtos IN PLACE, so a shared pointer is written concurrently by both
// serve goroutines — a data race on the ALPN list each listener advertises.
// Run under -race, this test fails against a shared config and passes against
// the clone cmd_serve.go now hands the UI server.
func TestCloneServerTLSConfigIsolatesTheUIServer(t *testing.T) {
	shared := &tls.Config{Certificates: []tls.Certificate{loopbackCert(t)}}

	apiLn := listenLoopback(t)
	uiLn := listenLoopback(t)

	api := &http.Server{Handler: http.NotFoundHandler(), TLSConfig: shared}
	ui := &http.Server{Handler: http.NotFoundHandler(), TLSConfig: cloneServerTLSConfig(shared)}

	var wg sync.WaitGroup
	wg.Add(2)
	// Launched together, as cmd_serve.go launches them.
	go func() { defer wg.Done(); _ = ui.ServeTLS(uiLn, "", "") }()
	go func() { defer wg.Done(); _ = api.ServeTLS(apiLn, "", "") }()

	// Long enough for both goroutines to get through setupHTTP2_ServeTLS,
	// which is where the write lands.
	time.Sleep(200 * time.Millisecond)
	_ = api.Close()
	_ = ui.Close()
	wg.Wait()

	if api.TLSConfig == ui.TLSConfig {
		t.Error("the API and UI servers hold the same *tls.Config — ServeTLS mutates it")
	}
}

func TestCloneServerTLSConfigPassesNilThrough(t *testing.T) {
	// The TLS-off path relies on a nil config staying nil: cmd_serve.go
	// branches on it to choose Serve over ServeTLS.
	if got := cloneServerTLSConfig(nil); got != nil {
		t.Errorf("cloneServerTLSConfig(nil) = %v, want nil", got)
	}
}
