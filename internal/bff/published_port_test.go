package bff

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// published_port_test.go covers the two sides of the container port boundary.
//
// Published as `-p 4580:4566`, Overcast listens on 4566 while a browser on the
// host reaches it on 4580. The SPA must be told 4580, but this process must
// keep dialling 4566 — the same value cannot serve both.

func TestDeriveAPIBaseURL_usesBrowserPortWhenRemapped(t *testing.T) {
	// Given the API listens on 4566 but is published on host port 4580
	cfg := UIConfig{APIPort: 4566, BrowserAPIPort: 4580}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:4581" // the remapped UI port

	// When the SPA bootstrap URL is derived
	got, known := deriveAPIBaseURL(r, cfg)
	if !known {
		t.Error("expected known endpoint for localhost:4581 with browser port 4580")
	}

	// Then the browser is pointed at the published port, not the listen port
	if want := "http://localhost:4580"; got != want {
		t.Errorf("deriveAPIBaseURL = %q, want %q", got, want)
	}
}

func TestDeriveAPIBaseURL_nativeBinaryUnchanged(t *testing.T) {
	// Given a native binary: no published port is known
	cfg := UIConfig{APIPort: 4566}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:4567"

	got, known := deriveAPIBaseURL(r, cfg)
	if !known {
		t.Error("expected known endpoint for localhost:4567 with default port")
	}

	// Then behaviour is exactly as before
	if want := "http://localhost:4566"; got != want {
		t.Errorf("deriveAPIBaseURL = %q, want %q", got, want)
	}
}

func TestDeriveAPIBaseURL_customListenPortNativeBinary(t *testing.T) {
	// Given a native binary on a non-default API port
	cfg := UIConfig{APIPort: 5000}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:5001"

	got, known := deriveAPIBaseURL(r, cfg)
	if !known {
		t.Error("expected known endpoint for localhost:5001 with APIPort 5000")
	}
	if want := "http://localhost:5000"; got != want {
		t.Errorf("deriveAPIBaseURL = %q, want %q", got, want)
	}
}

func TestDeriveAPIBaseURL_unknownWhenPortMismatch(t *testing.T) {
	// Given APIPort 4566, a request to port 4571 is not 4566+1, so it must
	// be treated as unknown rather than silently deriving the wrong port.
	cfg := UIConfig{APIPort: 4566}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "localhost:4571"

	got, known := deriveAPIBaseURL(r, cfg)
	if known {
		t.Error("expected unknown endpoint for port 4571 with APIPort 4566")
	}
	if got != "" {
		t.Errorf("deriveAPIBaseURL = %q, want empty string", got)
	}
}

func TestNormalizeEndpoint_rewritesPublishedPortToListenPort(t *testing.T) {
	// Given the BFF knows it is published on 4580 while listening on 4566
	defaultAPIURL = "http://localhost:4566"
	publishedAPIPort = 4580
	t.Cleanup(func() { publishedAPIPort = 0 })

	cases := []struct {
		name string
		in   string
		want string
	}{
		// Loopback endpoints are always rewritten to the internal port.
		{"localhost on the published port", "http://localhost:4580", "http://localhost:4566"},
		{"127.0.0.1 on the published port", "http://127.0.0.1:4580", "http://localhost:4566"},
		{"loopback on an unrelated port", "http://localhost:9999", "http://localhost:4566"},
		// Non-loopback endpoints are left alone — the endpoint switcher needs them.
		{"another host on the same port", "http://example.test:4580", "http://example.test:4580"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeEndpoint(tc.in); got != tc.want {
				t.Errorf("normalizeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeEndpoint_noRewriteWhenPortsMatch(t *testing.T) {
	// Given a native binary or a 1:1 port mapping, nothing is rewritten
	defaultAPIURL = "http://localhost:4566"
	publishedAPIPort = 0

	if got, want := normalizeEndpoint("http://localhost:4566"), "http://localhost:4566"; got != want {
		t.Errorf("normalizeEndpoint = %q, want %q", got, want)
	}
}

func TestResolveEndpoint_normalizesHeader(t *testing.T) {
	// Given the SPA sends the endpoint it was bootstrapped with
	defaultAPIURL = "http://localhost:4566"
	publishedAPIPort = 4580
	t.Cleanup(func() { publishedAPIPort = 0 })

	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	r.Header.Set(endpointHeader, "http://localhost:4580")

	// Then the BFF dials the port it can actually reach
	if got, want := resolveEndpoint(r), "http://localhost:4566"; got != want {
		t.Errorf("resolveEndpoint = %q, want %q", got, want)
	}
}
