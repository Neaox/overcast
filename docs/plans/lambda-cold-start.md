# Lambda cold-start & invoke-path latency — plan

> Status: investigation complete 2026-07-31. Landed so far (all 2026-07-31):
> Phase 1.3's first half (one-shot Docker stats + real instance memory/CPU in
> the UI, PR #403), Phase 1.1 (stored CodeHash — no per-acquire package
> rehash, PR #404), Phase 1.2 (deployment package split out of the function
> record — no per-invoke package decode, PR #405), Phase 1.4 + 1.3's second
> half (API Gateway route cache; REPORT memory off the response path,
> PR #406), and Phase 0 (cold-start phase timers, per-invoke TRACE timings,
> `scripts/bench-lambda.go`, and the before/after baseline below — this
> commit). Phase 1 is complete except 1.5 (CloudFront in-process origin
> dispatch): measured API GW gateway overhead is now sub-ms, so 1.5's ceiling
> is a few ms per request — low priority, keep gated on a dedicated
> measurement. Phase 2: 2.2 + 2.3 (artifact and image-presence caches,
> PR #408) and 2.1 (single-tar provisioning, this commit — cold p50
> ~355 → ~300 ms) landed 2026-07-31; 2.4 stays open only if the phase timers
> ever show it. Then Phase 3. Phase 4 not started.
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

## 4. Phase 0 — instrumentation & baseline — DONE (2026-07-31)

No behavior change; establishes the numbers every later item is judged by.
Landed: per-phase cold-start durations on the `lambda container started`
line (image_check / slot_wait / code_prep / create / copy / start /
await_ip + acquire_total), a per-invoke TRACE timing line (`lambda invoke
timings`: handler, log_wait, mem_wait, total), and `scripts/bench-lambda.go`
(paced; see its header for method). Sequenced after Phase 1 at the user's
direction — the baseline below therefore brackets Phase 1 (alpha.26 vs this
branch) rather than preceding it. The large-zip variant (item 3) and
recording INIT split per runtime remain available as follow-ups when Phase 2
needs them.

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

**What:** wall-clock latency of `POST /2015-03-31/functions/{name}/invocations`
(direct) and `GET /restapis/{id}/{stage}/_user_request_/bench` (API GW v1
AWS_PROXY), measured client-side by `scripts/bench-lambda.go`. A cold sample
= identity-changing env update (retires the warm set) → wait Active → settle
500 ms → one invoke; warm samples = sequential invokes on the warm container.
5 cold rounds × (1 cold + 4 warm + 4 API GW) per runtime, 2 s cooldown
between rounds, fully sequential.

**Environment (both runs identical):** Windows 11 Pro, Docker Desktop
(WSL2), repo on SSD; overcast dockerized (slim image,
`-v /var/run/docker.sock`, port 4590), hybrid store, hello-world inline-zip
functions at 128 MB, runtime images pre-cached; bench client in a
`golang:1.24` container via `host.docker.internal`. 2026-07-31.

**Before — `ghcr.io/neaox/overcast-slim:0.0.1-alpha.26` (pre-#402…#406):**

| Runtime    | cold p50 | cold p95 | cold max | warm p50  | warm p95  | apigw p50 | apigw p95 |
|------------|---------:|---------:|---------:|----------:|----------:|----------:|----------:|
| nodejs22.x | 1508 ms  | 2263 ms  | 3468 ms  | 2004.6 ms | 2005.2 ms | 2004.2 ms | 2005.2 ms |
| python3.13 | 2064 ms  | 2082 ms  | 2577 ms  | 2004.3 ms | 2005.2 ms | 2004.4 ms | 2005.2 ms |

Every warm and API GW invoke sat at ~2004 ms: the pre-one-shot Docker stats
call hit its 2 s timeout on every invocation in dockerized overcast — the
warm path was entirely gated on the stats stall (§2.2 item 3).

**After — this branch (Phase 1 complete: #403, #404, #405, #406):**

| Runtime    | cold p50 | cold p95 | cold max | warm p50 | warm p95 | apigw p50 | apigw p95 |
|------------|---------:|---------:|---------:|---------:|---------:|----------:|----------:|
| nodejs22.x | 359 ms   | 368 ms   | 383 ms   | 6.2 ms   | 11.3 ms  | 5.8 ms    | 7.1 ms    |
| python3.13 | 353 ms   | 511 ms   | 1339 ms  | 6.0 ms   | 19.7 ms  | 5.9 ms    | 50.6 ms   |

Warm p50 2004 ms → ~6 ms (~330×), API Gateway adds no measurable overhead
over direct invoke, cold p50 ~1.5–2.1 s → ~355 ms (~4–6×), and cold jitter
collapsed (nodejs p95 2263 → 368 ms). The remaining cold-start budget is the
Docker create/copy/start sequence plus INIT — the per-phase breakdown now
logged on every `lambda container started` line is the input for Phase 2.
(The python cold-max outlier, 1339 ms, was a single round on a busy daemon;
re-run before reading anything into it.)

**Phase 2.2 + 2.3 verification (2026-07-31, same protocol, 5 cold × 5 warm,
run immediately after a `docker build` so daemon-noisier than the run
above):** cold p50 457 ms (nodejs) / 365 ms (python), warm p50 ~6 ms —
parity with the Phase-1 baseline within noise, i.e. **no regression** on
hello-world zips, whose tar conversion is ~1 ms to begin with. The caches'
wins scale with package/layer size and daemon RTT count; quantifying them
needs the Phase 0 large-zip variant (still a follow-up) rather than this
workload.

---

## 5. Phase 1 — warm-path (and cold-path) hot-spot removal

Ordered by expected win. All are invoke-path subtractions with caches filled
off-path; none change observable behavior.

### 1.1 Stop rehashing the code zip per acquire — DONE (2026-07-31)
Landed: the function record stores `CodeHash` (hex SHA-256 of `CodeZip`),
maintained by a central `Function.setCode` that every code-mutation site goes
through (CreateFunction inline + S3-fetch, UpdateFunctionCode inline +
S3-fetch, source-editor save, s3_sync refresh). `functionCodeIdentity` and
`codeSha256` read the stored hash; records persisted before the field existed
fall back to hashing `CodeZip` with the same resulting value, so warm
containers survive the upgrade. Hot-reload functions keep their mtime-based
identity, bypassing the hash entirely. Pinned by `code_identity_test.go`:
setter invariant, legacy/setCode identity agreement, identity-trusts-hash
contract, and end-to-end CreateFunction / UpdateFunctionCode / s3-sync hash
assertions.

### 1.2 Stop decoding the code zip per invoke — DONE (2026-07-31)
Landed: the deployment package lives under its own store key
(`nsFunctionCode`, keyed region/name, base64). `putFunction` strips the zip
from the record (and writes the package key only when the in-memory record
actually carries bytes, so config-only updates never touch it);
`getFunction` — every invoke — decodes a small record. The bytes are
materialized by `lambdaStore.loadFunctionCode` only where needed: container
cold start (via `ContainerRuntime.SetCodeFetcher`, wired in service.go) and
the source viewer/editor handlers. `loadFunctionCode` resolves the region
from the function ARN so bare-context cold starts (provisioned-concurrency
replenish, ESM delivery) hit the right partition. Side win: the s3-sync
watcher's list-all-functions on every S3 PutObject no longer decodes any
packages. Legacy records that still embed `code_zip` keep decoding on read
and migrate to the split automatically on their first write — no data loss,
no redeploy. The decoded-Function cache sketched below was **skipped**: with
the zip out of the record, per-invoke unmarshal cost is negligible.
Pinned by `store_code_test.go`: record carries no bytes, package
round-trips, config-only puts leave the package untouched, delete removes
it, ARN-region resolution.

### 1.3 Take the Docker stats round trip off the invoke critical path — DONE (2026-07-31)
Landed in two halves (both 2026-07-31):
- `ContainerStatsOneShot` (`one-shot=true`) removed the daemon's ~1–2 s
  two-cycle wait from the stats call, and the instance tracker samples real
  memory/CPU for the UI on the snapshot path only (`refreshStats`, throttled).
- The remaining serial round trip moved off the response path: the sample is
  kicked off at invocation **submit** (overlapping handler execution), REPORT
  waits on it at most 50 ms (`reportMemoryMB`), and an unresolved sample
  reports the previous value while the goroutine folds its result in for the
  next invocation. `Max Memory Used` now reads the environment's **monotonic
  peak** across warm reuses — which is what AWS reports — rather than a
  post-completion point read, so this was a fidelity improvement as well as a
  latency one. Pinned by `report_memory_test.go` (monotonic peak, bounded
  wait, unreachable-daemon fallback) alongside the existing REPORT-shape
  tests.

### 1.4 API Gateway route cache — DONE (2026-07-31)
Landed: the execution path's per-request `Scan` + unmarshal of an API's
routing state is cached (`routeCache` in the apigateway store) — v1 resources
(methods and integrations are embedded in the resource records) and v2
routes, keyed by region-scoped API ID. Invalidation is centralized in the
only write funnels (`putResource`/`deleteResource`/`deleteAllResources`,
`putV2Route`/`deleteV2Route`/`deleteAllV2Routes`; verified no other
production writer touches those namespaces). Cached slices are shared across
requests and treated as read-only — the execution handlers already
copy-before-mutate for stage-variable substitution, and management handlers
keep using the uncached lists. Stage variables, v2 integrations, API records,
and authorizers still read live state per request (single cheap gets).
Deviation from the sketch: no precompiled matcher segments — the matcher
runs on the cached slice as-is; the Scan+unmarshal was the measured-shape
cost. Pinned by `route_cache_test.go`: end-to-end v1 and v2 execution
through every mutation type (repoint, add, delete, delete-all), each
asserting the very next request observes the change.

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

### 2.1 Single-tar container provisioning — DONE (2026-07-31)
Landed: one provisioning archive extracted at `/` — code entries prefixed
`var/task/` (`zipToTarPrefixed`, which also normalizes `./`-relative zip
names), layer tars prefixed `opt/` in layer order (later layers overwrite
earlier ones at extraction, matching AWS merge semantics and the previous
per-layer copies), the CA trust root, and the bootstrap — replacing up to
four sequential `CopyToContainer` round trips with one. Cached component
tars (2.2) are merged by entry re-copy (`appendTarEntries`); layer
resolution (`resolveLayerTars`) is now pure CPU with skip-warnings and
extension discovery unchanged. No directory the base image owns (`var/`,
`var/task/`, `opt/`, `etc/`) is ever emitted as a header, so extraction
cannot reset their modes — pinned by `provisioning_tar_test.go` along with
layer overwrite order. Verified live (bench-lambda, same protocol): cold
p50 ~355 ms → **~300 ms** (nodejs 300 / python 304), warm unchanged.

### 2.2 Pre-built artifacts — DONE (2026-07-31)
Landed (`tar_cache.go`, container_runtime.go):
- **zip→tar cache** keyed `code:{CodeHash}` in a byte-bounded LRU
  (`LAMBDA_TAR_CACHE_MB`, default 256, 0 disables; oversized artifacts are
  never cached). A hit also skips materializing the package from the store.
  Content-addressed keys mean code updates need no invalidation — old
  entries age out. *Deviation:* entries fill **on demand** at cold start
  only; the background pre-fill after deploy settle is deferred until
  Phase 3 introduces the settle debounce, and the debug-endpoint cache-size
  metric is deferred with it.
- **Bootstrap tar**: built once (`sync.OnceValues`).
- **CA bundle tar**: built once per process — certs are minted before the
  Lambda runtime exists and never rotate within a process (verified).
- **Layer tars**: cached per layer-version ARN with the discovered
  extension list, so a hit skips the fetch, the conversion, and the archive
  scan. Skipped/failed layers are never cached.
Pinned by `tar_cache_test.go` (LRU byte accounting, replacement, oversized
rejection, disabled-nil safety).

### 2.3 Image-presence cache — DONE (2026-07-31)
Landed: once `ensureImage` verifies an image+platform, later acquires skip
the per-acquire `ImageMatchesPlatform` daemon call (`imageVerified` map,
keyed by the same `imagePullKey` the pull-once map uses). A container create
failing with "No such image" (image removed behind our back) drops both the
verified entry **and the spent pull-once entry**, re-pulls, and retries the
create once — which also fixes a pre-existing bug where `docker rmi` of a
runtime image bricked that runtime until restart (the spent `sync.Once` made
`ensureImage` return nil without pulling). Matcher pinned in
`tar_cache_test.go`.
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
