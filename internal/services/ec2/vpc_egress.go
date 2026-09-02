package ec2

// vpc_egress.go — OVERCAST_VPC_EGRESS=routed (#1571): a container's route out
// is decided per subnet, from the route table associated with it, and carried
// by a second network per VPC.
//
// The shape, and why it is this shape. A VPC's plane (config.VPCNetwork) stays
// one `--internal` bridge that every container in the VPC joins, whatever its
// subnet routes to: an isolated database and a NAT-routed function in one VPC
// reach each other on AWS through the local route, and they could not if each
// egress class were its own bridge. So the plane carries the names and the
// intra-VPC traffic, and a container whose subnet has a `0.0.0.0/0` route to an
// attached internet gateway or an available NAT gateway *also* joins the VPC's
// egress network (config.VPCEgressNetwork): a routable, masquerading bridge
// carrying nothing but the default route. With the control plane `--internal`
// as well (dataplane.ControlPlaneInternal), that attachment is the only route
// out a container can have, so a subnet with no default route fails outbound
// connections with ENETUNREACH — which is the missing NAT gateway, caught
// locally.
//
// Two consequences shape everything below:
//
//   - Nothing is recreated to change a route. Granting or withdrawing egress
//     is one connect or disconnect on the egress network; the plane, the
//     container's address on it, and its DNS names are untouched. That is what
//     makes re-placement on a route-table change safe on a hot path, where the
//     internet-gateway flip (handler_vpc_docker.go) is the remove-and-recreate
//     it is because Docker cannot change `--internal` in place.
//   - The egress network's subnet is pinned, carved from config.VPCEgressPool.
//     A network that drew on Docker's own address pools would count against
//     the ~31 a stock daemon has in total, shared with every other tool on the
//     machine — the ceiling that made this mode unsafe to ship as a second
//     network per VPC. See docs/networking.md § The address-pool ceiling.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// defaultRouteCIDR is the destination of a route table's route out.
const defaultRouteCIDR = "0.0.0.0/0"

// egressNetworkReason is what /_overcast/health and `overcast network status`
// say beside an egress network.
const egressNetworkReason = "OVERCAST_VPC_EGRESS=routed: the VPC's egress network — routable; joined, " +
	"in addition to the VPC's internal plane, by every container whose subnet routes 0.0.0.0/0 to " +
	"an internet or NAT gateway, and the source of their default route"

// egressDecision is the answer for one subnet, or for the set of subnets one
// container was placed in: whether its route table grants a route out, and
// what decided it, in words a log line can carry.
type egressDecision struct {
	Routable bool

	// SubnetID and RouteTableID are the subnet that decided and the table it
	// was read from. For a routable set they name the granting subnet; for an
	// isolated one, the first subnet looked at.
	SubnetID     string
	RouteTableID string

	// Via is the gateway the default route names, when it names one: the
	// internet gateway or NAT gateway ID.
	Via string

	// Reason is the decision in one line.
	Reason string
}

// egressView is every store fact an egress decision reads, taken once so a
// pass over many subnets or placements does not re-read the region per
// subnet.
type egressView struct {
	subnets map[string]*Subnet
	tables  map[string][]*RouteTable // by VPC ID

	// igwAttachedTo maps an internet gateway ID to the VPC it is attached to,
	// or is absent for a detached gateway. A default route to a gateway that
	// is not attached to the subnet's VPC is a blackhole on AWS, and here.
	igwAttachedTo map[string]string

	// natState maps a NAT gateway ID to its state. A route to a NAT gateway
	// that is deleted, or gone, is a blackhole.
	natState map[string]string
}

func (h *Handler) egressView(ctx context.Context) (*egressView, *protocol.AWSError) {
	subnets, aerr := h.store.listSubnets(ctx)
	if aerr != nil {
		return nil, aerr
	}
	tables, aerr := h.store.listRouteTables(ctx)
	if aerr != nil {
		return nil, aerr
	}
	igws, aerr := h.store.listInternetGateways(ctx)
	if aerr != nil {
		return nil, aerr
	}
	nats, aerr := h.store.listNatGateways(ctx)
	if aerr != nil {
		return nil, aerr
	}
	v := &egressView{
		subnets:       make(map[string]*Subnet, len(subnets)),
		tables:        make(map[string][]*RouteTable, len(tables)),
		igwAttachedTo: make(map[string]string, len(igws)),
		natState:      make(map[string]string, len(nats)),
	}
	for _, s := range subnets {
		v.subnets[s.SubnetID] = s
	}
	for _, rt := range tables {
		v.tables[rt.VpcID] = append(v.tables[rt.VpcID], rt)
	}
	for _, igw := range igws {
		for _, att := range igw.Attachments {
			if att.State == "attached" {
				v.igwAttachedTo[igw.InternetGatewayID] = att.VpcID
			}
		}
	}
	for _, n := range nats {
		v.natState[n.NatGatewayID] = n.State
	}
	return v, nil
}

// routeTableFor returns the route table a subnet uses: the one explicitly
// associated with it, else its VPC's main table, else nil. That is AWS's own
// rule — every subnet has exactly one, and the main table is the default.
func (v *egressView) routeTableFor(sub *Subnet) *RouteTable {
	var main *RouteTable
	for _, rt := range v.tables[sub.VpcID] {
		for _, a := range rt.Associations {
			if a.SubnetID == sub.SubnetID {
				return rt
			}
			if a.Main && a.SubnetID == "" {
				main = rt
			}
		}
	}
	return main
}

// subnetEgress decides one subnet from its route table.
//
// The rule is the issue's table: a `0.0.0.0/0` route to an internet gateway
// attached to this VPC, or to a NAT gateway that is available, grants a route
// out; no default route withholds one; a default route whose target is gone or
// not attached is a blackhole, which withholds too. A default route to any
// other kind of target — a virtual private gateway, a peering connection, an
// instance — is not modelled as egress: none of those reaches the internet
// through anything Overcast runs.
func (v *egressView) subnetEgress(subnetID string) egressDecision {
	sub, ok := v.subnets[subnetID]
	if !ok {
		return egressDecision{SubnetID: subnetID, Reason: "subnet " + subnetID + " does not exist, so no route table grants it a route out"}
	}
	rt := v.routeTableFor(sub)
	if rt == nil {
		return egressDecision{SubnetID: subnetID, Reason: "subnet " + subnetID + " has no route table, not even a main one"}
	}
	d := egressDecision{SubnetID: subnetID, RouteTableID: rt.RouteTableID}
	for _, route := range rt.Routes {
		if route.DestinationCidrBlock != defaultRouteCIDR {
			continue
		}
		switch {
		case route.NatGatewayID != "":
			d.Via = route.NatGatewayID
			// "available" only. On AWS a route to a NAT gateway that is still
			// pending drops packets, and Overcast writes no other state
			// (handler_natgw.go, typed_logic.go) — so anything else is a
			// gateway that is going away, or one this region does not have.
			switch v.natState[route.NatGatewayID] {
			case "available":
				d.Routable = true
				d.Reason = fmt.Sprintf("subnet %s routes 0.0.0.0/0 to NAT gateway %s in %s", subnetID, route.NatGatewayID, rt.RouteTableID)
			case "":
				d.Reason = fmt.Sprintf("subnet %s routes 0.0.0.0/0 to NAT gateway %s in %s, which does not exist (blackhole)", subnetID, route.NatGatewayID, rt.RouteTableID)
			default:
				d.Reason = fmt.Sprintf("subnet %s routes 0.0.0.0/0 to NAT gateway %s in %s, which is %s (blackhole)", subnetID, route.NatGatewayID, rt.RouteTableID, v.natState[route.NatGatewayID])
			}
			return d
		case strings.HasPrefix(route.GatewayID, "igw-"):
			d.Via = route.GatewayID
			if v.igwAttachedTo[route.GatewayID] == sub.VpcID {
				d.Routable = true
				d.Reason = fmt.Sprintf("subnet %s routes 0.0.0.0/0 to internet gateway %s in %s", subnetID, route.GatewayID, rt.RouteTableID)
			} else {
				d.Reason = fmt.Sprintf("subnet %s routes 0.0.0.0/0 to internet gateway %s in %s, which is not attached to %s (blackhole)", subnetID, route.GatewayID, rt.RouteTableID, sub.VpcID)
			}
			return d
		default:
			target := route.GatewayID
			if target == "" {
				target = "a target Overcast does not model as egress"
			}
			d.Reason = fmt.Sprintf("subnet %s routes 0.0.0.0/0 to %s in %s, which is not an internet or NAT gateway", subnetID, target, rt.RouteTableID)
			return d
		}
	}
	d.Reason = fmt.Sprintf("subnet %s has no 0.0.0.0/0 route in %s", subnetID, rt.RouteTableID)
	return d
}

// placementEgress decides a container placed in several subnets: it gets a
// route out when any of them grants one. See dataplane.PlaceInSubnets for why
// "any" rather than "all". An empty set — a resource that named a VPC without
// naming where in it — is withheld, since there is no route table to read and
// withholding is the direction that does not fail on AWS.
func (v *egressView) placementEgress(subnetIDs []string) egressDecision {
	if len(subnetIDs) == 0 {
		return egressDecision{Reason: "the resource named no subnet, so no route table grants it a route out"}
	}
	first := egressDecision{}
	for i, id := range subnetIDs {
		d := v.subnetEgress(id)
		if d.Routable {
			return d
		}
		if i == 0 {
			first = d
		}
	}
	return first
}

// ── placement ────────────────────────────────────────────────────────────────

// egressNetworkForSubnets is Service.EgressNetworkForSubnets: the network a
// container in these subnets takes its default route from, or "" when its
// route tables give it none. Creating the egress network on demand is what
// makes the network count follow use — a VPC whose subnets all route nowhere
// never gets one.
//
// An error is the egress the template grants being undeliverable — the pool
// exhausted, the daemon refusing — and it fails the placement. The alternative,
// returning "" and placing the container without the route out its route table
// gives it, is `routed` quietly becoming `none` for one VPC, with nothing
// saying so until an ENETUNREACH turns up inside somebody's function.
func (h *Handler) egressNetworkForSubnets(ctx context.Context, vpcID string, subnetIDs []string) (string, error) {
	if !dataplane.Routed(h.cfg) || !h.dockerReady.Load() {
		return "", nil
	}
	log := h.log.WithRecorder(ctx)

	vpc, aerr := h.store.getVPC(ctx, vpcID)
	if aerr != nil {
		return "", nil // the caller checked launchability a moment ago; a VPC that vanished places on its plane or nowhere
	}
	// The default VPC's plane is the shared data plane, which is routable
	// under `routed` as AWS's default subnets are public: nothing to add.
	if vpc.IsDefault || vpc.DockerNetworkID == "" {
		return "", nil
	}
	view, aerr := h.egressView(ctx)
	if aerr != nil {
		return "", errors.New(aerr.Message)
	}
	d := view.placementEgress(subnetIDs)
	if !d.Routable {
		log.Info("vpc egress: no route out — the container joins only the VPC's internal plane",
			zap.String("vpc", vpcID), zap.Strings("subnets", subnetIDs), zap.String("reason", d.Reason))
		return "", nil
	}

	// The record already names one: hand it back without re-verifying. What
	// verifies an egress network is the startup reconcile, exactly as for the
	// VPC's plane — DockerNetworkForVpc returns vpc.DockerNetworkID from the
	// record too, and the region it belongs to has been reconciled by the
	// time this runs (PlaceInSubnets asks for the plane first). Re-running
	// the whole ensure per placement bought nothing and put a Docker round
	// trip on every Lambda cold start.
	if vpc.EgressNetworkID != "" {
		log.Info("vpc egress: route out granted — the container also joins the VPC's egress network",
			zap.String("vpc", vpcID), zap.String("network", vpc.EgressNetworkID), zap.String("reason", d.Reason))
		return vpc.EgressNetworkID, nil
	}

	// Under the plane's lock, for the record write: a gateway flip in flight
	// rewrites every record on the network, and one that raced this would
	// drop the egress fields it did not know about.
	_, unlock, aerr := h.lockVPCNetwork(ctx, vpcID)
	if aerr != nil {
		return "", errors.New(aerr.Message)
	}
	defer unlock()
	netID, err := h.ensureVPCEgressNetwork(ctx, vpcID)
	if err != nil {
		h.noteEgressProblem(vpcID, "", fmt.Sprintf("%s, but the VPC's egress network could not be created: %v", d.Reason, err))
		return "", fmt.Errorf("%s, but the VPC's egress network could not be created: %w", d.Reason, err)
	}
	log.Info("vpc egress: route out granted — the container also joins the VPC's egress network",
		zap.String("vpc", vpcID), zap.String("network", netID), zap.String("reason", d.Reason))
	return netID, nil
}

// recordPlacement is Service.RecordPlacement: which subnets a container was
// placed in, so reconcileVPCEgress can revisit the decision when a route table
// changes. Only under `routed`, where the record has a reader.
//
// It also closes the window the placement opens. EgressNetworkForSubnets read
// the route tables before this container existed, and Attach joined the egress
// network before saying so here — so a route mutation in between reconciled a
// VPC that did not yet know about this container, and left it on the wrong
// side of its own route table with nothing to converge it until the next
// mutation or a restart. Writing the record and re-reading the decision under
// the VPC's lock makes the two orderings equivalent: a mutation either lands
// before this and is seen by the re-read, or after it and finds the record.
func (h *Handler) recordPlacement(ctx context.Context, containerID string, p dataplane.Placement) {
	if !dataplane.Routed(h.cfg) || containerID == "" || p.VPCID == "" {
		return
	}
	log := h.log.WithRecorder(ctx)
	vpc, unlock, aerr := h.lockVPCNetwork(ctx, p.VPCID)
	if aerr != nil {
		log.Warn("vpc egress: could not lock the VPC to record a placement — a later route-table change will not move this container",
			zap.String("container", containerID), zap.String("vpc", p.VPCID), zap.String("error", aerr.Message))
		return
	}
	defer unlock()

	rec := &vpcPlacement{ContainerID: containerID, VpcID: p.VPCID, SubnetIDs: p.SubnetIDs, RecordedAt: h.clk.Now().UnixMilli()}
	if aerr := h.store.putPlacement(ctx, rec); aerr != nil {
		log.Warn("vpc egress: could not record a placement — a later route-table change will not move this container",
			zap.String("container", containerID), zap.String("vpc", p.VPCID), zap.String("error", aerr.Message))
		return
	}
	if !h.dockerReady.Load() || vpc.IsDefault || vpc.DockerNetworkID == "" {
		return
	}
	view, aerr := h.egressView(ctx)
	if aerr != nil {
		log.Warn("vpc egress: could not re-read the route tables after recording a placement",
			zap.String("container", containerID), zap.String("vpc", p.VPCID), zap.String("error", aerr.Message))
		return
	}
	want := view.placementEgress(p.SubnetIDs)
	egressID, egressName := vpc.EgressNetworkID, vpc.EgressNetworkName
	if want.Routable && egressID == "" {
		// The route tables granted egress while this container was being
		// attached, and the VPC has no network to carry it yet.
		id, err := h.ensureVPCEgressNetwork(ctx, vpc.VpcID)
		if err != nil {
			h.noteEgressProblem(vpc.VpcID, "", want.Reason+", but the VPC's egress network could not be created: "+err.Error())
			return
		}
		egressID, egressName = id, h.cfg.VPCEgressNetwork(vpc.VpcID)
	}
	if failure := h.movePlacementLocked(ctx, vpc, egressID, egressName, containerID, want); failure != "" {
		h.noteEgressProblem(vpc.VpcID, egressID, "a container could not be moved to match its route tables: "+failure)
		log.Error("vpc egress: a container could not be settled on the side of the egress network its route tables put it on",
			zap.String("container", containerID), zap.String("vpc", p.VPCID), zap.String("failure", failure))
	}
}

// ensureVPCEgressNetwork brings the VPC's egress network into existence, in
// the state its spec describes, and records it on the VPC. The caller holds
// the VPC plane's lock (lockVPCNetwork) for the record write. Returns the
// network's ID.
//
// It goes through docker.EnsureNetwork rather than a plain create, so an
// egress network left by a previous run is verified field by field like every
// other network Overcast reuses, and one that drifted with nothing attached is
// rebuilt. The /24 is allocated once and kept on the record, so the network
// comes back at the same address after a restart or a rebuild.
func (h *Handler) ensureVPCEgressNetwork(ctx context.Context, vpcID string) (string, error) {
	vpc, aerr := h.store.getVPC(ctx, vpcID)
	if aerr != nil {
		return "", errors.New(aerr.Message)
	}
	name := h.cfg.VPCEgressNetwork(vpc.VpcID)
	cidr := vpc.EgressCidrBlock
	if cidr == "" {
		allocated, err := h.allocateEgressCIDR(ctx)
		if err != nil {
			return "", err
		}
		cidr, vpc.EgressCidrBlock = allocated, allocated
	}
	spec := dataplane.VPCEgressNetworkSpec(h.cfg, dataplane.VPCEgressNetwork{
		VPCID:  vpc.VpcID,
		Subnet: cidr,
		Owner:  h.instanceDomain(ctx),
	}).Resolve(ctx, h.docker)
	status, err := docker.EnsureNetwork(ctx, h.docker, spec, h.log.ZapLogger())
	if err != nil {
		return "", err
	}
	status.Reason = egressNetworkReason
	h.recordNetworkStatus(status)

	info, err := h.docker.InspectNetwork(ctx, name)
	if err != nil {
		return "", fmt.Errorf("inspect egress network %s after ensuring it: %w", name, err)
	}
	if vpc.EgressNetworkID != info.ID || vpc.EgressNetworkName != name || vpc.EgressCidrBlock != cidr {
		vpc.EgressNetworkID, vpc.EgressNetworkName, vpc.EgressCidrBlock = info.ID, name, cidr
		if aerr := h.store.putVPC(ctx, vpc); aerr != nil {
			return "", errors.New(aerr.Message)
		}
		h.log.WithRecorder(ctx).Info("vpc egress network ready",
			zap.String("vpc", vpc.VpcID), zap.String("network", name), zap.String("subnet", cidr),
			zap.String("reason", egressNetworkReason))
	}
	return info.ID, nil
}

// allocateEgressCIDR picks the lowest /24 in config.VPCEgressPool that no
// VPC's egress network holds and that overlaps no VPC's own range, in any
// region: a VPC is regional, but the daemon and its address space are not.
//
// The pool's size is the ceiling on VPCs with egress — 256 for the default
// /16 — and running into it is the one failure this mode has that `open` does
// not. The error names the pool and how to widen it.
func (h *Handler) allocateEgressCIDR(ctx context.Context) (string, error) {
	// One allocation at a time across VPCs, and no wider: two placements in
	// two VPCs that both allocate would otherwise read the same records and
	// pick the same /24. Everything after the allocation — the Docker work
	// that dominates the cost — is per-VPC and serialised by the VPC's own
	// network lock, so it does not belong under a process-global one.
	h.egressMu.Lock()
	defer h.egressMu.Unlock()

	pool := h.cfg.VPCEgressPool
	if pool == "" {
		pool = config.DefaultVPCEgressPool
	}
	_, ipnet, err := net.ParseCIDR(pool)
	if err != nil {
		return "", fmt.Errorf("OVERCAST_VPC_EGRESS_POOL %q: %w", pool, err)
	}
	byRegion, aerr := h.store.vpcsByRegion(ctx)
	if aerr != nil {
		return "", errors.New(aerr.Message)
	}
	var vpcs []*VPC
	for _, regional := range byRegion {
		vpcs = append(vpcs, regional...)
	}
	taken := make(map[string]bool, len(vpcs))
	for _, v := range vpcs {
		if v.EgressCidrBlock != "" {
			taken[v.EgressCidrBlock] = true
		}
	}
	base := ipnet.IP.To4()
	ones, _ := ipnet.Mask.Size()
	count := 1 << (24 - ones)
	for i := range count {
		b := []byte{base[0], base[1], base[2], 0}
		n := (int(b[1])<<8 | int(b[2])) + i
		candidate := fmt.Sprintf("%d.%d.%d.0/24", b[0], (n>>8)&0xff, n&0xff)
		if taken[candidate] {
			continue
		}
		overlaps := false
		for _, v := range vpcs {
			if cidrsOverlap(candidate, v.CidrBlock) || (v.DockerCidrBlock != "" && cidrsOverlap(candidate, v.DockerCidrBlock)) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("OVERCAST_VPC_EGRESS_POOL %s has no free /24 left for another VPC's egress network "+
		"(%d in use) — delete VPCs that no longer need one, or set a wider pool", pool, len(taken))
}

// ── re-placement on a route-table change ─────────────────────────────────────

// reconcileVPCEgress revisits every recorded placement in vpcID after
// something that changes what its subnets route to — a route created or
// deleted, an association changed, a gateway attached or detached, a NAT
// gateway created or deleted — and moves each running container on or off the
// VPC's egress network to match. Nothing else about the container changes: its
// plane, its address, its names, its control-plane attachment all stay, so
// the Runtime API is reachable throughout and only the route out moves. That
// is the AWS shape too: a route-table change reroutes an ENI in place.
//
// Failures are recorded against the VPC for the health advisories rather than
// failing the API call that caused them — the route is metadata AWS never
// refuses, and the record is already written. Nothing about a failed move
// heals on its own, and the startup reconcile retries it.
func (h *Handler) reconcileVPCEgress(ctx context.Context, vpcID string) {
	// Guard before logging: every route-table mutation reaches this, in every
	// mode, and under the default `open` there is nothing to say.
	if !dataplane.Routed(h.cfg) {
		return
	}
	h.log.WithRecorder(ctx).Debug("vpc egress: revisiting placements",
		zap.String("vpc", vpcID), zap.Bool("docker_ready", h.dockerReady.Load()))
	if !h.dockerReady.Load() {
		return
	}
	// The route is already written and the API answer does not wait on this,
	// so a client that hangs up mid-CreateRoute must not leave half the VPC's
	// containers moved. The request's values — its region above all — are
	// kept; only its cancellation is dropped.
	ctx = context.WithoutCancel(ctx)
	vpc, unlock, aerr := h.lockVPCNetwork(ctx, vpcID)
	if aerr != nil {
		h.log.WithRecorder(ctx).Error("vpc egress: lock the VPC's network to revisit placements",
			zap.String("vpc", vpcID), zap.String("error", aerr.Message))
		return
	}
	defer unlock()
	h.reconcileVPCEgressLocked(ctx, vpc)
}

// reconcileVPCEgressLocked is reconcileVPCEgress with the plane's lock held.
func (h *Handler) reconcileVPCEgressLocked(ctx context.Context, vpc *VPC) {
	if !dataplane.Routed(h.cfg) || !h.dockerReady.Load() || vpc == nil || vpc.IsDefault || vpc.DockerNetworkID == "" {
		return
	}
	log := h.log.WithRecorder(ctx)

	placements, aerr := h.store.listPlacements(ctx, vpc.VpcID)
	if aerr != nil {
		log.Error("vpc egress: list placements", zap.String("vpc", vpc.VpcID), zap.String("error", aerr.Message))
		return
	}
	log.Debug("vpc egress: placements to revisit", zap.String("vpc", vpc.VpcID), zap.Int("count", len(placements)))
	if len(placements) == 0 {
		// Nothing left to be wrong about. Cleared here rather than at the end
		// so a VPC whose containers all went away does not keep an advisory
		// about a move that can no longer be attempted.
		h.netProblems.Delete(egressProblemKey(vpc.VpcID))
		return
	}
	view, aerr := h.egressView(ctx)
	if aerr != nil {
		log.Error("vpc egress: read route tables", zap.String("vpc", vpc.VpcID), zap.String("error", aerr.Message))
		return
	}

	decisions := make([]egressDecision, len(placements))
	needEgress := false
	for i, p := range placements {
		decisions[i] = view.placementEgress(p.SubnetIDs)
		needEgress = needEgress || decisions[i].Routable
	}
	egressID, egressName := vpc.EgressNetworkID, vpc.EgressNetworkName
	if needEgress && egressID == "" {
		id, err := h.ensureVPCEgressNetwork(ctx, vpc.VpcID)
		if err != nil {
			h.noteEgressProblem(vpc.VpcID, "", "a subnet's route table now grants egress, but the VPC's egress network could not be created: "+err.Error())
			log.Error("vpc egress: egress network could not be created — containers whose subnet now routes out are left without a route",
				zap.String("vpc", vpc.VpcID), zap.Error(err))
			return
		}
		egressID, egressName = id, h.cfg.VPCEgressNetwork(vpc.VpcID)
	}

	var failures []string
	for i, p := range placements {
		if failure := h.movePlacementLocked(ctx, vpc, egressID, egressName, p.ContainerID, decisions[i]); failure != "" {
			failures = append(failures, failure)
		}
	}
	if len(failures) > 0 {
		h.noteEgressProblem(vpc.VpcID, egressID, "containers could not be moved to match their route tables: "+strings.Join(failures, "; "))
		log.Error("vpc egress: containers could not be moved to match their route tables",
			zap.String("vpc", vpc.VpcID), zap.Strings("failures", failures))
		return
	}
	h.netProblems.Delete(egressProblemKey(vpc.VpcID))
}

// movePlacementLocked brings one container to the side of the egress network
// its subnets' route tables put it on, and returns what stood in the way, or
// "" when nothing did. The caller holds the VPC's network lock.
//
// A container Docker no longer has is not a failure: its placement is dropped
// and the pass moves on. Everything else is reported so it reaches the health
// advisories rather than being logged and lost.
func (h *Handler) movePlacementLocked(ctx context.Context, vpc *VPC, egressID, egressName, containerID string, want egressDecision) string {
	log := h.log.WithRecorder(ctx)
	c, err := h.docker.InspectContainer(ctx, containerID)
	if err != nil {
		if docker.IsNotFound(err) {
			_ = h.store.deletePlacement(ctx, containerID) // gone; nothing to move, nothing to keep
			return ""
		}
		return fmt.Sprintf("inspect container %s: %v", containerID, err)
	}
	on := egressID != "" && containerOnNetwork(c, egressID, egressName)
	switch {
	case want.Routable && !on:
		if egressID == "" {
			return fmt.Sprintf("container %s needs a route out but the VPC has no egress network", containerID)
		}
		err := h.docker.ConnectNetworkWithConfig(ctx, egressID, containerID,
			&docker.EndpointSettings{GwPriority: dataplane.EgressGatewayPriority})
		if err != nil {
			return fmt.Sprintf("container %s could not join the egress network: %v", containerID, err)
		}
		log.Info("vpc egress: route out granted to a running container",
			zap.String("vpc", vpc.VpcID), zap.String("container", containerID),
			zap.String("network", egressName), zap.String("reason", want.Reason))
	case !want.Routable && on:
		if err := h.docker.DisconnectNetwork(ctx, egressID, containerID); err != nil {
			return fmt.Sprintf("container %s could not leave the egress network: %v", containerID, err)
		}
		log.Info("vpc egress: route out withdrawn from a running container",
			zap.String("vpc", vpc.VpcID), zap.String("container", containerID),
			zap.String("network", egressName), zap.String("reason", want.Reason))
	}
	return ""
}

// containerOnNetwork reports whether a container's attachments include the
// network with the given ID or name. Docker keys them by name and carries the
// ID inside, and a caller may hold either.
func containerOnNetwork(c *docker.ContainerInspect, id, name string) bool {
	if c == nil {
		return false
	}
	for n, ep := range c.NetworkSettings.Networks {
		if (id != "" && ep.NetworkID == id) || (name != "" && n == name) {
			return true
		}
	}
	return false
}

// egressProblemKey separates a VPC's egress problem from its plane problem in
// netProblems: the gateway flip clears the one it owns on success, and must
// not clear a failed move it knows nothing about. Both render under the VPC.
func egressProblemKey(vpcID string) string { return vpcID + "/egress" }

// noteEgressProblem records that vpcID's containers do not have the egress
// their route tables call for, with what stood in the way.
func (h *Handler) noteEgressProblem(vpcID, netID, detail string) {
	h.netProblems.Store(egressProblemKey(vpcID), dataplane.VPCNetworkProblem{VpcID: vpcID, NetworkID: netID, Detail: detail})
}

// ── startup ──────────────────────────────────────────────────────────────────

// splitVPCNetworks divides an in-scope snapshot of EC2 networks into the VPC
// planes and the egress networks. The strategies index planes by their
// resource-id label, which an egress network shares with its plane, so handed
// the whole snapshot they would adopt one for the other.
func splitVPCNetworks(networks []docker.NetworkSummary) (planes, egress []docker.NetworkSummary) {
	for _, n := range networks {
		if n.VPCRole() == docker.VPCRoleEgress {
			egress = append(egress, n)
			continue
		}
		planes = append(planes, n)
	}
	return planes, egress
}

// egressNetworkIndex is the set of VPC egress networks one reconcile pass may
// act on, keyed by name — which is derived from the VPC ID
// (config.VPCEgressNetwork), so a region's pass can claim its own without
// asking the daemon anything.
//
// One index serves every region of a pass, for the reason vpcNetworkIndex
// does: the store keys VPCs by region and the daemon knows nothing of
// regions, so a region sweeping a private view of the daemon would take every
// other region's egress network for litter. Claiming removes the network from
// the index, so what is left when the last region has run is what no VPC
// anywhere names — and only that is swept.
type egressNetworkIndex struct {
	byName map[string]*docker.NetworkSummary
	byID   map[string]*docker.NetworkSummary
}

func newEgressNetworkIndex(existing []docker.NetworkSummary) *egressNetworkIndex {
	ix := &egressNetworkIndex{
		byName: make(map[string]*docker.NetworkSummary, len(existing)),
		byID:   make(map[string]*docker.NetworkSummary, len(existing)),
	}
	for i := range existing {
		n := &existing[i]
		ix.byName[n.Name] = n
		ix.byID[n.ID] = n
	}
	return ix
}

// claim takes the network named name out of the index and returns it, or nil
// when the daemon has none.
func (ix *egressNetworkIndex) claim(name string) *docker.NetworkSummary {
	if ix == nil {
		return nil
	}
	n, ok := ix.byName[name]
	if !ok {
		return nil
	}
	delete(ix.byName, name)
	delete(ix.byID, n.ID)
	return n
}

// unclaimed returns, by ID, the egress networks no VPC claimed.
func (ix *egressNetworkIndex) unclaimed() map[string]*docker.NetworkSummary {
	if ix == nil {
		return nil
	}
	return ix.byID
}

// reconcileRegionEgressNetworks is the egress half of one region's share of
// the startup reconcile, run after the strategies have matched that region's
// planes to its records.
//
// Under `routed`: every VPC's egress network is claimed by name, verified
// against its spec, and cleared from the record when gone; then every VPC's
// placements are revisited, because a route table may have changed while
// Overcast was down and a container adopted from the previous run would be on
// the wrong side of it.
//
// Under every other mode the records are cleared and nothing is claimed, so
// an egress network left by a `routed` run falls to the sweep. Under `open`
// it is only clutter, but under `none` a routable bridge with containers on
// it is exactly the leak the mode exists to close, and a container adopted
// from the previous run keeps its attachments until something takes them
// away.
// The region's VPCs are handed in rather than re-listed, as the plane pass
// does (Handler.adoptRegionNetworks). A list of its own would have an error
// path, and the only thing a region can do on that path is claim nothing —
// after which the end sweep removes every egress network in the region and
// disconnects the containers on them. There is no reading of a transient
// store error that should cost a running container its route out.
func (h *Handler) reconcileRegionEgressNetworks(ctx context.Context, vpcs []*VPC, ix *egressNetworkIndex) {
	log := h.log.WithRecorder(ctx)

	if !dataplane.Routed(h.cfg) {
		for _, vpc := range vpcs {
			if vpc.EgressNetworkID == "" && vpc.EgressNetworkName == "" {
				continue
			}
			log.Info("reconcile networks: forgetting a VPC egress network left by a previous OVERCAST_VPC_EGRESS=routed run",
				zap.String("vpc", vpc.VpcID), zap.String("network", vpc.EgressNetworkName),
				zap.String("mode", string(dataplane.EgressMode(h.cfg))))
			vpc.EgressNetworkID, vpc.EgressNetworkName = "", ""
			_ = h.store.putVPC(ctx, vpc)
		}
		return
	}

	for _, listed := range vpcs {
		if listed.IsDefault {
			continue
		}
		vpc, unlock, aerr := h.lockVPCNetwork(ctx, listed.VpcID)
		if aerr != nil {
			log.Error("reconcile networks: lock VPC network for the egress pass",
				zap.String("vpc", listed.VpcID), zap.String("error", aerr.Message))
			continue
		}
		name := h.cfg.VPCEgressNetwork(vpc.VpcID)
		if n := ix.claim(name); n != nil {
			if vpc.EgressNetworkID != n.ID || vpc.EgressNetworkName != name || (n.Subnet() != "" && vpc.EgressCidrBlock != n.Subnet()) {
				vpc.EgressNetworkID, vpc.EgressNetworkName = n.ID, name
				if sub := n.Subnet(); sub != "" {
					vpc.EgressCidrBlock = sub
				}
				_ = h.store.putVPC(ctx, vpc)
			}
			// Verified against its spec like every network Overcast reuses,
			// and reported to health either way.
			if _, err := h.ensureVPCEgressNetwork(ctx, vpc.VpcID); err != nil {
				h.noteEgressProblem(vpc.VpcID, n.ID, "the VPC's egress network is not in the state this configuration asks for and could not be rebuilt: "+err.Error())
			} else if current, aerr := h.store.getVPC(ctx, vpc.VpcID); aerr == nil {
				vpc = current
			}
		} else if vpc.EgressNetworkID != "" {
			// The record names a network that is gone. The next placement
			// that needs one recreates it, at the CIDR the record keeps.
			log.Info("reconcile networks: the VPC's egress network is gone — it is recreated when a placement next needs it",
				zap.String("vpc", vpc.VpcID), zap.String("network", vpc.EgressNetworkID))
			vpc.EgressNetworkID, vpc.EgressNetworkName = "", ""
			_ = h.store.putVPC(ctx, vpc)
		}
		h.reconcileVPCEgressLocked(ctx, vpc)
		unlock()
	}
}

// unrecordedEgress narrows sweep to the egress networks no VPC's record names
// now, on a second store read across every region — the egress counterpart of
// Handler.unrecorded, and fail-closed for the same reason: when the store
// cannot be read, nothing is swept. An egress network left behind costs a /24
// from a pool of 256; one removed in error takes the route out from under
// every container on it.
//
// It is also what covers the VPCs a region could not lock, which claim
// nothing and would otherwise be swept on a lock failure alone.
func (h *Handler) unrecordedEgress(ctx context.Context, sweep map[string]*docker.NetworkSummary) map[string]*docker.NetworkSummary {
	if len(sweep) == 0 {
		return sweep
	}
	byRegion, aerr := h.store.vpcsByRegion(ctx)
	if aerr != nil {
		h.log.WithRecorder(ctx).Error("reconcile networks: list VPCs before the egress sweep; sweeping nothing",
			zap.String("error", aerr.Message))
		return nil
	}
	for _, vpcs := range byRegion {
		for _, vpc := range vpcs {
			delete(sweep, vpc.EgressNetworkID)
		}
	}
	return sweep
}

// sweepEgressNetworks removes the egress networks no VPC in any region
// claimed — orphans of a deleted VPC, and, outside `routed`, every one the
// mode no longer has a use for. Only this instance's: one whose owner cannot
// be established belongs to a run this one knows nothing about.
//
// What it removes is narrowed twice, because removing one disconnects every
// container on it: by the claims the regions made, and then by a second store
// read that drops anything a record still names — see unrecordedEgress, which
// sweeps nothing at all when that read fails.
func (h *Handler) sweepEgressNetworks(ctx context.Context, byID map[string]*docker.NetworkSummary) {
	byID = h.unrecordedEgress(ctx, byID)
	if len(byID) == 0 {
		return
	}
	// The ownership rule and its logging are shared with the plane sweep
	// (sweepUnclaimed); only the removal differs, because an egress network
	// has to be emptied of every container first.
	h.sweepUnclaimed(ctx, byID, "egress: ", func(n *docker.NetworkSummary) {
		defer docker.LockNetwork(n.Name)()
		h.removeEgressNetwork(ctx, n.ID)
	})
}

// removeEgressNetwork takes every container off an egress network and removes
// it. Every container, not only Overcast's: the network carries nothing but a
// default route, so leaving one is losing a route out and never a name or an
// address, and Docker refuses the removal while any endpoint remains.
func (h *Handler) removeEgressNetwork(ctx context.Context, netID string) {
	log := h.log.WithRecorder(ctx)
	if netID == "" {
		return
	}
	info, err := h.docker.InspectNetwork(ctx, netID)
	if err != nil {
		if !docker.IsNotFound(err) {
			log.Warn("vpc egress: inspect egress network before removing it", zap.String("network", netID), zap.Error(err))
		}
		return
	}
	for id := range info.Containers {
		if err := h.docker.DisconnectNetwork(ctx, info.ID, id); err != nil {
			log.Warn("vpc egress: could not take a container off the egress network",
				zap.String("network", info.Name), zap.String("container", id), zap.Error(err))
		}
	}
	if err := h.docker.RemoveNetwork(ctx, info.ID); err != nil {
		log.Warn("vpc egress: remove egress network", zap.String("network", info.Name), zap.Error(err))
		return
	}
	log.Info("vpc egress: removed egress network", zap.String("network", info.Name))
}

// forgetVPCEgress is the egress half of forgetVPCNetwork: the VPC's egress
// network goes with the VPC, as do its placements and any problem recorded
// against them.
func (h *Handler) forgetVPCEgress(ctx context.Context, vpc *VPC) {
	h.netProblems.Delete(egressProblemKey(vpc.VpcID))
	if placements, aerr := h.store.listPlacements(ctx, vpc.VpcID); aerr == nil {
		for _, p := range placements {
			_ = h.store.deletePlacement(ctx, p.ContainerID)
		}
	}
	if !h.dockerReady.Load() || vpc.EgressNetworkID == "" {
		return
	}
	defer docker.LockNetwork(h.cfg.VPCEgressNetwork(vpc.VpcID))()
	h.removeEgressNetwork(ctx, vpc.EgressNetworkID)
}
