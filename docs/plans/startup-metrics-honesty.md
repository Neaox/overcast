# Honest startup metrics — plan

> Status: not started.
> Goal: stop attributing time overcast doesn't control (container creation,
> OS loader, antivirus scanning) to overcast's startup. Keep that time
> **visible** in the timeline as its own environment group, but exclude it
> from `startup_duration_ms` and label it truthfully per platform.

## Why (measured evidence)

Investigated 2026-07-19. A user-facing startup timeline showed
"Go runtime + package init: **2520ms**" for the Docker `overcast:dev`
container. Measured truth:

- `GODEBUG=inittrace=1` in the same image: **all Go package inits total
  ~45ms** (largest single init: 0.5ms).
- Forking `overcast serve` as a child process inside the container:
  fork → `runServe` = **10ms** (50ms via `su-exec`).
- The 0.5–2.5s comes from *before the binary runs*: the Docker
  entrypoints `exec` overcast so it inherits **PID 1**, and PID 1's
  `/proc` start time is when runc forked the container init — before
  namespaces, cgroups, bind mounts, and the shell entrypoint. All of
  that lands in the first phase because every anchor derives from
  `processStartTime()`.

Two code-level honesty bugs follow from that single anchor:

1. **The first phase is mislabeled.** `prof.mark("Go runtime + package init")`
   ([cmd_serve.go:59](../../cmd/overcast/cmd_serve.go)) measures
   `processStartTime() → runServe`, which in Docker includes container
   runtime setup and the shell entrypoint, and natively includes the OS
   loader and antivirus scanning.
2. **The headline number is polluted too.** `startup_duration_ms` =
   `readyTime − startTime` ([metrics.go:55](../../internal/router/metrics.go))
   where `startTime = processStartTime()`
   ([debug.go:77](../../internal/router/debug.go)) — so the metric that
   performance.md budgets at <50ms silently absorbed the environment time.
3. **performance.md is stale.** Its methodology section still says
   `startup_duration_ms` starts at a package-init `var startTime = time.Now()`;
   the code was since changed to `processStartTime()`. The doc and the
   code disagree, and both disagree with the <50ms claim's intent.

## Design

### Three anchors

| Anchor      | Source                                                        | Meaning                                   |
| ----------- | ------------------------------------------------------------- | ----------------------------------------- |
| `procStart` | `processStartTime()` (existing, per-OS)                       | OS-reported process creation              |
| `goStart`   | new: package-level `var GoStart = time.Now()` in a leaf package | earliest observable Go user code          |
| `ready`     | `readyTime` at end of `router.New()` (existing)               | routes wired, server ready                |

`goStart` lives in a new dependency-free package (e.g.
`internal/boottime`) imported by `internal/router` (and thus transitively
by everything that matters). Go initializes imported packages
dependency-first, so a leaf package's vars initialize before the packages
that import it. This is *best-effort earliest* — packages that don't
depend on it may init before it — but total init cost is ~45ms, so any
imprecision is small; document it as approximate.

### Reported metrics

- **`startup_duration_ms` = `ready − goStart`.** Go-side work only —
  the number the <50ms budget, `bench-startup`, and CI gates govern.
  This is a semantic change to an existing field; changelog it.
- **New field `pre_init_ms` = `goStart − procStart`.** Always reported
  (0/near-0 where `processStartTime` falls back to `time.Now()` —
  `procstart_other.go`).
- **`startup_phases`**: new first entry for the environment segment with
  a new `"environment": true` flag on `StartupPhase` so the web UI can
  render it distinctly and exclude it from the phase-sum total.
  Subsequent phase "Go runtime + package init" is re-anchored to
  `goStart` and becomes honest (~tens of ms).
- **`start_time` / `uptime`** stay anchored to `procStart` — for uptime,
  process creation *is* the honest anchor.
- `OVERCAST_PROFILE_STARTUP=1` stderr output prints the environment
  phase separately with the same label, and the total line uses the
  Go-anchored total.

### Platform-aware labeling

The environment segment means different things in different places; the
label should say which, so nobody repeats the 2026-07-19 goose chase:

- Linux **and** `os.Getpid() == 1` (both Docker entrypoints `exec` the
  binary): `"container init + entrypoint + exec (pre-Go)"`.
- Otherwise: `"OS process spawn: loader / AV / exec (pre-Go)"`. On
  native Windows this window is where Defender scans the unsigned
  ~50MB exe on first run — hundreds of ms users will rightly want to
  see attributed to their environment, not to overcast.

Keep the label choice in a small pure function
(`environmentPhaseLabel(goos string, pid int) string`) for direct unit
testing.

### Native binaries — why the same treatment is honest

`processStartTime()` is accurate for the process itself on every
platform (Windows `GetProcessTimes`, macOS `sysctl`, Linux `/proc`).
The PID 1 problem is Docker-specific (the fork predates the `exec` by
container-setup time). Natively, `pre_init_ms` is genuinely the loader +
AV + Go runtime bootstrap for this exact invocation — small on a warm
machine, large under aggressive AV — and surfacing it separately turns
"overcast is slow to start" reports into a one-look diagnosis. A shell
wrapper that `exec`s overcast folds the wrapper's time in (exec
preserves start time); the generic label already covers this.

## Workstreams

### P0 — failing tests first (per AGENTS.md)

1. Unit: with a stubbed `procStart` well before `goStart`,
   `startup_duration_ms` excludes the gap and `pre_init_ms` reports it.
   Requires making the anchor injectable for tests (package-level
   `var processStart = processStartTime()` overridable in-package, or a
   setter guarded for tests) — mirror however `clock.Clock` seams are
   done elsewhere; do not use `time.Now()` directly in new code paths.
2. Unit: `environmentPhaseLabel` returns the container label for
   ("linux", 1) and the generic label otherwise.
3. Unit/integration: `startup_phases[0]` is the environment phase with
   `environment: true`, and the remaining phases' `start_ms` are
   re-anchored (first Go phase starts at ~0).

### P1 — implementation

1. `internal/boottime` leaf package; import from `internal/router`.
2. Re-anchor `startup_duration_ms`, add `pre_init_ms`, add the
   `environment` flag to `StartupPhase`, emit the environment phase in
   `startupProfiler.finalize()` (or `RecordExternalPhase` from
   `cmd/overcast` before its first mark — pick one owner; finalize() is
   simpler since it already merges and seals).
3. Update `cmd/overcast/profile.go` phase names and stderr output.

### P2 — consumers

1. Web UI `web/src/features/metrics/startup-timeline.tsx` matches the
   phase by exact name (`n === "Go runtime + package init"`) — update
   matching, render the environment group greyed/hatched, exclude it
   from the displayed total, show `pre_init_ms` as its own annotation.
   (Do not edit `routeTree.gen.ts`; no route changes here anyway.)
2. `scripts/bench-startup.go` reads `startup_duration_ms` — verify it
   still parses; its internal-p50 numbers will drop to honest values.
   Refresh the sample table in performance.md if reprinting.

### P3 — documentation

1. **Fix the stale methodology section** in
   [docs/performance.md](../performance.md): it must describe the three
   anchors, state that `startup_duration_ms` is `goStart → ready`, that
   `pre_init_ms` exists and what it contains per platform (container
   setup when PID 1; loader/AV natively), and what is included/excluded
   — per the doc's own "Documenting performance claims" policy.
2. Changelog entry flagging the `startup_duration_ms` semantic change.
3. Cross-link from
   [docs/plans/cfn-sync-fastpath.md](./cfn-sync-fastpath.md) P3 (the
   perceived-latency doc work), which previously carried this as a
   ride-along note.

### Hygiene (before finishing)

- [ ] `gofmt -w` → `go vet` on changed packages; scoped tests for
      `./internal/router/...` and `./cmd/overcast/...`.
- [ ] `npx tsc --noEmit` in `web/` for the timeline change.
- [ ] `make bench-startup` passes; numbers recorded in the PR with
      conditions.
- [ ] Widen to `go build ./... && go vet ./...` last.

## Risks / open questions

- **Field semantics change**: anything external alerting on
  `startup_duration_ms` sees a drop. Acceptable — it's a correction —
  but it must be in the changelog, and the web UI ships in lockstep in
  the same repo.
- **`goStart` precision**: leaf-package var init is not guaranteed to be
  the absolute first Go instruction. Bound: total package init ≈ 45ms.
  Document as approximate; do not claim more than measured.
- **Fallback platforms** (`procstart_other.go` returns `time.Now()`):
  `pre_init_ms` ≈ 0. Fine — report what we measure; consider omitting
  the environment phase entirely when the anchor is a fallback so we
  never show a fabricated 0ms segment as if measured.
