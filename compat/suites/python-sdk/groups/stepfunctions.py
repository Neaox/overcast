"""
groups/stepfunctions.py — Step Functions compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import json
from lib.harness import TestContext
from lib.clients import make_clients


def _sfn(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("stepfunctions")


# ── sfn-statemachines ─────────────────────────────────────────────────────────

def CreateStateMachine(ctx: TestContext) -> None:
    sfn = _sfn(ctx)
    name = f"compat-{ctx.run_id}"
    definition = json.dumps({
        "Comment": "compat test",
        "StartAt": "Pass",
        "States": {
            "Pass": {"Type": "Pass", "End": True},
        },
    })
    role_arn = f"arn:aws:iam::000000000000:role/compat-sfn-{ctx.run_id}"
    resp = sfn.create_state_machine(
        name=name,
        definition=definition,
        roleArn=role_arn,
        type="EXPRESS",
    )
    if not resp.get("stateMachineArn"):
        raise AssertionError("CreateStateMachine: missing stateMachineArn")
    ctx["sfn_sm_arn"] = resp["stateMachineArn"]


def DescribeStateMachine(ctx: TestContext) -> None:
    sfn = _sfn(ctx)
    sm_arn = ctx.get("sfn_sm_arn")
    if not sm_arn:
        raise AssertionError("DescribeStateMachine: no state machine from CreateStateMachine")
    resp = sfn.describe_state_machine(stateMachineArn=sm_arn)
    if not resp.get("stateMachineArn"):
        raise AssertionError("DescribeStateMachine: missing stateMachineArn")


def ListStateMachines(ctx: TestContext) -> None:
    _sfn(ctx).list_state_machines()


def StartExecution(ctx: TestContext) -> None:
    sfn = _sfn(ctx)
    sm_arn = ctx.get("sfn_sm_arn")
    if not sm_arn:
        raise AssertionError("StartExecution: no state machine from CreateStateMachine")
    resp = sfn.start_execution(
        stateMachineArn=sm_arn,
        input=json.dumps({"key": "value"}),
    )
    if not resp.get("executionArn"):
        raise AssertionError("StartExecution: missing executionArn")


def DeleteStateMachine(ctx: TestContext) -> None:
    sfn = _sfn(ctx)
    sm_arn = ctx.get("sfn_sm_arn")
    if not sm_arn:
        return
    sfn.delete_state_machine(stateMachineArn=sm_arn)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateStateMachine": CreateStateMachine,
    "DescribeStateMachine": DescribeStateMachine,
    "ListStateMachines": ListStateMachines,
    "StartExecution": StartExecution,
    "DeleteStateMachine": DeleteStateMachine,
}

SETUP = {}
TEARDOWN = {
    "sfn-statemachines": lambda ctx: _teardown_state_machine(ctx),
}


def _teardown_state_machine(ctx: TestContext) -> None:
    sm_arn = ctx.get("sfn_sm_arn")
    if sm_arn:
        try:
            _sfn(ctx).delete_state_machine(stateMachineArn=sm_arn)
        except Exception:
            pass
