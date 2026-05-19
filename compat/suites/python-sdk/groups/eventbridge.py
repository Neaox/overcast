"""
groups/eventbridge.py — EventBridge compatibility test implementations.

Note: boto3 uses the service name "events" for EventBridge.
"""

from __future__ import annotations
import json
from lib.harness import TestContext
from lib.clients import make_clients


def _eb(ctx: TestContext):
    # boto3 uses "events" as the service identifier for EventBridge
    return make_clients(ctx.endpoint, ctx.region).eventbridge


# ── eventbridge-buses ─────────────────────────────────────────────────────────

def setup_eventbridge_buses(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = f"oc-{ctx.run_id}-bus"
    resp = eb.create_event_bus(Name=name)
    ctx["eb_bus_name"] = name
    ctx["eb_bus_arn"] = resp.get("EventBusArn", "")


def teardown_eventbridge_buses(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = ctx.get("eb_bus_name")
    if not name:
        return
    try:
        eb.delete_event_bus(Name=name)
    except Exception:
        pass


def CreateEventBus(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = f"oc-{ctx.run_id}-bus-create"
    resp = eb.create_event_bus(Name=name)
    if not resp.get("EventBusArn"):
        raise AssertionError(f"CreateEventBus: missing EventBusArn in {resp}")
    # Clean up
    try:
        eb.delete_event_bus(Name=name)
    except Exception:
        pass


def DescribeEventBus(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = ctx["eb_bus_name"]
    resp = eb.describe_event_bus(Name=name)
    if resp["Name"] != name:
        raise AssertionError(f"DescribeEventBus: name mismatch {resp['Name']!r}")
    if not resp.get("Arn"):
        raise AssertionError("DescribeEventBus: missing Arn")


def ListEventBuses(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = ctx["eb_bus_name"]
    resp = eb.list_event_buses()
    buses = resp.get("EventBuses", [])
    if not any(b["Name"] == name for b in buses):
        raise AssertionError(f"ListEventBuses: {name!r} not found in {[b['Name'] for b in buses]}")


def TagEventBus(ctx: TestContext) -> None:
    eb = _eb(ctx)
    arn = ctx["eb_bus_arn"]
    eb.tag_resource(ResourceARN=arn, Tags=[{"Key": "env", "Value": "compat"}])
    resp = eb.list_tags_for_resource(ResourceARN=arn)
    tags = {t["Key"]: t["Value"] for t in resp.get("Tags", [])}
    assert tags.get("env") == "compat", f"TagEventBus: env tag not found, got {tags}"


def ListTagsForResource(ctx: TestContext) -> None:
    eb = _eb(ctx)
    arn = ctx["eb_bus_arn"]
    resp = eb.list_tags_for_resource(ResourceARN=arn)
    tags = {t["Key"]: t["Value"] for t in resp.get("Tags", [])}
    if tags.get("env") != "compat":
        raise AssertionError(f"ListTagsForResource: expected env=compat, got {tags}")


def DeleteEventBus(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = f"oc-{ctx.run_id}-bus-del"
    eb.create_event_bus(Name=name)
    eb.delete_event_bus(Name=name)
    resp = eb.list_event_buses(NamePrefix=name)
    names = [b["Name"] for b in resp.get("EventBuses", [])]
    assert name not in names, f"DeleteEventBus: bus {name} still present"


# ── eventbridge-rules ─────────────────────────────────────────────────────────

def setup_eventbridge_rules(ctx: TestContext) -> None:
    eb = _eb(ctx)
    bus_name = f"oc-{ctx.run_id}-rulebus"
    eb.create_event_bus(Name=bus_name)
    ctx["eb_rule_bus"] = bus_name


def teardown_eventbridge_rules(ctx: TestContext) -> None:
    eb = _eb(ctx)
    bus_name = ctx.get("eb_rule_bus")
    if not bus_name:
        return
    # Remove targets + rules before deleting bus
    rule_name = ctx.get("eb_rule_name")
    if rule_name:
        target_ids = ctx.get("eb_target_ids", [])
        if target_ids:
            try:
                eb.remove_targets(Rule=rule_name, EventBusName=bus_name, Ids=target_ids)
            except Exception:
                pass
        try:
            eb.delete_rule(Name=rule_name, EventBusName=bus_name, Force=True)
        except Exception:
            pass
    try:
        eb.delete_event_bus(Name=bus_name)
    except Exception:
        pass


def PutRule(ctx: TestContext) -> None:
    eb = _eb(ctx)
    bus_name = ctx["eb_rule_bus"]
    rule_name = f"oc-{ctx.run_id}-rule"
    resp = eb.put_rule(
        Name=rule_name,
        EventBusName=bus_name,
        EventPattern=json.dumps({"source": ["com.example.overcast"]}),
        State="ENABLED",
    )
    if not resp.get("RuleArn"):
        raise AssertionError(f"PutRule: missing RuleArn in {resp}")
    ctx["eb_rule_name"] = rule_name


def DescribeRule(ctx: TestContext) -> None:
    eb = _eb(ctx)
    rule_name = ctx["eb_rule_name"]
    bus_name = ctx["eb_rule_bus"]
    resp = eb.describe_rule(Name=rule_name, EventBusName=bus_name)
    if resp["Name"] != rule_name:
        raise AssertionError(f"DescribeRule: name mismatch {resp['Name']!r}")
    if resp["State"] != "ENABLED":
        raise AssertionError(f"DescribeRule: expected ENABLED, got {resp['State']!r}")


def ListRules(ctx: TestContext) -> None:
    eb = _eb(ctx)
    rule_name = ctx["eb_rule_name"]
    bus_name = ctx["eb_rule_bus"]
    resp = eb.list_rules(EventBusName=bus_name)
    rules = resp.get("Rules", [])
    if not any(r["Name"] == rule_name for r in rules):
        raise AssertionError(f"ListRules: {rule_name!r} not found")


def PutTargets(ctx: TestContext) -> None:
    eb = _eb(ctx)
    rule_name = ctx["eb_rule_name"]
    bus_name = ctx["eb_rule_bus"]
    # Use a fake SQS ARN — target creation doesn't validate the ARN at registration time
    target_id = "t1"
    resp = eb.put_targets(
        Rule=rule_name,
        EventBusName=bus_name,
        Targets=[{
            "Id": target_id,
            "Arn": f"arn:aws:sqs:us-east-1:000000000000:oc-{ctx.run_id}-tgt",
        }],
    )
    if resp.get("FailedEntryCount", 0) > 0:
        raise AssertionError(f"PutTargets: {resp['FailedEntryCount']} failed entries {resp.get('FailedEntries')}")
    ctx["eb_target_ids"] = [target_id]


def ListTargetsByRule(ctx: TestContext) -> None:
    eb = _eb(ctx)
    rule_name = ctx["eb_rule_name"]
    bus_name = ctx["eb_rule_bus"]
    resp = eb.list_targets_by_rule(Rule=rule_name, EventBusName=bus_name)
    targets = resp.get("Targets", [])
    if not targets:
        raise AssertionError("ListTargetsByRule: no targets returned")


def DisableRule(ctx: TestContext) -> None:
    eb = _eb(ctx)
    rule_name = ctx["eb_rule_name"]
    bus_name = ctx["eb_rule_bus"]
    eb.disable_rule(Name=rule_name, EventBusName=bus_name)
    resp = eb.describe_rule(Name=rule_name, EventBusName=bus_name)
    if resp["State"] != "DISABLED":
        raise AssertionError(f"DisableRule: expected DISABLED, got {resp['State']!r}")


def EnableRule(ctx: TestContext) -> None:
    eb = _eb(ctx)
    rule_name = ctx["eb_rule_name"]
    bus_name = ctx["eb_rule_bus"]
    eb.enable_rule(Name=rule_name, EventBusName=bus_name)
    resp = eb.describe_rule(Name=rule_name, EventBusName=bus_name)
    if resp["State"] != "ENABLED":
        raise AssertionError(f"EnableRule: expected ENABLED, got {resp['State']!r}")


def RemoveTargets(ctx: TestContext) -> None:
    eb = _eb(ctx)
    rule_name = ctx["eb_rule_name"]
    bus_name = ctx["eb_rule_bus"]
    target_ids = ctx.get("eb_target_ids", ["t1"])
    resp = eb.remove_targets(Rule=rule_name, EventBusName=bus_name, Ids=target_ids)
    if resp.get("FailedEntryCount", 0) > 0:
        raise AssertionError(f"RemoveTargets: {resp['FailedEntryCount']} failed entries")
    ctx["eb_target_ids"] = []
    remaining = eb.list_targets_by_rule(Rule=rule_name, EventBusName=bus_name)
    assert len(remaining.get("Targets", [])) == 0, "RemoveTargets: targets still present"


def DeleteRule(ctx: TestContext) -> None:
    eb = _eb(ctx)
    bus_name = ctx["eb_rule_bus"]
    rule_name = f"oc-{ctx.run_id}-delrule"
    eb.put_rule(
        Name=rule_name,
        EventBusName=bus_name,
        EventPattern=json.dumps({"source": ["com.example.tmp"]}),
        State="ENABLED",
    )
    eb.delete_rule(Name=rule_name, EventBusName=bus_name, Force=True)
    resp = eb.list_rules(EventBusName=bus_name, NamePrefix=rule_name)
    names = [r["Name"] for r in resp.get("Rules", [])]
    assert rule_name not in names, f"DeleteRule: rule {rule_name} still present"


# ── eventbridge-events ────────────────────────────────────────────────────────

def setup_eventbridge_events(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = f"oc-{ctx.run_id}-evtbus"
    eb.create_event_bus(Name=name)
    ctx["eb_evt_bus"] = name


def teardown_eventbridge_events(ctx: TestContext) -> None:
    eb = _eb(ctx)
    name = ctx.get("eb_evt_bus")
    if not name:
        return
    try:
        eb.delete_event_bus(Name=name)
    except Exception:
        pass


def PutEvents(ctx: TestContext) -> None:
    eb = _eb(ctx)
    bus_name = ctx["eb_evt_bus"]
    resp = eb.put_events(
        Entries=[{
            "Source": "com.example.overcast",
            "DetailType": "TestEvent",
            "Detail": json.dumps({"key": "value"}),
            "EventBusName": bus_name,
        }]
    )
    if resp.get("FailedEntryCount", 0) > 0:
        raise AssertionError(f"PutEvents: {resp['FailedEntryCount']} failed entries {resp.get('Entries')}")


def PutEventsBatch(ctx: TestContext) -> None:
    eb = _eb(ctx)
    bus_name = ctx["eb_evt_bus"]
    entries = [
        {
            "Source": "com.example.overcast",
            "DetailType": f"BatchEvent{i}",
            "Detail": json.dumps({"index": i}),
            "EventBusName": bus_name,
        }
        for i in range(5)
    ]
    resp = eb.put_events(Entries=entries)
    if resp.get("FailedEntryCount", 0) > 0:
        raise AssertionError(f"PutEventsBatch: {resp['FailedEntryCount']} failed entries {resp.get('Entries')}")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateEventBus": CreateEventBus,
    "DescribeEventBus": DescribeEventBus,
    "ListEventBuses": ListEventBuses,
    "TagEventBus": TagEventBus,
    "ListEventBridgeTagsForResource": ListTagsForResource,
    "DeleteEventBus": DeleteEventBus,
    "PutRule": PutRule,
    "DescribeRule": DescribeRule,
    "ListRules": ListRules,
    "PutTargets": PutTargets,
    "ListTargetsByRule": ListTargetsByRule,
    "DisableRule": DisableRule,
    "EnableRule": EnableRule,
    "RemoveTargets": RemoveTargets,
    "DeleteRule": DeleteRule,
    "PutEvents": PutEvents,
    "PutEventsBatch": PutEventsBatch,
}

SETUP = {
    "eventbridge-buses": setup_eventbridge_buses,
    "eventbridge-rules": setup_eventbridge_rules,
    "eventbridge-events": setup_eventbridge_events,
}

TEARDOWN = {
    "eventbridge-buses": teardown_eventbridge_buses,
    "eventbridge-rules": teardown_eventbridge_rules,
    "eventbridge-events": teardown_eventbridge_events,
}
