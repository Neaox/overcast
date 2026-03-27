# Contributing to Overcast

Welcome — we're glad you're here.

This guide covers everything you need to go from zero to a merged pull request.
For AI agents and agent-assisted workflows, read [AGENTS.md](./AGENTS.md) first —
it has the complete implementation conventions. This file is the human-oriented
companion: setup, workflow, and how we work together.

---

## Contents

- [Project goals](#project-goals)
- [Prerequisites](#prerequisites)
- [First-time setup](#first-time-setup)
- [Development workflow](#development-workflow)
- [Code standards](#code-standards)
- [Logging standards](#logging-standards)
- [Testing](#testing)
- [Versioning and changelog](#versioning-and-changelog)
- [How to add an endpoint](#how-to-add-an-endpoint)
- [How to add a service](#how-to-add-a-service)
- [Commit conventions](#commit-conventions)
- [Pull request checklist](#pull-request-checklist)
- [Reporting bugs](#reporting-bugs)
- [Ideas for contributions](#ideas-for-contributions)

---

## Project goals

Overcast aims to be the best zero-config local cloud emulator:

1. Works with the AWS CLI without any changes
2. Works with all official AWS SDK clients
3. Drop-in replacement for LocalStack — switching is one line
4. Zero configuration — `docker run -p 4566:4566` is the whole setup
5. Fast startup and low memory — CI should not wait for the emulator
6. Honest about gaps — missing features say `501`, not `200` with wrong data
7. Fully open — MIT, no auth tokens, no telemetry, no usage limits
8. Production-quality internals — race-safe, well-tested, easy to contribute to

---

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| Go | 1.23+ | https://go.dev/dl/ |
| Docker | 24+ | https://docs.docker.com/get-docker/ |
| golangci-lint | latest | `brew install golangci-lint` or https://golangci-lint.run |
| Node.js | 18+ | Only for Lambda work — https://nodejs.org |

---

## First-time setup

```bash
# 1. Fork and clone
git clone https://github.com/your-org/overcast
cd overcast

# 2. Install Go dependencies
go mod tidy

# 3. Confirm everything passes
make test

# 4. Start the server (confirms it boots cleanly)
make run
# → overcast listening on :4566
```

If `make test` passes, you're ready.

---

## Development workflow

### Day-to-day commands

```bash
make test              # all tests + race detector — run before every commit
make test-unit         # fast unit tests (internal/) — run while writing code
make test-integration  # full integration suite
make test-coverage     # HTML coverage report → coverage.html
make lint              # golangci-lint
make fmt               # gofmt all files
make vet               # go vet
make run               # build and run on :4566
docker compose up      # run in Docker (rebuilds image)
```

### Step debugging

Full step debugging is supported. Set a breakpoint (click left of line number),
press F5, select a launch configuration. See **[docs/debugging.md](./docs/debugging.md)**
for the full guide including conditional breakpoints, logpoints, and debugging
test failures.

### TDD cycle — mandatory

We are strict about test-first development. The order is:

1. Write a **failing test** that describes the desired behaviour
2. Run `make test` — confirm the test fails for the right reason
3. Write the **minimum implementation** to make it pass
4. Run `make test` — all tests must pass with race detector
5. Refactor if needed — tests must still pass
6. Update `docs/services/<service>.md` and `CHANGELOG.md`

See [tests/AGENTS.md](./tests/AGENTS.md) for test conventions.

---

## Code standards

These are shared between human contributors and AI agents. The full rationale is
in [AGENTS.md](./AGENTS.md#code-quality-standards).

- **Format:** `gofmt`. Run `make fmt` before committing. Non-formatted code fails CI.
- **Lint:** `golangci-lint`. Run `make lint`. Config in `.golangci.yml`.
- **Errors:** Return errors as values, wrap with context. See the full guide below.
- **HTTP errors:** Use `protocol.WriteXMLError` (S3) or `protocol.WriteJSONError` (JSON services) — never raw `http.Error`.
- **Unimplemented:** Return `501` via `protocol.NotImplementedXML/JSON` — never a bare `404`.

### Error wrapping with cause (Go equivalent of `new Error(msg, { cause })`)

Go 1.13+ supports error wrapping — identical in intent to JavaScript's
`new Error("message", { cause: originalError })`.

**Standard Go wrapping** — add context, preserve the original:
```go
return fmt.Errorf("s3: put object %q: %w", key, err)
//                                          ^^ %w wraps err as the cause
```

**Inspecting the chain:**
```go
errors.Is(err, io.EOF)              // true if io.EOF is anywhere in the chain
errors.As(err, &specificType)       // extract a specific type from the chain
errors.Unwrap(err)                  // get the immediate cause
```

**For AWS errors, use `protocol.Wrap()`** — attaches an underlying cause while
presenting a clean AWS error code to the HTTP client:
```go
// The client sees: {"__type":"InternalError","message":"An internal error occurred."}
// The server logs: InternalError (cause: sqlite: disk I/O error)
return nil, protocol.Wrap(protocol.ErrInternalError, storageErr)
```

The cause is **never sent to clients** — this is tested. It is only available
for server-side logging and debugging. This is the recommended pattern any time
a state operation fails — never discard the underlying error.

See [AGENTS.md](./AGENTS.md#error-handling--wrapping-with-cause) for the full guide.
- **State:** All mutable state through `state.Store` — never direct maps or globals.
- **Comments:** Exported symbols need a doc comment. Use `// TODO(priority:Px):` for deferred work.
- **No globals:** All dependencies are injected via function parameters.
- **Time:** Never call `time.Now()` directly in service or handler code. Use the injected `clock.Clock` (from `internal/clock`). See [Time / clock injection](#time--clock-injection) below.

---

## Logging standards

We use structured logging (`go.uber.org/zap`). Never use `fmt.Sprintf` in log messages.

```go
// ✅ structured — fields are queryable and filterable
logger.Info("bucket created",
    zap.String("bucket", name),
    zap.String("region", cfg.Region),
)

// ❌ unstructured — just a string, can't filter or query
logger.Info(fmt.Sprintf("bucket %s created in %s", name, cfg.Region))
```

### When to use each level

| Level | Use for |
|-------|---------|
| `DEBUG` | Per-request detail (parsed params, state reads/writes, middleware steps). Useful during development, too noisy for normal operation. |
| `INFO` | Server lifecycle events and significant operations (server start, service enabled, resource created/deleted). |
| `WARN` | Unexpected conditions that were handled (debug mode enabled, large payload, feature stub activated). |
| `ERROR` | Failures that caused a 5xx response, panic recovery, or data loss risk. |

The Logger middleware logs every request at INFO — don't duplicate this in handlers.
Handler code should use DEBUG for operation detail.
Never log credentials, request bodies of sensitive operations, or values that may contain PII.

---

## Time / clock injection

**Never call `time.Now()` directly** in service handlers, stores, or any code
under `internal/services/`. Instead, use the injected `clock.Clock` from
`internal/clock` (a thin wrapper around `github.com/benbjohnson/clock`).

```go
// ✅ Correct — injectable, testable
type Handler struct {
    clk clock.Clock
    // ...
}

msg.SentTimestamp = h.clk.Now().UnixMilli()
msg.VisibleAfter  = h.clk.Now().Add(time.Duration(delay) * time.Second)

// ❌ Wrong — not testable without real sleeps
msg.SentTimestamp = time.Now().UnixMilli()
```

The clock is wired in through `router.New → s3.New / sqs.New → newHandler`.
In production, `clock.New()` (real wall-clock) is used. In tests, pass
`helpers.WithMockClock()` to `NewTestServer` and advance via `srv.Clock.Add(d)`:

```go
// Advance through a 30-second visibility timeout without any real sleep:
srv := helpers.NewTestServer(t, helpers.WithMockClock())
// ... send a message, receive it (marks it invisible for 30s) ...
srv.Clock.Add(31 * time.Second)   // instant — no time.Sleep required
// ... receive again — message is now visible again ...
```

This also applies to Lambda timeout enforcement and any future SNS retry backoffs.

---

## Testing

Tests use the **Given/When/Then** pattern. Full test conventions are in
[tests/AGENTS.md](./tests/AGENTS.md).

```go
func TestGetObject_notFound(t *testing.T) {
    // Given: a bucket with no objects
    srv := helpers.NewTestServer(t)
    createBucket(t, srv, "empty-bucket")

    // When: we GET a non-existent key
    resp, err := http.DefaultClient.Do(get(srv, "/empty-bucket/missing.txt"))
    require.NoError(t, err)
    defer resp.Body.Close()

    // Then: we get a well-formed NoSuchKey error
    helpers.AssertStatus(t, resp, http.StatusNotFound)
    helpers.AssertXMLError(t, resp, "NoSuchKey")
    helpers.AssertRequestID(t, resp)
}
```

---

## Versioning and changelog

We use [Semantic Versioning](https://semver.org/). Version bump rules:

| Change | Bump |
|--------|------|
| Breaking API change (env var rename, response format change) | MAJOR |
| New endpoint, new service, new feature | MINOR |
| Bug fix, performance improvement, documentation | PATCH |

**Every PR that changes user-facing behaviour must update `CHANGELOG.md`.**

Add your entry under `[Unreleased]`:

```markdown
## [Unreleased]

### Added
- S3: `GetBucketVersioning` endpoint (#42)

### Fixed
- SQS: `ReceiveMessage` now correctly applies `VisibilityTimeout` (#38)
```

---

## How to add an endpoint

Full checklist with code examples is in [AGENTS.md](./AGENTS.md#adding-a-new-endpoint-checklist).

Short version:
1. Write a failing test (GWT form)
2. Add request/response types to `handler.go`
3. Add handler method
4. Wire route or dispatch case
5. Update `docs/services/<service>.md`
6. Update `CHANGELOG.md`
7. `make test` must pass

---

## How to add a service

Full checklist is in [AGENTS.md](./AGENTS.md#adding-a-new-service-checklist).

---

## Commit conventions

[Conventional Commits](https://www.conventionalcommits.org/) format:

```
<type>(<scope>): <short description>

[optional body]

[optional footer — reference issues]
```

**Types:** `feat`, `fix`, `test`, `docs`, `refactor`, `chore`, `perf`

**Scopes:** `s3`, `sqs`, `dynamodb`, `sns`, `lambda`, `state`, `config`, `router`, `middleware`, `tls`, `debug`, `ci`

**Examples:**
```
feat(dynamodb): implement PutItem and GetItem (P1)
fix(sqs): apply VisibilityTimeout correctly on first receive
test(s3): add GWT tests for CopyObject cross-bucket
docs(dynamodb): mark PutItem and GetItem as supported
perf(state): use sync.Map for concurrent MemoryStore reads
chore(ci): add TODO-to-issue GitHub Action
```

---

## Pull request checklist

- [ ] `make test` passes (all tests, with race detector)
- [ ] `make lint` passes (no golangci-lint errors)
- [ ] New endpoints have integration tests in GWT form
- [ ] `docs/services/<service>.md` updated if applicable
- [ ] `CHANGELOG.md` updated under `[Unreleased]`
- [ ] Commit messages follow conventional commits
- [ ] No debug logging left in production paths
- [ ] No new global variables

---

## Reporting bugs

Open a [bug report](.github/ISSUE_TEMPLATE/bug_report.md) with:

1. The service and operation that's broken (e.g. "SQS / ReceiveMessage")
2. The operation's status in `docs/services/<service>.md` — if it says ❌, it's expected to not work
3. What you expected vs what happened (include the error code)
4. A minimal reproduction (curl or code snippet)
5. Your Overcast version and run mode

---

## Ideas for contributions

Good first issues:

- **DynamoDB P1** — tests are already written in `tests/integration/dynamodb/dynamodb_test.go`. Make them pass.
- **SNS P2** — CreateTopic, Subscribe (SQS protocol), Publish fan-out
- **Lambda Node.js execution** — complete `NodeRuntime.Invoke()` in `internal/services/lambda/node_runtime.go`
- **SQS → Lambda event source mapping** — implement CreateEventSourceMapping + background poller
- **S3 multipart upload** — the most-requested missing S3 feature
- **Debug UI** — a web interface for `/_debug/*` endpoints (plain HTML or React)
- **SigV4 validation** — see the TODO in `internal/middleware/sigv4.go`
- **Performance benchmarks** — `go test -bench` baseline for regression tracking

Check [GitHub Issues](https://github.com/your-org/overcast/issues) for the full
list of open work, tagged by priority (P1/P2/P3) and effort (small/medium/large).
