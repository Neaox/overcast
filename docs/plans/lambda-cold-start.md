# Lambda cold-start & invoke-path latency — plan

> Status: investigation complete 2026-07-31. Phase 1.3's first half (one-shot
> Docker stats + real instance memory/CPU in the UI) landed the same day on
> this branch; everything else not started.
> Goal: cut Lambda cold-start latency (especially via API Gateway / AppSync /
> function URLs / CloudFront) and shave per-invoke overhead on the warm path,
> **without** sacrificing fidelity — all observable behavior must keep matching
> AWS — and **without** granting functions more memory/CPU than AWS would.
> Guiding principle: move work to **deploy time or idle time**; never add work
> to the invoke hot path.

Related in-flight work: a separate agent is adding the missing `Init Duration`
field to cold-start REPORT lines (task "Emit Init Duration in cold-start
REPORT lines"). Phase 3 below depends on its semantics; coordinate before
starting Phase 3.

---

## 1. How AWS gets its cold-start times (reference model)

For the same code, memory, and CPU settings, AWS is faster than a Docker-based
emulator for reasons that are mostly *not* about giving the function more
resources:

1. **Sandbox creation is nearly free.** Firecracker microVMs are created on
   workers that are already booted and warm; the non-INIT overhead budget of an
   AWS cold start is on the order of 50–100 ms. Overcast's equivalent is a
   Docker container create + start, which is 300 ms–1 s on Docker Desktop.
2. **Code and images are lazily loaded from caches.** Zip packages are cached
   at the worker/AZ level; container images are chunked, deduplicated, and
   paged in on demand ("On-demand Container Loading in AWS Lambda", Brooker et
   al.) — execution begins before all bytes are local.
3. **Burst CPU during INIT** (~2 vCPU regardless of memory setting), throttled
   to the memory-proportional share afterwards. **Overcast already emulates
   this** (`initBurstCPUs` in
   [container_runtime.go](../../internal/services/lambda/container_runtime.go),
   throttled on the RIC's first `GET /next` via `ThrottleInitBurst`).
4. **Proactive initialization.** AWS documents that Lambda "may proactively
   initialize execution environments" ahead of traffic. Independent
   measurements (Stuyvenberg, 2023) attribute 50–85 % of inits for API
   Gateway-attached functions to proactive init. Those requests land on an
   already-initialized environment, report **no** `Init Duration`, and still
   see `AWS_LAMBDA_INITIALIZATION_TYPE=on-demand`. Much of AWS's perceived
   cold-start speed for API GW/AppSync is that the init happened *before* the
   request. This is documented AWS behavior, so emulating it is
   fidelity-preserving.
5. **SnapStart** (snapshot/restore for Java/Python/.NET) — opt-in, requires
   CRIU-style checkpointing that is not practical under Docker Desktop.
   **Explicitly out of scope.**

Implication: Overcast's INIT phase is already resource-faithful; the wins are
(a) deleting emulator-only Docker overhead from the cold path, (b) deleting
per-invoke overhead from the warm path, and (c) proactive init, which is the
same lever AWS itself pulls.

---

## 2. Where the time goes today (code-level inventory)

### 2.1 Cold path — `acquireContainer`
([container_runtime.go:314](../../internal/services/lambda/container_runtime.go))

Sequential steps, each a Docker daemon round trip unless noted:

| # | Step | Notes |
|---|------|-------|
| 1 | `ImageMatchesPlatform` | daemon RTT on **every** acquire, even when the image was verified seconds ago |
| 2 | cold-start semaphore | host protection; keep |
| 3 | `zipToTar(fn.CodeZip)` | pure CPU, re-done on every cold start of unchanged code |
| 4 | `CreateContainer` + `InspectContainer` | |
| 5 | `CopyToContainer` × up to 4 | code → `/var/task`, CA bundle → `/`, layers → `/opt`, bootstrap → `/` — each a separate tar upload + extraction; bootstrap tar and CA tar are **rebuilt from scratch each cold start** |
| 6 | `StartContainer` | the single biggest Docker cost (150–500 ms typical on Docker Desktop/WSL2) |
| 7 | VPC network connect (optional) | |
| 8 | `awaitContainerIP` | usually satisfied by the pre-start inspect |
| 9 | INIT inside container until first `GET /next` | the only part that corresponds to AWS `Init Duration`; already burst-CPU-faithful |

### 2.2 Warm path — costs paid on **every** invoke

These were found while auditing the trigger paths and are the "small things
that add up" — several are not small at all:

1. **Full function-record decode per invoke, including the code zip.**
   `Function.CodeZip []byte` is embedded in the persisted JSON record
   ([service.go:153](../../internal/services/lambda/service.go)), and
   `lambdaStore.getFunction`
   ([store.go:56](../../internal/services/lambda/store.go)) runs
   `json.Unmarshal` on the whole record on every invoke — which base64-decodes
   the entire deployment package. A 50 MB zip ⇒ ~67 MB of base64 decoded and
   allocated per request, warm or cold. Every trigger path hits this
   (`ServiceInvoker.Invoke`/`InvokeAsync` in
   [invoker.go](../../internal/services/lambda/invoker.go), the Invoke API
   handlers, ESM delivery).
2. **SHA-256 over the whole zip per acquire.** `InstancePool.takeWarm` calls
   `functionInstanceIdentity(fn)`
   ([runtime_pool.go:229](../../internal/services/lambda/runtime_pool.go)),
   whose `functionCodeIdentity`
   ([hot_reload.go:73](../../internal/services/lambda/hot_reload.go)) ends in
   `codeHashOf(fn.CodeZip)` — hashing the full package on **every**
   invocation, warm included. The record already carries a `CodeSha256`
   field maintained by the code-mutation paths.
3. **Blocking Docker stats call inside every invoke.** The REPORT line's
   `Max Memory Used` comes from `ci.currentMemoryMB()`
   ([container_runtime.go:1197](../../internal/services/lambda/container_runtime.go)),
   a Docker stats round trip executed **before `Invoke` returns**, because
   the REPORT line must be in the `X-Amz-Log-Result` tail snapshot.
   *Partially fixed 2026-07-31 (with the instance-stats work on this
   branch):* the call now uses `one-shot=true`, removing the daemon's
   two-cycle wait (~1–2 s, and the cause of `Max Memory Used: 0 MB` on
   Docker-in-Docker hosts where it overran the 2 s timeout). What remains on
   the critical path is a single fast stats round trip (~5–30 ms typical) —
   see Phase 1.3.
4. **API Gateway route resolution scans the store per request.**
   `ExecuteRestAPI` / `ExecuteV2API`
   ([handler_execution.go:49,303](../../internal/services/apigateway/handler_execution.go))
   call `listResources` / `listV2Routes`
   ([store.go:531,813](../../internal/services/apigateway/store.go)) on every
   request: a full `Scan` plus `json.Unmarshal` of **every** resource/route of
   the API (resources embed their methods and integrations), plus separate
   gets for API, stage, and (v2) integration. O(routes) JSON decode per
   request.
5. **CloudFront local origins dispatch over loopback TCP.** When an origin is
   served by the emulator itself (API GW invoke host, function URL, S3,
   AppSync), `buildOriginRequest`
   ([handler_proxy.go:376](../../internal/services/cloudfront/handler_proxy.go))
   sends the request to `http://127.0.0.1:{port}` — a real TCP connection and
   a second full pass through the router middleware per request.
6. Minor / verify-only: `EnsureLogStream` is called before every batch write
   in `writeEventsWithRetry`
   ([container_runtime.go:1369](../../internal/services/lambda/container_runtime.go))
   and per invoke in the invoker — served from the CWL store's in-memory
   cache, believed O(µs); confirm with the Phase 0 instrumentation and leave
   alone if so.

AppSync Lambda data sources and function URLs share `ServiceInvoker` /
`invokeSync`, so items 1–3 cover them; AppSync's VTL/JS evaluation is out of
scope here.

---

## 3. Ground rules for all phases

- **Fidelity envelope.** Observable behavior = REPORT/START/END lines, tail
  (`X-Amz-Log-Result`), env vars (`AWS_LAMBDA_INITIALIZATION_TYPE`, …),
  init-code execution timing semantics, throttle/queue behavior, CloudWatch
  contents. Any change must be invisible at that surface, or match a
  *documented* AWS behavior (proactive init).
- **No resource inflation.** CPU/memory stay exactly at the AWS-modelled
  values (`cpuAllocation`, `initBurstCPUs`, hard memory cap with swap off).
- **Hot path only gets cheaper.** New caches are filled at write/deploy/idle
  time or on first miss; the steady-state invoke path must do strictly less
  work than today. Anything speculative (pre-building tars, proactive init)
  runs in background goroutines that (a) are debounced behind config churn,
  (b) acquire the same admission budgets as real work but **non-blockingly**
  (try-acquire; skip/retry later if contended), so idle work can never queue
  ahead of a real invocation, and (c) are cancelled by function
  update/delete and pool Stop.
- **Regression discipline.** Phase 0 lands first; every subsequent item ships
  with before/after numbers from the same harness (docs/dev/performance.md
  rules: metric, method, environment). Warm-path p50 must not regress.
- **Idle-work definition.** "Idle" here is event-driven, not a scanner: a
  function becomes eligible for background work N seconds (default 10 s,
  clock-injected for tests) after its **last identity-changing write**
  (`CreateFunction`, `UpdateFunctionCode`, `UpdateFunctionConfiguration`,
  s3-sync refresh), with the timer reset on every new write. This matters
  because CDK deploys issue Create + several UpdateFunctionConfiguration calls
  back-to-back; un-debounced background work would churn containers/tars once
  per call. `InstancePool.InvalidateFunction` and the handler's `prewarmer`
  hook are the natural signal points.

---

## 4. Phase 0 — instrumentation & baseline (do first)

No behavior change; establishes the numbers every later item is judged by.

1. **Phase timers in `acquireContainer`.** Wrap each step (image check, tar
   build, create, copies, start, VPC, await-IP, await-ready) and log a single
   debug line with per-phase durations, e.g.
   `lambda cold start: fn=X total=1.24s image_check=8ms tar=112ms create=95ms copy=210ms start=420ms init=390ms`.
   The `progress` callback already delineates the steps; timing wraps it
   cheaply. INIT duration (container start → first `GET /next`) may already be
   measured by the parallel Init Duration task — reuse, don't duplicate.
2. **Warm-invoke breakdown** at trace level: getFunction decode, identity
   hash, acquire, submit→result, stats query, tail/drain waits.
3. **Bench harness** `scripts/bench-lambda.(go|sh)` (shape of
   `scripts/bench-startup.go`): deploys a hello-world zip for nodejs22.x and
   python3.13, then measures (a) cold p50/p95 (delete warm set between runs
   via function-config touch or container removal), (b) warm p50/p95, (c) the
   same through an API GW v2 route, N iterations each. Also one large-zip
   (≥30 MB) variant to expose items 2.2.1/2.2.2. Output a table suitable for
   pasting into PRs and this doc.
4. Record the baseline table in this doc under "Measured evidence" (below,
   currently empty).

### Measured evidence

_(fill in when Phase 0 lands; per docs/dev/performance.md include metric,
method, environment)_

---

## 5. Phase 1 — warm-path (and cold-path) hot-spot removal

Ordered by expected win. All are invoke-path subtractions with caches filled
off-path; none change observable behavior.

### 1.1 Stop rehashing the code zip per acquire
Use the stored `CodeSha256` in `functionCodeIdentity` when non-empty, falling
back to `codeHashOf(fn.CodeZip)` (and back-filling) only when absent (old
persisted records). Audit every code-mutation path to guarantee the invariant
"CodeZip changes ⇒ CodeSha256 changes": CreateFunction, UpdateFunctionCode,
s3_sync refresh ([s3_sync.go](../../internal/services/lambda/s3_sync.go)),
seed/reconcile. Hot-reload functions already bypass the zip hash (mtime-based
identity). Add a unit test asserting the invariant per mutation path, and one
asserting stale-warm-container retirement still triggers on code change (the
identity must still move).
*Risk:* a mutation path that updates the zip without the hash would leave
stale warm containers serving old code — the invariant tests are the guard.

### 1.2 Stop decoding the code zip per invoke
Two coupled changes:
- **Split `CodeZip` out of the per-invoke record** into its own store key
  (`nsFunctionCode`, keyed region/name), written by the same mutation paths.
  `getFunction` then decodes a small record; the zip is fetched by a new
  `getFunctionCode` **only where actually needed**: cold start
  (`acquireContainer`), GetFunction API (code URL/export paths), publish
  version. Backward compatibility: on read, if the legacy record still embeds
  `code_zip`, use it and migrate lazily on next write (malformed-persisted-
  state rule: never fail the read).
- **In-memory decoded-Function cache** keyed (region, name), invalidated in
  `putFunction`/`deleteFunction` (all writes funnel through them). Single
  process, so no cross-writer staleness. Callers currently receive a fresh
  `*Function` each call and some mutate it (state transitions build copies —
  verify); the cache must hand out either immutable snapshots or defensive
  copies of mutable maps (`Environment`, `Tags`). Given store writes are
  in-memory (hybrid), the *decode* is the cost — caching the decoded struct
  and cloning maps on read is still ~100× cheaper than re-unmarshal for big
  records, and after the CodeZip split the record is small enough that this
  cache is optional; measure, and skip if the split alone gets the win.
*Risk:* persistence-schema change — needs a fallback-read test with a legacy
record fixture; ESM/seed paths that list functions must tolerate zip-less
records.

### 1.3 Take the remaining Docker stats round trip off the invoke critical path
*Status: the expensive half landed 2026-07-31* — `ContainerStatsOneShot`
(`one-shot=true`) removed the daemon's ~1–2 s two-cycle wait from
`currentMemoryMB`, and the instance tracker now samples real memory/CPU for
the UI on the snapshot path (`refreshStats`, throttled, never on the invoke
path). Remaining work: one fast stats round trip (~5–30 ms typical) still
runs serially before `Invoke` returns.
`Max Memory Used` must still be real (fidelity), but the query need not run
*after* completion, serially:
- Kick off the stats query **concurrently** when the invocation is submitted
  (or at ~80 % of timeout for long invokes), so the value is usually resolved
  by the time the result arrives; if not yet resolved, wait with a small cap
  (e.g. 50 ms) then fall back to the container's last-known value (cached per
  instance), else 0 as today.
- REPORT line content and its presence in the tail snapshot are unchanged.
*Risk:* memory value may occasionally reflect a sample taken slightly before
peak; today's value is a point-read *after* completion which is also not the
cgroup peak (the code comments already accept best-effort here). Keep the
per-instance last-known cache updated on every resolved query so warm invokes
converge. Verify REPORT ordering tests still pass.

### 1.4 API Gateway compiled route table
Cache a compiled per-API routing structure (resources+methods+integrations
for v1; routes+integrations for v2, plus the matcher's precomputed segments)
keyed (region, apiID), built on first execution request, invalidated on any
write to that API's resources/methods/integrations/routes/stages/deployments
(the store methods are the funnel — add an invalidation hook where they
write). Stage variables and authorizer checks keep reading live state (cheap
single gets; correctness over speed there).
*Risk:* a write path that forgets to invalidate serves a stale route until
process restart. Mitigate by (a) centralizing invalidation in the store write
helpers, not the handlers, and (b) an execution test per mutation type
(create/update/delete resource, method, integration, route) asserting the
next request sees the change. The existing cross-region fallback continues to
work — cache per resolved region.

### 1.5 CloudFront local-origin in-process dispatch
For origins classified as served-locally
([handler_proxy.go:380](../../internal/services/cloudfront/handler_proxy.go)),
replace the loopback TCP hop with an in-process `http.RoundTripper` that
invokes the router's `Handler.ServeHTTP` against a cloned request. Same
request/response semantics (same URL, Host header, header forwarding), minus
TCP connect + kernel round trips + duplicate middleware entry cost (access
logging middleware must still run — it's part of the second pass today;
verify which middlewares are semantically load-bearing for origin requests:
host classification, region binding).
*Risk:* subtle divergence between "real HTTP request" and in-process request
(e.g. `RemoteAddr`, TLS state, `Content-Length` handling on retried
failover). Implement behind a flag defaulting on, with the TCP path kept as
fallback for one release; port the origin-failover tests to run against both.
This one is optional polish — measure first (likely ~1–3 ms/request); do it
last within Phase 1 or drop if the win doesn't justify the risk.

---

## 6. Phase 2 — cold-path Docker overhead

### 2.1 Single-tar container provisioning
Merge the up-to-4 `CopyToContainer` calls into **one** tar extracted at `/`:
entries prefixed `var/task/…` (code), `opt/…` (layers),
`var/overcast/bootstrap`, plus the CA bundle path. Extend
`zipToTarFiltered` with a path-prefix option.
**Do not emit parent-directory headers** (`var/`, `opt/`, `etc/`) — a dir
header would chmod the image's existing directories; only emit leaf dirs the
image doesn't own (`var/task/` subtree, `var/overcast/`). Preserve the layer
skip-warnings and extension discovery exactly (they operate on the zip, not
the tar).
*Risk:* permission/ownership drift inside the container ⇒ run the full
integration suite (`tests/integration/lambda`) incl. layers + extensions +
TLS-on (CA injection) before/after; add a test asserting `/var` and `/opt`
modes are untouched.

### 2.2 Pre-built artifacts, built at deploy time / idle
- **zip→tar cache** keyed by `CodeSha256` (+ prefix scheme version): LRU
  bounded by bytes (default e.g. 256 MB, env-tunable), entries built (a) in
  the background after the settle debounce (§3) following
  CreateFunction/UpdateFunctionCode — piggyback on the existing `prewarmer`
  hook in [handler_functions.go:547](../../internal/services/lambda/handler_functions.go) —
  and (b) on demand at cold start on miss (cache-fill then, too). Eviction
  never breaks anything; a miss just costs today's conversion.
- **Bootstrap tar**: static bytes — build once (`sync.OnceValue`).
- **CA bundle tar**: cache per cert generation; invalidate if the mapper ever
  rotates certs (today it doesn't at runtime — verify, then cache forever).
- **Layer tars**: cache per layer-version ARN (immutable by definition) with
  the same LRU.
*Risk:* memory growth — the LRU bound plus metrics (cache size in the debug
endpoint) covers it. Idle builds follow the try-acquire rule from §3 so they
never contend with real cold starts for CPU at deploy time.

### 2.3 Image-presence cache
After `ensureImage` verifies image+platform once, record it and skip the
`ImageMatchesPlatform` daemon call on subsequent acquires. Invalidate on:
pull failure, and on `CreateContainer` returning "No such image" (user ran
`docker rmi` mid-session) — in that error path, drop the cache entry, re-run
`ensureImage`, and retry the create once before failing.
*Risk:* only the `docker rmi` race, and the retry closes it.

### 2.4 Micro-cleanups (do only if Phase 0 shows them)
- The duplicate `RegisterContainerConfig` calls per cold start (pre-start,
  post-layer-discovery, post-IP) are lock-cheap; leave unless measured.
- `awaitContainerIP` backoff already usually short-circuits via the pre-start
  inspect; leave.

---

## 7. Phase 3 — proactive initialization (the AWS-matching lever)

Emulate AWS's documented proactive init so the *first* request through API
GW/AppSync/function URL usually lands on an already-initialized environment.

**Trigger model.** A function becomes a proactive-init candidate when **both**:
1. It has settled (§3 debounce — critical for CDK deploy churn), and
2. There is evidence it will be invoked: it is referenced by an API GW
   integration, function URL, AppSync data source, ESM, or CloudFront-routed
   path, **or** it has been invoked at least once this process lifetime.
   (AWS keys proactive init off traffic patterns; "wired to a trigger" is our
   equivalent. A helper that answers "is this ARN referenced anywhere" needs
   read-only lookups into apigateway/appsync/lambda-URL/ESM state — keep it
   event-driven: those services already know when an integration is created;
   emit/observe a bus event rather than polling.)

**Mechanics.** On candidacy, a background goroutine (owned by the pool,
tracked by `warmWG`, cancelled on Stop/update/delete):
- try-acquires the cold-start semaphore and admission budgets
  (non-blocking; reschedules after a delay if contended — never queues ahead
  of real work);
- creates **one** environment via the normal `Acquire` path with
  `initType = on-demand` (NOT provisioned — that's the fidelity-critical
  bit), then Releases it into the warm set exactly like
  `replenishProvisioned` does (borrow a checkedOut slot, `InstanceWarmed`
  observer with `provisioned=false` — check the tracker renders that state
  sensibly in the UI);
- respects the memory budget: skip entirely while over the high-water mark.

**Fidelity checklist.**
- `AWS_LAMBDA_INITIALIZATION_TYPE=on-demand` ✔ (matches AWS proactive init).
- Init code runs at environment creation, before first invoke ✔ (matches).
- First invoke on a proactively-initialized env must **omit** `Init
  Duration` in its REPORT — coordinate with the in-flight Init Duration task:
  its "init was triggered by this invoke" condition must be false for
  pool-created environments. Agree on the mechanism (e.g. the instance
  records whether an invoke was already waiting when INIT started).
- Idle sweep applies normally (15 min TTL) ✔ — AWS also reclaims these.

**Config.** `LAMBDA_PROACTIVE_INIT` (bool). Ship default **off** for one
release (observe churn/resource reports), then default **on** with
`LAMBDA_PROACTIVE_INIT=false` as the escape hatch. Document in
docs/services/lambda.md's env-var table either way.

**Regression risks (the ones to actively test):**
- *Deploy churn:* a CDK deploy updating a function 4× must produce **zero**
  proactive containers until the debounce elapses after the last write —
  test with the injected clock.
- *Update while proactive env exists:* identity change → `InvalidateFunction`
  retires it; must not trigger an immediate rebuild loop (rebuild only after
  re-settle).
- *Provisioned concurrency interplay:* a function with PC must not get an
  extra proactive env (PC already covers it); proactive envs must never be
  counted toward `provisionedInstances`.
- *Resource ceiling:* with N functions all wired to an API, proactive init
  must respect `MaxInstances`/memory budget and simply stop, not queue.
- *Never-invoked functions:* stay at zero containers unless wired to a
  trigger.

---

## 8. Phase 4 (deferred) — pre-created, not-started containers

Pre-pay `CreateContainer` + copies at idle (container in `created` state,
env/code baked), `StartContainer` only at invoke so INIT still happens on the
request and a future `Init Duration` stays truthful for genuinely-cold
invokes. This is the fallback lever if, after Phases 1–3, genuinely-cold
starts (proactive init disabled, or first-ever invoke) are still judged too
slow.

Deferred because: proactive init covers the common case with less machinery;
this adds a third container lifecycle state the pool/GC/tracker must model
(created-not-started containers leak invisibly — they don't run, don't log,
and `docker ps` doesn't show them without `-a`), identity invalidation must
destroy them, and the win is bounded (create+copy is the smaller half of the
Docker overhead vs `StartContainer`). If picked up: store them keyed by
identity next to the warm set, GC via the existing sweeper with their own
TTL, and count their (zero-until-started) memory correctly in admission.

---

## 9. Explicitly rejected

- **CRIU / Docker checkpoint (SnapStart emulation):** not available on
  Docker Desktop (Windows/macOS); niche payoff.
- **Keeping a pool of generic started runtime containers and injecting code
  at acquire:** env vars are baked at container creation; faking them at the
  process level breaks observable env fidelity (`/proc/1/environ`, SDK
  behavior). Rejected on fidelity.
- **Artificially delaying warm starts to "simulate" AWS cold-start latency:**
  docs/services/lambda.md already states cold-start latency simulation is not
  implemented; goal is faithful behavior at the lowest achievable latency,
  not latency theater.

---

## 10. Sequencing & acceptance

```
Phase 0 (instrumentation + baseline)
  └─► Phase 1  (1.1 → 1.2 → 1.3 → 1.4, 1.5 optional-by-measurement)
        └─► Phase 2  (2.2 & 2.3 independent; 2.1 after 2.2's tar cache
                      so the merged tar is what gets cached)
              └─► Phase 3  (after Init Duration task merges)
                    └─► Phase 4  (only if still needed)
```

Each item: failing-test-first where behavior is pinnable (identity
invariants, route-cache invalidation, REPORT format), bench numbers in the PR
(same harness, same machine, conditions documented per
docs/dev/performance.md), and this plan updated in the same commit as the
work (plan-docs-per-commit rule).

Acceptance targets (validate against Phase 0 baseline; revise there):
- Warm invoke p50 through API GW: measurably down, never up (>2 % warm-path
  regression on the harness blocks the PR).
- Cold invoke p50 (hello-world nodejs22.x, image cached, proactive init
  off): Docker-overhead portion (total minus INIT) down ≥ 40 % after
  Phases 1–2.
- With proactive init on: first request after deploy+settle through API GW
  hits a warm environment (0 cold starts in the harness's post-settle run).
- Full lambda + apigateway + cloudfront integration suites green; compat
  suite REPORT/env-var goldens unchanged.
