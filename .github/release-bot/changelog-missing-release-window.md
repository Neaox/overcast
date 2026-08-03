### No release note — and `$RELEASE_VERSION` is waiting to be tagged

**$RELEASE_VERSION** is prepared on `main` but has not shipped, and this is the
one window where a fragment is the *wrong* answer: `.changelog/` has to stay
empty, because `check-release-changelog.py` fails the release while any
unconsumed fragment remains. There is no release PR left to fold one into
either — it has already merged.

Editing `CHANGELOG.md` is not the way round it. That file belongs to the release
PR: `release-prep.yml` merges `main` into its branch on every push, and a second
hand in the same section aborts that merge and stops the release PR refreshing
itself. Fragments exist so that concurrent PRs never meet in one file.

I have changed nothing here. There is nothing wrong with this PR.

**If the change is release-note-worthy**, the honest options are:

- **Wait for the tag**, then add the fragment as usual. `main` in this state
  should be taking only what is needed to get $RELEASE_VERSION out
  ([AGENTS.md](../blob/main/AGENTS.md#merging--no-admin-ever)), so a change that
  can wait usually should.
- **Merge it now if the release needs it**, waive below, and add the fragment
  once the tag is out. Say so in the reason — the note then lands in the next
  release's section, describing something that shipped in this one, and whoever
  curates it needs to know that.

**If it needs none** — which most things merged in this window do, being release
plumbing — say so and I will clear the check:

```
/no-changelog release plumbing only, nothing a user can observe
```

The reason is required and it is kept, as the record of the decision.
`/needs-changelog` puts the question back.

Only the repository owner, an organisation member or a collaborator can waive
it. If that is not you, say in a comment why no note is needed and ask a
maintainer to run the command.

> This is the window *after* the release PR merged, where there is nothing left
> to fold a fragment into. It is the only one that reads this way.
>
> While that PR is still **open**, none of the above applies: add a fragment
> exactly as usual and I fold it into the release section for you on the next
> push to `main`, deleting the fragment as I go.
>
> And if you are reading this on the release PR itself, I am wrong and it is a
> bug — that PR is exempt and I should have said nothing.
