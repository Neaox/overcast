package ec2

// vpc_egress_typed_test.go — the route-table and NAT-gateway mutations are
// answered by the typed dispatch (ec2TypedOps), not by the legacy handler map,
// so the re-placement hook has to be on the body that actually runs.
//
// This is not a hypothetical. The first cut of OVERCAST_VPC_EGRESS=routed put
// the hook on the legacy handlers alone. Every unit test passed — they call
// those handlers directly — and against a real daemon a CreateRoute moved
// nothing at all, because Service.DispatchQuery had gone to the typed twin.
// The tests below cover both halves: the typed bodies behave, and no future
// operation loses its hook on either path without something failing.

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/dataplane"
)

// The acceptance case, through the body a real request reaches: a NAT gateway
// and a route added to an isolated subnet move the containers already running
// there onto the VPC's egress network, and removing the route takes them off.
func TestTypedRouteMutations_moveRunningContainers(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	private := createSubnet(t, h, vpcID, "10.9.1.0/24")
	public := createSubnet(t, h, vpcID, "10.9.0.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", private)
	rtID := mainRouteTableID(t, h, vpcID)

	// When: a NAT gateway and a default route to it arrive, both through the
	// typed bodies.
	natResp, aerr := h.createNatGatewayTyped(ctx, &createNatGatewayReq{SubnetID: public})
	if aerr != nil {
		t.Fatalf("createNatGatewayTyped: %s", aerr.Message)
	}
	natID := natResp.NatGateway.NatGatewayID
	if _, aerr := h.createRouteTyped(ctx, &createRouteReq{
		RouteTableID: rtID, DestinationCidrBlock: "0.0.0.0/0", NatGatewayID: natID,
	}); aerr != nil {
		t.Fatalf("createRouteTyped: %s", aerr.Message)
	}

	// Then: the running container sits on the VPC's egress network, ranked as
	// its default-route source, and is still on its plane.
	vpc := storedVPC(t, h, vpcID)
	if vpc.EgressNetworkID == "" {
		t.Fatalf("no egress network recorded after the typed CreateRoute")
	}
	ep, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn")
	if !on {
		t.Fatalf("ctr-fn was not moved onto the egress network by the typed CreateRoute")
	}
	if ep.gwPriority != dataplane.EgressGatewayPriority {
		t.Errorf("gwPriority = %d, want %d", ep.gwPriority, dataplane.EgressGatewayPriority)
	}
	if _, still := f.endpoint(vpc.DockerNetworkID, "ctr-fn"); !still {
		t.Errorf("ctr-fn left the plane")
	}

	// And the typed DeleteRoute withdraws it again, in place.
	if _, aerr := h.deleteRouteTyped(ctx, &deleteRouteReq{
		RouteTableID: rtID, DestinationCidrBlock: "0.0.0.0/0",
	}); aerr != nil {
		t.Fatalf("deleteRouteTyped: %s", aerr.Message)
	}
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); on {
		t.Errorf("ctr-fn kept its route out after the typed DeleteRoute")
	}

	// And so does deleting a NAT gateway a route still names.
	if _, aerr := h.createRouteTyped(ctx, &createRouteReq{
		RouteTableID: rtID, DestinationCidrBlock: "0.0.0.0/0", NatGatewayID: natID,
	}); aerr != nil {
		t.Fatalf("createRouteTyped (again): %s", aerr.Message)
	}
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); !on {
		t.Fatalf("ctr-fn did not regain its route out")
	}
	if _, aerr := h.deleteNatGatewayTyped(ctx, &deleteNatGatewayReq{NatGatewayID: natID}); aerr != nil {
		t.Fatalf("deleteNatGatewayTyped: %s", aerr.Message)
	}
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); on {
		t.Errorf("ctr-fn kept a route out through a NAT gateway that was deleted")
	}
}

// The typed association calls decide which table a subnet reads, which is the
// same decision by another route.
func TestTypedAssociationMutations_moveRunningContainers(t *testing.T) {
	f := newFakeVPCDocker(t)
	h := routedHandler(t, f)
	ctx := context.Background()
	vpcID := createVPC(t, h, "10.9.0.0/16")
	subnetID := createSubnet(t, h, vpcID, "10.9.1.0/24")
	placeContainer(t, h, f, vpcID, "ctr-fn", "10.9.1.5", subnetID)

	// Given: the main table routes out, so the container has a route out.
	igwID := createIGW(t, h)
	if rec := ec2Call(t, h.AttachInternetGateway, gatewayParams(igwID, vpcID)); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, aerr := h.createRouteTyped(ctx, &createRouteReq{
		RouteTableID: mainRouteTableID(t, h, vpcID), DestinationCidrBlock: "0.0.0.0/0", GatewayID: igwID,
	}); aerr != nil {
		t.Fatalf("createRouteTyped: %s", aerr.Message)
	}
	vpc := storedVPC(t, h, vpcID)
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); !on {
		t.Fatalf("setup: ctr-fn has no route out from the main table")
	}

	// When: the subnet is associated with a table that has no default route.
	isolated, aerr := h.createRouteTableTyped(ctx, &createRouteTableReq{VpcID: vpcID})
	if aerr != nil {
		t.Fatalf("createRouteTableTyped: %s", aerr.Message)
	}
	assoc, aerr := h.associateRouteTableTyped(ctx, &associateRouteTableReq{
		RouteTableID: isolated.RouteTable.RouteTableID, SubnetID: subnetID,
	})
	if aerr != nil {
		t.Fatalf("associateRouteTableTyped: %s", aerr.Message)
	}

	// Then: the route out is withdrawn, and restored when the association goes.
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); on {
		t.Errorf("ctr-fn kept a route out after being associated with an isolated table")
	}
	if _, aerr := h.disassociateRouteTableTyped(ctx, &disassociateRouteTableReq{
		AssociationID: assoc.AssociationID,
	}); aerr != nil {
		t.Fatalf("disassociateRouteTableTyped: %s", aerr.Message)
	}
	if _, on := f.endpoint(vpc.EgressNetworkID, "ctr-fn"); !on {
		t.Errorf("ctr-fn did not regain the main table's route out after disassociation")
	}
}

// Every operation that changes what a subnet routes to has to revisit the
// VPC's placements on *both* dispatch paths. A source check rather than a
// behavioural one because the failure it guards against is a hook missing
// from the body a request actually reaches, which no test of the other body
// can see. Fix a miss by adding the call, never by editing the list.
func TestEveryRouteMutation_revisitsPlacementsOnBothDispatchPaths(t *testing.T) {
	for _, tc := range []struct {
		file  string
		funcs []string
	}{
		{"handler_routetables.go", []string{
			"CreateRoute", "DeleteRoute", "AssociateRouteTable", "DisassociateRouteTable", "DeleteRouteTable",
		}},
		{"handler_natgw.go", []string{"CreateNatGateway", "DeleteNatGateway"}},
		{"typed_logic.go", []string{
			"createRouteTyped", "deleteRouteTyped", "associateRouteTableTyped",
			"disassociateRouteTableTyped", "deleteRouteTableTyped",
			"createNatGatewayTyped", "deleteNatGatewayTyped",
		}},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		for _, fn := range tc.funcs {
			if !strings.Contains(funcBodySource(t, string(src), fn), "reconcileVPCEgress") {
				t.Errorf("%s in %s does not call reconcileVPCEgress — under OVERCAST_VPC_EGRESS=routed "+
					"it changes what a subnet routes to, and the containers already running there would "+
					"keep the egress their route tables no longer describe", fn, tc.file)
			}
		}
	}
}

// funcBodySource returns the source of the named method on *Handler, from its
// declaration to the closing brace in column 0.
func funcBodySource(t *testing.T, src, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^func \(h \*Handler\) ` + regexp.QuoteMeta(name) + `\(`)
	loc := re.FindStringIndex(src)
	if loc == nil {
		t.Fatalf("no method %s on *Handler in the source read", name)
	}
	rest := src[loc[0]:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}
