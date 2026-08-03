### No release note — and this PR is preparing `$RELEASE_VERSION`

This PR sets `VERSION` to **$RELEASE_VERSION**, so it is the release itself —
but it also changes something outside `VERSION`, `CHANGELOG.md` and
`.changelog/`. A release PR that only consumes fragments is passed without a
word; this one is not that, and the extra change ships with the release.

I have changed nothing here. There is nothing wrong with this PR.

**This is the one place a `CHANGELOG.md` edit is the right answer.** Everywhere
else the file belongs to the release PR and a second hand in it aborts the
bot's merge — but here you *are* the release PR, and the
`## [$RELEASE_VERSION]` section is yours to write. Put the note straight into
it, in the house style, rather than adding a fragment: a fragment left in
`.changelog/` fails `check-release-changelog.py` and so fails the release.

The `refresh` job only ever appends to that section, so nothing you write there
is rewritten by the next push to `main`.

**If the extra change needs no note** — which it usually does not, being release
plumbing — say so and I will clear the check:

```
/no-changelog release plumbing on the release branch, nothing a user can observe
```

The reason is required and it is kept, as the record of the decision.
`/needs-changelog` puts the question back.

Only the repository owner, an organisation member or a collaborator can waive
it. If that is not you, say in a comment why no note is needed and ask a
maintainer to run the command.

> Worth asking whether the change belongs here at all. A release branch is for
> the release; a fix that could go through `main` on its own PR gets its own
> fragment, its own review, and folds itself into this section automatically.
