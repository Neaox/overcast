package bff

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// tls_test.go covers URL derivation when Overcast serves HTTPS: the SPA
// bootstrap must hand the browser an https API base URL (an https page cannot
// call an http API — mixed content), and the BFF itself must dial https.

func TestDeriveAPIBaseURL_httpsWhenTLSConfigured(t *testing.T) {
	// Given: both listeners serve TLS (OVERCAST_TLS=auto)
	cfg := UIConfig{APIPort: 4566, TLS: true}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost.overcast.sh:4567"

	// When: the SPA bootstrap URL is derived
	got, known := deriveAPIBaseURL(r, cfg)
	if !known {
		t.Error("expected known endpoint for localhost.overcast.sh:4567 with TLS")
	}

	// Then: the browser is pointed at the https API origin
	if want := "https://localhost.overcast.sh:4566"; got != want {
		t.Errorf("deriveAPIBaseURL = %q, want %q", got, want)
	}
}

func TestDeriveAPIBaseURL_httpsWhenRequestArrivedOverTLS(t *testing.T) {
	// Given: the request itself arrived over TLS (e.g. a TLS-terminating
	// proxy in front of a plain-HTTP Overcast)
	cfg := UIConfig{APIPort: 4566}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:4567"
	r.TLS = &tls.ConnectionState{}

	got, known := deriveAPIBaseURL(r, cfg)
	if !known {
		t.Error("expected known endpoint for localhost:4567 with TLS connection state")
	}
	if want := "https://localhost:4566"; got != want {
		t.Errorf("deriveAPIBaseURL = %q, want %q", got, want)
	}
}

func TestDeriveAPIBaseURL_plainHTTPUnchanged(t *testing.T) {
	// Given: no TLS anywhere — the existing default
	cfg := UIConfig{APIPort: 4566}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:4567"

	got, known := deriveAPIBaseURL(r, cfg)
	if !known {
		t.Error("expected known endpoint for localhost:4567")
	}
	if want := "http://localhost:4566"; got != want {
		t.Errorf("deriveAPIBaseURL = %q, want %q", got, want)
	}
}

func TestNewHandler_tlsSetsHTTPSDefaultAPIURL(t *testing.T) {
	// Given/When: a handler configured for a TLS emulator API
	prevDefault := defaultAPIURL
	t.Cleanup(func() {
		defaultAPIURL = prevDefault
		configureAPITransports(UIConfig{})
	})
	NewHandler(fstest.MapFS{}, fstest.MapFS{}, UIConfig{APIPort: 4566, TLS: true})

	// Then: the BFF's own fallback endpoint is https
	if want := "https://localhost:4566"; defaultAPIURL != want {
		t.Errorf("defaultAPIURL = %q, want %q", defaultAPIURL, want)
	}
	// And: the proxy clients carry a TLS-capable transport
	if bffHTTPClient.Transport == nil || bffStreamingClient.Transport == nil {
		t.Error("expected BFF clients to be given a TLS transport")
	}
}

func TestNewHandler_plainHTTPKeepsDefaultTransport(t *testing.T) {
	prevDefault := defaultAPIURL
	t.Cleanup(func() { defaultAPIURL = prevDefault })
	NewHandler(fstest.MapFS{}, fstest.MapFS{}, UIConfig{APIPort: 4566})

	if want := "http://localhost:4566"; defaultAPIURL != want {
		t.Errorf("defaultAPIURL = %q, want %q", defaultAPIURL, want)
	}
	if bffHTTPClient.Transport != nil || bffStreamingClient.Transport != nil {
		t.Error("expected BFF clients to keep the default transport without TLS")
	}
}
