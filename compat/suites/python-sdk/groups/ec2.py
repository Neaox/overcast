"""
groups/ec2.py — EC2 and VPC compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _ec2(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("ec2")


# ── ec2-instances ─────────────────────────────────────────────────────────────

def DescribeRegions(ctx: TestContext) -> None:
    _ec2(ctx).describe_regions()


def DescribeAvailabilityZones(ctx: TestContext) -> None:
    _ec2(ctx).describe_availability_zones()


def DescribeInstances(ctx: TestContext) -> None:
    _ec2(ctx).describe_instances()


def DescribeInstanceTypes(ctx: TestContext) -> None:
    _ec2(ctx).describe_instance_types()


# ── ec2-vpc ───────────────────────────────────────────────────────────────────

def CreateVpc(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    resp = ec2.create_vpc(CidrBlock="10.0.0.0/16")
    vpc = resp.get("Vpc", {})
    if not vpc.get("VpcId"):
        raise AssertionError("CreateVpc: missing VpcId")
    ctx["ec2_vpc_id"] = vpc["VpcId"]


def DescribeVpcs(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    resp = ec2.describe_vpcs()
    if resp.get("Vpcs") is None:
        raise AssertionError("DescribeVpcs: missing Vpcs")


def CreateSubnet(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    vpc_id = ctx.get("ec2_vpc_id")
    if not vpc_id:
        raise AssertionError("CreateSubnet: no VPC from CreateVpc")
    resp = ec2.create_subnet(VpcId=vpc_id, CidrBlock="10.0.1.0/24")
    subnet = resp.get("Subnet", {})
    if not subnet.get("SubnetId"):
        raise AssertionError("CreateSubnet: missing SubnetId")
    ctx["ec2_subnet_id"] = subnet["SubnetId"]


def CreateSecurityGroup(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    vpc_id = ctx.get("ec2_vpc_id")
    if not vpc_id:
        raise AssertionError("CreateSecurityGroup: no VPC from CreateVpc")
    resp = ec2.create_security_group(
        GroupName=f"compat-{ctx.run_id}",
        Description="compat test group",
        VpcId=vpc_id,
    )
    if not resp.get("GroupId"):
        raise AssertionError("CreateSecurityGroup: missing GroupId")
    ctx["ec2_sg_id"] = resp["GroupId"]


def DeleteSecurityGroup(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    sg_id = ctx.get("ec2_sg_id")
    if not sg_id:
        return
    ec2.delete_security_group(GroupId=sg_id)


def DeleteSubnet(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    subnet_id = ctx.get("ec2_subnet_id")
    if not subnet_id:
        return
    ec2.delete_subnet(SubnetId=subnet_id)


def DeleteVpc(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    vpc_id = ctx.get("ec2_vpc_id")
    if not vpc_id:
        return
    ec2.delete_vpc(VpcId=vpc_id)


# ── ec2-instances (additional) ────────────────────────────────────────────────

def DescribeImages(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    resp = ec2.describe_images()
    if resp.get("Images") is None:
        raise AssertionError("DescribeImages: missing Images")


def RunInstances(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    resp = ec2.run_instances(
        ImageId="ami-00000000",
        InstanceType="t2.micro",
        MinCount=1,
        MaxCount=1,
    )
    instances = resp.get("Instances", [])
    if not instances:
        raise AssertionError("RunInstances: no instances returned")
    instance_id = instances[0].get("InstanceId")
    if not instance_id:
        raise AssertionError("RunInstances: missing InstanceId")
    ctx["ec2_instance_id"] = instance_id


def StopInstances(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    instance_id = ctx.get("ec2_instance_id")
    if not instance_id:
        raise AssertionError("StopInstances: no instance from RunInstances")
    resp = ec2.stop_instances(InstanceIds=[instance_id])
    if not resp.get("StoppingInstances"):
        raise AssertionError("StopInstances: no StoppingInstances returned")


def StartInstances(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    instance_id = ctx.get("ec2_instance_id")
    if not instance_id:
        raise AssertionError("StartInstances: no instance from RunInstances")
    resp = ec2.start_instances(InstanceIds=[instance_id])
    if not resp.get("StartingInstances"):
        raise AssertionError("StartInstances: no StartingInstances returned")


def TerminateInstances(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    instance_id = ctx.get("ec2_instance_id")
    if not instance_id:
        raise AssertionError("TerminateInstances: no instance from RunInstances")
    resp = ec2.terminate_instances(InstanceIds=[instance_id])
    if not resp.get("TerminatingInstances"):
        raise AssertionError("TerminateInstances: no TerminatingInstances returned")


# ── ec2-vpc (additional) ─────────────────────────────────────────────────────

def DescribeSubnets(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    subnet_id = ctx.get("ec2_subnet_id")
    if not subnet_id:
        raise AssertionError("DescribeSubnets: no subnet from CreateSubnet")
    resp = ec2.describe_subnets(SubnetIds=[subnet_id])
    if not resp.get("Subnets"):
        raise AssertionError("DescribeSubnets: no subnets returned")


def CreateInternetGateway(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    resp = ec2.create_internet_gateway()
    igw = resp.get("InternetGateway", {})
    igw_id = igw.get("InternetGatewayId")
    if not igw_id:
        raise AssertionError("CreateInternetGateway: missing InternetGatewayId")
    ctx["ec2_igw_id"] = igw_id


def AttachInternetGateway(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    igw_id = ctx.get("ec2_igw_id")
    vpc_id = ctx.get("ec2_vpc_id")
    if not igw_id or not vpc_id:
        raise AssertionError("AttachInternetGateway: missing igw or vpc")
    ec2.attach_internet_gateway(InternetGatewayId=igw_id, VpcId=vpc_id)


# ── ec2-security-group-rules ────────────────────────────────────────────────

def AuthorizeSecurityGroupIngress(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    sg_id = ctx.get("ec2_sgrules_sg_id")
    if not sg_id:
        raise AssertionError("AuthorizeSecurityGroupIngress: no SG from setup")
    ec2.authorize_security_group_ingress(
        GroupId=sg_id,
        IpPermissions=[{
            "IpProtocol": "tcp",
            "FromPort": 443,
            "ToPort": 443,
            "IpRanges": [{"CidrIp": "10.0.0.0/8"}],
        }],
    )


def DescribeSecurityGroups(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    sg_id = ctx.get("ec2_sgrules_sg_id")
    if not sg_id:
        raise AssertionError("DescribeSecurityGroups: no SG from setup")
    resp = ec2.describe_security_groups(GroupIds=[sg_id])
    if not resp.get("SecurityGroups"):
        raise AssertionError("DescribeSecurityGroups: no security groups returned")


def RevokeSecurityGroupIngress(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    sg_id = ctx.get("ec2_sgrules_sg_id")
    if not sg_id:
        raise AssertionError("RevokeSecurityGroupIngress: no SG from setup")
    ec2.revoke_security_group_ingress(
        GroupId=sg_id,
        IpPermissions=[{
            "IpProtocol": "tcp",
            "FromPort": 443,
            "ToPort": 443,
            "IpRanges": [{"CidrIp": "10.0.0.0/8"}],
        }],
    )


# ── ec2-keypairs ─────────────────────────────────────────────────────────────

def CreateKeyPair(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    key_name = f"compat-{ctx.run_id}"
    resp = ec2.create_key_pair(KeyName=key_name)
    if not resp.get("KeyPairId"):
        raise AssertionError("CreateKeyPair: missing KeyPairId")
    ctx["ec2_key_name"] = key_name


def DescribeKeyPairs(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    key_name = ctx.get("ec2_key_name")
    if not key_name:
        raise AssertionError("DescribeKeyPairs: no key from CreateKeyPair")
    resp = ec2.describe_key_pairs(KeyNames=[key_name])
    if not resp.get("KeyPairs"):
        raise AssertionError("DescribeKeyPairs: no key pairs returned")


def DeleteKeyPair(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    key_name = ctx.get("ec2_key_name")
    if not key_name:
        return
    ec2.delete_key_pair(KeyName=key_name)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "DescribeRegions": DescribeRegions,
    "DescribeAvailabilityZones": DescribeAvailabilityZones,
    "DescribeInstances": DescribeInstances,
    "DescribeInstanceTypes": DescribeInstanceTypes,
    "DescribeImages": DescribeImages,
    "RunInstances": RunInstances,
    "StopInstances": StopInstances,
    "StartInstances": StartInstances,
    "TerminateInstances": TerminateInstances,
    "CreateVpc": CreateVpc,
    "DescribeVpcs": DescribeVpcs,
    "CreateSubnet": CreateSubnet,
    "DescribeSubnets": DescribeSubnets,
    "CreateSecurityGroup": CreateSecurityGroup,
    "DeleteSecurityGroup": DeleteSecurityGroup,
    "CreateInternetGateway": CreateInternetGateway,
    "AttachInternetGateway": AttachInternetGateway,
    "DeleteSubnet": DeleteSubnet,
    "DeleteVpc": DeleteVpc,
    "AuthorizeSecurityGroupIngress": AuthorizeSecurityGroupIngress,
    "DescribeSecurityGroups": DescribeSecurityGroups,
    "RevokeSecurityGroupIngress": RevokeSecurityGroupIngress,
    "CreateKeyPair": CreateKeyPair,
    "DescribeKeyPairs": DescribeKeyPairs,
    "DeleteKeyPair": DeleteKeyPair,
}

SETUP = {
    "ec2-security-group-rules": lambda ctx: _setup_sg_rules(ctx),
}
TEARDOWN = {
    "ec2-instances": lambda ctx: _teardown_instances(ctx),
    "ec2-vpc": lambda ctx: _teardown_vpc(ctx),
    "ec2-security-group-rules": lambda ctx: _teardown_sg_rules(ctx),
    "ec2-keypairs": lambda ctx: _teardown_keypairs(ctx),
}


def _teardown_vpc(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    sg_id = ctx.get("ec2_sg_id")
    if sg_id:
        try:
            ec2.delete_security_group(GroupId=sg_id)
        except Exception:
            pass
    igw_id = ctx.get("ec2_igw_id")
    vpc_id = ctx.get("ec2_vpc_id")
    if igw_id and vpc_id:
        try:
            ec2.detach_internet_gateway(InternetGatewayId=igw_id, VpcId=vpc_id)
        except Exception:
            pass
        try:
            ec2.delete_internet_gateway(InternetGatewayId=igw_id)
        except Exception:
            pass
    subnet_id = ctx.get("ec2_subnet_id")
    if subnet_id:
        try:
            ec2.delete_subnet(SubnetId=subnet_id)
        except Exception:
            pass
    if vpc_id:
        try:
            ec2.delete_vpc(VpcId=vpc_id)
        except Exception:
            pass


def _teardown_instances(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    instance_id = ctx.get("ec2_instance_id")
    if instance_id:
        try:
            ec2.terminate_instances(InstanceIds=[instance_id])
        except Exception:
            pass


def _setup_sg_rules(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    vpc_resp = ec2.create_vpc(CidrBlock="10.100.0.0/16")
    vpc_id = vpc_resp["Vpc"]["VpcId"]
    ctx["ec2_sgrules_vpc_id"] = vpc_id
    sg_resp = ec2.create_security_group(
        GroupName=f"compat-sgrules-{ctx.run_id}",
        Description="compat SG rules test",
        VpcId=vpc_id,
    )
    ctx["ec2_sgrules_sg_id"] = sg_resp["GroupId"]


def _teardown_sg_rules(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    sg_id = ctx.get("ec2_sgrules_sg_id")
    if sg_id:
        try:
            ec2.delete_security_group(GroupId=sg_id)
        except Exception:
            pass
    vpc_id = ctx.get("ec2_sgrules_vpc_id")
    if vpc_id:
        try:
            ec2.delete_vpc(VpcId=vpc_id)
        except Exception:
            pass


def _teardown_keypairs(ctx: TestContext) -> None:
    ec2 = _ec2(ctx)
    key_name = ctx.get("ec2_key_name")
    if key_name:
        try:
            ec2.delete_key_pair(KeyName=key_name)
        except Exception:
            pass
