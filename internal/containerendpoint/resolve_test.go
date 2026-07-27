package containerendpoint

import (
	"context"
	"errors"
	"net"
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

func TestScanInterfaceIP_skipsLinkLocalAutoconfiguredAddresses(t *testing.T) {
	// Given: a host whose interface list leads with a 169.254/16 APIPA address —
	// what Windows and Linux assign to an adapter that failed DHCP, and what a
	// disconnected Ethernet port leaves behind. Measured on a real Docker
	// Desktop host: containers cannot route to 169.254/16 at all.
	addrs := []net.Addr{
		ipNet("169.254.119.185"),
		ipNet("169.254.101.17"),
		ipNet("192.168.8.19"),
	}

	// When: the host address is scanned for.
	got := scanInterfaceIP(addrs)

	// Then: the routable address is chosen, not the dead autoconfigured one.
	if got != "192.168.8.19" {
		t.Errorf("scanInterfaceIP() = %q, want %q", got, "192.168.8.19")
	}
}

func TestScanInterfaceIP_skipsLoopbackAndIPv6(t *testing.T) {
	// Given: an interface list of addresses no sibling container can use.
	addrs := []net.Addr{ipNet("127.0.0.1"), ipNet("::1"), ipNet("fe80::1")}

	// When: the host address is scanned for.
	got := scanInterfaceIP(addrs)

	// Then: nothing is claimed, so the caller falls back.
	if got != "" {
		t.Errorf("scanInterfaceIP() = %q, want empty", got)
	}
}

func TestResolve_containerModeWithoutASharedNetwork(t *testing.T) {
	// Given: Overcast is containerised but cannot find its address on the task
	// network — a custom --hostname makes InspectContainer miss.
	dc := &fakeNetworkClient{inspectErr: errors.New("no such container")}

	// When: the container-reachable address is resolved.
	got := resolve(context.Background(), dc, "overcast_ecs", 4566, nil,
		func() bool { return true },
		func() string { return "172.17.0.2" }) // our own IP on an unrelated network

	// Then: the host alias is used rather than our own container IP, which a
	// sibling on a different network cannot route to.
	want := "http://host.docker.internal:4566"
	if got != want {
		t.Errorf("resolve() = %q, want %q", got, want)
	}
}

func TestResolve_hostMode(t *testing.T) {
	// Given: Overcast runs on the host, which has a routable address.
	dc := &fakeNetworkClient{inspectErr: errors.New("not a container")}

	// When: the container-reachable address is resolved.
	got := resolve(context.Background(), dc, "overcast_ecs", 4566, nil,
		func() bool { return false },
		func() string { return "192.168.8.19" })

	// Then: the host address is used.
	want := "http://192.168.8.19:4566"
	if got != want {
		t.Errorf("resolve() = %q, want %q", got, want)
	}
}

func TestResolve_hostModeWithoutARoutableAddress(t *testing.T) {
	// Given: Overcast runs on a host with no usable interface address.
	dc := &fakeNetworkClient{inspectErr: errors.New("not a container")}

	// When: the container-reachable address is resolved.
	got := resolve(context.Background(), dc, "overcast_ecs", 4566, nil,
		func() bool { return false },
		func() string { return "" })

	// Then: the host alias is the last resort — paired with an /etc/hosts entry
	// by Mapper.ExtraHosts so it resolves off Docker Desktop too.
	want := "http://host.docker.internal:4566"
	if got != want {
		t.Errorf("resolve() = %q, want %q", got, want)
	}
}

// ipNet builds a net.Addr for an address string, as net.InterfaceAddrs returns.
func ipNet(ip string) net.Addr {
	parsed := net.ParseIP(ip)
	mask := net.CIDRMask(24, 32)
	if parsed.To4() == nil {
		mask = net.CIDRMask(64, 128)
	}
	return &net.IPNet{IP: parsed, Mask: mask}
}

func TestEndpointURL_buildsAnHTTPOrigin(t *testing.T) {
	// Given/When/Then: the resolved host and Overcast's port become an origin
	// suitable for AWS_ENDPOINT_URL.
	if got := endpointURL("172.18.0.1", 4566); got != "http://172.18.0.1:4566" {
		t.Errorf("endpointURL() = %q, want %q", got, "http://172.18.0.1:4566")
	}
}
