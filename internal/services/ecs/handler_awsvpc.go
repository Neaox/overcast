package ecs

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

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
	subnetResolved = true
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
