"""
groups/cloudformation.py — CloudFormation compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import json
from lib.harness import TestContext
from lib.clients import make_clients


def _cfn(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("cloudformation")


# ── cloudformation-stacks ─────────────────────────────────────────────────────

def CreateStack(ctx: TestContext) -> None:
    cfn = _cfn(ctx)
    stack_name = f"compat-{ctx.run_id}"
    tpl = json.dumps({
        "AWSTemplateFormatVersion": "2010-09-09",
        "Resources": {
            "DummyBucket": {"Type": "AWS::S3::Bucket"},
        },
    })
    resp = cfn.create_stack(StackName=stack_name, TemplateBody=tpl)
    if not resp.get("StackId"):
        raise AssertionError("CreateStack: missing StackId")
    ctx["cfn_stack_name"] = stack_name


def DescribeStacks(ctx: TestContext) -> None:
    cfn = _cfn(ctx)
    stack_name = ctx.get("cfn_stack_name")
    if not stack_name:
        raise AssertionError("DescribeStacks: no stack from CreateStack")
    resp = cfn.describe_stacks(StackName=stack_name)
    if not resp.get("Stacks"):
        raise AssertionError("DescribeStacks: no stacks returned")


def _list_stack_names(ctx: TestContext, *statuses: str) -> list[str]:
    """Stack names from list_stacks, optionally filtered by status.

    Also checks the invariant a status filter has to hold: every summary
    returned is in one of the requested statuses.
    """
    kwargs = {"StackStatusFilter": list(statuses)} if statuses else {}
    resp = _cfn(ctx).list_stacks(**kwargs)
    names = []
    for summary in resp.get("StackSummaries", []):
        status = summary.get("StackStatus")
        if statuses and status not in statuses:
            raise AssertionError(
                f"ListStacks: {summary.get('StackName')} returned with status "
                f"{status}, not in filter {list(statuses)}"
            )
        names.append(summary.get("StackName"))
    return names


def ListStacks(ctx: TestContext) -> None:
    """Cover StackStatusFilter in both directions.

    A filter naming the stack's status must include it, and one naming a status
    it cannot hold must exclude it. Only the second catches an implementation
    that accepts the parameter and ignores it.

    The statuses are the ones a stack created in this group can legitimately be
    in — the suite does not wait for CREATE_COMPLETE — and DELETE_FAILED is one
    it cannot reach, since nothing has tried to delete it yet.
    """
    stack_name = ctx.get("cfn_stack_name")
    if not stack_name:
        raise AssertionError("ListStacks: no stack from CreateStack")

    all_names = _list_stack_names(ctx)
    if stack_name not in all_names:
        raise AssertionError(
            f"ListStacks: {stack_name} not in unfiltered listing {all_names}"
        )

    active = _list_stack_names(ctx, "CREATE_COMPLETE", "CREATE_IN_PROGRESS")
    if stack_name not in active:
        raise AssertionError(
            f"ListStacks: {stack_name} not in CREATE_COMPLETE/CREATE_IN_PROGRESS "
            f"listing {active}"
        )

    delete_failed = _list_stack_names(ctx, "DELETE_FAILED")
    if stack_name in delete_failed:
        raise AssertionError(
            f"ListStacks: {stack_name} returned by a DELETE_FAILED filter"
        )


def DeleteStack(ctx: TestContext) -> None:
    """Deletes its own stack rather than the group's shared one.

    UpdateStack runs against the shared stack and, as on AWS, a stack that has
    finished deleting cannot be updated — deleting the shared stack here would
    make the group's outcome depend on test order. Mirrors the cli suite.
    """
    cfn = _cfn(ctx)
    stack_name = f"compat-del-{ctx.run_id}"
    tpl = json.dumps({
        "AWSTemplateFormatVersion": "2010-09-09",
        "Resources": {
            "DummyBucket": {"Type": "AWS::S3::Bucket"},
        },
    })
    try:
        # Create is scaffolding, not the operation under test; DeleteStack
        # succeeds on AWS even for a stack that failed to create.
        cfn.create_stack(StackName=stack_name, TemplateBody=tpl)
    except Exception:
        pass
    cfn.delete_stack(StackName=stack_name)


def UpdateStack(ctx: TestContext) -> None:
    """Update the stack, once it has finished being created.

    create_stack is asynchronous — it returns a StackId with the stack still
    CREATE_IN_PROGRESS — and CloudFormation refuses an update to a stack that is
    mid-operation, so updating straight after creating is a race the suite would
    lose against real AWS as readily as against Overcast. The java-sdk suite
    waits the same way.
    """
    cfn = _cfn(ctx)
    stack_name = ctx.get("cfn_stack_name")
    if not stack_name:
        raise AssertionError("UpdateStack: no stack from CreateStack")
    cfn.get_waiter("stack_create_complete").wait(
        StackName=stack_name,
        WaiterConfig={"Delay": 1, "MaxAttempts": 60},
    )
    tpl = json.dumps({
        "AWSTemplateFormatVersion": "2010-09-09",
        "Resources": {
            "DummyBucket": {"Type": "AWS::S3::Bucket"},
        },
    })
    cfn.update_stack(StackName=stack_name, TemplateBody=tpl, UsePreviousTemplate=True)


def ValidateTemplate(ctx: TestContext) -> None:
    cfn = _cfn(ctx)
    tpl = json.dumps({
        "AWSTemplateFormatVersion": "2010-09-09",
        "Resources": {
            "Bucket": {"Type": "AWS::S3::Bucket"},
        },
    })
    cfn.validate_template(TemplateBody=tpl)


# ── cloudformation-eks-refs ───────────────────────────────────────────────────

# CloudFormation documents Ref and Fn::GetAtt separately for every resource
# type, and the EKS family disagrees with itself in ways only a deployed stack
# reveals: Ref on a Cluster is its name and the ARN is a distinct Arn attribute,
# Ref on a Nodegroup is "<cluster>/<nodegroup>", and Ref on an Addon is
# "<cluster>|<addon>" — a pipe, not a slash.
#
#   https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-cluster.html#aws-resource-eks-cluster-return-values
#   https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-nodegroup.html#aws-resource-eks-nodegroup-return-values
#   https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-eks-addon.html#aws-resource-eks-addon-return-values
#
# Overcast returned the cluster ARN from Ref (#1690) and separated the add-on
# with "/" (#1692). Both were wire-observable and neither was caught here, so
# this group asserts the values through CloudFormation alone — stack outputs and
# describe_stack_resources — which is what a template author actually sees.

# vpc-cni is one of the add-ons DescribeAddonVersions publishes. AWS refuses an
# AddonName it does not know, so the name is not arbitrary.
EKS_REFS_ADDON_NAME = "vpc-cni"
EKS_REFS_CLUSTER_ROLE_ARN = "arn:aws:iam::000000000000:role/compat-eks-refs-cluster"
EKS_REFS_NODE_ROLE_ARN = "arn:aws:iam::000000000000:role/compat-eks-refs-node"
EKS_REFS_SUBNET_ID = "subnet-1"

# Long enough for the stack to settle, short enough to fail rather than hang.
_EKS_REFS_WAITER_CONFIG = {"Delay": 1, "MaxAttempts": 120}


def _eks_refs_template(cluster_name: str, nodegroup_name: str) -> str:
    """The stack under test.

    The Nodegroup and the Addon are wired to their cluster purely by
    {"Ref": "Cluster"} — no literal cluster name and no DependsOn. That is the
    point: CloudFormation must hand CreateNodegroup and CreateAddon the cluster
    *name*, so a Ref that resolves to the ARN fails the stack outright.
    """
    return json.dumps({
        "AWSTemplateFormatVersion": "2010-09-09",
        "Resources": {
            "Cluster": {
                "Type": "AWS::EKS::Cluster",
                "Properties": {
                    "Name": cluster_name,
                    "RoleArn": EKS_REFS_CLUSTER_ROLE_ARN,
                    "ResourcesVpcConfig": {"SubnetIds": [EKS_REFS_SUBNET_ID]},
                },
            },
            "Nodegroup": {
                "Type": "AWS::EKS::Nodegroup",
                "Properties": {
                    "ClusterName": {"Ref": "Cluster"},
                    "NodegroupName": nodegroup_name,
                    "NodeRole": EKS_REFS_NODE_ROLE_ARN,
                    "Subnets": [EKS_REFS_SUBNET_ID],
                },
            },
            "Addon": {
                "Type": "AWS::EKS::Addon",
                "Properties": {
                    "ClusterName": {"Ref": "Cluster"},
                    "AddonName": EKS_REFS_ADDON_NAME,
                },
            },
        },
        "Outputs": {
            "ClusterRef": {"Value": {"Ref": "Cluster"}},
            "ClusterArn": {"Value": {"Fn::GetAtt": ["Cluster", "Arn"]}},
            "NodegroupRef": {"Value": {"Ref": "Nodegroup"}},
            "AddonRef": {"Value": {"Ref": "Addon"}},
        },
    })


def _eks_refs_expected_ids(cluster_name: str, nodegroup_name: str) -> dict[str, str]:
    """The AWS-documented Ref value — and so the physical resource ID — of each
    resource in the template."""
    return {
        "Cluster": cluster_name,
        "Nodegroup": f"{cluster_name}/{nodegroup_name}",
        "Addon": f"{cluster_name}|{EKS_REFS_ADDON_NAME}",
    }


def _account_from_stack_id(stack_id: str) -> str:
    """The account ID out of a stack ARN.

    ("arn:aws:cloudformation:<region>:<account>:stack/<name>/<uuid>"), so the
    expected EKS ARN can be built without reaching for a second AWS client.
    """
    parts = stack_id.split(":")
    if len(parts) < 6 or not parts[4]:
        raise AssertionError(
            f"stack ID {stack_id!r} is not a stack ARN, cannot read the account ID from it"
        )
    return parts[4]


def _describe_eks_refs_stack(ctx: TestContext, name_or_id: str) -> dict:
    stacks = _cfn(ctx).describe_stacks(StackName=name_or_id).get("Stacks", [])
    if not stacks:
        raise AssertionError(f"DescribeStacks: {name_or_id} returned no stacks")
    return stacks[0]


def _eks_refs_outputs(ctx: TestContext) -> dict[str, str]:
    """Stack outputs, read afresh for each test rather than stashed, so every
    assertion is made against a value the server just produced rather than one
    an earlier test cached."""
    stack_name = ctx.get("cfn_eks_stack_name")
    if not stack_name:
        raise AssertionError("DescribeStacks: no stack from CreateStackWithEksRefs")
    stack = _describe_eks_refs_stack(ctx, stack_name)
    return {o["OutputKey"]: o["OutputValue"] for o in stack.get("Outputs", [])}


def setup_cloudformation_eks_refs(ctx: TestContext) -> None:
    ctx["cfn_eks_stack_name"] = f"compat-eks-refs-{ctx.run_id}"
    ctx["cfn_eks_cluster_name"] = f"compat-eks-refs-cluster-{ctx.run_id}"
    ctx["cfn_eks_nodegroup_name"] = f"compat-eks-refs-ng-{ctx.run_id}"


def teardown_cloudformation_eks_refs(ctx: TestContext) -> None:
    """Delete the stack, which cascades to the cluster, the nodegroup and the
    add-on: CloudFormation owns every resource this group creates, and deleting
    a stack deletes the resources it owns."""
    stack_name = ctx.get("cfn_eks_stack_name")
    if stack_name:
        try:
            _cfn(ctx).delete_stack(StackName=stack_name)
        except Exception:
            pass


def CreateStackWithEksRefs(ctx: TestContext) -> None:
    cfn = _cfn(ctx)
    stack_name = ctx.get("cfn_eks_stack_name")
    resp = cfn.create_stack(
        StackName=stack_name,
        TemplateBody=_eks_refs_template(
            ctx.get("cfn_eks_cluster_name"), ctx.get("cfn_eks_nodegroup_name")
        ),
    )
    stack_id = resp.get("StackId")
    if not stack_id:
        raise AssertionError("CreateStack: missing StackId")
    ctx["cfn_eks_stack_id"] = stack_id

    # Outputs and physical resource IDs only exist once the stack settles.
    cfn.get_waiter("stack_create_complete").wait(
        StackName=stack_name, WaiterConfig=_EKS_REFS_WAITER_CONFIG
    )
    stack = _describe_eks_refs_stack(ctx, stack_name)
    if stack.get("StackStatus") != "CREATE_COMPLETE":
        raise AssertionError(
            f"CreateStack: {stack_name} is {stack.get('StackStatus')} "
            f"({stack.get('StackStatusReason', '')}), want CREATE_COMPLETE"
        )


def DescribeStackResourcesPhysicalIds(ctx: TestContext) -> None:
    """Assert the physical ID CloudFormation recorded for each resource.

    The physical ID is what Ref resolves to, so this is the same contract the
    output assertions below check, seen through the API an operator reaches for
    when a stack misbehaves.
    """
    stack_name = ctx.get("cfn_eks_stack_name")
    if not stack_name:
        raise AssertionError(
            "DescribeStackResources: no stack from CreateStackWithEksRefs"
        )
    resp = _cfn(ctx).describe_stack_resources(StackName=stack_name)
    physical_ids = {
        r.get("LogicalResourceId"): r.get("PhysicalResourceId")
        for r in resp.get("StackResources", [])
    }
    want = _eks_refs_expected_ids(
        ctx.get("cfn_eks_cluster_name"), ctx.get("cfn_eks_nodegroup_name")
    )
    for logical_id in ("Cluster", "Nodegroup", "Addon"):
        got = physical_ids.get(logical_id)
        if got != want[logical_id]:
            raise AssertionError(
                f"DescribeStackResources: {logical_id} PhysicalResourceId = "
                f"{got!r}, want {want[logical_id]!r}"
            )


def GetAttClusterArn(ctx: TestContext) -> None:
    """Cover the divergence AWS documents for AWS::EKS::Cluster.

    Ref is the cluster name, and the ARN is reachable only as the Arn attribute.
    Asserting both together is what makes the pair meaningful — either value
    alone looks plausible on its own.
    """
    outputs = _eks_refs_outputs(ctx)
    cluster = ctx.get("cfn_eks_cluster_name")
    if outputs.get("ClusterRef") != cluster:
        raise AssertionError(
            f"Ref on AWS::EKS::Cluster = {outputs.get('ClusterRef')!r}, "
            f"want the cluster name {cluster!r}"
        )
    account = _account_from_stack_id(ctx.get("cfn_eks_stack_id"))
    want = f"arn:aws:eks:{ctx.region}:{account}:cluster/{cluster}"
    if outputs.get("ClusterArn") != want:
        raise AssertionError(
            f"Fn::GetAtt [Cluster, Arn] = {outputs.get('ClusterArn')!r}, want {want!r}"
        )


def RefNodegroupIsClusterSlashName(ctx: TestContext) -> None:
    outputs = _eks_refs_outputs(ctx)
    want = _eks_refs_expected_ids(
        ctx.get("cfn_eks_cluster_name"), ctx.get("cfn_eks_nodegroup_name")
    )["Nodegroup"]
    if outputs.get("NodegroupRef") != want:
        raise AssertionError(
            f"Ref on AWS::EKS::Nodegroup = {outputs.get('NodegroupRef')!r}, want {want!r}"
        )


def RefAddonIsClusterPipeAddon(ctx: TestContext) -> None:
    outputs = _eks_refs_outputs(ctx)
    want = _eks_refs_expected_ids(
        ctx.get("cfn_eks_cluster_name"), ctx.get("cfn_eks_nodegroup_name")
    )["Addon"]
    if outputs.get("AddonRef") != want:
        raise AssertionError(
            f"Ref on AWS::EKS::Addon = {outputs.get('AddonRef')!r}, want {want!r} "
            "(pipe-separated, not a slash)"
        )


def DeleteStackWithEksRefs(ctx: TestContext) -> None:
    """Delete the group's own stack.

    A deleted stack is readable by stack ID and gone by name — AWS documents
    that asymmetry — so the stack ID is what proves the delete finished rather
    than merely started.
    """
    cfn = _cfn(ctx)
    stack_name = ctx.get("cfn_eks_stack_name")
    stack_id = ctx.get("cfn_eks_stack_id")
    if not stack_name or not stack_id:
        raise AssertionError("DeleteStack: no stack from CreateStackWithEksRefs")
    cfn.delete_stack(StackName=stack_name)
    cfn.get_waiter("stack_delete_complete").wait(
        StackName=stack_name, WaiterConfig=_EKS_REFS_WAITER_CONFIG
    )
    stack = _describe_eks_refs_stack(ctx, stack_id)
    if stack.get("StackStatus") != "DELETE_COMPLETE":
        raise AssertionError(
            f"DeleteStack: {stack_name} is {stack.get('StackStatus')} "
            f"({stack.get('StackStatusReason', '')}), want DELETE_COMPLETE"
        )


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "cloudformation-stacks:CreateStack": CreateStack,
    "cloudformation-stacks:DescribeStacks": DescribeStacks,
    "cloudformation-stacks:ListStacks": ListStacks,
    "cloudformation-stacks:UpdateStack": UpdateStack,
    "cloudformation-stacks:DeleteStack": DeleteStack,
    "cloudformation-stacks:ValidateTemplate": ValidateTemplate,
    "cloudformation-eks-refs:CreateStackWithEksRefs": CreateStackWithEksRefs,
    "cloudformation-eks-refs:DescribeStackResourcesPhysicalIds": DescribeStackResourcesPhysicalIds,
    "cloudformation-eks-refs:GetAttClusterArn": GetAttClusterArn,
    "cloudformation-eks-refs:RefNodegroupIsClusterSlashName": RefNodegroupIsClusterSlashName,
    "cloudformation-eks-refs:RefAddonIsClusterPipeAddon": RefAddonIsClusterPipeAddon,
    "cloudformation-eks-refs:DeleteStackWithEksRefs": DeleteStackWithEksRefs,
}

SETUP = {
    "cloudformation-eks-refs": setup_cloudformation_eks_refs,
}
TEARDOWN = {
    "cloudformation-stacks": lambda ctx: _teardown_cfn_stack(ctx),
    "cloudformation-eks-refs": teardown_cloudformation_eks_refs,
}


def _teardown_cfn_stack(ctx: TestContext) -> None:
    stack_name = ctx.get("cfn_stack_name")
    if stack_name:
        try:
            _cfn(ctx).delete_stack(StackName=stack_name)
        except Exception:
            pass
