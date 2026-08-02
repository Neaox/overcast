### No changelog fragment — deliberate, or forgotten?

This PR adds nothing under `.changelog/`, so it would ship with no line in the
release notes. That is often exactly right, and it is often an oversight, and
from here the two look identical — so it has to be said out loud rather than
assumed.

I have changed nothing here. There is nothing wrong with this PR.

**If the change is release-note-worthy**, add a fragment and push:

```sh
python3 scripts/changelog.py new -e '+ [sqs] long polling on `ReceiveMessage`'
```

The grammar, the categories and the breaking-change marker are in
[.changelog/README.md](../blob/main/.changelog/README.md) — never edit
`CHANGELOG.md`'s `[Unreleased]` section directly.

**If it genuinely needs none**, say so in a comment and I will clear this check:

```
/no-changelog CI-only: pins the release action to a digest, nothing shipped changes
```

The reason is required and it is kept: it is what a reviewer reads instead of
guessing, and it is the difference between a decision and a reflex. Comment
`/needs-changelog` to put the question back if it turns out to be wrong — and do
that yourself if later commits on this PR add something users would want to read
about, because the waiver covers the PR, not the commit it was written on.

Changes that usually need no fragment: CI-only, test-only, local tooling,
internal refactors, generated-file churn, and anything under `compat/` — those
suites observe the emulator rather than change it, however large the diff. The
full rule is
[.changelog/README.md § What merits a fragment](../blob/main/.changelog/README.md#what-merits-a-fragment).

Only the repository owner, an organisation member or a collaborator can waive
it. If that is not you, say in a comment why no fragment is needed and ask a
maintainer to run the command.
