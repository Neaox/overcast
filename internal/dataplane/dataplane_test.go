package dataplane

import (
	"context"
	"errors"
	"slices"
	"strings"
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
// setLegacyPin writes the deprecated OVERCAST_CONTROL_PLANE_INTERNAL pin.
// The tests still have to exercise it — it is still honoured — and routing
// every write through one helper keeps the deprecation suppression in one line
// rather than one per test.
func setLegacyPin(cfg *config.Config, mode config.ControlPlaneInternalMode, set bool) {
	//nolint:staticcheck // SA1019: the deprecated pin is still honoured where set (#1566), so it is still tested.
	cfg.ControlPlaneInternal, cfg.ControlPlaneInternalSet = mode, set
}

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

// The egress mode decides every network's isolation, and the control plane is
// not an exception to that — it was the *only* thing deciding it before, which
// is why a VPC's own `--internal` network isolated nothing (measured
// end-to-end: an isolated-subnet Lambda reached checkip.amazonaws.com and got a
// 403 from real sts.us-east-1, because it took its default route from the
// routable control plane).
//
// Every row also has to name itself. The reason is the reportable half of
// #1564: two engineers on one pinned version got different isolation, and
// nothing anywhere said which probe had fired.
func TestControlPlaneInternal_followsTheEgressMode(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})
	// A host where an internal control plane is deliverable, so the mode is
	// the only thing being tested here.
	runningInContainer = func() bool { return true }
	nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return true }

	cases := map[string]struct {
		mode       config.VPCEgressMode
		want       bool
		wantReason string
	}{
		"open leaves it routable":               {config.VPCEgressOpen, false, "OVERCAST_VPC_EGRESS=open"},
		"none isolates it":                      {config.VPCEgressNone, true, "OVERCAST_VPC_EGRESS=none"},
		"the zero value is read as the default": {"", false, "OVERCAST_VPC_EGRESS=open"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.VPCEgress = tc.mode

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

// The host probe no longer decides egress — that is the decoupling #1564 asked
// for. Whatever the host looks like, `open` leaves the plane routable and
// `none` asks for it to be isolated; only whether `none` can be *delivered*
// depends on the host, and that is the next test.
func TestControlPlaneInternal_egressDoesNotDependOnTheHost(t *testing.T) {
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
		{"containerised", true, false},
		{"native Linux daemon", false, true},
	}
	for _, host := range hosts {
		t.Run(host.name, func(t *testing.T) {
			runningInContainer = func() bool { return host.containerised }
			nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return host.native }

			cfg := testConfig()
			cfg.VPCEgress = config.VPCEgressOpen
			if got := ControlPlaneInternal(cfg)(context.Background(), nil); got.Internal {
				t.Errorf("open on a %s host = %+v, want the plane left routable", host.name, got)
			}

			cfg.VPCEgress = config.VPCEgressNone
			if got := ControlPlaneInternal(cfg)(context.Background(), nil); !got.Internal {
				t.Errorf("none on a %s host = %+v, want the plane isolated", host.name, got)
			}
		})
	}
}

// A Docker Desktop host cannot have an isolated control plane at all:
// containers there dial the host's own routable address, which `--internal`
// severs, so every invocation would strand at INIT. `none` is downgraded rather
// than applied, and says so — refusing to start would turn a partly-achievable
// mode into no mode at all on the most common developer platform.
func TestControlPlaneInternal_neverStrandsTheRuntimeAPI(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})
	runningInContainer = func() bool { return false }
	nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return false }

	cfg := testConfig()
	cfg.VPCEgress = config.VPCEgressNone

	got := ControlPlaneInternal(cfg)(context.Background(), nil)
	if got.Internal {
		t.Fatalf("decision = %+v, want the plane left routable on a host that cannot reach an "+
			"internal one", got)
	}
	if !strings.Contains(got.Reason, "overridden") {
		t.Errorf("Reason = %q, want it to say the mode was overridden", got.Reason)
	}
	if !slices.Contains(got.Warnings, ControlPlaneMustStayRoutableWarning) {
		t.Errorf("Warnings = %q, want the shortfall warning — a `none` that silently is not `none` "+
			"is the bug this whole change exists to end", got.Warnings)
	}
}

// The deprecated pin still wins where it is set, so a configuration written
// against #1566 keeps its answer — and setting it earns a notice naming the
// mode that means the same thing.
func TestControlPlaneInternal_deprecatedPinStillWins(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})
	runningInContainer = func() bool { return true }
	nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return true }

	cases := map[string]struct {
		mode            config.VPCEgressMode
		pin             config.ControlPlaneInternalMode
		want            bool
		wantReason      string
		wantReplacement string
	}{
		"true isolates a plane the mode left routable": {
			config.VPCEgressOpen, config.ControlPlaneInternalTrue,
			true, "OVERCAST_CONTROL_PLANE_INTERNAL=true", "OVERCAST_VPC_EGRESS=none",
		},
		"false routes a plane the mode isolated": {
			config.VPCEgressNone, config.ControlPlaneInternalFalse,
			false, "OVERCAST_CONTROL_PLANE_INTERNAL=false", "OVERCAST_VPC_EGRESS=open",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.VPCEgress = tc.mode
			setLegacyPin(cfg, tc.pin, true)

			got := ControlPlaneInternal(cfg)(context.Background(), nil)
			if got.Internal != tc.want {
				t.Errorf("Internal = %v, want %v", got.Internal, tc.want)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			found := false
			for _, w := range got.Warnings {
				if strings.Contains(w, "deprecated") && strings.Contains(w, tc.wantReplacement) {
					found = true
				}
			}
			if !found {
				t.Errorf("Warnings = %q, want a deprecation notice naming %s",
					got.Warnings, tc.wantReplacement)
			}
		})
	}
}

// A default installation never set the variable, and must never be warned about
// it. A deprecation notice that fires for everybody is a notice nobody reads.
func TestControlPlaneInternal_noDeprecationNoticeWhenUnset(t *testing.T) {
	restoreContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = restoreContainer })
	runningInContainer = func() bool { return true }

	cfg := testConfig()
	setLegacyPin(cfg, config.ControlPlaneInternalAuto, false)

	for _, w := range ControlPlaneInternal(cfg)(context.Background(), nil).Warnings {
		if strings.Contains(w, "deprecated") {
			t.Errorf("unset variable produced a deprecation notice: %q", w)
		}
	}
}

// Isolating the plane while the mode says `open` is a contradiction, and it
// costs egress a long way from its cause — ENETUNREACH inside somebody's
// application code, minutes later. Isolating it because the mode said `none` is
// the mode working, and warning about that would be noise.
func TestControlPlaneInternal_warnsOnlyWhenIsolationContradictsTheMode(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})
	runningInContainer = func() bool { return true }
	nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return true }

	cases := map[string]struct {
		mode        config.VPCEgressMode
		pin         config.ControlPlaneInternalMode
		wantWarning bool
	}{
		"pinned internal under open": {config.VPCEgressOpen, config.ControlPlaneInternalTrue, true},
		"isolated by none":           {config.VPCEgressNone, config.ControlPlaneInternalAuto, false},
		"routable under open":        {config.VPCEgressOpen, config.ControlPlaneInternalAuto, false},
		"pinned routable under none": {config.VPCEgressNone, config.ControlPlaneInternalFalse, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.VPCEgress = tc.mode
			setLegacyPin(cfg, tc.pin, false)
			got := ControlPlaneInternal(cfg)(context.Background(), nil)

			hasWarning := slices.Contains(got.Warnings, ControlPlaneEgressWarning)
			if hasWarning != tc.wantWarning {
				t.Errorf("egress warning present = %v, want %v (decision %+v)",
					hasWarning, tc.wantWarning, got)
			}
		})
	}
}

// The warning has to name the symptom an operator will actually search for and
// the setting that undoes it, or it is noise on an already busy startup.
func TestControlPlaneEgressWarning_namesTheSymptomAndTheFix(t *testing.T) {
	for _, want := range []string{
		"NO egress at all",
		"OVERCAST_VPC_EGRESS=none",
		"deprecated",
	} {
		if !strings.Contains(ControlPlaneEgressWarning, want) {
			t.Errorf("ControlPlaneEgressWarning does not mention %q:\n%s", want, ControlPlaneEgressWarning)
		}
	}
	for _, want := range []string{
		"strand",
		"NOT hermetic",
		"OVERCAST_VPC_EGRESS=none",
	} {
		if !strings.Contains(ControlPlaneMustStayRoutableWarning, want) {
			t.Errorf("ControlPlaneMustStayRoutableWarning does not mention %q:\n%s",
				want, ControlPlaneMustStayRoutableWarning)
		}
	}
}

// `none` has to mean none. The default data plane was never `--internal` before
// egress modes, so a machine could isolate its control plane and every VPC
// network and still have every non-VPC function reach the internet through it.
func TestDataPlaneInternal_followsTheEgressMode(t *testing.T) {
	cfg := testConfig()

	cfg.VPCEgress = config.VPCEgressOpen
	if got := Internal(cfg)(context.Background(), nil); got.Internal {
		t.Errorf("open = %+v, want the data plane routable", got)
	}
	cfg.VPCEgress = config.VPCEgressNone
	if got := Internal(cfg)(context.Background(), nil); !got.Internal {
		t.Errorf("none = %+v, want the data plane isolated", got)
	}
}

// `none` has to mean none: a VPC network is `--internal` whatever the template
// says, or the mode's promise has a hole in it.
//
// `open` leaves the gateway deciding, and that costs it nothing — the container
// is also on the routable control plane and takes its default route from there,
// so it has egress either way (measured end-to-end: a Lambda in an isolated
// subnet on an `Internal=true` network reached checkip.amazonaws.com and got a
// 403 from real sts.us-east-1). Flattening the flag would only make it lie
// about the template and leave `routed` nothing to inherit.
//
// What changed is that the flag no longer *decides* egress on its own, which is
// what made a private-with-NAT subnet indistinguishable from an isolated one.
func TestVPCNetworkInternal_theModeDecidesWhetherTheGatewayDecides(t *testing.T) {
	cfg := testConfig()

	cfg.VPCEgress = config.VPCEgressOpen
	if !VPCNetworkInternal(cfg, false) {
		t.Error("open with no gateway = routable, want the gateway still honoured (internal)")
	}
	if VPCNetworkInternal(cfg, true) {
		t.Error("open with a gateway attached = internal, want routable")
	}

	cfg.VPCEgress = config.VPCEgressNone
	for _, hasIGW := range []bool{true, false} {
		if !VPCNetworkInternal(cfg, hasIGW) {
			t.Errorf("none with hasIGW=%v = routable, want internal whatever the template says", hasIGW)
		}
	}
}

// A VPC network is the one Overcast resource named from an emulated resource id
// rather than from configuration, so two instances on one daemon can mint the
// same name. The owner label is what decides who may remove it, and an instance
// that cannot establish its own identity stamps nothing — because a sweep that
// stamps an empty owner would go on to claim every unlabelled network on the
// machine.
func TestVPCNetworkSpec_stampsOwnershipOnlyWhenItIsKnown(t *testing.T) {
	cfg := testConfig()

	spec := VPCNetworkSpec(cfg, "vpc-1", "10.0.0.0/16", "instance-a", false)
	if spec.Owner != "instance-a" || spec.Labels[docker.LabelInstance] != "instance-a" {
		t.Errorf("spec = %+v, want owner instance-a in both the field and the label", spec)
	}
	if spec.Name != cfg.VPCNetwork("vpc-1") {
		t.Errorf("Name = %q, want %q", spec.Name, cfg.VPCNetwork("vpc-1"))
	}

	anon := VPCNetworkSpec(cfg, "vpc-1", "10.0.0.0/16", "", false)
	if _, ok := anon.Labels[docker.LabelInstance]; ok {
		t.Errorf("labels = %v, want no instance label when the identity is unknown", anon.Labels)
	}
}

// PlaneSpecs wires the decisions in as InternalMode rather than resolving them
// eagerly — the point being that Probe, not this function, has a live Docker
// client to hand them. Both planes carry one now: `none` isolates both.
func TestPlaneSpecs_defersEveryDecisionToInternalMode(t *testing.T) {
	cfg := testConfig()
	specs := PlaneSpecs(cfg)

	if len(specs) != 2 {
		t.Fatalf("PlaneSpecs() returned %d specs, want 2", len(specs))
	}
	data, control := specs[0], specs[1]

	if data.Name != cfg.Network || data.InternalMode == nil {
		t.Errorf("data plane spec = %+v, want Name=%q and an InternalMode", data, cfg.Network)
	}
	if control.Name != Primary(cfg) || control.InternalMode == nil {
		t.Errorf("control plane spec = %+v, want Name=%q and an InternalMode", control, Primary(cfg))
	}
	// Both are labelled, or nothing can verify, account for or safely rebuild
	// them — an unlabelled network is the state that made #1564 invisible.
	for _, spec := range specs {
		if spec.Labels[docker.LabelManaged] != "true" || spec.Labels[docker.LabelService] != docker.ServiceCore {
			t.Errorf("%s labels = %v, want the managed/core identity labels", spec.Name, spec.Labels)
		}
		if spec.Owner != cfg.Network {
			t.Errorf("%s Owner = %q, want %q", spec.Name, spec.Owner, cfg.Network)
		}
	}

	restoreContainer := runningInContainer
	t.Cleanup(func() { runningInContainer = restoreContainer })
	runningInContainer = func() bool { return true }

	cfg.VPCEgress = config.VPCEgressNone
	for _, spec := range PlaneSpecs(cfg) {
		if got := spec.InternalMode(context.Background(), nil); !got.Internal {
			t.Errorf("%s InternalMode under none = %+v, want Internal=true", spec.Name, got)
		}
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
