"""
groups/sns.py — SNS compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import json
import time
from lib.harness import TestContext
from lib.clients import make_clients


def _sns(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).sns


def _sqs(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).sqs


# ── sns-topics ────────────────────────────────────────────────────────────────

def setup_sns_topics(ctx: TestContext) -> None:
    sns = _sns(ctx)
    resp = sns.create_topic(Name=f"{ctx.run_id}-sns-t")
    ctx["sns_topic_arn"] = resp["TopicArn"]


def teardown_sns_topics(ctx: TestContext) -> None:
    arn = ctx.get("sns_topic_arn")
    if arn:
        try:
            _sns(ctx).delete_topic(TopicArn=arn)
        except Exception:
            pass


def CreateTopic(ctx: TestContext) -> None:
    sns = _sns(ctx)
    resp = sns.create_topic(Name=f"{ctx.run_id}-sns-create")
    arn = resp.get("TopicArn")
    if not arn:
        raise AssertionError("CreateTopic: missing TopicArn")
    sns.delete_topic(TopicArn=arn)


def ListTopics(ctx: TestContext) -> None:
    sns = _sns(ctx)
    arn = ctx["sns_topic_arn"]
    paginator = sns.get_paginator("list_topics")
    arns = []
    for page in paginator.paginate():
        arns.extend(t["TopicArn"] for t in page.get("Topics", []))
    if arn not in arns:
        raise AssertionError(f"ListTopics: {arn!r} not found in topic list")


def GetTopicAttributes(ctx: TestContext) -> None:
    sns = _sns(ctx)
    arn = ctx["sns_topic_arn"]
    resp = sns.get_topic_attributes(TopicArn=arn)
    if "TopicArn" not in resp.get("Attributes", {}):
        raise AssertionError(f"GetTopicAttributes: missing TopicArn in attributes {resp.get('Attributes')}")


def SetTopicAttributes(ctx: TestContext) -> None:
    sns = _sns(ctx)
    arn = ctx["sns_topic_arn"]
    sns.set_topic_attributes(TopicArn=arn, AttributeName="DisplayName", AttributeValue="CompatTest")
    resp = sns.get_topic_attributes(TopicArn=arn)
    if resp["Attributes"].get("DisplayName") != "CompatTest":
        raise AssertionError(f"SetTopicAttributes: DisplayName not updated; got {resp['Attributes'].get('DisplayName')!r}")


def DeleteTopic(ctx: TestContext) -> None:
    sns = _sns(ctx)
    resp = sns.create_topic(Name=f"{ctx.run_id}-sns-del")
    arn = resp["TopicArn"]
    sns.delete_topic(TopicArn=arn)
    list_resp = sns.list_topics()
    arns = [t["TopicArn"] for t in list_resp.get("Topics", [])]
    if arn in arns:
        raise AssertionError(f"DeleteTopic: {arn!r} still listed after deletion")


# ── sns-publish ───────────────────────────────────────────────────────────────

def setup_sns_publish(ctx: TestContext) -> None:
    sns = _sns(ctx)
    resp = sns.create_topic(Name=f"{ctx.run_id}-sns-pub")
    ctx["sns_pub_arn"] = resp["TopicArn"]


def teardown_sns_publish(ctx: TestContext) -> None:
    arn = ctx.get("sns_pub_arn")
    if arn:
        try:
            _sns(ctx).delete_topic(TopicArn=arn)
        except Exception:
            pass


def Publish(ctx: TestContext) -> None:
    sns = _sns(ctx)
    arn = ctx["sns_pub_arn"]
    resp = sns.publish(TopicArn=arn, Message="hello from python")
    if not resp.get("MessageId"):
        raise AssertionError("Publish: missing MessageId")


def PublishWithAttributes(ctx: TestContext) -> None:
    sns = _sns(ctx)
    arn = ctx["sns_pub_arn"]
    resp = sns.publish(
        TopicArn=arn,
        Message="message with attrs",
        MessageAttributes={
            "source": {"DataType": "String", "StringValue": "compat-test"}
        },
    )
    if not resp.get("MessageId"):
        raise AssertionError("PublishWithAttributes: missing MessageId")


def PublishBatch(ctx: TestContext) -> None:
    sns = _sns(ctx)
    arn = ctx["sns_pub_arn"]
    resp = sns.publish_batch(
        TopicArn=arn,
        PublishBatchRequestEntries=[
            {"Id": "1", "Message": "batch msg 1"},
            {"Id": "2", "Message": "batch msg 2"},
        ],
    )
    if len(resp.get("Successful", [])) < 2:
        raise AssertionError(f"PublishBatch: expected 2 successful, got {resp}")


# ── sns-subscriptions ─────────────────────────────────────────────────────────

def setup_sns_subscriptions(ctx: TestContext) -> None:
    sns = _sns(ctx)
    sqs = _sqs(ctx)
    topic_resp = sns.create_topic(Name=f"{ctx.run_id}-sns-sub")
    ctx["sns_sub_topic_arn"] = topic_resp["TopicArn"]
    q_resp = sqs.create_queue(QueueName=f"{ctx.run_id}-sns-sub-q")
    ctx["sns_sub_queue_url"] = q_resp["QueueUrl"]
    q_attrs = sqs.get_queue_attributes(
        QueueUrl=q_resp["QueueUrl"], AttributeNames=["QueueArn"]
    )
    ctx["sns_sub_queue_arn"] = q_attrs["Attributes"]["QueueArn"]


def teardown_sns_subscriptions(ctx: TestContext) -> None:
    sns = _sns(ctx)
    sqs = _sqs(ctx)
    sub_arn = ctx.get("sns_sub_arn")
    if sub_arn:
        try:
            sns.unsubscribe(SubscriptionArn=sub_arn)
        except Exception:
            pass
    topic_arn = ctx.get("sns_sub_topic_arn")
    if topic_arn:
        try:
            sns.delete_topic(TopicArn=topic_arn)
        except Exception:
            pass
    queue_url = ctx.get("sns_sub_queue_url")
    if queue_url:
        try:
            sqs.delete_queue(QueueUrl=queue_url)
        except Exception:
            pass


def SubscribeSQS(ctx: TestContext) -> None:
    sns = _sns(ctx)
    topic_arn = ctx["sns_sub_topic_arn"]
    queue_arn = ctx["sns_sub_queue_arn"]
    resp = sns.subscribe(
        TopicArn=topic_arn,
        Protocol="sqs",
        Endpoint=queue_arn,
        Attributes={"RawMessageDelivery": "true"},
    )
    if not resp.get("SubscriptionArn"):
        raise AssertionError("SubscribeSQS: missing SubscriptionArn")
    ctx["sns_sub_arn"] = resp["SubscriptionArn"]


def ListSubscriptionsByTopic(ctx: TestContext) -> None:
    sns = _sns(ctx)
    topic_arn = ctx["sns_sub_topic_arn"]
    resp = sns.list_subscriptions_by_topic(TopicArn=topic_arn)
    subs = resp.get("Subscriptions", [])
    if not subs:
        raise AssertionError("ListSubscriptionsByTopic: no subscriptions returned")


def GetSubscriptionAttributes(ctx: TestContext) -> None:
    sns = _sns(ctx)
    sub_arn = ctx.get("sns_sub_arn")
    if not sub_arn:
        raise AssertionError("GetSubscriptionAttributes: no subscription ARN")
    resp = sns.get_subscription_attributes(SubscriptionArn=sub_arn)
    if "SubscriptionArn" not in resp.get("Attributes", {}):
        raise AssertionError(f"GetSubscriptionAttributes: missing SubscriptionArn; got {resp.get('Attributes')}")


def PublishDeliveredToSQS(ctx: TestContext) -> None:
    sns = _sns(ctx)
    sqs = _sqs(ctx)
    topic_arn = ctx["sns_sub_topic_arn"]
    queue_url = ctx["sns_sub_queue_url"]
    sns.publish(TopicArn=topic_arn, Message="deliver me")
    # Poll until message arrives (or timeout)
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        resp = sqs.receive_message(QueueUrl=queue_url, MaxNumberOfMessages=1, WaitTimeSeconds=0)
        msgs = resp.get("Messages", [])
        if msgs:
            sqs.delete_message(QueueUrl=queue_url, ReceiptHandle=msgs[0]["ReceiptHandle"])
            return
        time.sleep(0.1)
    raise AssertionError("PublishDeliveredToSQS: message not delivered within 5s")


def SetSubscriptionAttributes(ctx: TestContext) -> None:
    sns = _sns(ctx)
    sub_arn = ctx.get("sns_sub_arn")
    if not sub_arn:
        raise AssertionError("SetSubscriptionAttributes: no subscription ARN")
    sns.set_subscription_attributes(
        SubscriptionArn=sub_arn,
        AttributeName="RawMessageDelivery",
        AttributeValue="false",
    )
    attrs = sns.get_subscription_attributes(SubscriptionArn=sub_arn)
    assert attrs["Attributes"].get("RawMessageDelivery") == "false", "SetSubscriptionAttributes: value not set"


def Unsubscribe(ctx: TestContext) -> None:
    sns = _sns(ctx)
    sub_arn = ctx.get("sns_sub_arn")
    if not sub_arn:
        raise AssertionError("Unsubscribe: no subscription ARN")
    topic_arn = ctx.get("sns_topic_arn")
    sns.unsubscribe(SubscriptionArn=sub_arn)
    ctx["sns_sub_arn"] = None
    if topic_arn:
        subs = sns.list_subscriptions_by_topic(TopicArn=topic_arn)
        arns = [s["SubscriptionArn"] for s in subs.get("Subscriptions", [])]
        assert sub_arn not in arns, f"Unsubscribe: subscription {sub_arn} still present"


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateTopic": CreateTopic,
    "ListTopics": ListTopics,
    "GetTopicAttributes": GetTopicAttributes,
    "SetTopicAttributes": SetTopicAttributes,
    "DeleteTopic": DeleteTopic,
    "Publish": Publish,
    "PublishWithAttributes": PublishWithAttributes,
    "PublishBatch": PublishBatch,
    "SubscribeSQS": SubscribeSQS,
    "ListSubscriptionsByTopic": ListSubscriptionsByTopic,
    "GetSubscriptionAttributes": GetSubscriptionAttributes,
    "PublishDeliveredToSQS": PublishDeliveredToSQS,
    "SetSubscriptionAttributes": SetSubscriptionAttributes,
    "Unsubscribe": Unsubscribe,
}

SETUP = {
    "sns-topics": setup_sns_topics,
    "sns-publish": setup_sns_publish,
    "sns-subscriptions": setup_sns_subscriptions,
}

TEARDOWN = {
    "sns-topics": teardown_sns_topics,
    "sns-publish": teardown_sns_publish,
    "sns-subscriptions": teardown_sns_subscriptions,
}
