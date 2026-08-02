# Compat baseline, CI enforcement & cross-suite uniformity

> Status: enforcement, CI surfacing, flake pipeline, and the framework audit landed.
> The burn-down is finished — **zero grandfathered failures**, and CI now asserts
> that absolutely. The dotnet/rust parity backfill is outstanding.
>
> The policy this plan implements is documented for contributors in
> [compat/AGENTS.md § Baseline & uniformity policy](../../compat/AGENTS.md#baseline--uniformity-policy).
> This file tracks the remaining work; delete items as they land.

## Why

Three things were undermining the compat suites:

1. **`compat/baseline.json` was empty** — `{"version":1,"entries":[]}`. The whole
   ratchet (`--compare-baseline`, blocking in CI) enforced nothing, so CI was
   green while 31 tests failed on `main`.
2. **CI had a Docker blindspot** — every suite job set
   `OVERCAST_COMPAT_SKIP_DOCKER=1`, so Lambda invocation and ESM delivery were
   never exercised. The emulator answered from its stub and nothing noticed. Two
   of the 31 failures (the CDK ESM assertions) were this biting.
3. **Uniformity was policy-less** — `registry.json` was documented as the single
   source of truth, but nothing enforced it, and the new-service checklist only
   asked for node-js-sdk + cli.

## Landed

- **Baseline hardened** ([cmd/compat/baseline.go](../../cmd/compat/baseline.go)):
  `compareBaseline` now flags a failing test that is *absent* from the baseline
  (it previously iterated baseline entries only, so a brand-new failure was
  invisible); `lintBaselineChange` rejects a new entry arriving as `fail` (it
  previously iterated old entries only, so failures could be grandfathered by
  adding them). `--annotate` renders both as `::error` workflow commands.
- **Baseline populated** — 3,361 entries seeded from the first Docker-enabled CI
  run (the configuration CI now uses): 2,566 `pass`, 766 `skip`, 1 `na`, and
  **29 `fail` grandfathered for burn-down** — 28 measured, plus the flaky
  `PublishDeliveredToSQS` recorded at its worst observed outcome.
- **Parity checker** ([cmd/compat/parity.go](../../cmd/compat/parity.go)) —
  `--check-parity` / `--update-parity-debt`. Classifies every (suite, registry
  test) pair from a real run: implemented, registry gap, environmental skip,
  cascade, or `na`. Gaps must match [compat/parity-debt.json](../../compat/parity-debt.json)
  exactly; the file only shrinks. Registry gained an optional `"suites"` scope
  (used by `cdk-lifecycle`), and CDK is exempt from unscoped groups —
  it deploys stacks rather than calling operations one at a time.
- **Sentinel normalised** — java-sdk emitted `not implemented` and cli
  `not implemented in cli suite`; both now emit the shared
  `not yet implemented in <suite> test suite` that parity classification keys on.
  The legacy wording is still accepted so older artifacts classify correctly.
- **Docker in CI** — `OVERCAST_COMPAT_SKIP_DOCKER` removed from the matrix, with
  a pre-pull of `public.ecr.aws/lambda/nodejs:20` so a cold pull cannot time out
  inside a function-Active wait.
- **GitHub surfacing** ([scripts/compat-report.py](../../scripts/compat-report.py))
  — one report, four surfaces: job summary (gate failures first, then the matrix,
  new vs known failures, parity debt), `::error` annotations, a `Compat Report`
  JUnit check run (regressions are failures, expected gaps are skips), and a
  sticky PR comment. Artifacts unchanged.
- **Baseline auto-promotion** — on push to `main`, improvements are published
  back by the workflow. Originally a direct push to `main`; #393's
  required-check enforcement rejected that on every run (issue #440, ~50
  improvements stranded until the hand-promotion in PR #439), so promotion is
  now a **PR-based flow**: the improvement is force-pushed to the coalescing
  `automation/baseline-promotion` branch and auto-merged through the same
  required checks (including the baseline-change lint) as a human edit. It
  needs GitHub App credentials (`COMPAT_PROMOTION_APP_ID` /
  `COMPAT_PROMOTION_APP_PRIVATE_KEY`) because `GITHUB_TOKEN`-created PRs
  start no workflows; until those secrets exist the step warns accurately and
  promotion is applied by hand (recipe in PR #439).

## What turning Docker on changed

Enabling the Docker-dependent tests in CI was a net win and, more importantly,
told the truth for the first time:

| | Before (stub) | After (real containers) |
| --- | --- | --- |
| Total failures | 31 | **28 measured** (29 recorded — see the flake below) |
| cdk | 2 | **0** — the ESM assertions pass; R2 is done |
| java-sdk | 3 | **1** — the function-Active failures were an artefact of the stub |
| cli | 1 | **3** — two genuine bugs the stub was hiding (R7) |

`cli/lambda-invoke/InvokeDryRun` and `cli/eventbridge-buses/ListEventBuses` were
passing against a stub that never ran a container. That is exactly the blindspot
this work existed to close: the suite was green on a code path CI never
exercised.

## Done — the 26 grandfathered failures are burned down

Cleared by #457–#462, one PR per root cause with a reproducing test first. The
last main run recorded **2,690 `pass`, 0 `fail`, 676 `skip`, 1 `na`**; every
remaining `skip` is a suite that has not implemented the registry group yet
(rust-sdk 338, dotnet-sdk 303, java-sdk 35), which the parity backfill below
covers.

Two things then closed the loop, because a zero that only lives in a file is not
a guarantee:

- **The baseline was promoted to zero `fail`.** Auto-promotion had computed the
  same change on every main run since #462 and refused to publish it — branch
  protection blocks direct pushes and the promotion App credentials are still
  missing (issue #440) — so the recorded fail set sat 26 entries behind reality.
  Promoted by hand from the run-30693624750 artifact, per the recipe in #439.
- **An absolute gate now sits beside the relative one.** `--max-failures 0`
  ([cmd/compat/baseline.go](../../cmd/compat/baseline.go) `failuresOverLimit`),
  asserted in the aggregate job. `--compare-baseline` asks whether anything got
  worse than the file records; this asks whether anything failed at all. While
  the file could record a `fail` those were different questions, and the gap was
  exactly the stale-baseline window above. Quarantined flaky tests stay exempt.

The root causes, for the record:

| # | Root cause | Fails | Notes |
| --- | --- | --- | --- |
| R1 | `rds-subnet-groups` — "At least one SubnetId is required", plus the Describe cascade | 7 | node, python×2, go×2, cli, java. Fails identically in five suites, which points at the emulator's EC2/RDS path, not the suites. Largest single cluster; reproduces locally |
| R4 | dotnet-sdk suite bugs | 10 | Null-refs (PurgeQueue, DeleteItem, PublishBatch), s3-copy bucket collision, IAM `Get*Policy` URL-decode assertions, BatchGetSecretValue, appsync ordering, s3-multipart AbortMultipartUpload |
| R5 | rust-sdk opaque `service error` reporting, then the real bugs | 9 | Fix `SdkError` rendering first — the errors are currently unreadable. Then: dynamodb conditional-check expectations ×2, lambda-crud CreateFunction, s3-copy, BatchGetSecretValue, ssm masked-value ×2, appsync ×2 |
| R6 | Suites hardcode `nodejs18.x`, which the emulator now rejects | (inside R4/R5 counts) | The 4 appsync fails (dotnet+rust `CreateFunction`/`TagResource`) and rust `lambda-crud/CreateFunction` trace to `Runtime::Nodejs18x` in rust setups and the dotnet equivalent — bump to nodejs20.x and several fails + setup-skips clear at once |

R2 (CDK ESM) is done — it resolved the moment CI stopped skipping Docker.
R3 (java function readiness) and R7 (cli InvokeDryRun + eventbridge) are done —
#442 fixed the readiness wait and the Pending-invoke status code, and a 3×
workflow_dispatch flake-detection run of cli confirmed it (issue #398 closed).
The incidence pattern generalises: **a test failing identically in many suites
is an emulator bug (R1); a test failing in exactly one suite is a suite bug
(R4/R5)** — the report's lone-suite classification should exploit this.

## Dashboard QOL — reviewed 2026-08-01, scoped, not yet started

The dashboard works and the fundamentals are right (registry-driven matrix,
distinct fail/unimplemented states, SSE with catch-up buffering, results
persisted across restarts). The review found stability and usability gaps, in
priority order:

1. **SSE drops are invisible and unrecovered.** `use-event-stream.ts` opens an
   `EventSource` with no `onerror`/`onopen` handling: the browser auto-
   reconnects, but events missed during the outage are never back-filled, so
   the matrix silently drifts stale — and nothing tells the user the
   connection died. Fix: on reconnect, re-fetch `/results` and re-seed (the
   catch-up buffer logic already exists for startup); add a connection pill.
   The main web UI just built exactly this pattern (#369/#382) — mirror it.
2. **Failed run triggers are silent.** `use-run.ts` returns `{ok:false}` on a
   409 (run already active) or network error; no component surfaces it. A
   click that does nothing is indistinguishable from a broken UI. Fix: inline
   feedback on the run controls.
3. **compat/ui is invisible to CI.** No typecheck, no tests, no build runs in
   any workflow (the Web UI job covers `web/` only; CI never embeds the
   dashboard because compat runs headless there). Type rot lands silently.
   Fix: add `tsc -b` + `npm run build` for compat/ui to the compat workflow's
   build job (~40s, cached), and stand up vitest — the SSE reducer and
   registry-join logic are pure functions begging for tests. `web/` has the
   harness conventions to copy.
4. **No preference persistence.** Status filters, suite selection and scroll
   state reset on every reload; during a burn-down session the fail-filter is
   re-applied by hand each visit. Fix: localStorage for filter/selection
   state.
5. **Registry×suite scoping in the matrix**: with `suites` scoping now in the
   registry (cdk-lifecycle), the matrix should render out-of-scope cells as
   structurally absent rather than "not run", so per-suite pass rates exclude
   cells a suite can never fill.

## Stabilising flaky tests (pipeline live; list currently empty)

Quarantine is containment. The plan to actually remove it:

1. **Detect systematically, not by luck.**
   [compat-flake-detection.yml](../../.github/workflows/compat-flake-detection.yml)
   runs every suite 3× nightly against unchanged `main` and fails on any test
   that answers inconsistently and is not already quarantined. Both flakes so
   far were found by accident; this is the fix for that.
2. **Chase the shared root cause first.** Every flake found so far was the same
   shape — a resource is created, and the next call cannot find it.

   **Resolved (#388, nine entries un-quarantined): the shared root cause was in
   the suites, not the emulator.** Suite groups run in 8 parallel slots, and
   three groups violated group isolation, so a sibling group's teardown (or
   test) deleted a live resource mid-run:

   | Was quarantined | Actual cause |
   | --- | --- |
   | `dotnet-sdk/sns-subscriptions/*` (6 entries) | `sns-topics`' teardown swept topics by prefix `{RunId}-sns`, which also matched the live `-sns-sub`/`-sns-pub` topics of sibling groups |
   | `cli/ses-identities/*` (2 entries) | all three SES groups shared ONE identity, and every group's teardown deleted it |
   | `cli/eventbridge-buses/DeleteEventBus` (+ R7's `ListEventBuses`) | all three EventBridge groups shared ONE bus; setups re-created it, `DeleteEventBus` and every teardown deleted it |

   The `internal/state` write-visibility suspicion was ruled out: the memory
   backend used by compat CI is strongly consistent (single RWMutex), and the
   emulator was correctly executing deletes the suites really sent. Fixed by
   per-group resources (exact-name teardowns); validated 3× cli+dotnet locally
   with `compat-flake-detect.py` fully consistent.

   **Fixed earlier: `lambda-crud/DeleteFunction` (#414, un-quarantined).** Not
   the suite pattern but the RDS stale-snapshot mechanism again, in lambda:
   the CreateFunction prewarm callback persisted the create-time snapshot
   after the image pull, resurrecting a function deleted mid-pull (also struck
   cli on PRs #427/#430). The startup Pending-reconciler and the S3 code-sync
   watcher had the same write; all three now merge into a fresh read
   (deterministic repros in `handler_functions_test.go`,
   `seed_reconcile_test.go`, `s3_sync_test.go`).

   **Resolved: `cdk/cdk-lifecycle/VerifyTopicSubscription` (#435) — the
   quarantine list is now empty.** No parallelism needed: the stack's SNS
   topic fed the same queue the Lambda event source mapping polls, so the
   ESM's once-per-second poller intermittently consumed the published message
   before the test's ReceiveMessage saw it (the recorded CI failure is the
   delivery assert, "published SNS message was not delivered to SQS queue").
   The race would flake identically against real AWS. The topic now feeds a
   dedicated queue with no competing consumer, and the delivery budget rose
   from 2.5s to 10s. Validated 3× locally via the compose path, which needed
   two environment fixes to run cdk at all (asset-bucket `/etc/hosts` entry +
   `OVERCAST_HOSTNAME=overcast`).

   **Not every flake is that pattern, though — check for stale-snapshot
   write-backs too.** `cli/rds-instances/StartDBInstance` failed the gate twice
   on 2026-07-30 (`InvalidDBInstanceState`, with the `DeleteDBInstance`
   cascade) and turned out to be RDS-local, not the shared visibility race: the
   background goroutine that starts the DB container read the instance record,
   spent seconds starting the container, then wrote the stale snapshot back —
   clobbering any Stop/Delete transition that landed meanwhile. Only the cli
   suite paces Stop→Start slowly enough (~1 s per aws-cli invocation) to
   overlap the container-start window, which is why the SDK suites never saw
   it. Fixed at the root (merge container fields into a fresh read; tear down
   the container when the instance was deleted mid-start) with a deterministic
   repro in `internal/services/rds/handler_docker_race_test.go`, so it was
   never quarantined. Any other service that persists state from a goroutine
   spanning a slow external operation deserves the same audit.
3. **Weight cascades in the burn-down.** Neither quarantined test is flaky in
   itself: both are dependants of a *stable* failure whose cascade is
   non-deterministic (`dependency failed` one run, `fail` the next). Any
   baseline failure with dependants is therefore a latent intermittent gate
   failure, which moves R7 and R1 up the queue.
4. **Empty the list.** Each fix deletes its entry in the same PR; the lint
   allows removals freely and blocks additions until a reviewer applies the
   `quarantine-approved` label to the PR (see AGENTS.md — added when the #414
   quarantine had no green path after admin merges were retired).

Out of scope here but worth naming: the Go suite had its own instability —
`TestHostClassifier_lowercaseHostStaysAllocationFree` failed spuriously under
`-race` on unrelated PRs (#427 most recently). Root cause was in the code, not
the test: `regionSegment` matched regions with a package `regexp`, whose
matcher state lives in a `sync.Pool` — under `-race` the pool deliberately
drops items at random, so Classify allocated on some windows. Fixed by
hand-rolling the region-shape match (`isAWSRegionShaped`, pinned to the regexp
by an oracle test); the assertion itself was right and is unchanged.

## Framework audit (2026-08-01) — findings and hygiene

A full end-to-end review of the framework — isolation, assertions, sync
methodology, runner, CI, and UI — before expanding the suites. State at review
time: **26 grandfathered failures, quarantine list empty, R2/R3/R7 closed.**

**Fixed during the audit:**

- **Eight assertions in node-js-sdk could never fail.** The idiom
  `list?.some((x) => x.Y, val)` passes `val` as `thisArg` — it tests "some
  entry has a truthy field", not membership — and several sat inside
  `assert.notStrictEqual(x, "message")`, which compares against the *message
  string*. Every post-delete verification for S3 buckets, SNS topics,
  EventBridge buses/rules, CloudWatch log groups, EC2 VPCs and Step Functions
  state machines asserted nothing. This is the worst defect class for a compat
  suite: a fidelity blind spot that stays green. All eight repaired; running
  node+python locally against the current emulator surfaced **zero new
  failures** — the emulator actually implements these paths correctly, so the
  fix is pure coverage.
- **Latent #388-pattern isolation bug in `sts-assume`** (node-js + python):
  `AssumeRole` targeted `role/{runId}-role` — the exact role `iam-roles`
  creates, exercises `DeleteRole` on, and tears down in a sibling parallel
  slot. Benign today only because the emulator does not validate the role's
  existence; the day it does, this becomes an intermittent cross-group
  failure. Both suites now use `-sts-assume-role`.
- **Parity was one-directional.** The checker verified registry→suites but a
  suite could invent tests the registry never heard of — cdk shipped
  `VerifyFilteredDdbEsm` and `VerifyFilteredEsmDelivery` outside the registry
  and nothing noticed. `computeParity` now flags any result absent from the
  registry (honouring `suites` scoping), and the two cdk tests are registered
  (cdk-lifecycle: 33 → 35).

**Verified clean:**

- No prefix-based teardowns remain in any suite (the #436 class).
- rust-sdk lambda naming is per-group (scanner false positive: setups/teardowns
  maps); python logs/kinesis share tails across *different services* (no
  collision); cli kinesis and dotnet ssm hits were file-top helpers and the
  group's own teardown section.
- Parallelism is uniform: 8 slots in every harness checked (node Semaphore,
  go/cli buffered channel, python ThreadPoolExecutor), overridable via
  `OVERCAST_COMPAT_PARALLEL_SLOTS`.
- UI: joins on `/registry`, renders Pass / Fail / **Not impl.** / Skip / N/A as
  distinct states (true failures never conflated with unimplemented), persists
  the last run across restarts, streams live over SSE.

**Known local-env quirk (documented in memory, worth a docs note):** the first
compat run after `compose up` fails all Lambda invokes ("function is in a
failed state") until `overcast` is restarted once — a socket-gid issue, not an
emulator or suite bug.

**Remaining tidy-up so new tests are easy to add** (mechanical, per-suite PRs):

1. **Name hygiene rule, enforced by review not tooling:** every resource name
   embeds its group token (`{runId}-<group-token>-…`). The audit's scanner
   (banner-section + name-tail analysis) found the violations above; making it
   a CI lint needs per-language parsing and is not worth the fragility yet —
   re-run the scan when a suite gains groups.
2. **Assertion-parity is unenforced.** The registry syncs *names*; nothing
   keeps eight implementations asserting the same thing (dotnet's IAM
   `Get*Policy` URL-decode failures are suites disagreeing about assertions).
   Cheap first step: the report already knows a test's cross-suite incidence —
   surface "fails in exactly one suite" as a suite-bug-candidate list in the
   job summary. A shared behaviour spec is not worth it at this scale.
3. **Go/py/java assert idiom sweep:** the TS `.some(thisArg)` class does not
   exist in Go (explicit ifs), but python `assert x, msg`-style truthiness and
   java assertion helpers deserve the same 30-minute audit next time either
   suite is touched.
4. **cli suite runtime** (4m16s in CI, the slowest matrix job): one process
   spawn per aws-cli call. Acceptable; revisit only if the matrix wall-time
   starts to bind.

## Outstanding — parity backfill to zero

558 registry tests of debt across 112 groups
([compat/parity-debt.json](../../compat/parity-debt.json)):

| Suite | Debt | Shape |
| --- | --- | --- |
| rust-sdk | 297 | Missing iam (35) plus most non-core services |
| dotnet-sdk | 261 | Missing most non-core services |

go-sdk, java-sdk, python-sdk and cli reached **zero debt** in #344.

Order: dotnet-sdk and rust-sdk one service per PR:
sts, logs, ses, kinesis, eventbridge, cloudformation, ec2, ecs, cognito, rds,
sfn, waf, shield, apigateway, elasticache, cloudfront (47), appsync (50); rust
additionally iam (35). Both harnesses are registry-driven `TestName → impl`
maps, so each PR is client wiring plus one function per test, with `na` where
the SDK genuinely lacks the API. Each PR deletes its debt entries — the checker
fails if it doesn't.

## Verification

- `go run ./cmd/compat --compare-baseline --results-file compat-results.json` —
  zero regressions; mutating a result must fail with the right annotation.
- `go run ./cmd/compat --check-parity --results-file compat-results.json` —
  passes; adding a registry test without implementing it must fail per suite.
- `python3 scripts/compat_report_test.py` — report rendering and JUnit mapping.
- Full local run: `go run ./cmd/compat` (managed emulator, Docker suites via the
  socket).
