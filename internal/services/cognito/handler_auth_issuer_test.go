package cognito

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// TestIssuerURL_honoursConfiguredHostnameAndTLS covers the two inputs issuerURL
// ignores: the configured external hostname, and the scheme the server actually
// serves.
//
// issuerURL is "http://" + r.Host + "/" + region + "/" + poolID
// (handler_auth.go:307). Both defects are invisible to the integration suite:
// the hostname one because the caller's Host and the configured hostname
// coincide unless they are deliberately made to differ, and the scheme one
// because no test anywhere in the repo sets OVERCAST_TLS_CERT/OVERCAST_TLS_KEY,
// so cfg.TLSEnabled() is false in every server the suite builds and the https
// branches of serviceutil.ClientBaseURL and config.ExternalBaseURL never run
// outside internal/config's own unit tests.
//
// The scheme is the sharper half. With TLS on, Overcast serves the JWKS endpoint
// over https, but every token it mints claims an http issuer, and jwks_uri in
// the discovery document points at http too. An OIDC client either rejects the
// issuer or tries to fetch signing keys over a scheme the server does not
// answer, and in both cases token validation fails rather than degrades.
//
// serviceutil.ClientBaseURL resolves hostname and scheme from cfg — routing
// issuerURL through it fixes both defects at once, and is what every other
// client-facing URL in the codebase does.
//
// The port, deliberately, is the *caller's*, not cfg.Port. OIDC Discovery 1.0
// §4.3 requires the issuer to be byte-identical to the URL the configuration
// was retrieved from: a client that reached Overcast on a remapped port
// fetches discovery on that port, so an issuer carrying cfg.Port fails
// spec-compliant validation for exactly that caller, and points jwks_uri at a
// port they cannot dial. Overcast's own validation is port-agnostic
// (TestPoolIDFromIssuer_ignoresTheOrigin). See
// docs/plans/client-facing-url-minting.md.
func TestIssuerURL_honoursConfiguredHostnameAndTLS(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "configured hostname wins over the dial address",
			cfg: &config.Config{
				Region:   "us-east-1",
				Hostname: "overcast.local",
				Port:     4566,
			},
			want: "http://overcast.local:39783/us-east-1_abc123",
		},
		{
			name: "TLS makes the issuer https",
			cfg: &config.Config{
				Region:      "us-east-1",
				Hostname:    "overcast.local",
				Port:        4566,
				TLSCertFile: "/tmp/cert.pem",
				TLSKeyFile:  "/tmp/key.pem",
			},
			want: "https://overcast.local:39783/us-east-1_abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a service with that configuration.
			s := &Service{cfg: tt.cfg}

			// When: an issuer URL is built for a request that arrived on the
			// server's dial address rather than its advertised hostname.
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:39783/", nil)
			got := s.issuerURL(req, "us-east-1_abc123")

			// Then: the issuer reflects the configuration, not the dial address.
			if got != tt.want {
				t.Errorf("issuerURL = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPoolIDFromIssuer_ignoresTheOrigin guards the accommodation that makes
// per-caller issuer minting safe: Overcast validates its own tokens by reading
// the pool ID from the issuer's *path*, never by comparing the issuer string
// literally. Two callers who reached Overcast on different ports mint tokens
// with different iss strings for the same pool, and both must validate.
//
// Do not "tidy" ValidateCognitoToken into a literal issuer comparison — that
// breaks every token the moment the API port is remapped. See
// docs/plans/client-facing-url-minting.md, "Per-service requirements".
func TestPoolIDFromIssuer_ignoresTheOrigin(t *testing.T) {
	for _, iss := range []string{
		"http://localhost.overcast.sh:4566/us-east-1_abc123",
		"http://localhost.overcast.sh:4652/us-east-1_abc123", // published port
		"http://localhost:39783/us-east-1_abc123",            // harness port
		"https://overcast.local:8443/us-east-1_abc123",       // TLS proxy
	} {
		got, err := poolIDFromIssuer(iss)
		if err != nil {
			t.Errorf("poolIDFromIssuer(%q): %v", iss, err)
			continue
		}
		if got != "us-east-1_abc123" {
			t.Errorf("poolIDFromIssuer(%q) = %q, want %q", iss, got, "us-east-1_abc123")
		}
	}
}
