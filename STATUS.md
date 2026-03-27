# STATUS.md — Current implementation status

> This file tracks what's built, what's next, and key implementation notes.
> Update it as features land. For coding conventions see [AGENTS.md](./AGENTS.md).

---

## What this project is

High-performance local AWS service emulator, written in Go.
Single binary, single Docker container, sub-200ms startup, zero config.
Drop-in replacement for LocalStack on port 4566.

Target: AWS SDK v3 compatibility (Go, JavaScript, Python, Java).

---

## Current implementation status

| Service   | P1 | P2 | Notes |
|-----------|----|----|-------|
| S3        | ✅ | ✅ | Full P1+P2: bucket CRUD, object CRUD, list, copy, location |
| SQS       | ✅ | ✅ | Full P1+P2: queue + message CRUD, batches, purge, attributes |
| DynamoDB  | ❌ | ❌ | Dispatch wired, all return 501 — **implement next** |
| SNS       | ❌ | ❌ | Dispatch wired, all return 501 |
| Lambda    | ❌ | ❌ | Routes wired, NodeRuntime stub exists |

### Infrastructure status

| Component | Status |
|-----------|--------|
| Config (host, port, TLS, debug, services, state) | ✅ |
| Middleware chain (RequestID, Logger, Recovery, SigV4 stub) | ✅ |
| Protocol helpers (XML/JSON errors, ARNs, request IDs) | ✅ |
| MemoryStore | ✅ |
| SQLiteStore | ✅ |
| Health endpoint (`/_health`) | ✅ |
| Debug endpoints (`/_debug/*`) | ✅ (gated by OVERCAST_DEBUG) |
| HTTPS / TLS | ✅ (OVERCAST_TLS_CERT + OVERCAST_TLS_KEY) |
| Cross-platform build | ✅ (no CGO, pure-Go SQLite, Taskfile.yml for Windows) |
| `serviceutil` shared utilities | ✅ (request, pagination, validation, logging, lazy init) |
| Benchmarks | ✅ (state/memory_bench_test.go, make bench) |
| S3+SQS integration tests | ✅ passing |
| DynamoDB integration tests | ✅ passing (all return 501 as expected) |
| Config/protocol/state unit tests | ✅ |

---

## Architecture

HTTP requests → `cmd/overcast/main.go` → `internal/router` (chi router + middleware) →
service handler packages (`internal/services/{s3,sqs,...}`). Each service has `service.go`
(routing/dispatch), `store.go` (domain types + state), `handler.go` (HTTP handlers). State
flows through `state.Store` interface → `MemoryStore` or `SQLiteStore`. AWS wire format
helpers in `internal/protocol/`. Debug endpoints in `internal/router/debug.go` (gated by `cfg.Debug`).

---

## TDD workflow (mandatory)

1. Write failing test in `tests/integration/<service>/<service>_test.go`
2. Confirm failure: `make test-integration`
3. Write minimum implementation
4. Confirm passing: `make test` (all tests + race detector)
5. Refactor — tests must still pass
6. Update `docs/services/<service>.md` — flip the detail row and increment the summary count

Tests use Given/When/Then form. See `tests/AGENTS.md`.

---

## What to implement next (in priority order)

### 1. DynamoDB P1 (highest priority)

Tests are already written and failing in `tests/integration/dynamodb/dynamodb_test.go`.
Make them pass. Implementation order:

1. `CreateTable` / `DescribeTable` / `ListTables` / `DeleteTable`
2. `PutItem` / `GetItem` / `DeleteItem`
3. `Scan` (simplest — full table scan, optional FilterExpression)
4. `Query` (KeyConditionExpression with = on hash key, optional range conditions)
5. `UpdateItem`
6. `BatchGetItem` / `BatchWriteItem`

The expression evaluator (KeyConditionExpression, FilterExpression) is the hardest part.
Start with simple equality (`attr = :val`) and add operators incrementally, each with a test.

Files to create:
- `internal/services/dynamodb/store.go` — domain types (Table, Item) + state helpers
- `internal/services/dynamodb/handler.go` — HTTP handlers
- Update `internal/services/dynamodb/service.go` — wire handlers into dispatch switch

Use `serviceutil.DecodeJSON`, `serviceutil.RequireString`, and `serviceutil.Paginate`
throughout. Add a `ServiceLogger` to the Handler struct:

```go
type Handler struct {
    cfg   *config.Config
    store *dynamoStore
    log   *serviceutil.ServiceLogger
}
```

### 2. SNS P1

After DynamoDB P1. Write tests first.

1. `CreateTopic` / `DeleteTopic` / `ListTopics` / `GetTopicAttributes`
2. `Subscribe` (sqs protocol only first) / `Unsubscribe` / `ListSubscriptionsByTopic`
3. `Publish` → fan-out to SQS subscribers (requires EventBus or direct SQS call)

Fan-out delivery should use the Observer pattern via an `EventBus` in `internal/events/`.
This same bus will be used by SQS→Lambda ESM and DynamoDB Streams → Lambda later.

### 3. Lambda P2 (Node.js execution)

Lambda emulation has two layers:

**Control plane** (function CRUD) — straightforward HTTP, already stubbed.

**Data plane** (actual execution):
- Cold start: code extraction, runtime init, handler module load
- Timeout enforcement: SIGTERM at T-300ms, SIGKILL at timeout
- The **Lambda Runtime Interface** on `localhost:9001`:
  - `GET /2018-06-01/runtime/invocation/next`
  - `POST /2018-06-01/runtime/invocation/{id}/response`
  - `POST /2018-06-01/runtime/invocation/{id}/error`

Implementation order:
1. Function CRUD: `CreateFunction`, `GetFunction`, `UpdateFunctionCode`, `ListFunctions`, `DeleteFunction`
2. Sync `Invoke` — real Node.js execution via `NodeRuntime.Invoke()`
3. Async `Invoke` (fire and forget)
4. SQS → Lambda event source mapping (requires EventBus)

Files to create when implementing:
- `internal/services/lambda/executor.go` — process lifecycle manager
- `internal/services/lambda/runtime_api.go` — loopback Runtime Interface HTTP server
- `internal/services/lambda/runtime/bootstrap.js` — Node.js wrapper script

### 4. Event pipelines

Build `internal/events/EventBus` interface first, then wire:
- SQS → Lambda (event source mapping)
- SNS → SQS subscription delivery
- DynamoDB Streams → SQS (P3)
- DynamoDB Streams → Lambda (P3)

### 5. Future / P3

- S3 multipart upload
- DynamoDB GSI
- DynamoDB transactions
- SigV4 validation (`internal/middleware/sigv4.go` TODO block)
- Debug UI (web interface for `/_debug/*` endpoints)

---

## Common commands

```bash
make test               # all tests + race detector
make test-unit          # fast unit tests (internal/ only)
make test-integration   # integration tests (tests/integration/)
make test-coverage      # HTML coverage report
make run                # build + run on :4566
make lint               # golangci-lint
docker compose up       # run in Docker
```

---

## Environment variables

| Variable | Default | Notes |
|----------|---------|-------|
| `OVERCAST_HOST` | `0.0.0.0` | Bind address |
| `OVERCAST_PORT` | `4566` | |
| `OVERCAST_STATE` | `memory` | `memory` or `sqlite` |
| `OVERCAST_DATA_DIR` | `~/.overcast/data` | SQLite + persistence dir |
| `OVERCAST_SERVICES` | all | Comma-separated |
| `OVERCAST_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `OVERCAST_DEBUG` | `false` | Enable `/_debug/*` |
| `OVERCAST_TLS_CERT` | — | Path to cert (enables HTTPS) |
| `OVERCAST_TLS_KEY` | — | Path to key (required with TLS_CERT) |
| `OVERCAST_REGION` | `us-east-1` | |
| `OVERCAST_ACCOUNT_ID` | `000000000000` | |
| `OVERCAST_SIGV4_VALIDATE` | `false` | Not yet implemented |
