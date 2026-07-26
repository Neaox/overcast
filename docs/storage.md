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

`OVERCAST_STATE` set to anything else (including explicitly `memory`) always wins outright
— `auto` only ever applies when the variable is unset or literally `auto`.

To see what `auto` actually chose for a running instance, check either:

- the startup log line: `storage mode auto-detected: <mode> (<reason>) — set OVERCAST_STATE to override`
- the Metrics & Health page in the web console (or `GET /_debug/metrics`'s
  `advisories` array), which surfaces an info-level advisory whenever the resolved mode
  is `memory`, with the concrete steps to change it.

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

- **Leave `OVERCAST_STATE` unset** unless you have a specific reason not to — `auto` picks
  `hybrid` when there's evidence you want persistence (a mounted volume, an explicit data
  directory, or an existing database) and `memory` otherwise, which is usually exactly
  what you want without having to think about it.
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

## Put the data directory on an SSD

Every disk-backed backend (`hybrid`, `persistent`, `wal`) commits with `fsync`, and `fsync`
latency — not throughput — is what determines how fast they are. On an SSD a flush costs
single-digit milliseconds. On a spinning disk it routinely costs 100ms or more, because each
commit waits for a physical seek. Overcast measures this at startup and warns when the median
`fsync` exceeds 75ms; if you are seeing `hybrid: data directory fsync is slow` or
`hybrid flush slow`, the data directory is the reason.

**An SSD is recommended for `hybrid`, `persistent` and `wal`.** `memory` never touches the
disk and is unaffected.

Under Docker this is decided by where the Docker *volume* lives, not by where your project
is checked out. Docker Desktop keeps all named volumes inside a single virtual disk, so if
that disk sits on an HDD, every volume is slow no matter which drive you run from.

If your SSD has room for everything, the simplest fix is to move Docker's disk image onto it
— Docker Desktop → Settings → Resources → Advanced → *Disk image location*.

**If your SSD is too small to hold every volume**, place just the Overcast volume there by
creating a named volume bound to a specific path:

```bash
docker volume create --driver local \
  --opt type=none --opt o=bind --opt device=/path/on/ssd/overcast-data \
  overcast-data

docker run --rm -p 4566:4566 -v overcast-data:/data ghcr.io/neaox/overcast:latest
```

One caveat on Windows and macOS: a path on a host drive reaches the container through a
file-sharing layer (9p/virtiofs/gRPC-FUSE) whose `fsync` cost can be worse than the HDD you
were trying to avoid, so binding to `/mnt/e/...` or `/Users/...` may not help. Keep the path
inside the Linux VM's own filesystem instead — on WSL2, a directory under `/mnt/wsl/` or a
second VHDX stored on the SSD and attached with `wsl --mount --vhd`. Overcast's startup probe
reports the filesystem type it detected (`fsType`, `mountClass`) via `GET /_debug/metrics`,
so you can confirm you landed on a native filesystem rather than a shared mount.

Contributing to Overcast and need the implementation-level detail (WAL/overlay
internals, flush/compaction mechanics, migration behavior)? See
[docs/dev/storage-backends.md](./dev/storage-backends.md).
