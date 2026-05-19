package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// EC2 returns the EC2 service group.
func EC2() ServiceGroup {
	g := &ec2CliGroup{}
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

type ec2CliGroup struct{}

func (g *ec2CliGroup) setupInstances(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *ec2CliGroup) setupNoop(_ context.Context, _ *harness.TestContext) error      { return nil }

func (g *ec2CliGroup) teardownInstances(_ context.Context, t *harness.TestContext) error {
	if instanceID := t.GetString("instance_id"); instanceID != "" {
		awscli.Run(t.Endpoint, t.Region, "ec2", "terminate-instances", "--instance-ids", instanceID) //nolint:errcheck
	}
	return nil
}

func (g *ec2CliGroup) setupVPC(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *ec2CliGroup) teardownVPC(_ context.Context, t *harness.TestContext) error {
	// Best-effort cleanup
	if sgID := t.GetString("sg_id"); sgID != "" {
		awscli.Run(t.Endpoint, t.Region, "ec2", "delete-security-group", "--group-id", sgID) //nolint:errcheck
	}
	if igwID := t.GetString("igw_id"); igwID != "" {
		if vpcID := t.GetString("vpc_id"); vpcID != "" {
			awscli.Run(t.Endpoint, t.Region, "ec2", "detach-internet-gateway", "--internet-gateway-id", igwID, "--vpc-id", vpcID) //nolint:errcheck
		}
		awscli.Run(t.Endpoint, t.Region, "ec2", "delete-internet-gateway", "--internet-gateway-id", igwID) //nolint:errcheck
	}
	if subnetID := t.GetString("subnet_id"); subnetID != "" {
		awscli.Run(t.Endpoint, t.Region, "ec2", "delete-subnet", "--subnet-id", subnetID) //nolint:errcheck
	}
	if vpcID := t.GetString("vpc_id"); vpcID != "" {
		awscli.Run(t.Endpoint, t.Region, "ec2", "delete-vpc", "--vpc-id", vpcID) //nolint:errcheck
	}
	return nil
}

func (g *ec2CliGroup) DescribeRegions(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-regions")
	if err != nil {
		return err
	}
	regions, _ := out["Regions"].([]interface{})
	if len(regions) == 0 {
		return fmt.Errorf("DescribeRegions: no regions returned")
	}
	return nil
}

func (g *ec2CliGroup) DescribeAvailabilityZones(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-availability-zones")
	if err != nil {
		return err
	}
	azs, _ := out["AvailabilityZones"].([]interface{})
	if len(azs) == 0 {
		return fmt.Errorf("DescribeAvailabilityZones: no AZs returned")
	}
	return nil
}

func (g *ec2CliGroup) DescribeInstances(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-instances")
	return err
}

func (g *ec2CliGroup) DescribeInstanceTypes(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-instance-types",
		"--instance-types", "t3.micro",
	)
	return err
}

func (g *ec2CliGroup) CreateVpc(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "create-vpc",
		"--cidr-block", "10.0.0.0/16",
	)
	if err != nil {
		return err
	}
	vpc, _ := out["Vpc"].(map[string]interface{})
	vpcID, _ := vpc["VpcId"].(string)
	if vpcID == "" {
		return fmt.Errorf("CreateVpc: missing VpcId")
	}
	t.Set("vpc_id", vpcID)
	return nil
}

func (g *ec2CliGroup) DescribeVpcs(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-vpcs")
	return err
}

func (g *ec2CliGroup) CreateSubnet(_ context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("vpc_id")
	if vpcID == "" {
		return fmt.Errorf("CreateSubnet: no vpc_id from CreateVpc")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "create-subnet",
		"--vpc-id", fmt.Sprintf("%v", vpcID),
		"--cidr-block", "10.0.1.0/24",
	)
	if err != nil {
		return err
	}
	subnet, _ := out["Subnet"].(map[string]interface{})
	subnetID, _ := subnet["SubnetId"].(string)
	if subnetID == "" {
		return fmt.Errorf("CreateSubnet: missing SubnetId")
	}
	t.Set("subnet_id", subnetID)
	return nil
}

func (g *ec2CliGroup) CreateSecurityGroup(_ context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("vpc_id")
	if vpcID == "" {
		return fmt.Errorf("CreateSecurityGroup: no vpc_id from CreateVpc")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "create-security-group",
		"--group-name", fmt.Sprintf("compat-sg-%s", t.RunID),
		"--description", "compat test",
		"--vpc-id", vpcID,
	)
	if err != nil {
		return err
	}
	sgID, _ := out["GroupId"].(string)
	if sgID == "" {
		return fmt.Errorf("CreateSecurityGroup: missing GroupId")
	}
	t.Set("sg_id", sgID)
	return nil
}

func (g *ec2CliGroup) DeleteSecurityGroup(_ context.Context, t *harness.TestContext) error {
	sgID := t.GetString("sg_id")
	if sgID == "" {
		return fmt.Errorf("DeleteSecurityGroup: no sg_id from CreateSecurityGroup")
	}
	return awscli.Run(t.Endpoint, t.Region, "ec2", "delete-security-group",
		"--group-id", sgID,
	)
}

func (g *ec2CliGroup) DeleteSubnet(_ context.Context, t *harness.TestContext) error {
	subnetID := t.GetString("subnet_id")
	if subnetID == "" {
		return fmt.Errorf("DeleteSubnet: no subnet_id from CreateSubnet")
	}
	return awscli.Run(t.Endpoint, t.Region, "ec2", "delete-subnet",
		"--subnet-id", subnetID,
	)
}

func (g *ec2CliGroup) DeleteVpc(_ context.Context, t *harness.TestContext) error {
	vpcID := t.GetString("vpc_id")
	if vpcID == "" {
		return fmt.Errorf("DeleteVpc: no vpc_id from CreateVpc")
	}
	return awscli.Run(t.Endpoint, t.Region, "ec2", "delete-vpc",
		"--vpc-id", vpcID,
	)
}

// ── ec2-instances (additional) ─────────────────────────────────────────────

func (g *ec2CliGroup) DescribeImages(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-images",
		"--owners", "self", "--max-results", "5")
	if err != nil {
		return err
	}
	if out["Images"] == nil {
		return fmt.Errorf("DescribeImages: missing Images")
	}
	return nil
}

func (g *ec2CliGroup) RunInstances(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "run-instances",
		"--image-id", "ami-00000000",
		"--instance-type", "t2.micro",
		"--count", "1",
	)
	if err != nil {
		return err
	}
	instances, _ := out["Instances"].([]interface{})
	if len(instances) == 0 {
		return fmt.Errorf("RunInstances: no instances returned")
	}
	inst, _ := instances[0].(map[string]interface{})
	instanceID, _ := inst["InstanceId"].(string)
	if instanceID == "" {
		return fmt.Errorf("RunInstances: missing InstanceId")
	}
	t.Set("instance_id", instanceID)
	return nil
}

func (g *ec2CliGroup) StopInstances(_ context.Context, t *harness.TestContext) error {
	instanceID := t.GetString("instance_id")
	if instanceID == "" {
		return fmt.Errorf("StopInstances: no instance_id from RunInstances")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "stop-instances",
		"--instance-ids", instanceID)
	if err != nil {
		return err
	}
	stopping, _ := out["StoppingInstances"].([]interface{})
	if len(stopping) == 0 {
		return fmt.Errorf("StopInstances: no StoppingInstances returned")
	}
	return nil
}

func (g *ec2CliGroup) StartInstances(_ context.Context, t *harness.TestContext) error {
	instanceID := t.GetString("instance_id")
	if instanceID == "" {
		return fmt.Errorf("StartInstances: no instance_id from RunInstances")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "start-instances",
		"--instance-ids", instanceID)
	if err != nil {
		return err
	}
	starting, _ := out["StartingInstances"].([]interface{})
	if len(starting) == 0 {
		return fmt.Errorf("StartInstances: no StartingInstances returned")
	}
	return nil
}

func (g *ec2CliGroup) TerminateInstances(_ context.Context, t *harness.TestContext) error {
	instanceID := t.GetString("instance_id")
	if instanceID == "" {
		return fmt.Errorf("TerminateInstances: no instance_id from RunInstances")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "terminate-instances",
		"--instance-ids", instanceID)
	if err != nil {
		return err
	}
	terminating, _ := out["TerminatingInstances"].([]interface{})
	if len(terminating) == 0 {
		return fmt.Errorf("TerminateInstances: no TerminatingInstances returned")
	}
	return nil
}

// ── ec2-vpc (additional) ───────────────────────────────────────────────────

func (g *ec2CliGroup) DescribeSubnets(_ context.Context, t *harness.TestContext) error {
	subnetID := t.GetString("subnet_id")
	if subnetID == "" {
		return fmt.Errorf("DescribeSubnets: no subnet_id from CreateSubnet")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-subnets",
		"--subnet-ids", subnetID)
	if err != nil {
		return err
	}
	subnets, _ := out["Subnets"].([]interface{})
	if len(subnets) == 0 {
		return fmt.Errorf("DescribeSubnets: no subnets returned")
	}
	return nil
}

func (g *ec2CliGroup) CreateInternetGateway(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "create-internet-gateway")
	if err != nil {
		return err
	}
	igw, _ := out["InternetGateway"].(map[string]interface{})
	igwID, _ := igw["InternetGatewayId"].(string)
	if igwID == "" {
		return fmt.Errorf("CreateInternetGateway: missing InternetGatewayId")
	}
	t.Set("igw_id", igwID)
	return nil
}

func (g *ec2CliGroup) AttachInternetGateway(_ context.Context, t *harness.TestContext) error {
	igwID := t.GetString("igw_id")
	vpcID := t.GetString("vpc_id")
	if igwID == "" || vpcID == "" {
		return fmt.Errorf("AttachInternetGateway: missing igw_id or vpc_id")
	}
	return awscli.Run(t.Endpoint, t.Region, "ec2", "attach-internet-gateway",
		"--internet-gateway-id", igwID,
		"--vpc-id", vpcID,
	)
}

// ── ec2-security-group-rules ───────────────────────────────────────────────

func (g *ec2CliGroup) setupSGRules(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "create-vpc",
		"--cidr-block", "10.100.0.0/16")
	if err != nil {
		return err
	}
	vpc, _ := out["Vpc"].(map[string]interface{})
	vpcID, _ := vpc["VpcId"].(string)
	t.Set("sgrules_vpc_id", vpcID)

	sgOut, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "create-security-group",
		"--group-name", fmt.Sprintf("compat-sgrules-%s", t.RunID),
		"--description", "compat SG rules test",
		"--vpc-id", vpcID,
	)
	if err != nil {
		return err
	}
	sgID, _ := sgOut["GroupId"].(string)
	t.Set("sgrules_sg_id", sgID)
	return nil
}

func (g *ec2CliGroup) teardownSGRules(_ context.Context, t *harness.TestContext) error {
	if sgID := t.GetString("sgrules_sg_id"); sgID != "" {
		awscli.Run(t.Endpoint, t.Region, "ec2", "delete-security-group", "--group-id", sgID) //nolint:errcheck
	}
	if vpcID := t.GetString("sgrules_vpc_id"); vpcID != "" {
		awscli.Run(t.Endpoint, t.Region, "ec2", "delete-vpc", "--vpc-id", vpcID) //nolint:errcheck
	}
	return nil
}

func (g *ec2CliGroup) AuthorizeSecurityGroupIngress(_ context.Context, t *harness.TestContext) error {
	sgID := t.GetString("sgrules_sg_id")
	if sgID == "" {
		return fmt.Errorf("AuthorizeSecurityGroupIngress: no sgrules_sg_id from setup")
	}
	return awscli.Run(t.Endpoint, t.Region, "ec2", "authorize-security-group-ingress",
		"--group-id", sgID,
		"--protocol", "tcp",
		"--port", "443",
		"--cidr", "10.0.0.0/8",
	)
}

func (g *ec2CliGroup) DescribeSecurityGroups(_ context.Context, t *harness.TestContext) error {
	sgID := t.GetString("sgrules_sg_id")
	if sgID == "" {
		return fmt.Errorf("DescribeSecurityGroups: no sgrules_sg_id from setup")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-security-groups",
		"--group-ids", sgID)
	if err != nil {
		return err
	}
	sgs, _ := out["SecurityGroups"].([]interface{})
	if len(sgs) == 0 {
		return fmt.Errorf("DescribeSecurityGroups: no security groups returned")
	}
	return nil
}

func (g *ec2CliGroup) RevokeSecurityGroupIngress(_ context.Context, t *harness.TestContext) error {
	sgID := t.GetString("sgrules_sg_id")
	if sgID == "" {
		return fmt.Errorf("RevokeSecurityGroupIngress: no sgrules_sg_id from setup")
	}
	return awscli.Run(t.Endpoint, t.Region, "ec2", "revoke-security-group-ingress",
		"--group-id", sgID,
		"--protocol", "tcp",
		"--port", "443",
		"--cidr", "10.0.0.0/8",
	)
}

// ── ec2-keypairs ───────────────────────────────────────────────────────────

func (g *ec2CliGroup) teardownKeyPairs(_ context.Context, t *harness.TestContext) error {
	if keyName := t.GetString("key_name"); keyName != "" {
		awscli.Run(t.Endpoint, t.Region, "ec2", "delete-key-pair", "--key-name", keyName) //nolint:errcheck
	}
	return nil
}

func (g *ec2CliGroup) CreateKeyPair(_ context.Context, t *harness.TestContext) error {
	keyName := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "create-key-pair",
		"--key-name", keyName)
	if err != nil {
		return err
	}
	kpID, _ := out["KeyPairId"].(string)
	if kpID == "" {
		return fmt.Errorf("CreateKeyPair: missing KeyPairId")
	}
	t.Set("key_name", keyName)
	return nil
}

func (g *ec2CliGroup) DescribeKeyPairs(_ context.Context, t *harness.TestContext) error {
	keyName := t.GetString("key_name")
	if keyName == "" {
		return fmt.Errorf("DescribeKeyPairs: no key_name from CreateKeyPair")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ec2", "describe-key-pairs",
		"--key-names", keyName)
	if err != nil {
		return err
	}
	kps, _ := out["KeyPairs"].([]interface{})
	if len(kps) == 0 {
		return fmt.Errorf("DescribeKeyPairs: no key pairs returned")
	}
	return nil
}

func (g *ec2CliGroup) DeleteKeyPair(_ context.Context, t *harness.TestContext) error {
	keyName := t.GetString("key_name")
	if keyName == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "ec2", "delete-key-pair",
		"--key-name", keyName)
}
