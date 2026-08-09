package containerendpoint

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/docker"
)

// listen_test.go covers choosing a bind set for a server containers connect
// back to. The behaviour under test is a narrowing: before it, the Lambda
// Runtime API bound 0.0.0.0 unconditionally, so an unauthenticated control
// channel for every Lambda container was on whatever network the machine was
// attached to. Every case here therefore asserts what is *not* bound as much as
// what is.

// fakeListenClient is fakeNetworkClient plus network inspection, which is how
// the gateway of the Lambda network is found.
type fakeListenClient struct {
	fakeNetworkClient
	inspectedNetwork string
	network          *docker.NetworkInspect
	networkErr       error
}

func (f *fakeListenClient) InspectNetwork(_ context.Context, nameOrID string) (*docker.NetworkInspect, error) {
	f.inspectedNetwork = nameOrID
	return f.network, f.networkErr
}

// networkWithGateway builds an inspect result for a user-defined network whose
// IPAM pool has the given gateway.
func networkWithGateway(name, gateway string) *docker.NetworkInspect {
	return &docker.NetworkInspect{
		Name: name,
		IPAM: docker.NetworkIPAM{Config: []docker.NetworkIPAMConfig{{
			Subnet:  "172.19.0.0/16",
			Gateway: gateway,
		}}},
	}
}

// The environment probes resolveListen takes are shared with published_test.go:
// onHost and inContainer are declared there.

// bindableExcept accepts every address but the listed ones — the shape of a
// Docker Desktop host, where the daemon's gateway lives in a Linux VM and no
// listener on this kernel can hold it.
func bindableExcept(unbindable ...string) func(string) bool {
	return func(host string) bool { return !slices.Contains(unbindable, host) }
}

func bindableNone(string) bool { return false }

func TestResolveListen_prefersTheLambdaNetworkGatewayOverAHostInterface(t *testing.T) {
	// Given: Overcast on a host whose Lambda network has a gateway this kernel
	// can bind — a native Linux daemon.
	dc := &fakeListenClient{network: networkWithGateway("overcast_lambda", "172.19.0.1")}

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, "overcast_lambda", nil,
		onHost, func() string { return "192.168.1.20" }, bindableExcept())

	// Then: containers are pointed at the gateway and it is bound with
	// loopback — the machine's LAN address is left alone, which is the whole
	// point: the gateway is reachable from this machine's containers and from
	// nowhere else.
	if got.ContainerHost != "172.19.0.1" {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, "172.19.0.1")
	}
	if !slices.Equal(got.BindHosts, []string{"172.19.0.1", "127.0.0.1"}) {
		t.Errorf("BindHosts = %v, want [172.19.0.1 127.0.0.1]", got.BindHosts)
	}
	if got.Wildcard {
		t.Error("Wildcard = true, want false — a gateway was resolved")
	}
	if dc.inspectedNetwork != "overcast_lambda" {
		t.Errorf("inspected network %q, want %q", dc.inspectedNetwork, "overcast_lambda")
	}
}

func TestResolveListen_fallsBackToTheHostAddressWhenTheGatewayIsNotOurs(t *testing.T) {
	// Given: Docker Desktop — the network reports a gateway, but it belongs to
	// the daemon's VM, so binding it here fails.
	dc := &fakeListenClient{network: networkWithGateway("overcast_lambda", "172.19.0.1")}

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, "overcast_lambda", nil,
		onHost, func() string { return "192.168.1.20" }, bindableExcept("172.19.0.1"))

	// Then: the host's own routable address is used instead — Desktop routes
	// containers to the host itself — and still only that address, not every
	// interface the machine has.
	if got.ContainerHost != "192.168.1.20" {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, "192.168.1.20")
	}
	if !slices.Equal(got.BindHosts, []string{"192.168.1.20", "127.0.0.1"}) {
		t.Errorf("BindHosts = %v, want [192.168.1.20 127.0.0.1]", got.BindHosts)
	}
	if got.Wildcard {
		t.Error("Wildcard = true, want false — a host address was resolved")
	}
}

func TestResolveListen_usesOurOwnAddressOnTheNetworkWhenContainerised(t *testing.T) {
	// Given: Overcast is itself a container on the Lambda network. Its address
	// there is directly routable from siblings, and the gateway — which it
	// could not bind anyway — is nobody's answer.
	dc := &fakeListenClient{
		fakeNetworkClient: fakeNetworkClient{inspect: inspectOnNetwork("overcast_lambda", "172.19.0.5")},
		network:           networkWithGateway("overcast_lambda", "172.19.0.1"),
	}

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, "overcast_lambda", nil,
		inContainer, func() string { return "192.168.1.20" }, bindableExcept())

	// Then: our address on the network is what containers dial and what is
	// bound.
	if got.ContainerHost != "172.19.0.5" {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, "172.19.0.5")
	}
	if !slices.Equal(got.BindHosts, []string{"172.19.0.5", "127.0.0.1"}) {
		t.Errorf("BindHosts = %v, want [172.19.0.5 127.0.0.1]", got.BindHosts)
	}
	if dc.connectedTo != "overcast_lambda" {
		t.Errorf("connected to %q, want %q", dc.connectedTo, "overcast_lambda")
	}
}

func TestResolveListen_bindsEveryInterfaceRatherThanStrandContainers(t *testing.T) {
	// Given: a host where nothing resolves — the network cannot be inspected
	// and no interface address is usable.
	dc := &fakeListenClient{networkErr: errors.New("no such network")}

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, "overcast_lambda", nil,
		onHost, func() string { return "" }, bindableNone)

	// Then: the wildcard, flagged as such. A Runtime API nobody can reach fails
	// worse than one bound too widely — every invocation would hang at INIT —
	// so the fallback keeps the old behaviour and says it is doing so.
	if !got.Wildcard {
		t.Error("Wildcard = false, want true")
	}
	if !slices.Equal(got.BindHosts, []string{"0.0.0.0"}) {
		t.Errorf("BindHosts = %v, want [0.0.0.0]", got.BindHosts)
	}
	if got.ContainerHost != dockerInternalHost {
		t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, dockerInternalHost)
	}
}

func TestResolveListen_neverBindsTheWildcardAlongsideAResolvedAddress(t *testing.T) {
	// Given: each mode that resolves an address.
	cases := map[string]struct {
		dc            *fakeListenClient
		inContainer   func() bool
		hostIP        func() string
		bindable      func(string) bool
		wantContainer string
	}{
		"gateway": {
			dc:            &fakeListenClient{network: networkWithGateway("overcast_lambda", "172.19.0.1")},
			inContainer:   onHost,
			hostIP:        func() string { return "192.168.1.20" },
			bindable:      bindableExcept(),
			wantContainer: "172.19.0.1",
		},
		"host": {
			dc:            &fakeListenClient{networkErr: errors.New("no such network")},
			inContainer:   onHost,
			hostIP:        func() string { return "192.168.1.20" },
			bindable:      bindableExcept(),
			wantContainer: "192.168.1.20",
		},
		"container": {
			dc: &fakeListenClient{
				fakeNetworkClient: fakeNetworkClient{inspect: inspectOnNetwork("overcast_lambda", "172.19.0.5")},
			},
			inContainer:   inContainer,
			hostIP:        func() string { return "192.168.1.20" },
			bindable:      bindableExcept(),
			wantContainer: "172.19.0.5",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// When: the listen set is resolved.
			got := resolveListen(context.Background(), tc.dc, "overcast_lambda", nil,
				tc.inContainer, tc.hostIP, tc.bindable)

			// Then: the wildcard is absent. Binding it next to a specific
			// address is not a belt-and-braces measure, it is the exposure the
			// specific address was chosen to avoid.
			if slices.Contains(got.BindHosts, wildcardHost) {
				t.Errorf("BindHosts = %v, must not contain %q", got.BindHosts, wildcardHost)
			}
			if got.ContainerHost != tc.wantContainer {
				t.Errorf("ContainerHost = %q, want %q", got.ContainerHost, tc.wantContainer)
			}
			// And loopback is always there: it costs nothing, and it is what a
			// developer or a test on this machine reaches for.
			if !slices.Contains(got.BindHosts, loopbackHost) {
				t.Errorf("BindHosts = %v, want %q in it", got.BindHosts, loopbackHost)
			}
		})
	}
}

func TestResolveListen_ignoresAContainerModeAddressItCannotBind(t *testing.T) {
	// Given: Overcast reports an address on the network that this kernel will
	// not accept a listener on — a stale inspect result, or an address that
	// belongs to a namespace we are not in.
	dc := &fakeListenClient{
		fakeNetworkClient: fakeNetworkClient{inspect: inspectOnNetwork("overcast_lambda", "172.19.0.5")},
	}

	// When: the listen set is resolved.
	got := resolveListen(context.Background(), dc, "overcast_lambda", nil,
		inContainer, func() string { return "192.168.1.20" }, bindableExcept("172.19.0.5"))

	// Then: it is not configured. A container told to dial an address nothing
	// is listening on hangs until its invocation times out, which reads as a
	// broken function rather than a network mistake. The host branch is skipped
	// while containerised (our interface addresses are not routable from a
	// sibling), so the honest answer here is the wildcard.
	if !got.Wildcard {
		t.Errorf("Wildcard = false with BindHosts %v, want the fallback", got.BindHosts)
	}
}

func TestNetworkGateway_declinesGatewaysNoContainerCouldDial(t *testing.T) {
	cases := map[string]*docker.NetworkInspect{
		// The "host" and "none" networks, and any network created without an
		// IPAM pool: no gateway is not an error, it means the mode is n/a.
		"no IPAM pool": {Name: "host"},
		"empty gateway": {Name: "overcast_lambda", IPAM: docker.NetworkIPAM{
			Config: []docker.NetworkIPAMConfig{{Subnet: "172.19.0.0/16"}}}},
		"unparseable":  networkWithGateway("overcast_lambda", "not-an-ip"),
		"loopback":     networkWithGateway("overcast_lambda", "127.0.0.1"),
		"unspecified":  networkWithGateway("overcast_lambda", "0.0.0.0"),
		"link-local":   networkWithGateway("overcast_lambda", "169.254.1.1"),
		"IPv6 gateway": networkWithGateway("overcast_lambda", "fd00::1"),
	}

	for name, inspect := range cases {
		t.Run(name, func(t *testing.T) {
			// Given: a network whose gateway is unusable.
			dc := &fakeListenClient{network: inspect}

			// When/Then: no gateway is offered, so resolution moves on rather
			// than binding something arbitrary.
			if got := networkGateway(context.Background(), dc, "overcast_lambda"); got != "" {
				t.Errorf("networkGateway() = %q, want \"\"", got)
			}
		})
	}
}

func TestNetworkGateway_withoutADaemonOrANetwork(t *testing.T) {
	// Given: no Docker client, or no configured network — a service that
	// manages its own networks passes an empty name.
	// When/Then: neither panics, and neither invents a gateway.
	if got := networkGateway(context.Background(), nil, "overcast_lambda"); got != "" {
		t.Errorf("networkGateway(nil client) = %q, want \"\"", got)
	}
	if got := networkGateway(context.Background(), &fakeListenClient{}, ""); got != "" {
		t.Errorf("networkGateway(no network) = %q, want \"\"", got)
	}
}

func TestBindableHost_answersFromAnActualBind(t *testing.T) {
	// Given: loopback, which every platform this runs on can bind.
	// When/Then: the probe says so.
	if !bindableHost("127.0.0.1") {
		t.Error("bindableHost(127.0.0.1) = false, want true")
	}

	// And: TEST-NET-1 (RFC 5737), which is not an address of this machine.
	// The probe is what keeps a Docker Desktop gateway — reported by the
	// daemon, unbindable here — out of the bind set.
	if bindableHost("192.0.2.1") {
		t.Error("bindableHost(192.0.2.1) = true, want false")
	}
}
