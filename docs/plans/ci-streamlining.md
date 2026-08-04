# CI streamlining — where the time goes, and what is safe to cut

Status: A, B, C, E and F are implemented; D was investigated and declined. One
repository-settings change is left — see [Outcome](#outcome). The measurements
below are from before any of it landed, and are kept as the baseline the
changes were argued from.

The question this answers: CI is slow enough to be felt on every PR, on `main`,
and on a release. What can be made faster without buying the speed with
coverage or with confidence in a green tick?

Confidence is the harder half. A suite that is fast because it stopped checking
something is worse than a slow one, and a suite that goes red for reasons
unrelated to the change teaches people to re-run without reading — which is the
same failure with extra steps.

## Measurements

Job durations from real green runs, in seconds. CI: run `30888966223`
(`.github/workflows/test.yml`). Compat: the most recent green
`.github/workflows/compat.yml` run. These are single samples on GitHub-hosted
runners, so treat them as shape rather than as benchmarks.

| CI job | s | | Compat job | s |
| --- | --- | --- | --- | --- |
| Full test suite with coverage | 349 | | cli | 304 |
| Test suite (-tags slim) | 236 | | java-sdk | 97 |
| Test suite (-tags slim,nosqlite) | 231 | | cdk | 70 |
| Test suite (-tags slim,dev) | 214 | | Build compat binaries | 70 |
| Web UI | 187 | | go-sdk | 61 |
| Lint | 164 | | python-sdk | 56 |
| SQS integration (protocol dispatch) | 126 | | dotnet-sdk | 53 |
| Docker build (slim) / (console) | 107 each | | node-js-sdk | 48 |
| Vet | 62 | | rust-sdk | 41 |
| Cross-build (11 jobs) | 48–62 each | | Aggregate results | 35 |
| AWS operation coverage | 60 | | Prepare matrix / rust image | 5 / 7 |
| Actionlint | 35 | | | |
| Docs and capability registry | 23 | | | |
| Script tests | 12 | | | |
| Compat registry schema | 9 | | | |

Roughly **42 runner-minutes** for CI and **14** for compat, per PR.

### The critical paths

- **CI: `web` (187) → `coverage` (349) ≈ 536 s.** The longest job is blocked on
  the SPA build, because an untagged `go test ./...` needs a real `web/dist`
  for the embed.
- **Compat: `prepare` (5) → `build binaries` (70) → `cli` (304) → `aggregate`
  (35) ≈ 414 s.** The `cli` suite is 3× the next-slowest and sets the floor.

Everything else runs wide and lands well inside those two.

## Findings

### 1. Superseded runs are never cancelled

Neither `test.yml` nor `compat.yml` declares a `concurrency:` group. A
force-push — the normal way a PR is revised — leaves the previous run to
completion. Every amend or rebase costs a full duplicate of both workflows, and
the stale run can still report a verdict against a commit nobody is looking at
any more. `scripts/pr-wait.sh` already has to defend against this: its exit 8
means "the head moved mid-watch, so the result I just collected is stale".

This is the largest saving available at zero cost to coverage: nothing is
checked less, the same commits are still checked.

### 2. The critical path is a serialization, not a slow test

`coverage` waits 187 s for `web/dist` and then runs 349 s. `build-tags` runs
substantially the same tests in 214–236 s without waiting, because `-tags slim`
drops the embed entirely — the workflow already comments on deliberately not
taking the artifact.

So the untagged run exists for a genuinely small surface: the embedded SPA and
the routes registered only in `!slim` builds (`/_mcp`). Paying 187 s of
critical path for it is the expensive way to buy that.

### 3. The suite runs four times

`coverage` (untagged) plus three tag sets is ~1030 job-seconds of largely the
same tests, and is the single biggest block of compute in CI.

**This is mostly worth keeping** — see "What not to cut".

### 4. `cross-build` builds 10 targets on every PR

`cross-build` is `needs: [web, release-gate]` with no `if:`; only
`upload-artifacts` is conditional on the release-candidate check. So every PR
spends ~550 job-seconds proving that 10 GOOS/GOARCH combinations compile —
five `overcast` and five `overcastd`. Most of each job is runner startup
rather than compilation.

Cross-compilation breakage is real but rare, and it is a *compile* failure —
the class most cheaply caught, and least likely to be introduced by a change
that `go build ./...` already accepts.

**On investigation this should be left alone. See proposal D.**

### 5. One Docker Hub reference escaped the hardening that covers the rest

`main` went red immediately after `#615` merged, with:

```
ERROR: failed to resolve source metadata for docker.io/docker/dockerfile:1:
Head "https://registry-1.docker.io/v2/docker/dockerfile/manifests/1": i/o timeout
```

Nothing to do with the change; a network timeout to Docker Hub.

The obvious reading — "Docker Hub is unpinned and unmirrored" — is wrong, and
worth stating because it was this document's first draft. The hardening is
already there:

- `.github/actions/docker-hub-mirror` exists and points the runner's daemon at
  `mirror.gcr.io`.
- Every `FROM` in the Dockerfile is digest-pinned (`node:22-alpine@sha256:…`,
  `golang:1.24-alpine@sha256:…`, `alpine:3.20@sha256:…`).

Two narrow gaps let this one through, and they line up exactly with what failed:

- **The `docker:` job does not use the mirror.** The action is wired into
  `coverage` and `build-tags` — the jobs that pull engine images for
  integration tests — but not into the job whose whole purpose is pulling base
  layers.
- **`# syntax=docker/dockerfile:1` is the one unpinned reference in the file.**
  The BuildKit frontend is resolved from the registry before any `FROM` is
  read, so it fails the build before the pinned layers matter.

This is a **confidence** finding, not a speed one, and it is the more damaging
kind. A red that is not your fault is what trains people to re-run without
reading the log — and the next real failure gets the same treatment.

### 6. Flakes cost a full cycle each

Two unrelated failures hit in a single afternoon: the Docker Hub timeout above,
and `TestInvoke_logTail` (unregistered, passed on re-run at the same SHA).
Each costs ~10 minutes of wall clock plus the attention to prove it was not the
change under review.

Flake hygiene belongs in a CI-speed review, not only a quality one.

### 7. Local checks cannot see tag-gated code

`scripts/verify-changed.sh` runs `golangci-lint run ./...` for Go, which uses
the default build context. Nothing local — not that, not `go vet ./...`, not
`go test ./...` — ever compiles a `//go:build dev` or `nosqlite` file. A syntax
error in `capabilities_dev.go` or any `*_dev.go` passes every local gate and
fails only in the `-tags slim,dev` CI job.

Setting `build-tags: [dev]` in `.golangci.yml` looks like the fix and is not:
those files come in pairs (`registry_dev.go` / `registry_prod.go`,
`wrap_dev.go` / `wrap_prod.go`, `coverage_source_dev.go` /
`coverage_source_prod.go`), with `//go:build !dev` on the other side. Setting
the tag drops the prod half from analysis, moving the blind spot rather than
closing it.

## Proposals

Ordered by saving per unit of risk.

### Tier 1 — no coverage change

**A. Cancel superseded runs.** Add to `test.yml` and `compat.yml`:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}
```

`cancel-in-progress` must stay false for `push: main`, `release`, and anything
the release flow depends on — a cancelled `main` run leaves the branch with no
verdict, and a cancelled release run is worse. Gate it on the event, not the
workflow.

*Saves:* one full CI + compat run per force-push. *Risks:* none, if the event
gating is right.

**B. Close the two gaps in finding 5.** Digest-pin the BuildKit frontend in the
Dockerfile, and add the existing mirror action to the `docker:` job. Both are a
line each, and between them they cover the only unhardened path left.

*Saves:* no time directly; removes a recurring false red. *Risks:* a digest pin
needs a refresh path, and this one may not have it. `.github/dependabot.yml`
runs the `docker` ecosystem over `/`, which advances `FROM` digests — but that
parser reads `FROM` lines, and a `# syntax=` directive is a comment. Confirm on
the first Dependabot run after this lands whether the frontend is picked up; if
it is not, the pin needs an explicit refresh path or it rots quietly.

### Tier 2 — structural, needs design

**C. Take the SPA build off the critical path.** **Done — and it needed none of
the surgery proposed here.**

The proposal was to move `coverage` to `-tags slim` behind a new narrow job
covering the `!slim` surface. The premise was that the untagged job needs
`web/dist` because it embeds it. It does not: the committed
`web/dist/.gitkeep` is what keeps `//go:embed all:web/dist` resolving, a binary
without a real SPA serves the API and answers 503 on the UI port by design, and
nothing in the Go suite asks for real SPA content — the BFF's static-serving
tests inject an `fstest.MapFS` of their own. The full untagged suite,
`go vet ./...` and golangci-lint all pass against a `web/dist` holding nothing
but the placeholder.

So `vet`, `coverage`, `lint` and `sqs-protocol-dispatch` simply stopped waiting.
No new job, no `-tags slim`, no coverage-semantics change, and no renamed check
— which matters, because `Vet`, `Lint` and `Full test suite with coverage` are
all in the required set.

*Saved:* ~187 s of wall clock on every PR and every push to `main`.

**D. Leave `cross-build` alone.** Investigated and declined.

Consolidating the 10 targets into one looping job saves ~5 runner-minutes of
startup, and the earlier draft called it the safer option because it keeps full
coverage. The arithmetic says otherwise. `cross-build` starts after `web`
(187 s) and finishes around 250 s, comfortably inside the critical path. Ten
sequential builds in one job is roughly 250 s of work, so it would finish
around 437 s — **making `cross-build` the new critical path and undoing C.**

The alternatives are worse rather than better: scoping the PR target set breaks
the property `test.yml` states in a comment — that the PR check "builds exactly
what a release would ship" — and splitting into GOOS groups buys ~3 runner-
minutes for a permanent complication of a workflow deliberately shared with
`release.yml`.

It is off the critical path and not a required check. The ~5 runner-minutes are
real but they are the cheapest minutes in the run.

### Tier 3 — scope reduction, handle required checks carefully

**E. Path-filter the heavy workflows.** **Half done, and the half that is done
is the half that is safe.**

The risk in this item is the whole item: a required check that is filtered out
never reports, and the pull request waits on it forever. Which checks are
required is therefore the fact the item turns on, and it is now known. From the
`Protect Main` ruleset:

```
Vet, Lint, Actionlint, Web UI,
Test suite (-tags slim), Test suite (-tags slim,nosqlite),
Full test suite with coverage,
Docker build (console), Docker build (slim),
Breaking-change hold, Changelog entry, Changelog fragments
```

**No compat check is in that set.** So `compat.yml`'s `pull_request` trigger
can be filtered directly, with no shim, and now is: a PR touching only `**.md`,
`docs/**` or `.changelog/**` skips ~14 runner-minutes of SDK suites. The filter
is deliberately short — editing a published doc also regenerates
`internal/docssearch/index.gen.go` and `web/src/docs-index.gen.ts`, which are
not matched, so a docs change that touches the index still runs the suites.

**`test.yml` is gated rather than filtered**, and the difference is the whole
design. A `paths-ignore:` would stop those nine required checks reporting at
all. The obvious repair — a twin workflow declaring the same job names on the
inverse filter — is broken for the mixed case: GitHub skips on `paths-ignore`
only when *every* changed file matches but runs on `paths` when *any* one does,
so a pull request touching a doc and a handler satisfies both and two runs
report under one check name.

So there is no shim. A `changes` job classifies the diff
(`scripts/ci-scope.py`, pinned by `scripts/ci_scope_test.py`), every job keeps
its name and still runs, and only the expensive *steps* are conditional. A
prose-only pull request gets the same green checks in seconds.

Two consequences that are easy to miss and are commented in place:

- **`web` produces the `web-dist` artifact**, so anything downstream has to be
  gated on the same output or it waits for an upload that is not coming.
  `cross-build` is the only such consumer.
- **A release candidate must build whatever the paths say.**
  `release-candidate-check.sh` reads the *checked-out* `VERSION`, so during a
  release window `rc` is true on any pull request, not only the one that
  changed `VERSION`. `web`, `docker` and `cross-build` therefore gate on
  `code || rc`, which also keeps the existing release-candidate steps — whose
  conditions assume a checkout happened — correct.

### Also worth fixing: a required-set gap

`Test suite (-tags slim,dev)` is **not** in the required set, though
`-tags slim` and `-tags slim,nosqlite` are. It is the only job that compiles
tag-gated code — every `*_dev.go`, `internal/capabilities`, `internal/mcp` —
and it is the job that caught the gap in finding 7 when it reached CI.

A change can therefore go red in that job and still be mergeable. Adding it to
the ruleset is a repository-settings change rather than a code one, so it is
noted here rather than made.

### Tier 4 — the local gate

**F. Give `verify-changed.sh` the tag matrix.** Add `go vet -tags <t> ./...`
for CI's three sets to the Go check. It catches finding 7 exactly, fits the
script's charter (it already runs golangci-lint and deliberately does not run
the test suite), is fingerprint-cached like the rest, and `slim` means it never
needs `web/dist`.

Pair it with a line in AGENTS.md's *Before finishing* list. That list currently
mentions `-tags slim` only as a speed tip — "skips the UI entirely" — which
suggests tags make checks cheaper, when the point here is that a bare vet is
*blind* to tag-gated files.

*Saves:* no CI time; removes a class of avoidable red.

## What not to cut

**The three-way tag matrix's tests.** Each tag changes real behaviour: `slim`
changes which routes register and what is embedded, `nosqlite` swaps the store
implementation, `dev` swaps the capability registry. Reducing these to
vet-only would keep the compile check and lose the behavioural one. Finding 7
is direct evidence that tag-gated code breaks independently of everything else.

**Compat's zero-failure gate.** The baseline is at zero and CI enforces it
absolutely. Its value is precisely that it does not negotiate.

**The changelog gate.** It is seconds, and it is the only thing standing
between a release and an unexplained entry.

**`Vet` as a job** is arguably redundant with `Lint` (govet is enabled there)
and with the tag matrix's own vet, but it is 62 s in parallel and off the
critical path. Removing it buys nothing worth the argument.

## Outcome

| | |
| --- | --- |
| **A** cancel superseded runs | done |
| **B** mirror + frontend digest pin | done |
| **C** SPA off the critical path | done, ~187 s saved, and simpler than proposed |
| **D** consolidate `cross-build` | **declined** — would become the new critical path |
| **E** path-filter heavy workflows | done — compat filtered, `test.yml` gated |
| **F** local tag-matrix gate | done |

CI wall clock drops from roughly 9 minutes to roughly 6 on the critical path,
a force-push no longer costs a duplicate of both workflows, a prose-only pull
request skips ~56 runner-minutes across both workflows while reporting the same
green checks, and the two recurring false reds have had their causes removed
rather than their symptoms re-run.

One thing is left, and it is not code:

**Add `Test suite (-tags slim,dev)` to the required set.** It is the only job
that compiles tag-gated code, and it is currently advisory — a change can go
red in it and still be mergeable. That is a repository-settings change.

Note that it interacts with the gate above: it becomes a thirteenth required
name, and like the other twelve it reports because its job always runs. The
gate was built that way for exactly this reason — adding a required check needs
no corresponding change here.

The estimate this document opened with — 56 → 40 runner-minutes — was built on
D landing. Without it the figure is closer to 56 → 50 on a code PR, and near
zero on a docs-only one. The wall-clock improvement is the real result; the
runner-minute one was mostly D, and D was not worth its cost.
