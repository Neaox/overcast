package msk

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
)

// GetBootstrapBrokers used to return ExternalHostname():hostPort to every
// caller. Inside a container that name resolves to Overcast — a process that
// serves no Kafka on that port — so every containerized consumer failed. The
// address is now per-caller, on the same rule every other data-plane endpoint
// follows.
func TestBootstrapBrokerString_perCaller(t *testing.T) {
	const arn = "arn:aws:kafka:us-east-1:000000000000:cluster/events/abc-123"

	cases := []struct {
		name     string
		hostname string
		origin   string
		addr     string
		cluster  *Cluster
		want     string
	}{
		{
			name:     "a sibling container dials the broker hostname on the engine port",
			hostname: "localhost.overcast.sh",
			origin:   "http://localhost.overcast.sh:4566",
			addr:     "172.19.0.4",
			cluster:  &Cluster{ClusterArn: arn, HostPort: 49092},
			want:     "events.us-east-1.kafka.localhost.overcast.sh:9092",
		},
		{
			name:     "the host gets the published port, on a base that resolves",
			hostname: "localhost.overcast.sh",
			origin:   "http://localhost.overcast.sh:4566",
			addr:     "127.0.0.1:52000",
			cluster:  &Cluster{ClusterArn: arn, HostPort: 49092},
			want:     "events.us-east-1.kafka.localhost.overcast.sh:49092",
		},
		{
			// *.localhost does not resolve on Windows, so a name would strand
			// host callers there.
			name:     "the host gets loopback when the base carries no wildcard DNS",
			hostname: "localhost",
			origin:   "http://localhost:4566",
			addr:     "127.0.0.1:52000",
			cluster:  &Cluster{ClusterArn: arn, HostPort: 49092},
			want:     "127.0.0.1:49092",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{cfg: &config.Config{Region: "us-east-1", Hostname: tc.hostname, MSKPortBase: 49092}}

			ctx := middleware.ContextWithClientEndpoint(context.Background(), tc.origin)
			ctx = middleware.ContextWithClientAddr(ctx, tc.addr)

			if got := h.bootstrapBrokerString(ctx, tc.cluster); got != tc.want {
				t.Fatalf("bootstrapBrokerString = %q, want %q", got, tc.want)
			}
		})
	}
}

// With no container behind the cluster there is nothing to dial. Reporting the
// configured port base is better than an empty string only when Docker is known
// to be unavailable — otherwise the container is still starting and any address
// would be a guess.
func TestBootstrapBrokerString_withoutAContainer(t *testing.T) {
	h := &Handler{cfg: &config.Config{Region: "us-east-1", Hostname: "localhost", MSKPortBase: 49092}}
	ctx := context.Background()

	if got := h.bootstrapBrokerString(ctx, &Cluster{ClusterArn: "arn:aws:kafka:us-east-1:0:cluster/e/1"}); got != "127.0.0.1:49092" {
		t.Fatalf("Docker unavailable: got %q, want the port base on loopback", got)
	}

	h.dockerReady.Store(true)
	if got := h.bootstrapBrokerString(ctx, &Cluster{ClusterArn: "arn:aws:kafka:us-east-1:0:cluster/e/1"}); got != "" {
		t.Fatalf("container still starting: got %q, want empty", got)
	}
}

func TestClusterNameFromARN(t *testing.T) {
	cases := map[string]string{
		"arn:aws:kafka:us-east-1:000000000000:cluster/events/abc-123": "events",
		"arn:aws:kafka:us-east-1:000000000000:cluster/my-app/x":       "my-app",
		"not-an-arn": "",
	}
	for arn, want := range cases {
		if got := clusterNameFromARN(arn); got != want {
			t.Errorf("clusterNameFromARN(%q) = %q, want %q", arn, got, want)
		}
	}
}
