---
title: "Storage and persistence"
description: "Pick and configure a storage backend: what auto resolves to, what survives a restart on each of memory/wal/persistent/hybrid, per-service overrides, and which backends each published artifact ships with."
section: "Storage & Performance"
tags:
  - docs
  - storage
  - persistence
  - state
  - durability
  - slim
---

# Storage and persistence

Overcast keeps emulated state in one of four backends, selected with
`OVERCAST_STATE` (globally) or `OVERCAST_STATE_<SERVICE>` (per service). Leave it
unset and mount a volume — `auto` does the rest:

```bash
docker run --rm -p 4566:4566 \
  -v $(pwd)/overcast-data:/data \
  ghcr.io/overcast-sh/overcast:latest
```

That resolves to `hybrid`, because a volume is mounted at `/data`. With no volume
it resolves to `memory` — which is what CI wants, with zero configuration.

> [!IMPORTANT]
> `hybrid` and `persistent` need SQLite, and two published artifacts — the
> **`overcast-slim` image** and the **`overcastd` binaries** — are built without
> it. There, `auto` can only resolve to `memory`, mounting a volume changes
> nothing, and `wal` is the only durable backend. See
> [Builds without SQLite](#builds-without-sqlite).

## At a glance

| Backend                  | Durability                                                                           | Memory residency                                                              | Speed                                                                    |
| ------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
| `auto` (unset / default) | Resolved at startup to one of the rows below.                                        | Depends on what it resolves to.                                                | Depends on what it resolves to.                                          |
| `memory`                 | None — lost on process exit.                                                          | Full dataset, always.                                                           | Fastest — no disk I/O at all.                                              |
| `hybrid`                 | Async — writes are journaled immediately, batch-flushed to SQLite in the background.  | Partial — resource metadata is always in memory; bulk data reads from SQLite. | Fast for typical use — memory-speed for most reads, cheap async writes.   |
| `persistent`             | Every mutation committed to SQLite before the call returns.                           | None — every operation is a live SQLite query.                                | Slowest — a real disk (or page-cache) round trip per operation.           |
| `wal`                    | Append-only log, replayed on restart; fsync policy configurable.                      | Full dataset, always (an in-memory store with a durability log attached).     | Reads are memory-speed; writes pay a log append and periodic compaction. |

Which one to reach for:

| You want                                                              | Set                                                              |
| --------------------------------------------------------------------- | ---------------------------------------------------------------- |
| State to survive a restart, without thinking about it                 | Nothing — mount a volume and let `auto` pick `hybrid`            |
| Every write durable *before the call returns* (crash-recovery tests)  | `OVERCAST_STATE=persistent`                                      |
| Durability without SQLite (slim image, `overcastd`, small datasets)   | `OVERCAST_STATE=wal`                                             |
| The fastest possible run, nothing kept (tests, CI)                    | `OVERCAST_STATE=memory` — or just don't mount a volume           |
| One service durable and the rest ephemeral                            | `OVERCAST_STATE_<SERVICE>` — see [below](#per-service-storage-overrides) |

`hybrid`, `persistent` and `wal` all store their files under `OVERCAST_DATA_DIR`:
`overcast.db` for the two SQLite backends, `overcast.wal` for the log. On a slow
filesystem those files are the bottleneck — see
[Performance § Data dir placement](./performance.md#data-dir-placement-avoid-host-bind-mounts-on-docker-desktop).

## The `auto` default

When `OVERCAST_STATE` is unset, Overcast looks for evidence you want
persistence, in this order:

1. **The data directory is a mounted volume or bind mount.** A Docker named
   volume or bind mount at `OVERCAST_DATA_DIR` (the container's `/data` by
   default) is durable by construction — mounting it is itself the signal.
2. **`OVERCAST_DATA_DIR` was explicitly configured.** Setting it yourself
   (native installs, or a Docker image customization) is evidence you intend
   that directory to be used, whether or not it happens to be a mount.
3. **An existing Overcast database is already in the resolved data directory.**
   A regression guard: state persisted there by a previous run is never
   stranded in memory mode.

None of them, and `auto` resolves to **`memory`** — persisting into a directory
nobody asked for and nothing mounts is pointless. In practice:

| Command                                                          | Resolves to                                     |
| ---------------------------------------------------------------- | ----------------------------------------------- |
| `docker run -v mydata:/data …/overcast`                          | `hybrid` (signal 1)                             |
| `docker run …/overcast` (no volume — including CI)               | `memory`                                        |
| A native install with data already at `~/.overcast/data`         | `hybrid` (signal 3)                             |
| `docker run -v mydata:/data …/overcast-slim`                     | `memory` — no SQLite, signals not even consulted |

Any explicit `OVERCAST_STATE` (including `memory`) wins outright; `auto` applies
only when the variable is unset or literally `auto`. To see what it chose, read
the startup log line —
`storage mode auto-detected: <mode> (<reason>) — set OVERCAST_STATE to override`
— or the Metrics & Health page in the web console, which raises an advisory
whenever the resolved mode is `memory`.

## Builds without SQLite

Which artifact you run decides which backends exist at all:

| Artifact                                  | SQLite | `hybrid` / `persistent` | `memory` / `wal` | `auto` can resolve to |
| ----------------------------------------- | ------ | ----------------------- | ---------------- | --------------------- |
| `ghcr.io/overcast-sh/overcast` image      | yes    | available               | available        | `hybrid` or `memory`  |
| `overcast-<os>-<arch>` binaries           | yes    | available               | available        | `hybrid` or `memory`  |
| `ghcr.io/overcast-sh/overcast-slim` image | **no** | **unavailable**         | available        | **`memory` only**     |
| `overcastd-<os>-<arch>` binaries          | **no** | **unavailable**         | available        | **`memory` only**     |

In an artifact without SQLite:

- **`auto` always resolves to `memory`**, short-circuiting before the three
  signals are weighed — so **mounting a volume at `/data` buys you nothing**.
  The container starts normally, serves normally, and every bucket, queue and
  table is gone on restart.
- **`hybrid` and `persistent` refuse to start**, exiting non-zero with
  `init state backend: hybrid store: not compiled with SQLite support`. Failing
  loudly is deliberate: the alternative is pretending to persist.
- **`wal` works, and is how you get durability here.** It is an append-only log
  with no SQLite dependency, so it is compiled into every build. Its one
  constraint is in the table above: the whole dataset lives in memory.

So a persistent slim container needs the volume **and** the backend:

```bash
docker run --rm -p 4566:4566 \
  -v overcast-data:/data \
  -e OVERCAST_STATE=wal \
  ghcr.io/overcast-sh/overcast-slim:latest
```

A build without SQLite says so in the startup log's auto-detection reason:

```text
storage mode auto-detected: memory (hybrid requires SQLite support, which this build
excludes (-tags nosqlite); falling back to memory) — set OVERCAST_STATE to override
```

## What survives a restart or crash

| Backend      | After a graceful stop | After a process kill                | After an OS crash / power loss                   |
| ------------ | --------------------- | ----------------------------------- | ------------------------------------------------- |
| `memory`     | nothing               | nothing                             | nothing                                           |
| `wal`        | everything            | everything but a torn final entry   | everything but a torn final entry                 |
| `persistent` | everything            | everything acknowledged to a client | everything acknowledged to a client               |
| `hybrid`     | everything            | everything that reached the pending log | at most one flush interval's writes are lost |

`hybrid` writes land in a durable pending log immediately and flush to SQLite in
the background, so a kill loses nothing and a power cut loses a fraction of a
second. On a graceful stop Overcast tries to flush everything within
`OVERCAST_SHUTDOWN_TIMEOUT`; even when that budget runs out nothing is lost —
the remainder replays from the pending log on the next start.

> [!NOTE]
> **Bulk content is not in the state backend** and none of the above applies to
> it. S3 object bodies are files under `OVERCAST_DATA_DIR`, and images pushed to
> the emulated ECR live in a named Docker volume. The ECR case is the one worth
> knowing: images survive a restart on *any* backend, `memory` included, because
> the first read of a repository reconciles it against the registry — so
> re-creating a repository is enough to get its images back. See
> [ECR § Persistence](./services/ecr/limitations.md#persistence).

If the SQLite file becomes unreadable or corrupt, `persistent` and `hybrid` log a
warning and — `hybrid` only — keep serving in a degraded, memory-only mode for
the rest of the run rather than crashing. `/_overcast/health` reports it.

## Per-service storage overrides

Each service can use a different backend. Set `OVERCAST_STATE_<SERVICE>`, where
`<SERVICE>` is one of the [service names](./configuration.md#service-names) in
upper case — CloudWatch Logs is `logs`, so its override is `OVERCAST_STATE_LOGS`:

```bash
docker run --rm -p 4566:4566 \
  -e OVERCAST_STATE=memory \
  -e OVERCAST_STATE_DYNAMODB=persistent \
  -e OVERCAST_STATE_S3=hybrid \
  -v $(pwd)/data:/data \
  ghcr.io/overcast-sh/overcast:latest
```

DynamoDB now writes synchronously to disk, S3 flushes asynchronously, and every
other service is ephemeral. Each overridden service gets its own SQLite file
under `$OVERCAST_DATA_DIR/<service>/`.

> [!NOTE]
> Four services accept an override that can have no effect, and log a startup
> warning when one is set: `DYNAMODBSTREAMS` (a facade over `dynamodb`, which
> owns all stream state), `STS` (its session state lives under IAM's storage),
> and `BEDROCK`/`ORGANIZATIONS` (stateless stubs). Every other service's
> override works.

## Where the active configuration is visible

| Where                        | Shows                                                                                                                      |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `GET /_overcast/health`      | The `storage` object: resolved default (`default`), what was configured (`configured`), per-service overrides, backend health |
| Web console dashboard footer | The storage mode, with a tooltip listing overrides                                                                          |
| Startup log                  | Which mode `auto` picked and why                                                                                            |

## Related

- [Configuration reference](./configuration.md) — every `OVERCAST_STATE*`,
  `OVERCAST_HYBRID_*` and `OVERCAST_WAL_*` variable with its default.
- [Performance](./performance.md) — flush tuning, the slow-filesystem probe, and
  why a Docker Desktop bind mount is the wrong home for `/data`.
