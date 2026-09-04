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


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "cloudformation-stacks:CreateStack": CreateStack,
    "cloudformation-stacks:DescribeStacks": DescribeStacks,
    "cloudformation-stacks:ListStacks": ListStacks,
    "cloudformation-stacks:UpdateStack": UpdateStack,
    "cloudformation-stacks:DeleteStack": DeleteStack,
    "cloudformation-stacks:ValidateTemplate": ValidateTemplate,
}

SETUP = {}
TEARDOWN = {
    "cloudformation-stacks": lambda ctx: _teardown_cfn_stack(ctx),
}


def _teardown_cfn_stack(ctx: TestContext) -> None:
    stack_name = ctx.get("cfn_stack_name")
    if stack_name:
        try:
            _cfn(ctx).delete_stack(StackName=stack_name)
        except Exception:
            pass
