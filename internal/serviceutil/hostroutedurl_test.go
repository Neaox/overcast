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
			// The hostname is the operator's assertion that one name resolves
			// for every party; the port is the caller's, because theirs is the
			// only port proven dialable — cfg.Port is undialable from the host
			// whenever the API port is remapped. See
			// docs/plans/client-facing-url-minting.md.
			name:    "configured hostname wins over the dial address, the caller's port over the configured one",
			cfg:     &config.Config{Hostname: "localhost.overcast.sh", Port: 4566},
			reqHost: "127.0.0.1:34349",
			label:   "lambda-url", id: "urlid", region: "us-east-1", path: "/",
			want: "http://urlid.lambda-url.us-east-1.localhost.overcast.sh:34349/",
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
			want: "https://myapi.appsync-api.us-east-1.localhost:34349/graphql",
		},
		{
			name:    "api gateway v2 endpoint carries no path",
			cfg:     &config.Config{Hostname: "localhost", Port: 4566},
			reqHost: "127.0.0.1:34349",
			label:   "execute-api", id: "abc123", region: "ap-southeast-2", path: "",
			want: "http://abc123.execute-api.ap-southeast-2.localhost:34349",
		},
		{
			name:    "appsync graphql path",
			cfg:     &config.Config{Hostname: "localhost", Port: 4566},
			reqHost: "127.0.0.1:34349",
			label:   "appsync-api", id: "myapi", region: "eu-west-1", path: "/graphql",
			want: "http://myapi.appsync-api.eu-west-1.localhost:34349/graphql",
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

// TestSupportsHostRouting is the specification for the gate on the fields that
// have a path-style alternative Overcast also serves — AppSync's uris, and the
// console's REST v1 invoke URLs. Minting a host-routed URL on a base that
// cannot carry a subdomain hands the caller a name their resolver will not
// answer, which is worse than the shape difference the fallback costs.
//
// web/src/lib/host-routed-url.ts states the same rule for the console; the two
// must agree, or the console and the API disagree about the same deployment.
func TestSupportsHostRouting(t *testing.T) {
	cases := []struct {
		name string
		base string
		want bool
	}{
		{"wildcard-DNS domain", "http://localhost.overcast.sh:4566", true},
		{"other wildcard-DNS domain", "http://localhost.localstack.cloud:4566", true},
		{"operator-configured multi-label hostname", "https://overcast.example.test", true},
		{"bare localhost has no *.localhost DNS on Windows or macOS", "http://localhost:4566", false},
		{"docker service alias is single-label", "http://overcast:4566", false},
		{"IPv4 literal cannot carry a subdomain", "http://127.0.0.1:4566", false},
		{"IPv6 literal cannot carry a subdomain", "http://[::1]:4566", false},
		{"unparseable base", "://nope", false},
		{"empty base", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serviceutil.SupportsHostRouting(tc.base); got != tc.want {
				t.Errorf("SupportsHostRouting(%q) = %v, want %v", tc.base, got, tc.want)
			}
		})
	}
}

// TestSupportsHostRouting_coversEveryWildcardDomain fails if a domain is added
// to config.WildcardDNSDomains that the gate would then reject — the whole
// point of that list is that every subdomain of those names resolves.
func TestSupportsHostRouting_coversEveryWildcardDomain(t *testing.T) {
	for _, domain := range config.WildcardDNSDomains {
		if !serviceutil.SupportsHostRouting("http://" + domain + ":4566") {
			t.Errorf("SupportsHostRouting rejects wildcard-DNS domain %q", domain)
		}
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
