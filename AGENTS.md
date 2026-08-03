# AGENTS.md

> **For AI agents** (Claude, Copilot, Cursor, etc.) and agent-assisted workflows.
>
> This file is the canonical agent instructions file. Other filenames are symlinks to it
> so that different agents pick it up automatically (e.g. `CLAUDE.md` → `AGENTS.md`,
> `.github/copilot-instructions.md` → `../AGENTS.md`).
> If your agent expects a different filename, create a local symlink rather than copying:
> `ln -s AGENTS.md GITHUB.md` / `ln -s AGENTS.md COPILOT.md` / etc.
>
> **Read [CONTRIBUTING.md](./CONTRIBUTING.md) first** — it has coding standards,
> core principles, error handling, logging, performance, design patterns, service package
> structure, the endpoint/service checklists, and web UI standards.
> Everything there applies to you too — this file adds agent-specific guardrails only.
>
> For test conventions, see [tests/AGENTS.md](./tests/AGENTS.md).
> For smoke testing by hand — and for why the AWS CLI alone cannot verify an endpoint change —
> see [docs/dev/manual-testing.md](./docs/dev/manual-testing.md).
> For current implementation status and what to build next, see [STATUS.md](./STATUS.md).

## Repo-local skills

This repo includes skills under `.agents/skills`. The project `opencode.json`
registers that directory explicitly, and `.claude/skills` is a symlink to it, so
opencode and Claude Code both discover them without prompting.

- `aws-compatibility-review`: Use for AWS fidelity audits, compatibility tests, wire-format drift, and service behaviour parity.
- `bug-fix`: Use for diagnosing and fixing bugs with reproducing tests and full verification.
- `code-review`: Use for PR/code reviews, AWS parity checks, regression risk, performance, leaks, DRY/SOLID, and maintainability.
- `commit`: Use for clean commit creation, staging review, and commit-message hygiene.
- `git-worktrees`: Use for parallel multi-agent work that needs isolated worktrees.
- `github-issue-lifecycle`: Use for creating, triaging, updating, linking, and closing GitHub issues.
- `new-feature`: Use for adding AWS endpoints, services, CloudFormation resources, or other product features.
- `pull-request`: Use for preparing PRs, PR descriptions, commit hygiene, screenshots for visual changes, and CHANGELOG decisions.
- `stacked-prs`: Use when a PR depends on another PR that has not merged yet — building, linking, syncing and landing a chain of dependent PRs.

---

## What belongs here vs elsewhere

- **AGENTS.md (this file):** AI-agent-specific workflow rules and guardrails — things that only matter because an agent is executing work autonomously (e.g. "never leave the workspace broken", "run `go vet` before finishing"). If a human contributor wouldn't need to read it, it belongs here.
- **[CONTRIBUTING.md](./CONTRIBUTING.md):** Everything relevant to all contributors, human or AI — coding standards, architecture decisions, checklists, design patterns, web UI conventions. If a human opening a PR would benefit from reading it, it belongs there.
- **[README.md](./README.md):** Everything relevant to users of Overcast — installation, configuration, supported services, quickstart. Not for contributors.

---

## Non-goals — decision guide for agents

Before implementing anything, check these constraints. If a request conflicts, push back.

- **Not a staging environment.** No 100% API parity. Do not base production go/no-go decisions on Overcast tests.
- **Not a security boundary.** Credentials accepted but not validated. Never expose on a public network.
- **Not a performance testing tool.** No latency emulation, no request-rate limits, no per-service quotas. Lambda concurrency is the one place Overcast does refuse work, in two forms:
  - **Reserved concurrency** — set explicitly per function. Exceeding it throttles immediately with AWS's 429 `TooManyRequestsException` (`Reason: ReservedFunctionConcurrentInvocationLimitExceeded`), because that is behaviour applications are written against: retry policies, DLQs, ESM back-off, and the "set it to 0 to disable a function" idiom.
  - **Instance limits** (`LAMBDA_MAX_INSTANCES`, `LAMBDA_MAX_INSTANCES_PER_FUNCTION`) — host protection, not quota emulation. An invocation that cannot get a container first reclaims an idle one, then queues; **if it is still queued when the function's timeout expires it is throttled** with `Reason: ConcurrentInvocationLimitExceeded`, the same reason AWS gives when the account pool is exhausted. Raise the limits if you are hitting this rather than treating it as an AWS behaviour.

  Asynchronous invocations are never throttled back to the caller: they were already answered 202, so a throttle is retried internally, as on AWS. Do not add account-wide concurrency pools or request-per-second limits.
- **CloudFormation/IAM are partial.** Both services are implemented at a partial level. CloudFormation supports `CreateStack`/`DeleteStack`/`DescribeStacks`/`ListStacks` and provisions ~50 resource types (EC2/VPC, API Gateway, ECS, IAM, EventBridge, KMS, Lambda, S3, SQS, SNS, DynamoDB, Logs, SSM, SecretsManager, Step Functions) via internal dispatch to the emulated services. IAM supports roles, policies, users, groups, and instance profiles. `cdk deploy` works for stacks using supported resource types. Coverage is not exhaustive — continue ensuring all service implementations remain compatible with CloudFormation (standard ARN formats, required response fields, etc.).
- **Not a production dependency, ever.** Local dev and CI only. No durability guarantees, no security model.
- **Not a perfect replica.** We emulate the most-used 20% with high fidelity. Edge cases may differ.

---

## Repository layout

```
cmd/overcast/main.go         <- unified CLI: `overcast serve` (daemon) + bridge/status/trust subcommands
internal/
  bff/                       <- Go BFF: serves embedded SPA + /api/* proxy routes
  config/                    <- typed env-var config
  hostbridge/                <- mDNS publisher + port-80 reverse proxy (`overcast bridge`)
  router/                    <- chi router, middleware, health + debug endpoints
  middleware/                <- RequestID, Logger, Recovery, SigV4 stub
  protocol/                  <- AWS wire format (XML/JSON errors, ARNs, request IDs)
  state/                     <- Store interface + MemoryStore + SQLiteStore
  serviceutil/               <- shared helpers (request, pagination, validation, logging, lazy init)
  services/
    s3/                      <- 27 service packages; see STATUS.md for coverage
    sqs/
    dynamodb/
    ...
tests/
  AGENTS.md                  <- test conventions (GWT, mocks, helpers)
  helpers/                   <- TestServer, assertions, MockStore
  integration/               <- HTTP-level tests per service
docs/services/               <- per-service endpoint support matrices
```

---

## Code quality — quick reference

All coding standards are in [CONTRIBUTING.md](./CONTRIBUTING.md). This section is a lookup table for common agent decisions.

### Error format by service

| Service                    | Format                     | Helper                                    |
| -------------------------- | -------------------------- | ----------------------------------------- |
| S3                         | XML (bare `<Error>`)       | `protocol.WriteXMLError(w, r, aerr)`      |
| Route 53                   | XML (`ErrorResponse` env.) | `protocol.WriteQueryXMLError(w, r, aerr)` |
| SQS, SNS, DynamoDB, Lambda | JSON                       | `protocol.WriteJSONError(w, r, aerr)`     |
| Unimplemented              | Same format as the service | `protocol.NotImplementedXML/JSON(w, r)`   |

501 responses get `x-emulator-unsupported: true` and all responses get a request ID — both automatically. Never set either manually.

### Other rules not to forget

- **State:** all through `state.Store`; JSON serialisation in `store.go` only. Update both implementations when changing the interface.
- **Malformed persisted state must be isolated.** A single corrupt or stale record in `state.Store` must not make list/scan operations, unrelated resources, or the whole service return HTTP 500. When reading many records, skip malformed records and log/track the gap where practical; when reading one named resource, prefer a modeled AWS-style not-found/invalid-resource error if the record cannot be safely decoded. Only return `InternalError` for actual infrastructure failures (store unavailable, query failed, marshal failed), not for one bad persisted payload that can be isolated without breaking AWS-facing semantics.
- **Clock:** `clock.Clock` only — never `time.Now()`. See [CONTRIBUTING § Clock](./CONTRIBUTING.md#time--clock-injection).
- **Client-facing URLs:** mint through `serviceutil.ClientBaseURL` / `ClientBaseURLFromOrigin` (configured hostname, **caller's** port) — never from `r.Host` directly, never from config alone. Two deliberate divergences exist (SQS wire echo, ECR's registry URI) and are commented in place; do not "unify" them without reading [docs/plans/client-facing-url-minting.md](./docs/plans/client-facing-url-minting.md), which records each service's constraints and why.
- **Shared helpers:** use `serviceutil` — see [CONTRIBUTING § Utilities](./CONTRIBUTING.md#shared-utilities--use-serviceutil-never-duplicate).
- **CloudFormation handlers stay thin:** translate CloudFormation properties to the underlying service API, return AWS-shaped physical IDs/`Ref`/`GetAtt`, and encode replacement/delete semantics. Do not duplicate service validation, defaulting, persistence, lifecycle, or execution behavior; dispatch through the emulator router whenever possible. Add CloudFormation-specific validation or error translation only when it makes observable behavior closer to real AWS. See [CONTRIBUTING § CloudFormation integration](./CONTRIBUTING.md#cloudformation-integration).
- **S3 is the final routing fallback, after generated AWS operation ownership.** S3's broad bucket/object routes live on a private router rather than the main chi router. Explicit service routes run first; then the generated AWS operation registry may claim a modeled non-S3 request and return a protocol-correct `501`; only traffic without sufficient non-S3 ownership evidence delegates to S3. The explicit Smithy RPC v2 route similarly delegates to S3 only when `Smithy-Protocol` is absent. This ordering is deliberate because S3 has no distinguishing header or path prefix. Consequences:
  - When you add a service that uses **versioned REST paths** (e.g. `/2018-10-31/...`, `/v3/foo`) or any non-S3 root path, you must (a) register the routes in `RegisterRoutes`, and (b) add the path prefix to `detectService` in [internal/middleware/logger.go](./internal/middleware/logger.go). Otherwise every request to that service will appear in logs as `service=s3` and bypass IAM/region/SigV4 middleware that branches on service name.
  - If you see `service=s3` in logs for a request that clearly isn't S3 (e.g. `POST /2018-10-31/layers/.../versions`), verify both `detectService` and the actual route. The label alone no longer proves the request reached S3.
  - **Bugs cause fallback too.** A typo in a route path, a missing `RegisterRoutes` entry, a misnamed `chi.URLParam`, or middleware that mutates the URL can make a supported request miss its service handler. Depending on its method, path, and SigV4 scope, the symptom may now be either an S3 response or a generated non-S3 `501`. Confirm the explicit route matched before changing `detectService` or the generated registry. Logging classification, chi route matching, and generated fallback ownership are separate decisions.
  - 501s under `service=s3` for paths like `/<bucket>/?encryption=` or `/<bucket>/?policy=` are real S3 sub-resource calls and belong to S3.

---

## Checklists

The full checklists are in CONTRIBUTING.md:

- [How to add an endpoint](./CONTRIBUTING.md#how-to-add-an-endpoint)
- [How to add a service](./CONTRIBUTING.md#how-to-add-a-service)
- [Service package structure](./CONTRIBUTING.md#service-package-structure)
- [Web UI standards](./CONTRIBUTING.md#web-ui-standards)

---

## Agent workflow — before and after every task

### Before editing

1. Identify the **service** and **AWS protocol** (Query, JSON 1.1, REST JSON, REST XML)
2. Locate an **existing implementation** to mirror — copy the pattern, not the temptation to invent
3. Check **config impact** — does it need a new field in `*config.Config`?
4. Check **storage impact** — does it need `state.Store` changes? Both implementations?
5. Check **docs impact** — capability tables, service docs, STATUS.md, changelog fragment under `.changelog/` (never edit `CHANGELOG.md`'s `[Unreleased]` directly — see `.changelog/README.md`)
6. Define a **minimal useful test plan** — failing test first

### Before finishing

1. Run **scoped tests** (`go test -count=1 ./internal/services/x/... ./tests/integration/x/...`)
2. Run **`gofmt -w`** then **`go vet`** over changed packages
3. Run **`make docs`** if you changed capabilities or service behavior
4. Run **`make aws-models-check`** if you changed capabilities, protocol dispatch, generated AWS ownership, or operation routing
5. Verify **no custom endpoints** were introduced — everything must match real AWS wire format
6. Verify **CloudFormation handlers** are registered for any new resource types (or stubbed)
7. Widen to `go build ./...` and `go vet ./...` — these work on a bare checkout; see [Generated files](#generated-files) for the one thing they don't cover (a real `web/dist`)
8. Run **`make lint-go`** (golangci-lint). Build, vet and tests do **not** cover it: CI runs `Lint` as its own required job, and staticcheck findings it reports (unused variables, redundant declarations, merged decl/assign) pass all three of the above. If you touched `web/`, run **`make lint-web`** and **`pnpm run typecheck`** there too.

`make check` runs `fmt vet lint test` in one go and is the safest final gate — prefer it over assembling your own subset. Every command CI runs is in [.github/workflows/test.yml](./.github/workflows/test.yml); if your final check is narrower than that file, you have not verified the change.

A `git push` from Claude Code runs [scripts/verify-changed.sh](./scripts/verify-changed.sh) first (wired as a `PreToolUse` hook in [.claude/settings.json](./.claude/settings.json)) and blocks the push if it fails. It scopes to what the branch changed: golangci-lint when any `.go` file changed, `pnpm run typecheck` and `pnpm run lint` when `web/` changed. It takes a couple of minutes and it is a backstop, not a substitute for running the checks yourself — it deliberately does not run the test suite, and it stays out of the way (exit 0, with a warning) when a toolchain is unavailable. Run it directly any time: `bash scripts/verify-changed.sh`.

### Self-review the diff before committing or pushing

Green checks prove the code compiles and passes tests. They say nothing about whether the diff is the change you meant to make. **Read your own diff end to end before every commit, and read the whole branch diff before every push.**

```sh
git diff                    # unstaged
git diff --staged           # exactly what the commit will contain
git status --short          # untracked files you may have forgotten or never meant to add
git diff main...HEAD        # the whole branch, as the reviewer will see it
git diff --stat main...HEAD # scan for files you did not expect to have touched
```

Read it as the reviewer, not as the author: judge each hunk on whether it earns its place in *this* change, not on whether you remember writing it. If you cannot say why a hunk is there, it does not belong — revert it or split it out.

**This matters most in long sessions.** The longer a session runs, the more the branch diverges from anything you still hold in context: approaches tried and abandoned, debugging aids added under pressure, a helper written twice because the first one was forgotten, a file edited early against assumptions that later changed. None of it fails a build. All of it lands in the PR unless you look. Do a full self-review pass before the first commit of a long session even if you have been careful throughout — after several hours of churn your memory of the branch is a summary, and the diff is the fact.

Check for, at minimum:

- **Debug leftovers** — stray `fmt.Println`/`log.Printf`, `console.log`, commented-out code, `t.Skip`, hardcoded endpoints or credentials, temporary fixtures, scratch files, capture harnesses under `web/public/`, a `-run TestOne` narrowing left in a Makefile target.
- **Dead ends** — code from an approach you abandoned, now unreferenced. Helpers with no callers, config fields nothing reads, a test for behaviour that no longer exists. `go vet` and golangci-lint catch some of this; they do not catch an exported function nobody calls.
- **Accidental duplication** — a helper you wrote in service A that already existed in `serviceutil`, or that you wrote twice under two names in the same branch. Long sessions are where this happens.
- **Churn that nets to nothing** — files that appear in the diff only as reordered imports, reflowed comments, or a change made then substantially undone. Revert them; they cost the reviewer attention and prove nothing.
- **Unrelated edits** — changes to files outside the task, and other agents'/the user's uncommitted work swept in by `git add .`. Stage explicit paths. See the [`commit` skill § Staging Discipline](./.agents/skills/commit/SKILL.md).
- **Stale comments and docs** — a comment describing what the code did two iterations ago is worse than no comment. Same for a plan doc under `docs/plans/` that must be accurate as of this commit, a service doc's *both* tables, and STATUS.md.
- **Completeness against your own claims** — every `state.Store` change in both implementations, every new resource type registered in `provisioner.go`, the changelog fragment under `.changelog/`, the test that was supposed to fail first. Re-read the task and the [Before finishing](#before-finishing) list against the diff, not against your recollection.
- **Commit coherence** — if the branch has grown several unrelated reasons, split it into commits that each stand alone rather than pushing one commit that does four things.

**A review that finds something after the commit is not too late — amend.** Reviewing before you commit is the ideal, but a commit is cheap to correct while the branch is still yours, so never let "I have already committed it" become the reason a leftover ships. Fix it and `git commit --amend`, which is what [What agents must NOT do](#what-agents-must-not-do) already asks for mistakes you find in your own work: amending is fine even after pushing, on a branch you own that nobody shares, with `git push --force-with-lease`. Use a follow-up commit instead once the change is merged, the branch is genuinely shared, or unrelated commits sit on top — and note that a PR with auto-merge enabled can merge underneath you mid-task, which turns an intended amend into a follow-up whether you like it or not. A rejected `--force-with-lease` is that situation announcing itself; re-fetch and look before overriding it. What is never acceptable is committing something you know does not belong and leaving it for the reviewer, or narrating it as a known issue in the PR body.

For a substantial or long-running branch, run the [`code-review` skill](./.agents/skills/code-review/SKILL.md) over the diff before pushing — it applies the full AWS-parity, regression-risk, and maintainability checklists that a quick read-through will not. Delegating the read to a sub-agent is a good use of one: it reviews the diff without the anchoring bias of having written it. Fix what it finds before the push, not in a follow-up commit.

---

## Common mistakes

Agents most often trip on these — check before finishing:

- **Committing without reading the diff** — green checks do not tell you the diff is the change you meant to make. See [Self-review the diff](#self-review-the-diff-before-committing-or-pushing)
- **Creating non-AWS endpoints or custom response fields** — the AWS SDK must work unmodified
- **Changing wire formats without tests** — request/response shapes are the compatibility contract
- **Forgetting `make docs`** after capability changes — generated tables will drift
- **Forgetting `make aws-models-check`** after capability or operation-routing changes — the AWS operation coverage CI job will fail
- **Updating only one store implementation** — `MemoryStore` and `SQLiteStore` must stay in sync
- **Forgetting CloudFormation resource handlers** — every resource-creating endpoint needs an entry in `provisioner.go`
- **Using `time.Now()` instead of `clock.Clock`** — makes tests untestable
- **Bypassing `serviceutil` / duplicating helper logic** — DRY across services
- **Returning bare `404`** — unimplemented operations must return `501`
- **Using subfolders as sub-packages inside a service** — all service files live in one flat package
- **Testing only with raw HTTP** — prefer AWS SDK clients for management-plane validation where possible
- **Forgetting `make docs-index`** after editing `docs/` — the committed docs search index goes stale and CI fails
- **Adding a compat test to one suite only** — every SDK/CLI suite tests the same operations; add to `compat/suites/registry.json` first, then implement everywhere. `go run ./cmd/compat --check-parity` fails the build otherwise. See [compat/AGENTS.md § Baseline & uniformity policy](./compat/AGENTS.md#baseline--uniformity-policy)
- **Leaving the changelog question unanswered** — a PR that adds no fragment under `.changelog/` fails the `Changelog entry` check until someone says the omission was deliberate. Add the fragment, or comment `/no-changelog <reason>` on the PR (`gh pr comment <number> --body '/no-changelog test-only: new fixtures for the SQS suite'`). The reason is required and is kept; `/needs-changelog` puts the question back. A PR whose every file is in an area that never ships (`compat/`, `tests/`, test files anywhere, `docs/plans/`, `docs/dev/`, `.agents/`, contributor docs, local tooling) is passed without a word. Never edit `CHANGELOG.md` to clear it — that file belongs to the release PR, and a second hand in it aborts the bot's merge and stops the release PR refreshing itself. While a release PR is open just add the fragment; the bot folds it in for you. See [.changelog/README.md § When a change needs no fragment](./.changelog/README.md#when-a-change-needs-no-fragment)
- **Leaving a compat test failing** — the baseline is at zero failures and CI enforces that absolutely: *any* failing test fails the build, as does a result that gets worse than `compat/baseline.json` records. Never hand-edit the baseline to record a fix; improvements are promoted automatically on `main`

---

## Generated files

These generated sources are **committed** and must be regenerated through their owning command:

| File | Regenerate with |
| --- | --- |
| `internal/capabilities/all.gen.go` | `make generate-caps` |
| `internal/docssearch/index.gen.go` | `make docs-index` |
| `web/src/docs-index.gen.ts` | `make docs-index` |
| `internal/awsapi/manifest.gen.go` | `make generate-aws-operations` |

- **After editing a published doc under `docs/`, run `make docs-index` and commit the result.** CI fails otherwise: `make docs-check` compares both files against what `docs/` would produce.
- **`docs/plans/` and `docs/dev/` are NOT indexed — skip `make docs-index` for them.** [scripts/docs-index.go](./scripts/docs-index.go) skips both directories outright (`filepath.SkipDir`) and `isPublishedDocPath` excludes them, so regenerating after a plan or dev-doc edit produces an identical file and only costs you a minute. They are working documents, not user-facing pages.
- **A Markdown-only change needs no test run.** Editing a plan, a dev doc, or prose in a published doc cannot change Go behaviour, so `go test` proves nothing. Run tests when code, generated files, or test fixtures change. (Published docs still need `make docs-index`; the index is generated output, not a test.)
- **Never hand-edit or hand-merge generated files.** Resolve docs-index conflicts with `make docs-index`. Regenerate `internal/awsapi/manifest.gen.go` with `make generate-aws-operations`, using an `api-models-aws` checkout at the revision pinned in `models/aws/VERSION` and setting the `AWS_MODELS_DIR` and `AWS_MODELS_REVISION` variables required by the target.
- **Reproduce the AWS operation coverage CI job with `make aws-models-check`.** It validates the committed manifest, runtime ownership indexes, protocol identifiers, router coverage, and capability-to-model alignment without network access. With a pinned checkout, set `AWS_MODELS_DIR` to add the same byte-for-byte regeneration check used by the scheduled model-refresh workflow.
- `.gitattributes` marks generated sources `linguist-generated`, so GitHub review collapses them.
- A bare `git clone` builds: `go build ./...` and `go vet ./...` need no generation step.

### `web/dist` — the one thing you may still have to build

`embed.go` has `//go:embed all:web/dist`, and the SPA is build output, so it is *not* committed — only a `web/dist/.gitkeep` placeholder is, which keeps the embed pattern resolving. Consequences:

- Go builds always compile. A binary built without an SPA serves the API normally and returns a 503 naming `make build-web` on the web UI port — that response is the symptom, not a bug to investigate.
- Anything that must actually serve the UI needs `make build-web` first. `make ci-local-go`, the Docker build, and release builds all assert a real `web/dist/index.html` and fail loudly without one.
- Backend-only work never needs it: `go vet -tags slim ./...` skips the UI entirely.
- `-tags slim` also removes real routes, not just the UI — `/_mcp` is registered only in `!slim` builds. A test that exercises one of those surfaces must carry the same build constraint, or it fails under `-tags slim` and looks like a routing bug when it isn't. See [tests/AGENTS.md § Build-tag-sensitive tests](./tests/AGENTS.md#build-tag-sensitive-tests--guard-the-test-like-its-subject).

---

## Human handoff — when behaviour is unclear

1. Prefer **real AWS behaviour** — spin up a resource and test the edge case
2. Then **existing Overcast behaviour** — consistency within the codebase matters
3. Then **compatibility test expectations** — what do tests in `tests/` and `compat/` expect?

If a task would require broad architectural changes, **stop and surface the tradeoffs** rather than refactoring across services silently. A `501` with an honest explanation is better than a divergent `200`.

---

## Release awareness

Changes merged into `main` do **not** imply a stable release. Docker images are only published when a release tag is pushed. Do not assume code on `main` is available to end users — treat it as nightly/integration until tagged.

---

## Working efficiently

`go build ./...` and `go vet ./...` over the whole repo is slow. Go's build cache means **only changed packages recompile**, so scope verification to what actually changed:

```sh
# After touching internal/services/foo/ — build and vet only that subtree
go build ./internal/services/foo/...
go vet  ./internal/services/foo/...

# Or run its tests (test compilation implies vet, -count=1 avoids cached results)
go test -count=1 ./internal/services/foo/... ./tests/integration/foo/...
```

Widen to `./...` only once before marking a task done. Avoid `go build ./cmd/overcast` during iteration — the `cmd/overcast` main package embeds the web UI, adding unnecessary overhead for backend-only changes. Use `./cmd/overcast -tags slim` or stay within `./internal/...` until the final check.

`-tags slim` also drops the embedded SPA, so backend-only verification never needs `make build-web` — see [Generated files](#generated-files).

For TypeScript changes, run `pnpm run typecheck` in `web/`. Do not use `tsc --noEmit` directly: it resolves `web/tsconfig.json`, a solution-style config with `"files": []` and only project references, so it compiles zero files and always passes.

### Iterating on a specific test

When fixing a single handler or function, run only the relevant test rather than the full package suite:

```sh
go test -count=1 -run TestMyFunction ./internal/services/foo/...
```

To compile and vet without executing tests (fast syntax/type check):

```sh
go test -run=^$ -count=0 ./internal/services/foo/...
```

### Run `gofmt` before `go vet`

`go vet` can emit misleading output on unformatted code. Always format first:

```sh
gofmt -w ./internal/services/foo/
```

Or to check without writing: `gofmt -l ./internal/services/foo/`

### Check the editor error panel (workspace problems) before running a build

The language server surfaces compile and vet errors without a terminal round-trip. Use the `get_errors` tool to read the current problem list — if it's empty for the files you changed, a scoped `go vet` is usually sufficient confirmation.

### Use the `Explore` sub-agent for read-only investigation

When you need to understand an unfamiliar part of the codebase (e.g. "how does the SQS handler parse queue URLs?"), delegate to the `Explore` sub-agent rather than chaining many sequential searches in the main conversation. It returns a focused summary and keeps your context clean for implementation work.

---

## Merging — no `--admin`, ever

Merge with plain `gh pr merge --squash` (or `--rebase`). The main ruleset now
enforces what used to be convention: required status checks, PR-only changes,
linear history — and it has **no bypass actors**, so `--admin` cannot skip
anything and must not be attempted. A quarantine PR under the compat flake flow
still merges normally: its deliberate "Aggregate Compatibility Results" red is
not a required check.

While `VERSION` on main names an **untagged** version (a release is pending or
a release workflow has failed), merge only changes needed to get that release
out — `scripts/release-candidate-check.sh` printing `true` is the definition of
this window. Publishing itself always waits for the maintainer's approval of
the `release` environment.

**Never enable auto-merge on a release-prep PR.** Do not run `gh pr merge --auto`
against a PR that changes `VERSION`, and do not enable auto-merge on it through
the web UI. Auto-merge is fine for ordinary PRs, where letting the required
checks gate the merge is exactly the point. A release-prep PR is different: its
merge to `main` is what opens the release window and starts the release
workflow, and it should happen only after a human has smoke tested the RC image
that the PR itself published (see [RELEASE.md](./RELEASE.md) § Creating An Alpha
Release, step 6). Green checks say the build is sound; they do not say the
release is ready. Merge it as a deliberate, separate step.

This is a merge-timing rule, not a publishing safeguard — the `release`
environment's required reviewer already means nothing ships without the
maintainer approving the exact SHA.

## Stacked pull requests

When your work needs code from a PR that has not merged yet, stack on it rather
than duplicating the code or waiting: target that PR's branch instead of `main`.
GitHub understands stacks natively — it runs CI on **every** layer, not just the
bottom, and rebases and retargets the branches above when the bottom merges.

Build one with the `gh stack` extension (`gh stack init` / `add` / `submit` /
`sync`), turn existing branches into one with `gh stack init branch1 branch2`,
join **already-open** PRs with `gh stack link` (remote-only — it rebases and
force-pushes nothing, so it is safe against another agent's branch), or open a
PR against another PR's branch in the web UI. REST, GraphQL and webhooks expose
the stack for automation. Not supported in GitHub Desktop.

Four rules, each of which has cost this repo a recovery:

- **Merge bottom-up**, and **never `--delete-branch` a base that has children** —
  it closes the child PR and deletes its head branch, and a closed PR cannot be
  retargeted.
- **Never rebase or force-push a branch you do not own while its PR is open.** If
  a lower PR conflicts with `main`, its owner fixes it; ask rather than fixing it
  for them.
- **After anything in the stack merges, `gh stack sync`** (or fetch and reset to
  the remote) — GitHub has already rebased the branches above, so a local
  `git rebase` replays commits the server dropped.
- **A PR showing no checks at all is `CONFLICTING`, not queued** — GitHub
  dispatches no workflows on a conflicting PR. Check `mergeStateStatus` before
  waiting on CI.

Full workflow, including the generated-file conflict recipe, is in the
`stacked-prs` skill.

## Reserved ports — 4566 and 4567 belong to the user

Agents must **not** start their own test instances of Overcast on port **4566** (API) or **4567** (web UI) — those are reserved for the user's own running instance, unless the user explicitly directs otherwise. Starting a test instance on those ports silently breaks whatever the user is doing with their instance, or fails confusingly when theirs is already bound.

- Use `scripts/run-test-instance.sh` (or `.ps1`) — it picks a free port pair at or above 4570, refuses the reserved pair even when the scan base is moved, passes extra `docker run` args through after `--`, and prints the API endpoint and web UI URLs.
- When you want the user to look at something in a test instance, give them the **full clickable URL including the port** (e.g. `http://localhost:4570` / `http://localhost:4571`) — never say "open the web UI" and assume a port.
- The same courtesy applies in reverse: something already listening on 4566/4567 is the user's instance — never kill it, restart it, or point tests at it without being asked.

## What agents must NOT do

- **Never push directly to `main`.** Agents must not run `git push origin main`, push the current branch when it is `main`, create or move tags on `main`, or otherwise update protected release branches directly. Always work on a feature/release branch and use a pull request or explicit human-managed merge path. If a task appears to require a direct `main` push to trigger automation, stop and ask for human confirmation instead. (The compat workflow's baseline-promotion commit is the sole exception, and it belongs to the workflow — never to you: see [compat/AGENTS.md § Baseline & uniformity policy](./compat/AGENTS.md#baseline--uniformity-policy).)
- **Never commit directly on `main`.** All changes must go through a non-`main` branch and pull request. Before committing, run `git branch --show-current`; if it returns `main`, stop and ask before doing anything else. This applies to every change, including release prep, docs-only edits, generated files, and emergency fixes.
- **Start editing workflows on a branch.** At the start of any skill or workflow that may edit files or create commits, check `git branch --show-current`. If it returns `main`, create or switch to a task branch before editing; if unrelated worktree changes make that unsafe, stop and ask. Use clear branch names such as `fix/sqs-visibility-timeout`, `compat/elasticache-serverless-cache`, or `release/0.0.1-alpha.6`.
- **Amend related mistakes instead of narrating them.** If you forgot a directly related file such as a changelog fragment, generated doc, or focused test, amend or squash your own commit so the branch stays coherent. This is fine even after pushing when you own the branch, it is not shared, and you use `git push --force-with-lease` to avoid overwriting other people's work. Use a separate follow-up commit instead when the change is already merged, the branch is known to be shared, or unrelated commits now sit on top. Do not create noisy correction commits or give the user a running play-by-play of fixups unless the branch history is shared or the user asks for that detail.
- **Never leave the workspace in a broken state.** After every change, check the workspace problem list (compiler errors, type errors, lint errors) - via the `get_errors` tool, and fix any problems you introduced before considering the task done. You are not finished while problems you caused remain open.
  - **`go build ./...` is necessary but not sufficient.** It only catches compile errors. Also run `go vet ./...` to catch lint/static-analysis warnings (unused params, unused funcs, unnecessary nil checks, etc.) that appear in the VS Code Problems panel but don't fail compilation. Fix every warning you introduced.
  - **Sub-agents must do this too.** A sub-agent invoked by a parent agent is held to the same standard. Before returning a result, run `go build ./...` (for Go changes) and/or `pnpm run typecheck` in `web/` (for TypeScript changes — not `tsc --noEmit`, which typechecks nothing) and fix every error you caused. If a linter or vet warning is introduced (e.g. `go vet ./...` reports a new issue), fix it. Do not offload verification to the parent — own it.
- **Never commit or push a diff you have not read.** Read `git diff --staged` before every commit and `git diff main...HEAD` before every push, and remove anything that does not belong. This is not optional on long sessions — it matters most there. See [Self-review the diff](#self-review-the-diff-before-committing-or-pushing).
- Never implement a handler or fix a bug without a failing/reproducing test first
- Never return bare `404` for unimplemented operations — always `501`
- Never call `os.Getenv` in service code — use `*config.Config`
- Never update only the summary table in a service doc — update both tables
- Never publish a performance claim without measurement conditions — every number (startup time, memory, image size, latency) must document what was measured, how, and under what conditions. See [docs/dev/performance.md § Documenting performance claims](docs/dev/performance.md#documenting-performance-claims)
- Never do blocking work (store reads, network I/O, DDL, file reads) inside `<svc>.New()` or an `Init*` method called from `router.New()`. Use a `sync.Once`-guarded lazy-init method called from the handlers that need it. See [docs/dev/performance.md § Startup budget — rules for service authors](docs/dev/performance.md#startup-budget--rules-for-service-authors)
- Never edit `web/src/routeTree.gen.ts` — it is auto-generated by TanStack Router when the dev server runs (`pnpm run dev` in `web/`). After adding or changing route files, check whether the dev server is already running (the user usually has it running); if so, the file will update automatically. Only regenerate manually if the server is not running.
- Never assume that you are the only AGENT working - be careful with git operations that may break what others are working on (e.g `git stash` or `git checkout`)

---

## Tool use discipline — preventing hallucination in tool chains

Tools are the agent's primary source of truth about the runtime environment. To ensure clean, reliable tool-chaining:

### Ground truth rule

- **Tool outputs are authoritative.** If a tool returns data (e.g., `runtime_list_instances` returns a list of endpoints), that IS the current state. Never override it with cached knowledge, prior context, or assumptions.
- When a current tool result conflicts with prior context, surface the discrepancy explicitly for user review rather than silently choosing one.

### Chaining discipline

- **Use tool N's output as input to tool N+1.** For example: `runtime_list_instances` → use exactly those endpoints for `runtime_probe_instance`. Never probe endpoints not returned by the prior tool unless explicitly asked.
- **Document the dependency.** Annotate tool calls with the reason: "Using endpoint from runtime_list_instances: http://localhost:4566"
- **Never diverge into cached assumptions** once a tool chain is active. If prior knowledge suggests a different path, ask the user: "I know from earlier context that X, but the tool just returned Y—which should I use?"

### Validation before deviation

- Before using an endpoint, config value, or other runtime fact NOT returned by a tool, explicitly ask or state the assumption
- Default action when uncertain: use only what the current tools returned
- If no tool has provided it, that's a signal to call an appropriate tool

### Immutable snapshots

- Treat a tool's result as immutable within the task context
- Reference it by the snapshot, not by reconstructed logic
- Example: "Using the instances from step 1 (localhost:4566, 127.0.0.1:4566)"—then don't invent a third instance mid-chain

### This prevents

- Hallucinating service endpoints or config from prior logs
- Ignoring explicit tool output because prior context "feels" more reliable
- Skipping obvious follow-up tools because of assumptions about what they'd return
- Silent divergence from the stated task into cached knowledge paths
