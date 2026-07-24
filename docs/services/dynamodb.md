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

- **GSI consistency**: real DynamoDB GSIs are eventually consistent; the emulator is immediately consistent — items are visible in GSI queries the instant they are written.
- **TTL expiry** is not enforced in real-time. Items with expired TTL are removed by a background sweeper (runs hourly), not lazily on read.
- **PartiQL** (`ExecuteStatement`, `ExecuteTransaction`, `BatchExecuteStatement`) is explicitly out of scope for v1.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                 | ✅ Supported | ❌ Unsupported |
| ------------------------ | ------------ | -------------- |
| Table management         | 7            | 1              |
| Item operations          | 6            |                |
| Query & scan             | 2            |                |
| Transactions             | 2            |                |
| Streams interoperability | 1            |                |

---

## Endpoints

### Table management

| Operation                | Status         | Notes                                                                                                              | AWS Docs                                                                                               |
| ------------------------ | -------------- | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------ |
| `CreateTable`            | ✅ Supported   | Includes GSI/LSI definitions                                                                                       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html)            |
| `DeleteTable`            | ✅ Supported   |                                                                                                                    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteTable.html)            |
| `DescribeTable`          | ✅ Supported   |                                                                                                                    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTable.html)          |
| `ListTables`             | ✅ Supported   | `Limit`/`ExclusiveStartTableName` currently ignored — always returns all tables in one response ([pagination-plan](../pagination-plan.md)) | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTables.html)             |
| `UpdateTable`            | ✅ Supported   | BillingMode, ProvisionedThroughput, GSI create/delete/update-throughput, AttributeDefinitions, StreamSpecification | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTable.html)            |
| `DescribeTimeToLive`     | ✅ Supported   |                                                                                                                    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTimeToLive.html)     |
| `UpdateTimeToLive`       | ✅ Supported   | TTL-based item expiry; sweeper deletes expired items hourly                                                        | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTimeToLive.html)       |
| `RestoreTableFromBackup` | ❌ Unsupported |                                                                                                                    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_RestoreTableFromBackup.html) |

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

| Operation | Status       | Notes                                                                                                                                                                                              | AWS Docs                                                                              |
| --------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `Query`   | ✅ Supported | `KeyConditionExpression`, `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, `ScanIndexForward`, `Select=COUNT` | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html) |
| `Scan`    | ✅ Supported | `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, parallel scan (`Segment`/`TotalSegments`), `Select=COUNT`    | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html)  |

### Transactions

| Operation            | Status       | Notes                                               | AWS Docs                                                                                           |
| -------------------- | ------------ | --------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `TransactGetItems`   | ✅ Supported | Up to 100 items across tables                       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactGetItems.html)   |
| `TransactWriteItems` | ✅ Supported | Put, Update, Delete, ConditionCheck; all-or-nothing | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html) |

### Streams interoperability

| Operation          | Status       | Notes                                          | AWS Docs                                                                                                 |
| ------------------ | ------------ | ---------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `GetShardIterator` | ✅ Supported | TRIM_HORIZON, LATEST, AT/AFTER_SEQUENCE_NUMBER | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetShardIterator.html) |

<!-- END overcast:capabilities -->
