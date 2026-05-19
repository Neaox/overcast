"""
groups/kinesis.py — Kinesis compatibility test implementations.
"""

from __future__ import annotations
import base64
import time
from lib.harness import TestContext
from lib.clients import make_clients


def _kin(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).kinesis


def _wait_stream_active(kin, stream_name: str, max_wait: int = 10) -> None:
    deadline = time.time() + max_wait
    while time.time() < deadline:
        resp = kin.describe_stream_summary(StreamName=stream_name)
        status = resp["StreamDescriptionSummary"]["StreamStatus"]
        if status == "ACTIVE":
            return
        time.sleep(0.2)
    raise TimeoutError(f"Stream {stream_name!r} did not become ACTIVE within {max_wait}s")


# ── kinesis-streams ───────────────────────────────────────────────────────────

def setup_kinesis_streams(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = f"oc-{ctx.run_id}-stream"
    kin.create_stream(StreamName=name, ShardCount=1)
    _wait_stream_active(kin, name)
    ctx["kinesis_stream"] = name


def teardown_kinesis_streams(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx.get("kinesis_stream")
    if not name:
        return
    try:
        kin.delete_stream(StreamName=name, EnforceConsumerDeletion=True)
    except Exception:
        pass


def CreateStream(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = f"oc-{ctx.run_id}-create"
    kin.create_stream(StreamName=name, ShardCount=1)
    _wait_stream_active(kin, name)
    ctx["kinesis_create_stream"] = name
    kin.delete_stream(StreamName=name, EnforceConsumerDeletion=True)


def DescribeStream(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_stream"]
    resp = kin.describe_stream(StreamName=name)
    desc = resp["StreamDescription"]
    if desc["StreamName"] != name:
        raise AssertionError(f"DescribeStream: name mismatch {desc['StreamName']!r}")
    if not desc.get("Shards"):
        raise AssertionError("DescribeStream: no shards returned")


def DescribeStreamSummary(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_stream"]
    resp = kin.describe_stream_summary(StreamName=name)
    summary = resp["StreamDescriptionSummary"]
    if summary["StreamName"] != name:
        raise AssertionError(f"DescribeStreamSummary: name mismatch {summary['StreamName']!r}")
    if summary["OpenShardCount"] < 1:
        raise AssertionError(f"DescribeStreamSummary: expected ≥1 shard, got {summary['OpenShardCount']}")


def ListStreams(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_stream"]
    resp = kin.list_streams()
    if name not in resp.get("StreamNames", []):
        raise AssertionError(f"ListStreams: {name!r} not found in {resp.get('StreamNames')}")


def AddTagsToStream(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_stream"]
    kin.add_tags_to_stream(StreamName=name, Tags={"env": "compat", "suite": "python-sdk"})
    resp = kin.list_tags_for_stream(StreamName=name)
    tags = {t["Key"]: t["Value"] for t in resp.get("Tags", [])}
    if tags.get("env") != "compat":
        raise AssertionError(f"AddTagsToStream: env=compat tag not found after add; got {tags}")


def ListTagsForStream(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_stream"]
    resp = kin.list_tags_for_stream(StreamName=name)
    tags = {t["Key"]: t["Value"] for t in resp.get("Tags", [])}
    if tags.get("env") != "compat":
        raise AssertionError(f"ListTagsForStream: expected env=compat, got {tags}")


def DeleteStream(ctx: TestContext) -> None:
    # Already tested via setup lifecycle; test a separate transient stream
    kin = _kin(ctx)
    name = f"oc-{ctx.run_id}-del"
    kin.create_stream(StreamName=name, ShardCount=1)
    _wait_stream_active(kin, name)
    kin.delete_stream(StreamName=name, EnforceConsumerDeletion=True)
    resp = kin.list_streams()
    if name in resp.get("StreamNames", []):
        raise AssertionError(f"DeleteStream: {name!r} still listed after deletion")


# ── kinesis-records ───────────────────────────────────────────────────────────

def setup_kinesis_records(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = f"oc-{ctx.run_id}-rec"
    kin.create_stream(StreamName=name, ShardCount=1)
    _wait_stream_active(kin, name)
    ctx["kinesis_rec_stream"] = name


def teardown_kinesis_records(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx.get("kinesis_rec_stream")
    if not name:
        return
    try:
        kin.delete_stream(StreamName=name, EnforceConsumerDeletion=True)
    except Exception:
        pass


def PutRecord(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_rec_stream"]
    resp = kin.put_record(
        StreamName=name,
        Data=b"hello-record",
        PartitionKey="pk-1",
    )
    if not resp.get("ShardId"):
        raise AssertionError(f"PutRecord: missing ShardId in {resp}")
    ctx["kinesis_shard_id"] = resp["ShardId"]


def PutRecords(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_rec_stream"]
    records = [
        {"Data": f"record-{i}".encode(), "PartitionKey": f"pk-{i}"}
        for i in range(5)
    ]
    resp = kin.put_records(StreamName=name, Records=records)
    if resp.get("FailedRecordCount", 0) > 0:
        raise AssertionError(f"PutRecords: {resp['FailedRecordCount']} failed records")


def GetShardIterator(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_rec_stream"]
    shard_id = ctx.get("kinesis_shard_id") or "shardId-000000000000"
    resp = kin.get_shard_iterator(
        StreamName=name,
        ShardId=shard_id,
        ShardIteratorType="TRIM_HORIZON",
    )
    it = resp.get("ShardIterator")
    if not it:
        raise AssertionError("GetShardIterator: missing ShardIterator")
    ctx["kinesis_shard_iter"] = it


def GetRecords(ctx: TestContext) -> None:
    kin = _kin(ctx)
    it = ctx.get("kinesis_shard_iter")
    if not it:
        raise AssertionError("GetRecords: no shard iterator in context (run GetShardIterator first)")
    resp = kin.get_records(ShardIterator=it, Limit=100)
    records = resp.get("Records", [])
    if not records:
        raise AssertionError("GetRecords: no records returned (expected ≥1 from PutRecord/PutRecords)")
    # Verify data is readable
    _ = base64.b64decode(records[0]["Data"]) if isinstance(records[0]["Data"], str) else records[0]["Data"]


# ── kinesis-shards ────────────────────────────────────────────────────────────

def setup_kinesis_shards(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = f"oc-{ctx.run_id}-shard"
    kin.create_stream(StreamName=name, ShardCount=2)
    _wait_stream_active(kin, name)
    ctx["kinesis_shard_stream"] = name


def teardown_kinesis_shards(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx.get("kinesis_shard_stream")
    if not name:
        return
    try:
        kin.delete_stream(StreamName=name, EnforceConsumerDeletion=True)
    except Exception:
        pass


def ListShards(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_shard_stream"]
    resp = kin.list_shards(StreamName=name)
    shards = resp.get("Shards", [])
    if len(shards) < 1:
        raise AssertionError(f"ListShards: expected ≥1 shard, got {len(shards)}")


def SplitShard(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_shard_stream"]
    # Get first shard
    resp = kin.list_shards(StreamName=name)
    shards = resp.get("Shards", [])
    if not shards:
        raise AssertionError("SplitShard: no shards to split")
    shard = shards[0]
    # Compute midpoint of the hash range
    start = int(shard["HashKeyRange"]["StartingHashKey"])
    end = int(shard["HashKeyRange"]["EndingHashKey"])
    mid = str((start + end) // 2)
    kin.split_shard(StreamName=name, ShardToSplit=shard["ShardId"], NewStartingHashKey=mid)
    _wait_stream_active(kin, name)
    resp2 = kin.list_shards(StreamName=name)
    open_shards = [s for s in resp2.get("Shards", []) if "EndingSequenceNumber" not in s.get("SequenceNumberRange", {})]
    assert len(open_shards) >= 2, f"SplitShard: expected >=2 open shards, got {len(open_shards)}"


def MergeShards(ctx: TestContext) -> None:
    kin = _kin(ctx)
    name = ctx["kinesis_shard_stream"]
    resp = kin.list_shards(StreamName=name)
    open_shards = [
        s for s in resp.get("Shards", [])
        if "EndingSequenceNumber" not in s.get("SequenceNumberRange", {})
    ]
    if len(open_shards) < 2:
        raise AssertionError(f"MergeShards: need >=2 open shards, got {len(open_shards)}")
    kin.merge_shards(
        StreamName=name,
        ShardToMerge=open_shards[0]["ShardId"],
        AdjacentShardToMerge=open_shards[1]["ShardId"],
    )
    _wait_stream_active(kin, name)
    resp2 = kin.list_shards(StreamName=name)
    after_open = [
        s for s in resp2.get("Shards", [])
        if "EndingSequenceNumber" not in s.get("SequenceNumberRange", {})
    ]
    assert len(after_open) < len(open_shards), (
        f"MergeShards: expected fewer open shards, got {len(after_open)}"
    )


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateStream": CreateStream,
    "DescribeStream": DescribeStream,
    "DescribeStreamSummary": DescribeStreamSummary,
    "ListStreams": ListStreams,
    "AddTagsToStream": AddTagsToStream,
    "ListTagsForStream": ListTagsForStream,
    "DeleteStream": DeleteStream,
    "PutRecord": PutRecord,
    "PutRecords": PutRecords,
    "GetShardIterator": GetShardIterator,
    "GetRecords": GetRecords,
    "ListShards": ListShards,
    "SplitShard": SplitShard,
    "MergeShards": MergeShards,
}

SETUP = {
    "kinesis-streams": setup_kinesis_streams,
    "kinesis-records": setup_kinesis_records,
    "kinesis-shards": setup_kinesis_shards,
}

TEARDOWN = {
    "kinesis-streams": teardown_kinesis_streams,
    "kinesis-records": teardown_kinesis_records,
    "kinesis-shards": teardown_kinesis_shards,
}
