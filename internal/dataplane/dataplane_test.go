package dataplane

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
)

// fakeConnector records what a service asked Docker to attach, so the tests
// assert on the plane and aliases rather than on a daemon.
type fakeConnector struct {
	network string
	aliases []string
	calls   int
	err     error
}

func (f *fakeConnector) ConnectNetworkWithAliases(_ context.Context, network, _ string, aliases []string) error {
	f.calls++
	f.network, f.aliases = network, aliases
	return f.err
}

type fakeVPCResolver struct {
	status  string
	network string
}

func (f fakeVPCResolver) VPCNetworkStatus(context.Context, string) string    { return f.status }
func (f fakeVPCResolver) DockerNetworkForVpc(context.Context, string) string { return f.network }

func testConfig() *config.Config {
	return &config.Config{Network: "overcast", Hostname: "localhost"}
}

// The control plane is derived, never configured separately: there is no way to
// set one half of the pair.
func TestPrimary_isTheControlPlane(t *testing.T) {
	if got := Primary(testConfig()); got != "overcast_control" {
		t.Fatalf("Primary = %q, want overcast_control", got)
	}
}

func TestDataNetwork_defaultsToThePlaneAndPrefersAVPC(t *testing.T) {
	cfg := testConfig()

	if got := DataNetwork(cfg, Placement{}); got != "overcast" {
		t.Fatalf("no VPC: DataNetwork = %q, want overcast", got)
	}
	if got := DataNetwork(cfg, Placement{VPCNetwork: "overcast-vpc-vpc-abc"}); got != "overcast-vpc-vpc-abc" {
		t.Fatalf("with VPC: DataNetwork = %q, want the VPC network", got)
	}
}

// A container is created on the control plane and attached to exactly one data
// plane. Both matter: the first is what keeps the Runtime API reachable
// whatever VPC a resource joins, the second is what a VPC restricts.
func TestNetworks_isControlThenData(t *testing.T) {
	got := Networks(testConfig(), Placement{VPCNetwork: "overcast-vpc-vpc-abc"})
	want := []string{"overcast_control", "overcast-vpc-vpc-abc"}
	if !slices.Equal(got, want) {
		t.Fatalf("Networks = %#v, want %#v", got, want)
	}
}

func TestAttach_joinsTheDataPlaneWithAliases(t *testing.T) {
	// Given: a resource with endpoint names and no VPC.
	dc := &fakeConnector{}

	// When: it is attached.
	err := Attach(context.Background(), dc, testConfig(), "container-1",
		Placement{Aliases: []string{"db.us-east-1.rds.localhost"}})

	// Then: it lands on the default data plane carrying its names.
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if dc.network != "overcast" {
		t.Fatalf("attached to %q, want overcast", dc.network)
	}
	if !slices.Equal(dc.aliases, []string{"db.us-east-1.rds.localhost"}) {
		t.Fatalf("aliases = %#v", dc.aliases)
	}
}

func TestAttach_prefersTheVPCPlane(t *testing.T) {
	dc := &fakeConnector{}

	if err := Attach(context.Background(), dc, testConfig(), "container-1",
		Placement{VPCNetwork: "overcast-vpc-vpc-abc", Aliases: []string{"db.localhost"}}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if dc.network != "overcast-vpc-vpc-abc" {
		t.Fatalf("attached to %q, want the VPC network", dc.network)
	}
}

// Attach is called on reconcile paths for containers adopted from an earlier
// run, so it must be safe to repeat and must not fail on an empty container.
func TestAttach_isANoOpWithoutAContainer(t *testing.T) {
	dc := &fakeConnector{}
	if err := Attach(context.Background(), dc, testConfig(), "", Placement{}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if dc.calls != 0 {
		t.Fatalf("calls = %d, want 0", dc.calls)
	}
}

func TestAttach_wrapsTheDaemonError(t *testing.T) {
	sentinel := errors.New("no such network")
	dc := &fakeConnector{err: sentinel}

	err := Attach(context.Background(), dc, testConfig(), "container-1", Placement{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Attach error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestLaunchable(t *testing.T) {
	// An empty status predates the field and is an ordinary working VPC; an
	// unrecognised one is refused rather than guessed at.
	for _, status := range []string{"", "ok", "shared", "remapped"} {
		if !Launchable(status) {
			t.Errorf("Launchable(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"conflict", "unbacked", "something-new"} {
		if Launchable(status) {
			t.Errorf("Launchable(%q) = true, want false", status)
		}
	}
}

func TestPlaceInVPC(t *testing.T) {
	ctx := context.Background()

	t.Run("no resolver or no VPC is the default plane", func(t *testing.T) {
		got, err := PlaceInVPC(ctx, nil, "vpc-abc")
		if err != nil || got.VPCNetwork != "" {
			t.Fatalf("PlaceInVPC = %#v, %v", got, err)
		}
		got, err = PlaceInVPC(ctx, fakeVPCResolver{status: "ok", network: "net"}, "")
		if err != nil || got.VPCNetwork != "" {
			t.Fatalf("PlaceInVPC = %#v, %v", got, err)
		}
	})

	t.Run("a launchable VPC resolves to its network", func(t *testing.T) {
		got, err := PlaceInVPC(ctx, fakeVPCResolver{status: "shared", network: "overcast-vpc-vpc-abc"}, "vpc-abc")
		if err != nil {
			t.Fatalf("PlaceInVPC: %v", err)
		}
		if got.VPCNetwork != "overcast-vpc-vpc-abc" {
			t.Fatalf("VPCNetwork = %q", got.VPCNetwork)
		}
	})

	// Silently falling back to the default plane would place the resource
	// somewhere other than the VPC it asked for, which is how an unreachable
	// endpoint gets minted.
	t.Run("an unlaunchable VPC is an error, not a fallback", func(t *testing.T) {
		_, err := PlaceInVPC(ctx, fakeVPCResolver{status: "unbacked"}, "vpc-abc")
		if err == nil {
			t.Fatal("PlaceInVPC succeeded for an unbacked VPC")
		}
	})
}

// Hostnames is the answer to "what can a caller have been handed for this
// resource": every base, because the name is minted on the host the client
// reached Overcast on. Registering only the configured one is #872.
func TestHostnames_coversEveryBaseAndDropsUnusableNames(t *testing.T) {
	cfg := testConfig()

	got := Hostnames(cfg, func(base string) string { return "db.us-east-1.rds." + base }, "172.18.0.3", "")

	want := []string{
		"db.us-east-1.rds.localhost.overcast.sh",
		"db.us-east-1.rds.localhost.localstack.cloud",
		"db.us-east-1.rds.localhost.floci.io",
		"db.us-east-1.rds.localhost",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Hostnames = %#v, want %#v", got, want)
	}
}

func TestHostnames_keepsAnAdvertisedNameOutsideTheBases(t *testing.T) {
	cfg := testConfig()

	got := Hostnames(cfg, func(base string) string { return "db.rds." + base }, "db.rds.retired.example")

	if !slices.Contains(got, "db.rds.retired.example") {
		t.Fatalf("Hostnames = %#v, want the advertised name retained", got)
	}
}

// ContainerAddr answers "where is this container from Overcast's side", which
// is only meaningful when Overcast is itself containerised — on the host the
// caller must use the published port instead.
func TestContainerAddr_isEmptyOnTheHost(t *testing.T) {
	restore := runningInContainer
	runningInContainer = func() bool { return false }
	t.Cleanup(func() { runningInContainer = restore })

	if got := ContainerAddr(context.Background(), stubInspector{}, testConfig(), "container-1"); got != "" {
		t.Fatalf("ContainerAddr = %q, want empty on the host", got)
	}
}

func TestContainerAddr_readsTheControlPlaneAddress(t *testing.T) {
	restore := runningInContainer
	runningInContainer = func() bool { return true }
	t.Cleanup(func() { runningInContainer = restore })

	dc := stubInspector{addrs: map[string]string{"overcast_control": "172.19.0.7"}}
	if got := ContainerAddr(context.Background(), dc, testConfig(), "container-1"); got != "172.19.0.7" {
		t.Fatalf("ContainerAddr = %q, want 172.19.0.7", got)
	}
}

type stubInspector struct{ addrs map[string]string }

func (stubInspector) ConnectNetwork(context.Context, string, string) error { return nil }

func (s stubInspector) InspectContainer(context.Context, string) (*docker.ContainerInspect, error) {
	// NetworkSettings is an anonymous struct on ContainerInspect, so it is
	// filled in by assignment rather than in a literal.
	info := &docker.ContainerInspect{}
	info.NetworkSettings.Networks = make(map[string]docker.ContainerNetwork, len(s.addrs))
	for name, ip := range s.addrs {
		info.NetworkSettings.Networks[name] = docker.ContainerNetwork{IPAddress: ip}
	}
	return info, nil
}
