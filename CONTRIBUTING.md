# Contributing to Overcast

Welcome — we're glad you're here.

This guide covers everything you need to go from zero to a merged pull request — coding
standards, architecture decisions, performance expectations, and workflow.

For test conventions, see [tests/AGENTS.md](./tests/AGENTS.md).
For current implementation status and what to build next, see [STATUS.md](./STATUS.md).
AI agents using this repo should also read [AGENTS.md](./AGENTS.md) for agent-specific guardrails.

---

## Contents

- [Contributing to Overcast](#contributing-to-overcast)
  - [Contents](#contents)
  - [Project goals](#project-goals)
  - [Design philosophy: match real AWS](#design-philosophy-match-real-aws)
  - [Core principles](#core-principles)
  - [Supported platforms](#supported-platforms)
  - [Prerequisites](#prerequisites)
  - [First-time setup](#first-time-setup)
  - [Development workflow](#development-workflow)
    - [Day-to-day commands](#day-to-day-commands)
    - [Step debugging](#step-debugging)
    - [TDD cycle — mandatory](#tdd-cycle--mandatory)
  - [Code standards](#code-standards)
    - [Clean, idiomatic, performant code](#clean-idiomatic-performant-code)
  - [Error handling](#error-handling)
  - [Logging standards](#logging-standards)
    - [When to use each level](#when-to-use-each-level)
  - [Time / clock injection](#time--clock-injection)
  - [Shared utilities — use serviceutil, never duplicate](#shared-utilities--use-serviceutil-never-duplicate)
  - [Performance and safety](#performance-and-safety)
  - [Design patterns](#design-patterns)
  - [CloudFormation integration](#cloudformation-integration)
    - [How it works](#how-it-works)
    - [Rules](#rules)
  - [Testing](#testing)
  - [Versioning and changelog](#versioning-and-changelog)
  - [How to add an endpoint](#how-to-add-an-endpoint)
  - [How to add a service](#how-to-add-a-service)
  - [Service package structure](#service-package-structure)
  - [Web UI standards](#web-ui-standards)
    - [API access policy (SDK-first)](#api-access-policy-sdk-first)
    - [Frontend — Tailwind CSS v4](#frontend--tailwind-css-v4)
    - [Topology map methodology](#topology-map-methodology)
    - [Service home screen](#service-home-screen)
    - [Global search](#global-search)
    - [service-registry.ts and unsupported-services.ts](#service-registryts-and-unsupported-servicests)
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

## Design philosophy: match real AWS

The usefulness of Overcast is directly tied to how closely it behaves like real AWS. Every
behavioral difference — whether it's a wrong error code, a missing validation, a field with
a slightly different default, or a state transition that happens in the wrong order — is a
potential surprise waiting to bite a developer who trusted their local tests. The closer we
get to real AWS behaviour, the more confidently people can develop and test locally, and the
fewer "works on my machine" failures they hit when deploying.

This means:

- **Requests and responses are the compatibility contract.** Everything an AWS SDK sends to
  us and everything we send back — status codes, headers, body shape, field names, casing,
  default values, error codes, pagination tokens — MUST match real AWS. Internal
  implementation may differ freely, but the wire-level inputs and outputs are the public API
  and must be indistinguishable from the real service. This is what "compatibility" means.
- **Error codes and messages** should match what AWS actually returns, not what seems reasonable.
- **Response shapes** (field names, casing, nesting, default values) should mirror the real API.
- **Validation rules** should reject the same inputs AWS rejects, with the same error responses.
- **State transitions and side effects** should follow the same sequencing AWS uses.
- **Don't skip intermediate states.** If a real AWS resource goes through `CREATING` →
  `AVAILABLE`, the emulated version should too — even if the transition happens immediately
  after the initial response. Artificial delays are not required, but every state in the
  lifecycle must exist and be observable. Code that polls for `CREATING` before proceeding
  is real-world code, and it should work here the same way it works against AWS.
- **When you're unsure how AWS behaves, test it.** Spin up a real AWS resource, try the edge
  case, and replicate what you observe. Guessing leads to drift.

Perfect parity is not always achievable — some behaviours depend on AWS internals we can't
replicate, and that's fine. But fidelity is the default goal, not a stretch goal. When a
known divergence is unavoidable, document it explicitly (in the service doc and in code
comments) so users aren't caught off guard. Never silently return a `200` with wrong
behaviour — a `501` that says "not implemented" is always preferable to a response that
looks right but acts wrong.

---

## Core principles

These guide every decision — from architecture to variable naming. Read them before writing code.

1. **Test-first, always.** Failing test before every feature. Reproducing test before every fix.
2. **Correctness over completeness.** A missing `501` is better than a broken `200`.
3. **No global state.** All dependencies injected. Every component independently testable.
4. **One responsibility per file.** `service.go` routes. `handler.go` handles HTTP. `store.go` owns state.
5. **Explicit over implicit.** Errors are values. Config is typed. Nothing magic.
6. **DRY — never duplicate logic.** If the same pattern exists in two places, extract it. Shared helpers go in `serviceutil`. Shared types go in `protocol` or a common types file. Copy-paste is a bug. However, duplication is acceptable when the DRY abstraction would be harder to understand or maintain than the repeated code — avoid over-engineering.
7. **Idiomatic Go, always.** Follow [Effective Go](https://go.dev/doc/effective_go) and standard library conventions. Prefer simple, readable code over clever abstractions. Keep functions short and focused — one screen, one job. Use the type system to prevent misuse. If a reviewer has to ask "why?" the code is too clever.
8. **Performance is everyone's job.** Think about allocations, algorithmic complexity, and memory layout in every code path — not just hot paths. Pre-size collections, reuse buffers, stream large data, avoid unnecessary copies. Profile before optimising, but write efficient code from the start. Target: every handler ≤1 ms overhead above store access.
9. **Maintainability is a feature.** Code is read 10× more than it is written. Optimise for the next reader: consistent structure, clear naming, small interfaces, minimal coupling. If a change in one package forces changes in three others, the design is wrong.
10. **Honest TODOs.** Every `// TODO:` includes a description and priority:
    `// TODO(priority:P1): implement SigV4 validation` — picked up by the TODO-to-issue Action.
11. **AWS compatibility over test convenience.** Never diverge from real AWS behaviour to make tests easier.
    Async behaviour (SNS delivery, SQS visibility timeouts, Lambda cold starts) stays async. Tests adapt.
12. **AWS fidelity on core APIs — extensions are strictly additive.** Implemented AWS API
    endpoints must behave exactly as a real AWS SDK client expects. Never add non-standard
    fields to AWS responses, alter error codes, change state machine transitions, or introduce
    side effects that would surprise code tested against real AWS. Emulator-only features
    (progress SSE, source browsing, saved test events, topology graph) live behind `/_` prefixed
    internal endpoints or custom headers — never on the AWS API surface. If a feature cannot be
    implemented faithfully, return `501` rather than a divergent `200`.

---

## Supported platforms

| Platform | Arch         | Tier      | Notes                                        |
| -------- | ------------ | --------- | -------------------------------------------- |
| Linux    | amd64, arm64 | Primary   | Docker image target; CI runs here            |
| macOS    | amd64, arm64 | Primary   | Developer workstations; Apple Silicon native |
| Windows  | amd64        | Secondary | Console `.exe`; init hooks use `cmd.exe /c`  |

**All contributions must compile without error on every supported platform.**
Use build tags (`//go:build linux`, `//go:build !windows`, etc.) to isolate
platform-specific syscalls. Verify with:

```bash
GOOS=linux   GOARCH=amd64 go build ./...
GOOS=darwin  GOARCH=arm64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...
# or: make build-cross
```

`go build ./...` on the current platform only catches compile errors for that
platform. If you use `syscall`, `os/exec` with Unix-specific flags, or anything
from `golang.org/x/sys/unix`, add a build tag and a corresponding stub for other
platforms. See `internal/inithooks/hooks_unix.go` and `hooks_windows.go` for the
pattern, and `internal/router/procstart_*.go` for the process-start-time files.

---

## Prerequisites

| Tool          | Version | Install                                                   |
| ------------- | ------- | --------------------------------------------------------- |
| Go            | 1.24+   | https://go.dev/dl/                                        |
| Docker        | 24+     | https://docs.docker.com/get-docker/                       |
| golangci-lint | v1.64.8 | `brew install golangci-lint` or https://golangci-lint.run |
| actionlint    | v1.7.7  | `make lint-actions` uses pinned `go run` automatically         |
| Node.js       | 18+     | For web UI builds and Lambda work — https://nodejs.org    |

---

## First-time setup

```bash
# 1. Fork and clone
git clone https://github.com/Neaox/overcast
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
make lint              # all linters: Go/emulation, web UI, GitHub Actions
make lint-go           # Go/emulation lint (golangci-lint)
make lint-web          # web UI lint (ESLint)
make lint-actions      # GitHub Actions workflow lint (pinned actionlint)
make fmt               # gofmt all files
make vet               # go vet
make check             # aggregate pre-PR checks
make run               # build and run on :4566
docker compose up      # run in Docker (rebuilds image)
```

### Step debugging

Full step debugging is supported. Set a breakpoint (click left of line number),
press F5, select a launch configuration. See **[docs/debugging.md](./docs/debugging.md)**
for the full guide including conditional breakpoints, logpoints, and debugging
test failures.

### TDD cycle — mandatory

> [!IMPORTANT]
> We are strict about test-first development. Every feature starts with a failing test;
> every bug fix starts with a reproducing test. PRs without tests will not be merged.

The order is:

1. Write a **failing test** that describes the desired behaviour
2. Run `make test` — confirm the test fails for the right reason
3. Write the **minimum implementation** to make it pass
4. Run `make test` — all tests must pass with race detector
5. Refactor if needed — tests must still pass
6. Update service-doc prose (behavior notes, caveats) as needed, then regenerate generated docs tables with `make docs`; update `CHANGELOG.md`

See [tests/AGENTS.md](./tests/AGENTS.md) for test conventions.

---

## Code standards

- **Format:** `gofmt`. Run `make fmt` before committing. Non-formatted code fails CI.
- **Lint:** `golangci-lint`. Run `make lint`. Config in `.golangci.yml`.
- **Naming:** Exported types get doc comments. Error sentinels: `ErrBucketNotFound`. Constructors: `NewHandler(...)`.
- **Comments:** Exported symbols require doc comments (linter enforced). Mark deferred work with `// TODO(priority:Pn):`.
- **HTTP errors:** Use `protocol.WriteXMLError` (S3) or `protocol.WriteJSONError` (JSON services) — never raw `http.Error`.
- **HTTP success responses:** Use protocol writers (`protocol.WriteXML`, `protocol.WriteQueryXML`, `protocol.WriteJSON`, `protocol.WriteAWSJSON`) rather than ad-hoc `json.Marshal` + header writing in handlers.
- **Unimplemented:** Return `501` via the protocol-matching helper (`protocol.NotImplementedXML`, `protocol.NotImplementedQueryXML`, `protocol.NotImplementedJSON`) — never a bare `404`.
- **Query-protocol parse failures:** Return an AWS `InvalidArgument` Query XML error (`protocol.WriteQueryXMLError`) — do not map malformed form/query input to `NotImplemented`.
- **Request IDs:** Success and error responses must include the expected AWS request-id header via shared protocol helpers; do not manually omit or rename request-id headers.
- **State:** All mutable state through `state.Store` — never direct maps or globals.
- **No globals:** All dependencies are injected via function parameters.
- **Time:** Never call `time.Now()` directly in service or handler code. Use the injected `clock.Clock` (from `internal/clock`). See [Time / clock injection](#time--clock-injection) below.

### Clean, idiomatic, performant code

These apply **everywhere** — handlers, stores, tests, utilities, middleware:

- **No dead code.** Delete unused functions, variables, and imports. Do not comment out code "for later."
- **No magic numbers.** Use named constants. `maxPageSize = 1000` not `1000` scattered through handlers.
- **Small interfaces.** Accept the narrowest interface that works (`io.Reader` not `*os.File`). Produce concrete types.
- **Value receivers for read-only methods.** Pointer receivers only when mutating or when the struct is large.
- **Return early.** Guard clauses at the top, happy path unindented. Avoid deep nesting.
- **Table-driven tests.** One `t.Run` per case. Share setup, vary inputs. Never copy-paste a test and tweak one line.
- **Zero-alloc where practical.** Use `sync.Pool` for hot-path buffers, `strings.Builder` for concatenation, `strconv` over `fmt.Sprintf` for simple conversions. Avoid `interface{}` in tight loops.
- **Consistent structure across services.** If S3 does it one way and SQS another, unify — don't let inconsistency accumulate.

---

## Error handling

Wrap errors with cause — never discard:

```go
// Standard wrapping — add context, preserve the original
return fmt.Errorf("s3: put object %q: %w", key, err)
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

Use `errors.Is` / `errors.As` to inspect. `protocol.AsAWSError(err)` extracts an AWS error from the chain.

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

| Level   | Use for                                                                                                                              |
| ------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `DEBUG` | Per-request detail (parsed params, state reads/writes, middleware steps). Useful during development, too noisy for normal operation. |
| `INFO`  | Server lifecycle events and significant operations (server start, service enabled, resource created/deleted).                        |
| `WARN`  | Unexpected conditions that were handled (debug mode enabled, large payload, feature stub activated).                                 |
| `ERROR` | Failures that caused a 5xx response, panic recovery, or data loss risk.                                                              |

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

> Performance is not a phase — it is a property of every line of code.

**Targets:** <15 MiB idle memory. Watch for unexpected jumps in Docker image size — the full image is ~96 MB and the slim image ~36 MB; large increases should be justified (e.g. a new runtime dependency) and called out in the PR.

- Avoid allocations in hot paths — `json.Marshal` not `bytes.Buffer`+encoder.
- Pre-size slices: `make([]string, 0, n)`.
- **Stream data-heavy operations** — any operation that reads or writes large/unbounded data (object bodies, batch responses, scan results, log tails) **must** use `io.Reader`/`io.Writer` pipelines. Loading everything into memory first (`io.ReadAll`, `bytes.Buffer`) is only acceptable when the data is provably small and bounded. Prefer `io.Copy`, `json.NewDecoder(r.Body)`, and chunked writes over accumulate-then-send patterns.
- Measure before optimising: `make bench`.
- **Document measurement conditions for every performance claim.** A number without context is misleading. See [docs/performance.md § Documenting performance claims](docs/performance.md#documenting-performance-claims) for the required fields (what, how, environment, inclusions/exclusions).
- **Respect the startup budget.** No store reads, network I/O, DDL, file reads, or eager goroutine work in service `New()` or any `Init*` method called from `router.New()`. Use the `sync.Once` lazy-init pattern. See [docs/performance.md § Startup budget — rules for service authors](docs/performance.md#startup-budget--rules-for-service-authors).

**Goroutine leaks** — every goroutine must respect context cancellation:

```go
select { case msg := <-ch: ...; case <-ctx.Done(): return }
```

Also: always `defer ticker.Stop()`, always pass `r.Context()` to blocking calls.

**Cross-platform:** use `filepath.Join` (not string concat), `os.TempDir()` (not `/tmp`), no CGO (`modernc.org/sqlite`), no shell scripts in the build pipeline.

---

## Design patterns

| Pattern              | Where                                               | Purpose                                              |
| -------------------- | --------------------------------------------------- | ---------------------------------------------------- |
| Strategy             | `lambda.Runtime` interface                          | Swap runtimes without changing Lambda handler        |
| Registry             | `router.allServices`                                | Append to add a service; nothing else changes        |
| Repository           | `services/*/store.go`                               | Typed domain access; JSON serialisation in one place |
| Middleware chain     | `internal/middleware/`                              | RequestID -> Recovery -> Logger -> SigV4 -> service  |
| Dependency injection | `router.New(cfg, store, logger, clk, [hookRunner])` | No globals; everything testable                      |
| Functional options   | `tests/helpers.Option`                              | Flexible test server configuration                   |
| Observer (planned)   | `internal/events/`                                  | SNS->SQS, SQS->Lambda event pipelines                |

---

## CloudFormation integration

Every service that creates resources must have corresponding **CloudFormation resource
handlers** so that those resources can be provisioned via `cdk deploy` (or raw
`CreateStack`). This is not optional — CloudFormation is the primary way CDK users
interact with AWS, and if a resource type lacks a handler, CDK stacks that use it will
fail.

### How it works

The CloudFormation provisioner lives in `internal/services/cloudformation/provisioner.go`.
It maintains a `resourceHandlers` map from CloudFormation resource type strings
(e.g. `"AWS::SQS::Queue"`) to `resourceHandler` implementations. Each handler has two
methods:

```go
type resourceHandler interface {
    Create(ctx context.Context, cfnRouter chi.Router, cfg *config.Config,
           props map[string]interface{}, rCtx resourceContext) (physicalID string, attrs map[string]string, err error)
    Delete(ctx context.Context, cfnRouter chi.Router, cfg *config.Config,
           physicalID string, rCtx resourceContext) error
}
```

Handlers dispatch internal HTTP requests through the emulator's own router (via
`httptest.ResponseRecorder`), so they exercise the real service implementation. Three
dispatch helpers exist:

| Helper            | Protocol       | Used by                               |
| ----------------- | -------------- | ------------------------------------- |
| `internalQuery`   | Query/XML      | EC2, IAM                              |
| `internalJSON`    | JSON target    | ECS, EventBridge, KMS, Step Functions |
| `internalRequest` | REST path/JSON | API Gateway, Lambda, S3               |

### Rules

1. **Every resource-creating endpoint must have a CloudFormation handler.** When you add a
   new resource type to a service (e.g. a new `CreateFoo` operation), register a handler in
   `resourceHandlers` at `internal/services/cloudformation/provisioner.go:1233`. If a
   service already has CF handlers for some resource types but not the one you're adding,
   add the missing entry. If the service is entirely absent from `resourceHandlers` (e.g.
   EKS, MSK, Route53), create the handlers and register them — even stub handlers are
   better than nothing.
2. **Physical IDs must match AWS format.** Use the same identifier AWS returns
   (ARN, ID with correct prefix, URL, etc.).
3. **Return `GetAtt` attributes.** If the resource type supports `Fn::GetAtt`, return the
   relevant attributes from `Create` so that cross-resource references resolve correctly.
4. **Implement `Delete`.** Stack deletion must clean up all provisioned resources.
5. **Stub what you can't implement yet.** If a resource type is recognised by CDK but not
   yet fully supported, use `&stubResourceHandler{}` — this returns a synthetic physical ID
   so the stack can still complete. Never silently ignore an unknown resource type.
6. **Handler files live in the cloudformation package.** Group handlers by service:
   `provisioner_ec2.go`, `provisioner_apigw.go`, `provisioner_ecs.go`,
   `provisioner_resources.go` (for smaller services).

### Verifying CF compliance when adding a service or endpoint

When you add a new service or a resource-creating endpoint, verify that:

1. The `resourceHandlers` map in `provisioner.go` has an entry for the corresponding
   CloudFormation resource type (e.g. `"AWS::SQS::Queue"`)
2. The handler's `Create` method dispatches an internal HTTP call through the emulator's
   router — it should exercise the real service implementation, not short-circuit
3. `Delete` is implemented and removes the resource from the store
4. The physical ID format matches what AWS returns (check the real AWS documentation)
5. If the service uses a new dispatch protocol not covered by the three existing helpers
   (`internalQuery`, `internalJSON`, `internalRequest`), add the new helper to
   `provisioner.go`

**Every emulated service** that creates resources now has at least a stub handler in the
`resourceHandlers` map. Real (non-stub) handlers dispatch to the emulated service
implementation via the appropriate protocol helper and return the correct physical ID
and `Fn::GetAtt` attributes. See
[provisioner_json_coverage.go](../internal/services/cloudformation/provisioner_json_coverage.go)
and
[provisioner_query_rest_coverage.go](../internal/services/cloudformation/provisioner_query_rest_coverage.go)
for the most recently added handlers.

**Service implementation tiers and CF handler requirements:** Every service that reports
itself as `StatusSupported` or `StatusPartial` in `capabilities_dev.go` must be at least
**inert tier** — resources exist as metadata, can be created/listed/updated/deleted
as real AWS would, but don't "do" anything (no side effects like actually sending email
or provisioning containers). The CF provisioner must keep pace: when a service reaches
inert tier, its CF handlers must create real resources through the emulated service,
not just return synthetic stub IDs. When a service advances to **partial** or **full**
tier (resources have real side effects — e.g. Docker containers, actual message
delivery), the CF handlers should reflect that by passing through all relevant
configuration properties.

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

| Change                                                       | Bump  |
| ------------------------------------------------------------ | ----- |
| Breaking API change (env var rename, response format change) | MAJOR |
| New endpoint, new service, new feature                       | MINOR |
| Bug fix, performance improvement, documentation              | PATCH |

**Every PR that changes shipped runtime behaviour must update `CHANGELOG.md`.**

`CHANGELOG.md` is used as the basis for GitHub release notes. Keep it focused on
changes users need to know about when they install or run Overcast: new services,
new endpoints, AWS compatibility fixes, user-visible bug fixes, config/env var
changes, Docker/binary packaging changes, performance changes with measured
conditions, and documentation that materially changes user guidance.

Do not add changelog entries for purely internal development changes unless they
affect shipped artifacts or runtime behaviour. Examples that usually do not
belong in release notes: CI-only refactors, test-only changes, local tooling,
code cleanup, non-user-visible refactors, and workflow maintenance.

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

1. Write a **failing test** in `tests/integration/<service>/<service>_test.go` (GWT form)
2. **For new services using the typed pattern** (see [How to add a service](#how-to-add-a-service)):
   - Add request/response types and the codec-agnostic handler function to `typed_logic.go`
   - Register the operation in `typed_ops.go` via `op.NewTyped[In, Out]("OperationName", s.handlerTyped)`
   - The `Dispatch`/`DispatchQuery` method in `service.go` already routes through the typed dispatcher — no additional wiring needed
3. **For existing services with legacy dispatch** (see [Smithy wire protocols](./docs/smithy.md)):
   - Add request/response types to `handler.go` — match AWS SDK wire format exactly (casing matters)
   - Add handler method; wire the route or dispatch case
4. Add state helpers to `store.go` if needed
5. If the endpoint creates a new resource type, add or update the CloudFormation resource handler in `internal/services/cloudformation/` — see [CloudFormation integration](#cloudformation-integration). Even if the service is not yet in `resourceHandlers`, you must at minimum register a `&stubResourceHandler{}` entry so that CDK stacks using the service don't fail. Register the handler in `resourceHandlers`, return the correct physical ID and `GetAtt` attributes, and implement `Delete`.
6. Update `capabilities_dev.go` for the service — change or add the `Capability` entry for this operation to `StatusSupported` (or `StatusPartial`/`StatusWIP` if incomplete).
   Keep this file current whenever operations are added, removed, renamed, or implementation status changes; this metadata is the source of truth consumed by capgen/docgen for generated service docs and `STATUS.md` coverage output.
   Then regenerate the static snapshot and refresh the docs:
   ```sh
   make generate-caps   # regenerates internal/capabilities/all.gen.go
   make docs            # rewrites the capabilities table in docs/services/<service>.md
   make check-caps      # verifies all dispatcher entries have a matching capability
   ```
   > **Do not manually edit the table in `docs/services/<service>.md`.** Everything between the `<!-- BEGIN overcast:capabilities -->` and `<!-- END overcast:capabilities -->` markers is overwritten by `make docs`. Edit `capabilities_dev.go` and re-run `make docs` instead.
   >
   > **AWS Docs links** are auto-generated from the `serviceDocsBaseMap` in `cmd/capgen/main.go` — no per-operation `DocsURL` is needed for most operations. If a service is missing from that map, add it. Use the `DocsURL` field on a `Capability` entry only to override the link for a specific operation (e.g. when the URL pattern differs from the service base).
7. Add entry to `CHANGELOG.md` under `[Unreleased]`
8. Add or extend the corresponding test group in `compat/suites/node-js-sdk/src/groups/<service>.ts` to cover the new endpoint
9. **Web UI** — if the new endpoint exposes data the user would want to see or manage:
   - Update the service's list/detail pages in `web/src/features/<service>/` (or create them if they don't exist)
   - Add topology nodes/edges in `internal/router/topology.go` if the endpoint creates a new resource type that has relationships to other services
   - Wire SSE cache invalidation in `web/src/hooks/use-event-stream.ts` so the UI updates in real time when the resource is created/deleted
10. `make test` — all tests must pass with `-race`

> [!NOTE]
> **Windows / dev-container:** `go test -race ./...` (full workspace) can hang or be very
> slow when the source is on a Windows host volume (e.g. `E:\`) because the race detector
> rebuilds everything and every file I/O crosses the Hyper-V boundary. The Vite polling
> watcher makes this worse.
>
> Recommended workflow on Windows hosts:
>
> - During active dev, run targeted tests without `-race`:
>   `go test -count=1 ./tests/integration/s3/` etc.
> - Run the full race-enabled suite (`make test`) only before pushing/merging — ideally
>   inside the container where the filesystem is local:
>   `docker compose -f docker-compose.dev.yml run --rm test`

---

## How to add a service

1. Create `internal/services/<n>/` with the standard file layout:
   - **`service.go`** — `Service` struct, `New`, route registration, `Dispatch`/`DispatchQuery` methods with codec dispatch
   - **`typed_ops.go`** — `typedOps()` returning `map[string]op.Operation` via `op.NewTyped[In, Out]` registrations; also `Operations()` and `SupportedProtocols()` for the `ProtocolService` interface
   - **`typed_logic.go`** — codec-agnostic handler functions (`func(ctx, *Input) (*Output, *protocol.AWSError)`) and the request/response types
   - **`store.go`** — state access, JSON serialisation
   - `handler.go` / `handler_stubs.go` — only if there is legacy dispatch code (existing services); new services should NOT create these files
   - `capabilities_dev.go` — `//go:build dev` operation inventory
2. **All new services must use the typed dispatch pattern from the start** (see [Smithy wire protocols](./docs/smithy.md)). The `Dispatch` (or `DispatchQuery` for Query-protocol services) method must check `codec.FromContext(ctx)` at the top and route to the typed handler when a codec is present. The legacy `handler.go` / `handler_stubs.go` split only exists for older services that predate the codec infrastructure — do not create these files in new services. See `internal/services/scheduler/` (REST-path) or `internal/services/ecr/` (JSON-target) as canonical examples.
3. Implement `router.Service` interface; append to `allServices` in `internal/router/router.go`. For JSON-protocol services implement `router.TargetDispatcher` (`TargetPrefix()` + `Dispatch()`). For Query-protocol services implement `router.QueryDispatcher` (`DispatchQuery()`). For REST-path services implement `PathPrefixService`.
4. **Respect the startup budget.** `<svc>.New()` and any `Init*` method called from `router.New()` must be pure field assignment — no store reads, no network I/O, no DDL, no synchronous file reads, no goroutines that do work before their first tick. See [docs/performance.md § Startup budget — rules for service authors](./docs/performance.md#startup-budget--rules-for-service-authors) for the full rule set and the lazy-init pattern.
5. Create `internal/services/<n>/capabilities_dev.go` — declare every operation the service exposes, with the correct `Status` for each. Use `//go:build dev` at the top. See `internal/services/sqs/capabilities_dev.go` as the canonical example. Then generate and check:
   ```sh
   make generate-caps   # adds the new service to internal/capabilities/all.gen.go
   make docs            # creates the capabilities table in docs/services/<n>.md
   make check-caps      # optional: only works for dispatcher-based services
   ```
6. Write P1 tests in `tests/integration/<n>/<n>_test.go`
7. Add CloudFormation resource handlers for every resource type the service creates — register them in `resourceHandlers` in `internal/services/cloudformation/provisioner.go`. If the service creates resources that AWS has CloudFormation types for (which is nearly always the case), you must add the entries. At minimum, use `&stubResourceHandler{}` for resource types you can't fully implement yet — this lets CDK stacks succeed while the implementation is incomplete. See [CloudFormation integration](#cloudformation-integration) for the full rules, dispatch helpers, and verification checklist.
8. Create `docs/services/<n>.md` using the template in `docs/README.md`. Add the sentinel markers (`<!-- BEGIN overcast:capabilities -->` / `<!-- END overcast:capabilities -->`) and run `make docs` to populate the capabilities table automatically. Everything between those markers is overwritten on every run — never edit it by hand. Any prose that belongs in the doc (behaviour notes, caveats, example snippets) must live **outside** the markers.
9. Add service to README.md table and `CHANGELOG.md`
10. Create `compat/suites/node-js-sdk/src/groups/<n>.ts` with compat tests covering all P1 operations and register the group in `compat/suites/node-js-sdk/src/index.ts`; add the service to the CLI suite if applicable (`compat/suites/cli/`)
11. **Web UI** — consider whether developers using Overcast would find it useful to see or administer this service's resources from the management console (most CRUD-style services qualify; internal plumbing like STS usually does not). If yes:

- Add an entry to `SERVICES` in `web/src/lib/service-registry.ts`. This is the **single registration point** — `nav-services.ts` (sidebar + search) and `dashboard.tsx` (dashboard cards) both derive from it automatically. Set the relevant fields:
  - `to`, `category`, `description` — required for sidebar navigation
  - `dashboardDescription` — longer card description (falls back to `description`)
  - `dashboardLabel` — alternate dashboard label (falls back to `label`, e.g. `"EC2 / VPC"`)
  - `docKey` — enables the docs button on dashboard cards
  - `nav: false` — omit from sidebar but still show a dashboard card (e.g. KMS, STS)
  - `dashboardCard: false` — omit from dashboard but still show in sidebar (e.g. WAF, CloudWatch)
- Create list and detail pages in `web/src/features/<n>/` and `web/src/routes/<n>/` (follow an existing service like SSM or KMS as a template)
- Add topology nodes and edges in `internal/router/topology.go` so the service appears on the system map with its resource relationships
- Add SSE event types and wire cache invalidation in `web/src/hooks/use-event-stream.ts` so the UI updates in real time
- Add an AWS SDK client factory in `web/src/services/aws-clients.ts`; if the service needs a custom BFF route (beyond simple JSON proxy), add a handler in `internal/bff/bff.go` and register it in `bff.NewHandler`

---

## Service package structure

Within a service package, split files by **lifecycle stage and concern** — never by individual operation, never by using subfolders (subfolders = separate packages, which breaks access to private types).

### Typed pattern (required for all new services)

| File                  | Contains                                                                       |
| --------------------- | ------------------------------------------------------------------------------ |
| `service.go`          | `Service` struct, `New`, `Dispatch`/`DispatchQuery` with codec check at top    |
| `typed_ops.go`        | `typedOps()` → `map[string]op.Operation`, `Operations()`, `SupportedProtocols()` |
| `typed_logic.go`      | Codec-agnostic handlers (`func(ctx, *In) (*Out, *protocol.AWSError)`) + types  |
| `typed_ops_test.go`   | Handler unit tests (typed path)                                                |
| `store.go`            | State access, JSON serialisation                                               |
| `capabilities_dev.go` | `//go:build dev` — operation inventory                                         |

### Legacy pattern (existing services only — do not use for new services)

| File                  | Contains                                                                       |
| --------------------- | ------------------------------------------------------------------------------ |
| `service.go`          | `Service` struct, `New`, route registration                                    |
| `handler.go`          | Dispatcher methods + **fully implemented** handlers only                       |
| `handler_stubs.go`    | All `NotImplementedXML`/`NotImplementedQueryXML`/`NotImplementedJSON` stubs    |
| `handler_<group>.go`  | Implemented handlers for one feature group, when that group exceeds ~200 lines |
| `store.go`            | State access, JSON serialisation                                               |
| `types.go`            | Domain types and error constructors, when `store.go` grows large               |
| `capabilities_dev.go` | `//go:build dev` — operation inventory for MCP, docs, and coverage checks      |

**Rule: `handler.go` must never contain a stub.**
Stubs live in `handler_stubs.go`. When implementing an operation, _move_ its method body from `handler_stubs.go` into `handler.go` (or into the appropriate `handler_<group>.go`). This makes `handler.go` a complete, accurate inventory of what works — a reader should be able to tell at a glance what is implemented without scrolling past placeholder methods.

**Rule: new services must never create `handler.go` or `handler_stubs.go`.**
These files are artifacts of the pre-codec architecture. New services use `typed_ops.go` + `typed_logic.go` instead. The `typedOps()` map in `typed_ops.go` is the single source of truth for which operations are implemented — unimplemented operations are simply not registered. The `Dispatch`/`DispatchQuery` method returns 501 generically for any operation not found in `typedOp` (no per-operation stub needed). Both legacy and typed services declare capabilities in `capabilities_dev.go`, where unsupported ops are marked `StatusUnsupported` for documentation/coverage reporting.

**Rule: support metadata must be code-first, not prose-first.**
Human-written docs are important, but they are not a stable machine-readable source of truth for support status.

- The authoritative support inventory should live in code, close to the service implementation.
- For dispatcher-based services, the implemented operation registry in `handler.go` and the remaining stubs in `handler_stubs.go` are the current practical source of truth.
- As coverage reporting matures, each service should expose machine-readable support metadata (service name, operation list, implementation state, notes, tier, and optional CloudFormation/UI links) from code rather than relying on Markdown parsing.
- `capabilities_dev.go` is the authoritative per-service operation inventory today; keep it accurate at all times.
- capgen/docgen, MCP coverage tools, status surfaces, and generated docs consume that code-derived metadata or a generated manifest derived from it.
- Human-facing docs in `docs/services/` and summary files such as `STATUS.md` should be generated from or validated against the code-derived metadata in tests or checks.

Preferred direction:

- Keep prose for explanation, caveats, and usage notes.
- Keep support status in code.
- Fail CI when machine-readable support metadata and docs drift.

**Rule: do not add manual operation tables to service docs.**
`docs/services/<service>.md` already contains a generated summary table and a per-endpoint breakdown (produced by `make docs` from `capabilities_dev.go`). Do not add a hand-written duplicate above or alongside the generated block — they will drift immediately and confuse contributors. If the generated table is missing a column or status you need, add it to `capabilities_dev.go` and extend `capgen`/`docgen` instead of writing a parallel table by hand.

**Rule: never hand-edit generated status/coverage tables.**
The following sections are generated and must only be updated via tooling:

- `STATUS.md` block between `<!-- BEGIN overcast:status -->` and `<!-- END overcast:status -->`
- service-doc capability blocks in `docs/services/<service>.md` between `<!-- BEGIN overcast:capabilities -->` and `<!-- END overcast:capabilities -->`

After changing capabilities or operation support, run:

```bash
make docs
```

This command runs the capgen/docgen pipeline that regenerates service capability blocks and the `STATUS.md` coverage table from `capabilities_dev.go`.

If you changed docs manually and are unsure whether generated sections drifted, re-run `make docs` before committing.

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

## Web UI standards

### API access policy (SDK-first)

For Web UI data access, use AWS SDK clients wherever possible.

- Use service clients from `web/src/services/aws-clients.ts` for AWS API
  operations and AWS-compatible resources.
- Use direct `fetch` calls only for Overcast-specific extension
  endpoints (for example, `/_*` internal APIs such as topology,
  observability, or emulator-only tooling endpoints).
- Do not replace available AWS SDK calls with ad-hoc `fetch` wrappers;
  keep AWS-surface behavior and typing anchored to SDK clients.

### Frontend — Tailwind CSS v4

The web UI uses **Tailwind CSS v4**. When writing or editing component styles:

1. **Always prefer canonical Tailwind classes** over arbitrary-value syntax (`[…]`).
   - Good: `translate-y-0.5`, `gap-2`, `p-4`, `text-sm`
   - Bad: `translate-y-[2px]`, `gap-[8px]`, `p-[16px]`, `text-[14px]`
2. **Arbitrary values are a last resort** — only use square-bracket syntax when there is genuinely no canonical class available (e.g. a one-off brand colour or a value outside the default scale).
3. **Use Tailwind v4 syntax** — Tailwind 4 changed some conventions. Prefer `*:` (universal child selector variant) over `[&>…]` when targeting children. Consult the [Tailwind v4 docs](https://tailwindcss.com/docs) when unsure.
4. **Run the canonical upgrade** if you notice non-canonical classes: `npx @tailwindcss/upgrade`.

### Topology map methodology

The system map is both a **diagnostic surface** and a **graph-based workspace** for
building, testing, and iterating on stacks. It is not a frame-perfect replay of
backend state. Its job is to make distributed behaviour legible at a glance: what
connected to what, what just happened, and how a developer can explore, tweak, and
reason about a stack by interacting with the graph directly.

That means the topology map should prefer **observability over literal timing** for
fast transient actions.

It also means map interactions should support fast iteration. The graph is not just a
read-only status board; it is a visual way to inspect resources, trigger actions,
follow relationships, and refine a stack while seeing the consequences in context.

Rules:

1. **Dilate only genuinely fast transitions.** If an action happens too quickly to be
   perceived in the UI, keep its visual state alive long enough to be seen.
2. **Do not slow states that are already human-visible.** Long-running or naturally
   observable states should render honestly and should not be artificially prolonged.
3. **Preserve sequencing even when dilated.** A node should still show the correct
   order of transitions (`visible` → `in-flight` → `done`, `idle` → `active`, etc.).
   Time dilation is for readability, not for inventing new lifecycles.
4. **Use one visual-state model per node type.** Counts, badges, row states, pulses,
   and ghost rows for the same resource should be derived from the same visual logic.
   Never let the header say one thing while the embedded detail list says another.
5. **Keep the AWS API truthful.** Time dilation belongs only in the map UI and other
   emulator-specific observability surfaces. Never change AWS-compatible API behaviour
   or backend state timing to satisfy the map.

Preferred techniques:

- **Ghost rows / tombstones** for recently removed items so deletes remain visible.
- **Short TTL pulses / edge glows / write flashes** for events that would otherwise be
  imperceptible.
- **Visual dwell windows** for extremely short intermediate states, such as a message
  that is received and deleted too quickly to ever be noticed as in-flight.
- **Client-side countdowns or decay** when the goal is to communicate that a transient
  state is draining away rather than disappearing instantly.

Node-level QOL guidance:

1. **Design each node around the most useful developer actions for that resource.**
   The node should expose the highest-value interaction a developer is likely to want
   in the middle of an iterative workflow.
2. **Prefer direct, contextual actions over navigation when the task is small and
   frequent.** If a developer commonly wants to send a message, publish a payload,
   invoke a function, inspect recent logs, or peek a queue, those actions should be
   considered for the node itself rather than forcing a page transition first.
3. **Keep actions intuitive and resource-native.** A node should feel like a compact,
   graph-local version of the service: queues send and receive messages, topics
   publish, functions invoke and show logs, log groups expose streams, and so on.
4. **Bias toward actions that help exploration, testing, and iteration.** Prioritise
   features that let a developer try something quickly, observe the result in context,
   and continue iterating without losing their place on the graph.
5. **Do not overload nodes with low-value controls.** Add actions deliberately.
   If an interaction is rare, destructive, configuration-heavy, or easier to
   understand on the dedicated resource page, keep it there.
6. **Surface state and action together when possible.** The best node interactions
   let a developer act and immediately see the effect in the same place.
7. **Treat node space as scarce.** Every always-visible bit of node UI should earn
   its place by being useful, sensible, and immediately understandable in context.

When deciding whether a node needs a QOL action, ask:

- What is the first thing a developer wants to do with this resource while looking at the map?
- Can that action be completed safely and clearly without leaving the graph?
- Will doing it on-node make the graph feel more useful as a stack-building and debugging workspace?
- Is the action common enough to justify persistent space in a compact node UI?
- Given the tight space inside nodes, does each surfaced detail justify occupying that space?

Examples:

- SQS messages may remain visually `in-flight` for a short dwell window on the map,
  then transition to a crossed-out `done` ghost row, even if the real delete already
  happened.
- SQS nodes may expose send-message and queue-peek interactions directly on the node
  because they are common, fast feedback loops during development.
- SNS nodes may expose publish-on-node because testing fan-out is a frequent graph-local action.
- Lambda nodes may expose test invoke and recent-log access because developers often
  want to trigger a function and inspect its effect without losing graph context.
- Lambda instances, event pulses, and write bursts may linger briefly after the raw
  event so developers can understand what just occurred.
- CloudWatch log stream activity dots may stay active for a short recent-activity
  window rather than dropping to idle immediately after a write.

When adding or changing topology-map behaviour, ask:

- Would a developer be able to notice this transition without slowing it down?
- If not, what is the smallest visual dwell that makes it understandable?
- Are the node badge, counters, list rows, and animations all telling the same story?

### Service home screen

Every service list/home page **must** include a `ServiceDocsButton` in its `PageHeader`
actions. This button opens the Overcast docs modal for the service, linking users to the
endpoint support matrix and AWS documentation.

```tsx
// In the component:
const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

// In PageHeader actions (always first in the action group):
<ServiceDocsButton
  service="elasticache"   // matches the docs/services/<service>.md filename
  label="ElastiCache"
  open={docsOpen}
  onOpen={openDocs}
  onClose={closeDocs}
/>
```

See [function-list.tsx](web/src/features/lambda/components/function-list.tsx) as the
canonical example. Apply this to every new service's home component and retrofit it to
any existing service page that is missing it.

### Global search

Every service at **inert tier or above** must register a search contributor so its
resources appear in the global search (⌘K / Ctrl+K).

1. Create `web/src/lib/search-contributors/<service>.ts` using `createSearchContributor`:

```ts
import { myService } from "@/services/api";
import { createSearchContributor } from "./create-contributor";
import type { MyResource } from "@aws-sdk/client-my-service";

createSearchContributor<MyResource>({
  id: "myservice",
  cacheKey: (ep) => [ep.baseUrl, ep.region, "myservice", "resources"] as const,
  fetchAll: () => myService.listResources(),
  matchFields: (r) => [r.name, r.arn],
  toResult: (r) => ({
    id: `myservice:${r.name}`,
    label: r.name ?? "",
    sublabel: r.arn,
    service: "My Service",
    serviceKey: "/myservice",
    type: "Resource",
    href: `/myservice/${encodeURIComponent(r.name ?? "")}`,
  }),
});
```

2. Import it in `web/src/lib/search-contributors/index.ts`.

The `cacheKey` **must** include `ep.baseUrl` and `ep.region` as the first two elements —
this matches the key shape produced by feature-level `data.ts` query options, so the
contributor can read from the cache without a network round-trip.

### service-registry.ts and unsupported-services.ts

When a service moves from "unsupported" to any implemented tier:

- **Remove** its entry from the `CATALOG` array in `web/src/lib/unsupported-services.ts`.
- **Add** a full entry to `SERVICES` in `web/src/lib/service-registry.ts` (including `to`, `category`, `description`). The sidebar (`nav-services.ts`) and dashboard card list (`dashboard.tsx`) both derive from this automatically.

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
- [ ] `capabilities_dev.go` updated and `make generate-caps` re-run if any operations changed
- [ ] `make check-caps` passes (for dispatcher-based services)
- [ ] `make docs-check` passes (no uncommitted doc drift)
- [ ] `docs/services/<service>.md` — capabilities table regenerated via `make docs` (never edited by hand); any prose/behaviour notes outside the sentinel markers are up to date
- [ ] CloudFormation resource handlers registered in `resourceHandlers` for every new resource type — see [CloudFormation integration](#cloudformation-integration)
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

Check [GitHub Issues](https://github.com/Neaox/overcast/issues) for open work.
Look for the `good first issue` label if you're new, or filter by priority
(`P1`, `P2`, `P3`) and effort (`small`, `medium`, `large`) to find something
that fits.
