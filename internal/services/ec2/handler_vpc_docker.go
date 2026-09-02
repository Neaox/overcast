package ec2

import (
	"context"
	"os"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/containerendpoint"
	"github.com/overcast-sh/overcast/internal/docker"
)

// This file holds the low-level Docker-network primitives used by the VPC
// network strategies in vpc_strategy.go. All policy decisions (when to
// create, share, or skip a network) live in the strategies — this file only
// knows how to talk to Docker.

// reconcileNetworks is the entrypoint wired from router.reconcileDockerNetworks
// via Service.ReconcileNetworks. It delegates to the active strategy and must
// tolerate every error path without aborting overcastd startup.
func (h *Handler) reconcileNetworks(ctx context.Context, networks []docker.NetworkSummary) {
	log := h.log.WithRecorder(ctx)

	if !h.dockerReady.Load() {
		return
	}
	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		log.Error("reconcile networks: list VPCs", zap.String("error", aerr.Message))
		return
	}
	h.vpcStrategy.Reconcile(ctx, vpcs, h.networksInScope(ctx, networks))

	// Reconcile adopts networks that already existed, so joining only on create
	// would leave Overcast off every network that survived a restart.
	for _, vpc := range vpcs {
		h.joinVPCNetwork(ctx, vpc.DockerNetworkID)
	}
}

// nsInstance holds this instance's sweep-domain identity, stamped into
// docker.LabelInstance on every VPC network it creates. See
// serviceutil.InstanceDomain.
const nsInstance = "ec2:instance"

// instanceDomain is this instance's identity, or "" when the store cannot
// establish one — in which case nothing is ours to remove.
func (h *Handler) instanceDomain(ctx context.Context) string {
	if h.instances == nil {
		return ""
	}
	return h.instances.Resolve(ctx)
}

// networksInScope narrows a daemon-wide snapshot of EC2 networks to the ones
// this instance may act on.
//
// The snapshot is every network on the daemon carrying overcast.service=ec2,
// whoever created it. Two Overcasts sharing a daemon — a developer's live
// instance and a test server, or two agents' instances — each see the other's
// VPC networks in it, and unfiltered the strategies' orphan pass removed them:
// a network is "orphaned" by having no VPC record in *this* store, and the
// other instance's records are not here. That is how a test run took a live
// instance's overcast-vpc-* network with it.
//
// The rule is the container GC's (docker.LabelInstance). A network labelled
// with another instance's identity is invisible: neither adopted nor removed.
// One labelled with ours is fully in scope. One with no label predates the
// label — it stays adoptable, so a VPC that survived an upgrade still finds its
// network, but removeOrphanedNetworks leaves it, because absence is not
// permission.
func (h *Handler) networksInScope(ctx context.Context, networks []docker.NetworkSummary) []docker.NetworkSummary {
	domain := h.instanceDomain(ctx)
	log := h.log.WithRecorder(ctx)
	in := make([]docker.NetworkSummary, 0, len(networks))
	for _, n := range networks {
		if owner := n.Instance(); owner != "" && owner != domain {
			log.Debug("reconcile networks: leaving a network another instance owns",
				zap.String("network", n.ID), zap.String("name", n.Name), zap.String("owner", owner))
			continue
		}
		in = append(in, n)
	}
	return in
}

// removeOrphanedNetworks removes what a strategy's adoption pass left
// unclaimed in byID — and only what this instance created. networksInScope has
// already dropped other instances' networks, so what remains is ours or
// unlabelled; the unlabelled are retained and said so, since their owner
// cannot be established and an unremovable orphan is a far cheaper mistake
// than deleting a network another instance is serving. prefix names the
// strategy in the log, as each strategy's own messages do.
func (h *Handler) removeOrphanedNetworks(ctx context.Context, byID map[string]*docker.NetworkSummary, prefix string) {
	log := h.log.WithRecorder(ctx)
	domain := h.instanceDomain(ctx)
	for id, n := range byID {
		if domain == "" || n.Instance() != domain {
			log.Info("reconcile networks: "+prefix+"retaining an unclaimed network whose owner cannot be established",
				zap.String("vpc", n.ResourceID()),
				zap.String("network", id))
			continue
		}
		log.Info("reconcile networks: "+prefix+"removing orphaned network",
			zap.String("vpc", n.ResourceID()),
			zap.String("network", id))
		if err := h.removeDockerVPCNetwork(ctx, id); err != nil {
			log.Warn("reconcile networks: "+prefix+"remove orphaned network",
				zap.String("network", id),
				zap.Error(err))
		}
	}
}

// joinVPCNetwork attaches Overcast itself to a VPC's Docker network.
//
// A VPC network is a private bridge Overcast is not on by default, so anything
// in-process that has to *reach* a task — the ELB data plane forwarding to an
// awsvpc target, most of all — cannot dial the address the task actually has.
// Overcast already joins the ECS and Lambda networks for the same reason; this
// is that, per VPC. Connecting is idempotent, so callers may repeat it freely.
//
// Only meaningful when Overcast is itself containerised: on the host there is
// no container to attach, and reaching a bridge subnet is the host's routing
// question rather than ours.
func (h *Handler) joinVPCNetwork(ctx context.Context, netID string) {
	log := h.log.WithRecorder(ctx)

	self, ok := h.selfContainer()
	if !ok || netID == "" {
		return
	}
	if err := h.docker.ConnectNetwork(ctx, netID, self); err != nil {
		log.Warn("vpc network: could not attach Overcast to the VPC network — "+
			"the load balancer data plane cannot reach tasks in this VPC",
			zap.String("network", netID), zap.Error(err))
		return
	}
	log.Debug("vpc network: Overcast attached", zap.String("network", netID))
}

// leaveVPCNetwork detaches Overcast from a VPC network. Docker refuses to
// remove a network that still has endpoints, so the join above would otherwise
// make every VPC network undeletable.
func (h *Handler) leaveVPCNetwork(ctx context.Context, netID string) {
	log := h.log.WithRecorder(ctx)

	self, ok := h.selfContainer()
	if !ok || netID == "" {
		return
	}
	if err := h.docker.DisconnectNetwork(ctx, netID, self); err != nil {
		// Not being attached is the common case here (host mode, a network
		// created before this ran), and Docker reports it as an error.
		log.Debug("vpc network: detach Overcast",
			zap.String("network", netID), zap.Error(err))
	}
}

// The two environment probes behind selfContainer, indirected so a test can
// stand in for a machine it cannot arrange: whether this process is
// containerised, and which container that is.
var (
	runningInContainer = containerendpoint.RunningInContainer
	ownHostname        = os.Hostname
)

// selfContainer returns the container Overcast is running in. Docker accepts a
// container's short ID where it accepts a name, and inside a container the
// hostname is that short ID unless it was overridden — the same identification
// containerendpoint uses to find Overcast's address on the ECS network.
func (h *Handler) selfContainer() (string, bool) {
	if !h.dockerReady.Load() || h.docker == nil || !runningInContainer() {
		return "", false
	}
	hostname, err := ownHostname()
	if err != nil || hostname == "" {
		return "", false
	}
	return hostname, true
}

// createDockerVPCNetwork creates a Docker bridge network for the given VPC
// using its CidrBlock (or DockerCidrBlock if the active strategy has set
// one). The network is --internal unless the VPC has an attached internet
// gateway. Returns the Docker network ID on success.
func (h *Handler) createDockerVPCNetwork(ctx context.Context, vpc *VPC) (string, error) {
	return h.createDockerVPCNetworkInternal(ctx, vpc, !h.vpcHasInternetGateway(ctx, vpc.VpcID))
}

// createDockerVPCNetworkInternal is createDockerVPCNetwork with the --internal
// flag supplied rather than derived. The internet-gateway toggle recreates the
// network to flip it, and knows the value it wants.
func (h *Handler) createDockerVPCNetworkInternal(ctx context.Context, vpc *VPC, internal bool) (string, error) {
	// Stamped with this instance's identity, which is what lets a later
	// reconcile — ours after a restart, or a neighbour's on the same daemon —
	// tell whose network this is. See networksInScope.
	labels := h.instances.ManagedLabels(ctx, "ec2", vpc.VpcID)
	labels["overcast.vpc-id"] = vpc.VpcID

	netID, err := h.docker.CreateNetworkWithOptions(ctx, docker.CreateNetworkOptions{
		Name:     "overcast-vpc-" + vpc.VpcID,
		Labels:   labels,
		Subnet:   preferredDockerSubnet(vpc),
		Internal: internal,
	})
	if err != nil {
		return "", err
	}
	h.joinVPCNetwork(ctx, netID)
	return netID, nil
}

// removeDockerVPCNetwork removes a Docker network by ID. Missing networks
// are treated as success.
func (h *Handler) removeDockerVPCNetwork(ctx context.Context, netID string) error {
	if netID == "" {
		return nil
	}
	h.leaveVPCNetwork(ctx, netID)
	return h.docker.RemoveNetwork(ctx, netID)
}

// vpcHasInternetGateway returns true if any internet gateway is attached to
// the given VPC.
func (h *Handler) vpcHasInternetGateway(ctx context.Context, vpcID string) bool {
	igws, err := h.store.listInternetGateways(ctx)
	if err != nil {
		return false
	}
	for _, igw := range igws {
		for _, att := range igw.Attachments {
			if att.VpcID == vpcID && att.State == "attached" {
				return true
			}
		}
	}
	return false
}
