# Release Process

This document describes how to cut an Overcast release using the repository's
GitHub release workflow.

Overcast is still pre-1.0. Treat every release as a local-development and CI
tool release, not a production readiness claim.

## Release Artifacts

Publishing a GitHub release triggers `.github/workflows/release.yml`, which:

- verifies the GitHub release tag matches `VERSION`
- runs `go vet ./...`
- runs `go test -race -count=1 -timeout=600s ./...`
- runs `npx tsc --noEmit` for the web UI
- builds the web UI
- uploads native binaries for Linux, macOS, and Windows
- uploads `SHA256SUMS`
- publishes Docker images to GHCR:
  - `ghcr.io/neaox/overcast:<version>`
  - `ghcr.io/neaox/overcast-slim:<version>`
- publishes a channel tag:
  - prereleases: `:<channel>` such as `:alpha`
  - stable releases: `:latest`
- replaces the GitHub release notes with generated notes from `CHANGELOG.md`

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

## Preflight Checklist

Before creating the GitHub release:

1. Confirm `main` is green for the standard test workflow.
2. Confirm the compatibility workflow completed and uploaded `compat-results.json`.
3. Review `compat-results.json` for unexpected regressions:
   ```sh
   go run ./cmd/compat --report --results-file compat-results.json
   go run ./cmd/compat --compare-baseline --results-file compat-results.json
   ```
4. Confirm `CHANGELOG.md` has release-worthy notes under `[Unreleased]` or a
   versioned section matching the release version.
5. Set `VERSION` to the exact release version without the leading `v`.
6. Run local scoped checks for release metadata changes:
   ```sh
   go test -count=1 ./cmd/compat
   go vet ./cmd/compat ./compat
   ```
7. Commit and merge the release-prep change to `main` before creating the
   GitHub release.

## Creating An Alpha Release

For the first alpha release:

1. Update `VERSION`:
   ```text
   0.0.1-alpha.0
   ```
2. Move the relevant `CHANGELOG.md` notes out of `[Unreleased]` into:
   ```markdown
   ## [0.0.1-alpha.0] - YYYY-MM-DD
   ```
3. Merge the release-prep PR to `main`.
4. Create a new GitHub release from `main`:
   - tag: `v0.0.1-alpha.0`
   - target: `main`
   - mark as prerelease
   - notes can be brief; the workflow replaces them with generated notes
5. Watch the `Release` workflow until all jobs pass.
6. Verify the GitHub release contains native binaries and `SHA256SUMS`.
7. Verify the Docker images exist:
   ```sh
   docker pull ghcr.io/neaox/overcast:0.0.1-alpha.0
   docker pull ghcr.io/neaox/overcast:alpha
   docker pull ghcr.io/neaox/overcast-slim:0.0.1-alpha.0
   docker pull ghcr.io/neaox/overcast-slim:alpha
   ```
8. Smoke test the slim image:
   ```sh
   docker run --rm -d --name overcast-smoke -p 4566:4566 ghcr.io/neaox/overcast-slim:0.0.1-alpha.0
   curl -sf http://localhost:4566/_health
   docker stop overcast-smoke
   ```

## If The Release Workflow Fails

Do not reuse a published tag for a different commit.

If the GitHub release was created but artifacts failed:

1. Fix the issue on `main`.
2. Create a new prerelease version, for example `0.0.1-alpha.1`.
3. Create a new GitHub release tag, for example `v0.0.1-alpha.1`.
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
   - restores an empty `[Unreleased]` section in `CHANGELOG.md` if it was moved
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
