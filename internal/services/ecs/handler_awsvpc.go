package ecs

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
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
// are rejected with InvalidParameterException. The boolean subnetResolved indicates
// whether the subnet was successfully resolved to a VPC (vs. synthetic/non-EC2 subnet).
// Returns (subnetID, vpcID, dockerNetworkID, subnetResolved, error).
func (h *Handler) resolveAwsvpcPlacement(
	ctx context.Context,
	networkConfiguration *NetworkConfiguration,
	opName string,
) (subnetID, vpcID, networkID string, subnetResolved bool, aerr *protocol.AWSError) {
	if networkConfiguration == nil {
		return "", "", "", false, nil
	}
	subnetID = firstOrEmpty(networkConfiguration.AwsvpcConfiguration, func(a *AwsvpcConfiguration) string {
		if len(a.Subnets) > 0 {
			return a.Subnets[0]
		}
		return ""
	})
	if subnetID == "" || h.vpcResolver == nil {
		return subnetID, "", "", false, nil
	}
	vpcID = h.vpcResolver.VpcIDForSubnet(ctx, subnetID)
	if vpcID == "" {
		// Preserve existing ECS behaviour for synthetic/non-EC2 subnets.
		return subnetID, "", "", false, nil
	}
	switch status := h.vpcResolver.VPCNetworkStatus(ctx, vpcID); status {
	case "", "ok", "shared", "remapped":
		networkID = h.vpcResolver.DockerNetworkForVpc(ctx, vpcID)
		return subnetID, vpcID, networkID, true, nil
	case "conflict", "unbacked":
		return subnetID, vpcID, "", true, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    fmt.Sprintf("VPC '%s' is not launchable for %s (network status=%s).", vpcID, opName, status),
			HTTPStatus: http.StatusBadRequest,
		}
	default:
		return subnetID, vpcID, "", true, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    fmt.Sprintf("VPC '%s' is not launchable for %s (network status=%s).", vpcID, opName, status),
			HTTPStatus: http.StatusBadRequest,
		}
	}
}
