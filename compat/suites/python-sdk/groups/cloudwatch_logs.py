"""
groups/cloudwatch_logs.py — CloudWatch Logs compatibility test implementations.
"""

from __future__ import annotations
import time
from lib.harness import TestContext
from lib.clients import make_clients


def _logs(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).logs


# ── logs-groups ───────────────────────────────────────────────────────────────

def setup_logs_groups(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = f"/compat/{ctx.run_id}"
    logs.create_log_group(logGroupName=name)
    ctx["log_group"] = name


def teardown_logs_groups(ctx: TestContext) -> None:
    name = ctx.get("log_group")
    if name:
        try:
            _logs(ctx).delete_log_group(logGroupName=name)
        except Exception:
            pass


def CreateLogGroup(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = f"/compat/{ctx.run_id}-create"
    logs.create_log_group(logGroupName=name)
    try:
        resp = logs.describe_log_groups(logGroupNamePrefix=name)
        groups = resp.get("logGroups", [])
        if not any(g["logGroupName"] == name for g in groups):
            raise AssertionError(f"CreateLogGroup: {name!r} not found after creation")
    finally:
        logs.delete_log_group(logGroupName=name)


def DescribeLogGroups(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_group"]
    resp = logs.describe_log_groups(logGroupNamePrefix=name)
    groups = resp.get("logGroups", [])
    if not any(g["logGroupName"] == name for g in groups):
        raise AssertionError(f"DescribeLogGroups: {name!r} not found")


def PutRetentionPolicy(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_group"]
    logs.put_retention_policy(logGroupName=name, retentionInDays=7)
    resp = logs.describe_log_groups(logGroupNamePrefix=name)
    groups = [g for g in resp.get("logGroups", []) if g["logGroupName"] == name]
    assert groups and groups[0].get("retentionInDays") == 7, "PutRetentionPolicy: retention!=7"


def VerifyRetentionPolicy(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_group"]
    resp = logs.describe_log_groups(logGroupNamePrefix=name)
    groups = resp.get("logGroups", [])
    matching = [g for g in groups if g["logGroupName"] == name]
    if not matching:
        raise AssertionError(f"VerifyRetentionPolicy: log group {name!r} not found")
    if matching[0].get("retentionInDays") != 7:
        raise AssertionError(f"VerifyRetentionPolicy: expected 7 days, got {matching[0].get('retentionInDays')}")


def DeleteRetentionPolicy(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_group"]
    logs.delete_retention_policy(logGroupName=name)
    resp = logs.describe_log_groups(logGroupNamePrefix=name)
    groups = [g for g in resp.get("logGroups", []) if g["logGroupName"] == name]
    assert groups and "retentionInDays" not in groups[0], "DeleteRetentionPolicy: retention still set"


def DeleteLogGroup(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = f"/compat/{ctx.run_id}-del"
    logs.create_log_group(logGroupName=name)
    logs.delete_log_group(logGroupName=name)
    resp = logs.describe_log_groups(logGroupNamePrefix=name)
    groups = resp.get("logGroups", [])
    if any(g["logGroupName"] == name for g in groups):
        raise AssertionError(f"DeleteLogGroup: {name!r} still listed after deletion")


def CreateLogStream(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_group"]
    stream = f"stream-grp-{ctx.run_id}"
    logs.create_log_stream(logGroupName=name, logStreamName=stream)


def TagLogGroup(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_group"]
    logs.tag_log_group(logGroupName=name, tags={"env": "test"})


# ── logs-events ───────────────────────────────────────────────────────────────

def setup_logs_events(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = f"/compat/{ctx.run_id}-events"
    stream = "mystream"
    logs.create_log_group(logGroupName=name)
    logs.create_log_stream(logGroupName=name, logStreamName=stream)
    ctx["log_events_group"] = name
    ctx["log_events_stream"] = stream


def teardown_logs_events(ctx: TestContext) -> None:
    name = ctx.get("log_events_group")
    if name:
        try:
            _logs(ctx).delete_log_group(logGroupName=name)
        except Exception:
            pass


def PutLogEvents(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_events_group"]
    stream = ctx["log_events_stream"]
    now_ms = int(time.time() * 1000)
    logs.put_log_events(
        logGroupName=name,
        logStreamName=stream,
        logEvents=[
            {"timestamp": now_ms, "message": "event one"},
            {"timestamp": now_ms + 1, "message": "event two"},
        ],
    )


def GetLogEvents(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_events_group"]
    stream = ctx["log_events_stream"]
    resp = logs.get_log_events(
        logGroupName=name,
        logStreamName=stream,
        startFromHead=True,
    )
    events = resp.get("events", [])
    if not events:
        raise AssertionError("GetLogEvents: no events returned")
    messages = [e["message"] for e in events]
    if "event one" not in messages:
        raise AssertionError(f"GetLogEvents: expected 'event one' in {messages}")


def FilterLogEvents(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_events_group"]
    resp = logs.filter_log_events(logGroupName=name, filterPattern="event two")
    events = resp.get("events", [])
    if not events:
        raise AssertionError("FilterLogEvents: no events returned matching 'event two'")


def DescribeLogStreams(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_events_group"]
    stream = ctx["log_events_stream"]
    resp = logs.describe_log_streams(logGroupName=name)
    streams = [s["logStreamName"] for s in resp.get("logStreams", [])]
    if stream not in streams:
        raise AssertionError(f"DescribeLogStreams: {stream!r} not found in {streams}")


def DeleteLogStream(ctx: TestContext) -> None:
    logs = _logs(ctx)
    name = ctx["log_events_group"]
    stream = ctx["log_events_stream"]
    logs.delete_log_stream(logGroupName=name, logStreamName=stream)
    resp = logs.describe_log_streams(logGroupName=name)
    streams = [s["logStreamName"] for s in resp.get("logStreams", [])]
    if stream in streams:
        raise AssertionError(f"DeleteLogStream: {stream!r} still listed")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateLogGroup": CreateLogGroup,
    "DescribeLogGroups": DescribeLogGroups,
    "PutRetentionPolicy": PutRetentionPolicy,
    "VerifyRetentionPolicy": VerifyRetentionPolicy,
    "DeleteRetentionPolicy": DeleteRetentionPolicy,
    "CreateLogStream": CreateLogStream,
    "TagLogGroup": TagLogGroup,
    "DeleteLogGroup": DeleteLogGroup,
    "PutLogEvents": PutLogEvents,
    "GetLogEvents": GetLogEvents,
    "FilterLogEvents": FilterLogEvents,
    "DescribeLogStreams": DescribeLogStreams,
    "DeleteLogStream": DeleteLogStream,
}

SETUP = {
    "logs-groups": setup_logs_groups,
    "logs-events": setup_logs_events,
}

TEARDOWN = {
    "logs-groups": teardown_logs_groups,
    "logs-events": teardown_logs_events,
}
