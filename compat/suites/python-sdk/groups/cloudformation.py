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


def ListStacks(ctx: TestContext) -> None:
    cfn = _cfn(ctx)
    cfn.list_stacks(StackStatusFilter=["CREATE_COMPLETE"])


def DeleteStack(ctx: TestContext) -> None:
    cfn = _cfn(ctx)
    stack_name = ctx.get("cfn_stack_name")
    if not stack_name:
        raise AssertionError("DeleteStack: no stack from CreateStack")
    cfn.delete_stack(StackName=stack_name)


def UpdateStack(ctx: TestContext) -> None:
    cfn = _cfn(ctx)
    stack_name = ctx.get("cfn_stack_name")
    if not stack_name:
        raise AssertionError("UpdateStack: no stack from CreateStack")
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
    "CreateStack": CreateStack,
    "DescribeStacks": DescribeStacks,
    "ListStacks": ListStacks,
    "UpdateStack": UpdateStack,
    "DeleteStack": DeleteStack,
    "ValidateTemplate": ValidateTemplate,
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
