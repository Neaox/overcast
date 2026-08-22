# Compat baseline, CI enforcement & cross-suite uniformity

> Status: enforcement, CI surfacing, flake pipeline, and the framework audit landed.
> The burn-down is finished — **zero grandfathered failures**, and CI now asserts
> that absolutely. The dashboard QOL items closed 2026-08-23 (#1184, #1185), and
> so did the framework audit's three deferred hygiene gaps (name hygiene lint,
> cross-suite assertion parity report, non-TS assert-idiom sweep) via
> [#1186](https://github.com/Neaox/overcast/issues/1186) — Java's deeper
> per-method assert audit is the one piece still open, tracked as a follow-up.
> Outstanding as of 2026-08-23: the parity backfill (now across all six SDK
> suites — the registry has grown since the dotnet/rust-only snapshot).
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
  needs GitHub App credentials (`COMPAT_PROMOTION_CLIENT_ID` /
  `COMPAT_PROMOTION_PRIVATE_KEY`) because `GITHUB_TOKEN`-created PRs
  start no workflows; without them the step warns accurately and
  promotion is applied by hand (recipe in PR #439).

  **The App** — created and installed 2026-08-03, which closes the last thing
  blocking #440. It holds exactly two repository permissions: **Contents: read
  & write** (force-push `automation/baseline-promotion`; merge once checks
  pass) and **Pull requests: read & write** (open the PR, arm auto-merge). No
  Workflows permission — the promotion commit touches `compat/baseline.json`
  and nothing else. No Administration and no branch-protection bypass: the PR
  clears the same required checks as a human edit, and `main` requires no
  approving review, so auto-merge lands it unaided. Identified by **Client
  ID**, not App ID (`app-id` is deprecated as of the action's v3), stored in
  secrets beside the key like `RELEASE_APP_CLIENT_ID`.

  **Exercised.** The flow ran end to end on 2026-08-03: PR #491
  (`chore(compat): promote baseline improvements`) was opened from
  `automation/baseline-promotion` by the `overcast-compat` App and auto-merged
  through the required checks. The promotion pipeline is live.

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

## Dashboard QOL — reviewed 2026-08-01, closed 2026-08-23 (#1184, #1185)

The dashboard works and the fundamentals are right (registry-driven matrix,
distinct fail/unimplemented states, SSE with catch-up buffering, results
persisted across restarts). The 2026-08-01 review found five stability and
usability gaps; all five are now fixed, filed as issues #1184/#1185 and closed
by the same PR:

1. **SSE drops were invisible and unrecovered.** `use-event-stream.ts` opened
   an `EventSource` with no `onerror`/`onopen` handling. Fixed: `compat/ui/src/lib/reconnecting-event-source.ts`
   wraps the connection with exponential backoff (1s/2s/4s/5s-capped) and
   reports every state transition; `use-event-stream.ts` re-fetches `/results`
   and re-seeds on every reconnect (not just on mount), and `components/header.tsx`
   renders a live connection pill (`components/connection-pill.tsx`). Mirrors
   the main web UI's #369/#382 pattern, scaled down (no shared worker — the
   compat server already replays its full buffer to a fresh connection).
2. **Failed run triggers were silent.** `use-run.ts` returned `{ok:false}` on a
   409 or network error with nothing surfacing it. Fixed: `use-run.ts` now
   dispatches a `toast_error` action carrying the server's own response body
   (or a network-error message), rendered by `components/toast-stack.tsx`.
3. **compat/ui was invisible to CI.** No typecheck, build, or test job existed
   anywhere. Fixed: the `Compat UI` job in `.github/workflows/test.yml` runs
   `tsc -b`, `vitest run`, and the production build on every push/PR (gated by
   `ci-scope.py` the same way as every other job in that workflow — it is a
   prose-vs-code classifier, not a per-directory filter, so this isn't scoped
   to `compat/ui/**` specifically). `compat/ui` now has a full vitest + Testing
   Library setup (`vitest.config.ts`, `src/test/setup.ts`).
4. **No preference persistence.** Status filter and scroll position now
   persist via `lib/persisted-storage.ts` (a thin, failure-tolerant
   localStorage wrapper). "Suite selection" didn't exist as a control before
   this — added a click-to-hide toggle on each suite's header chip
   (`hiddenSuites` in `App.tsx`) so there is something concrete to persist.
5. **Registry×suite scoping in the matrix.** `lib/matrix-scope.ts` tells a real
   gap apart from a cell that is structurally out of scope for a `suites`-
   scoped registry group; `components/service-table.tsx` renders the latter
   with a distinct dimmed-dot treatment instead of the same play-button/`—`
   gap styling, and never offers to trigger a run for it. No group inside the
   SDK matrix uses `suites` scoping yet (`cdk-lifecycle` is the only one today,
   and `cdk` is filtered out of the SDK matrix entirely) — this is the general
   mechanism for the next one, covered by `lib/matrix-scope.test.ts`.

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

1. ~~**Name hygiene rule, enforced by review not tooling.**~~ **Closed by
   [#1186](https://github.com/Neaox/overcast/issues/1186), 2026-08-23.** A
   full per-language re-scan (every literal that constructs a name including
   the run id) turned out to be unusable on its own — dozens of unrelated
   services share a bare `compat-{runId}` scaffold harmlessly, and a naive
   "same literal in two files" check flagged 60+ false positives of exactly
   that shape. `scripts/compat-name-hygiene.py` narrows the comparison to
   literals tagged with a resource-kind keyword (`role`, `bucket`, `queue`,
   `topic`, `table`, `function`, `secret`, `alias`) next to the run id, so
   only a genuinely shared AWS namespace collision — the actual sts-assume-role
   bug shape — gets flagged; untagged scaffolding is never compared. Wired
   into the `Compat registry schema` CI job (`.github/workflows/test.yml`)
   and `make compat-registry-check`, with reviewed exceptions in
   `compat/suites/name-hygiene-allowlist.json` (empty today — nothing needed
   one). The sweep itself found one live instance of the un-narrowed bug: `go-sdk`'s
   `ses-templates` group named its SES template literally `{runId}` with no
   group token at all (`ses.go`); fixed to `{runId}-ses-tmpl` to match the
   sibling suites' `-tmpl` convention.
2. ~~**Assertion-parity is unenforced.**~~ **Closed by
   [#1186](https://github.com/Neaox/overcast/issues/1186), 2026-08-23,** via
   the cheap first step named above: `scripts/compat-report.py`'s
   `suite_bug_candidates()` now surfaces, in the job summary, every registry
   test that fails in exactly one suite while at least two other suites
   implementing the same test pass it — informational only, does not gate
   the run. A shared behaviour spec remains not worth it at this scale.
3. ~~**Go/py/java assert idiom sweep.**~~ **Done for Go, Python and Java in
   [#1186](https://github.com/Neaox/overcast/issues/1186), 2026-08-23 (Java's
   deeper per-method pass landed in the follow-up to #1295 the same day);
   dotnet/rust got the deeper pass too and came back clean.** Python: 36 bare `assert cond, msg` statements across 12
   files in `compat/suites/python-sdk/groups/` converted to
   `if not (cond): raise AssertionError(msg)` — a bare `assert` is stripped
   entirely under `python -O`/`PYTHONOPTIMIZE`, the same silently-vanishing-
   assertion risk as TS's `.some(thisArg)`, and the suite's own AGENTS.md
   already documented "raise AssertionError to fail" as the sanctioned idiom.
   Go: the `.some(thisArg)` idiom itself still doesn't exist, but a
   `_ = resp` sweep (the anti-pattern this file's own Assertion-contract
   section already named) found 5 live, registered tests in
   `compat/suites/go-sdk/internal/groups/` (`eventbridge.go` ×3, `iam.go`,
   `secretsmanager.go`, `ses.go`) that checked only `err` and threw the
   response away with zero assertion on state — fixed to assert real fields,
   matching the sibling suites' equivalent checks. Two more `_ = resp` hits
   (`eventbridge.go`'s `TestEventPattern`, `lambda.go`'s `InvokeWithError`)
   turned out to be dead code — never registered in an ImplMap, not in
   `registry.json`, never executed — left alone pending a follow-up
   decision (delete vs. wire up as real coverage). `cli` (the other Go
   suite) audited clean. Java: the bare `assert` keyword (disabled unless run
   with `-ea`) is not used anywhere — the suite consistently uses the
   `Assertions` helper — so that bug class doesn't apply; the deeper "is every
   response actually asserted" pass was then done by hand, per service.
   Method: every registered test in `groups/*.java` is a 4-space-indented
   `private void x(TestContext)` body closed by a bare `    }` (which is what
   makes the earlier brace-matching problem disappear), so each was split out,
   flagged if it calls a Describe/Get/List-shaped operation and either drops
   the response at statement level, never references the bound variable, or
   references it only outside an `Assertions.*` call — then every flagged body
   was read. 31 flagged → **18 real** (the other 13 were expect-NotFound
   probes or input gathering): AppSync ×9 (`ListApiKeys`, `ListDataSources`,
   `ListFunctions`, `ListResolvers`, `GetType`, `ListTypes`, `GetDomainName`,
   `ListDomainNames`, `GetApiCache` — all bare calls), Cognito
   `DescribeUserPoolClientTokenValidity` (described the client and asserted
   nothing about the 2/3/7 validity values it exists to check), EC2
   `DescribeAvailabilityZones`/`DescribeInstanceTypes` (the latter now asks
   for `t3.micro` explicitly — an unfiltered call is legitimately empty), IAM
   `UpdateUser` (the `GetUser` after the rename was discarded), RDS
   `DescribeDBEngineVersions`, Shield `DescribeSubscription`, and SQS
   `ChangeMessageVisibility`/`DeleteMessageBatch`, which `return`ed silently
   on an empty receive where every sibling suite fails. Two more shapes
   surfaced on the way and were fixed in the same pass: (a) ~18
   `assertNotNull(resp.<list>())` on SDK-v2 list fields, which the SDK
   auto-constructs and never returns null — an assertion that cannot fail —
   replaced with membership/non-empty checks against what the group created
   (AppSync `ListGraphqlApis`/`ListResolversByFunction`, Cognito
   `ListUserPools`/`ListUserPoolClients`/`ListUsers`, EC2 `DescribeImages`,
   IAM `ListRoles`/`ListPolicies`/`ListAttachedRolePolicies`, Logs
   `GetLogEvents`/`FilterLogEvents`, Kinesis `GetRecords`, KMS
   `ListResourceTags`, Lambda `ListLayers`, Shield `ListProtections`, SSM
   `DescribeParameters`/`ListTagsForResource`); the CloudFront `*List()` and
   STS `credentials()` hits are structs that can be null and were left alone.
   (b) IAM `AttachRolePolicy`/`ListAttachedRolePolicies`/`DetachRolePolicy`
   read `managedPolicyArn`, which only the *iam-policies* group's context ever
   sets — the runner hands each group its own `TestContext` — so all three
   no-op'd and passed; they now attach/list/detach the AWS-managed
   `AmazonS3ReadOnlyAccess` like node/go do. Verified by building the suite
   image (compile + registry unit tests) and running the 22 touched groups
   against a throwaway `ghcr.io/neaox/overcast:dev` container: 148/148 pass,
   every changed test confirmed executed. `dotnet-sdk`/`rust-sdk` (outside the
   issue's original scope) got the same deeper pass: dotnet's flags were all
   multi-line `Assertions.*` continuations, teardowns or expect-NotFound
   probes, and its `NotNull(<list>)` checks are meaningful on AWSSDK 4.0
   (collections are nullable there); rust binds all 131 read responses across
   174 registered tests with `let` and checks each. Both **audited clean, no
   changes**. What the pass did find is that the *siblings* share the Java
   discard shape verbatim — go-sdk drops ~51 read responses on the `_, err :=`
   line (a shape the `_ = resp` grep above never saw), python-sdk has ~32 bare
   read calls, node-js-sdk a handful plus `Array.isArray` on always-array
   fields — tracked in [#1321](https://github.com/Neaox/overcast/issues/1321).
4. **cli suite runtime** (4m16s in CI, the slowest matrix job): one process
   spawn per aws-cli call. Acceptable; revisit only if the matrix wall-time
   starts to bind.

## Outstanding — parity backfill to zero

As of 2026-08-21: **2,002 registry tests of debt across 317 groups**
([compat/parity-debt.json](../../compat/parity-debt.json)). The debt has grown
since the original snapshot (558 tests / 112 groups) because the registry
itself grew — a run of cli-first coverage PRs (#973–#978, #996, #1001, #1082:
AppConfig, AppConfigData, OpenSearch, Backup, ECR, MSK, EKS, rds-clusters, …)
added groups the SDK suites have not implemented yet:

| Suite | Debt (tests) | Groups |
| --- | --: | --: |
| rust-sdk | 567 | 97 |
| dotnet-sdk | 527 | 92 |
| go-sdk | 227 | 32 |
| java-sdk | 227 | 32 |
| node-js-sdk | 227 | 32 |
| python-sdk | 227 | 32 |

cli remains at **zero debt** (go/java/python/cli originally reached zero in
#344; the four SDK suites re-accrued debt as the registry expanded).

Order: dotnet-sdk and rust-sdk one service per PR, then the newer groups across
the remaining SDK suites. Both harnesses are registry-driven `TestName → impl`
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
