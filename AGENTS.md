# AGENTS.md

> **Read this first** — for AI agents (Claude, Copilot, Cursor, etc.) and any
> contributor making changes via an agent-assisted workflow.
>
> For human contributors, start with [CONTRIBUTING.md](./CONTRIBUTING.md).
> For test conventions, see [tests/AGENTS.md](./tests/AGENTS.md).
> For current implementation status and what to build next, see [STATUS.md](./STATUS.md).

---

## Project goals

1. Works with the official AWS CLI and all official AWS SDK clients (Go, JS/TS, Python, Java, .NET) without changes.
2. Drop-in replacement for LocalStack — same port, same env var conventions, one-line migration.
3. Zero configuration — `docker run -p 4566:4566` is the full getting-started guide.
4. Fast — sub-200ms startup, <15 MiB idle, tiny image.
5. Honest about gaps — unimplemented endpoints return `501`. Silent failures are worse than loud ones.
6. Fully open — MIT, no telemetry, no auth tokens, no usage limits.
7. Production-quality internals — race-safe, well-tested, well-documented, easy to contribute to.

## Non-goals

- **Not a staging environment.** No 100% API parity. Do not base production go/no-go decisions on Overcast tests.
- **Not a security boundary.** Credentials accepted but not validated. Never expose on a public network.
- **Not a performance testing tool.** No latency emulation, no throttling, no quotas.
- **Not CloudFormation/IAM.** Both are out of scope for v1.
- **Not a production dependency, ever.** Local dev and CI only. No durability guarantees, no security model.
- **Not a perfect replica.** We emulate the most-used 20% with high fidelity. Edge cases may differ.

---

## Repository layout

```
cmd/overcast/main.go         <- binary entry point
internal/
  config/                    <- typed env-var config
  router/                    <- chi router, middleware, health + debug endpoints
  middleware/                <- RequestID, Logger, Recovery, SigV4 stub
  protocol/                  <- AWS wire format (XML/JSON errors, ARNs, request IDs)
  state/                     <- Store interface + MemoryStore + SQLiteStore
  serviceutil/               <- shared helpers (request, pagination, validation, logging, lazy init)
  services/
    s3/                      <- P1+P2 complete
    sqs/                     <- P1+P2 complete
    dynamodb/                <- implement next
    sns/                     <- stub
    lambda/                  <- stub
tests/
  AGENTS.md                  <- test conventions (GWT, mocks, helpers)
  helpers/                   <- TestServer, assertions, MockStore
  integration/               <- HTTP-level tests per service
docs/services/               <- per-service endpoint support matrices
```

---

## Core principles

1. **Test-first, always.** Failing test before every feature. Reproducing test before every fix.
2. **Correctness over completeness.** A missing `501` is better than a broken `200`.
3. **No global state.** All dependencies injected. Every component independently testable.
4. **One responsibility per file.** `service.go` routes. `handler.go` handles HTTP. `store.go` owns state.
5. **Explicit over implicit.** Errors are values. Config is typed. Nothing magic.
6. **Honest TODOs.** Every `// TODO:` includes a description and priority:
   `// TODO(priority:P1): implement SigV4 validation` — picked up by the TODO-to-issue Action.
7. **AWS compatibility over test convenience.** Never diverge from real AWS behaviour to make tests easier.
   Async behaviour (SNS delivery, SQS visibility timeouts, Lambda cold starts) stays async. Tests adapt.

---

## Code quality

- **Format/lint/vet:** `make fmt`, `make lint`, `make vet` — CI enforces all three.
- **Naming:** Exported types get doc comments. Error sentinels: `ErrBucketNotFound`. Constructors: `NewHandler(...)`.
- **Comments:** Exported symbols require doc comments (linter enforced). Mark deferred work with `// TODO(priority:Pn):`.

### Error handling

Wrap errors with cause — never discard:

```go
// Standard wrapping
return fmt.Errorf("s3: put object %q: %w", key, err)

// AWS errors — client sees Code+Message; cause preserved for logging
return nil, protocol.Wrap(protocol.ErrInternalError, err)
```

Use `errors.Is` / `errors.As` to inspect. `protocol.AsAWSError(err)` extracts an AWS error from the chain.
The cause is **never sent to clients** — only logged.

### Logging

Structured via `go.uber.org/zap`. Never `fmt.Sprintf` inside a log message.

```go
// correct
logger.Info("queue created", zap.String("queue", name), zap.String("arn", q.ARN))

// wrong
logger.Info(fmt.Sprintf("queue %s created", name))
```

| Level   | Use for                                                   |
| ------- | --------------------------------------------------------- |
| `DEBUG` | Per-request detail, state reads/writes                    |
| `INFO`  | Lifecycle events (server start, resource created/deleted) |
| `WARN`  | Unexpected but handled (debug mode on, large payload)     |
| `ERROR` | 5xx responses, data loss risk                             |

The Logger middleware covers request logging — handlers must not log at INFO for individual operations.

---

## Design patterns

| Pattern              | Where                                 | Purpose                                              |
| -------------------- | ------------------------------------- | ---------------------------------------------------- |
| Strategy             | `lambda.Runtime` interface            | Swap runtimes without changing Lambda handler        |
| Registry             | `router.allServices`                  | Append to add a service; nothing else changes        |
| Repository           | `services/*/store.go`                 | Typed domain access; JSON serialisation in one place |
| Middleware chain     | `internal/middleware/`                | RequestID -> Recovery -> Logger -> SigV4 -> service  |
| Dependency injection | `router.New(cfg, store, logger, clk)` | No globals; everything testable                      |
| Functional options   | `tests/helpers.Option`                | Flexible test server configuration                   |
| Observer (planned)   | `internal/events/`                    | SNS->SQS, SQS->Lambda event pipelines                |

---

## Clock injection — never use `time.Now()` directly

All service code **must** use the injected `clock.Clock` (from `internal/clock`).

```go
// inject and use
type Handler struct { clk clock.Clock }
msg.SentTimestamp = h.clk.Now().UnixMilli()

// forbidden in service code
msg.SentTimestamp = time.Now().UnixMilli()
```

Clock flows: `router.New(clk) -> service.New(clk) -> handler`. Production: `clock.New()`.
Tests: `helpers.WithMockClock()` — call `srv.Clock.Add(d)` to jump forward without sleeping.

---

## Shared utilities — use serviceutil, never duplicate

```go
serviceutil.DecodeJSON(w, r, &req)           // JSON body -> struct, writes error on failure
serviceutil.RequireString(w, r, v, "Name")   // validates required field
serviceutil.QueryInt(r, "max-keys", 1000)    // query param with default
serviceutil.Paginate(items, limit, token)    // opaque continuation tokens
serviceutil.BucketName(name)                // validates AWS naming rules
serviceutil.ServiceLogger(logger, "s3")     // scoped structured logger
serviceutil.LazyInit.Do(fn)                // sync.Once with retry; Reset() for tests
```

Add to `serviceutil` when a pattern appears in two or more services. Never add service-specific logic there.

---

## Performance and safety

**Targets:** <50 MiB Docker image, <15 MiB idle memory.

- Avoid allocations in hot paths — `json.Marshal` not `bytes.Buffer`+encoder.
- Pre-size slices: `make([]string, 0, n)`.
- **Stream data-heavy operations** — any operation that reads or writes large/unbounded data (object bodies, batch responses, scan results, log tails) **must** use `io.Reader`/`io.Writer` pipelines. Loading everything into memory first (`io.ReadAll`, `bytes.Buffer`) is only acceptable when the data is provably small and bounded. Prefer `io.Copy`, `json.NewDecoder(r.Body)`, and chunked writes over accumulate-then-send patterns.
- Measure before optimising: `make bench`.

**Goroutine leaks** — every goroutine must respect context cancellation:

```go
select { case msg := <-ch: ...; case <-ctx.Done(): return }
```

Also: always `defer ticker.Stop()`, always pass `r.Context()` to blocking calls.

**Cross-platform:** use `filepath.Join` (not string concat), `os.TempDir()` (not `/tmp`), no CGO (`modernc.org/sqlite`), no shell scripts in the build pipeline.

---

## Adding a new endpoint (checklist)

1. Write a **failing test** in `tests/integration/<service>/<service>_test.go` (GWT form)
2. Add request/response types to `handler.go` — match AWS SDK wire format exactly (casing matters)
3. Add handler method; wire the route or dispatch case
4. Add state helpers to `store.go` if needed
5. Update `docs/services/<service>.md` — flip the detail row to ✅ and increment the summary table count; **never update only one of the two**
6. Add entry to `CHANGELOG.md` under `[Unreleased]`
7. `make test` — all tests must pass with `-race`

> **Windows / dev-container note:** `go test -race ./...` (full workspace) can hang or be
> very slow when the source is on a Windows host volume (e.g. `E:\`) because the race
> detector rebuilds everything and every file I/O crosses the Hyper-V boundary. The Vite
> polling watcher makes this worse.
> **Recommended workflow on Windows hosts:**
>
> - During active dev, run targeted tests without `-race`:
>   `go test -count=1 ./tests/integration/s3/` etc.
> - Run the full race-enabled suite (`make test`) only before pushing/merging — ideally
>   inside the container where the filesystem is local:
>   `docker compose -f docker-compose.dev.yml run --rm test`

## Adding a new service (checklist)

1. Create `internal/services/<n>/` with `service.go`, `store.go`, `handler.go`
2. Implement `router.Service` interface; append to `allServices` in `internal/router/router.go`
3. Write P1 tests in `tests/integration/<n>/<n>_test.go`
4. Create `docs/services/<n>.md` using the template in `docs/README.md`
5. Add service to README.md table and `CHANGELOG.md`

---

## Error response rules

| Service                    | Format                     | Helper                                  |
| -------------------------- | -------------------------- | --------------------------------------- |
| S3                         | XML                        | `protocol.WriteXMLError(w, r, aerr)`    |
| SQS, SNS, DynamoDB, Lambda | JSON                       | `protocol.WriteJSONError(w, r, aerr)`   |
| Unimplemented              | Same format as the service | `protocol.NotImplementedXML/JSON(w, r)` |

Every 501 gets `x-emulator-unsupported: true` automatically. Every response gets a request ID automatically. Never set these manually.

## State backend rules

- All state through `state.Store` — never maps, files, or globals.
- JSON serialisation in `store.go` — never in handlers.
- Do not change the `Store` interface without updating both implementations.

---

## What agents must NOT do

- Never implement a handler without a failing test written first
- Never fix a bug without a test that reproduces it first
- Never return bare `404` for unimplemented operations — always `501`
- Never call `os.Getenv` in service code — use `*config.Config`
- Never call `time.Now()` directly in service or handler code — use `clock.Clock`
- Never leave a `TODO` without a description
- Never commit code that fails `make lint` or `make test`
- Never update only the summary table in a service doc — update both tables
- Never add external dependencies without justification
- Never edit `web/src/routeTree.gen.ts` — it is auto-generated by TanStack Router when the dev server runs (`npm run dev` in `web/`). After adding or changing route files, check whether the dev server is already running (the user usually has it running); if so, the file will update automatically. Only regenerate manually if the server is not running.

---

### Service file layout

Within a service package, split files by **lifecycle stage and concern** — never by individual operation, never by using subfolders (subfolders = separate packages, which breaks access to private types).

| File                 | Contains                                                                       |
| -------------------- | ------------------------------------------------------------------------------ |
| `service.go`         | `Service` struct, `New`, route registration                                    |
| `handler.go`         | Dispatcher methods + **fully implemented** handlers only                       |
| `handler_stubs.go`   | All `NotImplementedXML`/`NotImplementedJSON` stubs                             |
| `handler_<group>.go` | Implemented handlers for one feature group, when that group exceeds ~200 lines |
| `store.go`           | State access, JSON serialisation                                               |
| `types.go`           | Domain types and error constructors, when `store.go` grows large               |

**Rule: `handler.go` must never contain a stub.**
Stubs live in `handler_stubs.go`. When implementing an operation, _move_ its method body from `handler_stubs.go` into `handler.go` (or into the appropriate `handler_<group>.go`). This makes `handler.go` a complete, accurate inventory of what works — a reader should be able to tell at a glance what is implemented without scrolling past placeholder methods.

**Rule: Split `handler.go` only when a coherent group of implemented handlers exceeds ~200 lines.**
Split by feature group, not by HTTP method or operation name. Good split points:

| File                    | When to create it                                                                                               |
| ----------------------- | --------------------------------------------------------------------------------------------------------------- |
| `handler_multipart.go`  | CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts are all implemented |
| `handler_versioning.go` | Versioning + lifecycle group is implemented                                                                     |
| `handler_tagging.go`    | Object/bucket tagging handlers are implemented                                                                  |

Never split `handler_stubs.go` — one stub file per service is always sufficient.

**Rule: Never use subfolders inside a service package.**
`internal/services/s3/buckets/` would require exporting `s3Store`, `errNoSuchBucket`, and every other private symbol. The cost always outweighs the benefit. Multiple files in the same package is the correct Go pattern (the standard library and the AWS SDK both do this).

---

## Versioning and changelog

Semantic versioning. Every PR that changes user-facing behaviour **must** add an entry to `[Unreleased]` in `CHANGELOG.md`. Docs-only PRs may omit.
