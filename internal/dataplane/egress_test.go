package dataplane

// egress_test.go — OVERCAST_VPC_EGRESS=routed as the dataplane sees it (#1571):
// the control plane and every VPC plane are internal, a subnet's route table
// earns a container a second, routable network, and a placement that came with
// subnets is recorded so it can be revisited.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
)

func routedConfig() *config.Config {
	cfg := testConfig()
	cfg.VPCEgress = config.VPCEgressRouted
	return cfg
}

// Under `routed` the control plane has to be internal: a routable one hands
// every container a route out whatever its route table says, which is the
// measured finding the mode exists to correct.
func TestControlPlaneInternal_routedIsolatesItWhereItCan(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})
	runningInContainer = func() bool { return true }
	nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return true }
	resetWarningsOnce()

	got := ControlPlaneInternal(routedConfig())(context.Background(), nil)
	if !got.Internal {
		t.Fatalf("decision = %+v, want the control plane internal under routed", got)
	}
	if got.Reason != "OVERCAST_VPC_EGRESS=routed" {
		t.Errorf("Reason = %q", got.Reason)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none — isolation asked for by the mode is the mode working", got.Warnings)
	}
}

// On a host where the control plane cannot be isolated, `routed` degrades the
// way `none` does — and says so in its own words, because the shortfall is a
// different one: not a leak, but a withholding that cannot happen.
func TestControlPlaneInternal_routedSaysWhatDesktopCosts(t *testing.T) {
	restoreContainer := runningInContainer
	restoreDaemon := nativeLinuxDaemon
	t.Cleanup(func() {
		runningInContainer = restoreContainer
		nativeLinuxDaemon = restoreDaemon
	})
	runningInContainer = func() bool { return false }
	nativeLinuxDaemon = func(context.Context, *docker.Client) bool { return false }
	resetWarningsOnce()

	got := ControlPlaneInternal(routedConfig())(context.Background(), nil)
	if got.Internal {
		t.Fatalf("decision = %+v, want the plane left routable on a host that cannot reach an internal one", got)
	}
	if !slices.Contains(got.Warnings, RoutedControlPlaneMustStayRoutableWarning) {
		t.Errorf("Warnings = %q, want the routed-specific shortfall", got.Warnings)
	}
	for _, want := range []string{"routed", "withhold", "route table"} {
		if !strings.Contains(RoutedControlPlaneMustStayRoutableWarning, want) {
			t.Errorf("the warning does not mention %q", want)
		}
	}
}

// The VPC's plane is internal under routed whatever the gateway says: egress
// is a second network, decided per subnet, not a property of the VPC.
func TestVPCNetworkInternal_routedKeepsEveryPlaneInternal(t *testing.T) {
	for _, gateway := range []bool{true, false} {
		if !VPCNetworkInternal(routedConfig(), gateway) {
			t.Errorf("VPCNetworkInternal(routed, gateway=%t) = false, want true", gateway)
		}
	}
	if !strings.Contains(VPCNetworkEgressReason(routedConfig(), true), config.VPCEgressNetworkSuffix) {
		t.Errorf("the reason for an internal plane under routed should name the egress network: %q",
			VPCNetworkEgressReason(routedConfig(), true))
	}
	if VPCNetworkInternal(testConfig(), true) {
		t.Errorf("open with a gateway must still be routable")
	}
}

func TestVPCEgressNetworkSpec_isRoutableMasqueradingAndLabelled(t *testing.T) {
	cfg := routedConfig()
	spec := VPCEgressNetworkSpec(cfg, VPCEgressNetwork{VPCID: "vpc-1", Subnet: "198.18.0.0/24", Owner: "me"}).Resolve(context.Background(), nil)

	if spec.Name != "overcast-vpc-vpc-1-egress" {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.Internal {
		t.Errorf("an egress network must never be internal")
	}
	if spec.Subnet != "198.18.0.0/24" {
		t.Errorf("Subnet = %q, want it pinned — an unpinned one draws on Docker's address pools", spec.Subnet)
	}
	if spec.Options[docker.OptionIPMasquerade] != "true" {
		t.Errorf("Options = %v, want IP masquerade pinned on — it is what carries egress on a bridge", spec.Options)
	}
	if spec.Labels[docker.LabelVPCRole] != docker.VPCRoleEgress {
		t.Errorf("role label = %q, want %q so the reconcile can tell it from the plane", spec.Labels[docker.LabelVPCRole], docker.VPCRoleEgress)
	}
	if spec.Labels["overcast.vpc-id"] != "vpc-1" || spec.Labels[docker.LabelInstance] != "me" {
		t.Errorf("labels = %v, want the VPC and owner", spec.Labels)
	}
	if plane := VPCNetworkSpec(cfg, VPCNetwork{VPCID: "vpc-1"}); plane.Labels[docker.LabelVPCRole] != docker.VPCRolePlane {
		t.Errorf("the plane's role label = %q, want %q", plane.Labels[docker.LabelVPCRole], docker.VPCRolePlane)
	}
}

// fakeSubnetPlacer is a resolver that also decides egress per subnet and
// wants to hear where its placements landed — the EC2 service under routed.
type fakeSubnetPlacer struct {
	fakeVPCResolver
	egress    string
	egressErr error
	askedFor  []string
	recorded  map[string]Placement
}

func (f *fakeSubnetPlacer) EgressNetworkForSubnets(_ context.Context, _ string, subnetIDs []string) (string, error) {
	f.askedFor = subnetIDs
	return f.egress, f.egressErr
}

func (f *fakeSubnetPlacer) RecordPlacement(_ context.Context, containerID string, p Placement) {
	if f.recorded == nil {
		f.recorded = map[string]Placement{}
	}
	f.recorded[containerID] = p
}

// fakeEgressConnector is a connector that can rank a network — the real
// client — recording the ranking it was handed.
type fakeEgressConnector struct {
	fakeConnector
	ranked map[string]int
}

func (f *fakeEgressConnector) ConnectNetworkWithConfig(ctx context.Context, network, container string, cfg *docker.EndpointSettings) error {
	if f.ranked == nil {
		f.ranked = map[string]int{}
	}
	f.ranked[network] = cfg.GwPriority
	return f.fakeConnector.ConnectNetworkWithAliases(ctx, network, container, cfg.Aliases)
}

func TestPlaceInSubnets_asksTheSubnetsAndCarriesTheEgressNetwork(t *testing.T) {
	ctx := context.Background()
	r := &fakeSubnetPlacer{fakeVPCResolver: fakeVPCResolver{status: "ok", network: "plane"}, egress: "egress"}

	got, err := PlaceInSubnets(ctx, r, "vpc-1", []string{"subnet-a", "subnet-b"})
	if err != nil {
		t.Fatalf("PlaceInSubnets: %v", err)
	}
	if got.VPCNetwork != "plane" || got.EgressNetwork != "egress" {
		t.Errorf("placement = %+v, want the plane and the egress network", got)
	}
	if got.VPCID != "vpc-1" || !slices.Equal(got.SubnetIDs, []string{"subnet-a", "subnet-b"}) {
		t.Errorf("placement = %+v, want the VPC and subnets kept for the record", got)
	}
	if !slices.Equal(r.askedFor, []string{"subnet-a", "subnet-b"}) {
		t.Errorf("the placer was asked about %v, want every subnet the resource named", r.askedFor)
	}
	if !slices.Equal(Networks(testConfig(), got), []string{"overcast_control", "plane", "egress"}) {
		t.Errorf("Networks = %v, want control, plane, egress", Networks(testConfig(), got))
	}
}

// A resolver that cannot decide per subnet — every mode but routed, or a
// test fake — places by VPC alone, as before.
func TestPlaceInSubnets_fallsBackToTheVPCWithoutASubnetPlacer(t *testing.T) {
	got, err := PlaceInSubnets(context.Background(), fakeVPCResolver{status: "ok", network: "plane"}, "vpc-1", []string{"subnet-a"})
	if err != nil {
		t.Fatalf("PlaceInSubnets: %v", err)
	}
	if got.VPCNetwork != "plane" || got.EgressNetwork != "" {
		t.Errorf("placement = %+v, want the plane and no egress network", got)
	}
}

// The egress network the template grants being unavailable fails the
// placement: quietly placing without it is `routed` becoming `none` for one
// VPC with nothing saying so.
func TestPlaceInSubnets_anUndeliverableEgressNetworkIsAnError(t *testing.T) {
	r := &fakeSubnetPlacer{fakeVPCResolver: fakeVPCResolver{status: "ok", network: "plane"}, egressErr: errors.New("pool exhausted")}
	_, err := PlaceInSubnets(context.Background(), r, "vpc-1", []string{"subnet-a"})
	if err == nil || !strings.Contains(err.Error(), "pool exhausted") || !strings.Contains(err.Error(), "vpc-1") {
		t.Fatalf("err = %v, want the placer's reason, naming the VPC", err)
	}
}

func TestAttach_joinsTheEgressNetworkRankedAndRecordsThePlacement(t *testing.T) {
	ctx := context.Background()
	r := &fakeSubnetPlacer{fakeVPCResolver: fakeVPCResolver{status: "ok", network: "plane"}, egress: "egress"}
	p, err := PlaceInSubnets(ctx, r, "vpc-1", []string{"subnet-a"})
	if err != nil {
		t.Fatal(err)
	}
	p.Aliases = []string{"db.local"}
	dc := &fakeEgressConnector{}

	if err := Attach(ctx, dc, testConfig(), "ctr-1", p); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// The plane first, with the names; the egress network after, ranked as
	// the default-route source and with no names — it carries only the route.
	if !slices.Equal(dc.networks, []string{"plane", "egress"}) {
		t.Errorf("networks joined = %v, want plane then egress", dc.networks)
	}
	if dc.ranked["egress"] != EgressGatewayPriority {
		t.Errorf("egress ranked %d, want %d", dc.ranked["egress"], EgressGatewayPriority)
	}
	if _, ranked := dc.ranked["plane"]; ranked {
		t.Errorf("the plane was joined through the ranked connect; it must carry the aliases instead")
	}
	rec, ok := r.recorded["ctr-1"]
	if !ok {
		t.Fatalf("the placement was not recorded for ctr-1: %v", r.recorded)
	}
	if rec.VPCID != "vpc-1" || !slices.Equal(rec.SubnetIDs, []string{"subnet-a"}) {
		t.Errorf("recorded = %+v", rec)
	}
}

// A connector that cannot rank — a fake — still joins the egress network;
// only the ranking is lost, which is the same degradation an old daemon gets.
func TestAttach_joinsTheEgressNetworkUnrankedWhenTheConnectorCannotRank(t *testing.T) {
	dc := &fakeConnector{}
	p := Placement{VPCNetwork: "plane", EgressNetwork: "egress"}
	if err := Attach(context.Background(), dc, testConfig(), "ctr-1", p); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if !slices.Equal(dc.networks, []string{"plane", "egress"}) {
		t.Errorf("networks joined = %v", dc.networks)
	}
}

func TestAttach_withoutAnEgressNetworkNothingChanges(t *testing.T) {
	dc := &fakeEgressConnector{}
	if err := Attach(context.Background(), dc, testConfig(), "ctr-1", Placement{VPCNetwork: "plane"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dc.networks, []string{"plane"}) || len(dc.ranked) != 0 {
		t.Errorf("networks = %v ranked = %v, want the plane alone", dc.networks, dc.ranked)
	}
}

func TestAttachEgress_isTheEgressHalfOnItsOwn(t *testing.T) {
	ctx := context.Background()
	r := &fakeSubnetPlacer{fakeVPCResolver: fakeVPCResolver{status: "ok", network: "plane"}, egress: "egress"}
	p, err := PlaceInSubnets(ctx, r, "vpc-1", []string{"subnet-a"})
	if err != nil {
		t.Fatal(err)
	}
	dc := &fakeEgressConnector{}
	if err := AttachEgress(ctx, dc, "task-1", p); err != nil {
		t.Fatalf("AttachEgress: %v", err)
	}
	if !slices.Equal(dc.networks, []string{"egress"}) || dc.ranked["egress"] != EgressGatewayPriority {
		t.Errorf("networks = %v ranked = %v, want only the egress network, ranked", dc.networks, dc.ranked)
	}
	if _, ok := r.recorded["task-1"]; !ok {
		t.Errorf("the placement was not recorded")
	}
}

func TestEgressMode_readsTheZeroValueAsOpen(t *testing.T) {
	if EgressMode(nil) != config.VPCEgressOpen || EgressMode(&config.Config{}) != config.VPCEgressOpen {
		t.Errorf("EgressMode of nothing must be open")
	}
	if !Routed(routedConfig()) || Routed(testConfig()) {
		t.Errorf("Routed does not follow the mode")
	}
}

// The other half of the shortfall, and the one nothing said before: the
// shared data plane stays routable under `routed` — the resources that named
// no VPC have egress on AWS too — which is only safe while a VPC-placed
// container is held to its VPC network alone.
func TestInternal_routedSaysWhenPlacementCannotHoldAContainerToItsVPC(t *testing.T) {
	// Given: routed on a host whose resolver never bound, so DataNetworks
	// puts every VPC-placed container on the shared plane as well.
	cfg := routedConfig()
	cfg.DNSListening = false
	resetWarningsOnce()

	// When: the data plane's isolation is decided.
	got := Internal(cfg)(context.Background(), nil)

	// Then: it stays routable — it must — and the decision carries the
	// warning that says what that costs the mode.
	if got.Internal {
		t.Fatalf("decision = %+v, want the data plane routable under routed", got)
	}
	if !slices.Contains(got.Warnings, RoutedPlacementNotEnforcedWarning) {
		t.Fatalf("Warnings = %q, want the placement shortfall", got.Warnings)
	}
	if PlacementEnforced(cfg) {
		t.Errorf("PlacementEnforced = true with the resolver down")
	}
	// And the container really is on both, which is what the warning claims.
	networks := DataNetworks(cfg, Placement{VPCNetwork: "overcast-vpc-vpc-1"})
	if !slices.Contains(networks, cfg.Network) {
		t.Errorf("DataNetworks = %q, want the shared plane among them", networks)
	}
}

func TestInternal_routedIsSilentWherePlacementHolds(t *testing.T) {
	// Given: the same mode on a host where placement is enforced.
	cfg := routedConfig()
	resetWarningsOnce()

	got := Internal(cfg)(context.Background(), nil)
	if got.Internal {
		t.Fatalf("decision = %+v, want the data plane routable under routed", got)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none where routed can keep its promise", got.Warnings)
	}
	if !PlacementEnforced(cfg) {
		t.Errorf("PlacementEnforced = false with the resolver listening")
	}

	// And no other mode says it, however the host is arranged: `open` grants
	// egress on purpose and `none` withholds it on every network alike.
	for _, mode := range []config.VPCEgressMode{config.VPCEgressOpen, config.VPCEgressNone} {
		other := testConfig()
		other.VPCEgress, other.DNSListening = mode, false
		resetWarningsOnce()
		if w := Internal(other)(context.Background(), nil).Warnings; len(w) != 0 {
			t.Errorf("mode %q warned %q", mode, w)
		}
	}
}
