This is the release PR for **$VERSION**. I opened it from the changelog fragments
that were on `main`, and I will keep it up to date as more land.

## What I did

- Set `VERSION` to `$VERSION`
- Added the `## [$VERSION]` section to `CHANGELOG.md`
- Repointed the compare links at the new tag
- Deleted the fragment files those entries came from

## What happens next

- **On every push to `main`** — I merge it into this branch, append any new
  entries to `## [$VERSION]`, and comment to say what I added. You do not need to
  do anything to keep this PR mergeable.
- **When you merge this** — the release workflow builds and tests, then waits for
  your approval in the `release` environment before anything is published.

## Optional, whenever it suits

The entries are one bullet each, straight from the fragments. The house style
merges same-area entries into single bullets and tightens the prose — see
[.changelog/README.md](../blob/main/.changelog/README.md). Reword freely: I only
ever append, and never edit what is already in the section.

Worth doing before you merge: smoke test the release-candidate image rather than
a local rebuild. Its pull command and digest are in the release-candidate comment
below.

---

I cannot merge this myself. `VERSION` is owned in `.github/CODEOWNERS` so it needs
your review, and every publish job waits on the `release` environment.
