package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func EC2(c *clients.Clients) ServiceGroup {
	g := &ec2Group{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"DescribeRegions":               g.DescribeRegions,
			"DescribeAvailabilityZones":     g.DescribeAvailabilityZones,
			"DescribeInstances":             g.DescribeInstances,
			"DescribeInstanceTypes":         g.DescribeInstanceTypes,
			"DescribeImages":                g.DescribeImages,
			"RunInstances":                  g.RunInstances,
			"StopInstances":                 g.StopInstances,
			"StartInstances":                g.StartInstances,
			"TerminateInstances":            g.TerminateInstances,
			"CreateVpc":                     g.CreateVpc,
			"DescribeVpcs":                  g.DescribeVpcs,
			"CreateSubnet":                  g.CreateSubnet,
			"DescribeSubnets":               g.DescribeSubnets,
			"CreateSecurityGroup":           g.CreateSecurityGroup,
			"DeleteSecurityGroup":           g.DeleteSecurityGroup,
			"CreateInternetGateway":         g.CreateInternetGateway,
			"AttachInternetGateway":         g.AttachInternetGateway,
			"DeleteSubnet":                  g.DeleteSubnet,
			"DeleteVpc":                     g.DeleteVpc,
			"AuthorizeSecurityGroupIngress": g.AuthorizeSecurityGroupIngress,
			"DescribeSecurityGroups":        g.DescribeSecurityGroups,
			"RevokeSecurityGroupIngress":    g.RevokeSecurityGroupIngress,
			"CreateKeyPair":                 g.CreateKeyPair,
			"DescribeKeyPairs":              g.DescribeKeyPairs,
			"DeleteKeyPair":                 g.DeleteKeyPair,
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
