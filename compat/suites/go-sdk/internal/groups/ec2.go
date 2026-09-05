package groups

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

func EC2(c *clients.Clients) ServiceGroup {
	g := &ec2Group{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"ec2-instances:DescribeRegions":           g.DescribeRegions,
			"ec2-instances:DescribeAvailabilityZones": g.DescribeAvailabilityZones,
			"ec2-instances:DescribeInstances":         g.DescribeInstances,
			"ec2-instances:DescribeInstanceTypes":     g.DescribeInstanceTypes,
			// ECR models a DescribeImages of its own, so the bare key is
			// ambiguous and the loader refuses it.
			"ec2-instances:DescribeImages":                               g.DescribeImages,
			"ec2-instances:RunInstances":                                 g.RunInstances,
			"ec2-instances:RunInstancesHonoursPlacementAvailabilityZone": g.RunInstancesHonoursPlacementAvailabilityZone,
			"ec2-instances:StopInstances":                                g.StopInstances,
			"ec2-instances:StartInstances":                               g.StartInstances,
			"ec2-instances:TerminateInstances":                           g.TerminateInstances,
			"ec2-vpc:CreateVpc":                                          g.CreateVpc,
			"ec2-vpc:DescribeVpcs":                                       g.DescribeVpcs,
			"ec2-vpc:CreateVpnGateway":                                   g.CreateVpnGateway,
			"ec2-vpc:AttachVpnGateway":                                   g.AttachVpnGateway,
			"ec2-vpc:DescribeVpnGateways":                                g.DescribeVpnGateways,
			"ec2-vpc:CreateSubnet":                                       g.CreateSubnet,
			"ec2-vpc:DescribeSubnets":                                    g.DescribeSubnets,
			"ec2-vpc:CreateSecurityGroup":                                g.CreateSecurityGroup,
			"ec2-vpc:DeleteSecurityGroup":                                g.DeleteSecurityGroup,
			"ec2-vpc:CreateInternetGateway":                              g.CreateInternetGateway,
			"ec2-vpc:AttachInternetGateway":                              g.AttachInternetGateway,
			"ec2-vpc:DetachVpnGateway":                                   g.DetachVpnGateway,
			"ec2-vpc:DeleteVpnGateway":                                   g.DeleteVpnGateway,
			"ec2-vpc:DeleteSubnet":                                       g.DeleteSubnet,
			"ec2-vpc:DeleteVpc":                                          g.DeleteVpc,
			"ec2-security-group-rules:AuthorizeSecurityGroupIngress":     g.AuthorizeSecurityGroupIngress,
			"ec2-security-group-rules:DescribeSecurityGroups":            g.DescribeSecurityGroups,
			"ec2-security-group-rules:RevokeSecurityGroupIngress":        g.RevokeSecurityGroupIngress,
			"ec2-keypairs:CreateKeyPair":                                 g.CreateKeyPair,
			"ec2-keypairs:DescribeKeyPairs":                              g.DescribeKeyPairs,
			"ec2-keypairs:DeleteKeyPair":                                 g.DeleteKeyPair,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"ec2-instances":            g.setupInstances,
			"ec2-vpc":                  g.setupVPC,
			"ec2-security-group-rules": g.setupSGRules,
			"ec2-keypairs":             g.setupNoop,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"ec2-instances":            g.teardownInstances,
			"ec2-vpc":                  g.teardownVPC,
			"ec2-security-group-rules": g.teardownSGRules,
			"ec2-keypairs":             g.teardownKeyPairs,
		},
	}
}

type ec2Group struct{ c *clients.Clients }

func (g *ec2Group) cl() *ec2.Client { return g.c.EC2() }

func (g *ec2Group) setupInstances(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *ec2Group) setupVPC(_ context.Context, _ *harness.TestContext) error       { return nil }
func (g *ec2Group) setupNoop(_ context.Context, _ *harness.TestContext) error      { return nil }

func (g *ec2Group) teardownInstances(ctx context.Context, t *harness.TestContext) error {
	if instanceID := t.GetString("ec2_instance_id"); instanceID != "" {
		g.cl().TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}}) //nolint:errcheck
	}
	if instanceID := t.GetString("ec2_az_instance_id"); instanceID != "" {
		g.cl().TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}}) //nolint:errcheck
	}
	return nil
}

func (g *ec2Group) teardownVPC(ctx context.Context, t *harness.TestContext) error {
	if sgID := t.GetString("ec2_sg_id"); sgID != "" {
		g.cl().DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}) //nolint:errcheck
	}
	if igwID := t.GetString("ec2_igw_id"); igwID != "" {
		if vpcID := t.GetString("ec2_vpc_id"); vpcID != "" {
			g.cl().DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{InternetGatewayId: aws.String(igwID), VpcId: aws.String(vpcID)}) //nolint:errcheck
		}
		g.cl().DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{InternetGatewayId: aws.String(igwID)}) //nolint:errcheck
	}
	if vpnGatewayID := t.GetString("ec2_vpn_gateway_id"); vpnGatewayID != "" {
		if vpcID := t.GetString("ec2_vpc_id"); vpcID != "" {
			g.cl().DetachVpnGateway(ctx, &ec2.DetachVpnGatewayInput{VpcId: aws.String(vpcID), VpnGatewayId: aws.String(vpnGatewayID)}) //nolint:errcheck
		}
		g.cl().DeleteVpnGateway(ctx, &ec2.DeleteVpnGatewayInput{VpnGatewayId: aws.String(vpnGatewayID)}) //nolint:errcheck
	}
	if subnetID := t.GetString("ec2_subnet_id"); subnetID != "" {
		g.cl().DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)}) //nolint:errcheck
	}
	if vpcID := t.GetString("ec2_vpc_id"); vpcID != "" {
		g.cl().DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)}) //nolint:errcheck
	}
	return nil
}

func (g *ec2Group) DescribeRegions(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DescribeRegions(ctx, &ec2.DescribeRegionsInput{})
	return err
}

func (g *ec2Group) DescribeAvailabilityZones(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{})
	return err
}

func (g *ec2Group) DescribeInstances(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	return err
}

func (g *ec2Group) DescribeInstanceTypes(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{})
	return err
}

func (g *ec2Group) CreateVpc(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
	})
	if err != nil {
		return err
	}
	if resp.Vpc == nil || resp.Vpc.VpcId == nil {
		return fmt.Errorf("CreateVpc: missing VpcId")
	}
	t.Set("ec2_vpc_id", *resp.Vpc.VpcId)
	return nil
}

func (g *ec2Group) DescribeVpcs(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeVpcs(ctx, &ec2.DescribeVpcsInput{})
	if err != nil {
		return err
	}
	if resp.Vpcs == nil {
		return fmt.Errorf("DescribeVpcs: missing Vpcs")
	}
	return nil
}

func (g *ec2Group) CreateSubnet(ctx context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("ec2_vpc_id")
	if vpcID == "" {
		return fmt.Errorf("CreateSubnet: no VPC from CreateVpc")
	}
	resp, err := g.cl().CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String("10.0.1.0/24"),
	})
	if err != nil {
		return err
	}
	if resp.Subnet == nil || resp.Subnet.SubnetId == nil {
		return fmt.Errorf("CreateSubnet: missing SubnetId")
	}
	t.Set("ec2_subnet_id", *resp.Subnet.SubnetId)
	return nil
}

func (g *ec2Group) CreateSecurityGroup(ctx context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("ec2_vpc_id")
	if vpcID == "" {
		return fmt.Errorf("CreateSecurityGroup: no VPC from CreateVpc")
	}
	groupName := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(groupName),
		Description: aws.String("compat test group"),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		return err
	}
	if resp.GroupId == nil {
		return fmt.Errorf("CreateSecurityGroup: missing GroupId")
	}
	t.Set("ec2_sg_id", *resp.GroupId)
	return nil
}

func (g *ec2Group) DeleteSecurityGroup(ctx context.Context, t *harness.TestContext) error {
	sgID := t.GetString("ec2_sg_id")
	if sgID == "" {
		return nil
	}
	_, err := g.cl().DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: aws.String(sgID),
	})
	return err
}

func (g *ec2Group) DeleteSubnet(ctx context.Context, t *harness.TestContext) error {
	subnetID := t.GetString("ec2_subnet_id")
	if subnetID == "" {
		return nil
	}
	_, err := g.cl().DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
		SubnetId: aws.String(subnetID),
	})
	return err
}

func (g *ec2Group) DeleteVpc(ctx context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("ec2_vpc_id")
	if vpcID == "" {
		return nil
	}
	// AWS refuses DeleteVpc while an internet gateway is still attached
	// (DependencyViolation), and this group's own AttachInternetGateway test
	// leaves one attached with nothing downstream detaching it — detach (and
	// delete) it here first, the same way DeleteSecurityGroup/DeleteSubnet/
	// DeleteVpnGateway already run ahead of DeleteVpc in the dependency graph.
	if igwID := t.GetString("ec2_igw_id"); igwID != "" {
		g.cl().DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{InternetGatewayId: aws.String(igwID), VpcId: aws.String(vpcID)}) //nolint:errcheck
		g.cl().DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{InternetGatewayId: aws.String(igwID)})                           //nolint:errcheck
	}
	_, err := g.cl().DeleteVpc(ctx, &ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	})
	return err
}

// ── ec2-instances (additional) ─────────────────────────────────────────────

func (g *ec2Group) DescribeImages(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().DescribeImages(ctx, &ec2.DescribeImagesInput{})
	if err != nil {
		return err
	}
	if resp.Images == nil {
		return fmt.Errorf("DescribeImages: missing Images")
	}
	return nil
}

func (g *ec2Group) RunInstances(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-00000000"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		return err
	}
	if len(resp.Instances) == 0 {
		return fmt.Errorf("RunInstances: no instances returned")
	}
	if resp.Instances[0].InstanceId == nil {
		return fmt.Errorf("RunInstances: missing InstanceId")
	}
	t.Set("ec2_instance_id", *resp.Instances[0].InstanceId)
	return nil
}

// RunInstancesHonoursPlacementAvailabilityZone launches into a zone that is
// deliberately not the region's first one, so that a server which ignores
// Placement.AvailabilityZone and reports its own default is caught. Both the
// RunInstances response and a follow-up DescribeInstances must report the
// requested zone.
func (g *ec2Group) RunInstancesHonoursPlacementAvailabilityZone(ctx context.Context, t *harness.TestContext) error {
	zone := t.Region + "c"
	resp, err := g.cl().RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-00000000"),
		InstanceType: ec2types.InstanceTypeT2Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		Placement:    &ec2types.Placement{AvailabilityZone: aws.String(zone)},
	})
	if err != nil {
		return err
	}
	if len(resp.Instances) == 0 {
		return fmt.Errorf("RunInstances: no instances returned")
	}
	instanceID := aws.ToString(resp.Instances[0].InstanceId)
	if instanceID == "" {
		return fmt.Errorf("RunInstances: missing InstanceId")
	}
	t.Set("ec2_az_instance_id", instanceID)
	if got := placementZone(resp.Instances[0].Placement); got != zone {
		return fmt.Errorf("RunInstances: Placement.AvailabilityZone = %q, want %q", got, zone)
	}

	desc, err := g.cl().DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return err
	}
	if len(desc.Reservations) == 0 || len(desc.Reservations[0].Instances) == 0 {
		return fmt.Errorf("DescribeInstances: instance %s not returned", instanceID)
	}
	if got := placementZone(desc.Reservations[0].Instances[0].Placement); got != zone {
		return fmt.Errorf("DescribeInstances: Placement.AvailabilityZone = %q, want %q", got, zone)
	}
	return nil
}

// placementZone reads an instance's availability zone, tolerating a missing
// Placement so a bad response is reported as a zone mismatch rather than a panic.
func placementZone(p *ec2types.Placement) string {
	if p == nil {
		return ""
	}
	return aws.ToString(p.AvailabilityZone)
}

func (g *ec2Group) StopInstances(ctx context.Context, t *harness.TestContext) error {
	instanceID := t.GetString("ec2_instance_id")
	if instanceID == "" {
		return fmt.Errorf("StopInstances: no instance from RunInstances")
	}
	resp, err := g.cl().StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return err
	}
	if len(resp.StoppingInstances) == 0 {
		return fmt.Errorf("StopInstances: no StoppingInstances returned")
	}
	return nil
}

func (g *ec2Group) StartInstances(ctx context.Context, t *harness.TestContext) error {
	instanceID := t.GetString("ec2_instance_id")
	if instanceID == "" {
		return fmt.Errorf("StartInstances: no instance from RunInstances")
	}
	resp, err := g.cl().StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return err
	}
	if len(resp.StartingInstances) == 0 {
		return fmt.Errorf("StartInstances: no StartingInstances returned")
	}
	return nil
}

func (g *ec2Group) TerminateInstances(ctx context.Context, t *harness.TestContext) error {
	instanceID := t.GetString("ec2_instance_id")
	if instanceID == "" {
		return fmt.Errorf("TerminateInstances: no instance from RunInstances")
	}
	resp, err := g.cl().TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return err
	}
	if len(resp.TerminatingInstances) == 0 {
		return fmt.Errorf("TerminateInstances: no TerminatingInstances returned")
	}
	return nil
}

// ── ec2-vpc (additional) ───────────────────────────────────────────────────

func (g *ec2Group) DescribeSubnets(ctx context.Context, t *harness.TestContext) error {
	subnetID := t.GetString("ec2_subnet_id")
	if subnetID == "" {
		return fmt.Errorf("DescribeSubnets: no subnet from CreateSubnet")
	}
	resp, err := g.cl().DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetID},
	})
	if err != nil {
		return err
	}
	if len(resp.Subnets) == 0 {
		return fmt.Errorf("DescribeSubnets: no subnets returned")
	}
	return nil
}

func (g *ec2Group) CreateInternetGateway(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{})
	if err != nil {
		return err
	}
	if resp.InternetGateway == nil || resp.InternetGateway.InternetGatewayId == nil {
		return fmt.Errorf("CreateInternetGateway: missing InternetGatewayId")
	}
	t.Set("ec2_igw_id", *resp.InternetGateway.InternetGatewayId)
	return nil
}

func (g *ec2Group) AttachInternetGateway(ctx context.Context, t *harness.TestContext) error {
	igwID := t.GetString("ec2_igw_id")
	vpcID := t.GetString("ec2_vpc_id")
	if igwID == "" || vpcID == "" {
		return fmt.Errorf("AttachInternetGateway: missing igw_id or vpc_id")
	}
	_, err := g.cl().AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwID),
		VpcId:             aws.String(vpcID),
	})
	return err
}

func (g *ec2Group) CreateVpnGateway(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateVpnGateway(ctx, &ec2.CreateVpnGatewayInput{
		Type:          ec2types.GatewayTypeIpsec1,
		AmazonSideAsn: aws.Int64(65001),
	})
	if err != nil {
		return err
	}
	if resp.VpnGateway == nil || resp.VpnGateway.VpnGatewayId == nil {
		return fmt.Errorf("CreateVpnGateway: missing VpnGatewayId")
	}
	t.Set("ec2_vpn_gateway_id", *resp.VpnGateway.VpnGatewayId)
	return nil
}

func (g *ec2Group) AttachVpnGateway(ctx context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("ec2_vpc_id")
	vpnGatewayID := t.GetString("ec2_vpn_gateway_id")
	if vpcID == "" {
		return fmt.Errorf("AttachVpnGateway: no VPC from CreateVpc")
	}
	if vpnGatewayID == "" {
		return fmt.Errorf("AttachVpnGateway: no gateway from CreateVpnGateway")
	}
	resp, err := g.cl().AttachVpnGateway(ctx, &ec2.AttachVpnGatewayInput{
		VpcId:        aws.String(vpcID),
		VpnGatewayId: aws.String(vpnGatewayID),
	})
	if err != nil {
		return err
	}
	if resp.VpcAttachment == nil || resp.VpcAttachment.VpcId == nil || *resp.VpcAttachment.VpcId != vpcID {
		return fmt.Errorf("AttachVpnGateway: VpcAttachment VpcId mismatch")
	}
	return nil
}

func (g *ec2Group) DescribeVpnGateways(ctx context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("ec2_vpc_id")
	vpnGatewayID := t.GetString("ec2_vpn_gateway_id")
	if vpcID == "" {
		return fmt.Errorf("DescribeVpnGateways: no VPC from CreateVpc")
	}
	if vpnGatewayID == "" {
		return fmt.Errorf("DescribeVpnGateways: no gateway from CreateVpnGateway")
	}
	resp, err := g.cl().DescribeVpnGateways(ctx, &ec2.DescribeVpnGatewaysInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("attachment.state"), Values: []string{"attached"}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return err
	}
	if len(resp.VpnGateways) != 1 {
		return fmt.Errorf("DescribeVpnGateways: expected one VPN gateway, got %d", len(resp.VpnGateways))
	}
	return nil
}

func (g *ec2Group) DetachVpnGateway(ctx context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("ec2_vpc_id")
	vpnGatewayID := t.GetString("ec2_vpn_gateway_id")
	if vpcID == "" || vpnGatewayID == "" {
		return nil
	}
	_, err := g.cl().DetachVpnGateway(ctx, &ec2.DetachVpnGatewayInput{
		VpcId:        aws.String(vpcID),
		VpnGatewayId: aws.String(vpnGatewayID),
	})
	return err
}

func (g *ec2Group) DeleteVpnGateway(ctx context.Context, t *harness.TestContext) error {
	vpnGatewayID := t.GetString("ec2_vpn_gateway_id")
	if vpnGatewayID == "" {
		return nil
	}
	_, err := g.cl().DeleteVpnGateway(ctx, &ec2.DeleteVpnGatewayInput{
		VpnGatewayId: aws.String(vpnGatewayID),
	})
	return err
}

// ── ec2-security-group-rules ───────────────────────────────────────────────

func (g *ec2Group) setupSGRules(ctx context.Context, t *harness.TestContext) error {
	vpcResp, err := g.cl().CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.100.0.0/16")})
	if err != nil {
		return err
	}
	t.Set("ec2_sgrules_vpc_id", *vpcResp.Vpc.VpcId)

	sgResp, err := g.cl().CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(fmt.Sprintf("compat-sgrules-%s", t.RunID)),
		Description: aws.String("compat SG rules test"),
		VpcId:       vpcResp.Vpc.VpcId,
	})
	if err != nil {
		return err
	}
	t.Set("ec2_sgrules_sg_id", *sgResp.GroupId)
	return nil
}

func (g *ec2Group) teardownSGRules(ctx context.Context, t *harness.TestContext) error {
	if sgID := t.GetString("ec2_sgrules_sg_id"); sgID != "" {
		g.cl().DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}) //nolint:errcheck
	}
	if vpcID := t.GetString("ec2_sgrules_vpc_id"); vpcID != "" {
		g.cl().DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)}) //nolint:errcheck
	}
	return nil
}

func (g *ec2Group) AuthorizeSecurityGroupIngress(ctx context.Context, t *harness.TestContext) error {
	sgID := t.GetString("ec2_sgrules_sg_id")
	if sgID == "" {
		return fmt.Errorf("AuthorizeSecurityGroupIngress: no SG from setup")
	}
	_, err := g.cl().AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []ec2types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(443),
				ToPort:     aws.Int32(443),
				IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
			},
		},
	})
	return err
}

func (g *ec2Group) DescribeSecurityGroups(ctx context.Context, t *harness.TestContext) error {
	sgID := t.GetString("ec2_sgrules_sg_id")
	if sgID == "" {
		return fmt.Errorf("DescribeSecurityGroups: no SG from setup")
	}
	resp, err := g.cl().DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	if err != nil {
		return err
	}
	if len(resp.SecurityGroups) == 0 {
		return fmt.Errorf("DescribeSecurityGroups: no security groups returned")
	}
	return nil
}

func (g *ec2Group) RevokeSecurityGroupIngress(ctx context.Context, t *harness.TestContext) error {
	sgID := t.GetString("ec2_sgrules_sg_id")
	if sgID == "" {
		return fmt.Errorf("RevokeSecurityGroupIngress: no SG from setup")
	}
	_, err := g.cl().RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []ec2types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(443),
				ToPort:     aws.Int32(443),
				IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
			},
		},
	})
	return err
}

// ── ec2-keypairs ───────────────────────────────────────────────────────────

func (g *ec2Group) teardownKeyPairs(ctx context.Context, t *harness.TestContext) error {
	if keyName := t.GetString("ec2_key_name"); keyName != "" {
		g.cl().DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyName: aws.String(keyName)}) //nolint:errcheck
	}
	return nil
}

func (g *ec2Group) CreateKeyPair(ctx context.Context, t *harness.TestContext) error {
	keyName := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateKeyPair(ctx, &ec2.CreateKeyPairInput{
		KeyName: aws.String(keyName),
	})
	if err != nil {
		return err
	}
	if resp.KeyPairId == nil {
		return fmt.Errorf("CreateKeyPair: missing KeyPairId")
	}
	t.Set("ec2_key_name", keyName)
	return nil
}

func (g *ec2Group) DescribeKeyPairs(ctx context.Context, t *harness.TestContext) error {
	keyName := t.GetString("ec2_key_name")
	if keyName == "" {
		return fmt.Errorf("DescribeKeyPairs: no key from CreateKeyPair")
	}
	resp, err := g.cl().DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{
		KeyNames: []string{keyName},
	})
	if err != nil {
		return err
	}
	if len(resp.KeyPairs) == 0 {
		return fmt.Errorf("DescribeKeyPairs: no key pairs returned")
	}
	return nil
}

func (g *ec2Group) DeleteKeyPair(ctx context.Context, t *harness.TestContext) error {
	keyName := t.GetString("ec2_key_name")
	if keyName == "" {
		return nil
	}
	_, err := g.cl().DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
		KeyName: aws.String(keyName),
	})
	return err
}
