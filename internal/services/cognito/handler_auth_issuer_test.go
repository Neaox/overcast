package cognito

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Neaox/overcast/internal/config"
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
// serviceutil.ClientBaseURL already resolves hostname, port and scheme together
// from cfg — routing issuerURL through it fixes both defects at once, and is
// what every other client-facing URL in the codebase does.
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
			want: "http://overcast.local:4566/us-east-1/us-east-1_abc123",
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
			want: "https://overcast.local:4566/us-east-1/us-east-1_abc123",
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
