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


# ── sfn-executions ────────────────────────────────────────────────────────────
#
# The execution engine really interprets the definition, so these tests assert
# on what the execution produced rather than just that a call succeeded.

_EXEC_DEFINITION = json.dumps({
    "Comment": "compat executions",
    "StartAt": "Hello",
    "States": {"Hello": {"Type": "Pass", "Result": {"greeting": "hello"}, "End": True}},
})


def _setup_executions(ctx: TestContext) -> None:
    """Provision this group's own state machine — groups never share resources."""
    resp = _sfn(ctx).create_state_machine(
        name=f"compat-exec-{ctx.run_id}",
        definition=_EXEC_DEFINITION,
        roleArn=f"arn:aws:iam::000000000000:role/compat-sfn-exec-{ctx.run_id}",
        type="EXPRESS",
    )
    if not resp.get("stateMachineArn"):
        raise AssertionError("setup sfn-executions: missing stateMachineArn")
    ctx["sfn_exec_sm_arn"] = resp["stateMachineArn"]


def _teardown_executions(ctx: TestContext) -> None:
    sm_arn = ctx.get("sfn_exec_sm_arn")
    if sm_arn:
        try:
            _sfn(ctx).delete_state_machine(stateMachineArn=sm_arn)
        except Exception:
            pass


def ExecStartExecution(ctx: TestContext) -> None:
    sm_arn = ctx.get("sfn_exec_sm_arn")
    if not sm_arn:
        raise AssertionError("StartExecution: no state machine from setup")
    resp = _sfn(ctx).start_execution(
        stateMachineArn=sm_arn,
        name=f"run-{ctx.run_id}",
        input=json.dumps({"key": "value"}),
    )
    if not resp.get("executionArn"):
        raise AssertionError("StartExecution: missing executionArn")
    ctx["sfn_exec_arn"] = resp["executionArn"]


def DescribeExecution(ctx: TestContext) -> None:
    exec_arn = ctx.get("sfn_exec_arn")
    if not exec_arn:
        raise AssertionError("DescribeExecution: no execution from StartExecution")
    resp = _sfn(ctx).describe_execution(executionArn=exec_arn)
    if not resp.get("status"):
        raise AssertionError("DescribeExecution: missing status")
    if resp.get("executionArn") != exec_arn:
        raise AssertionError("DescribeExecution: executionArn did not round-trip")


def GetExecutionHistory(ctx: TestContext) -> None:
    exec_arn = ctx.get("sfn_exec_arn")
    if not exec_arn:
        raise AssertionError("GetExecutionHistory: no execution from StartExecution")
    resp = _sfn(ctx).get_execution_history(executionArn=exec_arn)
    if not resp.get("events"):
        raise AssertionError("GetExecutionHistory: no events")


def ListExecutions(ctx: TestContext) -> None:
    sm_arn = ctx.get("sfn_exec_sm_arn")
    if not sm_arn:
        raise AssertionError("ListExecutions: no state machine from setup")
    resp = _sfn(ctx).list_executions(stateMachineArn=sm_arn)
    exec_arn = ctx.get("sfn_exec_arn")
    arns = [e.get("executionArn") for e in resp.get("executions", [])]
    if exec_arn not in arns:
        raise AssertionError(f"ListExecutions: execution {exec_arn} not listed")


def StopExecution(ctx: TestContext) -> None:
    exec_arn = ctx.get("sfn_exec_arn")
    if not exec_arn:
        raise AssertionError("StopExecution: no execution from StartExecution")
    _sfn(ctx).stop_execution(executionArn=exec_arn)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "sfn-statemachines:CreateStateMachine": CreateStateMachine,
    "sfn-statemachines:DescribeStateMachine": DescribeStateMachine,
    "sfn-statemachines:ListStateMachines": ListStateMachines,
    "sfn-statemachines:StartExecution": StartExecution,
    "sfn-statemachines:DeleteStateMachine": DeleteStateMachine,
    # sfn-executions — group-qualified so StartExecution does not collide with
    # the sfn-statemachines test of the same name.
    "sfn-executions:StartExecution": ExecStartExecution,
    "sfn-executions:DescribeExecution": DescribeExecution,
    "sfn-executions:GetExecutionHistory": GetExecutionHistory,
    "sfn-executions:ListExecutions": ListExecutions,
    "sfn-executions:StopExecution": StopExecution,
}

SETUP = {
    "sfn-executions": _setup_executions,
}
TEARDOWN = {
    "sfn-statemachines": lambda ctx: _teardown_state_machine(ctx),
    "sfn-executions": _teardown_executions,
}


def _teardown_state_machine(ctx: TestContext) -> None:
    sm_arn = ctx.get("sfn_sm_arn")
    if sm_arn:
        try:
            _sfn(ctx).delete_state_machine(stateMachineArn=sm_arn)
        except Exception:
            pass
