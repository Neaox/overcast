# Compat baseline, CI enforcement & cross-suite uniformity

> Status: enforcement and CI surfacing landed. Burn-down and parity backfill outstanding.
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
- **Baseline auto-promotion** — on push to `main`, improvements are committed
  back by the workflow. The sole documented exception to "never push to `main`",
  granted to the workflow only.

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

## Outstanding — burn down the 29 grandfathered failures

One PR per root cause, reproducing test first. The baseline shrinks by
auto-promotion on merge; nothing to hand-edit.

| # | Root cause | Fails | Notes |
| --- | --- | --- | --- |
| R1 | `rds-subnet-groups` — "At least one SubnetId is required", plus the Describe cascade | 6 | node, python×2, go×2, cli. Determine whether the suites' subnet setup or the emulator's EC2/RDS path is wrong |
| R3 | java-sdk Lambda function readiness | 1 | Mostly resolved by real containers; one failure left |
| R4 | dotnet-sdk suite bugs | 11 | Null-refs (PurgeQueue, DeleteItem, PublishBatch), s3-copy bucket collision, IAM `Get*Policy` URL-decode assertions, BatchGetSecretValue, appsync ordering |
| R5 | rust-sdk opaque `service error` reporting, then the real bugs | 9 | Fix `SdkError` rendering first — the errors are currently unreadable; ssm masked-value expectation; BatchGetSecretValue |
| R6 | Suites hardcode `nodejs18.x`, which the emulator now rejects | ~17 cascading skips | appsync-functions and lambda-invoke setup across dotnet/rust — no longer a `fail`, but it masks whole groups |
| R7 | **New — exposed by running Lambda for real** | 2 | `cli/eventbridge-buses/ListEventBuses` is explained and fixed: all three cli EventBridge groups shared one bus, and sibling setups/teardowns deleted or re-created it mid-run (see the flake section). `cli/lambda-invoke/InvokeDryRun` (`InvalidParameterValueException` on Invoke) passes locally post-#434 — the lambda stale-snapshot fix plausibly covered it (an invoke racing a state transition); confirm via baseline auto-promotion on the next green main runs |

R2 (CDK ESM) is done — it resolved the moment CI stopped skipping Docker.

## Outstanding — stabilise the flaky tests

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

   **Still quarantined: `cdk/cdk-lifecycle/VerifyTopicSubscription` (#435).**
   It fits neither mechanism — cdk-lifecycle is a single group with no
   in-suite parallelism — so it keeps its own tracking issue.

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

## Outstanding — fix the quarantined flake

[compat/flaky.json](../../compat/flaky.json) holds one real flake, found by
running the same tree through CI twice:

- **`dotnet-sdk/sns-subscriptions/PublishDeliveredToSQS`** — intermittently
  reports `Topic does not exist: oc-<run>-dn-sns-sub`, on a topic
  `SubscribeSQS` used successfully moments earlier in the same group. So the
  topic is being *lost between calls*, not never created. Suspect a race in the
  emulator's SNS topic store, or setup ordering in the suite.
  `Unsubscribe` cascades from it.

Quarantine is a holding pen, not a resolution: the entry stays out of the gate
in both directions until someone fixes the bug and deletes it. It is worth
treating as high priority — an intermittent emulator bug is exactly the kind of
thing users hit and cannot reproduce.

Cascade skips must shrink with their root cause — check the skip clusters, not
just the fail count. End state: zero `fail` entries, plus a CI guard asserting
it stays that way (flips the grandfather clause off permanently).

## Outstanding — parity backfill to zero

602 registry tests of debt across 126 groups
([compat/parity-debt.json](../../compat/parity-debt.json)):

| Suite | Debt | Shape |
| --- | --- | --- |
| rust-sdk | 297 | Missing iam (35) plus most non-core services |
| dotnet-sdk | 261 | Missing most non-core services |
| go-sdk | 17 | elasticache (12) + ec2-vpc (5) |
| java-sdk | 17 | elasticache (12) + ec2-vpc (5) |
| python-sdk | 5 | ec2-vpc |
| cli | 5 | ec2-vpc |

Order: the small suites first (go-sdk, java-sdk, python-sdk, cli — mirror the
existing implementations), then dotnet-sdk and rust-sdk one service per PR:
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
