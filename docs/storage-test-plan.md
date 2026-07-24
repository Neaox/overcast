# Storage stabilization — full regression test plan

> **Scope:** the entire `feat/storage-stabilization` branch (Phases 1–2, Phase 3 Waves 1–3 of
> [storage-plan.md](./storage-plan.md)) before merge to `main`. The storage layer sits under every
> service, so the blast radius of a regression is global — this plan is deliberately broader than
> the per-item test requirements already met during development.
>
> **How to read it:** tiers T0–T6, ordered cheapest-first. Each tier lists what it protects
> against, exact commands, and pass criteria. "Gate" tiers (T0–T4) are automated and must be green
> before merge; T5–T6 are pre-release checks, partly manual. The **Status** column records the
> latest full execution against this branch.
>
> All Go commands assume no host toolchain: run them via `scripts/docker-go.sh` / `.ps1`, inside
> the devcontainer, or in a throwaway `golang:1.24-bookworm` container with the tree copied in
> (fastest and gives undistorted timings for `-race`/benchmarks — bind mounts skew both).

---

## T0 — Static analysis (gate, minutes)

Protects against: compile errors, vet warnings, type errors, drift between generated and source docs.

| Check | Command | Pass criteria |
|---|---|---|
| Go build (all packages) | `go build -tags slim ./...` | clean (`slim` skips the web-dist embed and generated docs index — required outside `make docs`/`npm build` environments) |
| Go vet | `go vet -tags slim ./...` | zero findings |
| gofmt | `gofmt -l` over changed files, LF-normalized first (`tr -d '\r'`) on Windows checkouts | empty output |
| TypeScript | `npx tsc --noEmit` in `web/` | clean |
| Lint | `make lint` (devcontainer/CI — golangci-lint + ESLint + actionlint) | clean |

## T1 — Unit + race suites (gate, ~30 min)

Protects against: logic regressions in the storage core and every touched service; data races in
the new concurrency (flush loops, debounced buffers, sweepers, maintenance goroutines).

| Check | Command | Pass criteria |
|---|---|---|
| Full test tree | `go test -count=1 ./...` | all green |
| Race: storage core | `go test -race -count=1 ./internal/state/...` | green |
| Race: concurrent services | `go test -race -count=1 ./internal/services/cloudwatch/... ./internal/services/cloudformation/... ./internal/services/dynamodb/... ./internal/router/...` | green |
| Web unit | `npx vitest run` in `web/` | all green |

**Named regression gates** (each guards a specific bug fixed on this branch — a failure here is a
recurrence, not noise):

- `TestHybridStore_DirtyEntryThresholdTriggersEarlyFlush` ×10 under `-race`, **in isolation** —
  the seed lost-wakeup fix. Historical rates: 5/10 pass before the fix, 20/20 after. If it fails
  in a *full-suite* run but passes 10/10 isolated, that's the documented load-flake pattern
  (storage-plan.md Wave 1/2 notes); the failure message now dumps the health snapshot to
  disambiguate a stuck/failing flush from a never-started one.
- Crash-recovery suite (`internal/state`: torn final line, corrupt mid-file line, replay
  idempotence, flush-failure retention, `TestHybridStore_DegradedPendingLogCap_stopsFileGrowth`).
- ScanPage no-duplicates/no-gaps suites across all five `Store` implementations (`*_ScanPage_*`).
- Migration runner suite (`internal/state/migrate_test.go`) + per-service migration tests
  (logs blob→row conversion fixtures, DynamoDB table adoption).
- Poisoned-row isolation tests (one corrupt record never fails a list — state + service level).
- N+1 sweep behavior tests (`internal/services/apigateway/store_n1_sweep_test.go`).

## T2 — Storage-mode matrix (gate for hybrid/memory; pre-release for persistent/wal)

Protects against: behavior that only holds in one backend. Every functional guarantee must hold
under `OVERCAST_STATE = memory | hybrid | persistent | wal`, plus at least one
`OVERCAST_STATE_<SVC>` override combination (which wraps everything in `NamespacedStore` — the
interface-erasure class of bug fixed in 1.1).

- Automated today: dual-backend unit suites run both implementations directly (DynamoDB
  items/streams, logs `event_backend_test.go`, metric retention per backend, store parity suites
  in `internal/state`). These are part of T1.
- Integration suites run against the test server's default backend. **Pre-release:** re-run
  `go test -count=1 ./tests/integration/...` with `OVERCAST_STATE=persistent` and `=wal`
  exported, and once with a mixed override (e.g. `OVERCAST_STATE_S3=memory` on hybrid) —
  the 1.1 regression test covers the routing, this covers the breadth. `wal` mode has the
  thinnest standing coverage; prioritize it if time-boxed.

## T3 — Persistence, upgrade, crash (pre-release, semi-automated)

Protects against: data loss across restarts, broken migrations against real pre-branch data,
unrecoverable state after a crash.

1. **Restart persistence (automated):** integration tests already cover write → restart server →
   read for DynamoDB and S3; the hybrid pending-log replay path is unit-covered.
2. **Upgrade path (manual, once per release):** take a data dir produced by the current `main`
   binary (or a copy of a real dev `/data` volume), start the branch binary against it, verify:
   migrations 1→21 apply in order with one `overcast.db.bak-v0` backup written; CloudWatch Logs
   blob events appear via `GetLogEvents` (blob→row conversion) and the old `logs:events` kv rows
   are gone; DynamoDB items intact; `cdk diff` against a previously-deployed stack shows no drift.
3. **Downgrade story:** restore `overcast.db.bak-v<N>` + the old binary; document-only, no tooling.
4. **Crash mid-burst (manual):** `docker kill -s KILL` during a write burst; restart; verify the
   "hybrid pending log replay" log line, no data older than the last fsync interval (100 ms
   default) lost, and no startup error from a torn final log line.
5. **Corrupt/unopenable DB:** replace `overcast.db` with garbage; server must start memory-only,
   log the degradation once, report `Healthy:false` via `/_health`, and cap the pending log at
   64 MiB (unit-covered; spot-check the health surface manually).

## T4 — Concurrency + burst (gate; benchmarks recorded per storage-plan 2.1)

Protects against: throughput collapse or quadratic behavior under load — the class of problem
that motivated the plan (blob rewrites, N+1 scans, single-connection contention).

| Workload | How | Pass criteria |
|---|---|---|
| CloudWatch **PutMetricData burst** | `go test -bench 'BenchmarkCloudWatch_PutMetricDataHybrid' -run '^$' ./internal/services/cloudwatch/` | allocs/op and B/op flat across 0 / 2 000 / 8 000 retained points (allocations are the deterministic signal; wall time wobbles with I/O). This gate exists because the Wave 1 retention change made per-put cost O(points-in-window) on hybrid — quadratic over a burst; fixed by removing the inline prune (reads filter, sweep deletes). Measured 2026-07-24, golang:1.24-bookworm container on Docker Desktop/Windows 11 (Ryzen, 24 threads), container-native FS, `-benchtime 1s`: pre-fix 240→40 539→160 170 allocs/op and 23 KB→2.6 MB→10.6 MB/op at 0/2 000/8 000 retained (~79 ms/op at 8 000); post-fix 10–20 allocs/op, ~1.3–1.9 KB/op, ~66–70 µs/op, flat at every preload size. |
| CloudWatch **Logs append burst** | `go test -bench 'BenchmarkLogsStore_AppendEvents' -run '^$' ./internal/services/cloudwatch/logs/` | ns/op flat across 100 / 10 000 / 1 000 000 pre-existing events (the blob design grew linearly; the table design must not) |
| Hybrid mixed read/write during flush | `go test -bench 'BenchmarkHybridStore_MixedReadWriteDuringFlush' -run '^$' ./internal/state/` | reads don't stall behind flush transactions (read-pool split, 1.2) |
| Sustained-write bound | `TestHybridStore_DirtyEntryThresholdTriggersEarlyFlush` + 1.4 acceptance: dirty count stays bounded regardless of `FlushInterval` | covered in T1 |
| Cold start vs DB size | `go test -bench 'BenchmarkHybridStore_ColdStartHydration' -run '^$' ./internal/state/` | linear in row count, no regression vs recorded baselines |

Benchmark conditions per [performance.md](./performance.md): record machine, container vs native,
tmpfs vs disk, and Go version alongside any number quoted from these.

## T5 — End-to-end (pre-release, manual + scripted)

Protects against: integration seams no Go test crosses — wire format with real SDKs, the BFF/web
UI, Docker packaging.

1. **Docker images build:** `docker build` both `overcast` and `overcast-slim` targets.
2. **SDK smoke against `docker compose up`:** real `aws` CLI: S3 put/get/list-objects-v2 (+
   multipart), SQS send/receive/purge, DynamoDB put/get (restart in the middle to prove
   persistence), Logs put-log-events/get-log-events, `cloudwatch put-metric-data` in a loop
   (~1 000 puts to one metric) then `get-metric-statistics` — verifies the burst fix end-to-end
   and that expired points never surface.
3. **CDK:** `cdk bootstrap` + deploy/destroy of a stack using the supported resource matrix —
   the highest-value single test (exercises CFN events row storage, IAM, S3 assets, Lambda, and
   the hybrid flush path under real deploy load).
4. **Web UI (Raw State Debugger, Wave 3):** against a namespace with >1 000 keys — initial page
   loads 500, scrolling fetches more, tree view collapses/expands under virtualization, key
   search shows the "Load more" affordance instead of auto-paging, deep-link to a key on an
   unloaded page resolves via the single-key fallback, `/_debug/reset` clears virtual namespaces
   (logs/DynamoDB) too. Check `/_debug/metrics?includeRowCounts=true` shows flush history and
   sane counts while the burst from step 2 is running.
5. **Compat suite:** run the `rust-sdk-compat` image job if the release includes it.

## T6 — Operational behavior (pre-release, manual)

1. **Startup gating:** against a large existing DB, requests during the migration window get 503
   `ServiceUnavailable` + `Retry-After` (never a hang or empty-state success); `/_`-prefixed
   endpoints stay reachable throughout. Before the port binds, clients see connection-refused —
   both states are SDK-retryable.
2. **Shutdown:** `docker stop` completes within the grace period; on a deliberately slowed
   `/data` (bind mount), the "store close exceeded shutdown timeout" warning appears, the process
   still exits, and the next start replays the pending log cleanly. `serverErr`-path shutdown
   (kill the listener) runs the same cleanup tail (CloudWatch Logs `Stop` flush included).
3. **Long-run soak (optional):** leave the daemon under light CDK/SDK traffic for a few hours;
   confirm RSS and `overcast.db` size plateau (retention sweeps + auto_vacuum), no goroutine
   growth in `/_debug/pprof/goroutine`.

---

## Execution record — branch `feat/storage-stabilization` @ post-burst-fix

Environment: `golang:1.24-bookworm` container (tree copied to container-native FS), Docker Desktop
on Windows 11, Node v24 on host. See git history for the exact commit.

| Tier | Result |
|---|---|
| T0 build/vet (`-tags slim ./...`) | ✅ clean |
| T0 tsc | ✅ clean |
| T1 full `go test ./...` | ✅ (see PR/session notes for timings) |
| T1 race: state + cloudwatch(+logs) + cfn + dynamodb + router | ✅ |
| T1 lost-wakeup gate (×5–10 isolated `-race`) | ✅ |
| T1 web `vitest run` (full) | ✅ |
| T4 PutMetricData burst benchmark | ✅ flat after fix (10–20 allocs/op regardless of retained points); pre-fix grew ~20 allocs per retained point per put — see the T4 table for full numbers/conditions |
| T4 Logs append burst benchmarks | ✅ flat across preload sizes |
| T2 persistent/wal integration matrix | ⬜ pre-release |
| T3 upgrade-path against real `main` data dir | ⬜ pre-release |
| T5 Docker/SDK/CDK/UI smoke | ⬜ pre-release |
| T6 startup/shutdown/soak | ⬜ pre-release |
