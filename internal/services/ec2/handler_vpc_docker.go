package ec2

import (
	"context"
	"os"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/containerendpoint"
	"github.com/Neaox/overcast/internal/docker"
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
	h.vpcStrategy.Reconcile(ctx, vpcs, networks)

	// Reconcile adopts networks that already existed, so joining only on create
	// would leave Overcast off every network that survived a restart.
	for _, vpc := range vpcs {
		h.joinVPCNetwork(ctx, vpc.DockerNetworkID)
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
	labels := docker.ManagedLabels("ec2", vpc.VpcID)
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
