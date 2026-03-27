# Performance and memory guide

Overcast aims to be fast and lean: sub-200ms startup, under 15 MiB at idle,
and low per-request overhead. CI pipelines should not wait for the emulator.

This guide covers the patterns to follow and pitfalls to avoid.

---

## Goals

| Metric | Target |
|--------|--------|
| Startup time | < 200ms |
| Idle memory | < 15 MiB |
| Docker image | < 50 MiB (without Node.js), < 100 MiB (with Node.js for Lambda) |
| Request overhead (emulator-added latency) | < 1ms for simple operations |

---

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

The binary is built with `-trimpath -ldflags="-w -s"` which:
- `-trimpath` removes source file paths from the binary (security + size)
- `-w` removes DWARF debug info (~30% size reduction)
- `-s` removes the symbol table (~10% additional reduction)

With `CGO_ENABLED=0` (pure-Go SQLite) there is no system library dependency.
The binary is fully static and can run in a scratch container.

---

## Docker image size

The Dockerfile uses a multi-stage build:
- Build stage: `golang:1.23-alpine` (large, not shipped)
- Runtime stage: `alpine:3.20` + Node.js + the binary

Keep the runtime image lean:
- Don't add unnecessary `apk` packages
- Use `--no-cache` on every `apk add` to avoid the package index staying in the layer
- The Node.js install is optional — if Lambda support is not needed, remove it

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
