---
title: "DynamoDB operations"
description: "Every DynamoDB operation Overcast declares — 21 of 28 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - dynamodb
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# DynamoDB operations

21 of 28 listed operations are implemented. Back to [DynamoDB](../dynamodb.md).

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
| `ListTables`             | ✅ Supported   | Region-scoped — lists only tables in the request's region; Limit (default/max 100) and ExclusiveStartTableName honored; LastEvaluatedTableName echoed when more tables remain              | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTables.html)             |
| `UpdateTable`            | ✅ Supported   | BillingMode, ProvisionedThroughput, GSI create/delete/update-throughput, AttributeDefinitions, StreamSpecification                                                                         | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTable.html)            |
| `DescribeTimeToLive`     | ✅ Supported   |                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTimeToLive.html)     |
| `UpdateTimeToLive`       | ✅ Supported   | TTL-based item expiry; sweeper deletes expired items hourly                                                                                                                                | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTimeToLive.html)       |
| `RestoreTableFromBackup` | ❌ Unsupported |                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_RestoreTableFromBackup.html) |

### Item operations

| Operation        | Status       | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                   | AWS Docs                                                                                       |
| ---------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `PutItem`        | ✅ Supported | Includes `ConditionExpression`, `ReturnValues` (`ALL_OLD`); key attributes must be present and carry their declared `AttributeDefinitions` type, while non-key attributes stay schemaless; records AWS/DynamoDB CloudWatch metrics SuccessfulRequestLatency, ConsumedWriteCapacityUnits, UserErrors/SystemErrors; a reserved word used as a bare attribute name in an expression is rejected, as AWS does — reach it through `ExpressionAttributeNames` | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_PutItem.html)        |
| `GetItem`        | ✅ Supported | Includes `ProjectionExpression`; a `Key` must name exactly the key schema's attributes with their declared types; records SuccessfulRequestLatency, ConsumedReadCapacityUnits, UserErrors/SystemErrors; a reserved word used as a bare attribute name in an expression is rejected, as AWS does — reach it through `ExpressionAttributeNames`                                                                                                           | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetItem.html)        |
| `UpdateItem`     | ✅ Supported | SET/REMOVE/ADD/DELETE clauses; all `ReturnValues` variants; upsert; a `Key` must name exactly the key schema's attributes with their declared types; records SuccessfulRequestLatency, ConsumedWriteCapacityUnits, UserErrors/SystemErrors; a reserved word used as a bare attribute name in an expression is rejected, as AWS does — reach it through `ExpressionAttributeNames`                                                                       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateItem.html)     |
| `DeleteItem`     | ✅ Supported | `ConditionExpression`, `ReturnValues` (`ALL_OLD`); a `Key` must name exactly the key schema's attributes with their declared types; records SuccessfulRequestLatency, ConsumedWriteCapacityUnits, UserErrors/SystemErrors; a reserved word used as a bare attribute name in an expression is rejected, as AWS does — reach it through `ExpressionAttributeNames`                                                                                        | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteItem.html)     |
| `BatchGetItem`   | ✅ Supported | Up to 100 items across tables; every key is checked against its table's key schema before any read; records SuccessfulRequestLatency/ConsumedReadCapacityUnits per table touched                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchGetItem.html)   |
| `BatchWriteItem` | ✅ Supported | Up to 25 put/delete operations; every item and key is checked against its table's key schema before any write is applied; records SuccessfulRequestLatency/ConsumedWriteCapacityUnits per table touched                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchWriteItem.html) |

### Query & scan

| Operation | Status       | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | AWS Docs                                                                              |
| --------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `Query`   | ✅ Supported | `KeyConditionExpression` (values type-checked against the declared key types), `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, `ScanIndexForward`, `Select=COUNT`; `ConsistentRead=true` on a GSI is rejected as AWS does; records SuccessfulRequestLatency and ConsumedReadCapacityUnits (with GlobalSecondaryIndexName when IndexName names a real GSI); a reserved word used as a bare attribute name in an expression is rejected, as AWS does — reach it through `ExpressionAttributeNames` | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html) |
| `Scan`    | ✅ Supported | `FilterExpression`, `Limit` (applied before `FilterExpression` per AWS semantics), `ExclusiveStartKey`/`LastEvaluatedKey` pagination, parallel scan (`Segment`/`TotalSegments`), `Select=COUNT`; `ConsistentRead=true` on a GSI is rejected as AWS does; records SuccessfulRequestLatency and ConsumedReadCapacityUnits (with GlobalSecondaryIndexName when IndexName names a real GSI); a reserved word used as a bare attribute name in an expression is rejected, as AWS does — reach it through `ExpressionAttributeNames`                                                         | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html)  |

### Transactions

| Operation            | Status       | Notes                                                                                                                                                                                                                                                                                                           | AWS Docs                                                                                           |
| -------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `TransactGetItems`   | ✅ Supported | Up to 100 items across tables; every key is checked against its table's key schema; records SuccessfulRequestLatency/ConsumedReadCapacityUnits per table (2x weighted, matching AWS's transactional-read capacity accounting)                                                                                   | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactGetItems.html)   |
| `TransactWriteItems` | ✅ Supported | Put, Update, Delete, ConditionCheck; all-or-nothing; a key-schema mismatch is a `ValidationException` raised before anything is applied, not a cancellation reason; records SuccessfulRequestLatency/ConsumedWriteCapacityUnits per table (2x weighted, matching AWS's transactional-write capacity accounting) | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html) |

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

## Related

- [DynamoDB](../dynamodb.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
