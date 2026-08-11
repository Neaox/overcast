---
title: "Storage backends"
description: "Compares Overcast's four storage backends — memory, wal, persistent, hybrid — by durability and what survives a crash or restart, how to choose one, and which backends each published artifact actually ships with."
section: "Getting Started"
tags:
  - docs
  - storage
  - state
  - durability
  - slim
---

# Storage backends

Overcast supports four concrete storage backends, selected with `OVERCAST_STATE`
(globally) or `OVERCAST_STATE_<SERVICE>` (per service). This page compares them by
durability and what happens to your data across a restart or crash. For the full list of
environment variables, Docker examples, and per-service override syntax, see
[docs/README.md § Persistence](./README.md#persistence). For tuning guidance (bind
mounts, the `hybrid` flush knobs, storage-related startup warnings), see
[docs/performance.md](./performance.md).

---

## At a glance

| Backend                 | Durability                                                                            | Memory residency                                                              | Speed                                                                    |
| ------------------------ | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
| `auto` (unset / default) | Resolved at startup to one of the rows below — see "The `auto` default" below.         | Depends on what it resolves to.                                                | Depends on what it resolves to.                                          |
| `memory`                  | None — lost on process exit.                                                            | Full dataset, always.                                                           | Fastest — no disk I/O at all.                                              |
| `hybrid`                  | Async — writes are journaled immediately, batch-flushed to SQLite in the background.    | Partial — resource metadata is always in memory; bulk data reads from SQLite. | Fast for typical use — memory-speed for most reads, cheap async writes.   |
| `persistent`              | Every mutation committed to SQLite before the call returns.                             | None — every operation is a live SQLite query.                                | Slowest — a real disk (or page-cache) round trip per operation.           |
| `wal`                     | Append-only log, replayed on restart; fsync policy configurable.                        | Full dataset, always (in-memory store with a durability log attached).        | Reads are memory-speed; writes pay a log append and periodic compaction. |

`auto` is the default when `OVERCAST_STATE` is unset (or explicitly set to `auto`) — see
below for exactly how it resolves. Among the four concrete backends, `hybrid` is the
right choice for most local development that needs to survive a restart — it's both
fast and durable without paying a synchronous disk round trip on every operation.

> [!IMPORTANT]
> `hybrid` and `persistent` need SQLite, and two of the published artifacts — the
> **`overcast-slim` image** and the **`overcastd` binaries** — are built without it. In
> those, `auto` can only ever resolve to `memory`, mounting a volume changes nothing, and
> `wal` is the only durable backend available. See
> [Builds without SQLite](#builds-without-sqlite).

---

## The `auto` default

When `OVERCAST_STATE` is unset, Overcast picks a mode based on whether there's evidence
you want persistence, checked in this order:

1. **The data directory is a mounted volume or bind mount.** A Docker named volume or
   bind mount at `OVERCAST_DATA_DIR` (the container's `/data` by default) is durable by
   construction — mounting it is itself the signal.
2. **`OVERCAST_DATA_DIR` was explicitly configured.** Setting it yourself (native
   installs, or a Docker image customization) is evidence you intend that directory to
   be used, whether or not it happens to be a mount.
3. **An existing Overcast database is already present in the resolved data directory.**
   This is a regression guard: if you (or a previous container) already persisted state
   there, `auto` will never strand it in memory mode, even with neither of the signals
   above present.

If none of these hold, `auto` resolves to **`memory`** — persisting into a directory
nobody asked for and nothing mounts is pointless, so the fast, ephemeral backend is the
better default. In practice this means:

- `docker run -v mydata:/data ghcr.io/neaox/overcast` → **`hybrid`** (signal 1), with zero
  configuration.
- `docker run ghcr.io/neaox/overcast` (no volume) → **`memory`** — including CI, where
  containers typically run with no data volume, so `auto` lands on the fast, ephemeral
  mode CI wants automatically.
- A native install that already has data at `~/.overcast/data` → **`hybrid`** (signal 3),
  so upgrading to this default never silently drops existing data.
- `docker run -v mydata:/data ghcr.io/neaox/overcast-slim` → **`memory`**, because the slim
  image has no SQLite and `hybrid` does not exist in it. The signals above are not even
  consulted. See [Builds without SQLite](#builds-without-sqlite).

`OVERCAST_STATE` set to anything else (including explicitly `memory`) always wins outright
— `auto` only ever applies when the variable is unset or literally `auto`.

To see what `auto` actually chose for a running instance, check either:

- the startup log line: `storage mode auto-detected: <mode> (<reason>) — set OVERCAST_STATE to override`
- the Metrics & Health page in the web console (or `GET /_debug/metrics`'s
  `advisories` array), which surfaces an info-level advisory whenever the resolved mode
  is `memory`, with the concrete steps to change it.

---

## Builds without SQLite

`hybrid` and `persistent` store their data in SQLite, and **two of the published artifacts
are compiled without a SQLite driver** (`-tags nosqlite`). Which artifact you are running
therefore decides which backends exist at all:

| Artifact                             | SQLite | `hybrid` / `persistent` | `memory` / `wal` | `auto` can resolve to |
| ------------------------------------ | ------ | ----------------------- | ---------------- | --------------------- |
| `ghcr.io/neaox/overcast` image       | yes    | available               | available        | `hybrid` or `memory`  |
| `overcast-<os>-<arch>` binaries      | yes    | available               | available        | `hybrid` or `memory`  |
| `ghcr.io/neaox/overcast-slim` image  | **no** | **unavailable**         | available        | **`memory` only**     |
| `overcastd-<os>-<arch>` binaries     | **no** | **unavailable**         | available        | **`memory` only**     |

In an artifact without SQLite:

- **`auto` always resolves to `memory`.** It short-circuits before the three signals above
  are weighed, so **mounting a volume at `/data` buys you nothing** — the container starts
  normally, serves normally, and every bucket, queue and table is gone on restart. This is
  the one case where the `auto` rule described above does not apply.
- **`OVERCAST_STATE=hybrid` and `OVERCAST_STATE=persistent` refuse to start**, exiting
  non-zero with `init state backend: hybrid store: not compiled with SQLite support`.
  Failing loudly is deliberate: the alternative is pretending to persist.
- **`OVERCAST_STATE=wal` works, and is how you get durability from these artifacts.** The
  `wal` backend is an append-only log with no SQLite dependency, so it is compiled into
  every build. Its constraint is the one in the table at the top of this page — the whole
  dataset lives in memory — which local development and CI comfortably satisfy.

So a persistent slim container needs the volume **and** the backend:

```bash
docker run --rm -p 4566:4566 \
  -v overcast-data:/data \
  -e OVERCAST_STATE=wal \
  ghcr.io/neaox/overcast-slim:alpha
```

If you specifically want `hybrid` or `persistent`, use the full `ghcr.io/neaox/overcast`
image or an `overcast-<os>-<arch>` binary — both include SQLite and behave exactly as the
rest of this page describes.

To tell which kind of build you are running, read the startup log: a build without SQLite
says so in the auto-detection reason.

```text
storage mode auto-detected: memory (hybrid requires SQLite support, which this build
excludes (-tags nosqlite); falling back to memory) — set OVERCAST_STATE to override
```

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

**Bulk content is not in the state backend and is not covered by the list above.** S3
object bodies are files under `OVERCAST_DATA_DIR`, and container images pushed to the
emulated ECR are in a named Docker volume. Both are reclaimed by deleting them, not by
changing `OVERCAST_STATE`. The ECR one is the case worth knowing: images survive a restart
on any backend, including `memory`, because the first read of a repository reconciles it
against the registry and rediscovers what is there — so re-creating a repository is enough
to get its images back. See [ECR § Persistence](./services/ecr.md#persistence).

**If the underlying SQLite file becomes unreadable or corrupt**, `persistent` and `hybrid`
log a warning and — for `hybrid` only — keep serving reads and writes in a degraded,
memory-only mode for the rest of that run rather than crashing; `/_health` reports this
condition. This is a rare, unusual-environment case, not something to expect in normal use.

---

## Choosing a backend

- **Leave `OVERCAST_STATE` unset** unless you have a specific reason not to — `auto` picks
  `hybrid` when there's evidence you want persistence (a mounted volume, an explicit data
  directory, or an existing database) and `memory` otherwise, which is usually exactly
  what you want without having to think about it.
- **Reach for `persistent`** when correctness depends on every write being durable
  *before the call returns* — for example, reproducing a crash-recovery scenario, or
  verifying behavior that must not depend on `hybrid`'s async flush timing.
- **Reach for `wal`** when you want durability with a simpler on-disk format than SQLite
  and your dataset is small enough to live entirely in memory comfortably. It is also the
  *only* durable backend in the `overcast-slim` image and the `overcastd` binaries — see
  [Builds without SQLite](#builds-without-sqlite).
- **Use `memory`** for tests, CI, and any workflow that doesn't need state to survive a
  restart — it's the fastest backend by a wide margin.
- **Mix backends per service** with `OVERCAST_STATE_<SERVICE>` — e.g. `hybrid` globally
  with `persistent` only for the one service under test. See
  [docs/README.md § Per-service storage overrides](./README.md#per-service-storage-overrides).

Contributing to Overcast and need the implementation-level detail (WAL/overlay
internals, flush/compaction mechanics, migration behavior)? See
[docs/dev/storage-backends.md](./dev/storage-backends.md).
