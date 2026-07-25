---
title: "Storage backends"
description: "Compares Overcast's four storage backends — memory, wal, persistent, hybrid — by durability and what survives a crash or restart, and how to choose one."
section: "Getting Started"
tags:
  - docs
  - storage
  - state
  - durability
---

# Storage backends

Overcast supports four storage backends, selected with `OVERCAST_STATE` (globally) or
`OVERCAST_STATE_<SERVICE>` (per service). This page compares them by durability and
what happens to your data across a restart or crash. For the full list of environment
variables, Docker examples, and per-service override syntax, see
[docs/README.md § Persistence](./README.md#persistence). For tuning guidance (bind
mounts, the `hybrid` flush knobs, storage-related startup warnings), see
[docs/performance.md](./performance.md).

---

## At a glance

| Backend      | Durability                                                                                      | Memory residency                                                       | Speed                                                                    |
| ------------ | ------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
| `memory`     | None — lost on process exit.                                                                      | Full dataset, always.                                                     | Fastest — no disk I/O at all.                                              |
| `hybrid`     | Async — writes are journaled immediately, batch-flushed to SQLite in the background.               | Partial — resource metadata is always in memory; bulk data reads from SQLite. | Fast for typical use — memory-speed for most reads, cheap async writes.   |
| `persistent` | Every mutation committed to SQLite before the call returns.                                        | None — every operation is a live SQLite query.                            | Slowest — a real disk (or page-cache) round trip per operation.           |
| `wal`        | Append-only log, replayed on restart; fsync policy configurable.                                   | Full dataset, always (in-memory store with a durability log attached).    | Reads are memory-speed; writes pay a log append and periodic compaction. |

`hybrid` is the default and the right choice for most local development — it's both fast
and durable across restarts without paying a synchronous disk round trip on every operation.

---

## What survives a restart or crash, per backend

- **`memory`** — nothing. All state is lost on process exit, whether graceful or a crash.
  This is the right choice for tests, CI, and any workflow that doesn't need state to
  outlive the process.
- **`wal`** — everything, replayed from the append-only log on the next start. If the
  process was killed mid-write, only the torn final log entry is dropped; everything
  before it survives. The whole dataset must fit comfortably in memory in this mode.
- **`persistent`** — everything. Every mutation is committed to SQLite before the call
  that made it returns, so a crash at any point loses nothing that was ever acknowledged
  to a client.
- **`hybrid`** (default) — nearly everything. Writes land in a durable pending log
  immediately and are flushed to SQLite in the background. A process kill loses nothing
  that reached the pending log; an OS crash or power loss loses at most one flush
  interval's worth of writes (a fraction of a second by default), recovered by replaying
  the pending log on the next start. On a graceful stop (e.g. `docker stop`), Overcast
  tries to flush everything within a shutdown budget; even if that budget runs out,
  nothing is lost — the remaining writes replay from the pending log on the next start.

**If the underlying SQLite file becomes unreadable or corrupt**, `persistent` and `hybrid`
log a warning and — for `hybrid` only — keep serving reads and writes in a degraded,
memory-only mode for the rest of that run rather than crashing; `/_health` reports this
condition. This is a rare, unusual-environment case, not something to expect in normal use.

---

## Choosing a backend

- **Default to `hybrid`** unless you have a specific reason not to.
- **Reach for `persistent`** when correctness depends on every write being durable
  *before the call returns* — for example, reproducing a crash-recovery scenario, or
  verifying behavior that must not depend on `hybrid`'s async flush timing.
- **Reach for `wal`** when you want durability with a simpler on-disk format than SQLite
  and your dataset is small enough to live entirely in memory comfortably.
- **Use `memory`** for tests, CI, and any workflow that doesn't need state to survive a
  restart — it's the fastest backend by a wide margin.
- **Mix backends per service** with `OVERCAST_STATE_<SERVICE>` — e.g. `hybrid` globally
  with `persistent` only for the one service under test. See
  [docs/README.md § Per-service storage overrides](./README.md#per-service-storage-overrides).

Contributing to Overcast and need the implementation-level detail (WAL/overlay
internals, flush/compaction mechanics, migration behavior)? See
[docs/dev/storage-backends.md](./dev/storage-backends.md).
