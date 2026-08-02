### On hold until $RELEASE_VERSION ships

Release PR $RELEASE_LINK is preparing **$RELEASE_VERSION**. A $RELEASE_KIND release
promises that nothing in it breaks, and its section of `CHANGELOG.md` has
already been written and reviewed — merging a breaking change into `main` now
would ship a break under a version number that said there was none.

I have changed nothing here. There is nothing wrong with this PR.

**What is breaking**

$BREAKING_ENTRIES

**What happens next**

The moment $RELEASE_LINK is merged or closed I re-run this check and clear it
myself — nothing to re-push. If your entry then needs folding into the released
section, the changelog check will say so.

If you would rather not wait:

- **Move it to the next major.** Comment `/retarget v$NEXT_MAJOR` and I will
  repoint this PR at `v$NEXT_MAJOR`, creating that branch off `main` if it does
  not exist yet.
- **Split the compatible part out.** This check reads breaking entries only, so
  the rest of the change can go in now as its own PR.
- **Correct the entry, if it is wrong.** A `-` (Removed) line counts as breaking
  unless it says otherwise, so an entry can end up here by accident — write `-.`
  after the kind and push. See
  [.changelog/README.md](../blob/main/.changelog/README.md#breaking-changes).
