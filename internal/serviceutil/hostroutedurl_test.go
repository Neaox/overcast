package serviceutil_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// TestHostRoutedURL is the specification for the one helper every service uses
// to mint a Host-routed AWS URL:
//
//	{scheme}://{id}.{label}.{region}.{host}[:{port}]{path}
//
// It is built on ClientBaseURL rather than config.ExternalBaseURL because only
// ClientBaseURL falls back to the request port when cfg.Port is unset, and only
// it honours cfg.TLSEnabled() — see docs/plans/harness-representativeness-audit.md
// finding 2 for what the other one degenerates to.
func TestHostRoutedURL(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *config.Config
		reqHost string
		label   string
		id      string
		region  string
		path    string
		want    string
	}{
		{
			name:    "falls back to the request host when no hostname is configured",
			cfg:     &config.Config{},
			reqHost: "127.0.0.1:34349",
			label:   "lambda-url", id: "urlid", region: "us-east-1", path: "/",
			want: "http://urlid.lambda-url.us-east-1.127.0.0.1:34349/",
		},
		{
			name:    "configured hostname wins over the dial address",
			cfg:     &config.Config{Hostname: "localhost.overcast.sh", Port: 4566},
			reqHost: "127.0.0.1:34349",
			label:   "lambda-url", id: "urlid", region: "us-east-1", path: "/",
			want: "http://urlid.lambda-url.us-east-1.localhost.overcast.sh:4566/",
		},
		{
			name:    "configured hostname with no port takes the request port",
			cfg:     &config.Config{Hostname: "localhost"},
			reqHost: "127.0.0.1:34349",
			label:   "execute-api", id: "abc123", region: "us-east-1", path: "",
			want: "http://abc123.execute-api.us-east-1.localhost:34349",
		},
		{
			name:    "TLS yields https",
			cfg:     &config.Config{Hostname: "localhost", Port: 4566, TLSCertFile: "c.pem", TLSKeyFile: "k.pem"},
			reqHost: "127.0.0.1:34349",
			label:   "appsync-api", id: "myapi", region: "us-east-1", path: "/graphql",
			want: "https://myapi.appsync-api.us-east-1.localhost:4566/graphql",
		},
		{
			name:    "api gateway v2 endpoint carries no path",
			cfg:     &config.Config{Hostname: "localhost", Port: 4566},
			reqHost: "127.0.0.1:34349",
			label:   "execute-api", id: "abc123", region: "ap-southeast-2", path: "",
			want: "http://abc123.execute-api.ap-southeast-2.localhost:4566",
		},
		{
			name:    "appsync graphql path",
			cfg:     &config.Config{Hostname: "localhost", Port: 4566},
			reqHost: "127.0.0.1:34349",
			label:   "appsync-api", id: "myapi", region: "eu-west-1", path: "/graphql",
			want: "http://myapi.appsync-api.eu-west-1.localhost:4566/graphql",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a request arriving on reqHost
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Host = tc.reqHost

			// When: a host-routed URL is minted
			got := serviceutil.HostRoutedURL(tc.cfg, r, tc.label, tc.id, tc.region, tc.path)

			// Then: it matches the canonical AWS shape on the reachable base
			if got != tc.want {
				t.Errorf("HostRoutedURL() =\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestHostRoutedURL_emptyRegionOmitsTheSegment covers the region-less form
// Overcast accepts on the routing side (see middleware.ParseHostRoute, where
// Region is optional). Minting one is unusual but must not produce a host with
// an empty label between two dots.
func TestHostRoutedURL_emptyRegionOmitsTheSegment(t *testing.T) {
	// Given: a request and no region
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "127.0.0.1:4566"

	// When: a URL is minted with an empty region
	got := serviceutil.HostRoutedURL(&config.Config{Hostname: "localhost", Port: 4566},
		r, "execute-api", "abc123", "", "")

	// Then: the region segment is omitted rather than left empty
	if want := "http://abc123.execute-api.localhost:4566"; got != want {
		t.Errorf("HostRoutedURL() = %q, want %q", got, want)
	}
}
