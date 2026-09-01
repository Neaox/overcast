---
title: "DynamoDB Streams"
description: "DynamoDB Streams accepts the AWS JSON 1.0 API over the shared root endpoint with X-Amz-Target: DynamoDBStreams_20120810.\u003cOperation\u003e. It also accepts Smithy RPC v2 CBOR at..."
section: "Service Reference"
tags:
  - docs
  - dynamodb
  - dynamodbstreams
  - services
  - streams
---

# DynamoDB Streams

DynamoDB Streams accepts the AWS JSON 1.0 API over the shared root endpoint
with `X-Amz-Target: DynamoDBStreams_20120810.<Operation>`. It also accepts
Smithy RPC v2 CBOR at `/service/DynamoDBStreams/operation/<Operation>` with
`Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.

---

## Streams are region-scoped

A table's stream belongs to the table's region, and
[DynamoDB tables are region-scoped](./dynamodb.md). Two same-named tables in
different regions therefore have two distinct stream ARNs and two entirely
separate record sets, with independent `TRIM_HORIZON` and `LATEST` positions —
a write in one region never appears in, or advances, the other's stream.

Consequences, all matching AWS's regional endpoints:

- `ListStreams` reports only streams for tables in the request's region.
- `DescribeStream` and `GetShardIterator` answer `ResourceNotFoundException` for
  a stream ARN belonging to another region.
- A shard iterator names the region it was issued for, so `GetRecords` always
  pages the stream it was handed rather than resolving the table name afresh.
  An iterator issued before this behaviour existed still works, resolving
  against the request's region as it used to.
- Every record carries `awsRegion`, as AWS's do.

Stream consumers match on region too: a Lambda event source mapping or an
EventBridge pipe whose source ARN names one region's stream is not triggered by
writes to a same-named table in another region.

---

<!-- BEGIN overcast:capabilities -->

## Operations

All 4 listed operations are implemented.
Per-operation status, notes and AWS API links: [DynamoDB Streams operations](dynamodbstreams/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Operations_Amazon_DynamoDB_Streams.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
