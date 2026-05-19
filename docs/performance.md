# Performance and memory guide

Overcast aims to be fast and lean: sub-50ms startup, under 15 MiB at idle,
and low per-request overhead. CI pipelines should not wait for the emulator.

This guide covers the patterns to follow and pitfalls to avoid.

---

## Goals

| Metric                                    | Target                                       |
| ----------------------------------------- | -------------------------------------------- |
| Startup time                              | < 50ms (currently ~22ms p50, hybrid backend) |
| Idle memory                               | < 15 MiB                                     |
| Docker image (slim)                       | < 40 MiB — Go binary only, no web UI         |
| Docker image (console)                    | < 100 MiB — includes web management console  |
| Request overhead (emulator-added latency) | < 1ms for simple operations                  |

Performance claims above are measured with default settings, including `OVERCAST_EKS_MODE=mock`.
The opt-in EKS live mode (`OVERCAST_EKS_MODE=live`) launches k3s containers and has materially higher
startup and memory cost by design; treat that mode as a separate operating profile.

### Per-backend startup (cold start, empty data dir)

All four storage backends defer the modernc/sqlite cold-migrate cost
(~200–340 ms) off the critical path. The migration runs in a background
goroutine; the first DB-touching request blocks on it.

| Backend               | Internal startup_ms | Wall spawn-to-ready |
| --------------------- | ------------------- | ------------------- |
| `memory`              | 1–2 ms              | ~40 ms              |
| `hybrid` (default)    | 4–5 ms              | ~40 ms              |
| `wal` (SQLite, async) | 5–8 ms              | ~40 ms              |
| `persistent` (SQLite) | 2–6 ms              | ~40 ms              |

Measured 2026-04-17 in the dev container (Debian 12, x86_64, Go 1.23,
modernc/sqlite pure-Go driver, all 27 services registered, no SDK
clients connected) with `OVERCAST_STATE=<backend>`,
`OVERCAST_DATA_DIR=<empty tmp>`, polling `/_metrics` every 5 ms from a
sibling Go process. Wall time is `os.Process.Start` → first HTTP 200
on `/_metrics`. Internal startup is `startup_duration_ms` from that
endpoint (package-init `startTime` → end of `router.New()`). Numbers
are best-of-5 cold runs (fresh `tmp` dir each iteration); warm-cache
runs are 1–2 ms faster across the board and not reported.

---

## Documenting performance claims

Every performance claim in this project — in README, docs, changelogs, or
commit messages — **must** include the measurement conditions. A number
without context is meaningless and can mislead users.

For each claim, document at minimum:

1. **What is measured** — the exact metric (wall-clock startup, heap allocated,
   RSS, image size, p99 latency, etc.).
2. **How it is measured** — tool, command, or code path that produces the
   number (e.g. `/_metrics` endpoint, `runtime.MemStats`, `docker images`,
   `go test -bench`).
3. **Environment** — OS, architecture, Go version, container vs bare-metal,
   number of enabled services, store backend, and any other variable that
   materially affects the result.
4. **What is included / excluded** — e.g. startup time includes service
   registration but excludes background SQLite seeding and SMTP bind;
   idle memory is measured before any requests are served.

### Current measurement methodology

**Startup time (`startup_duration_ms`):**
`var startTime = time.Now()` in `internal/router/debug.go` (package-level
init) → `readyTime = time.Now()` at the end of `router.New()` in
`internal/router/router.go`. This measures the wall-clock time to construct
all services and wire all routes. It **excludes** background work that is
deferred past `readyTime`: SQLite schema migration (runs in a goroutine on
all SQLite-backed stores — `persistent`, `wal`, and `hybrid`; first
DB-touching request blocks until it finishes), DynamoDB SQLite DDL (lazy,
runs on first use), SMTP mock server bind (goroutine), HybridStore
SQLite→memory seeding (goroutine), ECS built-in capacity-provider seeding
(lazy, runs on first capacity-provider request), and API Gateway
domain-registry hydration (lazy, runs on first domain-name request).
Reported via `GET /_metrics`.

**Idle memory (`sys_bytes`, `heap_alloc_bytes`):**
Captured from `runtime.MemStats` via `GET /_metrics` after startup, before
any client requests. `sys_bytes` is total memory obtained from the OS
(≈ RSS). `heap_alloc_bytes` is live heap objects only.

**Docker image size:**
`docker images --format '{{.Size}}'` after `docker build`. Multi-stage build;
slim target includes only the Go binary (Lambda functions run in their own
Docker containers pulled from `public.ecr.aws/lambda/{runtime}`). Console
target adds Node.js (for the BFF server), the web UI SPA, and BFF bundle.

---

## Startup budget — rules for service authors

Startup time is a shared resource. Every service runs inside the same
`router.New()` call, so any expensive work a service does at construction
time is added directly to every user's cold-start latency. These rules
exist so we can keep startup under 50 ms even as we grow to 50+ services.

### Hard rules (MUST NOT)

A service's `New()` and any `Init*` method called from `router.New()` **MUST NOT**:

1. **Read from the state store.** No `store.Get`, `store.List`, `store.Scan`.
   The `HybridStore` seeds memory asynchronously; a read during `router.New()`
   blocks on that seed and pessimises startup for every user. If you need to
   hydrate an in-memory index from persisted state, do it lazily on first
   request — see the "Lazy initialisation pattern" below.
2. **Perform synchronous network I/O.** No `http.Get`, no `net.Dial`, no
   Docker ping, no DNS lookup. If an external resource is required, probe
   it in a background goroutine (see `docker.Supervisor.Probe`).
3. **Bind a listening socket in the foreground.** Listening is cheap, but
   `Accept` loops must run in a goroutine that `router.New()` does not
   wait on. Example: the SMTP mock server binds and serves in a goroutine;
   the handler uses a `LazyMailer` that blocks only if an actual email is
   sent before bind completes.
4. **Run schema migrations or DDL synchronously.** SQLite `CREATE TABLE`
   and index creation can take tens of milliseconds. Gate DDL behind a
   `sync.Once` fired from the handlers that need the table — see
   `internal/services/dynamodb/item_store.go` for the canonical pattern.
5. **Spawn long-lived goroutines that do work before their first tick.**
   A `time.NewTicker` plus `for { select { case <-ticker.C: ... } }` is
   fine (negligible until the first tick). Anything that does a unit of
   work immediately upon goroutine start is not — defer it.
6. **Read files from disk, unless the file is small, bounded in size,
   and already required for correctness at startup** (e.g. TLS cert when
   TLS is enabled). Config files, fixture data, and user content are all
   out of bounds.
7. **Compile regular expressions or parse large literals at request time
   if they can be compiled at package init.** Conversely, don't add
   package-init regex compilations for features used by <10% of users —
   use `sync.OnceValue` so the cost is paid only by users who exercise
   the feature.
8. **Call another service's `Init*` or "reload from store" method
   synchronously from `router.New()`.** Even if your own constructor is
   pure, a downstream `ReloadAll`/`Hydrate` that touches the store will
   block on the HybridStore seed and silently re-pessimise startup. If a
   reload is required for correctness, run it in a `go func()` and have
   the consumer block on a ready signal only when the relevant feature
   is actually used. Reference: `lambdaSvc.InitESMDelivery` wraps
   `mgr.ReloadAll` in `go` so ESM hydration runs in the background.
9. **Capture a live `*sql.DB` handle in a service constructor when the
   handle is owned by an asynchronously-opened backend (HybridStore).**
   The handle does not exist yet when `router.New()` runs. Take a
   `func() *sql.DB` resolver instead and call it inside your `sync.Once`
   `init()` block. Reference: `internal/services/dynamodb/item_store.go`
   and `stream_store.go` accept `dbFn func() *sql.DB`.

### Soft rules (SHOULD)

A service's `New()` **SHOULD**:

1. **Be a pure field assignment** — take in dependencies, return a
   struct. No method calls beyond trivial constructors (`newHandler(...)`,
   `serviceutil.NewServiceLogger(...)`).
2. **Assume nothing about call order** — other services' `Init*` methods
   may run after yours. If you need to wire to another service, expose an
   `Init*` method that the router calls after all `New()` calls complete.
3. **Defer any work that depends on the state store or network** — even
   if it seems fast today. A fast path on an empty dataset becomes a slow
   path once a real user hits the emulator with persisted data.

### Lazy initialisation pattern

When a service has work that genuinely needs to happen once before the
first relevant request (e.g. schema DDL, seed data, cache warming), wrap
it in a `sync.Once` on the `Handler` and call the wrapper at the start
of every handler that depends on it.

```go
// internal/services/<svc>/handler.go
type Handler struct {
    // ... other fields ...
    readyOnce sync.Once
}

// ensureReady runs one-time setup on first use. Called from every
// handler that depends on the setup — subsequent calls are free
// (sync.Once is ~1 ns on the fast path).
func (h *Handler) ensureReady() {
    h.readyOnce.Do(func() {
        // expensive work: DDL, seeding, registry hydration, etc.
    })
}

// internal/services/<svc>/handler_foo.go
func (h *Handler) Foo(w http.ResponseWriter, r *http.Request) {
    h.ensureReady()
    // ... normal handler ...
}
```

Reference implementations:

- **DynamoDB SQLite DDL** — `internal/services/dynamodb/item_store.go`
  (`init()` method, `sync.Once` per backend).
- **ECS built-in capacity providers** — `internal/services/ecs/handler.go`
  (`ensureBuiltinProviders`, called from capacity-provider handlers).
- **API Gateway domain-registry hydration** —
  `internal/services/apigateway/handler.go` (`ensureRegistryHydrated`,
  called from domain-name handlers).

### Deferred work via background goroutines

When the work can run truly in parallel with `router.New()` and doesn't
need to complete before any specific request, spawn a goroutine from
inside the service constructor (or from the router) and **never wait on
it inside `router.New()`**. If callers must eventually observe the
result, use a ready channel and block only at the consumer.

Reference implementations:

- **HybridStore SQLite→memory seed** — `internal/state/hybrid.go`
  (`seedFromSQLite`, `waitLoaded` guards reads, not init).
- **SQLiteStore schema migration** — `internal/state/sqlite.go`
  (`runMigrate` goroutine, `ensureReady` blocks on `<-ready` from each
  public method). The modernc/sqlite parser/codegen init plus the first
  `CREATE TABLE` cost ~200–340 ms on a cold cache; deferring it makes
  `persistent` and `wal` startup-equivalent to `memory` and `hybrid`.
- **SMTP mock server** — `internal/router/router.go` (goroutine calls
  `Listen` + `Serve`; `LazyMailer` blocks only at `Send`).
- **Docker availability probe** — `internal/docker/probe.go` (retries
  in a goroutine; services check `dockerReady` atomic flag).

### How to verify you haven't regressed startup

**Quick (single command — recommended before each release):**

```sh
make bench-startup            # all 4 backends, 5 iterations each
# or with options:
go run ./scripts/bench-startup.go -n 10 -threshold 50 -v
```

The script builds overcast, spawns it with a clean data dir for each
backend, polls `/_metrics`, kills the process, and prints a summary table:

```
Backend       p50       p95       max      mean  │  int-p50  heap-p50   sys-p50
────────────────────────────────────────────────────────────────────────────────────────────
memory       38.2ms    40.1ms    41.0ms    38.8ms  │    1.4ms   4.2 MiB  12.8 MiB
hybrid       39.5ms    42.3ms    43.1ms    40.2ms  │    4.8ms   4.5 MiB  13.1 MiB
wal          40.1ms    43.8ms    44.2ms    41.0ms  │    6.2ms   4.3 MiB  12.9 MiB
persistent   39.8ms    41.5ms    42.0ms    40.1ms  │    3.1ms   4.4 MiB  13.0 MiB
```

Exits non-zero if any backend's p50 wall time exceeds `-threshold`
(default 80 ms). Use this as a CI gate or pre-release sanity check.

**Manual (for ad-hoc investigation):**

1. Build: `make build` (or `go build ./...`).
2. Run with a clean data dir and an empty config:
   `rm -rf /tmp/overcast && ./bin/overcast serve` (or `make run`).
3. Measure 10 cold starts and record p50/max:
   `for i in $(seq 1 10); do rm -rf /tmp/overcast-$i && \
OVERCAST_DATA_DIR=/tmp/overcast-$i ./bin/overcast serve & \
sleep 0.5 && curl -s localhost:4566/_metrics | \
jq .startup_duration_ms && kill $!; done`
4. If p50 increased by >5 ms, identify which phase owns the regression
   and apply one of the patterns above. **Do not merge a change that
   increases startup time without a documented justification in the PR.**

### Diagnosing where startup time goes (`OVERCAST_PROFILE_STARTUP`)

When p50 regresses or you want to understand the breakdown, set the
env var `OVERCAST_PROFILE_STARTUP=1` and run `./bin/overcast serve` once.
Each phase prints to stderr:

```
startup-profile  config.Load                +1.2ms   (=1.2ms)
startup-profile  state.buildStore           +0.6ms   (=1.8ms)
startup-profile  service constructors       +12.8ms  (=14.6ms)
startup-profile  cross-service wiring       +0.4ms   (=15.0ms)
startup-profile  router.New (full)          +22.2ms  (=22.2ms)
startup-profile  sqlite.migrate             +289ms   (background)
```

Anything in the foreground budget that is unexpectedly large points to
one of the hard-rule violations above. Background phases (suffixed
`(background)`) do not block readiness — they're informational.

A common smell: the foreground total is small but a service's first
real request is slow because its `sync.Once` `init()` is doing the work
the constructor used to do. That's correct — the cost moved off the
critical path. If first-request latency matters for that service, warm
it from a goroutine instead.

## Memory leaks in Go — what to watch for

Go has a garbage collector, so the classic C-style "forgot to free" leaks don't
apply. But Go has its own leak patterns that are subtle and accumulate silently
in long-running servers.

### 1. Goroutine leaks — the most common

A goroutine that is blocked waiting on a channel that nobody will ever write to
will live forever. The GC cannot collect goroutines — they are roots.

```go
// ❌ Leaked goroutine: if done is never closed, this goroutine lives forever.
go func() {
    for {
        select {
        case msg := <-ch:
            process(msg)
        // Missing: case <-done: return
        }
    }
}()

// ✅ Always respect context cancellation or provide a done channel.
go func() {
    for {
        select {
        case msg := <-ch:
            process(msg)
        case <-ctx.Done():
            return // goroutine exits cleanly when context is cancelled
        }
    }
}()
```

**In Overcast specifically:**

- The Lambda process supervisor goroutine must exit when its context is cancelled.
- The SQS visibility-timeout ticker must be stopped when the message is deleted.
- Any background poller (future event source mapping) must stop on server shutdown.

**How to detect:** `runtime.NumGoroutine()` in tests. The `goleak` linter.
The `/_debug/health` endpoint will include goroutine count when implemented.

### 2. Context not propagated

Always pass `r.Context()` (or a derived context) to every blocking call. If you
don't, the operation keeps running after the client disconnects or the server
starts shutting down.

```go
// ❌ Ignores client disconnection and server shutdown
raw, found, err := s.store.Get(context.Background(), ns, key)

// ✅ Respects the request lifecycle
raw, found, err := s.store.Get(r.Context(), ns, key)
```

### 3. Timers and tickers not stopped

`time.NewTicker` and `time.NewTimer` hold a goroutine and an internal channel
until `Stop()` is called. Always stop them.

```go
// ❌ Timer goroutine leaks if the function returns early.
timer := time.NewTimer(timeout)
if err := doWork(); err != nil {
    return err // timer never stopped
}
<-timer.C

// ✅ Always defer Stop().
timer := time.NewTimer(timeout)
defer timer.Stop()
```

### 4. Unbounded slice/map growth

In-memory queues and caches must have a bound or a TTL. For Overcast:

- SQS queues accumulate messages — this is intentional but document it.
- Large S3 object bodies stored in MemoryStore accumulate in `[]byte` — fine for
  dev use, but warn in the debug endpoint if total state exceeds a threshold.

### 5. String ↔ []byte conversion

Each conversion allocates. Avoid converting in hot paths.

```go
// ❌ Allocates a new string each time
key := string(someBytes)

// ✅ In hot paths, work with []byte throughout
```

---

## Performance patterns

### Avoid allocations in hot paths

Every allocation adds GC pressure. In the request handling path:

```go
// ❌ Allocates a new buffer on every request
buf := new(bytes.Buffer)
json.NewEncoder(buf).Encode(resp)

// ✅ Use json.Marshal which is a single allocation
body, err := json.Marshal(resp)
```

For frequently-called operations, use `go test -bench` to measure before and
after any change that affects allocation patterns.

### Pre-size slices where count is known or estimated

```go
// ❌ Repeated reallocation as the slice grows
var keys []string
for k := range s.data { keys = append(keys, k) }

// ✅ Pre-size when you know the count
keys := make([]string, 0, len(s.data))
for k := range s.data { keys = append(keys, k) }
```

### Use `io.Reader` streaming for large bodies

Don't read entire request bodies into memory for S3 PutObject.
Instead, stream to storage:

```go
// ❌ Loads entire body into memory — bad for large objects
body, err := io.ReadAll(r.Body)

// ✅ For future streaming storage: stream to an io.Writer
// (Current in-memory store requires the full body — this is a known tradeoff)
```

The current `MemoryStore` must hold the full body as `[]byte` for `GetObject` to work.
This is an acceptable tradeoff for local dev. If streaming storage is added later
(e.g. a file-backed store), handlers should stream.

### Measure before optimising

```bash
# Run benchmarks and show memory allocations
go test -bench=. -benchmem -count=3 ./...
make bench
```

---

## Binary size

The binary is built with `-trimpath -ldflags="-w -s -X main.version=$(VERSION)"` which:

- `-trimpath` removes source file paths from the binary (security + size)
- `-w` removes DWARF debug info (~30% size reduction)
- `-s` removes the symbol table (~10% additional reduction)
- `-X main.version=…` injects the version from the `VERSION` file at build time

With `CGO_ENABLED=0` (pure-Go SQLite) there is no system library dependency.
The binary is fully static and can run in a scratch container.

---

## Docker image size

The Dockerfile uses a multi-stage, multi-target build:

- `go-builder`: `golang:1.24-alpine` — cross-compiles the Go binary (not shipped)
- `web-builder`: `node:22-alpine` — builds the SPA and BFF server (not shipped)
- `slim` target: `alpine:3.20` + Go binary only (~36 MB)
- default (console) target: extends `slim` with Node.js, the web UI SPA, and BFF server

Keep the runtime image lean:

- Don't add unnecessary `apk` packages
- Use `--no-cache` on every `apk add` to avoid the package index staying in the layer
- Node.js is only in the console image (for the BFF server); Lambda functions run in their own Docker containers

---

## Running benchmarks

```bash
# All benchmarks
make bench

# Single package
go test -bench=. -benchmem ./internal/state/...

# Specific benchmark with profiling
go test -bench=BenchmarkMemoryStore_Get -benchmem -cpuprofile=cpu.prof ./internal/state/...
go tool pprof cpu.prof
```

When adding a new benchmark, name it `Benchmark<Type>_<operation>` and place it
in the `_test.go` file alongside the unit tests for that package.
