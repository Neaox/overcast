"""
groups/pipes.py — EventBridge Pipes compatibility test implementations.

Covers pipe CRUD, the loud rejection of a source/target combination the
emulator cannot run, and an end-to-end delivery from a DynamoDB stream source
to an SQS target.

The group provisions its own table and queues so it never races another group
(issue #388), and the delivery assertion matches on this group's own record
rather than "the first message received".
"""

from __future__ import annotations

import json
import time

from botocore.exceptions import ClientError

from lib.clients import make_clients
from lib.harness import TestContext


def _pipes(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).pipes


def _role_arn(ctx: TestContext) -> str:
    # RoleArn is a required CreatePipe member. Overcast does not evaluate it.
    return "arn:aws:iam::000000000000:role/oc-compat-pipe"


# ── pipes-wiring ──────────────────────────────────────────────────────────────

def setup_pipes_wiring(ctx: TestContext) -> None:
    clients = make_clients(ctx.endpoint, ctx.region)
    table = f"oc-{ctx.run_id}-pipe-src"
    queue = f"oc-{ctx.run_id}-pipe-dst"

    clients.dynamodb.create_table(
        TableName=table,
        AttributeDefinitions=[{"AttributeName": "id", "AttributeType": "S"}],
        KeySchema=[{"AttributeName": "id", "KeyType": "HASH"}],
        BillingMode="PAY_PER_REQUEST",
        StreamSpecification={"StreamEnabled": True, "StreamViewType": "NEW_AND_OLD_IMAGES"},
    )
    desc = clients.dynamodb.describe_table(TableName=table)
    ctx["pipe_table"] = table
    ctx["pipe_source_arn"] = desc["Table"]["LatestStreamArn"]

    created = clients.sqs.create_queue(QueueName=queue)
    attrs = clients.sqs.get_queue_attributes(
        QueueUrl=created["QueueUrl"], AttributeNames=["QueueArn"]
    )
    ctx["pipe_queue_url"] = created["QueueUrl"]
    ctx["pipe_target_arn"] = attrs["Attributes"]["QueueArn"]
    ctx["pipe_name"] = f"oc-{ctx.run_id}-pipe"


def teardown_pipes_wiring(ctx: TestContext) -> None:
    clients = make_clients(ctx.endpoint, ctx.region)
    try:
        clients.pipes.delete_pipe(Name=ctx["pipe_name"])
    except Exception:
        pass
    try:
        clients.sqs.delete_queue(QueueUrl=ctx["pipe_queue_url"])
    except Exception:
        pass
    try:
        clients.dynamodb.delete_table(TableName=ctx["pipe_table"])
    except Exception:
        pass


def CreatePipe(ctx: TestContext) -> None:
    resp = _pipes(ctx).create_pipe(
        Name=ctx["pipe_name"],
        Source=ctx["pipe_source_arn"],
        Target=ctx["pipe_target_arn"],
        RoleArn=_role_arn(ctx),
    )
    if not resp.get("Arn"):
        raise AssertionError(f"CreatePipe: missing Arn in {resp}")
    if resp.get("Name") != ctx["pipe_name"]:
        raise AssertionError(f"CreatePipe: name mismatch {resp.get('Name')!r}")
    if resp.get("DesiredState") != "RUNNING":
        raise AssertionError(f"CreatePipe: DesiredState {resp.get('DesiredState')!r}, want RUNNING")


def DescribePipe(ctx: TestContext) -> None:
    resp = _pipes(ctx).describe_pipe(Name=ctx["pipe_name"])
    if resp.get("Source") != ctx["pipe_source_arn"]:
        raise AssertionError(f"DescribePipe: Source {resp.get('Source')!r}")
    if resp.get("Target") != ctx["pipe_target_arn"]:
        raise AssertionError(f"DescribePipe: Target {resp.get('Target')!r}")
    if resp.get("RoleArn") != _role_arn(ctx):
        raise AssertionError(f"DescribePipe: RoleArn {resp.get('RoleArn')!r} did not round-trip")


def ListPipes(ctx: TestContext) -> None:
    resp = _pipes(ctx).list_pipes(NamePrefix=f"oc-{ctx.run_id}")
    names = [p.get("Name") for p in resp.get("Pipes", [])]
    if ctx["pipe_name"] not in names:
        raise AssertionError(f"ListPipes: {ctx['pipe_name']} not in {names}")


def CreatePipeRejectsUnsupportedTarget(ctx: TestContext) -> None:
    """A well-formed ARN naming a service the emulator cannot deliver to must
    fail the call rather than being stored and silently ignored."""
    try:
        _pipes(ctx).create_pipe(
            Name=f"oc-{ctx.run_id}-pipe-bad",
            Source=ctx["pipe_source_arn"],
            Target="arn:aws:logs:us-east-1:000000000000:log-group:/aws/pipes/compat",
            RoleArn=_role_arn(ctx),
        )
    except ClientError as err:
        code = err.response["Error"]["Code"]
        if code != "ValidationException":
            raise AssertionError(f"CreatePipe: error code {code!r}, want ValidationException")
        return
    raise AssertionError("CreatePipe accepted a target the emulator cannot deliver to")


def PipeDeliversToTarget(ctx: TestContext) -> None:
    """A record written to the source table reaches the target queue."""
    clients = make_clients(ctx.endpoint, ctx.region)
    marker = f"oc-{ctx.run_id}-delivery"

    # The pipe reaches RUNNING asynchronously; write until it is live.
    for _ in range(20):
        state = clients.pipes.describe_pipe(Name=ctx["pipe_name"]).get("CurrentState")
        if state == "RUNNING":
            break
        time.sleep(0.1)

    clients.dynamodb.put_item(TableName=ctx["pipe_table"], Item={"id": {"S": marker}})

    for _ in range(20):
        resp = clients.sqs.receive_message(
            QueueUrl=ctx["pipe_queue_url"], MaxNumberOfMessages=10, WaitTimeSeconds=1
        )
        for message in resp.get("Messages", []):
            body = message.get("Body", "")
            try:
                clients.sqs.delete_message(
                    QueueUrl=ctx["pipe_queue_url"], ReceiptHandle=message["ReceiptHandle"]
                )
            except Exception:
                pass
            # Match on this group's own record, never "the first message".
            if marker in body:
                record = json.loads(body)
                if record.get("eventSource") != "aws:dynamodb":
                    raise AssertionError(f"delivered record is not a DynamoDB record: {body}")
                return
        time.sleep(0.1)
    raise AssertionError("no pipe delivery reached the target queue")


def UpdatePipe(ctx: TestContext) -> None:
    resp = _pipes(ctx).update_pipe(
        Name=ctx["pipe_name"],
        DesiredState="STOPPED",
        RoleArn=_role_arn(ctx),
        Description="compat",
    )
    if resp.get("DesiredState") != "STOPPED":
        raise AssertionError(f"UpdatePipe: DesiredState {resp.get('DesiredState')!r}, want STOPPED")


def DeletePipe(ctx: TestContext) -> None:
    resp = _pipes(ctx).delete_pipe(Name=ctx["pipe_name"])
    if resp.get("CurrentState") != "DELETING":
        raise AssertionError(f"DeletePipe: CurrentState {resp.get('CurrentState')!r}, want DELETING")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreatePipe": CreatePipe,
    "DescribePipe": DescribePipe,
    "ListPipes": ListPipes,
    "CreatePipeRejectsUnsupportedTarget": CreatePipeRejectsUnsupportedTarget,
    "PipeDeliversToTarget": PipeDeliversToTarget,
    "UpdatePipe": UpdatePipe,
    "DeletePipe": DeletePipe,
}

SETUP = {
    "pipes-wiring": setup_pipes_wiring,
}

TEARDOWN = {
    "pipes-wiring": teardown_pipes_wiring,
}
