---
name: release
description: "Cut an Overcast release and test the release candidate before it ships. Use when: preparing a release, curating changelog fragments into a version section, bumping VERSION, running the Release Prep workflow, smoke testing or regression testing an RC image, or deciding whether a compat result blocks the release."
compatibility: opencode
metadata:
  audience: maintainers
  workflow: release
  languages: "go,typescript,markdown"
argument-hint: "Target version, or the RC image tag to test"
license: MIT
---

# Release — Overcast

Cutting a release is two jobs. The **mechanics** — version, changelog, PR, workflow, approval — are
documented in [RELEASE.md](../../../RELEASE.md) and largely automated. The **judgement** is deciding
whether the candidate those mechanics produced is fit to publish, and that is what this skill is
mostly about.

[RELEASE.md](../../../RELEASE.md) is the authority on process. Where this file and RELEASE.md
disagree, RELEASE.md wins and this file is stale — fix it. This skill does not restate the steps; it
says which ones need a human looking at them, and it adds the release-candidate testing pass that
RELEASE.md only names in passing.

---

## When to Use

- Preparing a release-prep PR, by workflow or by hand
- Curating `.changelog/` fragments into a versioned `CHANGELOG.md` section
- Testing a release candidate before merging the release-prep PR
- Deciding whether a compat regression, a flaky quarantine, or a late merge blocks the release
- Keeping an open release PR current as `main` moves underneath it

Do NOT use this skill to merge or publish. `VERSION` is CODEOWNER-owned, the merge is deliberate and
human, and every publish job waits on the `release` environment's required reviewer. An agent
prepares and tests; it does not ship.

---

## Guardrails

- **Never on `main`.** Run `git branch --show-current` before the first edit. Release prep is
  `release/x.y.z-alpha.n`, created off `main`. See [AGENTS.md](../../../AGENTS.md) § Git and branch
  safety.
- **Never enable auto-merge on a release-prep PR.** Its merge opens the release window; that is a
  deliberate step, not a race with CI.
- **Never edit `CHANGELOG.md` from a non-release PR.** The refresh job merges `main` into the release
  branch on every push, and a second hand in that file aborts the merge and stops the release PR
  keeping itself current. Other PRs add a fragment; the bot folds it in.
- **Exactly one release in flight.** Dispatching prep while a release PR is open for a different
  version fails by design.

---

## Preparing

The normal path is the **Release Prep** workflow (`workflow_dispatch`), which does the mechanical
half and opens the PR:

```sh
gh workflow run release-prep.yml -f version=0.0.1-alpha.29
```

What it cannot do is **curate**. It emits one bullet per fragment; the house style merges same-area
entries into single bullets and tightens the prose (`.changelog/README.md`). Treat the section it
writes as a draft to edit.

Two things in the draft need care rather than tidying:

- **Breaking entries.** Each is flagged `**BREAKING**` and carries a `migration:` note. Both the fact
  of the break and what to do about it must survive curation — they are the part of the notes a user
  cannot work out for themselves. Lead with them; do not let them dissolve into a merged bullet.
- **Entries that overlap.** Several fragments often describe one feature from different angles
  (service, web UI, CloudFormation). Merge them into one bullet per area so a reader sees the feature
  rather than the PR sequence.

Validate before pushing rather than discovering a missing link reference in CI:

```sh
python scripts/check-release-changelog.py 0.0.1-alpha.29
```

Note `python`, not `python3` — the release docs say `python3`, which on some contributor machines is
a stub that exits without running anything.

Then work [RELEASE.md § Preflight Checklist](../../../RELEASE.md#preflight-checklist). Two of its
items are judgement calls, not checkboxes:

- **`main` green** — a red **Aggregate Compatibility Results** has two readings. A regression in an
  area this release touches is a blocker. A red whose only cause is new `compat/flaky.json` entries
  is the quarantine sign-off from the flake flow, not a regression (`compat/AGENTS.md` § Stabilising
  a flaky test).
- **The release PR is a snapshot** of `.changelog/` as it stood when the branch last changed, and
  `main` keeps moving. Late fixes are what a release window attracts. The refresh job folds new
  entries in automatically, but folded bullets are uncurated — read them.

---

## The release candidate

**RC = release candidate.** CI treats any same-repo PR whose `VERSION` carries no `v<VERSION>` tag as
one (`scripts/release-candidate-check.sh`), so the release-prep PR publishes, per build:

- `ghcr.io/overcast-sh/overcast:<version>-rc.<n>` — the console image
- `ghcr.io/overcast-sh/overcast-slim:<version>-rc.<n>` — the slim image, no web UI, no SQLite
- the native binaries, as workflow artifacts

`<n>` increments per build, so every push to the release branch produces a new candidate and the bot
comment on the PR always describes the current one, with pull commands and image digests.

**Test the RC image, not a local rebuild.** The whole point is to exercise the bits CI produced —
the build path, the embedded web assets, the pinned base image digests — none of which a local
`docker build` reproduces faithfully.

### Testing the candidate is the agent's job, not a handover

**Once CI has published the RC images, run the passes below and post the evidence.** Do not stop at
"the RC is ready to smoke test" and hand the list to the user — an agent that opens the release PR
and then asks someone else to exercise it has done the half of the work that was already automated
and skipped the half that was not. The mechanics are a workflow; this is the part a person would
otherwise have to do by hand, which is exactly why it is worth doing for them.

Two things bound it. Green CI checks do **not** substitute: they prove the build is sound, not that
the changelog's claims are true. And testing is not merging — `VERSION` stays CODEOWNER-owned, the
merge stays a deliberate human step, and every publish job still waits on the `release`
environment's reviewer.

Concretely, the agent — not the user — does all of this:

1. **Pull the RC console image by digest** from the bot comment and run it with
   `scripts/run-test-instance.sh --mount-docker-socket` on a remapped port. The **console** image is
   the one to test the UI against: the SPA is embedded in it and absent from slim. Confirm the
   digest you pulled matches the bot comment, so the evidence names bits that actually exist.
2. **Verify every claim in the release section of `CHANGELOG.md`** against that instance (steps
   1–3 below). This is the point of the exercise: the section you just curated is a set of
   promises a user will read and act on, and until each one has been run against the candidate it
   is an assertion, not a fact. Work bullet by bullet and record the request and the response for
   each — including the ones that turn out to be true, because "verified" with no evidence behind
   it is worth no more than the note it is checking.
3. **Drive the web UI yourself with the `chrome-devtools` MCP and take the screenshots** (step 4).
   Headless, against the running RC, with real state seeded through the SDK first. Do not describe
   a page in prose, do not ask the user to open it, and do not skip it because the release "is
   mostly backend" — if a `[web/...]` bullet is in the changelog, there is a page to photograph.
4. **Post one comment on the release PR** with the summary and the screenshots embedded (see
   [Evidence to keep](#evidence-to-keep)). The approval is made against that comment, so it has to
   exist before you say the RC is ready.

---

## Docs-only candidates

When every entry in the curated section carries a `[docs]` prefix, the pass
below shrinks to smoke plus one console check — nothing built from Go or the
web UI changed, only the docs it serves. Run:

- **Smoke** on both images (§1): start each on a remapped port, hit
  `/_overcast/health`, and make one bare-SDK call.
- **Console docs viewer**, driven headlessly: the nav renders, one split
  guide and one service sub-page open, and search finds both.

Cite the targeted (§3) and compat (§2) suites from CI rather than re-running
them. Anything non-docs in the section restores the full pass below.

---

## What to test

### 1. Smoke — is it alive and correctly wired

Not "does it start". Starting proves very little, and the failures that reach a release are
endpoint-shaped. [docs/dev/manual-testing.md](../../dev/manual-testing.md) is required reading
before you conclude anything passed; its checklist is the bar.

The parts that catch real bugs:

- **A remapped port**, never `-p 4566:4566`. A 1:1 mapping makes a config-derived origin accidentally
  correct and hides the entire class. `scripts/run-test-instance.sh` (or `.ps1`) picks a free pair at
  or above 4570 and refuses the reserved pair.
- **A bare SDK client** — `new SQSClient({})`, no `--endpoint-url`, no explicit `endpoint`. The AWS
  CLI honours `AWS_ENDPOINT_URL` for everything and so cannot reproduce the bugs the JS/.NET/Java
  SDKs hit, which resolve SQS from the queue URL's own origin.
- **From inside a container**, for anything endpoint-related. `localhost` inside a Lambda is that
  Lambda.
- **Both images.** Slim is the same API surface with no web UI or SQLite — worth one pass, and it is
  the one nobody remembers to check.

```sh
scripts/run-test-instance.sh --image ghcr.io/overcast-sh/overcast:<version>-rc.<n>
scripts/run-test-instance.sh --image ghcr.io/overcast-sh/overcast-slim:<version>-rc.<n>
```

Services backed by real containers — Lambda, ECS, RDS, ElastiCache, MSK — need the Docker socket
mounted, or they degrade to metadata-only stubs and you will be testing the stub. Add
`--mount-docker-socket`, which appends exactly that one bind mount:

```sh
scripts/run-test-instance.sh --mount-docker-socket \
  --image ghcr.io/overcast-sh/overcast:<version>-rc.<n>
```

**Required: route reachability against this same instance.** `TestAllDeclaredCapabilitiesAreReachable`
(`tests/integration/router/route_reachability_dev_test.go`) already runs this in-process on every PR,
but it exercises `router.New` directly, not the built image — different embedded web assets, whatever
build tags actually shipped, and no real Docker daemon behind the container-backed services. Re-run it
against the RC container above, which is where its live-instance requirement is satisfied — no separate
instance to stand up:

```sh
go run -tags dev ./scripts/route-reachability.go -endpoint <the URL run-test-instance.sh printed>
```

A clean report is `0 declared operations are unreachable at their modeled binding.` Any `unreachable`
row blocks the release: it means an implementation is mounted somewhere no AWS SDK will ever call it,
the exact fault [docs/plans/route-reachability-audit.md](../../../docs/plans/route-reachability-audit.md)
was written to close out. Re-run with `-show-body` on any `shared-path` row to see which service actually
answered before deciding it is one of the audit's already-verified shared bindings rather than a new one.

### 2. Regression — the compatibility suite

**Do not re-run what the release PR's workflows already ran.** Every push to the
release branch runs the full CI matrix and the compatibility suites, and the
required **Aggregate Compatibility Results** check on the release PR is the
regression evidence — cite that run (link the workflow run and its summary) in
the evidence comment instead of reproducing it locally. The same principle
covers the unit/integration matrix and route reachability's in-process variant:
green required checks on the release PR are evidence to cite, not work to
repeat. What CI does **not** do is the manual half — the smoke pass on the RC
images with a real SDK on a remapped port, the claim-by-claim walk of the
release section, and the console screenshots — and that is where the agent's
time goes.

Run the suites locally against the RC image only when CI has NOT produced an
equivalent run — a targeted re-check after a late fix, a suite the release
branch's filters skipped, or a machine-local condition CI cannot reproduce. In
that case, point the runner at the candidate image rather than letting it build. **This is two commands, and
combining them runs nothing** — `--max-failures`, like `--compare-baseline`, `--report` and
`--check-parity`, is a *gate mode*: it reads an existing `--results-file` and exits without
executing a single test (`cmd/compat/main.go`, and `compat/AGENTS.md` § "Flags that read a results
file instead of producing one").

```sh
# 1. run the suites
go run ./cmd/compat \
  --overcast-image ghcr.io/overcast-sh/overcast:<version>-rc.<n> \
  --format json --results-file compat-results.json

# 2. then gate the results it produced
go run ./cmd/compat --results-file compat-results.json --max-failures 0
```

**Not through `scripts/docker-go.sh`.** That wrapper is the right answer for
almost every other `go` command here, and the wrong one for this: the container
it runs in has no Docker socket, so the runner cannot start anything and reports
`no way to start Overcast` (`compat/AGENTS.md` § Running the suites). compat
drives Docker itself, so it runs on the host.

**Passing both at once fails silently in the worst possible direction.** The run never happens, the
gate reads whatever `compat-results.json` is already on disk — the default path, and a file a
previous run very likely left there — and prints `failure gate passed`, exit 0. A release can be
signed off against yesterday's results having executed nothing. If the suites did run, the log ends
with per-suite output and the results file's mtime is fresh; check both before believing a pass.

`--max-failures 0` is the gate proper: since the baseline was burned to zero, **any** `fail` reds the
run regardless of what the baseline says. Read the results with `--report`, which separates
unimplemented services (expected) from genuine failures and cascade failures — fix the root cause of
a cascade before chasing its dependents.

A previously passing result becoming `fail` or `unimplemented` blocks the release unless a maintainer
explicitly accepts it (RELEASE.md § Compatibility Evidence).

### 3. Targeted — the changelog section is the test plan

This is the step that gets skipped, and it is the one that only exists at release time. **Every
bullet you just curated is a claim about behaviour that a user will read and rely on.** Walk the
section and exercise the claims against the running RC, prioritising:

- **Every `**BREAKING**` entry.** Its `migration:` note is a test case written out for you: the note
  says what a user must change, so verify the old shape now fails the way the note says it does, and
  that the new shape works. A break that does not actually break is a documentation bug shipping to
  every reader.
- **Anything newly strict.** Entries phrased "now rejects", "now requires", "returns 501 instead of"
  are behaviour a stack in the wild is currently relying on. Probe the rejection, not just the happy
  path.
- **Anything gated by an env var** — the default is what users get, so test the default *and* the
  flag. A feature only reachable behind a flag nobody sets is a different release than the notes
  imply.
- **Newly emulated behaviour that used to be a no-op.** These are the entries most likely to be
  wrong, because nothing previously depended on them being right.
- **Then every remaining bullet.** The four above are the order to work in, not the scope. The
  section is the test plan in full: each bullet is a sentence a user will act on, so each one gets
  exercised or gets named as untested. "Prioritise" means start there, not stop there.

**Get the shape right before calling a claim false.** A claim that looks broken is often a probe
built from the note's prose rather than from the API. Before reporting a failure, check the request
against the implementation or the pinned model — a PromQL alarm is detected from the dotted
`EvaluationCriteria.PromQLCriteria.Query` form, and a flat `EvaluationCriteria=…` is silently a
different request. Re-run the corrected shape, and report what the corrected run showed.

**Mind the coverage gap.** Compat suites do not cover every service. Cross-check the areas this
release touched against `compat/suites/registry.json`: an area with Go integration tests but no
compat group is not exercised by step 2 at all, and step 3 is the only thing standing between it and
users. Say so in the release evidence when it happens.

### 4. Console — only the full image

The web UI is embedded in the console image and absent from slim. Open the pages this release
changed against a *running* RC with real state created through the SDK, not an empty emulator — a
page that renders fine with no data is the usual way a broken view passes review.

There is no e2e framework in the repo, but this pass is **not** manual and **not** something to hand
back to the user. Drive it headlessly with the **`chrome-devtools` MCP server** the repo declares in
[`.mcp.json`](../../../.mcp.json), exactly as for any other visual change — the full workflow, and
the reasoning behind each step, is in the [`pull-request` skill § Visual
Evidence](../pull-request/SKILL.md#visual-evidence). Claude Code runs it `--headless --isolated`, so
an agent with no display can capture the same images a reviewer would see. If the tools are absent,
the client has not picked the file up: restart the session rather than reaching for another
mechanism.

Three things that skill covers and this pass depends on:

- **`--mount-docker-socket` is what makes the console connect at all** on a remapped port. Without
  it the container cannot see its own port mapping, and every screenshot is of the *Connect to
  Overcast* screen.
- **`wait_for` the text that only exists once the state is real** — a seeded resource's name — never
  a fixed sleep. A screenshot of a spinner is worse than none.
- **`emulate` sets the axes**, so a light/dark or narrow/wide pair differs only in the axis under
  test. Pick the axis the change actually moves and say why; a blanket cross-product is its own
  failure.

Write the images outside the repo tree and attach them to the PR comment below.

### 5. Cleanup

`docker ps -a` empty of `overcast-*` and any test-instance containers, and no leftover volumes from
EFS or RDS runs. A leaked container from a release test is the one that confuses the next session.

---

## Evidence to keep — post it as a PR comment

RELEASE.md § Compatibility Evidence asks for these. **Post them as a single comment on the release
PR** naming the exact RC tag and digest you tested, so the approval is made against something rather
than against a memory of a session nobody else saw:

- the RC tag and image digests, so it is unambiguous which bits were exercised
- `compat-results.json` from the run against the RC image
- the **Aggregate Compatibility Results** workflow summary
- any baseline comparison output
- a claim-by-claim table for step 3: every bullet in the release section, and whether it was
  verified, with the observed evidence — the request and the response, not an assertion
- the step 4 screenshots, embedded
- what you did **not** test, and why — the gaps matter more than the passes, because they are what
  nobody else can infer from a green check

Report failures plainly. A release note that says a thing works, when the candidate showed it does
not, is worse than no note.

**Anything you find that is not a regression and not a blocker becomes a GitHub issue**, with the
reproduction, the pre-existing-versus-new determination, and the blast radius — not a line in the PR
comment that scrolls away. Say so in the comment and link it. Determine which it is by running the
same probe against the previous release's image (`ghcr.io/overcast-sh/overcast:<previous>`) and, where the
answer matters, by dating the code with `git log -S` and `git tag --contains`: a fault present in
every tag is not this release's problem and must not hold it up.

---

## After the merge

The `Release` workflow builds and tests unattended, then **pauses at the `release` environment**. It
publishes nothing until someone approves the exact SHA. Then work RELEASE.md § Post-Release
Checklist — notes generated from `CHANGELOG.md`, checksums matching, both image families published,
and the follow-up PR restoring an empty `[Unreleased]`.

If the workflow fails, do not reuse a published tag for a different commit. RELEASE.md § If The
Release Workflow Fails has the recovery, and the short version is: roll forward to the next
prerelease counter rather than repointing a tag.
