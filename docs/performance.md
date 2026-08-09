---
title: "Performance and memory guide"
description: "Overcast aims to be fast and lean: sub-50ms startup, under 15 MiB at idle, and low per-request overhead. CI pipelines should not wait for the emulator."
section: "Getting Started"
tags:
  - docs
  - guide
  - memory
  - performance
---

# Performance and memory guide

Overcast aims to be fast and lean: sub-50ms startup, under 15 MiB at idle,
and low per-request overhead. CI pipelines should not wait for the emulator.

This guide covers what to expect and how to tune Overcast for your workflow.

---

## Goals

| Metric                                    | Target                                       |
| ----------------------------------------- | --------------------------------------------- |
| Startup time                              | < 50ms (currently ~22ms p50, hybrid backend) |
| Idle memory                               | < 15 MiB                                     |
| Docker image (slim)                       | < 40 MiB — Go binary only, no web UI         |
| Docker image (console)                    | < 100 MiB — includes web management console  |
| Request overhead (emulator-added latency) | < 1ms for simple operations                  |

Performance claims above are measured with default settings, including `OVERCAST_EKS_MODE=mock`.
The opt-in EKS live mode (`OVERCAST_EKS_MODE=live`) launches k3s containers and has materially higher
startup and memory cost by design; treat that mode as a separate operating profile.

### Per-backend startup (cold start, empty data dir)

The two SQLite-backed backends (`hybrid` and `persistent`) defer the
modernc/sqlite cold-migrate cost (~200–340 ms) off the critical path. The
migration runs in a background goroutine; the first DB-touching request blocks
on it. `memory` and `wal` use no SQLite and never pay it at all.

| Backend                     | Internal startup_ms | Wall spawn-to-ready |
| ---------------------------- | ------------------- | ------------------- |
| `memory`                     | 1–2 ms              | ~40 ms              |
| `hybrid` (auto, when persisting) | 4–5 ms          | ~40 ms              |
| `wal` (append log, async fsync) | 5–8 ms           | ~40 ms              |
| `persistent` (SQLite)        | 2–6 ms              | ~40 ms              |

Each row was measured with `OVERCAST_STATE` set explicitly to that backend. The default
(`OVERCAST_STATE` unset, i.e. `auto`) resolves to one of these at startup — `hybrid` when
a volume/bind mount or existing database is found, `memory` otherwise — so its measured
cost is whichever row it resolves to, plus the (negligible) resolution check itself. See
[docs/storage.md § The auto default](./storage.md#the-auto-default).

The `overcast-slim` image and the `overcastd` binaries are built without SQLite, so
`hybrid` and `persistent` do not exist in them and `auto` there always resolves to
`memory` — a mounted volume does not change that. Their durable option is `wal`. See
[docs/storage.md § Builds without SQLite](./storage.md#builds-without-sqlite).

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

## Storage backend tuning

Overcast's default (`OVERCAST_STATE` unset, i.e. `auto`) picks `hybrid` or `memory` at
startup based on whether there's evidence you want persistence — see
[docs/storage.md § The auto default](./storage.md#the-auto-default) for the exact rule,
and [§ Builds without SQLite](./storage.md#builds-without-sqlite) for the two artifacts
where `hybrid` is not an option at all.
If you're tuning for a specific case — crash-recovery testing, or a slow bind-mounted data
directory — these are the levers. See [docs/storage.md](./storage.md) for a full comparison
of the four concrete backends by durability and what survives a restart.

### Fast, disposable state in CI

CI needs no `OVERCAST_STATE` setting at all: a CI container typically runs with no data
volume mounted, so `auto` already resolves to `memory` — skipping disk I/O entirely, with
no durability, which is exactly right for a pipeline that starts fresh every run. Setting
`OVERCAST_STATE=memory` explicitly remains fine (and is still the right call if your CI
setup happens to mount a volume you don't want used, or you just prefer to be explicit
about it) — it's simply no longer necessary for the common case. The same applies per
service via `OVERCAST_STATE_<SERVICE>=memory` for a single noisy service; see
[docs/README.md § Per-service storage overrides](./README.md#per-service-storage-overrides)
for the override syntax.

### Data dir placement — avoid host bind mounts on Docker Desktop

SQLite (all persistent backends) fsyncs on every commit. When `/data`
is a **bind mount from a Windows or macOS host path**, each fsync
crosses Docker Desktop's file-sharing layer and costs orders of
magnitude more than a native filesystem write. Measured 2026-07-19
(Docker Desktop on Windows 11 / WSL2, hybrid backend, `/data` bound to
an NTFS path): background flushes of **3–8 entries took 0.6–1.0 s**
(`hybrid flush slow` warnings in the log; threshold 500 ms). The hybrid
backend's flush is asynchronous — it steals the dirty map first, so
requests are not blocked — but shutdown's final synchronous flush, lazy
namespace loads, and the `persistent`/`wal` backends' commit path all
pay this tax directly.

If you see `hybrid flush slow` in the logs, move the data dir off the
bind mount: use a **named Docker volume** for `/data` (data lives in the
Docker Desktop VM's native filesystem) and, if you need the state on the
host, export it explicitly instead of bind-mounting it.

The ideal setup with `docker run`:

```sh
docker volume create overcast-data
docker run -d --name overcast \
  -p 4566:4566 -p 4567:4567 \
  -e OVERCAST_STATE=hybrid \
  -v overcast-data:/data \
  ghcr.io/neaox/overcast
```

The same with `docker compose`:

```yaml
services:
  overcast:
    image: ghcr.io/neaox/overcast
    ports:
      - "4566:4566" # AWS API endpoint
      - "4567:4567" # web management console
    environment:
      OVERCAST_STATE: hybrid
    volumes:
      - overcast-data:/data # named volume — NOT ./some/host/path:/data
volumes:
  overcast-data:
```

When you need the state on the host (backup, inspection), export it
explicitly rather than bind-mounting:

```sh
docker cp overcast:/data ./overcast-data-backup
```

**Sharing a Lambda layer cache from the host is still fine.** The
bind-mount penalty is about SQLite's per-commit fsyncs — read-mostly
files like cached layer zips don't pay it. To pre-download layers on the
host (or share one cache across containers), bind-mount just the layer
directory and point `LAMBDA_LAYER_CACHE_DIR` at it, while `/data` stays
on the named volume:

```yaml
services:
  overcast:
    image: ghcr.io/neaox/overcast
    ports:
      - "4566:4566"
      - "4567:4567"
    environment:
      OVERCAST_STATE: hybrid
      LAMBDA_LAYER_CACHE_DIR: /layers
    volumes:
      - overcast-data:/data # SQLite state: named volume (fsync-sensitive)
      - ./layer-cache:/layers # layer zips: host bind mount is fine (read-mostly)
volumes:
  overcast-data:
```

### Startup slow-filesystem probe

Since the storage-pressure-handling work, `HybridStore` runs a one-time
fsync micro-probe in the background right after construction (never on
the request path): it writes a few KB to a throwaway file in the data
dir, fsyncs it, times the round trip, and removes the file. If that
takes longer than **75ms**, it logs one `WARN` line naming the data dir
and suggesting a named Docker volume, and links back to this section.

75ms is deliberately well above native/container-native filesystem noise
(a healthy fsync of a few KB is low single digits of milliseconds, even on
a loaded CI runner) and well below the bind-mount tax measured above
(600ms–1s for a multi-entry flush) — it exists to catch exactly that
pathology early, at startup, rather than waiting for the first `hybrid
flush slow` warning under real write load.

The probe's outcome is also queryable, not just logged: `GET
/_debug/metrics` includes a `dataDirProbe` object per store
(`{fsyncMillis, slow, probedAt}`) alongside the existing flush-history and
pending-log diagnostics, so tooling (or a future web UI health panel) can
surface it without scraping logs. A probe that fails outright (e.g. a
permission error) is reported as absent (`dataDirProbe: null`) rather than
a false "fast" or "slow" reading — that's a different, less actionable
condition than "the check ran and found a slow disk."

**What to do if you see the warning:** same remedy as `hybrid flush slow`
above — move `/data` off the bind mount and onto a named Docker volume.

### Hybrid flush knobs — and when not to touch them

The default `hybrid` backend batches writes to SQLite in the background rather than
committing synchronously. A few environment variables control that batching:

| Variable                                  | Default    | What it does                                                     |
| ------------------------------------------ | ---------- | ------------------------------------------------------------------ |
| `OVERCAST_HYBRID_FLUSH_INTERVAL`           | `5s`       | How often the background flush loop runs.                         |
| `OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD`    | `10000`    | Flush early once this many entries are dirty.                     |
| `OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD`     | `8MiB`     | Flush early once the dirty set reaches this size.                 |
| `OVERCAST_HYBRID_SYNC`                     | `interval` | fsync policy for the pending log: `always`, `interval`, or `never`. |
| `OVERCAST_HYBRID_SYNC_INTERVAL`            | `100ms`    | fsync cadence when `OVERCAST_HYBRID_SYNC=interval`.                |

These defaults are tuned for typical local-dev and CI usage — fast enough that writes
never block a request, durable enough that a crash loses at most a fraction of a second
of data (see [docs/storage.md](./storage.md) for the full durability picture). Don't
change them unless you've measured a specific problem:

- Lowering `OVERCAST_HYBRID_SYNC_INTERVAL` or setting `OVERCAST_HYBRID_SYNC=always`
  trades write throughput for a smaller durability window — only worth it if you're
  reproducing a scenario that needs sub-100ms durability guarantees.
- Raising the dirty thresholds delays flushes further, which increases the amount of
  data at risk on an OS crash or power loss without measurably speeding up requests
  (writes are already async and non-blocking).
- If you see a `hybrid flush slow` warning in the logs, the fix is almost always to
  move `/data` off a Docker Desktop bind mount, not to change these knobs — see
  "Data dir placement" above.

Also worth knowing: `OVERCAST_SHUTDOWN_TIMEOUT` (default 5s) bounds how long a graceful
shutdown waits for the final flush to finish before exiting anyway. Nothing is lost either
way — the remaining writes replay from the pending log on the next start — so raise it only
if you want shutdown to reliably wait for the flush (for example, to avoid the `store close
exceeded shutdown timeout` log line), not because data is at risk.

---

## Client-perceived latency — where "overcast feels slow" actually goes

Fast request handling does not guarantee a fast-*feeling* workflow.
When a tool like the CDK drives overcast, most of the wall-clock time
the user experiences is spent in the client, not the emulator. Before
assuming overcast itself is slow, establish which side owns the time —
the request log (every real AWS API call is logged at `INFO` by default —
see [`OVERCAST_LOG_LEVEL`](./README.md#configuration-reference)) with
`docker logs --timestamps` shows every request's duration and, by omission,
every gap where the emulator was idle.

Worked example, measured 2026-07-19 (Docker Desktop on Windows 11 /
WSL2, `overcast:dev`, hybrid backend, 15 services; a CDK v2
bootstrap-and-deploy of four application stacks that took ~45 s
wall-clock while every overcast response completed in <200 ms):

| Segment                      | Wall time | Owner    | Notes                                                                 |
| ----------------------------- | --------- | -------- | --------------------------------------------------------------------- |
| CDK CLI (Node.js) startup    | ~5 s      | client   | before the first request reaches overcast                             |
| Toolkit stack check/deploy   | ~5.4 s    | client   | dominated by CDK poll intervals; responses <200 ms                    |
| `cdk synth`                  | ~8.4 s    | client   | zero requests hit the emulator during this gap                        |
| 4 × app stack deploy         | ~21 s     | client   | ~5.1 s per stack = one SDK waiter `minDelay`; see below               |
| Emulator processing (total)  | <2 s      | overcast | sum of all request durations across the entire window                 |

**The SDK-waiter tax.** Stack provisioning is asynchronous: `CreateStack`
/ `ExecuteChangeSet` return `*_IN_PROGRESS` and a goroutine provisions
the resources — typically in milliseconds (probe: a 3-resource stack
reached `CREATE_COMPLETE` **184 ms** after `CreateStack`, measured by
polling `DescribeStacks` every 100 ms with `curl`). But the AWS SDK
waiter checks immediately — sees `IN_PROGRESS` because provisioning
started microseconds earlier — then sleeps its 5 s `minDelay` before
looking again. Every fast stack therefore costs one full waiter cycle
regardless of emulator speed. A fix that shortens this window (a bounded
synchronous wait so the waiter's first check already sees the terminal
status) is tracked in [docs/plans/cfn-sync-fastpath.md](plans/cfn-sync-fastpath.md).

**What the emulator cannot fix:** CDK CLI startup, `cdk synth`, and any
other client-side work show up as request-log silence. Report those
upstream or restructure the workflow (e.g. `cdk deploy --concurrency`);
no overcast change will touch them.

---

Contributing to Overcast and need the internals — startup-budget rules for service
authors, how to document a performance claim, or benchmark discipline? See
[docs/dev/performance.md](./dev/performance.md).
