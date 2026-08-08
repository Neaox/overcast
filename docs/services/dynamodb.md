---
title: "DynamoDB"
description: "DynamoDB accepts AWS JSON 1.0 and Smithy RPC v2 CBOR. JSON operations are identified by the X-Amz-Target header (e.g. DynamoDB_20120810.PutItem); CBOR operations use..."
section: "Service Reference"
tags:
  - docs
  - dynamodb
  - services
---

# DynamoDB

> AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/Welcome.html

DynamoDB accepts AWS JSON 1.0 and Smithy RPC v2 CBOR. JSON operations are
identified by the `X-Amz-Target` header (e.g. `DynamoDB_20120810.PutItem`);
CBOR operations use `/service/DynamoDB/operation/<Operation>` with
`Smithy-Protocol: rpc-v2-cbor`.

All data types are supported in the request/response format. The emulator
stores items in their DynamoDB JSON wire format internally to avoid
serialisation round-trip issues.

---

## Known limitations

- **GSI consistency**: real DynamoDB GSIs are eventually consistent; the emulator is immediately consistent — items are visible in GSI queries the instant they are written. Asking for a strongly consistent read on a GSI (`ConsistentRead=true` with a GSI `IndexName`) is still rejected with a `ValidationException`, exactly as AWS does, so code written against the emulator cannot come to depend on a read mode AWS has no way to serve.
- **TTL expiry** is not enforced in real-time. Items with expired TTL are removed by a background sweeper (runs hourly), not lazily on read.
- **PartiQL** (`ExecuteStatement`, `ExecuteTransaction`, `BatchExecuteStatement`) is explicitly out of scope for v1.
- **Every other modeled DynamoDB operation** — global tables, backups, exports and imports, resource policies, contributor insights, PartiQL — answers `501 Not Implemented` with `x-emulator-unsupported: true`, in DynamoDB's own AWS JSON 1.0 error envelope. Only an `X-Amz-Target` naming no AWS operation at all gets `400 UnknownOperationException`. The endpoint tables below name the global-table operations explicitly; the rest follow the same rule without being listed one by one.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                 | ✅ Supported | ❌ Unsupported |
| ------------------------ | ------------ | -------------- |
| Table management         | 7            | 1              |
| Item operations          | 6            |                |
| Query & scan             | 2            |                |
| Transactions             | 2            |                |
| Tags                     | 3            |                |
| Streams interoperability | 1            |                |
| Global tables            |              | 6              |

---

## Endpoints

### Table management

| Operation                | Status         | Notes                                                                                                                                                                                      | AWS Docs                                                                                               |
| ------------------------ | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------ |
| `CreateTable`            | ✅ Supported   | Includes GSI/LSI definitions; an omitted `BillingMode` defaults to `PROVISIONED`, which requires `ProvisionedThroughput` on the table and on every GSI, while `PAY_PER_REQUEST` rejects it | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html)            |
| `DeleteTable`            | ✅ Supported   |                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteTable.html)            |
| `DescribeTable`          | ✅ Supported   |                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTable.html)          |
| `ListTables`             | ✅ Supported   | Limit (default/max 100) and ExclusiveStartTableName honored; LastEvaluatedTableName echoed when more tables remain                                                                         | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTables.html)             |
| `UpdateTable`            | ✅ Supported   | BillingMode, ProvisionedThroughput, GSI create/delete/update-throughput, AttributeDefinitions, StreamSpecification                                                                         | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTable.html)            |
| `DescribeTimeToLive`     | ✅ Supported   |                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTimeToLive.html)     |
| `UpdateTimeToLive`       | ✅ Supported   | TTL-based item expiry; sweeper deletes expired items hourly                                                                                                                                | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTimeToLive.html)       |
| `RestoreTableFromBackup` | ❌ Unsupported |                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_RestoreTableFromBackup.html) |

### Item operations

| Operation        | Status       | Notes                                                              | AWS Docs                                                                                       |
| ---------------- | ------------ | ------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `PutItem`        | ✅ Supported | Includes `ConditionExpression`, `ReturnValues` (`ALL_OLD`)         | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_PutItem.html)        |
| `GetItem`        | ✅ Supported | Includes `ProjectionExpression`                                    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetItem.html)        |
| `UpdateItem`     | ✅ Supported | SET/REMOVE/ADD/DELETE clauses; all `ReturnValues` variants; upsert | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateItem.html)     |
| `DeleteItem`     | ✅ Supported | `ConditionExpression`, `ReturnValues` (`ALL_OLD`)                  | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteItem.html)     |
| `BatchGetItem`   | ✅ Supported | Up to 100 items across tables                                      | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchGetItem.html)   |
| `BatchWriteItem` | ✅ Supported | Up to 25 put/delete operations                                     | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchWriteItem.html) |

### Query & scan

| Operation | Status       | Notes                                                                                                                                                                                                                                                      | AWS Docs                                                                              |
| --------- | ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `Query`   | ✅ Supported | `KeyConditionExpression`, `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, `ScanIndexForward`, `Select=COUNT`; `ConsistentRead=true` on a GSI is rejected as AWS does | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html) |
| `Scan`    | ✅ Supported | `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, parallel scan (`Segment`/`TotalSegments`), `Select=COUNT`; `ConsistentRead=true` on a GSI is rejected as AWS does    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html)  |

### Transactions

| Operation            | Status       | Notes                                               | AWS Docs                                                                                           |
| -------------------- | ------------ | --------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `TransactGetItems`   | ✅ Supported | Up to 100 items across tables                       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactGetItems.html)   |
| `TransactWriteItems` | ✅ Supported | Put, Update, Delete, ConditionCheck; all-or-nothing | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html) |

### Tags

| Operation            | Status       | Notes                                              | AWS Docs                                                                                           |
| -------------------- | ------------ | -------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `TagResource`        | ✅ Supported | Merges tags; max 50; validates key/value lengths   | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TagResource.html)        |
| `ListTagsOfResource` | ✅ Supported | Returns tags as Key/Value array                    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTagsOfResource.html) |
| `UntagResource`      | ✅ Supported | Removes specified keys; idempotent on missing keys | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UntagResource.html)      |

### Streams interoperability

| Operation          | Status       | Notes                                          | AWS Docs                                                                                                 |
| ------------------ | ------------ | ---------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `GetShardIterator` | ✅ Supported | TRIM_HORIZON, LATEST, AT/AFTER_SEQUENCE_NUMBER | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetShardIterator.html) |

### Global tables

| Operation                     | Status         | Notes                             | AWS Docs                                                                                                    |
| ----------------------------- | -------------- | --------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `CreateGlobalTable`           | ❌ Unsupported | Overcast emulates a single region | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateGlobalTable.html)           |
| `DescribeGlobalTable`         | ❌ Unsupported | Overcast emulates a single region | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeGlobalTable.html)         |
| `DescribeGlobalTableSettings` | ❌ Unsupported | Overcast emulates a single region | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeGlobalTableSettings.html) |
| `ListGlobalTables`            | ❌ Unsupported | Overcast emulates a single region | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListGlobalTables.html)            |
| `UpdateGlobalTable`           | ❌ Unsupported | Overcast emulates a single region | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateGlobalTable.html)           |
| `UpdateGlobalTableSettings`   | ❌ Unsupported | Overcast emulates a single region | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateGlobalTableSettings.html)   |

<!-- END overcast:capabilities -->
