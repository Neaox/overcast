---
title: "DynamoDB"
description: "Tables, items, expressions, indexes and transactions, stored in DynamoDB's own wire format. Tables are region-scoped, GSIs are immediately consistent, and PartiQL is out of scope."
section: "Service Reference"
tags:
  - docs
  - dynamodb
  - services
---

# DynamoDB

Full item, expression, index and transaction support, with tables scoped to a
region exactly as on AWS.

**Status:** ✅ Supported

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws dynamodb create-table --table-name orders \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST

aws dynamodb put-item --table-name orders \
  --item '{"id": {"S": "o-1"}, "total": {"N": "42"}}'
aws dynamodb get-item --table-name orders --key '{"id": {"S": "o-1"}}'
```

## What works

| Area | Behaviour |
| --- | --- |
| Items | `PutItem`, `GetItem`, `UpdateItem`, `DeleteItem`, `BatchGetItem` (100), `BatchWriteItem` (25) |
| Expressions | Condition, filter, key-condition, projection and update expressions, with `SET`/`REMOVE`/`ADD`/`DELETE` and every `ReturnValues` variant |
| Query and scan | `Limit` applied before `FilterExpression` as AWS does, `ExclusiveStartKey` pagination, `ScanIndexForward`, `Select=COUNT`, parallel scan by `Segment`/`TotalSegments` |
| Indexes | GSIs and LSIs, including `ALL`/`KEYS_ONLY`/`INCLUDE` projections and GSI create/delete/throughput updates through `UpdateTable` |
| Transactions | `TransactGetItems` and `TransactWriteItems`, all-or-nothing, with `ConditionCheck` |
| Billing modes | An omitted `BillingMode` defaults to `PROVISIONED`, which requires `ProvisionedThroughput` on the table and every GSI; `PAY_PER_REQUEST` rejects it |
| Data types | Every attribute type; items are stored in DynamoDB's JSON wire format, so nothing is lost to a round trip |
| Streams | `StreamSpecification` on create and update — see [DynamoDB Streams](dynamodbstreams.md) |
| Metrics | `SuccessfulRequestLatency`, `ConsumedRead`/`WriteCapacityUnits` and `UserErrors`/`SystemErrors` are recorded to CloudWatch, transactional operations at AWS's 2× weighting |
| Protocols | AWS JSON 1.0 (`X-Amz-Target: DynamoDB_20120810.<Operation>`) and Smithy RPC v2 CBOR |

## Differences from AWS

| Difference | Detail |
| --- | --- |
| GSIs are immediately consistent | An item is visible to a GSI query the instant it is written. `ConsistentRead=true` with an `IndexName` is still rejected, exactly as AWS rejects it, so code cannot come to depend on a read mode AWS will not serve |
| TTL is swept, not lazy | Expired items are deleted by an hourly sweeper rather than being hidden on read, so an expired item can still be returned |
| No PartiQL | `ExecuteStatement`, `ExecuteTransaction` and `BatchExecuteStatement` are out of scope |
| No global tables | Overcast emulates one region per request; the global-table operations answer `501` |
| No backups | Backups, exports, imports, restores, resource policies and contributor insights answer `501` |
| Throughput is not enforced | Provisioned capacity is recorded and reported; nothing is throttled |

Unimplemented-but-modelled operations answer `501 Not Implemented` with
`x-emulator-unsupported: true`, in DynamoDB's own error envelope. An
`X-Amz-Target` naming no AWS operation at all gets `400
UnknownOperationException`.

## Gotchas

> [!IMPORTANT]
> Tables are regional. A table created in `us-east-1` is invisible from
> `eu-west-1` — `ListTables` omits it, `DescribeTable` answers
> `ResourceNotFoundException`, and the same name can exist in both regions with
> entirely separate items, index entries and streams. The region comes from the
> request: the SigV4 credential scope, a regional endpoint hostname, or
> `OVERCAST_DEFAULT_REGION` when the request names none.

<!-- BEGIN overcast:capabilities -->

## Operations

21 of 28 listed operations are implemented.
Per-operation status, notes and AWS API links: [DynamoDB operations](dynamodb/operations.md).

<!-- END overcast:capabilities -->

## Related

- [DynamoDB Streams](dynamodbstreams.md)
- [Storage and persistence](../storage.md#what-survives-a-restart-or-crash) — where table data lives between restarts
- [AWS API reference](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
