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

- `ghcr.io/neaox/overcast:<version>-rc.<n>` — the console image
- `ghcr.io/neaox/overcast-slim:<version>-rc.<n>` — the slim image, no web UI, no SQLite
- the native binaries, as workflow artifacts

`<n>` increments per build, so every push to the release branch produces a new candidate and the bot
comment on the PR always describes the current one, with pull commands and image digests.

**Test the RC image, not a local rebuild.** The whole point is to exercise the bits CI produced —
the build path, the embedded web assets, the pinned base image digests — none of which a local
`docker build` reproduces faithfully.

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
scripts/run-test-instance.sh --image ghcr.io/neaox/overcast:<version>-rc.<n>
scripts/run-test-instance.sh --image ghcr.io/neaox/overcast-slim:<version>-rc.<n>
```

Services backed by real containers — Lambda, ECS, RDS, ElastiCache, MSK — need the Docker socket
mounted, or they degrade to metadata-only stubs and you will be testing the stub. Add
`--mount-docker-socket`, which appends exactly that one bind mount:

```sh
scripts/run-test-instance.sh --mount-docker-socket \
  --image ghcr.io/neaox/overcast:<version>-rc.<n>
```

### 2. Regression — the compatibility suite

Point the runner at the candidate image rather than letting it build:

```sh
scripts/docker-go.sh run ./cmd/compat \
  --overcast-image ghcr.io/neaox/overcast:<version>-rc.<n> \
  --format json --results-file compat-results.json --max-failures 0
```

`--max-failures 0` is the gate: since the baseline was burned to zero, **any** `fail` reds the run
regardless of what the baseline says. Read the results with `--report`, which separates unimplemented
services (expected) from genuine failures and cascade failures — fix the root cause of a cascade
before chasing its dependents.

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

**Mind the coverage gap.** Compat suites do not cover every service. Cross-check the areas this
release touched against `compat/suites/registry.json`: an area with Go integration tests but no
compat group is not exercised by step 2 at all, and step 3 is the only thing standing between it and
users. Say so in the release evidence when it happens.

### 4. Console — only the full image

The web UI is embedded in the console image and absent from slim. Open the pages this release
changed against a *running* RC with real state created through the SDK, not an empty emulator — a
page that renders fine with no data is the usual way a broken view passes review. There is no e2e
framework in the repo; this pass is manual and deliberate.

### 5. Cleanup

`docker ps -a` empty of `overcast-*` and any test-instance containers, and no leftover volumes from
EFS or RDS runs. A leaked container from a release test is the one that confuses the next session.

---

## Evidence to keep

RELEASE.md § Compatibility Evidence asks for these; attach them to the release PR so the approval is
made against something:

- `compat-results.json` from the run against the RC image
- the **Aggregate Compatibility Results** workflow summary
- any baseline comparison output
- what you tested by hand in step 3, and what you did **not** — the gaps matter more than the passes,
  because they are what nobody else can infer from a green check

Report failures plainly. A release note that says a thing works, when the candidate showed it does
not, is worse than no note.

---

## After the merge

The `Release` workflow builds and tests unattended, then **pauses at the `release` environment**. It
publishes nothing until someone approves the exact SHA. Then work RELEASE.md § Post-Release
Checklist — notes generated from `CHANGELOG.md`, checksums matching, both image families published,
and the follow-up PR restoring an empty `[Unreleased]`.

If the workflow fails, do not reuse a published tag for a different commit. RELEASE.md § If The
Release Workflow Fails has the recovery, and the short version is: roll forward to the next
prerelease counter rather than repointing a tag.
