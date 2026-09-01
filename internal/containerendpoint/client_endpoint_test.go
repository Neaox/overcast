package containerendpoint

import (
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
)

// AWS_ENDPOINT_URL carrying a raw address costs containers two things: it dies
// when Overcast's container is recreated on a new address, and it forces every
// SDK to path-style, so no virtual-hosted URL an SDK derives from it resolves.
func TestClientEndpoint_PrefersASplitHorizonName(t *testing.T) {
	m := New(&config.Config{Port: 4566}, "http://172.18.0.2:4566")

	if got, want := m.ClientEndpoint(), "http://localhost.overcast.sh:4566"; got != want {
		t.Errorf("ClientEndpoint() = %q, want %q", got, want)
	}
	// The address form is still what /etc/hosts and --dns point at, or the name
	// would have nothing to resolve to.
	if got, want := m.Endpoint(), "http://172.18.0.2:4566"; got != want {
		t.Errorf("Endpoint() = %q, want the address %q", got, want)
	}
	if got := m.ExtraHosts(); len(got) == 0 || got[0] != "localhost.overcast.sh:172.18.0.2" {
		t.Errorf("ExtraHosts() = %v, want the name pinned to the address", got)
	}
}

// An operator who set OVERCAST_HOSTNAME chose the name they want advertised.
func TestClientEndpoint_UsesConfiguredHostname(t *testing.T) {
	m := New(&config.Config{Port: 4566, Hostname: "overcast.test"}, "http://172.18.0.2:4566")

	if got, want := m.ClientEndpoint(), "http://overcast.test:4566"; got != want {
		t.Errorf("ClientEndpoint() = %q, want %q", got, want)
	}
}

// "localhost" means the container itself, so it can never be advertised — the
// built-in split-horizon name is used instead. This is the default test and
// dev configuration, so getting it wrong would break everything.
func TestClientEndpoint_IgnoresLoopbackHostname(t *testing.T) {
	m := New(&config.Config{Port: 4566, Hostname: "localhost"}, "http://172.18.0.2:4566")

	if got, want := m.ClientEndpoint(), "http://localhost.overcast.sh:4566"; got != want {
		t.Errorf("ClientEndpoint() = %q, want %q", got, want)
	}
}

// With nothing to pin a name to, the address is all a container can use.
func TestClientEndpoint_FallsBackToTheAddress(t *testing.T) {
	if got := New(&config.Config{Port: 4566}, "").ClientEndpoint(); got != "" {
		t.Errorf("ClientEndpoint() = %q, want empty for an inert mapper", got)
	}
	// A hostname endpoint (the host.docker.internal fallback) is pinned to
	// host-gateway, so a split-horizon name works there too.
	m := New(&config.Config{Port: 4566}, "http://host.docker.internal:4566")
	if got, want := m.ClientEndpoint(), "http://localhost.overcast.sh:4566"; got != want {
		t.Errorf("ClientEndpoint() = %q, want %q", got, want)
	}
}

// An invoke payload carrying a host-minted queue URL is the last route by which
// an unreachable origin gets into a container: environment is rewritten, and
// URLs the container mints for itself are already per-caller, but a payload was
// passed through untouched. AWS SDKs resolve the SQS endpoint from the QueueUrl,
// so the container's client dialled its own loopback.
func TestRewriteBytes_repointsHostMintedURLsInPayloads(t *testing.T) {
	m := New(&config.Config{Port: 4566}, "http://172.18.0.2:4566").WithPublishedPort(4623)

	payload := []byte(`{"queueUrl":"http://localhost:4623/000000000000/orders","note":"keep me"}`)
	want := `{"queueUrl":"http://172.18.0.2:4566/000000000000/orders","note":"keep me"}`
	if got := string(m.RewriteBytes(payload)); got != want {
		t.Errorf("RewriteBytes() = %s, want %s", got, want)
	}
}

func TestRewriteBytes_leavesUnrelatedPayloadsUntouched(t *testing.T) {
	m := New(&config.Config{Port: 4566}, "http://172.18.0.2:4566").WithPublishedPort(4623)

	for _, payload := range []string{
		"",
		`{"url":"https://api.example.com/v1"}`,
		`{"url":"http://localhost:3000/callback"}`, // a port Overcast does not serve
		`{"plain":"no urls here"}`,
	} {
		if got := string(m.RewriteBytes([]byte(payload))); got != payload {
			t.Errorf("RewriteBytes(%s) = %s, want it untouched", payload, got)
		}
	}
	// An untouched payload is returned as the same backing array, not a copy.
	in := []byte(`{"plain":"no urls here"}`)
	if out := m.RewriteBytes(in); &out[0] != &in[0] {
		t.Error("RewriteBytes copied a payload it did not change")
	}
}

// A nil mapper is inert rather than a panic, matching the rest of the type.
func TestRewriteBytes_nilMapper(t *testing.T) {
	var m *Mapper
	in := []byte(`{"queueUrl":"http://localhost:4623/q"}`)
	if got := string(m.RewriteBytes(in)); got != string(in) {
		t.Errorf("RewriteBytes on a nil mapper = %s, want it untouched", got)
	}
}
