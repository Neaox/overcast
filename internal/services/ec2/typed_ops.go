package ec2

import (
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/protocol/op"
)

func (h *Handler) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"DescribeRegions":               op.NewTyped("DescribeRegions", h.describeRegionsTyped),
		"DescribeAvailabilityZones":     op.NewTyped("DescribeAvailabilityZones", h.describeAzsTyped),
		"DescribeInstances":             op.NewTyped("DescribeInstances", h.describeInstancesTyped),
		"DescribeInstanceTypes":         op.NewTyped("DescribeInstanceTypes", h.describeInstanceTypesTyped),
		"CreateVpc":                     op.NewTyped[createVpcReq, createVpcResp]("CreateVpc", h.createVpcTyped),
		"DescribeVpcs":                  op.NewTyped("DescribeVpcs", h.describeVpcsTyped),
		"DeleteVpc":                     op.NewTyped[deleteVpcReq, deleteVpcResp]("DeleteVpc", h.deleteVpcTyped),
		"CreateSubnet":                  op.NewTyped[createSubnetReq, createSubnetResp]("CreateSubnet", h.createSubnetTyped),
		"DeleteSubnet":                  op.NewTyped[deleteSubnetReq, deleteSubnetResp]("DeleteSubnet", h.deleteSubnetTyped),
		"CreateSecurityGroup":           op.NewTyped[createSecurityGroupReq, createSGResp]("CreateSecurityGroup", h.createSecurityGroupTyped),
		"DeleteSecurityGroup":           op.NewTyped[deleteSecurityGroupReq, deleteSGResp]("DeleteSecurityGroup", h.deleteSecurityGroupTyped),
		"AuthorizeSecurityGroupIngress": op.NewTyped[authorizeSGIngressReq, authorizeSGIngressResp]("AuthorizeSecurityGroupIngress", h.authorizeSGIngressTyped),
		"AuthorizeSecurityGroupEgress":  op.NewTyped[authorizeSGEgressReq, authorizeSGEgressResp]("AuthorizeSecurityGroupEgress", h.authorizeSGEgressTyped),
		"RevokeSecurityGroupIngress":    op.NewTyped[revokeSGIngressReq, revokeSGIngressResp]("RevokeSecurityGroupIngress", h.revokeSGIngressTyped),
		"RevokeSecurityGroupEgress":     op.NewTyped[revokeSGEgressReq, revokeSGEgressResp]("RevokeSecurityGroupEgress", h.revokeSGEgressTyped),
		"DescribeSecurityGroups":        op.NewTyped("DescribeSecurityGroups", h.describeSecurityGroupsTyped),
		"DescribeSubnets":               op.NewTyped("DescribeSubnets", h.describeSubnetsTyped),
		"RunInstances":                  op.NewTyped[runInstancesReq, runInstancesResp]("RunInstances", h.runInstancesTyped),
		"TerminateInstances":            op.NewTyped[terminateInstancesReq, terminateInstancesResp]("TerminateInstances", h.terminateInstancesTyped),
		"StartInstances":                op.NewTyped[startInstancesReq, startInstancesResp]("StartInstances", h.startInstancesTyped),
		"StopInstances":                 op.NewTyped[stopInstancesReq, stopInstancesResp]("StopInstances", h.stopInstancesTyped),
		"DescribeImages":                op.NewTyped("DescribeImages", h.describeImagesTyped),
		"CreateKeyPair":                 op.NewTyped[createKeyPairReq, createKeyPairResp]("CreateKeyPair", h.createKeyPairTyped),
		"DescribeKeyPairs":              op.NewTyped("DescribeKeyPairs", h.describeKeyPairsTyped),
		"DeleteKeyPair":                 op.NewTyped[deleteKeyPairReq, deleteKeyPairResp]("DeleteKeyPair", h.deleteKeyPairTyped),
		"CreateRouteTable":              op.NewTyped[createRouteTableReq, createRouteTableResp]("CreateRouteTable", h.createRouteTableTyped),
		"DescribeRouteTables":           op.NewTyped("DescribeRouteTables", h.describeRouteTablesTyped),
		"DeleteRouteTable":              op.NewTyped[deleteRouteTableReq, deleteRouteTableResp]("DeleteRouteTable", h.deleteRouteTableTyped),
		"CreateRoute":                   op.NewTyped[createRouteReq, createRouteResp]("CreateRoute", h.createRouteTyped),
		"DeleteRoute":                   op.NewTyped[deleteRouteReq, deleteRouteResp]("DeleteRoute", h.deleteRouteTyped),
		"AssociateRouteTable":           op.NewTyped[associateRouteTableReq, associateRouteTableResp]("AssociateRouteTable", h.associateRouteTableTyped),
		"DisassociateRouteTable":        op.NewTyped[disassociateRouteTableReq, disassociateRouteTableResp]("DisassociateRouteTable", h.disassociateRouteTableTyped),
		"CreateInternetGateway":         op.NewTyped[createIGWReq, createIGWResp]("CreateInternetGateway", h.createIGWTyped),
		"DescribeInternetGateways":      op.NewTyped("DescribeInternetGateways", h.describeIGWsTyped),
		"DeleteInternetGateway":         op.NewTyped[deleteIGWReq, deleteIGWResp]("DeleteInternetGateway", h.deleteIGWTyped),
		"AttachInternetGateway":         op.NewTyped[attachIGWReq, attachIGWResp]("AttachInternetGateway", h.attachIGWTyped),
		"DetachInternetGateway":         op.NewTyped[detachIGWReq, detachIGWResp]("DetachInternetGateway", h.detachIGWTyped),
		"CreateVpcPeeringConnection":    op.NewTyped[createVPCPeeringReq, createVPCPeeringResp]("CreateVpcPeeringConnection", h.createVPCPeeringTyped),
		"AcceptVpcPeeringConnection":    op.NewTyped[acceptVPCPeeringReq, acceptVPCPeeringResp]("AcceptVpcPeeringConnection", h.acceptVPCPeeringTyped),
		"DescribeVpcPeeringConnections": op.NewTyped("DescribeVpcPeeringConnections", h.describeVPCPeeringsTyped),
		"DeleteVpcPeeringConnection":    op.NewTyped[deleteVPCPeeringReq, deleteVPCPeeringResp]("DeleteVpcPeeringConnection", h.deleteVPCPeeringTyped),
		"CreateTags":                    op.NewTyped[createTagsReq, createTagsResp]("CreateTags", h.createTagsTyped),
		"DeleteTags":                    op.NewTyped[deleteTagsReq, deleteTagsResp]("DeleteTags", h.deleteTagsTyped),
		"DescribeTags":                  op.NewTyped("DescribeTags", h.describeTagsTyped),
		"AllocateAddress":               op.NewTyped[allocateAddressReq, allocateAddressResp]("AllocateAddress", h.allocateAddressTyped),
		"ReleaseAddress":                op.NewTyped[releaseAddressReq, releaseAddressResp]("ReleaseAddress", h.releaseAddressTyped),
		"DescribeAddresses":             op.NewTyped("DescribeAddresses", h.describeAddressesTyped),
		"AssociateAddress":              op.NewTyped[associateAddressReq, associateAddressResp]("AssociateAddress", h.associateAddressTyped),
		"DisassociateAddress":           op.NewTyped[disassociateAddressReq, disassociateAddressResp]("DisassociateAddress", h.disassociateAddressTyped),
		"CreateNatGateway":              op.NewTyped[createNatGatewayReq, createNatGatewayResp]("CreateNatGateway", h.createNatGatewayTyped),
		"DescribeNatGateways":           op.NewTyped("DescribeNatGateways", h.describeNatGatewaysTyped),
		"DeleteNatGateway":              op.NewTyped[deleteNatGatewayReq, deleteNatGatewayResp]("DeleteNatGateway", h.deleteNatGatewayTyped),
		"ModifySubnetAttribute":         op.NewTyped[modifySubnetAttributeReq, modifySubnetAttributeResp]("ModifySubnetAttribute", h.modifySubnetAttributeTyped),
		"ModifyVpcAttribute":            op.NewTyped[modifyVpcAttributeReq, modifyVpcAttributeResp]("ModifyVpcAttribute", h.modifyVpcAttributeTyped),
		"DescribeVpcAttribute":          op.NewTyped("DescribeVpcAttribute", h.describeVpcAttributeTyped),
		"DescribeDhcpOptions":           op.NewTyped("DescribeDhcpOptions", h.describeDhcpOptionsTyped),
		"DescribeAccountAttributes":     op.NewTyped("DescribeAccountAttributes", h.describeAccountAttributesTyped),
		"CreateVpnGateway":              op.NewTyped[createVpnGatewayReq, createVpnGatewayResp]("CreateVpnGateway", h.createVpnGatewayTyped),
		"AttachVpnGateway":              op.NewTyped[attachVpnGatewayReq, attachVpnGatewayResp]("AttachVpnGateway", h.attachVpnGatewayTyped),
		"DescribeVpnGateways":           op.NewTyped("DescribeVpnGateways", h.describeVpnGatewaysTyped),
		"DetachVpnGateway":              op.NewTyped[detachVpnGatewayReq, detachVpnGatewayResp]("DetachVpnGateway", h.detachVpnGatewayTyped),
		"DeleteVpnGateway":              op.NewTyped[deleteVpnGatewayReq, deleteVpnGatewayResp]("DeleteVpnGateway", h.deleteVpnGatewayTyped),
		"CreateNetworkInterface":        op.NewTyped[createNetworkInterfaceReq, createNetworkInterfaceResp]("CreateNetworkInterface", h.createNetworkInterfaceTyped),
		"DescribeNetworkInterfaces":     op.NewTyped("DescribeNetworkInterfaces", h.describeNetworkInterfacesTyped),
		"DeleteNetworkInterface":        op.NewTyped[deleteNetworkInterfaceReq, deleteNetworkInterfaceResp]("DeleteNetworkInterface", h.deleteNetworkInterfaceTyped),
		"ModifyInstanceAttribute":       op.NewTyped[modifyInstanceAttributeReq, modifyInstanceAttributeResp]("ModifyInstanceAttribute", h.modifyInstanceAttributeTyped),
		"CreateVpcEndpoint":             op.NewTyped[createVpcEndpointReq, createVpcEndpointResp]("CreateVpcEndpoint", h.createVpcEndpointTyped),
		"DescribeVpcEndpoints":          op.NewTyped("DescribeVpcEndpoints", h.describeVpcEndpointsTyped),
		"DeleteVpcEndpoints":            op.NewTyped[deleteVpcEndpointsReq, deleteVpcEndpointsResp]("DeleteVpcEndpoints", h.deleteVpcEndpointsTyped),
	}
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}
