package dataplane

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
)

// fakeConnector records what a service asked Docker to attach, so the tests
// assert on the plane and aliases rather than on a daemon.
type fakeConnector struct {
	network  string
	networks []string
	aliases  []string
	calls    int
	err      error
}

func (f *fakeConnector) ConnectNetworkWithAliases(_ context.Context, network, _ string, aliases []string) error {
	f.calls++
	f.network, f.aliases = network, aliases
	f.networks = append(f.networks, network)
	return f.err
}

type fakeVPCResolver struct {
	status  string
	network string
}

func (f fakeVPCResolver) VPCNetworkStatus(context.Context, string) string    { return f.status }
func (f fakeVPCResolver) DockerNetworkForVpc(context.Context, string) string { return f.network }

// testConfig is a deployment where placement restricts: the resolver is
// listening, so a connection a VPC forbids fails by name. That is the normal
// containerised setup and the one most of these cases care about; the host
// deployment where it does not hold has its own case below.
func testConfig() *config.Config {
	return &config.Config{Network: "overcast", Hostname: "localhost", DNSListening: true}
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
	if got := Networks(testConfig(), Placement{}); !slices.Equal(got, []string{"overcast_control", "overcast"}) {
		t.Fatalf("no VPC: Networks = %#v", got)
	}
}

// Naming a VPC is what restricts a resource — on AWS, placement subtracts. This
// is the assertion that says so; before enforcement it read the other way.
func TestDataNetworks_aVPCPlacedResourceLeavesTheDefaultPlane(t *testing.T) {
	cfg := testConfig()

	got := DataNetworks(cfg, Placement{VPCNetwork: "overcast-vpc-vpc-abc"})
	if !slices.Equal(got, []string{"overcast-vpc-vpc-abc"}) {
		t.Fatalf("DataNetworks = %#v, want the VPC network alone", got)
	}
}

// The way out is an AWS field, not an Overcast one: RDS's PubliclyAccessible,
// ECS's assignPublicIp. Someone who needs it here needs it on AWS too.
func TestDataNetworks_publicKeepsTheDefaultPlane(t *testing.T) {
	cfg := testConfig()

	got := DataNetworks(cfg, Placement{VPCNetwork: "overcast-vpc-vpc-abc", Public: true})
	want := []string{"overcast-vpc-vpc-abc", "overcast"}
	if !slices.Equal(got, want) {
		t.Fatalf("DataNetworks = %#v, want %#v", got, want)
	}
}

// Enforcement follows the resolver. Without it — a native Windows or macOS
// host, where there is no /etc/resolv.conf to read upstreams from and the
// resolver declines to start — a forbidden connection would hang on Overcast's
// own address rather than fail by name, which is the failure the guard exists
// to remove. So the restriction is withheld there rather than delivered blind.
func TestDataNetworks_notEnforcedWithoutTheResolver(t *testing.T) {
	cfg := testConfig()
	cfg.DNSListening = false

	got := DataNetworks(cfg, Placement{VPCNetwork: "overcast-vpc-vpc-abc"})
	want := []string{"overcast-vpc-vpc-abc", "overcast"}
	if !slices.Equal(got, want) {
		t.Fatalf("DataNetworks = %#v, want %#v — the union, since the failure would be a hang", got, want)
	}
}

// A resource with no VPC is already on the default plane; Public cannot make it
// more reachable, and must not duplicate the attachment.
func TestDataNetworks_publicIsInertWithoutAVPC(t *testing.T) {
	cfg := testConfig()

	if got := DataNetworks(cfg, Placement{Public: true}); !slices.Equal(got, []string{"overcast"}) {
		t.Fatalf("DataNetworks = %#v, want the default plane once", got)
	}
}

// The seeded default VPC's backing network *is* the default plane, so a
// resource placed in it must not be attached twice — with or without Public.
func TestDataNetworks_defaultVPCIsNotADoubleAttachment(t *testing.T) {
	cfg := testConfig()

	for _, public := range []bool{false, true} {
		got := DataNetworks(cfg, Placement{VPCNetwork: cfg.Network, Public: public})
		if !slices.Equal(got, []string{"overcast"}) {
			t.Fatalf("public=%v: DataNetworks = %#v, want one attachment", public, got)
		}
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

// A VPC-placed resource is attached to its VPC network and nowhere else. This
// is the restriction itself: before enforcement the same call also joined the
// default plane, which is why anything could reach anything.
func TestAttach_aVPCPlacedResourceLeavesTheDefaultPlane(t *testing.T) {
	dc := &fakeConnector{}

	if err := Attach(context.Background(), dc, testConfig(), "container-1",
		Placement{VPCNetwork: "overcast-vpc-vpc-abc", Aliases: []string{"db.localhost"}}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !slices.Equal(dc.networks, []string{"overcast-vpc-vpc-abc"}) {
		t.Fatalf("attached to %#v, want the VPC network alone", dc.networks)
	}
	if !slices.Equal(dc.aliases, []string{"db.localhost"}) {
		t.Fatalf("aliases = %#v", dc.aliases)
	}
}

// Public re-adds the default plane, carrying the same names — a caller outside
// the VPC has to be able to *resolve* it, not merely route to it.
func TestAttach_publicAlsoJoinsTheDefaultPlaneWithAliases(t *testing.T) {
	dc := &fakeConnector{}

	if err := Attach(context.Background(), dc, testConfig(), "container-1",
		Placement{VPCNetwork: "overcast-vpc-vpc-abc", Public: true, Aliases: []string{"db.localhost"}}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	want := []string{"overcast-vpc-vpc-abc", "overcast"}
	if !slices.Equal(dc.networks, want) {
		t.Fatalf("attached to %#v, want %#v", dc.networks, want)
	}
	if !slices.Equal(dc.aliases, []string{"db.localhost"}) {
		t.Fatalf("aliases on the default plane = %#v", dc.aliases)
	}
}

// A VPC network that happens to equal the default plane — which is exactly what
// the seeded default VPC carries — must not be attached twice.
func TestAttach_doesNotDoubleAttachTheDefaultVPC(t *testing.T) {
	dc := &fakeConnector{}

	if err := Attach(context.Background(), dc, testConfig(), "container-1",
		Placement{VPCNetwork: "overcast"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !slices.Equal(dc.networks, []string{"overcast"}) {
		t.Fatalf("attached to %#v, want one attachment", dc.networks)
	}
}

// A container adopted after a restart may predate the plane layout, so it needs
// the control plane too — ContainerAddr looks for it there, and without it a
// containerized Overcast falls back to a host port it cannot reach.
func TestAttachAdopted_alsoJoinsTheControlPlane(t *testing.T) {
	dc := &fakeConnector{}

	if err := AttachAdopted(context.Background(), dc, testConfig(), "container-1",
		Placement{Aliases: []string{"db.localhost"}}); err != nil {
		t.Fatalf("AttachAdopted: %v", err)
	}
	want := []string{"overcast_control", "overcast"}
	if !slices.Equal(dc.networks, want) {
		t.Fatalf("attached to %#v, want %#v", dc.networks, want)
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

// TestControlPlaneInternal_auto covers the three-row table of
// docs/plans/container-network-topology.md § 5: containerised is always
// on-link and safe, a native Linux host's gateway stays on-link on an
// `--internal` bridge, and Docker Desktop's host address does not — plus the
// case the table does not name, where the daemon probe cannot tell.
//
// Every row also has to name itself. The reason is the reportable half of
// #1564: two engineers on one pinned version got different isolation, and
// nothing anywhere said which probe had fired.
func TestControlPlaneInternal_auto(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})

	cases := map[string]struct {
		containerised bool
		native        bool
		want          bool
		wantReason    string
	}{
		"containerised is always internal, even if the daemon probe would say no": {
			containerised: true, native: false,
			want: true, wantReason: "auto: Overcast is containerised",
		},
		"native Linux host: the gateway stays on-link": {
			containerised: false, native: true,
			want: true, wantReason: "auto: native Linux Docker daemon",
		},
		"Docker Desktop host: the daemon probe declines": {
			containerised: false, native: false,
			want: false, wantReason: "auto: Docker Desktop, or a daemon this host could not probe",
		},
	}

	cfg := testConfig()
	cfg.ControlPlaneInternal = config.ControlPlaneInternalAuto
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			runningInContainer = func() bool { return tc.containerised }
			nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return tc.native }

			got := ControlPlaneInternal(cfg)(context.Background(), nil)
			if got.Internal != tc.want {
				t.Errorf("Internal = %v, want %v", got.Internal, tc.want)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestControlPlaneInternal_pinnedOverridesTheHostProbe is the point of #1564:
// an explicit OVERCAST_CONTROL_PLANE_INTERNAL settles the answer whatever the
// host looks like, so one pinned version behaves the same on every machine.
//
// Both pinned values are exercised against *both* hosts the probe can tell
// apart, since a setting that only won on the host that already agreed with it
// would be no setting at all.
func TestControlPlaneInternal_pinnedOverridesTheHostProbe(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})

	hosts := []struct {
		name          string
		containerised bool
		native        bool
	}{
		{"containerised, where auto would say internal", true, false},
		{"Docker Desktop, where auto would say not internal", false, false},
	}
	pins := []struct {
		mode       config.ControlPlaneInternalMode
		want       bool
		wantReason string
	}{
		{config.ControlPlaneInternalTrue, true, "OVERCAST_CONTROL_PLANE_INTERNAL=true"},
		{config.ControlPlaneInternalFalse, false, "OVERCAST_CONTROL_PLANE_INTERNAL=false"},
	}

	for _, host := range hosts {
		for _, pin := range pins {
			t.Run(host.name+"/"+string(pin.mode), func(t *testing.T) {
				runningInContainer = func() bool { return host.containerised }
				nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return host.native }

				cfg := testConfig()
				cfg.ControlPlaneInternal = pin.mode

				got := ControlPlaneInternal(cfg)(context.Background(), nil)
				if got.Internal != pin.want {
					t.Errorf("Internal = %v, want %v", got.Internal, pin.want)
				}
				if got.Reason != pin.wantReason {
					t.Errorf("Reason = %q, want %q", got.Reason, pin.wantReason)
				}
			})
		}
	}
}

// An empty ControlPlaneInternal is the zero value of a Config built directly
// rather than through config.Load — which is what every test helper and
// embedding caller in the tree does — and it has to mean auto rather than
// "pinned to nothing".
func TestControlPlaneInternal_zeroValueMeansAuto(t *testing.T) {
	restoreContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = restoreContainer })
	runningInContainer = func() bool { return true }

	cfg := testConfig()
	cfg.ControlPlaneInternal = ""

	if got := ControlPlaneInternal(cfg)(context.Background(), nil); !got.Internal {
		t.Errorf("zero-value mode = %+v, want the auto probe's answer (internal)", got)
	}
}

// PlaneSpecs wires ControlPlaneInternal in as InternalMode rather than
// resolving it eagerly — the point being that Probe, not this function, is
// what has a live Docker client to hand it.
func TestPlaneSpecs_defersTheControlPlaneDecisionToInternalMode(t *testing.T) {
	cfg := testConfig()
	specs := PlaneSpecs(cfg)

	if len(specs) != 2 {
		t.Fatalf("PlaneSpecs() returned %d specs, want 2", len(specs))
	}
	data, control := specs[0], specs[1]

	if data.Name != cfg.Network || data.InternalMode != nil {
		t.Errorf("data plane spec = %+v, want Name=%q and no InternalMode", data, cfg.Network)
	}
	if control.Name != Primary(cfg) {
		t.Errorf("control plane spec Name = %q, want %q", control.Name, Primary(cfg))
	}
	if control.InternalMode == nil {
		t.Fatal("control plane spec InternalMode is nil, want ControlPlaneInternal")
	}

	restoreContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = restoreContainer })
	runningInContainer = func() bool { return true }

	if got := control.InternalMode(context.Background(), nil); !got.Internal {
		t.Errorf("control plane InternalMode(containerised) = %+v, want Internal=true", got)
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

// fakeReconnector records the detach/attach ordering Reattach depends on: an
// alias set only changes if the container leaves the network before it rejoins.
type fakeReconnector struct {
	fakeConnector
	order         []string
	disconnectErr error
	disconnected  []string
}

func (f *fakeReconnector) ConnectNetworkWithAliases(ctx context.Context, network, container string, aliases []string) error {
	f.order = append(f.order, "connect "+network)
	return f.fakeConnector.ConnectNetworkWithAliases(ctx, network, container, aliases)
}

func (f *fakeReconnector) DisconnectNetwork(_ context.Context, network, _ string) error {
	f.order = append(f.order, "disconnect "+network)
	f.disconnected = append(f.disconnected, network)
	return f.disconnectErr
}

// Attach cannot change an alias set — Docker fixes aliases when a container
// joins a network and ConnectNetworkWithConfig swallows the second connect as
// success. Reattach exists to leave and rejoin, and the order is the whole
// point: disconnect must precede connect on each plane.
func TestReattach_leavesThePlaneBeforeRejoiningIt(t *testing.T) {
	f := &fakeReconnector{}
	cfg := testConfig()

	if err := Reattach(context.Background(), f, cfg, "c1",
		Placement{Aliases: []string{"orders.cluster.localhost"}}); err != nil {
		t.Fatalf("Reattach: %v", err)
	}

	want := []string{"disconnect overcast", "connect overcast"}
	if !slices.Equal(f.order, want) {
		t.Errorf("call order = %v, want %v", f.order, want)
	}
	if !slices.Equal(f.aliases, []string{"orders.cluster.localhost"}) {
		t.Errorf("aliases = %v, want the new set to be advertised on the rejoin", f.aliases)
	}
}

// A VPC-placed public container sits on two planes, and both carry the names.
// Reattach has to visit every one of them or the container answers to the new
// set on one plane and the old set on another.
func TestReattach_coversEveryPlaneThePlacementPutsItOn(t *testing.T) {
	f := &fakeReconnector{}
	cfg := testConfig()

	if err := Reattach(context.Background(), f, cfg, "c1",
		Placement{VPCNetwork: "overcast-vpc-vpc-abc", Public: true, Aliases: []string{"db.localhost"}}); err != nil {
		t.Fatalf("Reattach: %v", err)
	}

	want := []string{"overcast-vpc-vpc-abc", "overcast"}
	if !slices.Equal(f.disconnected, want) {
		t.Errorf("disconnected = %v, want %v", f.disconnected, want)
	}
	if !slices.Equal(f.networks, want) {
		t.Errorf("reconnected = %v, want %v", f.networks, want)
	}
}

// A container that is not on the plane has nothing to leave, and that is not a
// failure — it is the state a caller is trying to reach. Rejoining still has to
// happen, or a promotion that raced a restart would leave the name nowhere.
func TestReattach_toleratesAContainerThatIsNotOnThePlane(t *testing.T) {
	f := &fakeReconnector{disconnectErr: errors.New("container c1 is not connected to network overcast")}
	cfg := testConfig()

	if err := Reattach(context.Background(), f, cfg, "c1",
		Placement{Aliases: []string{"db.localhost"}}); err != nil {
		t.Fatalf("Reattach: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("connect calls = %d, want the rejoin to happen anyway", f.calls)
	}
}
