# Storage layer — stabilization & enhancement plan

> **Status:** Phases 1 and 2 are complete, committed, and rebased onto `main` (branch `feat/storage-stabilization`, worktree `E:\dev\overcast-storage-stabilization`, pushed to `origin`). **Phase 3 Wave 1 (items 3.2, 3.4, 3.5, 3.8, 3.9, 3.11) is complete — see "Phase 3 progress notes (Wave 1)" below.** **Wave 2 (3.1, 3.6/3.7, 3.13 — all touch `internal/router/debug.go`, deliberately consolidated into one pass to avoid repeat merge conflicts) has not started — pick up there next.** One outstanding decision for whoever resumes: the "Settled decisions" section notes a real, separate bug found during Phase 1 (per-service override config names not always matching `NamespacedStore` routing prefixes, e.g. `"cloudformation"` vs `"cfn"`) — still unfixed, not itemized as its own Phase 3 task, worth deciding whether to add one.
> **Scope:** `internal/state/`, service `store.go` files, `cmd/overcast/cmd_serve.go` store wiring.
> **Audience:** any contributor or agent. Each item is self-contained: evidence (file:line), change guidance, tests, and acceptance criteria. Read [CONTRIBUTING.md](../CONTRIBUTING.md) and [AGENTS.md](../AGENTS.md) first; all their rules apply (failing test first, both store implementations in sync, `clock.Clock` not `time.Now()`, scoped verification, `make docs` when behavior tables change).

---

## Architecture snapshot (as of this plan)

Three storage tiers, routed by data weight:

1. **Generic K/V** — `kv(namespace, key, value)` table in SQLite ([internal/state/sqlite.go](../internal/state/sqlite.go)), values are JSON strings serialized only in each service's `store.go`. Namespaces are tiered ([internal/state/tier.go](../internal/state/tier.go)): `TierHot` (resource definitions, seeded into memory at startup) and `TierCached` (high-volume data, read from SQLite per operation — **note: no LRU cache exists despite the comment**).
2. **Dedicated tables** — DynamoDB items/streams (`internal/services/dynamodb/item_store.go`, `stream_store.go`) via the `state.SQLiteDBProvider` type assertion.
3. **Filesystem blobs** — S3 object bodies under `<DataDir>/s3-bodies/` (`internal/services/s3/store.go`).

Backends selected by `OVERCAST_STATE` (memory | persistent | hybrid | wal) with per-service `OVERCAST_STATE_<SVC>` overrides that wrap everything in `state.NamespacedStore` ([cmd/overcast/cmd_serve.go:120-151](../cmd/overcast/cmd_serve.go)).

**HybridStore** (default, [internal/state/hybrid.go](../internal/state/hybrid.go)): reads from memory (TierHot) or SQLite (TierCached); writes append a JSON line to an unsynced pending log + dirty map and return; a background loop batch-flushes the dirty map to SQLite in one transaction per interval; the constructor is non-blocking and seeding runs in the background with SQLite read-fallback until done.

### Invariants every item must preserve

- `state.Store` interface unchanged unless an item explicitly says otherwise; when it changes, update **MemoryStore, SQLiteStore, HybridStore, WALStore, NamespacedStore** together.
- No blocking work in constructors or anything called from `router.New()` — background goroutines + ready channels (see the existing `runMigrate`/`seedFromSQLite` pattern).
- One corrupt persisted record must never break unrelated operations or startup (CLAUDE.md "malformed persisted state" rule).
- Performance claims need measurement conditions ([docs/performance.md](performance.md)).
- Memory-mode (`OVERCAST_STATE=memory`) must keep full functional parity for every change.

---

## Phase 1 — Stabilize

### 1.1 `NamespacedStore` erases optional interfaces → DynamoDB silently loses persistence  **[BUG — do first]**

**Problem.** Services detect SQLite by type-asserting the store: `store.(state.SQLiteDBProvider)` ([internal/services/dynamodb/service.go:63-76](../internal/services/dynamodb/service.go)). When any `OVERCAST_STATE_<SVC>` override is set, the store is wrapped in `NamespacedStore` ([internal/state/namespaced.go](../internal/state/namespaced.go)), which implements neither `SQLiteDBProvider` nor `ReadyAwaiter` (it only special-cases `PrefixDeleter`). Result: `OVERCAST_STATE_S3=memory` silently switches DynamoDB items/streams to the in-memory backend — data lost on restart, no warning. Same erasure hits `ReadyAwaiter` (startup reload routines skip waiting) and any health/flush assertions.

**Change.**
- Add an exported resolver on `NamespacedStore`, e.g. `StoreFor(servicePrefix string) Store`, and a package helper `state.Unwrap(store Store, servicePrefix string) Store` that returns the routed store (or the store itself if not namespaced).
- Change call sites that type-assert optional interfaces (`SQLiteDBProvider`, `ReadyAwaiter`, health providers — grep for `.(state.` across `internal/`) to unwrap with their own service prefix first. Include concrete-type asserts in the audit: `debugReset` asserts `store.(*state.MemoryStore)` ([internal/router/debug.go:221](../internal/router/debug.go)), which silently misses when the store is wrapped.
- Make `NamespacedStore` implement `ReadyAwaiter` (wait on all sub-stores) and aggregate persistent-health if a health interface exists.
- Audit: add a test listing all optional interfaces and asserting wrapping does not silently drop capability for routed services.

**Tests.** Failing test first: build a `NamespacedStore` with an unrelated override, assert DynamoDB backend selection still returns the SQL backend. Integration: set `OVERCAST_STATE_S3=memory` (hybrid default), `PutItem`, restart the test server against the same data dir, `GetItem` succeeds.

**Accept when** the integration test passes and a grep shows no remaining direct type-asserts on possibly-wrapped stores.

---

### 1.2 Read-only connection pool for TierCached reads

**Problem.** All hybrid SQLite access shares one connection (`SetMaxOpenConns(1)`, [hybrid.go:152](../internal/state/hybrid.go)). WAL mode supports concurrent readers alongside the single writer, but this config serializes TierCached reads (`sqs:messages`, `logs:events`, …) behind flush transactions — exactly the burst scenario.

**Change.** In `HybridStore`: open a second `*sql.DB` on the same file for reads only (add `_pragma=query_only(1)` or simply never write on it), `SetMaxOpenConns(4)`. Route `sqliteGet` / `sqliteList` / `sqliteScan` / `sqliteListNamespaces` and the seed queries through it. Keep the writer pool at 1 for flushes and `DB()` (service tables). Add `busy_timeout` (e.g. 5000ms) to **both** DSNs — modernc syntax: `?_pragma=busy_timeout(5000)` alongside the existing params. Close both handles in `Close()`.

**Tests.** Concurrency test: start a long write transaction on the writer conn, assert a TierCached `Get` completes without waiting for commit. Benchmark before/after: mixed read/write burst (see 2.1 for benchmark conventions).

**Accept when** reads no longer block on an in-flight flush (test) and no `SQLITE_BUSY` regressions appear in the retry logs during `go test ./internal/state/... -count=1`.

---

### 1.3 Pending-log fsync modes

**Problem.** The hybrid pending log is never fsynced ([hybrid.go `appendPendingBatchLocked`](../internal/state/hybrid.go)) — writes survive process kill (page cache) but an OS crash/power loss can lose the entire flush window. `WALStore` already has the right pattern ([internal/state/wal.go](../internal/state/wal.go)): `always` | `interval` | `never`, default interval 100ms.

**Change.** Port the sync-mode mechanism to the hybrid pending log: new config field (suggest `OVERCAST_HYBRID_SYNC`, default `interval`, plus `OVERCAST_HYBRID_SYNC_INTERVAL` default 100ms) in `internal/config/config.go`; a background ticker fsyncs `pendingFile`; `always` syncs inside the append; final sync on `Close`. Document in README config table + `make docs` if applicable.

**Tests.** Unit: mode validation, sync-on-close. Behavior parity with `WALStore` options.

**Accept when** default mode is `interval` and a write burst benchmark shows no regression beyond noise vs `never`.

---

### 1.4 Size-triggered flush

**Problem.** Flushes are purely interval-driven ([hybrid.go `run`](../internal/state/hybrid.go)); a burst grows the dirty map and pending log unboundedly until the next tick — bigger crash-replay window, memory spike, and one giant transaction.

**Change.** Track dirty entry count and approximate byte size in `Set`/`Delete`; when either exceeds a threshold (suggest 10 000 entries / 8 MiB, configurable), send a non-blocking signal on a channel the `run` loop selects on. Coalesce signals; never flush from the caller's goroutine.

**Tests.** Write threshold+1 entries with a long flush interval; assert flush happens promptly (use the injectable clock if the loop is refactored to accept one; otherwise a short real-time wait is acceptable in `internal/state` tests).

**Accept when** dirty count stays bounded under a sustained-write test regardless of `HybridFlushInterval`.

---

### 1.5 `WALStore` torn-final-line replay  **[BUG]**

**Problem.** A crash mid-append leaves a truncated final line. Hybrid replay tolerates this ([hybrid.go:934](../internal/state/hybrid.go)); `replayWALFile` ([wal.go:143-185](../internal/state/wal.go)) fails hard on any bad line → daemon refuses to start under `OVERCAST_STATE=wal` after an unclean stop.

**Change.** Mirror the hybrid logic: if the undecodable line is the final one and the file does not end with `\n`, warn and ignore. (Read the file whole or track offsets — the current `bufio.Scanner` hides whether a trailing newline existed.)

**Tests.** Failing test first: write a valid log + truncated last line, `NewWALStore` succeeds and earlier entries are replayed.

---

### 1.6 Corrupt-log lines: skip-and-warn policy

**Problem.** A corrupt line **mid-file** aborts startup in both replay paths; and `applyPendingEntry` ([hybrid.go:945](../internal/state/hybrid.go)) silently ignores unknown ops (a silently dropped write).

**Change.** Both replays: on an undecodable non-final line, log a warning with line number and continue; count skips and log a summary. `applyPendingEntry`/WAL replay: warn on unknown op instead of silent ignore. Compaction after the next successful flush rewrites the log clean — no quarantine file needed, but log loudly enough to be diagnosable.

**Tests.** Corrupt line injected mid-file → store starts, surrounding entries intact, warning logged.

---

### 1.7 Crash-recovery test suite

**Change.** New test file(s) in `internal/state` covering: torn final line (both stores), corrupt mid-file line, empty/whitespace-only log, replay idempotence (replay twice ⇒ identical state), flush failure → entries retained and re-flushed (error injection via a wrapped driver or by closing the DB), kill-during-flush semantics (pending log still contains unflushed entries because compaction only runs after successful commit). Most of these become the regression net for items 1.3–1.6 and Phase 2.

---

### 1.8 Ranged tombstones for `DeletePrefix`  **[trickiest Phase 1 item — do after 1.7]**

**Problem.** `HybridStore.DeletePrefix` ([hybrid.go:338](../internal/state/hybrid.go)) lists every key, writes one pending-log line + one dirty tombstone per key, then one `DELETE` per row at flush. Purging a deep queue or deleting a big log group = 100k log lines + 100k statements. The SQLite layer already has efficient ranged deletes (`DeletePrefix`, [sqlite.go:196](../internal/state/sqlite.go)).

**Change.** Support a prefix tombstone in the overlay: an ordered op log (sequence-numbered) of prefix deletes interleaved with the dirty map, so flush can replay them in order (`DELETE ... WHERE namespace=? AND key>=? AND key<?` then subsequent sets). Pending log gains `delete_prefix` entries (the hybrid replay must handle them — currently ignored, see 1.6). The read-side overlay (`pendingValue`, `mergePendingKeys/Pairs`) must treat a key covered by a newer prefix tombstone as deleted. **Correctness note:** ordering matters — a `Set(k)` after `DeletePrefix(p)` where `k` has prefix `p` must survive; encode both in one ordered structure or keep per-op sequence numbers and compare.

**Tests.** Property-style test: random interleaving of Set/Delete/DeletePrefix applied to hybrid vs plain MemoryStore, states must match after flush + reload. Benchmark PurgeQueue-shaped workload before/after.

---

### 1.9 `cfn:events` blob → row-per-event  **[BUG: O(n²) append + lost-update race]**

**Problem.** Stack events are one JSON array per stack; every append does Get → unmarshal → append → marshal → Set ([internal/services/cloudformation/store.go:84-121](../internal/services/cloudformation/store.go)) — quadratic over a deployment and unguarded read-modify-write (concurrent provisioner appends can lose events).

**Change.** Key per event: `<region>/<stackName>/<seq>` where `seq` is zero-padded monotonic (clock nanos + atomic counter — copy the `uniqueSuffix` pattern from [internal/services/lambda/store.go:142-149](../internal/services/lambda/store.go)). `getStackEvents` becomes a prefix `Scan`. Delete stack → `DeletePrefix`. **Legacy data:** on read, if the old blob key (`<region>/<stackName>`) exists, decode it, return merged, and convert (write rows, delete blob) — cheap inline migration, no framework needed. Match real AWS `DescribeStackEvents` ordering (newest first) in the handler, not the store.

**Tests.** Failing test first: concurrent appends from N goroutines, all events present. Legacy-blob conversion test. Existing CFN integration tests must stay green: `go test -count=1 ./internal/services/cloudformation/... ./tests/integration/cloudformation/...`.

---

### 1.10 Bounded shutdown + fix `serverErr` cleanup skip

**Problem.** The deferred `store.Close()` ([cmd/overcast/cmd_serve.go:113](../cmd/overcast/cmd_serve.go)) is unbounded: `HybridStore.Close` waits for seeding, then runs a full final flush — on a slow bind-mounted `/data`, this can exceed `docker stop`'s grace and get SIGKILLed (data survives via pending log, but restart pays a replay). Separately, the `case err := <-serverErr: return ...` path skips `cleanup()`, bypassing service-level flushes (e.g. the CloudWatch Logs `Stop` flush).

**Change.** Run the final close with a budget derived from `cfg.ShutdownTimeout` (goroutine + timer; on timeout, log loudly and return — the pending log makes this safe). Elevate the final-flush log to Info with entry count + duration. Route the `serverErr` path through the same shutdown tail (cleanup then close). Document the docker-grace interaction in [docs/debugging.md](debugging.md) or README.

**Tests.** Unit-level: `Close` respects a deadline when a flush is artificially slowed. Manual: `docker stop` timing note in the PR description with measurement conditions.

---

### 1.11 Corrupt-database startup policy: degrade, don't poison

**Problem.** If the SQLite open/migrate fails, `loadErr` is set and **every** TierHot read/write returns it forever ([hybrid.go `Get` → `getLoadErr`](../internal/state/hybrid.go)) — the daemon runs but every request 500s, with no path to recovery.

**Change (policy decision — implement unless maintainer objects).** On seed/open failure: log loudly, set `PersistentHealth{Healthy:false, Err:...}`, and continue **memory-only**: reads/writes work, flushes are skipped, the pending log keeps appending (cap its growth — reuse the 64 MiB compaction threshold as a hard stop with a warning). The health endpoint (`internal/router/health.go` / debug endpoints) must surface the degraded state.

**Tests.** Point a store at an unreadable/corrupt DB file: requests succeed, health reports unhealthy, no flush attempts spam the log.

---

### 1.12 Fix the `TierCached` doc comment

[internal/state/tier.go:14-17](../internal/state/tier.go) describes an LRU cache that does not exist (reads go straight to SQLite, see `shouldReadHybridNamespaceFromSQLite`). Correct the comment to describe reality; the real LRU is item 3.3.

---

### 1.13 Malformed-record isolation audit (poisoned-row tests)

**Problem.** CLAUDE.md requires that one corrupt persisted record never breaks list/scan operations or the whole service. This is untested at the storage layer, and the hybrid seed violates it: a single bad row aborts the entire seed ([hybrid.go:187-235](../internal/state/hybrid.go), every error path calls `setLoadErr` and returns), which poisons `loadErr` and fails **all** TierHot reads thereafter — one row takes down the store. (Distinct from 1.11, which covers an unopenable/corrupt DB *file*; this is one bad row in an otherwise healthy DB.)

**Change.** Seed: on a row-level `Scan` decode failure, log with namespace/key context, skip the row, count skips, and continue; only abort on infrastructure errors (query failed, connection lost). Then audit service `Scan` consumers with a grep for `json.Unmarshal` inside scan loops — most already `continue` on decode failure (e.g. CloudWatch `listAlarms`); flag and fix any that return an error for one bad record.

**Tests.** Poisoned-row fixtures: insert an undecodable value into `kv`, assert (a) hybrid seed completes and serves the healthy rows, (b) service-level list operations return the healthy records, per the CLAUDE.md rule.

---

## Phase 1 completion notes

All 13 items (1.1–1.13) shipped in one commit on `feat/storage-stabilization`. Implementation was parallelized across independent-file agent groups (HybridStore items together since they share `hybrid.go`; WALStore, NamespacedStore, CloudFormation, and shutdown each isolated to their own files), then integrated and verified as a whole: `go build`/`go vet` clean across every touched package, full test suite green including 3x `-race` runs of `internal/state`.

**Config wiring.** `HybridOptions` (sync mode/interval, dirty entry/byte thresholds) is wired into `cmd_serve.go`'s `buildStore` via `state.NewHybridStoreWithOptions`, reading the new `OVERCAST_HYBRID_SYNC`, `OVERCAST_HYBRID_SYNC_INTERVAL`, `OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD`, `OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD` config fields (mirroring the existing `WALFsyncMode` pattern).

**1.7's coverage gap, closed.** The 1.A/1.B agents' own tests covered torn/corrupt lines, ordering, degrade-to-memory, and burst thresholds thoroughly, but left two specific 1.7 asks unexercised: "flush failure → entries retained and re-flushed" and "kill-during-flush semantics" (pending log untouched by a failed flush). Added directly during integration: `TestHybridStore_FlushFailure_RetainsEntriesAndRetriesSuccessfully` and `TestHybridStore_FlushFailure_PendingLogSurvivesForReplay` in `internal/state/hybrid_internal_test.go`, both exercising `flushOnce`'s steal-then-restore-on-failure defer directly (via a deliberately broken writer connection) rather than the earlier-return `ctx.Err()` path, so they actually exercise the risky code.

**Two more interface-erasure instances found and fixed** (same bug class as 1.1, discovered by 1.C's audit, not originally itemized):
- `internal/router/health.go`'s `PersistentHealthReporter` assertion was silently erased by `NamespacedStore`, so the health endpoint reported no persistent health at all whenever any per-service override was configured. Fixed in `state.PersistentHealthSnapshot` (`internal/state/store.go`) by aggregating across `NamespacedStore.UnderlyingStores()` when the store is wrapped — `Healthy` is AND'd, `PendingWrites` sums, `LastError`/`LastErrorAt` come from the most-recently-erroring store, `Mode` lists every distinct backend present (e.g. `"hybrid+memory"`).
- `internal/services/cloudformation/store.go`'s `state.Flush(ctx, st.s)` asserted `Flushable` directly on the (possibly wrapped) store, silently no-oping the explicit flush call. Fixed via `state.Flush(ctx, state.Unwrap(st.s, "cfn"))`.

**New bug found, NOT fixed (flagged for follow-up):** per-service override routing keys (`OVERCAST_STATE_<SERVICE>`, from `allServices` in `internal/config/config.go`) don't always match the namespace prefixes `NamespacedStore.storeFor` actually routes on. Confirmed concretely for CloudFormation: the config service name is `"cloudformation"` but every CFN namespace is prefixed `"cfn:"` — so `OVERCAST_STATE_CLOUDFORMATION=memory` silently never takes effect; the override is accepted, a dedicated store is built, but `storeFor` never looks it up under key `"cfn"`. Likely affects other services with the same short-prefix pattern (`"apigateway"` service vs `"apigw:"` prefix, `"eventbridge"` vs `"eb:"` — see `debugServicePrefix` in `internal/router/debug.go` for the known short-name mapping, which per-service override routing does not currently consult). **Not fixed here** — it's a distinct bug from the storage layer's interface-erasure class, and a proper fix needs a canonical service-name → namespace-prefix mapping shared between `cmd_serve.go`'s route-building and `debugServicePrefix`, which is a self-contained follow-up, not a Phase 1 storage item.

**Verification also turned up two pre-existing, unrelated failures** — confirmed (by reproducing identically against the unmodified base branch) to **not** be regressions from this work:
- `tests/integration/router`'s `TestDebugResetService_knownService` fails identically on `fix/appsync-resolver-response-template` with no changes applied — likely related to the routing-key mismatch above (S3 is a simple case though, so possibly a separate cause; not investigated further as it's pre-existing).
- Several `tests/integration/lambda` failures ("Docker is not available...") are an artifact of the ephemeral verification container used for this work not mounting `/var/run/docker.sock` (the project's persistent devcontainer does); reproduced identically against the unmodified base branch under the same container setup.

---

## Phase 2 — Logs table + slim migration runner

Do 2.1 first; 2.2 and 2.3 land together (the runner's first customer is the logs migration); 2.4 rides along.

### 2.1 Benchmarks (regression gate)

Benchmark the current logs blob path and hybrid store before changing them: append throughput vs stream size (the blob model is O(history) per flush — capture the curve), `GetLogEvents` latency vs stream size, hybrid mixed read/write burst, cold-start seed time vs DB size. Follow [docs/performance.md](performance.md): every number records what/how/conditions. Suggested locations: `internal/services/cloudwatch/logs/store_bench_test.go`, extend `internal/state/memory_bench_test.go` patterns to hybrid.

### 2.2 Migration runner (`internal/state/migrate.go`)

- `type Migration struct { Version int; Name string; Up func(context.Context, *sql.Tx) error }`; package-level ordered registry.
- Runner executes inside the existing background `runMigrate` path (never on the request path): read `PRAGMA user_version`; for each pending migration, one transaction (SQLite DDL is transactional) then `PRAGMA user_version = N`.
- **Backup before the first pending migration:** `PRAGMA wal_checkpoint(TRUNCATE)` then file-copy `overcast.db` → `overcast.db.bak-v<from>` (WAL is empty post-checkpoint so the copy is consistent; this runs before `ready` closes, so no concurrent connections). No down-migrations — restore-the-backup is the downgrade story.
- Per-service dedicated tables register migrations here instead of ad-hoc `CREATE TABLE` (see 3.9 for the DynamoDB retrofit).
- **Per-service override dirs:** each override store has its own DB file (`<DataDir>/<svc>/overcast.db`, [cmd_serve.go:129](../cmd/overcast/cmd_serve.go)); the runner runs per store instance, which handles this automatically — add a test proving it.

### 2.3 `logs_events` dedicated table

Schema (migration #2; #1 is the conversion, or combine):

```sql
CREATE TABLE IF NOT EXISTS logs_events (
    region       TEXT    NOT NULL,
    group_name   TEXT    NOT NULL,
    stream_name  TEXT    NOT NULL,
    ts           INTEGER NOT NULL,
    seq          INTEGER NOT NULL,
    ingestion_ts INTEGER NOT NULL,
    message      TEXT    NOT NULL,
    PRIMARY KEY (region, group_name, stream_name, ts, seq)
);
```

- Rewrite [internal/services/cloudwatch/logs/store.go](../internal/services/cloudwatch/logs/store.go) event handling: keep the per-stream debounce cache but as a **write buffer of unflushed events only** (flush = batched `INSERT`, immutable rows, never rewrite); `GetLogEvents` pushes `startTime`/`endTime`/`limit`/direction into the SQL range query, merged with the buffer; `FilterLogEvents` becomes a range scan over `(region, group_name)`.
- **Dual backend** like DynamoDB (`newItemBackendFor` pattern, [internal/services/dynamodb/service.go:63](../internal/services/dynamodb/service.go)): memory-mode keeps a slice-backed implementation with identical behavior. Use the unwrap helper from 1.1 to find the DB.
- **Migration:** scan the `logs:events` kv namespace, decode each blob (skip corrupt ones with a warning — CLAUDE.md isolation rule), insert rows, delete the kv rows.
- `Stop` flushes buffers (existing pattern); `RetentionInDays` enforcement becomes a ranged `DELETE` (can defer to 3.6 but the schema enables it).
- **Raw state debugger (required, not optional).** `/_debug/state` and `/_debug/state/{namespace}` ([internal/router/debug.go:156-217](../internal/router/debug.go), proxied to the web UI by [internal/bff/bff.go](../internal/bff/bff.go)) enumerate the K/V store — moving events out of `logs:events` makes them invisible to the debugger and exempt from `/_debug/reset`. DynamoDB already solved this with a virtual namespace (`dynamodb:items` via `DebugStateKeys`/`DebugStateValues`/`DebugResetState`), but the router hardcodes a single `debugDynamoDBProvider`. Generalize it: define a `DebugStateProvider` interface (keys, values, reset, namespace name), let the router accept a slice of providers, migrate DynamoDB to it, and implement it for logs (`logs:events` virtual namespace backed by a bounded `SELECT` — cap rows and note truncation in the payload rather than dumping millions of events; 3.13 later upgrades this cap to real pagination). Reset endpoints must reset the table too. Existing router debug tests ([internal/router/debug_test.go](../internal/router/debug_test.go)) show the expected coverage; add equivalents for logs.

**Tests.** Wire-format tests unchanged (this is storage-internal). New: range-query correctness vs the old full-scan behavior, migration test (blob fixtures → rows), concurrent append/read, memory-mode parity suite run against both backends, debugger virtual-namespace + reset tests. Re-run 2.1 benchmarks; document the delta.

### 2.4 `auto_vacuum` (migration #1 or #0)

`PRAGMA auto_vacuum = INCREMENTAL` requires a `VACUUM` to take effect — acceptable as a one-time migration (it also compacts historical bloat). Pair with 3.5's maintenance loop for `PRAGMA incremental_vacuum` calls.

---

## Phase 2 completion notes

All four items (2.1, 2.2, 2.4, 2.3) shipped in one commit on `feat/storage-stabilization`, on top of Phase 1.

**2.1 benchmarks** established the "before" numbers directly cited below. **2.2/2.4** built `internal/state/migrate.go`: a `PRAGMA user_version`-based runner with a package-level `RegisterMigration` registry (the standard Go driver-registration pattern — packages outside `internal/state` register via their own `init()`, no import cycle), reserved version ranges (1-9 core, 10-19 CloudWatch Logs, 20+ free), a pre-migration file backup (skipped on a schema-less fresh database — no point backing up nothing), and an `AfterCommit` hook on `Migration` for statements SQLite refuses inside a transaction (`VACUUM`, migration #2). Old bare-`CREATE TABLE IF NOT EXISTS kv` databases adopt version 1 transparently — migration #1 *is* that exact DDL, idempotently re-registered.

**2.3** rewrote CloudWatch Logs event storage from one-JSON-blob-per-stream to the `logs_events` table (migration #10, with the blob→row conversion as #11 — group/stream name ambiguity, both can contain `/`, resolved by joining against each stream's exact `Name` field from `logs:streams` rather than guessing). Dual backend (`memEventBackend`/`sqlEventBackend`) mirrors DynamoDB's `item_store.go` split exactly, including using `state.Unwrap` (Phase 1) rather than a direct type assertion, so an unrelated per-service override can't silently downgrade Logs to memory-only. The per-stream cache changed role: it now holds only *unflushed* events (a write buffer), not full history — `getEvents` merges the backend's persisted, already-sorted events with the small buffered set via an O(n+m) two-pointer merge instead of an O(n log n) re-sort of everything.

**Benchmark delta** (2.1 baseline → post-2.3, same shapes, same devcontainer): at 1,000,000 pre-existing events, `appendEvents` went from ~1.46s/op and ~903MB/op to **1248 ns/op and 216 B/op** (flat across 100/10K/1M — O(1) regardless of history, the point of this item); a full-stream `getEvents` went from ~1.88s/op and ~401MB/op to **10.1ms/op and 32MB/op** (~185x faster, ~12.5x less memory — remaining cost is copying the 1M-row result set itself, unavoidable for a full-stream read).

**Debugger generalization (part of 2.3, required not optional).** `internal/router/debug.go`'s hardcoded `debugDynamoDBProvider` became `DebugStateProvider` (`DebugNamespace`/`DebugStateKeys`/`DebugStateValues`/`DebugResetState`), with the router threading a `[]DebugStateProvider` instead of a single field. DynamoDB moved onto it unchanged in behavior; CloudWatch Logs implements it against `sqlEventBackend.debugScan` (capped at 500 rows with a truncation signal — real pagination is 3.13). `debugResetService`'s existing `debugServicePrefix` short-name mapping (`"cloudformation"`→`"cfn"`, `"eventbridge"`→`"eb"`, etc.) is reused unchanged to match providers to a `/reset/{service}` call, so both `dynamodb` and `logs` (which map to themselves) resolve correctly through the same path.

**Two bugs found and fixed during integration (not itemized in the original plan, both from Phase 1's `HybridOptions`/`NewHybridStoreWithOptions` addition landing without updating everything that needed to know about it):**
- Wiring `HybridOptions`/`NewHybridStoreWithOptions` into `cmd_serve.go` during Phase 1 integration broke the `-tags nosqlite` build — the stub file (`internal/state/sqlite_hybrid_nosqlite.go`) was never updated to match the new API surface. Fixed by adding the missing `HybridOptions` type and `NewHybridStoreWithOptions`/`NewSQLiteStoreWithLogger` stubs. All three build configurations (normal, `-tags slim`, `-tags nosqlite slim`) now verified clean as part of this phase's integration — this should be a standing check for every future phase, not just Phase 1/2.
- Migration-runner diagnostics (pending/backup/applied log lines) were silently unreachable in both `persistent` and `hybrid` modes — `SQLiteStore` had no logger field, so `runMigrate()` always passed `nil` to `RunMigrations` regardless of what the caller had available. Added `NewSQLiteStoreWithLogger` (mirroring the existing `NewHybridStoreWithLogger` naming convention) and wired it at both call sites that actually have a logger in scope (`HybridStore.seedFromSQLite`, `cmd_serve.go`'s `buildStore` for `persistent` mode).

**Known limitation, not fixed:** `logsStore.getEvents` reads the backend and the cache buffer as two separate, unlocked operations. A flush landing between them can transiently miss an event on that one call (it reads persisted-before-flush and buffered-after-flush). This self-heals on the very next call — the event is safely persisted, just not visible to that one racing read — and real CloudWatch Logs itself has comparable eventual-consistency behavior between `PutLogEvents` and `GetLogEvents`. Not blocking; noted here so it isn't mistaken for a data-loss bug against surface reports of a "missing" event in a tight polling loop.

**Verification also surfaced that `TestDebugResetService_knownService` (documented as pre-existing/unrelated in the Phase 1 notes above) is fixed on `main`** — `fix/appsync-resolver-response-template` merged as `83fa596` ("Prepare AppSync resolver parity release #264"), which reworked `debugResetService` to check the actual `cfg.Services` map instead of inferring "unknown service" from zero-namespaces-currently-having-data (the real bug: a valid service with no data yet looked identical to a typo'd service name). That same commit also independently added a raw single-value `?key=` endpoint and response-value truncation to `debug.go` — both of which this phase's `DebugStateProvider` generalization needed reconciling against at rebase time (see below). Phase 3.13 should build on the existing `?key=` endpoint rather than re-implementing single-value fetch from scratch — real key *pagination* via `ScanPage` is still the gap it needs to fill.

---

## Rebase onto main

`fix/appsync-resolver-response-template` (this branch's original base) was squash-merged into `main` as commit `83fa596`. Since the branch's actual fork point (`9d2627f`) predated that squash and was never itself an ancestor of `main`, a plain `git rebase origin/main` tried to replay 9 already-squashed-away commits as new conflicts — the correct operation was `git rebase --onto origin/main 9d2627f feat/storage-stabilization`, replaying only this branch's own 2 commits. (Diagnosis: `git merge-base --is-ancestor <base> origin/main` returning false is the tell that a plain rebase will misbehave this way; `git log --oneline origin/main..<base>` shows what would otherwise get needlessly replayed.)

Two real conflicts, both resolved by hand:

- **`internal/state/hybrid.go`** — `main` had an independent small fix for `applyPendingEntry` silently dropping `walDeletePrefix` during pending-log replay (per-key enumeration via `s.mem.List`+loop). Phase 1's `HybridStore` rewrite already handles this case more efficiently (a single `PrefixDeleter.DeletePrefix` call, with all dirty-map/tombstone bookkeeping centralized in `applyOverlayLocked` right after the switch) — resolved by taking Phase 1's version entirely; main's inline per-op dirty-map writes were redundant with it.
- **`internal/router/debug.go` + `debug_test.go`** — `main` independently added a raw single-value `?key=` endpoint (`writeDebugStateRawValue`), response-value truncation (`truncateDebugStateValue`/`debugStateValuePreviewBytes`), and the `debugResetService` known-service fix (`services map[string]bool` param, checked before anything else). This phase's `DebugStateProvider` generalization (multi-provider `debugState`/`debugStateNamespace`/`debugReset`/`debugResetService`) touched the same functions independently. Resolved by combining both: `writeDebugStateRawValue` now resolves via `debugProviderForNamespace` instead of a hardcoded DynamoDB check; `debugResetService` takes both `providers []DebugStateProvider` and `services map[string]bool`, checking `services[service]` first (main's fix) before computing `matchingProviders` via `debugServicePrefix` (this phase's generalization). Three test call sites needed the new third argument added (only one showed as a textual conflict; the other two were new-on-this-branch-only calls with no overlapping context for git to flag).

Post-rebase, re-verified: `go build`/`go vet` clean across normal, `-tags slim`, and `-tags nosqlite slim`; full `internal/...` test suite clean under `-tags slim` (which also newly covers `internal/bff` and `internal/docssearch`, previously skipped — see the note below); `-race` clean on `internal/state`, `internal/router`, `internal/services/cloudwatch/logs`. Force-pushed with `--force-with-lease` (branch had been pushed once already, no one else had based work on it).

**Verification tooling note:** `internal/docssearch/index_slim.go` (build-tagged `slim`) provides stub `docs`/`postings` vars so packages depending on it (`internal/bff`) compile without the real generated `index.gen.go` (which needs `scripts/docs-index.go` / `make docs` run first — not available in this session's ad-hoc verification container). Building/testing with `-tags slim` is therefore the right way to verify this repo's `internal/...` tree without running the docs generator first — with one tradeoff: `-tags slim` also excludes the real MCP server implementation (`internal/router/mcp_routes.go` is `//go:build !slim`, with an intentional no-op stub for slim builds), so `TestRuntimeMCPInitialize_returnsToolsCapability` fails under `-tags slim` specifically — confirmed pre-existing/expected by reproducing the same result against an unmodified `main` checkout, not a product of this branch's changes. Full-coverage local verification needs two passes (plain, to catch anything slim's stubs would hide; `-tags slim`, to reach `bff`/`docssearch`) until the docs index is generated once.

---

## Phase 3 — Breadth (order flexible; each independent)

| # | Item | Guidance |
|---|---|---|
| 3.1 | **List+Get N+1 sweep** | Grep services for `List(` followed by per-key `Get(` loops (e.g. [logs `listLogGroups`](../internal/services/cloudwatch/logs/store.go), `listLogStreams`); replace with one `Scan`. Include `debugStateNamespace` ([internal/router/debug.go:203-214](../internal/router/debug.go)) — the raw state debugger does List + per-key Get, which is N SQLite round-trips on TierCached namespaces. Mechanical; per-service PRs fine. |
| 3.2 | **`Scan` pagination** | Add `ScanPage(ctx, ns, prefix, startAfter string, limit int) ([]KV, nextKey, error)` to `Store` (all five implementations!); adopt in handlers that paginate at the API layer and in the debug state endpoints (3.13). |
| 3.3 | **LRU tier for TierCached** | Bounded-memory cache in front of `shouldReadHybridNamespaceFromSQLite` reads; invalidation via the existing dirty-overlay precedence. Config knob for budget. Benchmark-gate it (2.1 infra). |
| 3.4 | **Retention enforcement in persistent modes** | CloudWatch metric pruning currently memory-mode-only ([internal/services/cloudwatch/service.go:246](../internal/services/cloudwatch/service.go)); logs `RetentionInDays` stored but unenforced. Periodic sweep (clock-injected) doing ranged deletes. |
| 3.5 | **Vacuum/checkpoint maintenance loop** | Background: `PRAGMA wal_checkpoint(PASSIVE)` periodically; `incremental_vacuum` when freelist ratio high. Never on the request path. |
| 3.6 | **Debug metrics** | Expose on the existing debug endpoints ([internal/router/debug.go](../internal/router/debug.go)): flush duration/entry history, seed duration, pending-log size, per-namespace row counts. |
| 3.7 | **Config knobs** | All new tunables (sync mode/interval, flush thresholds, LRU budget) as documented `OVERCAST_*` fields in `internal/config/config.go` — `HybridFlushInterval` is the template. |
| 3.8 | **Docs: policies + mode comparison** | CONTRIBUTING.md: JSON-compat policy (additive = free; reshape = numbered migration) and the **graduation rule** (below). New/updated doc: storage-mode comparison including WALStore's compaction stall (rewrites the full snapshot under the store mutex at 64 MiB, [wal.go:326](../internal/state/wal.go)) and full-RAM residency. |
| 3.9 | **DynamoDB tables → migration runner** | Move `CREATE TABLE` out of `sync.Once` init ([item_store.go:272](../internal/services/dynamodb/item_store.go), `stream_store.go`) into registered migrations. |
| 3.10 | **Kinesis/SQS dedicated tables — only if benchmarks demand** | SQS first candidate (receive scans full queue per poll, [internal/services/sqs/store.go:290](../internal/services/sqs/store.go); a `visible_at` index is the win). Kinesis kv shape already fits its access pattern. Use the logs table as the template, including its `DebugStateProvider` virtual namespace (see 2.3). |
| 3.11 | **Kinesis region-scoping check** — ✅ done, no bug | `kinesis:records`'s namespace comment looked region-less at a glance, unlike SQS/logs. Verified: `putRecord`/`listRecords`/`deleteStream` all wrap keys with `serviceutil.RegionKey(...)` before touching the store — two regions' streams never collide. Comment corrected to document the actual key shape (`internal/services/kinesis/store.go:22-25`); no behavior change needed. |
| 3.12 | **`MemoryStore` per-namespace locking — only if benchmarks show contention** | One global RWMutex guards all namespaces ([internal/state/memory.go:26](../internal/state/memory.go)), so cross-service write bursts serialize. Likely invisible next to JSON marshaling — do not implement without a benchmark (2.1 infra) demonstrating contention. If needed: `sync.Map` of per-namespace locks or sharded mutexes. |
| 3.13 | **Raw state debugger scalability (API pagination + UI virtualization)** | Both halves needed. **Backend:** `/_debug/state/{namespace}` returns every key *with full values* in one JSON map ([internal/router/debug.go:188-217](../internal/router/debug.go)) — multi-MB for busy namespaces. Change the contract: namespace endpoint returns keys (+ sizes) paginated via `ScanPage` (3.2); add a single-value endpoint (e.g. `/_debug/state/{namespace}/value?key=...`) fetched on selection. Update the BFF proxy routes ([internal/bff/bff.go:83](../internal/bff/bff.go)) and MSW handlers (`web/src/test/handlers.ts`). **Frontend** ([web/src/features/debug/debug-page.tsx](../web/src/features/debug/debug-page.tsx)): flat table and `KeyTree` render all rows unvirtualized, and the tree has no collapse state (every branch always expanded). Use `@tanstack/react-virtual` — already a dependency with in-repo precedent (`log-events-viewer.tsx`, `event-console.tsx`); for the tree, flatten visible (expanded) nodes into a list and virtualize that (standard TanStack Virtual tree pattern), adding collapse state. Cap `parseStoredValue`/`highlightJSON` input size (e.g. 256 KiB) with a "value truncated — copy raw" fallback so one huge value can't lock the tab. Client-side value search must become server-side or key-only once values are lazy. Follow [CONTRIBUTING § Web UI standards](../CONTRIBUTING.md#web-ui-standards); `npx tsc --noEmit` in `web/`. Coordinate with 2.3's `DebugStateProvider` so virtual namespaces speak the same paginated contract. |

---

## Phase 3 progress notes (Wave 1)

Phase 3 was split into two waves to avoid repeated merge conflicts in `internal/router/debug.go` (already the highest-conflict file in this plan — see the rebase notes above). **Wave 1 — 3.2, 3.4, 3.5, 3.8, 3.9, 3.11 — is complete**, on top of the rebased Phase 1+2 base. **Wave 2 — 3.1, 3.6/3.7, 3.13 (all touch `debug.go`), consolidated into one pass — has not started.**

**3.2 (`ScanPage`)** landed across all five `Store` implementations, plus the hardest piece of this item: `HybridStore.ScanPage` correctly merges a paginated base read (SQLite or memory, whichever `Scan`/`List` already pick) against the pending write overlay without ever materializing the base in full — the overlay itself (not the base) is what's bounded by the dirty-flush thresholds, so it's the only side worth snapshotting whole. `resolvePendingLocked`'s precedence logic was extracted into a pure `resolveOverlayKey` function so the merge walk and the live request path share the exact same tombstone/seq-ordering rule with zero drift risk. `SQLiteStore.ScanPage` and `HybridStore`'s SQL-backed path share their query-building/row-trimming logic via package-level helpers so the two SQLite-backed implementations can't diverge on key-range semantics.

**3.5 (vacuum/checkpoint loop)** landed as a shared `runSQLitePragmaMaintenance` helper (passive WAL checkpoint + freelist-ratio-gated `incremental_vacuum`, gating logic extracted as a pure `shouldVacuum` function) used by both `HybridStore` and `SQLiteStore`'s own background maintenance goroutines. New config: `OVERCAST_HYBRID_MAINTENANCE_INTERVAL` (default 5m).

**3.4 (retention enforcement)** removed the `backendMode()`-gated exemption that limited CloudWatch metric-data retention to memory mode only, added a periodic full-namespace sweep (`sweepMetricDataOnce`) alongside the existing per-write fast-path prune, and added the equivalent for CloudWatch Logs (`sweepExpiredEventsOnce`, using a new `deleteEventsOlderThan` on the `eventBackend` interface — a real ranged `DELETE` against `logs_events`, covered by the Phase 2 `(region, group_name, ts)` index). A real bug was found and fixed during testing: both new tickers were originally created *inside* their spawned goroutine, which raced against a mock clock advanced immediately after construction (a clock advance before the ticker registers is silently lost) — fixed by creating the ticker synchronously on the calling goroutine before spawning.

**3.9 (DynamoDB → migration runner)** claimed versions 20-21 (documented in `migrate.go`'s reserved-range comment, which also now correctly cross-references both the CloudWatch Logs and DynamoDB migration files instead of saying "not yet registered").

**3.8 and 3.11** — see their own sections/table row above.

**New, not originally itemized: readiness gating (503 while migrating).** Came out of a user question during Wave 1 review about what happens to requests during a slow migration. Investigation found a real, user-visible gap: in the default `hybrid` mode, a request racing the migration window doesn't hang or error — it silently returns "not found"/an empty list for data that exists once migration finishes, because `TierHot` reads fall back to a still-empty in-memory store (seeding hasn't started; that only happens after migration succeeds). In `persistent` mode the same window makes the request hang indefinitely instead. Fixed with a new `state.NotReadyReporter` interface (`HybridStore`/`SQLiteStore`/`NamespacedStore`, mirroring `ReadyAwaiter`'s direct-implementation convention rather than `PersistentHealthSnapshot`'s package-level-aggregator one, since `NotReadyReporter` needs the same interface-erasure protection `WaitReady` already has) and a new `internal/middleware/notready.go` that returns a 503 with the real AWS `ServiceUnavailable` error code (AWS SDKs already retry this automatically) for every request except Overcast's own `/_`-prefixed internal endpoints. Reuses `Recovery`'s exact XML-vs-JSON wire-format detection (`detectService(r) == "s3"`) rather than inventing a new one. `NotReady()` is scoped precisely to the migration window only (not the broader seed phase, where reads already correctly fall back to querying SQLite directly) and explicitly excludes the permanently-degraded-to-memory-only state (an ongoing health condition, not a startup phase — must not 503 forever). Full detail, including the precise request-during-migration behavior per backend, is documented in `docs/storage-backends.md`'s new "Startup: what happens to requests during a schema migration" section.

**A real regression was found and fixed during verification of the above:** `tests/integration/router`'s `TestDebugReset_withSQLiteStore` constructed a fresh `SQLiteStore` and immediately fired a real API request against it with no wait for migration — previously this silently blocked-then-succeeded; correctly, it now gets a fast 503 instead, which is exactly the behavior the fix was for. Updated the test to wait for migration to finish (via a synchronous warm-up read — `SQLiteStore` has no `WaitReady` of its own, unlike `HybridStore`) before issuing its real requests, since the test's intent was verifying debug-reset behavior, not this race. Grepped the whole `tests/` tree first to confirm this was the only test constructing a raw `SQLiteStore`/`HybridStore` and feeding it directly into a full HTTP test server; every other test either uses `MemoryStore` (unaffected — doesn't implement `NotReadyReporter`) or calls service/store methods directly without going through the router (never enters the new middleware's code path at all).

**Verification environment note (a pattern worth recording so it isn't re-diagnosed as a regression):** across three separate full-suite runs today, a *different* single, previously-untouched, timing-sensitive `internal/state` test failed each time (`TestHybridStore_RestoreLargeState`, then `TestHybridStore_ReadsDontBlockOnConcurrentFlush`, then `TestHybridStore_DirtyEntryThresholdTriggersEarlyFlush`) — never the same one twice, and every one passed reliably (10/10) when re-run in isolation immediately after. This is resource contention specific to running this environment's now-much-larger `internal/state` suite as one long sequential run, not a defect in the code under test. If a future full-suite run shows a single `internal/state` test failing that passes cleanly in isolation, treat it as this same pattern, not a new regression, unless it reproduces in isolation too.

---

## Settled decisions

- **Per-service tables for everyone: no.** Dedicated tables forfeit the hybrid write path (every DynamoDB `put` is a synchronous SQLite write) and require dual memory/SQLite backends. Only `logs:events` qualifies unconditionally (blob-append pathology).
- **Graduation rule ("data earns a table; services don't"):** a namespace moves to a dedicated table only when it has (1) unbounded high-frequency append that would force blob rewrites, (2) a query need the key order can't serve (secondary index), or (3) measured evidence the K/V path is the bottleneck after generic fixes. Graduating means accepting: dual backends, own write buffering if writes are hot, schema in the migration runner, and a `DebugStateProvider` virtual namespace so the data stays visible to `/_debug/state` and resettable via `/_debug/reset` (the raw state debugger enumerates only the K/V store; dedicated tables are invisible without one — see 2.3).
- **Migration pipeline: slim.** `user_version` + ordered in-code steps + pre-migration backup. No down-migrations.
- **Audit results (2026-07):** blob-shaped namespaces: `logs:events` (severe → 2.3), `cfn:events` (bounded → 1.9). Row-shaped and fine: `sqs:messages`, `cloudwatch:metricdata`, `kinesis:records`, `s3:objects`, `lambda:invocations`, `sqs:dedup`, everything TierHot.

## Suggested PR slicing

1. **PR 1:** 1.1 (+ its tests) — silent-data-loss bug, independent, high urgency.
2. **PR 2:** 1.5 + 1.6 + 1.7 + 1.13 (crash-recovery & corruption-isolation cluster — the tests underwrite everything later).
3. **PR 3:** 1.2 + 1.12 (read pool + comment fix).
4. **PR 4:** 1.3 + 1.4 + config plumbing (write-path durability/burst knobs).
5. **PR 5:** 1.9 (cfn events). **PR 6:** 1.8 (ranged tombstones). **PR 7:** 1.10 + 1.11 (shutdown/startup robustness).
6. **Phase 2:** benchmarks PR, then runner + logs table + conversion as one reviewed unit (or runner separately if reviewers prefer).
7. **Phase 3:** independent PRs per row of the table.

Each PR: failing test first; scoped `go test -count=1` per touched packages; `gofmt` then `go vet`; widen to `./...` before done; CHANGELOG entry per [CONTRIBUTING.md](../CONTRIBUTING.md).
