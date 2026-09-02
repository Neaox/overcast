package ec2

// vpc_egress_test.go — OVERCAST_VPC_EGRESS=routed (#1571): egress decided per
// subnet from its route table, carried by a second network per VPC, and
// revisited for running containers when a route table changes.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/state"
)

// ── the decision ─────────────────────────────────────────────────────────────

func viewFor(t *testing.T, subnets []*Subnet, tables []*RouteTable, igwAttachedTo, natState map[string]string) *egressView {
	t.Helper()
	v := &egressView{
		subnets:       map[string]*Subnet{},
		tables:        map[string][]*RouteTable{},
		igwAttachedTo: igwAttachedTo,
		natState:      natState,
	}
	if v.igwAttachedTo == nil {
		v.igwAttachedTo = map[string]string{}
	}
	if v.natState == nil {
		v.natState = map[string]string{}
	}
	for _, s := range subnets {
		v.subnets[s.SubnetID] = s
	}
	for _, rt := range tables {
		v.tables[rt.VpcID] = append(v.tables[rt.VpcID], rt)
	}
	return v
}

func mainTable(id, vpcID string, routes ...Route) *RouteTable {
	return &RouteTable{RouteTableID: id, VpcID: vpcID, Routes: routes,
		Associations: []RouteTableAssociation{{AssociationID: "rtbassoc-main", RouteTableID: id, Main: true}}}
}

func subnetTable(id, vpcID, subnetID string, routes ...Route) *RouteTable {
	return &RouteTable{RouteTableID: id, VpcID: vpcID, Routes: routes,
		Associations: []RouteTableAssociation{{AssociationID: "rtbassoc-" + subnetID, RouteTableID: id, SubnetID: subnetID}}}
}

func TestSubnetEgress_followsTheRouteTable(t *testing.T) {
	sub := &Subnet{SubnetID: "subnet-a", VpcID: "vpc-1"}
	igwRoute := Route{DestinationCidrBlock: "0.0.0.0/0", GatewayID: "igw-1"}
	natRoute := Route{DestinationCidrBlock: "0.0.0.0/0", NatGatewayID: "nat-1"}

	cases := map[string]struct {
		tables        []*RouteTable
		igwAttachedTo map[string]string
		natState      map[string]string
		wantRoutable  bool
		wantVia       string
		wantReason    string
	}{
		"a default route to an attached internet gateway grants": {
			tables: []*RouteTable{mainTable("rtb-main", "vpc-1", igwRoute)}, igwAttachedTo: map[string]string{"igw-1": "vpc-1"},
			wantRoutable: true, wantVia: "igw-1", wantReason: "internet gateway igw-1",
		},
		"a default route to a detached internet gateway is a blackhole": {
			tables:       []*RouteTable{mainTable("rtb-main", "vpc-1", igwRoute)},
			wantRoutable: false, wantVia: "igw-1", wantReason: "blackhole",
		},
		"a default route to a gateway attached elsewhere is a blackhole": {
			tables: []*RouteTable{mainTable("rtb-main", "vpc-1", igwRoute)}, igwAttachedTo: map[string]string{"igw-1": "vpc-2"},
			wantRoutable: false, wantReason: "not attached to vpc-1",
		},
		"a default route to an available NAT gateway grants": {
			tables: []*RouteTable{mainTable("rtb-main", "vpc-1", natRoute)}, natState: map[string]string{"nat-1": "available"},
			wantRoutable: true, wantVia: "nat-1", wantReason: "NAT gateway nat-1",
		},
		"a default route to a NAT gateway that does not exist is a blackhole": {
			tables:       []*RouteTable{mainTable("rtb-main", "vpc-1", natRoute)},
			wantRoutable: false, wantVia: "nat-1", wantReason: "does not exist (blackhole)",
		},
		"a default route to a deleted NAT gateway is a blackhole": {
			tables: []*RouteTable{mainTable("rtb-main", "vpc-1", natRoute)}, natState: map[string]string{"nat-1": "deleted"},
			wantRoutable: false, wantReason: "deleted (blackhole)",
		},
		"no default route withholds": {
			tables:       []*RouteTable{mainTable("rtb-main", "vpc-1", Route{DestinationCidrBlock: "10.0.0.0/16", GatewayID: "local"})},
			wantRoutable: false, wantReason: "no 0.0.0.0/0 route in rtb-main",
		},
		"a default route to a virtual private gateway is not egress": {
			tables:       []*RouteTable{mainTable("rtb-main", "vpc-1", Route{DestinationCidrBlock: "0.0.0.0/0", GatewayID: "vgw-1"})},
			wantRoutable: false, wantReason: "not an internet or NAT gateway",
		},
		"an explicit association wins over the main table": {
			tables: []*RouteTable{
				mainTable("rtb-main", "vpc-1", igwRoute),
				subnetTable("rtb-private", "vpc-1", "subnet-a", Route{DestinationCidrBlock: "10.0.0.0/16", GatewayID: "local"}),
			},
			igwAttachedTo: map[string]string{"igw-1": "vpc-1"},
			wantRoutable:  false, wantReason: "no 0.0.0.0/0 route in rtb-private",
		},
		"a VPC with no route table at all withholds": {
			wantRoutable: false, wantReason: "has no route table",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := viewFor(t, []*Subnet{sub}, tc.tables, tc.igwAttachedTo, tc.natState)
			got := v.subnetEgress("subnet-a")
			if got.Routable != tc.wantRoutable {
				t.Errorf("Routable = %t, want %t (%s)", got.Routable, tc.wantRoutable, got.Reason)
			}
			if tc.wantVia != "" && got.Via != tc.wantVia {
				t.Errorf("Via = %q, want %q", got.Via, tc.wantVia)
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want it to mention %q", got.Reason, tc.wantReason)
			}
		})
	}

	t.Run("an unknown subnet withholds", func(t *testing.T) {
		got := viewFor(t, nil, nil, nil, nil).subnetEgress("subnet-nope")
		if got.Routable || !strings.Contains(got.Reason, "does not exist") {
			t.Errorf("decision = %+v", got)
		}
	})
}

// A container in several subnets gets a route out when any of them grants
// one, and the decision names the subnet that did.
func TestPlacementEgress_anySubnetGrants(t *testing.T) {
	v := viewFor(t,
		[]*Subnet{{SubnetID: "subnet-isolated", VpcID: "vpc-1"}, {SubnetID: "subnet-nat", VpcID: "vpc-1"}},
		[]*RouteTable{
			mainTable("rtb-main", "vpc-1"),
			subnetTable("rtb-nat", "vpc-1", "subnet-nat", Route{DestinationCidrBlock: "0.0.0.0/0", NatGatewayID: "nat-1"}),
		},
		nil, map[string]string{"nat-1": "available"})

	if got := v.placementEgress([]string{"subnet-isolated", "subnet-nat"}); !got.Routable || got.SubnetID != "subnet-nat" {
		t.Errorf("mixed placement = %+v, want granted by subnet-nat", got)
	}
	if got := v.placementEgress([]string{"subnet-isolated"}); got.Routable {
		t.Errorf("isolated placement = %+v, want withheld", got)
	}
	if got := v.placementEgress(nil); got.Routable || !strings.Contains(got.Reason, "named no subnet") {
		t.Errorf("subnet-less placement = %+v, want withheld with the reason", got)
	}
}

// ── against the (fake) daemon ────────────────────────────────────────────────

func routedHandler(t *testing.T, f *fakeVPCDocker) *Handler {
	t.Helper()
	h := vpcDockerHandler(t, f, "shared")
	h.cfg.VPCEgress = config.VPCEgressRouted
	return h
}

func createSubnet(t *testing.T, h *Handler, vpcID, cidr string) string {
	t.Helper()
	rec := ec2Call(t, h.CreateSubnet, url.Values{"VpcId": {vpcID}, "CidrBlock": {cidr}, "AvailabilityZone": {"us-east-1a"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateSubnet: %d %s", rec.Code, rec.Body.String())
	}
	return xmlValue(t, rec.Body.String(), "subnetId")
}

func mainRouteTableID(t *testing.T, h *Handler, vpcID string) string {
	t.Helper()
	tables, aerr := h.store.listRouteTables(context.Background())
	if aerr != nil {
		t.Fatal(aerr.Message)
	}
	for _, rt := range tables {
		if rt.VpcID != vpcID {
			continue
		}
		for _, a := range rt.Associations {
			if a.Main {
				return rt.RouteTableID
			}
		}
	}
	t.Fatalf("no main route table for %s", vpcID)
	return ""
}

func createRoute(t *testing.T, h *Handler, rtID string, target url.Values) {
	t.Helper()
	params := url.Values{"RouteTableId": {rtID}, "DestinationCidrBlock": {"0.0.0.0/0"}}
	for k, v := range target {
		params[k] = v
	}
	if rec := ec2Call(t, h.CreateRoute, params); rec.Code != http.StatusOK {
		t.Fatalf("CreateRoute: %d %s", rec.Code, rec.Body.String())
	}
}

func deleteRoute(t *testing.T, h *Handler, rtID string) {
	t.Helper()
	if rec := ec2Call(t, h.DeleteRoute, url.Values{"RouteTableId": {rtID}, "DestinationCidrBlock": {"0.0.0.0/0"}}); rec.Code != http.StatusOK {
		t.Fatalf("DeleteRoute: %d %s", rec.Code, rec.Body.String())
	}
}

func createNAT(t *testing.T, h *Handler, subnetID string) string {
	t.Helper()
	rec := ec2Call(t, h.CreateNatGateway, url.Values{"SubnetId": {subnetID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateNatGateway: %d %s", rec.Code, rec.Body.String())
	}
	return xmlValue(t, rec.Body.String(), "natGatewayId")
}

// placeContainer stands in for a service placing a container: it sits on the
// VPC's plane and its placement is on record, as dataplane.Attach leaves it.
func placeContainer(t *testing.T, h *Handler, f *fakeVPCDocker, vpcID, containerID, ip string, subnetIDs ...string) {
	t.Helper()
	vpc := storedVPC(t, h, vpcID)
	f.attach(vpc.DockerNetworkID, containerID, ip)
	h.recordPlacement(context.Background(), containerID, dataplane.Placement{VPCNetwork: vpc.DockerNetworkID, VPCID: vpcID, SubnetIDs: subnetIDs})
}

func TestEgressNetworkForSubnets_createsTheEgressNetworkOnDemand(t *testing.T) {
	// Given: under routed, a VPC whose main route table sends 0.0.0.0/0 to
	// an attached internet gateway — a public subnet.
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	igwID := createIGW(t, h)
	if rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID)); rec.Code != http.StatusOK {
		t.Fatalf("AttachInternetGateway: %d %s", rec.Code, rec.Body.String())
	}
	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"GatewayId": {igwID}})

	// Then: the plane is still --internal — under routed the gateway never
	// decides the plane — and no egress network exists until a placement
	// needs one.
	plane := f.network("overcast-vpc-" + vpcID)
	if plane == nil || !plane.internal {
		t.Fatalf("plane after attach = %+v, want it kept internal under routed", plane)
	}
	if f.countNetworks("-egress") != 0 {
		t.Fatalf("an egress network exists before anything was placed")
	}

	// When: a container is placed in the public subnet.
	netID, err := h.egressNetworkForSubnets(ctx, vpcID, []string{subnetID})
	if err != nil {
		t.Fatalf("egressNetworkForSubnets: %v", err)
	}

	// Then: the VPC's egress network exists — routable, masquerading, on a
	// /24 carved from the pool, labelled as the VPC's egress role — and the
	// record names it.
	egress := f.network("overcast-vpc-" + vpcID + "-egress")
	if egress == nil || egress.id != netID {
		t.Fatalf("egress network = %+v, want it created as %s", egress, netID)
	}
	if egress.internal {
		t.Errorf("the egress network is --internal")
	}
	if egress.subnet != "198.18.0.0/24" {
		t.Errorf("egress subnet = %q, want the first /24 of the default pool", egress.subnet)
	}
	if egress.options[docker.OptionIPMasquerade] != "true" {
		t.Errorf("egress options = %v, want masquerade pinned on", egress.options)
	}
	if egress.labels[docker.LabelVPCRole] != docker.VPCRoleEgress || egress.labels["overcast.vpc-id"] != vpcID {
		t.Errorf("egress labels = %v", egress.labels)
	}
	if got := storedVPC(t, h, vpcID); got.EgressNetworkID != netID || got.EgressNetworkName != egress.name || got.EgressCidrBlock != "198.18.0.0/24" {
		t.Errorf("stored VPC egress = %q %q %q", got.EgressNetworkID, got.EgressNetworkName, got.EgressCidrBlock)
	}

	// And a second placement reuses it rather than creating another.
	again, err := h.egressNetworkForSubnets(ctx, vpcID, []string{subnetID})
	if err != nil || again != netID {
		t.Errorf("second placement = %q, %v; want the same network %s", again, err, netID)
	}
	if f.countNetworks("-egress") != 1 {
		t.Errorf("egress networks = %d, want 1", f.countNetworks("-egress"))
	}

	// And a subnet with no route out gets no network at all.
	private := createSubnet(t, h, vpcID, "10.9.2.0/24")
	rec := ec2Call(t, h.CreateRouteTable, url.Values{"VpcId": {vpcID}})
	privateRT := xmlValue(t, rec.Body.String(), "routeTableId")
	if rec := ec2Call(t, h.AssociateRouteTable, url.Values{"RouteTableId": {privateRT}, "SubnetId": {private}}); rec.Code != http.StatusOK {
		t.Fatalf("AssociateRouteTable: %d %s", rec.Code, rec.Body.String())
	}
	if got, err := h.egressNetworkForSubnets(ctx, vpcID, []string{private}); err != nil || got != "" {
		t.Errorf("isolated subnet placement = %q, %v; want no egress network", got, err)
	}
}

func TestEgressNetworkForSubnets_isInertOutsideRouted(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := vpcDockerHandler(t, f, "shared")
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	igwID := createIGW(t, h)
	if rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID)); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"GatewayId": {igwID}})

	if got, err := h.egressNetworkForSubnets(context.Background(), vpcID, []string{subnetID}); err != nil || got != "" {
		t.Errorf("under open = %q, %v; want nothing", got, err)
	}
	if f.countNetworks("-egress") != 0 {
		t.Errorf("an egress network was created under open")
	}
	h.recordPlacement(context.Background(), "ctr", dataplane.Placement{VPCID: vpcID, SubnetIDs: []string{subnetID}})
	if placements, _ := h.store.listPlacements(context.Background(), ""); len(placements) != 0 {
		t.Errorf("placements recorded under open: %d", len(placements))
	}
}

// The acceptance case: a NAT gateway and a route added to an isolated subnet
// grant egress without a restart — to containers already running there.
func TestCreateRoute_movesRunningContainersOntoTheEgressNetwork(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	private := createSubnet(t, h, vpcID, "10.9.1.0/24")
	public := createSubnet(t, h, vpcID, "10.9.0.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", private)
	placeContainer(t, h, f, vpcID, "ctr-db", "10.9.1.6", private)
	rtID := mainRouteTableID(t, h, vpcID)

	// When: a NAT gateway appears and the main table routes 0.0.0.0/0 to it.
	natID := createNAT(t, h, public)
	if f.countNetworks("-egress") != 0 {
		t.Fatalf("a NAT gateway with no route to it created an egress network")
	}
	createRoute(t, h, rtID, url.Values{"NatGatewayId": {natID}})

	// Then: both running containers now sit on the VPC's egress network,
	// ranked as their default-route source, with their plane attachment
	// untouched.
	vpc := storedVPC(t, h, vpcID)
	if vpc.EgressNetworkID == "" {
		t.Fatalf("no egress network recorded after the route")
	}
	for _, id := range []string{"ctr-fn", "ctr-db"} {
		ep, on := f.endpoint(vpc.EgressNetworkID, id)
		if !on {
			t.Errorf("%s is not on the egress network after CreateRoute", id)
			continue
		}
		if ep.gwPriority != dataplane.EgressGatewayPriority {
			t.Errorf("%s joined the egress network with priority %d, want %d", id, ep.gwPriority, dataplane.EgressGatewayPriority)
		}
		if _, still := f.endpoint(vpc.DockerNetworkID, id); !still {
			t.Errorf("%s left the plane", id)
		}
	}
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("problems = %+v, want none", got)
	}

	// When: the route goes away again.
	deleteRoute(t, h, rtID)

	// Then: the route out is withdrawn, in place.
	for _, id := range []string{"ctr-fn", "ctr-db"} {
		if _, on := f.endpoint(vpc.EgressNetworkID, id); on {
			t.Errorf("%s is still on the egress network after DeleteRoute", id)
		}
		if _, still := f.endpoint(vpc.DockerNetworkID, id); !still {
			t.Errorf("%s left the plane", id)
		}
	}

	// And a route to a NAT gateway that is then deleted is a blackhole.
	createRoute(t, h, rtID, url.Values{"NatGatewayId": {natID}})
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); !on {
		t.Fatalf("ctr-fn did not regain its route out")
	}
	if rec := ec2Call(t, h.DeleteNatGateway, url.Values{"NatGatewayId": {natID}}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); on {
		t.Errorf("ctr-fn kept its route out through a deleted NAT gateway")
	}
	_ = ctx
}

// Under routed the gateway flip recreates nothing — the plane stays internal
// — and what an attach or detach changes is which subnets route out.
func TestAttachInternetGateway_routedKeepsThePlaneAndMovesContainers(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", subnetID)
	igwID := createIGW(t, h)
	// The route first, while the gateway is detached: a blackhole.
	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"GatewayId": {igwID}})
	before := storedVPC(t, h, vpcID)
	if before.EgressNetworkID != "" {
		t.Fatalf("a route to a detached gateway earned an egress network")
	}

	// When: the gateway is attached.
	if rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID)); rec.Code != http.StatusOK {
		t.Fatalf("AttachInternetGateway: %d %s", rec.Code, rec.Body.String())
	}

	// Then: the plane is the same network, still internal; the container
	// gained the egress network.
	after := storedVPC(t, h, vpcID)
	if after.DockerNetworkID != before.DockerNetworkID {
		t.Errorf("the plane was recreated by the attach: %s → %s", before.DockerNetworkID, after.DockerNetworkID)
	}
	if plane := f.network("overcast-vpc-" + vpcID); plane == nil || !plane.internal {
		t.Errorf("plane = %+v, want internal", plane)
	}
	if _, on := f.endpoint(after.EgressNetworkID, "ctr-fn"); after.EgressNetworkID == "" || !on {
		t.Errorf("ctr-fn is not on the egress network after the attach")
	}

	// When: detached again.
	if rec := ec2Call(t, h.DetachInternetGateway, gatewayParams(igwID, vpcID)); rec.Code != http.StatusOK {
		t.Fatalf("DetachInternetGateway: %d %s", rec.Code, rec.Body.String())
	}
	if _, on := f.endpoint(after.EgressNetworkID, "ctr-fn"); on {
		t.Errorf("ctr-fn kept its route out after the detach")
	}
}

// A move that Docker refuses reaches the advisories, and a later success
// clears it.
func TestReconcileVPCEgress_reportsAMoveItCannotMake(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", subnetID)
	f.failConnect = map[string]bool{"ctr-fn": true}
	natID := createNAT(t, h, subnetID)
	rtID := mainRouteTableID(t, h, vpcID)

	createRoute(t, h, rtID, url.Values{"NatGatewayId": {natID}})

	problems := h.networkProblems()
	if len(problems) != 1 || problems[0].VpcID != vpcID || !strings.Contains(problems[0].Detail, "ctr-fn") {
		t.Fatalf("problems = %+v, want one naming ctr-fn", problems)
	}

	f.failConnect = nil
	deleteRoute(t, h, rtID)
	createRoute(t, h, rtID, url.Values{"NatGatewayId": {natID}})
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("problems = %+v, want the successful move to clear it", got)
	}
}

// A container that is gone is pruned from the record rather than reported.
func TestReconcileVPCEgress_prunesPlacementsOfContainersThatAreGone(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	placeContainer(t, h, f, vpcID, "ctr-gone", "10.9.1.5", subnetID)
	f.missing = map[string]bool{"ctr-gone": true}
	natID := createNAT(t, h, subnetID)

	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"NatGatewayId": {natID}})

	if placements, _ := h.store.listPlacements(context.Background(), vpcID); len(placements) != 0 {
		t.Errorf("placements = %d, want the gone container pruned", len(placements))
	}
	if got := h.networkProblems(); len(got) != 0 {
		t.Errorf("problems = %+v, want none for a container that is simply gone", got)
	}
}

// After a restart, egress networks are adopted by name — never mistaken for
// the plane by the strategies — and placements are revisited against route
// tables that changed while Overcast was down.
func TestReconcileNetworks_routedAdoptsEgressNetworksAndRevisitsPlacements(t *testing.T) {
	f := newFakeVPCDocker(t)
	store := state.NewMemoryStore()
	h := vpcDockerHandlerOn(t, f, "shared", store)
	h.cfg.VPCEgress = config.VPCEgressRouted
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", subnetID)
	natID := createNAT(t, h, subnetID)
	rtID := mainRouteTableID(t, h, vpcID)
	createRoute(t, h, rtID, url.Values{"NatGatewayId": {natID}})
	before := storedVPC(t, h, vpcID)
	if _, on := f.endpoint(before.EgressNetworkID, "ctr-fn"); !on {
		t.Fatalf("setup: ctr-fn is not on the egress network")
	}

	// While "down": the route is removed straight from the store, so nothing
	// moved the container.
	rt, aerr := h.store.getRouteTable(ctx, rtID)
	if aerr != nil {
		t.Fatal(aerr.Message)
	}
	rt.Routes = rt.Routes[:1]
	if aerr := h.store.putRouteTable(ctx, rt); aerr != nil {
		t.Fatal(aerr.Message)
	}
	// And the record forgets the egress network's id, as an older record
	// would not have it.
	before.EgressNetworkID, before.EgressNetworkName = "", ""
	if aerr := h.store.putVPC(ctx, before); aerr != nil {
		t.Fatal(aerr.Message)
	}

	// When: a new handler over the same store reconciles against the daemon.
	h2 := vpcDockerHandlerOn(t, f, "shared", store)
	h2.cfg.VPCEgress = config.VPCEgressRouted
	h2.reconcileNetworks(ctx, f.summaries())

	// Then: the plane is still the plane and the egress network was adopted
	// as the egress network, not the other way round.
	after := storedVPC(t, h2, vpcID)
	if after.DockerNetworkID != before.DockerNetworkID {
		t.Errorf("plane = %s after reconcile, want %s", after.DockerNetworkID, before.DockerNetworkID)
	}
	if egress := f.network("overcast-vpc-" + vpcID + "-egress"); egress == nil || after.EgressNetworkID != egress.id {
		t.Errorf("egress network record = %q, want the adopted %+v", after.EgressNetworkID, egress)
	}
	if f.countNetworks("-egress") != 1 {
		t.Errorf("egress networks = %d, want the one adopted", f.countNetworks("-egress"))
	}
	// And the container lost the route out its route table no longer grants.
	if _, on := f.endpoint(after.EgressNetworkID, "ctr-fn"); on {
		t.Errorf("ctr-fn kept a route out the route table withdrew while Overcast was down")
	}
	if _, still := f.endpoint(after.DockerNetworkID, "ctr-fn"); !still {
		t.Errorf("ctr-fn left the plane")
	}
}

// Under any other mode, an egress network left by a routed run is taken
// down — its containers off it first — because under `none` it is a leak.
func TestReconcileNetworks_otherModesRemoveLeftoverEgressNetworks(t *testing.T) {
	f := newFakeVPCDocker(t)
	store := state.NewMemoryStore()
	h := vpcDockerHandlerOn(t, f, "shared", store)
	h.cfg.VPCEgress = config.VPCEgressRouted
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", subnetID)
	natID := createNAT(t, h, subnetID)
	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"NatGatewayId": {natID}})
	egressID := storedVPC(t, h, vpcID).EgressNetworkID
	if _, on := f.endpoint(egressID, "ctr-fn"); egressID == "" || !on {
		t.Fatalf("setup: no egress attachment")
	}

	// When: the same store comes up under `none`.
	h2 := vpcDockerHandlerOn(t, f, "shared", store)
	h2.cfg.VPCEgress = config.VPCEgressNone
	h2.reconcileNetworks(ctx, f.summaries())

	// Then: the egress network is gone, the container is off it, and the
	// record no longer names it.
	if f.has(egressID) {
		t.Errorf("the egress network survived a reconcile under none")
	}
	if got := storedVPC(t, h2, vpcID); got.EgressNetworkID != "" || got.EgressNetworkName != "" {
		t.Errorf("record still names the egress network: %+v", got)
	}
	if _, still := f.endpoint(storedVPC(t, h2, vpcID).DockerNetworkID, "ctr-fn"); !still {
		t.Errorf("ctr-fn left the plane")
	}
}

func TestDeleteVpc_removesTheEgressNetworkAndItsPlacements(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", subnetID)
	natID := createNAT(t, h, subnetID)
	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"NatGatewayId": {natID}})
	vpc := storedVPC(t, h, vpcID)

	// The container has to be off the networks for the VPC to be deletable at
	// all (DependencyViolation is about records, not endpoints; the fake
	// refuses to remove a network with endpoints, as Docker does).
	f.mu.Lock()
	delete(f.networks[vpc.DockerNetworkID].endpoints, "ctr-fn")
	f.mu.Unlock()
	h.forgetVPCNetwork(ctx, vpc)

	if f.has(vpc.EgressNetworkID) {
		t.Errorf("the egress network survived the VPC")
	}
	if placements, _ := h.store.listPlacements(ctx, vpcID); len(placements) != 0 {
		t.Errorf("placements = %d after the VPC was forgotten", len(placements))
	}
}

func TestAllocateEgressCIDR_skipsTakenAndOverlappingRanges(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	ctx := context.Background()
	put := func(v *VPC) {
		t.Helper()
		if aerr := h.store.putVPC(ctx, v); aerr != nil {
			t.Fatal(aerr.Message)
		}
	}
	put(&VPC{VpcID: "vpc-a", CidrBlock: "10.0.0.0/16", EgressCidrBlock: "198.18.0.0/24"})
	// A VPC whose own range sits in the pool: the next /24 must step over it.
	put(&VPC{VpcID: "vpc-b", CidrBlock: "198.18.1.0/24"})

	got, err := h.allocateEgressCIDR(ctx)
	if err != nil || got != "198.18.2.0/24" {
		t.Errorf("allocateEgressCIDR = %q, %v; want 198.18.2.0/24", got, err)
	}

	h.cfg.VPCEgressPool = "198.18.0.0/24"
	if got, err := h.allocateEgressCIDR(ctx); err == nil || !strings.Contains(err.Error(), "OVERCAST_VPC_EGRESS_POOL") {
		t.Errorf("exhausted pool = %q, %v; want an error naming the setting", got, err)
	}
}

// The pool running out fails the placement loudly, and reaches the
// advisories, rather than placing the container without the route out its
// route table grants.
func TestEgressNetworkForSubnets_anExhaustedPoolFailsThePlacement(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	h.cfg.VPCEgressPool = "198.18.0.0/24"
	ctx := context.Background()
	if aerr := h.store.putVPC(ctx, &VPC{VpcID: "vpc-taken", CidrBlock: "10.0.0.0/16", EgressCidrBlock: "198.18.0.0/24"}); aerr != nil {
		t.Fatal(aerr.Message)
	}
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	natID := createNAT(t, h, subnetID)
	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"NatGatewayId": {natID}})

	_, err := h.egressNetworkForSubnets(ctx, vpcID, []string{subnetID})
	if err == nil || !strings.Contains(err.Error(), "OVERCAST_VPC_EGRESS_POOL") || !strings.Contains(err.Error(), natID) {
		t.Fatalf("err = %v, want the pool named and the route that granted egress", err)
	}
	problems := h.networkProblems()
	if len(problems) != 1 || problems[0].VpcID != vpcID {
		t.Errorf("problems = %+v, want one for %s", problems, vpcID)
	}
}

// The issue's second acceptance criterion, in its own words: a NAT gateway
// and a route added to an isolated subnet grant egress to containers placed
// there *afterwards*, without a restart. The first criterion's other half —
// that the same subnet grants nothing before the route — is asserted here
// too, since the two are the same call a moment apart.
func TestNatGatewayAndRoute_grantEgressToContainersPlacedAfterwards(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	isolated := createSubnet(t, h, vpcID, "10.9.1.0/24")
	public := createSubnet(t, h, vpcID, "10.9.0.0/24")

	// Given: the subnet's route table has no 0.0.0.0/0 route, so a container
	// placed in it now gets no route out.
	if got, err := h.egressNetworkForSubnets(ctx, vpcID, []string{isolated}); err != nil || got != "" {
		t.Fatalf("before the route: egress network = %q, %v; want none", got, err)
	}

	// When: a NAT gateway is created and the table routes 0.0.0.0/0 to it.
	natID := createNAT(t, h, public)
	createRoute(t, h, mainRouteTableID(t, h, vpcID), url.Values{"NatGatewayId": {natID}})

	// Then: a container placed in the same subnet afterwards is given the
	// VPC's egress network — no restart, and the daemon holds exactly one.
	netID, err := h.egressNetworkForSubnets(ctx, vpcID, []string{isolated})
	if err != nil {
		t.Fatalf("after the route: %v", err)
	}
	egress := f.network("overcast-vpc-" + vpcID + "-egress")
	if egress == nil || netID != egress.id {
		t.Fatalf("after the route: egress network = %q, want the VPC's %+v", netID, egress)
	}
	if f.countNetworks("-egress") != 1 {
		t.Errorf("egress networks = %d, want 1", f.countNetworks("-egress"))
	}

	// And a placement that names the subnets in the other order, or names
	// both, gets the same answer — any granting subnet grants.
	if got, err := h.egressNetworkForSubnets(ctx, vpcID, []string{isolated, public}); err != nil || got != netID {
		t.Errorf("multi-subnet placement = %q, %v; want %s", got, err, netID)
	}
}

// The startup reconcile covers every region, and one region's pass must not
// take another region's egress network for litter. The plane pass learned
// this in #1577; the egress pass shares one index across the regions for the
// same reason, and only what no region claimed is swept.
func TestReconcileNetworks_routedKeepsEveryRegionsEgressNetwork(t *testing.T) {
	f := newFakeVPCDocker(t)
	store := state.NewMemoryStore()
	h := vpcDockerHandlerOn(t, f, "shared", store)
	h.cfg.VPCEgress = config.VPCEgressRouted
	mine := h.instances.Resolve(context.Background())
	if mine == "" {
		t.Fatal("the handler has no instance identity; the sweep below could not act")
	}

	// Given: a VPC in the default region with a real egress network, earned
	// by a NAT-routed subnet.
	homeVPC := createVPC(t, h, "10.9.0.0/16")
	homeSubnet := createSubnet(t, h, homeVPC, "10.9.1.0/24")
	placeContainer(t, h, f, homeVPC, "ctr-home", "10.9.1.5", homeSubnet)
	natID := createNAT(t, h, homeSubnet)
	createRoute(t, h, mainRouteTableID(t, h, homeVPC), url.Values{"NatGatewayId": {natID}})
	homeEgress := "overcast-vpc-" + homeVPC + "-egress"
	if f.network(homeEgress) == nil {
		t.Fatalf("setup: the default region's VPC has no egress network")
	}

	// And a VPC in another region whose egress network is on the daemon but
	// absent from this region's view of the store.
	awayVPC := &VPC{VpcID: otherVPCID, CidrBlock: "10.20.0.0/16", State: "available", NetworkStatus: vpcNetworkStatusOK}
	awayVPC.DockerNetworkID = f.add("overcast-vpc-"+otherVPCID, "10.20.0.0/16", egressLabels(otherVPCID, mine, docker.VPCRolePlane))
	awayEgress := "overcast-vpc-" + otherVPCID + "-egress"
	awayVPC.EgressNetworkID = f.add(awayEgress, "198.18.9.0/24", egressLabels(otherVPCID, mine, docker.VPCRoleEgress))
	awayVPC.EgressNetworkName, awayVPC.EgressCidrBlock = awayEgress, "198.18.9.0/24"
	if aerr := h.store.putVPC(regionCtx(otherRegion), awayVPC); aerr != nil {
		t.Fatal(aerr.Message)
	}

	// And an egress network for a VPC no region holds at all.
	orphan := f.add("overcast-vpc-vpc-deadbeef-egress", "198.18.99.0/24",
		egressLabels("vpc-deadbeef", mine, docker.VPCRoleEgress))

	// When: one full reconcile runs.
	h.reconcileNetworks(context.Background(), f.summaries())

	// Then: both regions keep their egress network, and each record names it.
	for _, tc := range []struct{ region, vpcID, name string }{
		{"", homeVPC, homeEgress},
		{otherRegion, otherVPCID, awayEgress},
	} {
		if f.network(tc.name) == nil {
			t.Errorf("%s was swept by another region's pass", tc.name)
			continue
		}
		ctx := context.Background()
		if tc.region != "" {
			ctx = regionCtx(tc.region)
		}
		vpc, aerr := h.store.getVPC(ctx, tc.vpcID)
		if aerr != nil {
			t.Fatal(aerr.Message)
		}
		if vpc.EgressNetworkName != tc.name || vpc.EgressNetworkID != f.network(tc.name).id {
			t.Errorf("%s record = %q/%q, want %q", tc.vpcID, vpc.EgressNetworkID, vpc.EgressNetworkName, tc.name)
		}
	}

	// And only the one no VPC anywhere names is gone.
	if f.has(orphan) {
		t.Errorf("an egress network no VPC in any region names survived the sweep")
	}
}

// egressLabels is the label set a VPC network of the given role carries when
// this instance created it.
func egressLabels(vpcID, owner, role string) map[string]string {
	labels := docker.ManagedLabels("ec2", vpcID)
	labels["overcast.vpc-id"] = vpcID
	labels[docker.LabelVPCRole] = role
	labels[docker.LabelInstance] = owner
	return labels
}
