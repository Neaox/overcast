package ecs

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// effectiveNetworkMode returns the networkMode AWS applies to a task definition.
// An explicit mode always wins. When it was left unset at registration, AWS
// defaults to bridge — except for Fargate, which only supports awsvpc, so a
// Fargate launch of a task definition that never named a mode is treated as
// awsvpc rather than bridge.
func effectiveNetworkMode(td *TaskDefinition, launchType string) string {
	if td != nil && td.NetworkMode != "" {
		return td.NetworkMode
	}
	if launchType == "FARGATE" || (td != nil && isFargate(td.RequiresCompatibilities)) {
		return "awsvpc"
	}
	return "bridge"
}

// validateAwsvpcNetworkConfiguration checks a RunTask/CreateService
// networkConfiguration against the task definition it will run.
//
// AWS keys this requirement on the task definition's networkMode, not on
// launchType — which is what the error message itself says. Keying it on
// launchType instead (as this once did) lets two shapes AWS rejects through:
// an awsvpc task definition launched with launchType EC2, and the Fargate
// shape CDK emits by default, where the launch type is carried by a
// capacityProviderStrategy and launchType is absent entirely. Both then create
// a service that can never place a task.
func validateAwsvpcNetworkConfiguration(td *TaskDefinition, launchType string, netCfg *NetworkConfiguration) *protocol.AWSError {
	if effectiveNetworkMode(td, launchType) != "awsvpc" {
		return nil
	}
	if netCfg == nil {
		return &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "Network Configuration must be provided when networkMode is 'awsvpc'.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	// An awsvpcConfiguration carrying no subnet is as unplaceable as no
	// configuration at all, and AWS rejects it rather than accepting a service
	// that cannot start. The wording here is the one AWS is understood to
	// return; it has not been pinned against a captured response, unlike the
	// message above, which the AWS docs quote verbatim.
	if netCfg.AwsvpcConfiguration == nil || len(netCfg.AwsvpcConfiguration.Subnets) == 0 {
		return &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "subnets can not be empty.",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

// resolveAwsvpcPlacement validates that a VPC is launchable for ECS awsvpc tasks/services.
// A VPC is launchable when its network status is ok, shared, or remapped.
// VPCs with status conflict (strict mode collision) or unbacked (Docker unavailable)
// are rejected with InvalidParameterException. placement.subnetResolved indicates
// whether the subnet was successfully resolved to a VPC (vs. synthetic/non-EC2 subnet).
func (h *Handler) resolveAwsvpcPlacement(
	ctx context.Context,
	networkConfiguration *NetworkConfiguration,
	opName string,
) (awsvpcPlacement, *protocol.AWSError) {
	if networkConfiguration == nil {
		return awsvpcPlacement{}, nil
	}
	placement := awsvpcPlacement{
		subnetID: firstOrEmpty(networkConfiguration.AwsvpcConfiguration, func(a *AwsvpcConfiguration) string {
			if len(a.Subnets) > 0 {
				return a.Subnets[0]
			}
			return ""
		}),
		assignPublicIP: strings.EqualFold(
			firstOrEmpty(networkConfiguration.AwsvpcConfiguration, func(a *AwsvpcConfiguration) string {
				return a.AssignPublicIp
			}), "ENABLED"),
	}
	if placement.subnetID == "" || h.vpcResolver == nil {
		return placement, nil
	}
	vpcID := h.vpcResolver.VpcIDForSubnet(ctx, placement.subnetID)
	if vpcID == "" {
		// Preserve existing ECS behaviour for synthetic/non-EC2 subnets.
		return placement, nil
	}
	status := h.vpcResolver.VPCNetworkStatus(ctx, vpcID)
	switch status {
	case "", "ok", "shared", "remapped":
		placement.subnetResolved = true
		placement.remapped = status == "remapped"
		placement.networkID = h.vpcResolver.DockerNetworkForVpc(ctx, vpcID)
		return placement, nil
	default:
		placement.subnetResolved = true
		return placement, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    fmt.Sprintf("VPC '%s' is not launchable for %s (network status=%s).", vpcID, opName, status),
			HTTPStatus: http.StatusBadRequest,
		}
	}
}

// attachTaskENI joins a task container to its VPC's Docker network and leaves
// the task reporting the address that container really holds.
//
// The two have to agree. The ENI address is what DescribeTasks reports and what
// an ip-type target group is registered with, so a load balancer dials it — and
// it was allocated by EC2's own counter, independently of Docker's IPAM, which
// hands out addresses of its own from the same subnet. The two drift apart the
// moment either allocates once more than the other, and the load balancer then
// dials an address nothing answers on.
//
// So the address is asked for rather than assumed: the connect requests the
// allocated one, and whatever the container ends up with is read back and
// recorded.
//
// carriesENI says whether this container is the one holding the task's single
// reported address. For an awsvpc task that is the container holding its network
// namespace, and it is the only one attached at all — every other container in
// the task runs inside it and shares the address outright, as on AWS. For a task
// whose containers were each attached separately it is the first of them, the
// rest having addresses of their own that the task does not report.
func (h *Handler) attachTaskENI(ctx context.Context, task *Task, placement awsvpcPlacement, dockerID string, carriesENI bool) error {
	wanted := ""
	log := h.log.WithRecorder(ctx)
	if carriesENI && !placement.remapped {
		wanted = taskTargetAddress(task)
	}

	if wanted != "" {
		err := h.docker.ConnectNetworkWithConfig(ctx, placement.networkID, dockerID, &docker.EndpointSettings{
			IPAMConfig: &docker.EndpointIPAMConfig{IPv4Address: wanted},
		})
		if err == nil {
			return nil
		}
		// Docker refuses an address outside the network's subnet or already in
		// use. Neither is fatal: connect on its terms and record what it gave.
		log.Debug("ecs: could not pin the task's ENI address; using Docker's",
			zap.String("network", placement.networkID),
			zap.String("requested", wanted),
			zap.Error(err))
	}

	if err := h.docker.ConnectNetwork(ctx, placement.networkID, dockerID); err != nil {
		return err
	}
	if !carriesENI || placement.remapped {
		return nil
	}
	if actual := h.containerIPOnNetwork(ctx, dockerID, placement.networkID); actual != "" {
		setTaskENIAddress(task, actual)
	}
	return nil
}

// containerIPOnNetwork returns the address a container holds on one specific
// network. Docker keys a container's networks by name, so the network the
// caller knows by ID is found through each entry's own NetworkID.
func (h *Handler) containerIPOnNetwork(ctx context.Context, dockerID, networkID string) string {
	info, err := h.docker.InspectContainer(ctx, dockerID)
	if err != nil || info == nil {
		return ""
	}
	for _, n := range info.NetworkSettings.Networks {
		if n.NetworkID == networkID && n.IPAddress != "" {
			return n.IPAddress
		}
	}
	return ""
}

// setTaskENIAddress rewrites the privateIPv4Address on a task's ENI attachment.
func setTaskENIAddress(task *Task, ip string) {
	for i := range task.Attachments {
		if task.Attachments[i].Type != "ElasticNetworkInterface" {
			continue
		}
		for j := range task.Attachments[i].Details {
			if task.Attachments[i].Details[j].Name == "privateIPv4Address" {
				task.Attachments[i].Details[j].Value = ip
				return
			}
		}
	}
}
