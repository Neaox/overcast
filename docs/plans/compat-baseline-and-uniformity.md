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
  **28 `fail` grandfathered for burn-down**.
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
| Total failures | 31 | **28** |
| cdk | 2 | **0** — the ESM assertions pass; R2 is done |
| java-sdk | 3 | **1** — the function-Active failures were an artefact of the stub |
| cli | 1 | **3** — two genuine bugs the stub was hiding (R7) |

`cli/lambda-invoke/InvokeDryRun` and `cli/eventbridge-buses/ListEventBuses` were
passing against a stub that never ran a container. That is exactly the blindspot
this work existed to close: the suite was green on a code path CI never
exercised.

## Outstanding — burn down the 28 grandfathered failures

One PR per root cause, reproducing test first. The baseline shrinks by
auto-promotion on merge; nothing to hand-edit.

| # | Root cause | Fails | Notes |
| --- | --- | --- | --- |
| R1 | `rds-subnet-groups` — "At least one SubnetId is required", plus the Describe cascade | 6 | node, python×2, go×2, cli. Determine whether the suites' subnet setup or the emulator's EC2/RDS path is wrong |
| R3 | java-sdk Lambda function readiness | 1 | Mostly resolved by real containers; one failure left |
| R4 | dotnet-sdk suite bugs | 10 | Null-refs (PurgeQueue, DeleteItem, PublishBatch), s3-copy bucket collision, IAM `Get*Policy` URL-decode assertions, BatchGetSecretValue, appsync ordering |
| R5 | rust-sdk opaque `service error` reporting, then the real bugs | 9 | Fix `SdkError` rendering first — the errors are currently unreadable; ssm masked-value expectation; BatchGetSecretValue |
| R6 | Suites hardcode `nodejs18.x`, which the emulator now rejects | ~17 cascading skips | appsync-functions and lambda-invoke setup across dotnet/rust — no longer a `fail`, but it masks whole groups |
| R7 | **New — exposed by running Lambda for real** | 2 | `cli/lambda-invoke/InvokeDryRun` → `InvalidParameterValueException` on Invoke; `cli/eventbridge-buses/ListEventBuses` → created bus not found. Both cascade (3 further skips). Investigate the emulator first: the CLI sends the same wire request the SDKs do, and the SDK suites pass |

R2 (CDK ESM) is done — it resolved the moment CI stopped skipping Docker.

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
