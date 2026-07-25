# Storage backend internals

This is a contributor-depth comparison of the four `state.Store` implementations
(`internal/state/memory.go`, `wal.go`, `sqlite.go`, `hybrid.go`). For the **mode/durability
comparison table, what survives a restart, and how to choose a backend**, see
[docs/storage.md](../storage.md) — this page does not repeat that material. For **how to
configure** a backend (`OVERCAST_STATE`, per-service overrides, Docker examples), see
[docs/README.md § Persistence](../README.md#persistence). Use this page when you need to
reason about *why* a backend behaves the way it does at the implementation level: durability
mechanics, what stays resident in memory, read/write performance shape, and known limitations
worth knowing before you touch this code or pick a backend for a workload you're benchmarking.

See also [CONTRIBUTING.md § Persisted state](../../CONTRIBUTING.md#persisted-state-json-compatibility-table-graduation-and-migrations)
for the policy on evolving persisted JSON structs and when a namespace earns its own table.

---

## Startup: what happens to requests during a schema migration

Both SQLite-backed modes (`persistent`, `hybrid`) run schema migrations
([internal/state/migrate.go](../../internal/state/migrate.go)) in a background goroutine before the
store is usable — this is normally instant (a no-op check of `PRAGMA user_version` against an
already-up-to-date database), but is not always instant, and the two failure-adjacent windows
below are real, not test artifacts.

**The one-time cost is real and scales with database size.** The `auto_vacuum` migration's
`AfterCommit` runs a full `VACUUM` the first time an existing database is opened under the
migration runner — this rewrites the entire file, so its cost is proportional to database size
and disk speed. Anyone upgrading Overcast with a large pre-existing `overcast.db` pays this real,
one-time cost on their next startup; on a slow disk this can take tens of seconds or more. This
isn't a bug — it's an unavoidable characteristic of `VACUUM` — but it's worth knowing about
before assuming a slow first-startup-after-upgrade means something is broken.

**The server looks ready before migrations finish, and the logs go quiet while a migration is
still running.** `overcast listening` logs as soon as the HTTP listener binds — store
construction is non-blocking by design (see [CONTRIBUTING § Startup budget](../../CONTRIBUTING.md)),
so this happens before the background migration goroutine has necessarily finished, or even
started. `RunMigrations` logs `sqlite migrations pending` (with the full list of what's about to
run) before anything executes, and `sqlite migration applied` per migration — but only *after*
each one completes, with an `elapsed` field showing how long it took *retroactively*. There is no
progress log *during* a long-running migration (e.g. the `VACUUM` above) — the logs go silent for
its entire duration, which is indistinguishable from a hang if you're watching them without
knowing this.

**What a request sees during this window: a fast 503, by design.** Requests that arrive while a
migration is still in flight are rejected by the `NotReady` middleware
([internal/middleware/notready.go](../../internal/middleware/notready.go)) with a 503
`ServiceUnavailable` response (the real AWS error code, which AWS SDKs already retry
automatically) plus a `Retry-After: 2` header — in the service's own wire format (XML for S3,
JSON elsewhere). Overcast's own `/_`-prefixed endpoints (`/_health`, `/_debug/*`, …) are exempt,
so operators can still check status while a long migration (the `VACUUM` above) runs.

The middleware exists because of what each backend would otherwise do in this window — worth
knowing when reasoning about the store internals, since the underlying behavior is still there
beneath the gate:

- **`persistent` mode**: every store method calls `ensureReady(ctx)` first, which blocks until
  migration finishes — an ungated request would simply hang until its own client timeout.
- **`hybrid` mode (default)**: `TierHot` reads fall back to the in-memory store when SQLite
  isn't ready, and that memory store is still empty during migration (seeding only begins after
  migration succeeds) — an ungated request would get **"not found" / an empty list** for data
  that genuinely exists once migration finishes, which is worse than an error because nothing
  looks wrong. This gap is scoped to the migration window only: once migration finishes and
  seeding begins, reads fall back to querying SQLite directly and are accurate (just slower than
  the warmed cache), which is why `NotReady()` reports false from that point on.

**Writes are never at risk in either mode** — in hybrid they land in the in-memory overlay and
pending log immediately regardless of migration/seed state; in persistent they block on
`ensureReady` like reads. And a store that *permanently* degrades to memory-only (unopenable/
corrupt database — see the `hybrid` section below) is deliberately **not** gated: that's an
ongoing health condition surfaced via `PersistentHealth`, not a startup phase, so it must not
503 forever.

---

## `memory`

Everything lives in a `sync.RWMutex`-guarded B-tree per namespace
([internal/state/memory.go](../../internal/state/memory.go)). No disk I/O of any kind. Fastest
backend, zero durability. This is the right default for tests and CI — see
[docs/dev/development-setup.md](./development-setup.md), which already recommends
`OVERCAST_STATE=memory` for worktrees and fast local iteration.

---

## `wal`

`WALStore` ([internal/state/wal.go](../../internal/state/wal.go)) is a `MemoryStore` with an
append-only log bolted on for durability. Every `Get`/`List`/`Scan` goes straight to the
embedded `MemoryStore` — there is no disk read on the hot path, and no separate on-disk
representation of the data other than the append log itself. This means:

- **The entire dataset must fit comfortably in memory, always** — same as `memory` mode, plus
  the log file on disk. `wal` is not a disk-backed store in the sense `persistent` is; it's a
  memory store with a durability mechanism attached, and its memory profile scales with your
  data exactly the way `memory` mode's does.
- Every `Set`/`Delete`/`DeletePrefix` writes to the in-memory tree, appends a JSON line to the
  log, and (depending on `WALSyncMode`) may fsync inline.

### Known limitation: compaction stalls writes for the full rewrite

When the append log crosses `MaxLogBytes` (default 64 MiB), the next mutation triggers
`compactLocked` ([wal.go:361](../../internal/state/wal.go)), which rewrites the *entire current
dataset* to a fresh snapshot file, synchronously: open a temp file, encode every key in every
namespace to it, `fsync`, close, close the active log file, rename, reopen. This all happens
while holding `WALStore.mu` — which `maybeCompact` acquires from inside `Set`/`Delete`/
`DeletePrefix` and holds for the entire compaction ([wal.go:349](../../internal/state/wal.go)).

The practical effect:

- **Writes stall for the whole compaction.** Any concurrent `Set`/`Delete`/`DeletePrefix`
  blocks on `WALStore.mu` until compaction finishes — bounded by disk write speed for the
  full dataset, not just the recent log tail.
- **Reads are largely unaffected.** `Get`/`List`/`Scan` call the embedded `MemoryStore`
  directly and never acquire `WALStore.mu` at all. The one brief exception is
  `writeSnapshot`'s scan of the data
  ([wal.go:402](../../internal/state/wal.go)), which holds `MemoryStore`'s own `RLock` — but
  `RLock` is shared, so concurrent reads proceed normally; only a concurrent *write* (which
  needs `MemoryStore`'s exclusive `Lock`) queues behind it, and then queues again behind
  `WALStore.mu` for the remainder of the compaction.

For a namespace with sustained high write volume and a large total dataset, this means
periodic multi-second (or longer, depending on disk speed) write stalls once the log crosses
the threshold. `wal` is a good fit for small-to-medium state that needs simple crash
durability without a SQLite dependency; it is the wrong choice for a workload with both a
large dataset and steady write pressure — `hybrid` or `persistent` degrade more gracefully
there.

---

## `persistent`

`SQLiteStore` ([internal/state/sqlite.go](../../internal/state/sqlite.go)) has no memory layer
at all — `Get`/`Set`/`Delete`/`List`/`Scan` each issue one SQLite statement directly
([sqlite.go:151-227](../../internal/state/sqlite.go), `Get` through `DeletePrefix`) against a
single writer connection
(`SetMaxOpenConns(1)`). Every mutation is durable the instant the call returns (subject to
`PRAGMA synchronous`), and there is no separate read pool — reads and writes share the one
connection, so a slow write can make a concurrent read wait. This is the most durable and the
most predictable backend, at the cost of paying a real disk round trip (or at minimum a
kernel/page-cache round trip) on every single operation. Use it when you need every write
durable synchronously — for example, reproducing a crash-recovery scenario, or verifying
behavior that must not depend on `hybrid`'s async flush timing — and can accept the latency.

---

## `hybrid` (default)

`HybridStore` ([internal/state/hybrid.go](../../internal/state/hybrid.go)) splits namespaces
into two tiers ([internal/state/tier.go](../../internal/state/tier.go)):

- **`TierHot`** — resource definitions (queues, tables, functions, stacks, …). Seeded into
  memory in a background goroutine at startup and always read from memory afterward. Small,
  finite, and needed for instant dashboard/topology renders.
- **`TierCached`** — high-volume data-plane namespaces (`sqs:messages`, `logs:events`,
  `cloudwatch:metricdata`, `kinesis:records`, …). These are **read straight from SQLite on
  every access** via `shouldReadHybridNamespaceFromSQLite`
  ([hybrid.go:1752](../../internal/state/hybrid.go)), overlaid with a small pending-write cache
  for changes not yet flushed. `tier.go`'s doc comment is explicit about this: *"There is
  currently no in-memory LRU cache in front of SQLite for these namespaces — every read not
  covered by the pending overlay is a SQLite round trip."* An LRU-bounded cache tier is a
  possible future enhancement (`docs/plans/storage-plan.md` item 3.3 in the repository), not implemented today — the comment
  used to claim an LRU existed; that was corrected to describe reality.

TierCached reads don't queue behind an in-flight flush transaction — they go through a
dedicated read-only connection pool opened alongside the writer connection
(`openReadPool`/`readDB`, [hybrid.go:459](../../internal/state/hybrid.go), `SetMaxOpenConns(4)`),
which WAL mode allows to proceed concurrently with the single writer. That solves the
*blocking* problem; it does not add caching. Under sustained load, a `TierCached` namespace
still pays a real SQLite query per operation — just one that doesn't wait on a writer lock.

Writes are cheap and async: `Set`/`Delete`/`DeletePrefix` update the in-memory overlay and
append to a pending log, then return — a background loop batches the dirty set to SQLite on a
timer (`OVERCAST_HYBRID_FLUSH_INTERVAL`, default 5s) or when a dirty-entry/byte threshold is
crossed (`OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD` / `_DIRTY_BYTE_THRESHOLD`, defaults 10 000
entries / 8 MiB), whichever comes first. The pending log's fsync policy is configurable
(`OVERCAST_HYBRID_SYNC`: `always` | `interval` | `never`, default `interval` at
`OVERCAST_HYBRID_SYNC_INTERVAL` = 100 ms — the same mechanism as `wal` mode's
`OVERCAST_WAL_FSYNC`). This is why `hybrid` is fast *and* durable enough for local dev: a
process kill loses nothing that reached the pending log; an OS crash/power loss loses at most
one sync interval (100 ms by default), recovered by pending-log replay — not the whole dataset.
See [docs/performance.md](../performance.md) for guidance on when (and when not) to touch these
knobs from a tuning perspective.

Replay tolerates real-world crash damage: a torn final line (the signature of a kill
mid-append) is logged and ignored, a corrupt line anywhere else is logged and skipped, and the
file is streamed rather than loaded whole, so replay memory is bounded by the largest single
entry. The same tolerance applies to `wal` mode's log replay.

**Degraded mode (unopenable/corrupt database): degrade, don't poison.** If the SQLite file
can't be opened, migrated, or seeded, the store logs loudly once and continues **memory-only**
for the rest of the process lifetime: reads and writes keep working, flushes are skipped, and
`PersistentHealth` reports unhealthy (surfaced via `/_health`). The pending log keeps
appending so a restart can replay — but with flushes gone, nothing ever compacts it, so its
growth is hard-capped at 64 MiB; past the cap, writes are memory-only for the rest of the run
and a one-time warning is logged. One corrupt *row* (as opposed to a corrupt file) never
triggers this: the seed skips undecodable rows individually and keeps loading the rest.

### Known limitation: `TierCached` has no caching layer

Worth restating plainly since it's easy to assume otherwise from the name "hybrid": only
`TierHot` gets memory-speed reads. A `TierCached` namespace under heavy read load (e.g.
polling `sqs:messages` or `logs:events` from many goroutines) generates a real SQLite query
per read, every time, forever — bounded only by the read pool's four connections and SQLite's
own performance. This is fine for the emulator's typical local-dev/CI read volumes; it is the
first place to look if a `TierCached` namespace shows up as a bottleneck in a benchmark (see
[CONTRIBUTING.md § Data earns a table](../../CONTRIBUTING.md#data-earns-a-table) for the
graduation criteria before reaching for a dedicated table instead).

### Known limitation: reads fail fast, not slow, under severe disk contention

`TierCached` reads that hit a transient SQLite error (`SQLITE_BUSY`/`SQLITE_LOCKED`) retry for up
to `hybridSQLiteReadRetryTimeout` (2 seconds) before giving up and returning an error rather than
blocking indefinitely — a deliberate fail-fast choice, not a bug. Under the read/write isolation
this backend is built around (a dedicated read connection pool, separate from the single writer —
see above), this ceiling is not normally reachable by Overcast's own write load: a stress test of
8 concurrent writers against 8 concurrent readers on the same `TierCached` namespace passes
reliably with normal latency in isolation. It *can* be reached if the underlying disk is severely
contended from something external to Overcast's own writes — a resource-starved host, a busy CI
runner sharing the same disk — in which case a read genuinely can exhaust the 2-second budget and
surface an error to the caller. That's expected, bounded degradation (better than hanging forever)
given this project's local-dev/CI scope, not a design flaw in the read/write split — but worth
knowing if you see an occasional read error under heavy host-level I/O pressure that isn't
reproducible when Overcast is the only thing touching the disk.

---

## Shutdown: the flush budget and `docker stop`

Graceful shutdown ends with a final synchronous flush of everything the hybrid store hasn't
written to SQLite yet, then the database close. That close is **time-budgeted by
`OVERCAST_SHUTDOWN_TIMEOUT`** (default 5s, [cmd_serve.go `closeStoreBounded`](../../cmd/overcast/cmd_serve.go)):
if the flush can't finish inside the budget — realistic on a slow bind-mounted `/data` volume
under Docker Desktop — Overcast logs `store close exceeded shutdown timeout`, exits anyway, and
the unflushed writes replay from the pending log on the next start. Nothing is lost either way;
the budget exists so a slow disk can't push the process past `docker stop`'s ~10s grace period
and get it SIGKILLed with no record of why. If you routinely see the timeout warning, either
raise `OVERCAST_SHUTDOWN_TIMEOUT` (and `docker stop -t` to match) or move `/data` off the
bind mount. Error-path shutdowns (listener failure) run the same cleanup tail as
signal-triggered ones, including service-level buffered flushes like CloudWatch Logs' write
cache.
