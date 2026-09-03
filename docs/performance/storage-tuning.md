---
title: "Storage tuning"
description: "Where /data should live, what the slow-filesystem probe is telling you, and the hybrid flush knobs — with the reasons not to touch most of them."
section: "Storage & Performance"
tags:
  - docker
  - docs
  - performance
  - storage
  - tuning
---

# Storage tuning

The levers for a specific case: a slow bind-mounted data directory,
crash-recovery testing, or a `hybrid flush slow` warning in the log. Which
backend to pick in the first place is [Storage and
persistence](../storage.md); the numbers to expect are
[Performance and memory](../performance.md).

## Fast, disposable state in CI

CI needs no `OVERCAST_STATE` setting: a CI container mounts no data volume, so
`auto` already resolves to `memory` and skips disk I/O entirely. Setting it
explicitly is still the right call if your CI mounts a volume you do not want
used. The same applies per service via `OVERCAST_STATE_<SERVICE>=memory` for a
single noisy service; see [Storage and persistence § Per-service storage
overrides](../storage.md#per-service-storage-overrides) for the override syntax.

## Data dir placement — avoid host bind mounts on Docker Desktop

SQLite (all persistent backends) fsyncs on every commit. When `/data` is a
**bind mount from a Windows or macOS host path**, each fsync crosses Docker
Desktop's file-sharing layer and costs orders of magnitude more than a native
filesystem write. Measured 2026-07-19 (Docker Desktop on Windows 11 / WSL2,
hybrid backend, `/data` bound to an NTFS path): background flushes of **3–8
entries took 0.6–1.0 s** (`hybrid flush slow` warnings in the log; threshold
500 ms). The hybrid backend's flush is asynchronous — it steals the dirty map
first, so requests are not blocked — but shutdown's final synchronous flush,
lazy namespace loads, and the `persistent`/`wal` backends' commit path all pay
this tax directly.

If you see `hybrid flush slow` in the logs, move the data dir off the bind
mount: use a **named Docker volume** for `/data`, so the data lives in the
Docker Desktop VM's native filesystem.

```bash
docker volume create overcast-data
docker run -d --name overcast \
  -p 4566:4566 -p 4567:4567 \
  -e OVERCAST_STATE=hybrid \
  -v overcast-data:/data \
  ghcr.io/overcast-sh/overcast
```

The same with `docker compose`:

```yaml
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast
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

When you need the state on the host (backup, inspection), export it explicitly
rather than bind-mounting:

```bash
docker cp overcast:/data ./overcast-data-backup
```

**Sharing a Lambda layer cache from the host is still fine.** The bind-mount
penalty is about SQLite's per-commit fsyncs, and read-mostly files like cached
layer zips do not pay it. To pre-download layers on the host (or share one cache
across containers), bind-mount just the layer directory and point
`LAMBDA_LAYER_CACHE_DIR` at it, while `/data` stays on the named volume:

```yaml
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast
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

## Startup slow-filesystem probe

The `hybrid` backend runs a one-time fsync micro-probe in the background right
after startup, never on the request path: it writes a few KB to a throwaway file
in the data dir, fsyncs it, times the round trip, and removes the file. Over
**75 ms**, it logs one `WARN` line naming the data dir and suggesting a named
Docker volume.

That threshold sits above native and container-native filesystem noise (a
healthy fsync of a few KB is low single-digit milliseconds, even on a loaded CI
runner) and below the bind-mount tax measured above (600 ms–1 s for a
multi-entry flush), so it catches that pathology at startup rather than at the
first `hybrid flush slow` warning under real write load.

The outcome is queryable as well as logged: `GET /_overcast/debug/metrics`
includes a `dataDirProbe` object per store (`{fsyncMillis, slow, probedAt}`)
alongside the flush-history and pending-log diagnostics. A probe that fails
outright — a permission error, say — reports as absent (`dataDirProbe: null`)
rather than a false "fast" or "slow" reading.

**What to do if you see the warning:** the same remedy as `hybrid flush slow`
above — move `/data` off the bind mount and onto a named Docker volume.

## Hybrid flush knobs — and when not to touch them

The default `hybrid` backend batches writes to SQLite in the background rather
than committing synchronously. A few environment variables control that
batching:

| Variable | Default | What it does |
| --- | --- | --- |
| `OVERCAST_HYBRID_FLUSH_INTERVAL` | `5s` | How often the background flush loop runs. |
| `OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD` | `10000` | Flush early once this many entries are dirty. |
| `OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD` | `8MiB` | Flush early once the dirty set reaches this size. |
| `OVERCAST_HYBRID_SYNC` | `interval` | fsync policy for the pending log: `always`, `interval`, or `never`. |
| `OVERCAST_HYBRID_SYNC_INTERVAL` | `100ms` | fsync cadence when `OVERCAST_HYBRID_SYNC=interval`. |

The defaults are tuned for local-dev and CI usage: fast enough that writes never
block a request, durable enough that a crash loses at most a fraction of a
second of data (see [Storage and persistence](../storage.md) for the full
durability picture). Change them only against a measured problem:

- Lowering `OVERCAST_HYBRID_SYNC_INTERVAL` or setting `OVERCAST_HYBRID_SYNC=always`
  trades write throughput for a smaller durability window — only worth it if you
  are reproducing a scenario that needs sub-100 ms durability guarantees.
- Raising the dirty thresholds delays flushes further, which increases the
  amount of data at risk on an OS crash or power loss without measurably
  speeding up requests (writes are already async and non-blocking).
- A `hybrid flush slow` warning is almost always the bind mount above, not these
  knobs.

`OVERCAST_SHUTDOWN_TIMEOUT` (default 5s) bounds how long a graceful shutdown
waits for the final flush before exiting anyway. Nothing is lost either way —
the remaining writes replay from the pending log on the next start — so raise it
only to have shutdown reliably wait for the flush, for example to avoid the
`store close exceeded shutdown timeout` log line.

## Related

- [Performance and memory](../performance.md) — the startup and memory numbers to expect
- [Storage and persistence](../storage.md) — which backend to pick, and what each one guarantees
- [Debug endpoints](../debug-endpoints.md) — `/_overcast/debug/metrics` and what it reports
- [Environment variable reference](../configuration/reference.md) — every variable on this page
