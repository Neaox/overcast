"""
groups/dynamodb.py — DynamoDB compatibility test implementations for the Python suite.
"""

from __future__ import annotations
import time
from lib.harness import TestContext
from lib.clients import make_clients

_TABLE_SCHEMA = {
    "KeySchema": [
        {"AttributeName": "pk", "KeyType": "HASH"},
        {"AttributeName": "sk", "KeyType": "RANGE"},
    ],
    "AttributeDefinitions": [
        {"AttributeName": "pk", "AttributeType": "S"},
        {"AttributeName": "sk", "AttributeType": "S"},
    ],
    "BillingMode": "PAY_PER_REQUEST",
}


def _ddb(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region).dynamodb


def _wait_active(ddb, table_name: str, max_wait: int = 10) -> None:
    deadline = time.monotonic() + max_wait
    while time.monotonic() < deadline:
        resp = ddb.describe_table(TableName=table_name)
        if resp["Table"]["TableStatus"] == "ACTIVE":
            return
        time.sleep(0.2)
    raise TimeoutError(f"Table {table_name} did not become ACTIVE in {max_wait}s")


# ── dynamodb-tables ───────────────────────────────────────────────────────────

def setup_dynamodb_tables(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ctx["ddb_table"] = name


def teardown_dynamodb_tables(ctx: TestContext) -> None:
    name = ctx.get("ddb_table")
    if name:
        try:
            _ddb(ctx).delete_table(TableName=name)
        except Exception:
            pass


def CreateTable(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb-create"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ddb.delete_table(TableName=name)


def DescribeTable(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = ctx["ddb_table"]
    resp = ddb.describe_table(TableName=name)
    if resp["Table"]["TableName"] != name:
        raise AssertionError(f"DescribeTable: wrong name {resp['Table']['TableName']!r}")
    if resp["Table"]["TableStatus"] != "ACTIVE":
        raise AssertionError(f"DescribeTable: unexpected status {resp['Table']['TableStatus']!r}")


def ListTables(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = ctx["ddb_table"]
    resp = ddb.list_tables()
    if name not in resp.get("TableNames", []):
        raise AssertionError(f"ListTables: {name!r} not found in table list")


def UpdateTable(ctx: TestContext) -> None:
    # Overcast may not support all UpdateTable operations; DeletionProtection toggle is simple.
    ddb = _ddb(ctx)
    name = ctx["ddb_table"]
    ddb.update_table(TableName=name, DeletionProtectionEnabled=False)
    resp = ddb.describe_table(TableName=name)
    if resp["Table"]["TableName"] != name:
        raise AssertionError(f"UpdateTable: table {name!r} not found after update")


def DeleteTable(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb-del"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ddb.delete_table(TableName=name)
    resp = ddb.list_tables()
    if name in resp.get("TableNames", []):
        raise AssertionError(f"DeleteTable: {name!r} still listed after deletion")


# ── dynamodb-items ────────────────────────────────────────────────────────────

def setup_dynamodb_items(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb-items"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ctx["ddb_items_table"] = name


def teardown_dynamodb_items(ctx: TestContext) -> None:
    name = ctx.get("ddb_items_table")
    if name:
        try:
            _ddb(ctx).delete_table(TableName=name)
        except Exception:
            pass


def PutItem(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_items_table"]
    ddb.put_item(
        TableName=table,
        Item={"pk": {"S": "user#1"}, "sk": {"S": "profile"}, "name": {"S": "Alice"}},
    )
    resp = ddb.get_item(
        TableName=table,
        Key={"pk": {"S": "user#1"}, "sk": {"S": "profile"}},
    )
    assert resp.get("Item", {}).get("name", {}).get("S") == "Alice", "PutItem: name!=Alice"


def GetItem(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_items_table"]
    resp = ddb.get_item(
        TableName=table,
        Key={"pk": {"S": "user#1"}, "sk": {"S": "profile"}},
    )
    item = resp.get("Item", {})
    if item.get("name", {}).get("S") != "Alice":
        raise AssertionError(f"GetItem: expected name=Alice, got {item}")


def UpdateItem(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_items_table"]
    ddb.update_item(
        TableName=table,
        Key={"pk": {"S": "user#1"}, "sk": {"S": "profile"}},
        UpdateExpression="SET #n = :n",
        ExpressionAttributeNames={"#n": "name"},
        ExpressionAttributeValues={":n": {"S": "Bob"}},
    )
    resp = ddb.get_item(
        TableName=table,
        Key={"pk": {"S": "user#1"}, "sk": {"S": "profile"}},
    )
    if resp["Item"]["name"]["S"] != "Bob":
        raise AssertionError(f"UpdateItem: expected name=Bob, got {resp['Item']}")


def PutItemConditionFail(ctx: TestContext) -> None:
    import botocore.exceptions
    ddb = _ddb(ctx)
    table = ctx["ddb_items_table"]
    try:
        ddb.put_item(
            TableName=table,
            Item={"pk": {"S": "user#1"}, "sk": {"S": "profile"}, "name": {"S": "Charlie"}},
            ConditionExpression="attribute_not_exists(pk)",
        )
        raise AssertionError("PutItemConditionFail: expected ConditionalCheckFailedException")
    except botocore.exceptions.ClientError as exc:
        if exc.response["Error"]["Code"] != "ConditionalCheckFailedException":
            raise


def DeleteItem(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_items_table"]
    ddb.delete_item(
        TableName=table,
        Key={"pk": {"S": "user#1"}, "sk": {"S": "profile"}},
    )
    resp = ddb.get_item(
        TableName=table,
        Key={"pk": {"S": "user#1"}, "sk": {"S": "profile"}},
    )
    if resp.get("Item"):
        raise AssertionError("DeleteItem: item still present after delete")


# ── dynamodb-query ────────────────────────────────────────────────────────────

def setup_dynamodb_query(ctx: TestContext) -> None:
    from boto3.dynamodb.conditions import Key as DKey
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb-q"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ctx["ddb_q_table"] = name
    # seed 5 items
    for i in range(5):
        ddb.put_item(
            TableName=name,
            Item={
                "pk": {"S": "part#1"},
                "sk": {"S": f"item#{i:03d}"},
                "val": {"N": str(i * 10)},
                "tag": {"S": "even" if i % 2 == 0 else "odd"},
            },
        )


def teardown_dynamodb_query(ctx: TestContext) -> None:
    name = ctx.get("ddb_q_table")
    if name:
        try:
            _ddb(ctx).delete_table(TableName=name)
        except Exception:
            pass


def Query(ctx: TestContext) -> None:
    from boto3.dynamodb.conditions import Key as DKey
    ddb = _ddb(ctx)
    table = ctx["ddb_q_table"]
    resp = ddb.query(
        TableName=table,
        KeyConditionExpression="pk = :pk",
        ExpressionAttributeValues={":pk": {"S": "part#1"}},
    )
    if resp.get("Count", 0) < 5:
        raise AssertionError(f"Query: expected ≥5 items, got {resp.get('Count')}")


def QueryWithFilterExpression(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_q_table"]
    resp = ddb.query(
        TableName=table,
        KeyConditionExpression="pk = :pk",
        FilterExpression="#t = :t",
        ExpressionAttributeNames={"#t": "tag"},
        ExpressionAttributeValues={":pk": {"S": "part#1"}, ":t": {"S": "even"}},
    )
    for item in resp.get("Items", []):
        if item["tag"]["S"] != "even":
            raise AssertionError(f"QueryWithFilterExpression: unexpected item {item}")


def QueryWithLimit(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_q_table"]
    resp = ddb.query(
        TableName=table,
        KeyConditionExpression="pk = :pk",
        ExpressionAttributeValues={":pk": {"S": "part#1"}},
        Limit=2,
    )
    if resp.get("Count", 0) > 2:
        raise AssertionError(f"QueryWithLimit: expected ≤2 items, got {resp.get('Count')}")


def QueryPagination(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_q_table"]
    resp1 = ddb.query(
        TableName=table,
        KeyConditionExpression="pk = :pk",
        ExpressionAttributeValues={":pk": {"S": "part#1"}},
        Limit=2,
    )
    if not resp1.get("LastEvaluatedKey"):
        raise AssertionError("QueryPagination: no LastEvaluatedKey in first page")
    resp2 = ddb.query(
        TableName=table,
        KeyConditionExpression="pk = :pk",
        ExpressionAttributeValues={":pk": {"S": "part#1"}},
        ExclusiveStartKey=resp1["LastEvaluatedKey"],
    )
    if resp2.get("Count", 0) == 0:
        raise AssertionError("QueryPagination: second page empty")


def Scan(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_q_table"]
    resp = ddb.scan(TableName=table)
    if resp.get("Count", 0) < 5:
        raise AssertionError(f"Scan: expected ≥5 items, got {resp.get('Count')}")


def ScanWithFilter(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_q_table"]
    resp = ddb.scan(
        TableName=table,
        FilterExpression="#t = :t",
        ExpressionAttributeNames={"#t": "tag"},
        ExpressionAttributeValues={":t": {"S": "odd"}},
    )
    for item in resp.get("Items", []):
        if item["tag"]["S"] != "odd":
            raise AssertionError(f"ScanWithFilter: unexpected item {item}")


# ── dynamodb-batch ────────────────────────────────────────────────────────────

def setup_dynamodb_batch(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb-batch"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ctx["ddb_batch_table"] = name


def teardown_dynamodb_batch(ctx: TestContext) -> None:
    name = ctx.get("ddb_batch_table")
    if name:
        try:
            _ddb(ctx).delete_table(TableName=name)
        except Exception:
            pass


def BatchWriteItem(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_batch_table"]
    requests = [
        {"PutRequest": {"Item": {"pk": {"S": f"batch#{i}"}, "sk": {"S": "v0"}, "n": {"N": str(i)}}}}
        for i in range(3)
    ]
    resp = ddb.batch_write_item(RequestItems={table: requests})
    if resp.get("UnprocessedItems"):
        raise AssertionError(f"BatchWriteItem: unprocessed items {resp['UnprocessedItems']}")


def BatchGetItem(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_batch_table"]
    keys = [{"pk": {"S": f"batch#{i}"}, "sk": {"S": "v0"}} for i in range(3)]
    resp = ddb.batch_get_item(RequestItems={table: {"Keys": keys}})
    items = resp.get("Responses", {}).get(table, [])
    if len(items) < 3:
        raise AssertionError(f"BatchGetItem: expected 3 items, got {len(items)}")


# ── dynamodb-txn ──────────────────────────────────────────────────────────────

def setup_dynamodb_txn(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb-txn"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ctx["ddb_txn_table"] = name


def teardown_dynamodb_txn(ctx: TestContext) -> None:
    name = ctx.get("ddb_txn_table")
    if name:
        try:
            _ddb(ctx).delete_table(TableName=name)
        except Exception:
            pass


def TransactWriteItems(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_txn_table"]
    ddb.transact_write_items(
        TransactItems=[
            {"Put": {"TableName": table, "Item": {"pk": {"S": "txn#1"}, "sk": {"S": "a"}, "v": {"S": "one"}}}},
            {"Put": {"TableName": table, "Item": {"pk": {"S": "txn#2"}, "sk": {"S": "a"}, "v": {"S": "two"}}}},
        ]
    )
    resp = ddb.get_item(TableName=table, Key={"pk": {"S": "txn#1"}, "sk": {"S": "a"}})
    assert resp.get("Item", {}).get("v", {}).get("S") == "one", "TransactWriteItems: txn#1 not found"


def TransactGetItems(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_txn_table"]
    resp = ddb.transact_get_items(
        TransactItems=[
            {"Get": {"TableName": table, "Key": {"pk": {"S": "txn#1"}, "sk": {"S": "a"}}}},
            {"Get": {"TableName": table, "Key": {"pk": {"S": "txn#2"}, "sk": {"S": "a"}}}},
        ]
    )
    items = [r.get("Item") for r in resp.get("Responses", [])]
    if not all(items):
        raise AssertionError(f"TransactGetItems: some items missing: {items}")


def TransactWriteConditionFail(ctx: TestContext) -> None:
    import botocore.exceptions
    ddb = _ddb(ctx)
    table = ctx["ddb_txn_table"]
    try:
        ddb.transact_write_items(
            TransactItems=[{
                "Put": {
                    "TableName": table,
                    "Item": {"pk": {"S": "txn#1"}, "sk": {"S": "a"}, "v": {"S": "fail"}},
                    "ConditionExpression": "attribute_not_exists(pk)",
                },
            }]
        )
        raise AssertionError("TransactWriteConditionFail: expected TransactionCanceledException")
    except botocore.exceptions.ClientError as exc:
        if exc.response["Error"]["Code"] != "TransactionCanceledException":
            raise


# ── dynamodb-ttl ──────────────────────────────────────────────────────────────

def setup_dynamodb_ttl(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    name = f"{ctx.run_id}-ddb-ttl"
    ddb.create_table(TableName=name, **_TABLE_SCHEMA)
    _wait_active(ddb, name)
    ctx["ddb_ttl_table"] = name


def teardown_dynamodb_ttl(ctx: TestContext) -> None:
    name = ctx.get("ddb_ttl_table")
    if name:
        try:
            _ddb(ctx).delete_table(TableName=name)
        except Exception:
            pass


def UpdateTimeToLive(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_ttl_table"]
    ddb.update_time_to_live(
        TableName=table,
        TimeToLiveSpecification={"Enabled": True, "AttributeName": "ttl"},
    )
    resp = ddb.describe_time_to_live(TableName=table)
    status = resp.get("TimeToLiveDescription", {}).get("TimeToLiveStatus")
    assert status in ("ENABLED", "ENABLING"), f"UpdateTimeToLive: expected ENABLED, got {status}"


def DescribeTimeToLive(ctx: TestContext) -> None:
    ddb = _ddb(ctx)
    table = ctx["ddb_ttl_table"]
    resp = ddb.describe_time_to_live(TableName=table)
    spec = resp.get("TimeToLiveDescription", {})
    if spec.get("TimeToLiveStatus") not in ("ENABLED", "ENABLING"):
        raise AssertionError(f"DescribeTimeToLive: unexpected status {spec}")


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateTable": CreateTable,
    "DescribeTable": DescribeTable,
    "ListTables": ListTables,
    "UpdateTable": UpdateTable,
    "DeleteTable": DeleteTable,
    "PutItem": PutItem,
    "GetItem": GetItem,
    "UpdateItem": UpdateItem,
    "PutItemConditionFail": PutItemConditionFail,
    "DeleteItem": DeleteItem,
    "Query": Query,
    "QueryWithFilterExpression": QueryWithFilterExpression,
    "QueryWithLimit": QueryWithLimit,
    "QueryPagination": QueryPagination,
    "Scan": Scan,
    "ScanWithFilter": ScanWithFilter,
    "BatchWriteItem": BatchWriteItem,
    "BatchGetItem": BatchGetItem,
    "TransactWriteItems": TransactWriteItems,
    "TransactGetItems": TransactGetItems,
    "TransactWriteConditionFail": TransactWriteConditionFail,
    "UpdateTimeToLive": UpdateTimeToLive,
    "DescribeTimeToLive": DescribeTimeToLive,
}

SETUP = {
    "dynamodb-tables": setup_dynamodb_tables,
    "dynamodb-items": setup_dynamodb_items,
    "dynamodb-query": setup_dynamodb_query,
    "dynamodb-batch": setup_dynamodb_batch,
    "dynamodb-txn": setup_dynamodb_txn,
    "dynamodb-ttl": setup_dynamodb_ttl,
}

TEARDOWN = {
    "dynamodb-tables": teardown_dynamodb_tables,
    "dynamodb-items": teardown_dynamodb_items,
    "dynamodb-query": teardown_dynamodb_query,
    "dynamodb-batch": teardown_dynamodb_batch,
    "dynamodb-txn": teardown_dynamodb_txn,
    "dynamodb-ttl": teardown_dynamodb_ttl,
}
