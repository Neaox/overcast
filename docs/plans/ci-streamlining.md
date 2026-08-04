# CI streamlining — where the time goes, and what is safe to cut

Status: proposal. Nothing here is implemented.

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

### 4. `cross-build` builds 11 targets on every PR

`cross-build` is `needs: [web, release-gate]` with no `if:`; only
`upload-artifacts` is conditional on the release-candidate check. So every PR
spends ~600 job-seconds proving that 11 GOOS/GOARCH combinations compile. Most
of each job is runner startup rather than compilation.

Cross-compilation breakage is real but rare, and it is a *compile* failure —
the class most cheaply caught, and least likely to be introduced by a change
that `go build ./...` already accepts.

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

**C. Take the SPA build off the critical path.** Run the coverage job with
`-tags slim` and add one narrow job that exercises what only exists in a
non-slim build — the embedded SPA handler and the `!slim` routes — taking the
`web/dist` artifact. That job is small and parallel; the long one stops waiting.

*Saves:* up to ~187 s of wall clock on every PR and every push to `main`.
*Risks:* the narrow job has to actually cover the non-slim surface, or this
trades wall clock for a hole. Worth writing the job first, confirming it fails
when `/_mcp` regresses, and only then changing `coverage`.

**D. Fold `cross-build` into fewer jobs, or scope it.** Either loop the targets
inside one job (saves ~9 runner-minutes, costs wall clock in that job, which is
off the critical path anyway), or keep a two-target smoke on PRs and run all 11
on `main` and on release candidates.

*Saves:* ~9 runner-minutes per PR. *Risks:* scoping delays discovery of a
GOOS-specific break from PR to `main`. The loop-in-one-job option has no such
tradeoff and is the safer of the two.

### Tier 3 — scope reduction, handle required checks carefully

**E. Path-filter the heavy workflows.** A docs-only PR does not need 14
runner-minutes of SDK suites. `compat.yml` already uses `paths-ignore` on
`push`, but its `pull_request` trigger is unfiltered.

*Risks, and they are the real content of this item:* a required check that is
skipped never reports, and the PR waits forever. This needs the standard
skip-shim — a cheap job with the same name that reports success when the filter
excludes the real one — or the check must not be in the required set. Do not
add a path filter to a required check without one.

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

## Suggested order

1. **A** and **B** — independent, days not weeks, and B stops the bleeding that
   makes everything else look worse than it is.
2. **F** — small, and closes the gap that prompted this review.
3. **C** — the real wall-clock win; write the narrow non-slim job first.
4. **D**, then **E** with its skip-shim.

Expected outcome if all land: PR wall clock roughly 9 → 6 minutes, runner
minutes roughly 56 → 40 per PR, and a materially lower rate of red that is
nobody's fault.
