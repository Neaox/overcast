# DynamoDB

> AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/Welcome.html

DynamoDB uses a JSON API. Operations are identified by the
`X-Amz-Target` header (e.g. `DynamoDB_20120810.PutItem`).

All data types are supported in the request/response format. The emulator
stores items in their DynamoDB JSON wire format internally to avoid
serialisation round-trip issues.

---

## Summary

| Category | ✅ Supported | ⚠️ Partial | 🚧 WIP | ❌ Unsupported |
|----------|------------|-----------|--------|--------------|
| Table management | 3 | 0 | 0 | 4 |
| Item operations | 3 | 0 | 0 | 4 |
| Query & scan | 2 | 0 | 0 | 2 |
| Indexes (GSI/LSI) | 0 | 0 | 0 | 3 |
| Transactions | 0 | 0 | 0 | 4 |
| Streams | 0 | 0 | 0 | 4 |
| Backups | 0 | 0 | 0 | 4 |

---

## Endpoints

### Table management

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `CreateTable` | ✅ Supported | Includes GSI/LSI definitions | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html) |
| `DeleteTable` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteTable.html) |
| `DescribeTable` | ✅ Supported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTable.html) |
| `ListTables` | ✅ Supported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListTables.html) |
| `UpdateTable` | ❌ Unsupported | GSI additions, billing mode changes | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTable.html) |
| `DescribeTimeToLive` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeTimeToLive.html) |
| `UpdateTimeToLive` | ❌ Unsupported | TTL-based item expiry | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateTimeToLive.html) |

### Item operations

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `PutItem` | ✅ Supported | Includes `ConditionExpression` support | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_PutItem.html) |
| `GetItem` | ✅ Supported | Includes `ProjectionExpression` | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetItem.html) |
| `UpdateItem` | ❌ Unsupported | `UpdateExpression` (SET, REMOVE, ADD, DELETE) | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateItem.html) |
| `DeleteItem` | ✅ Supported | Includes `ConditionExpression` | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteItem.html) |
| `BatchGetItem` | ❌ Unsupported | Up to 100 items across tables | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchGetItem.html) |
| `BatchWriteItem` | ❌ Unsupported | Up to 25 put/delete operations | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_BatchWriteItem.html) |
| `ExecuteStatement` (PartiQL) | ❌ Unsupported | PartiQL is out of scope for v1 | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ExecuteStatement.html) |

### Query & scan

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `Query` | ✅ Supported | `KeyConditionExpression`, `FilterExpression`, pagination | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html) |
| `Scan` | ✅ Supported | Full table scan with `FilterExpression` | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html) |
| `Query` on GSI | ❌ Unsupported | Requires GSI to be created and maintained | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/GSI.html) |
| Parallel scan (`Segment` / `TotalSegments`) | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Scan.html#Scan.ParallelScan) |

### Indexes (GSI / LSI)

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| GSI (Global Secondary Index) | ❌ Unsupported | Requires index maintenance on write | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/GSI.html) |
| LSI (Local Secondary Index) | ❌ Unsupported | Created at table creation time | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/LSI.html) |
| `DescribeGlobalTable` | ❌ Unsupported | Multi-region replication not applicable | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeGlobalTable.html) |

### Transactions

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `TransactGetItems` | ❌ Unsupported | Up to 25 items | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactGetItems.html) |
| `TransactWriteItems` | ❌ Unsupported | Put, Update, Delete, ConditionCheck | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html) |
| `ExecuteTransaction` (PartiQL) | ❌ Unsupported | PartiQL is out of scope for v1 | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ExecuteTransaction.html) |
| Optimistic locking (`ConditionExpression`) | ❌ Unsupported | Required for correct transaction semantics | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/DynamoDBMapper.OptimisticLocking.html) |

### Streams

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `DescribeStream` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_DescribeStream.html) |
| `GetRecords` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetRecords.html) |
| `GetShardIterator` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_GetShardIterator.html) |
| `ListStreams` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_streams_ListStreams.html) |

### Backups

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `CreateBackup` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateBackup.html) |
| `DeleteBackup` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DeleteBackup.html) |
| `DescribeBackup` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeBackup.html) |
| `RestoreTableFromBackup` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_RestoreTableFromBackup.html) |

---

## Known limitations

- Expression parsing (`ConditionExpression`, `FilterExpression`, `UpdateExpression`,
  `ProjectionExpression`) is complex. A full expression evaluator is required for
  correct behaviour — this is one of the highest-effort parts of DynamoDB emulation.
- GSI consistency: real DynamoDB GSIs are eventually consistent. The emulator
  may choose to make them immediately consistent for simplicity — document the
  divergence clearly when implemented.
- TTL expiry is not enforced in real-time. Items with expired TTL are removed
  lazily on next read (or on a background sweep).
- PartiQL (`ExecuteStatement`, `ExecuteTransaction`, `BatchExecuteStatement`)
  is explicitly out of scope for v1.
