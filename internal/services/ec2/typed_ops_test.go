package ec2

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var ec2Ops = []string{
	"DescribeRegions", "DescribeAvailabilityZones",
	"DescribeInstances", "DescribeInstanceTypes",
	"CreateVpc", "DescribeVpcs", "DeleteVpc",
	"CreateSubnet", "DeleteSubnet",
	"CreateSecurityGroup", "DeleteSecurityGroup",
	"AuthorizeSecurityGroupIngress", "AuthorizeSecurityGroupEgress",
	"RevokeSecurityGroupIngress", "RevokeSecurityGroupEgress",
	"DescribeSecurityGroups", "DescribeSubnets",
	"RunInstances", "TerminateInstances", "StartInstances", "StopInstances",
	"DescribeImages",
	"CreateKeyPair", "DescribeKeyPairs", "DeleteKeyPair",
	"CreateRouteTable", "DescribeRouteTables", "DeleteRouteTable",
	"CreateRoute", "DeleteRoute",
	"AssociateRouteTable", "DisassociateRouteTable",
	"CreateInternetGateway", "DescribeInternetGateways", "DeleteInternetGateway",
	"AttachInternetGateway", "DetachInternetGateway",
	"CreateVpcPeeringConnection", "AcceptVpcPeeringConnection",
	"DescribeVpcPeeringConnections", "DeleteVpcPeeringConnection",
	"CreateTags", "DeleteTags", "DescribeTags",
	"AllocateAddress", "ReleaseAddress", "DescribeAddresses",
	"AssociateAddress", "DisassociateAddress",
	"CreateNatGateway", "DescribeNatGateways", "DeleteNatGateway",
	"ModifySubnetAttribute", "ModifyVpcAttribute",
	"DescribeVpcAttribute", "DescribeDhcpOptions", "DescribeAccountAttributes",
	"CreateVpnGateway", "AttachVpnGateway", "DescribeVpnGateways",
	"DetachVpnGateway", "DeleteVpnGateway",
	"CreateNetworkInterface", "DescribeNetworkInterfaces", "DeleteNetworkInterface",
	"ModifyInstanceAttribute",
	"CreateVpcEndpoint", "DescribeVpcEndpoints", "DeleteVpcEndpoints",
}

func TestTypedOps_matchLegacyRegistry(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.handler.typedOp) != len(ec2Ops) {
		t.Fatalf("typed op count = %d, want %d", len(s.handler.typedOp), len(ec2Ops))
	}
	for _, name := range ec2Ops {
		operation, ok := s.handler.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
	}
}
