# Release Process

This document describes how to cut an Overcast release using the repository's
GitHub release workflow.

Overcast is still pre-1.0. Treat every release as a local-development and CI
tool release, not a production readiness claim.

## Automation Overview

The normal release path is a release-prep pull request that updates `VERSION`
and is merged to `main`. Never push release-prep changes directly to `main`.
When `.github/workflows/release.yml` sees `VERSION` change on `main`, it
automatically builds, tests, creates or updates the matching GitHub release,
uploads native binaries, and publishes Docker images.

The same workflow can also run from:

- a published GitHub release event
- manual `workflow_dispatch`

In every trigger path, the workflow reads `VERSION`, derives the tag as
`v<VERSION>` unless a release event supplied a tag, and rejects mismatches between
the tag and `VERSION`.

## End To End

Day to day, PRs merge to `main` and each records its release notes as entry
lines in a fragment file under `.changelog/` — written with
`python3 scripts/changelog.py new`, documented in
[.changelog/README.md](.changelog/README.md). `CHANGELOG.md`'s `[Unreleased]`
section stays empty; nothing accumulates there, so concurrent PRs never
conflict over the changelog.

When a release is wanted:

1. **Branch** (never `main`) and set `VERSION` to the new version.
2. **Curate.** `changelog.py assemble` prints a draft section built from every
   fragment currently in `.changelog/`, grouped by category then area, with
   breaking entries flagged `**BREAKING**` and their `migration:` notes
   attached. Edit that draft into `CHANGELOG.md` in the house style, then
   delete every fragment file.
3. **Open the PR.** CI treats any same-repo PR whose `VERSION` carries no tag
   as a release candidate: it publishes RC images and the native binaries and
   maintains one bot comment linking them. Smoke test those bits — they are
   what CI built, not a local rebuild.
4. **Re-curate whenever `main` moves.** A PR merged after this branch last
   changed does not re-trigger anything here, and its fragments would ride
   onto `main` through a clean union merge. The changelog gate fails the PR
   until the new entries are folded into the release section — see
   [Keeping The Release PR Current](#keeping-the-release-pr-current).
5. **Merge.** The `Release` workflow builds and tests unattended, then pauses
   at the `release` environment for a one-click approval of the exact SHA.
6. **Approve.** Binaries, `SHA256SUMS`, both Docker images and the GitHub
   release publish, with release notes generated from the `CHANGELOG.md`
   section.

## Release Artifacts

The release workflow:

- verifies the release tag matches `VERSION`
- verifies `CHANGELOG.md` has a non-empty section for the release version
- verifies `[Unreleased]` is empty before publishing
- verifies no unconsumed changelog fragments remain in `.changelog/`
- runs `go vet ./...`
- runs `go test -race -count=1 -coverprofile=coverage.out -timeout=600s ./...`
- runs `pnpm run typecheck` for the web UI (both `tsconfig.app.json` and `tsconfig.node.json`)
- builds the web UI
- uploads native binaries for Linux, macOS, and Windows
- uploads `SHA256SUMS`
- publishes Docker images to GHCR:
  - `ghcr.io/neaox/overcast:<version>`
  - `ghcr.io/neaox/overcast-slim:<version>`
- publishes a channel tag:
  - prereleases: `:<channel>` such as `:alpha`
  - stable releases: `:latest`
- creates or updates the GitHub release
- replaces the GitHub release notes with generated notes from the versioned
  `CHANGELOG.md` section

## Version Format

Use SemVer tags with a leading `v` for GitHub releases.

Examples:

- Alpha: `v0.0.1-alpha.0`
- Later alpha: `v0.0.1-alpha.1`
- Beta: `v0.0.1-beta.0`
- Stable: `v0.1.0`

The release workflow strips the leading `v` and requires the result to exactly
match the contents of `VERSION`.

For example:

```text
VERSION = 0.0.1-alpha.0
GitHub release tag = v0.0.1-alpha.0
```

For prereleases, the Docker channel tag is derived from the prerelease suffix.
`0.0.1-alpha.0` publishes `ghcr.io/neaox/overcast:alpha` and
`ghcr.io/neaox/overcast-slim:alpha`.

**Choosing the number is not a judgement call while in alpha.** The prerelease
counter increments and the base version stays put, whatever the release
contains — a breaking change does not move it. Nothing is guaranteed stable
yet, and although we do not break things on purpose, things are changeable
enough that changing them may break you. So `0.0.1-alpha.27` is followed by
`0.0.1-alpha.28` whether the release adds a service, fixes a bug, or removes
an endpoint.

That does not make breakage unimportant, it makes *saying so* the job: a
breaking entry is marked in its fragment and carries a `migration:` note, and
curation must land both in the release section (see below). The versioning
rules in [CHANGELOG.md](./CHANGELOG.md) — where breaking means MAJOR — take
effect at 1.0, and the markers accumulated through alpha are what will let
them be applied mechanically rather than reconstructed from memory.

## Branch Safety

Release prep must happen on a dedicated branch and be merged through a pull
request. Do not commit release-prep changes while the current branch is `main`,
even if you do not intend to push directly.

Create or switch to the release branch before making release-prep edits. This is
the first step of the workflow, not something to defer until commit time.

Before editing `VERSION` or `CHANGELOG.md`, verify the branch:

```sh
git branch --show-current
```

If the command returns `main`, stop and create/switch to a release branch first:

```sh
git switch -c release/x.y.z-alpha.n
```

For agents: if you discover you are already on `main` during release prep, stop
and ask before committing. The protected workflow is branch PR -> merge to
`main` -> release automation, not local commits on `main`.

## Preflight Checklist

Before merging the release-prep PR to `main`:

1. Confirm `main` is green for the standard test workflow. (The required
   status checks on `main` enforce most of this per merge already.)
2. Confirm **Aggregate Compatibility Results** is green on the latest `main`
   run — the baseline gate does the regression comparison automatically and
   lists any `pass -> fail` as annotations. Two readings of a red matter:
   regressions in areas your release touches are blockers; a red whose only
   cause is new `compat/flaky.json` entries is the quarantine sign-off from
   the flake flow, not a regression (see compat/AGENTS.md § Stabilising a
   flaky test).
3. Assemble the release notes from the changelog fragments:
   ```sh
   python3 scripts/changelog.py assemble x.y.z-alpha.n
   ```
   Curate the printed draft into a versioned section that exactly matches
   `VERSION`, for example `## [x.y.z-alpha.n] - YYYY-MM-DD` — merge same-area
   entries into single bullets per the house style (see
   `.changelog/README.md`) — then delete every consumed fragment file
   (everything in `.changelog/` except `README.md`).

   Entries the draft flags `**BREAKING**` need care: each carries a
   `migration:` note, and both the fact of the break and what to do about it
   must survive into the release section. They are the part of the notes a
   user cannot work out for themselves, so lead with them rather than letting
   them dissolve into a merged bullet.
4. Set `VERSION` to the exact release version without the leading `v`.
5. Ensure `[Unreleased]` exists but has no entries, and `.changelog/` holds
   only `README.md`. The workflow fails if `[Unreleased]` contains release
   notes or a fragment was left unconsumed. This is checked twice: once
   against the PR as pushed, and once against the tree the PR would produce
   if merged into `main` as it stands right now. The second check exists
   because a PR merged after this branch was last pushed does not re-trigger
   anything here — a fragment that lands mid-window would otherwise ride onto
   `main` through a clean union merge and fail the release after the merge.
   If it trips, re-curate the new fragment into the release section and push;
   the update re-runs the gate.
6. Run local scoped checks for release metadata changes:
   ```sh
   go test -count=1 ./cmd/compat
   go vet ./cmd/compat ./compat
   ```
7. Open a PR containing the release-prep change and merge it through GitHub.
   The merge commit on `main` starts the automated release. Do not push the
   release-prep commit directly to `main`.

## Creating An Alpha Release

### Automated prep

The **Release Prep** workflow
([`.github/workflows/release-prep.yml`](.github/workflows/release-prep.yml),
`workflow_dispatch`) does the mechanical half of steps 1-5 below: derives the
version, assembles and inserts the release section, repoints both compare
links, writes `VERSION`, deletes the consumed fragments, opens the PR, and
comments a summary that lists any breaking changes with their migration notes.

It stops there by design. It never merges — `VERSION` is owned in
[CODEOWNERS](.github/CODEOWNERS) — and publishing still waits on the `release`
environment's required reviewer.

What it cannot do is **curate**. The generated section is one bullet per entry;
the house style merges same-area entries into single bullets and tightens the
prose. Treat the PR as a draft to edit, not as finished notes.

Re-running it against an open release PR **does not rewrite the section**. It
comments with the fragments that have landed on `main` since — which is also
the staleness notification described in
[Keeping The Release PR Current](#keeping-the-release-pr-current) — because
regenerating would discard curation already done. Pass `regenerate: true` only
when you genuinely want the section replaced wholesale. `dry_run: true` prints
the summary and diffstat without pushing or opening anything.

It requires the `RELEASE_APP_ID` and `RELEASE_APP_PRIVATE_KEY` secrets and
fails without them, deliberately: a PR opened with the default `GITHUB_TOKEN`
triggers no `pull_request` workflows, so it would arrive with no CI, no RC
images and no changelog gate. The App is also what makes the PR author someone
other than the maintainer, so the CODEOWNERS review on `VERSION` is a check a
human can actually satisfy.

The steps below remain the manual path, and describe what the workflow does.

For an alpha release:

1. Create and work on a release branch, not `main`:
   ```sh
   git switch -c release/x.y.z-alpha.n
   ```
2. Update `VERSION`:
   ```text
   x.y.z-alpha.n
   ```
3. Assemble and curate the changelog fragments into:
   ```markdown
   ## [x.y.z-alpha.n] - YYYY-MM-DD
   ```
   using `python3 scripts/changelog.py assemble x.y.z-alpha.n` as the starting
   draft, then delete the consumed fragment files from `.changelog/`. Carry
   every `**BREAKING**` entry and its `migration:` note into the section — see
   Preflight step 3.
4. Leave the `[Unreleased]` section present and empty (it stays empty between
   releases; fragments are the only unreleased record).
5. Update the compare links at the bottom of `CHANGELOG.md` — add a
   `[x.y.z-alpha.n]` reference and repoint `[Unreleased]` at the new tag:
   ```markdown
   [Unreleased]: https://github.com/Neaox/overcast/compare/vx.y.z-alpha.n...HEAD
   [x.y.z-alpha.n]: https://github.com/Neaox/overcast/compare/v<previous>...vx.y.z-alpha.n
   ```
   Then confirm steps 2-5 with the same validator the `Release` workflow runs,
   rather than discovering a missing link reference in CI:
   ```sh
   python3 scripts/check-release-changelog.py x.y.z-alpha.n
   ```
6. Commit the release-prep changes on the release branch and open a PR.
   CI treats any same-repo PR whose `VERSION` has no `v<VERSION>` tag yet as
   a **release candidate** (`scripts/release-candidate-check.sh` — this also
   covers follow-up PRs after a failed release workflow, when the unreleased
   version already sits on `main`). Each candidate build publishes
   `ghcr.io/neaox/overcast[-slim]:<version>-rc.<n>` (linux/amd64, `<n>`
   increments per build), uploads the ten native binaries as workflow
   artifacts, and maintains one bot comment on the PR with pull commands,
   image digests, and the artifact table. Smoke test the RC image — the
   exact bits CI built — rather than a local rebuild.
7. Merge the release-prep PR to `main`.
8. Watch the `Release` workflow. It builds and tests unattended, then
   **pauses at the `release` environment** before publishing anything —
   approve the deployment (one click, shows the exact SHA) to let the
   publish jobs run. Then watch until all jobs pass.
9. Verify the GitHub release `v<VERSION>` exists and contains native
   binaries plus `SHA256SUMS`.
10. Verify the Docker images exist:
   ```sh
   docker pull ghcr.io/neaox/overcast:<version>
   docker pull ghcr.io/neaox/overcast:alpha
   docker pull ghcr.io/neaox/overcast-slim:<version>
   docker pull ghcr.io/neaox/overcast-slim:alpha
   ```
11. Smoke test the slim image:
   ```sh
   docker run --rm -d --name overcast-smoke -p 4566:4566 ghcr.io/neaox/overcast-slim:<version>
   curl -sf http://localhost:4566/_health
   docker stop overcast-smoke
   ```

## Keeping The Release PR Current

A release PR is a snapshot of `.changelog/` as it stood when the branch last
changed, and `main` keeps moving underneath it. This is the normal case, not
an edge one: late fixes are exactly what a release window attracts.

GitHub does not help here. A `pull_request` run is triggered by pushes to the
*head* branch, so merging something else into `main` re-runs nothing and the
PR's green checks stay green while going stale. The failure that follows is
quiet: the release PR deleted one set of fragments and the new PR added
another, so the merge is a clean union, the new fragment rides onto `main`
next to the `VERSION` bump, and the release fails *after* merging.

Two mechanisms catch it:

- The `Release` workflow re-runs `check-release-changelog.py` against the tree
  the PR **would produce if merged into `main` as it stands now**, not against
  the PR's stale base. A fragment that landed mid-window fails the PR by name.
- The compat and baseline gates compare against `origin/main` fetched at job
  time, so a `compat/flaky.json` or baseline change on `main` shows up as a
  spurious diff on any branch that has not caught up.

When either trips, the fix is the same:

1. Update the branch from `main` (merge or rebase — the repository
   squash-merges, so the branch's own history does not reach `main`).
2. Curate the newly arrived entries into the release section and delete their
   fragment files.
3. Push. That re-runs every check and re-publishes the RC images and binaries,
   so the bot comment describes the candidate you would actually ship.

A release PR that has been open across several merges is often cheaper to
recut than to reconcile: `assemble` regenerates the whole draft, and the
curation already done can be pasted back over it.

## Manual Release Trigger

Manual GitHub release creation is optional. Use it only when the PR-merge
automation did not run or a maintainer intentionally wants to republish the
release from the existing commit. Do not use a direct push to `main` as a manual
release trigger.

If creating a GitHub release manually:

1. Use tag `v<VERSION>`, for example `v0.0.1-alpha.0`.
2. Target the release-prep commit on `main`.
3. Mark prerelease versions as prereleases.
4. Keep notes brief; the workflow replaces them with generated notes from
   `CHANGELOG.md` after assets and Docker images publish.

## If The Release Workflow Fails

Do not reuse a published tag for a different commit.

While a release is pending or failed — that is, while `VERSION` on `main`
has no `v<VERSION>` tag — every same-repo PR is treated as a release
candidate (`scripts/release-candidate-check.sh`): changelog validation runs,
RC images publish, and the merge discipline in AGENTS.md applies. Re-running
the release workflow pauses at the `release` environment for approval again,
like any other publish.

If the workflow created a GitHub release but artifacts failed:

1. Fix the issue on `main`.
2. Create a new prerelease version, for example `0.0.1-alpha.1`.
3. Move the failed release notes forward, update `VERSION`, and merge to `main`
   so the workflow creates the next tag/release, for example `v0.0.1-alpha.1`.
4. Mark the failed release as superseded in its notes, or delete it if no
   artifacts were consumed.

If the failure is only transient infrastructure and the release tag still points
at the intended commit, rerunning the failed workflow jobs is acceptable.

## Post-Release Checklist

After the release workflow succeeds:

1. Confirm release notes were generated from `CHANGELOG.md`.
2. Confirm native binaries download and checksums match `SHA256SUMS`.
3. Confirm both Docker image families are published.
4. Confirm `README.md` quick-start commands work with the version tag.
5. Open a follow-up PR that:
   - sets `VERSION` to the next development version if needed
   - restores an empty `[Unreleased]` section in `CHANGELOG.md` if it was
     removed (it must always exist, and stays empty — unreleased changes are
     fragments under `.changelog/`)
   - updates compare links at the bottom of `CHANGELOG.md`

## Compatibility Evidence

Compatibility tests are not a 100% AWS parity gate. They are a regression and
coverage signal.

Before release, keep these artifacts for review:

- merged `compat-results.json`
- GitHub workflow summary from Compatibility Tests
- any baseline comparison output

Known unsupported APIs may remain as `fail` or `unimplemented`. A previously
passing compat result becoming `fail` or `unimplemented` should block the
release unless a maintainer explicitly accepts the regression.
