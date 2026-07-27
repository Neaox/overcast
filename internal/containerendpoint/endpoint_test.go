package containerendpoint

import (
	"slices"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/config"
)

// endpoint_test.go covers the two mechanisms that keep AWS resource URLs
// dialable from inside containers Overcast starts.
//
// Background: AWS SDKs resolve the SQS endpoint from the QueueUrl rather than
// from client configuration (@aws-sdk/middleware-sdk-sqs replaces the resolved
// endpoint with the QueueUrl's origin; .NET and Java v1 use the queue URL as
// the request URI outright). AWS_ENDPOINT_URL does not protect against this —
// it is resolved through the endpoint ruleset, never as config.endpoint. So a
// queue URL minted on the host as http://localhost:4566/... and handed to a
// container through its environment sends that container's SQS client to its
// own loopback, not to Overcast.

func TestRewriteURLs_repointsLoopbackOvercastOrigins(t *testing.T) {
	// Given: a mapper for an Overcast reachable at a container-routable IP.
	m := New(&config.Config{Port: 4566}, "http://172.18.0.1:4566")

	// When/Then: loopback origins on Overcast's port are re-pointed wherever
	// they appear in the value — bare, in a list, or inside a JSON blob.
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "bare queue URL",
			value: "http://localhost:4566/000000000000/orders",
			want:  "http://172.18.0.1:4566/000000000000/orders",
		},
		{
			name:  "127.0.0.1 origin",
			value: "http://127.0.0.1:4566/000000000000/orders-dlq",
			want:  "http://172.18.0.1:4566/000000000000/orders-dlq",
		},
		{
			name:  "comma-separated list",
			value: "http://localhost:4566/000000000000/a,http://localhost:4566/000000000000/b",
			want:  "http://172.18.0.1:4566/000000000000/a,http://172.18.0.1:4566/000000000000/b",
		},
		{
			name:  "embedded in JSON",
			value: `{"queue":"http://localhost:4566/000000000000/c"}`,
			want:  `{"queue":"http://172.18.0.1:4566/000000000000/c"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.RewriteURLs(tc.value); got != tc.want {
				t.Errorf("RewriteURLs(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestRewriteURLs_leavesNonOvercastURLsAlone(t *testing.T) {
	// Given: a mapper for an Overcast serving port 4566.
	m := New(&config.Config{Port: 4566}, "http://172.18.0.1:4566")

	// When/Then: a third-party API, a loopback URL on a port Overcast does not
	// serve, and a split-horizon hostname are all left as the user set them.
	for _, value := range []string{
		"https://api.example.com/v1",
		"http://localhost:3000/callback",
		"http://localhost.overcast.sh:4566/000000000000/orders",
	} {
		if got := m.RewriteURLs(value); got != value {
			t.Errorf("RewriteURLs(%q) = %q, want it untouched", value, got)
		}
	}
}

func TestRewriteURLs_inertWithoutAnEndpoint(t *testing.T) {
	// Given: a mapper built before the container-reachable endpoint is known.
	m := New(&config.Config{Port: 4566}, "")

	// When: a loopback URL is rewritten.
	value := "http://localhost:4566/000000000000/orders"

	// Then: it is left alone rather than blanked out.
	if got := m.RewriteURLs(value); got != value {
		t.Errorf("RewriteURLs(%q) = %q, want it untouched", value, got)
	}
}

func TestExtraHosts_mapsSplitHorizonNamesToOvercast(t *testing.T) {
	// Given: Overcast running in a container, reachable by IP from the
	// container network.
	m := New(&config.Config{Port: 4566}, "http://172.18.0.1:4566")

	// When: the container's /etc/hosts entries are computed.
	got := m.ExtraHosts()

	// Then: every split-horizon name shadows its public 127.0.0.1 record with
	// Overcast's address, so one URL is dialable from host and container alike.
	for _, want := range []string{
		"localhost.overcast.sh:172.18.0.1",
		"localhost.localstack.cloud:172.18.0.1",
		"localhost.floci.io:172.18.0.1",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("ExtraHosts() = %v, missing %q", got, want)
		}
	}
}

func TestExtraHosts_usesHostGatewayWhenOvercastRunsOnHost(t *testing.T) {
	// Given: Overcast running on the host rather than in a container.
	m := New(&config.Config{Port: 4566}, "http://host.docker.internal:4566")

	// When: the container's /etc/hosts entries are computed.
	got := m.ExtraHosts()

	// Then: names resolve through Docker's host-gateway rather than a name the
	// container's resolver would have to look up.
	if !slices.Contains(got, "localhost.overcast.sh:host-gateway") {
		t.Errorf("ExtraHosts() = %v, want localhost.overcast.sh:host-gateway", got)
	}
}

func TestExtraHosts_includesConfiguredHostnameAndOverrides(t *testing.T) {
	// Given: a deployment with its own external hostname and an extra
	// split-horizon domain configured.
	m := New(&config.Config{
		Port:              4566,
		Hostname:          "overcast.local",
		SplitHorizonHosts: []string{"localhost.example.test"},
	}, "http://172.18.0.1:4566")

	// When: the container's /etc/hosts entries are computed.
	got := m.ExtraHosts()

	// Then: both the configured hostname and the extra domain resolve to
	// Overcast, so URLs minted under either are dialable inside the container.
	for _, want := range []string{"overcast.local:172.18.0.1", "localhost.example.test:172.18.0.1"} {
		if !slices.Contains(got, want) {
			t.Errorf("ExtraHosts() = %v, missing %q", got, want)
		}
	}
}

func TestExtraHosts_skipsLoopbackAndIPHostnames(t *testing.T) {
	// Given: a deployment whose external hostname is "localhost" — a name no
	// /etc/hosts entry can usefully shadow — alongside a bare IP override.
	m := New(&config.Config{
		Port:              4566,
		Hostname:          "localhost",
		SplitHorizonHosts: []string{"10.1.2.3"},
	}, "http://172.18.0.1:4566")

	// When: the container's /etc/hosts entries are computed.
	got := m.ExtraHosts()

	// Then: no entry claims localhost (which would break the container's own
	// loopback resolution) or a bare IP (already routable, or already wrong).
	for _, entry := range got {
		name, _, _ := strings.Cut(entry, ":")
		if name == "localhost" || name == "127.0.0.1" || name == "10.1.2.3" {
			t.Errorf("ExtraHosts() = %v, must not remap %q", got, name)
		}
	}
}

func TestExtraHosts_emptyWhenEndpointIsUnusable(t *testing.T) {
	// Given: a mapper with no endpoint resolved yet.
	m := New(&config.Config{Port: 4566}, "")

	// When/Then: no /etc/hosts entries are produced, rather than entries
	// pointing nowhere.
	if got := m.ExtraHosts(); len(got) != 0 {
		t.Errorf("ExtraHosts() = %v, want none", got)
	}
}
