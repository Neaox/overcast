package ec2

import (
	"context"

	"go.uber.org/zap"

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
	if !h.dockerReady.Load() {
		return
	}
	vpcs, aerr := h.store.listVPCs(ctx)
	if aerr != nil {
		h.log.Error("reconcile networks: list VPCs", zap.String("error", aerr.Message))
		return
	}
	h.vpcStrategy.Reconcile(ctx, vpcs, networks)
}

// createDockerVPCNetwork creates a Docker bridge network for the given VPC
// using its CidrBlock (or DockerCidrBlock if the active strategy has set
// one). The network is --internal unless the VPC has an attached internet
// gateway. Returns the Docker network ID on success.
func (h *Handler) createDockerVPCNetwork(ctx context.Context, vpc *VPC) (string, error) {
	labels := docker.ManagedLabels("ec2", vpc.VpcID)
	labels["overcast.vpc-id"] = vpc.VpcID

	subnet := vpc.CidrBlock
	if vpc.DockerCidrBlock != "" {
		subnet = vpc.DockerCidrBlock
	}

	return h.docker.CreateNetworkWithOptions(ctx, docker.CreateNetworkOptions{
		Name:     "overcast-vpc-" + vpc.VpcID,
		Labels:   labels,
		Subnet:   subnet,
		Internal: !h.vpcHasInternetGateway(ctx, vpc.VpcID),
	})
}

// removeDockerVPCNetwork removes a Docker network by ID. Missing networks
// are treated as success.
func (h *Handler) removeDockerVPCNetwork(ctx context.Context, netID string) error {
	if netID == "" {
		return nil
	}
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
