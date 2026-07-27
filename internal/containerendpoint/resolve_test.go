package containerendpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/Neaox/overcast/internal/docker"
)

// resolve_test.go covers deriving an address that sibling containers can
// actually dial. "host.docker.internal" is not it: the name only exists on
// Docker Desktop, so on native Linux a container handed that name cannot reach
// Overcast at all.

// fakeNetworkClient stands in for *docker.Client, recording the network it was
// asked to join and returning a canned inspect result.
type fakeNetworkClient struct {
	connectedTo string
	connectErr  error
	inspect     *docker.ContainerInspect
	inspectErr  error
}

func (f *fakeNetworkClient) ConnectNetwork(_ context.Context, networkID, _ string) error {
	f.connectedTo = networkID
	return f.connectErr
}

func (f *fakeNetworkClient) InspectContainer(_ context.Context, _ string) (*docker.ContainerInspect, error) {
	return f.inspect, f.inspectErr
}

// inspectOnNetwork builds an inspect result placing the container on network
// with the given IP.
func inspectOnNetwork(network, ip string) *docker.ContainerInspect {
	ci := &docker.ContainerInspect{}
	ci.NetworkSettings.Networks = map[string]struct {
		IPAddress string `json:"IPAddress"`
	}{network: {IPAddress: ip}}
	return ci
}

func TestNetworkIP_returnsOvercastAddressOnTheSharedNetwork(t *testing.T) {
	// Given: Overcast is itself a container, already reachable on the ECS
	// network at a routable IP.
	dc := &fakeNetworkClient{inspect: inspectOnNetwork("overcast_ecs", "172.18.0.5")}

	// When: its address on that network is looked up.
	got := networkIP(context.Background(), dc, "overcast_ecs", "overcast-abc123")

	// Then: the IP is returned, and Overcast joined the network so sibling
	// containers on it can route to that IP.
	if got != "172.18.0.5" {
		t.Errorf("networkIP() = %q, want %q", got, "172.18.0.5")
	}
	if dc.connectedTo != "overcast_ecs" {
		t.Errorf("connected to %q, want %q", dc.connectedTo, "overcast_ecs")
	}
}

func TestNetworkIP_emptyWhenNotAttachedToTheNetwork(t *testing.T) {
	// Given: an inspect result that does not list the network we asked about.
	dc := &fakeNetworkClient{inspect: inspectOnNetwork("bridge", "172.17.0.2")}

	// When: the address on the ECS network is looked up.
	got := networkIP(context.Background(), dc, "overcast_ecs", "overcast-abc123")

	// Then: nothing is returned, so the caller falls back rather than handing
	// out an IP from the wrong network.
	if got != "" {
		t.Errorf("networkIP() = %q, want empty", got)
	}
}

func TestNetworkIP_emptyWhenInspectFails(t *testing.T) {
	// Given: a Docker daemon that cannot inspect us.
	dc := &fakeNetworkClient{inspectErr: errors.New("no such container")}

	// When: the address is looked up.
	got := networkIP(context.Background(), dc, "overcast_ecs", "overcast-abc123")

	// Then: the caller falls back instead of failing the task start.
	if got != "" {
		t.Errorf("networkIP() = %q, want empty", got)
	}
}

func TestEndpointURL_buildsAnHTTPOrigin(t *testing.T) {
	// Given/When/Then: the resolved host and Overcast's port become an origin
	// suitable for AWS_ENDPOINT_URL.
	if got := endpointURL("172.18.0.1", 4566); got != "http://172.18.0.1:4566" {
		t.Errorf("endpointURL() = %q, want %q", got, "http://172.18.0.1:4566")
	}
}
