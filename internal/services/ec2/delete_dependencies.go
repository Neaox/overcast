package ec2

// delete_dependencies.go — the dependency checks DeleteSecurityGroup,
// DeleteSubnet, and DeleteVpc consult before they touch the store.
//
// Real AWS refuses each of these operations with DependencyViolation while
// something still points at the resource being deleted, rather than deleting
// it out from under a dependent and leaving that dependent pointing at a
// ghost. Until #1135 this emulator deleted unconditionally, which let
// CloudFormation teardown ordering bugs go unnoticed instead of surfacing as
// DELETE_FAILED the way they do against real AWS.
//
// Each check reads the same stores the data plane already maintains
// (instances, network interfaces, subnets, gateways, …) rather than keeping a
// second, parallel ledger of what is attached to what. Every function returns
// nil when nothing blocks the delete.
//
// All three deletes are reached only through the legacy `http.HandlerFunc`
// path — PR #1217's typed registry kept them there (see typed_dispatch.go's
// allow-list) — so each check is exposed as one function on *Handler that the
// legacy DeleteX handler below calls directly. If a typed twin is ever added
// for one of these, it calls the same function and answers identically by
// construction, the property describe.go holds for Describe* filters.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// securityGroupDependencyError reports the group's refusal to be deleted, if
// any: CannotDelete for the VPC's default group, DependencyViolation while an
// instance still references it.
//
// AWS: deleting a security group that is associated with an instance or
// network interface, is referenced by another security group in the same
// VPC, or has a VPC association fails with DependencyViolation; the default
// group for a VPC cannot be deleted at all.
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteSecurityGroup.html
//
// This emulator does not model a security group attaching directly to a
// network interface (NetworkInterface has no Groups field — only an instance
// launch records a group reference, on Instance.SecurityGroups) or a rule
// referencing another group (IpPermission carries only IpRanges, never a
// UserIdGroupPairs equivalent). So the only attachment checked here is the
// one the data plane actually threads through: an instance's SecurityGroups.
func (h *Handler) securityGroupDependencyError(ctx context.Context, sg *SecurityGroup) *protocol.AWSError {
	if sg.GroupName == "default" {
		return ec2err("CannotDelete",
			fmt.Sprintf("the specified group: %q name: \"default\" cannot be deleted by a user", sg.GroupID),
			http.StatusBadRequest)
	}
	instances, aerr := h.store.listInstances(ctx)
	if aerr != nil {
		return aerr
	}
	for _, inst := range instances {
		if inst.State.Name == "terminated" {
			continue
		}
		for _, ref := range inst.SecurityGroups {
			if ref.GroupID == sg.GroupID {
				return ec2err("DependencyViolation",
					fmt.Sprintf("resource %s has a dependent object", sg.GroupID),
					http.StatusBadRequest)
			}
		}
	}
	return nil
}

// subnetDependencyError reports DependencyViolation while anything still
// lives inside the subnet.
//
// AWS: "You must terminate all running instances in the subnet before you can
// delete the subnet."
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteSubnet.html
//
// A NAT gateway sits in a subnet too, and real AWS attaches a network
// interface to every instance automatically — but this emulator's
// CreateNatGateway and RunInstances do not create a NetworkInterface record
// for either, so both are checked directly here rather than relying on an ENI
// scan alone.
func (h *Handler) subnetDependencyError(ctx context.Context, subnetID string) *protocol.AWSError {
	blocked := func() *protocol.AWSError {
		return ec2err("DependencyViolation",
			fmt.Sprintf("The subnet '%s' has dependencies and cannot be deleted", subnetID),
			http.StatusBadRequest)
	}

	enis, aerr := h.store.listNetworkInterfaces(ctx)
	if aerr != nil {
		return aerr
	}
	for _, eni := range enis {
		if eni.SubnetID == subnetID {
			return blocked()
		}
	}

	instances, aerr := h.store.listInstances(ctx)
	if aerr != nil {
		return aerr
	}
	for _, inst := range instances {
		if inst.SubnetID == subnetID && inst.State.Name != "terminated" {
			return blocked()
		}
	}

	natGateways, aerr := h.store.listNatGateways(ctx)
	if aerr != nil {
		return aerr
	}
	for _, ngw := range natGateways {
		if ngw.SubnetID == subnetID {
			return blocked()
		}
	}

	return nil
}

// vpcDependencyError reports DependencyViolation while the VPC still holds
// anything beyond what DeleteVpc itself auto-deletes with it: the default
// security group (see securityGroupDependencyError's carve-out — DeleteVpc
// never calls it) and the main route table (see deleteRouteTablesForVPC).
//
// AWS: "You must detach or delete all gateways and resources that are
// associated with the VPC before you can delete it… When you delete the VPC,
// it deletes the default security group, network ACL, and route table for
// the VPC."
// https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteVpc.html
func (h *Handler) vpcDependencyError(ctx context.Context, vpcID string) *protocol.AWSError {
	blocked := func() *protocol.AWSError {
		return ec2err("DependencyViolation",
			fmt.Sprintf("The vpc '%s' has dependencies and cannot be deleted", vpcID),
			http.StatusBadRequest)
	}

	subnets, aerr := h.store.listSubnets(ctx)
	if aerr != nil {
		return aerr
	}
	for _, s := range subnets {
		if s.VpcID == vpcID {
			return blocked()
		}
	}

	igws, aerr := h.store.listInternetGateways(ctx)
	if aerr != nil {
		return aerr
	}
	for _, igw := range igws {
		for _, a := range igw.Attachments {
			if a.VpcID == vpcID {
				return blocked()
			}
		}
	}

	vgws, aerr := h.store.listVpnGateways(ctx)
	if aerr != nil {
		return aerr
	}
	for _, vgw := range vgws {
		for _, a := range vgw.Attachments {
			if a.VpcID == vpcID {
				return blocked()
			}
		}
	}

	endpoints, aerr := h.store.listVpcEndpoints(ctx)
	if aerr != nil {
		return aerr
	}
	for _, ep := range endpoints {
		if ep.VpcID == vpcID {
			return blocked()
		}
	}

	peerings, aerr := h.store.listVpcPeeringConnections(ctx)
	if aerr != nil {
		return aerr
	}
	for _, pcx := range peerings {
		if pcx.Status.Code == "deleted" {
			continue
		}
		if pcx.RequesterVpcInfo.VpcID == vpcID || pcx.AccepterVpcInfo.VpcID == vpcID {
			return blocked()
		}
	}

	// A NAT gateway requires a subnet, so a lingering one is already caught
	// above. A network interface does too, but is checked directly anyway —
	// it is one of the three things AWS's own DeleteVpc error names
	// ("subnets, gateways, or ENIs"), and this guards against a future
	// data-model change that decouples an ENI from its subnet.
	enis, aerr := h.store.listNetworkInterfaces(ctx)
	if aerr != nil {
		return aerr
	}
	for _, eni := range enis {
		if eni.VpcID == vpcID {
			return blocked()
		}
	}

	instances, aerr := h.store.listInstances(ctx)
	if aerr != nil {
		return aerr
	}
	for _, inst := range instances {
		if inst.VpcID == vpcID && inst.State.Name != "terminated" {
			return blocked()
		}
	}

	return nil
}
