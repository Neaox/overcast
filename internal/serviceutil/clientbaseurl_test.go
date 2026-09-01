package serviceutil_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// clientbaseurl_test.go — the one rule for client-facing URL bases.
//
// Host: the configured OVERCAST_HOSTNAME when set (the operator's assertion
// that one name resolves for every party), else the caller's own. Port: the
// caller's, always — their request is the only proof of a dialable port, and
// Overcast cannot see its own port mapping — falling back to cfg.Port when the
// request carries none. Scheme: https if Overcast serves TLS or the caller
// arrived over it.
//
// The port is what this contract exists for: with the API port remapped
// (`docker run -p 4652:4566`), a URL carrying cfg.Port is undialable by every
// host-side caller. Measured before the fix: SQS queue URLs carried 4652 while
// AppSync's uris, API Gateway v2's apiEndpoint and the Cognito issuer carried
// 4566. See docs/plans/client-facing-url-minting.md.

func requestOn(host string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = host
	return r
}

func TestClientBaseURL_configuredHostnameOnTheCallersPort(t *testing.T) {
	cfg := &config.Config{Hostname: "localhost.overcast.sh", Port: 4566}

	// A host-side caller reached the published port; that port is the only one
	// they can dial back on.
	if got, want := serviceutil.ClientBaseURL(cfg, requestOn("localhost:4652")),
		"http://localhost.overcast.sh:4652"; got != want {
		t.Errorf("published-port caller: ClientBaseURL = %q, want %q", got, want)
	}
	// A sibling container reached the listen port directly.
	if got, want := serviceutil.ClientBaseURL(cfg, requestOn("172.18.0.2:4566")),
		"http://localhost.overcast.sh:4566"; got != want {
		t.Errorf("container caller: ClientBaseURL = %q, want %q", got, want)
	}
}

// A request with no port, or no request at all, falls back to the configured
// port rather than dropping it.
func TestClientBaseURL_fallsBackToTheConfiguredPort(t *testing.T) {
	cfg := &config.Config{Hostname: "localhost.overcast.sh", Port: 4566}

	if got, want := serviceutil.ClientBaseURL(cfg, requestOn("localhost")),
		"http://localhost.overcast.sh:4566"; got != want {
		t.Errorf("portless Host: ClientBaseURL = %q, want %q", got, want)
	}
	if got, want := serviceutil.ClientBaseURL(cfg, nil),
		"http://localhost.overcast.sh:4566"; got != want {
		t.Errorf("nil request: ClientBaseURL = %q, want %q", got, want)
	}
}

// X-Forwarded-Host wins over Host — that is the origin the client dialled when
// Overcast sits behind the dev proxy.
func TestClientBaseURL_honoursForwardedHost(t *testing.T) {
	cfg := &config.Config{Hostname: "localhost.overcast.sh", Port: 4566}
	r := requestOn("127.0.0.1:4566")
	r.Header.Set("X-Forwarded-Host", "localhost:8080")

	if got, want := serviceutil.ClientBaseURL(cfg, r), "http://localhost.overcast.sh:8080"; got != want {
		t.Errorf("ClientBaseURL = %q, want %q", got, want)
	}
}

// The scheme upgrades to https when either side says so — Overcast's own TLS
// listener, or a caller who arrived over https via a fronting proxy — and
// never downgrades. A synthetic internal request carries no TLS state, which
// is why config must be able to assert it.
func TestClientBaseURL_schemeUpgradesNeverDowngrades(t *testing.T) {
	tls := &config.Config{Hostname: "localhost.overcast.sh", Port: 4566, TLSCertFile: "c.pem", TLSKeyFile: "k.pem"}
	if got, want := serviceutil.ClientBaseURL(tls, requestOn("localhost:4652")),
		"https://localhost.overcast.sh:4652"; got != want {
		t.Errorf("TLS config: ClientBaseURL = %q, want %q", got, want)
	}

	plain := &config.Config{Hostname: "localhost.overcast.sh", Port: 4566}
	r := requestOn("localhost:8443")
	r.Header.Set("X-Forwarded-Proto", "https")
	if got, want := serviceutil.ClientBaseURL(plain, r),
		"https://localhost.overcast.sh:8443"; got != want {
		t.Errorf("https caller: ClientBaseURL = %q, want %q", got, want)
	}
}

// Without a configured hostname the caller's origin is returned verbatim —
// their own host is the only name known to resolve for them.
func TestClientBaseURL_noHostnameEchoesTheCaller(t *testing.T) {
	cfg := &config.Config{Port: 4566}
	if got, want := serviceutil.ClientBaseURL(cfg, requestOn("172.18.0.2:4566")),
		"http://172.18.0.2:4566"; got != want {
		t.Errorf("ClientBaseURL = %q, want %q", got, want)
	}
	if got, want := serviceutil.ClientBaseURL(cfg, nil),
		"http://localhost:4566"; got != want {
		t.Errorf("nil request falls back to ExternalBaseURL: got %q, want %q", got, want)
	}
}

// ClientBaseURLFromOrigin is the same contract for context-shaped callers —
// CloudFormation, Cognito's typed path — which hold a middleware-stamped
// origin rather than an *http.Request. One function, so the four hand-kept
// copies that used to disagree cannot drift again.
func TestClientBaseURLFromOrigin_matchesTheRequestForm(t *testing.T) {
	cfg := &config.Config{Hostname: "localhost.overcast.sh", Port: 4566}

	cases := []struct {
		origin string
		want   string
	}{
		{origin: "http://localhost:4652", want: "http://localhost.overcast.sh:4652"},
		{origin: "http://172.18.0.2:4566", want: "http://localhost.overcast.sh:4566"},
		// The caller's https survives — never a downgrade.
		{origin: "https://localhost:8443", want: "https://localhost.overcast.sh:8443"},
		// No caller at all (background work) falls back to the canonical base.
		{origin: "", want: "http://localhost.overcast.sh:4566"},
	}
	for _, tc := range cases {
		if got := serviceutil.ClientBaseURLFromOrigin(cfg, tc.origin); got != tc.want {
			t.Errorf("FromOrigin(%q) = %q, want %q", tc.origin, got, tc.want)
		}
	}

	// And with no hostname configured, the origin is echoed verbatim.
	bare := &config.Config{Port: 4566}
	if got := serviceutil.ClientBaseURLFromOrigin(bare, "http://10.0.0.9:4652"); got != "http://10.0.0.9:4652" {
		t.Errorf("FromOrigin without hostname = %q, want the origin verbatim", got)
	}
}

// URL minting is a response-path cost, not a routing-path one, but it runs on
// every CreateQueue, DescribeStacks, token mint and discovery fetch — worth
// knowing the number rather than asserting "negligible".
func BenchmarkClientBaseURLFromOrigin(b *testing.B) {
	cfg := &config.Config{Hostname: "localhost.overcast.sh", Port: 4566}
	b.ReportAllocs()
	for b.Loop() {
		serviceutil.ClientBaseURLFromOrigin(cfg, "http://localhost:4652")
	}
}
