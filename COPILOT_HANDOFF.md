# Copilot Handoff: DynamoDB P1 Implementation

## What was done (completed)

1. **Refactored S3 and SQS handlers** to use `serviceutil` package (`DecodeJSON`, `RequireString`, `ServiceLogger`, etc.)
2. **Added `ServiceLogger`** to S3 and SQS services
3. **Fixed state error handling** to use `protocol.Wrap`
4. Project **builds clean** (`go build ./...`) and all existing tests pass:
   - Unit tests: `internal/config`, `internal/protocol`, `internal/serviceutil`, `internal/state` — all pass
   - Integration tests: S3 and SQS — all pass

## What needs to be done: DynamoDB P1

**The #1 priority is making the failing DynamoDB integration tests pass.**

Tests are already written at `tests/integration/dynamodb/dynamodb_test.go` and currently all fail with 501 (expected).

### Files to create

1. **`internal/services/dynamodb/store.go`** — Domain types + state helpers
2. **`internal/services/dynamodb/handler.go`** — HTTP handlers

### File to modify

3. **`internal/services/dynamodb/service.go`** — Wire handlers into dispatch switch, add logger

### Implementation order (follow the test file order)

| # | Operation | Test(s) | Notes |
|---|-----------|---------|-------|
| 1 | `CreateTable` | `TestCreateTable_success`, `TestCreateTable_duplicate` | Returns `TableDescription` with `TableName` + `TableStatus: "ACTIVE"`. Duplicate → 400 `ResourceInUseException` |
| 2 | `DescribeTable` | `TestDescribeTable_success`, `TestDescribeTable_notFound` | Not found → 400 `ResourceNotFoundException` |
| 3 | `PutItem` | `TestPutItem_success`, `TestPutItem_tableNotFound` | Items use DynamoDB JSON format: `{"S": "value"}`, `{"N": "123"}` |
| 4 | `GetItem` | `TestGetItem_success`, `TestGetItem_notFound` | Missing key → 200 with empty `Item` (NOT an error) |
| 5 | `DeleteItem` | `TestDeleteItem_success` | Idempotent — deleting non-existent key is OK |
| 6 | `Scan` | `TestScan_returnsAllItems`, `TestScan_emptyTable` | Returns `{"Items": [...], "Count": N}` |
| 7 | `Query` | `TestQuery_byHashKey` | Needs `KeyConditionExpression` parsing (at minimum: `attrName = :val`) |
| 8 | `TransactWriteItems` | `TestTransactWriteItems_returns501` | Should stay 501 with `x-emulator-unsupported: true` header |

### Domain model (store.go)

Follow the pattern in `internal/services/sqs/store.go` and `internal/services/s3/store.go`:

```go
// Namespaces
const (
    nsTables = "dynamodb:tables"
    nsItems  = "dynamodb:items"
)

type Table struct {
    TableName            string            `json:"TableName"`
    KeySchema            []KeySchemaElement `json:"KeySchema"`
    AttributeDefinitions []AttributeDef    `json:"AttributeDefinitions"`
    TableStatus          string            `json:"TableStatus"`
    BillingMode          string            `json:"BillingMode,omitempty"`
    // ... ARN, creation timestamp, etc.
}

type KeySchemaElement struct {
    AttributeName string `json:"AttributeName"`
    KeyType       string `json:"KeyType"` // "HASH" or "RANGE"
}

type AttributeDef struct {
    AttributeName string `json:"AttributeName"`
    AttributeType string `json:"AttributeType"` // "S", "N", "B"
}
```

Items are stored as `map[string]any` in DynamoDB JSON format (e.g., `{"id": {"S": "user-1"}, "name": {"S": "Alice"}}`).

Item store key: `tableName + "/" + hashKeyValue` (or `tableName + "/" + hashKeyValue + "/" + sortKeyValue` for composite keys).

### Handler pattern (handler.go)

Follow the SQS handler pattern exactly:

```go
type Handler struct {
    cfg   *config.Config
    store *dynamoStore
    log   *serviceutil.ServiceLogger
}

func (h *Handler) CreateTable(w http.ResponseWriter, r *http.Request) {
    var req createTableRequest
    if !serviceutil.DecodeJSON(w, r, &req) {
        return
    }
    if !serviceutil.RequireString(w, r, req.TableName, "TableName") {
        return
    }
    // ... validate with serviceutil.TableName(req.TableName)
    // ... check duplicate, store, return response
}
```

### Service.go changes needed

The current `dynamodb.New(cfg, store)` needs to accept a `*zap.Logger` parameter (like SQS does) and create a `ServiceLogger`. Update `router.go:49` accordingly:

```go
// router.go line 49: change from
dynamodb.New(cfg, store),
// to
dynamodb.New(cfg, store, logger),
```

Wire the dispatch switch to call handler methods instead of returning 501.

### Query/KeyConditionExpression

The hardest part. For P1, only simple equality is needed: `userId = :uid` where `:uid` maps to a value in `ExpressionAttributeValues`. The test `TestQuery_byHashKey` uses this pattern.

Minimal approach:
1. Parse `KeyConditionExpression` for pattern: `<attrName> = <:placeholder>`
2. Look up `:placeholder` in `ExpressionAttributeValues`
3. Scan all items in the table, filter by hash key match
4. Return matching items

### Key patterns to follow

- Use `serviceutil.DecodeJSON`, `serviceutil.RequireString`, `serviceutil.ValidateAndRespond`
- Use `protocol.WriteJSON` for success responses, `protocol.WriteJSONError` for errors
- Use `protocol.Wrap(protocol.ErrInternalError, err)` for state store errors
- State store: `state.Store` interface with `Get/Set/Delete/List` by namespace+key
- JSON marshal/unmarshal items to/from `state.Store` string values (see SQS/S3 store.go)

### What NOT to implement (keep as 501)

- `TransactWriteItems` / `TransactGetItems` — P3
- `UpdateItem` — tests exist but aren't in the failing test file yet; implement if time allows
- `BatchGetItem` / `BatchWriteItem` — implement after core CRUD works
- GSI, LSI, Streams — P3

## Quick verification commands

```bash
go build ./...                            # must compile
go test ./internal/... -count=1           # unit tests must pass
go test ./tests/integration/s3/... -count=1   # must stay green
go test ./tests/integration/sqs/... -count=1  # must stay green
go test ./tests/integration/dynamodb/... -count=1  # target: make these pass
```
