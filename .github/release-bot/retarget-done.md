### Retargeted to `$TARGET_BRANCH`

- Base: `$PREVIOUS_BASE` → **`$TARGET_BRANCH`**$CREATED_NOTE
- Asked for by @$REQUESTED_BY

The hold no longer applies. A breaking change is exactly what a next-major
branch is for, so this PR is free to merge there whenever it is ready.

**Read the diff before you do.** Repointing a PR recomputes its merge base: if
`$TARGET_BRANCH` has moved on from `main` since it was branched, the diff may
show commits that are not yours, or conflict. Rebase onto `$TARGET_BRANCH` if
it does.

Point it back with `/retarget $PREVIOUS_BASE`.
