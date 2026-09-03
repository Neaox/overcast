---
title: "Performance and memory"
description: "The startup, memory and image-size targets, what each storage backend costs at startup, and where client-side wall time goes when a workflow feels slow."
section: "Storage & Performance"
tags:
  - docs
  - guide
  - memory
  - performance
---

# Performance and memory

Overcast aims to be fast and lean: sub-50 ms startup, under 15 MiB at idle, and
low per-request overhead. CI pipelines should not wait for the emulator.

## Targets

| Metric | Target |
| --- | --- |
| Startup time | < 50 ms (currently ~22 ms p50, hybrid backend) |
| Idle memory | < 15 MiB |
| Docker image (slim) | < 40 MiB — Go binary only, no web console |
| Docker image (console) | < 100 MiB — includes web management console |
| Request overhead (emulator-added latency) | < 1 ms for simple operations |

Every claim here is measured with default settings, including
`OVERCAST_EKS_MODE=mock`. The opt-in EKS live mode
(`OVERCAST_EKS_MODE=live`) launches k3s containers and costs materially more at
startup and in memory by design; treat it as a separate operating profile.

## Per-backend startup (cold start, empty data dir)

| Backend | Internal startup_ms | Wall spawn-to-ready |
| --- | --- | --- |
| `memory` | 1–2 ms | ~40 ms |
| `hybrid` (auto, when persisting) | 4–5 ms | ~40 ms |
| `wal` (append log, async fsync) | 5–8 ms | ~40 ms |
| `persistent` (SQLite) | 2–6 ms | ~40 ms |

The two SQLite-backed backends (`hybrid` and `persistent`) defer the
modernc/sqlite cold-migrate cost (~200–340 ms) off the critical path: the
migration runs in a background goroutine and the first DB-touching request
blocks on it. `memory` and `wal` use no SQLite and never pay it at all.

Each row was measured with `OVERCAST_STATE` set explicitly to that backend. The
default, `auto`, costs whichever row it resolves to plus a negligible resolution
check — see [The auto default](./storage.md#the-auto-default). The
`overcast-slim` image and the `overcastd` binaries have no SQLite, so their
durable option is `wal` — see [Builds without
SQLite](./storage.md#builds-without-sqlite). Where `/data` lives and how the
hybrid backend flushes are what move these numbers on a real machine — see
[Storage tuning](./performance/storage-tuning.md).

<details>
<summary>Measurement conditions</summary>

Measured 2026-04-17 in the dev container (Debian 12, x86_64, Go 1.23,
modernc/sqlite pure-Go driver, every service registered, no SDK clients
connected) with `OVERCAST_STATE=<backend>`, `OVERCAST_DATA_DIR=<empty tmp>`,
polling `/_overcast/metrics` every 5 ms from a sibling Go process. Wall time is
`os.Process.Start` → first HTTP 200 on `/_overcast/metrics`. Internal startup is
`startup_duration_ms` from that endpoint (package-init `startTime` → end of
`router.New()`). Numbers are best-of-5 cold runs (fresh `tmp` dir each
iteration); warm-cache runs are 1–2 ms faster across the board and not reported.

</details>

## Client-perceived latency — where "Overcast feels slow" actually goes

Fast request handling does not guarantee a fast-*feeling* workflow. When a tool
like the CDK drives Overcast, most of the wall-clock time the user experiences
is spent in the client. Establish which side owns the time before assuming it is
the emulator: every real AWS API call is logged at `INFO` by default (see
[`OVERCAST_LOG_LEVEL`](./configuration/log-levels.md)), so `docker logs
--timestamps` shows every request's duration and, by omission, every gap where
the emulator was idle.

Worked example, measured 2026-07-19 (Docker Desktop on Windows 11 / WSL2, hybrid
backend; a CDK v2 bootstrap-and-deploy of four application stacks that took
~45 s wall-clock while every Overcast response completed in <200 ms):

| Segment | Wall time | Owner | Notes |
| --- | --- | --- | --- |
| CDK CLI (Node.js) startup | ~5 s | client | before the first request reaches Overcast |
| Toolkit stack check/deploy | ~5.4 s | client | dominated by CDK poll intervals; responses <200 ms |
| `cdk synth` | ~8.4 s | client | zero requests hit the emulator during this gap |
| 4 × app stack deploy | ~21 s | client | ~5.1 s per stack = one SDK waiter `minDelay`; see below |
| Emulator processing (total) | <2 s | Overcast | sum of all request durations across the entire window |

**The SDK-waiter tax.** Stack provisioning is asynchronous: `CreateStack` /
`ExecuteChangeSet` return `*_IN_PROGRESS` and a goroutine provisions the
resources, typically in milliseconds (probe: a 3-resource stack reached
`CREATE_COMPLETE` **184 ms** after `CreateStack`, measured by polling
`DescribeStacks` every 100 ms with `curl`). But the AWS SDK waiter checks
immediately, sees `IN_PROGRESS` because provisioning started microseconds
earlier, then sleeps its 5 s `minDelay` before looking again. Every fast stack
therefore costs one full waiter cycle regardless of emulator speed. Overcast
shortens that window with a bounded synchronous wait
(`OVERCAST_CFN_SYNC_WAIT_MS`, default 1000 ms) so the waiter's first check on a
fast stack already sees the terminal status — see
[CloudFormation](./services/cloudformation.md).

**What the emulator cannot fix:** CDK CLI startup, `cdk synth`, and any other
client-side work show up as request-log silence. Report those upstream or
restructure the workflow (for example `cdk deploy --concurrency`); no Overcast
change will touch them.

## Related

- [Storage tuning](./performance/storage-tuning.md) — where `/data` should live, the slow-filesystem probe, and the hybrid flush knobs
- [Storage and persistence](./storage.md) — which backend to pick, and what each one guarantees
- [Debug endpoints](./debug-endpoints.md) — the metrics these numbers come from
- [Troubleshooting](./troubleshooting.md) — when something is failing rather than slow
