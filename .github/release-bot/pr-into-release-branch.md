### Based on `$BASE_BRANCH` — retarget it onto `$DEFAULT_BRANCH`

This pull request merges into the release branch for **$RELEASE_VERSION**, and
nothing merges into a release branch but me. I have changed nothing here, and
there is nothing wrong with the change — it is pointed at the wrong branch.

**Why that branch is mine**

On every push to `$DEFAULT_BRANCH` I merge it into the open release branch, fold
any new changelog fragments into the `## [$RELEASE_VERSION]` section of
`CHANGELOG.md`, and delete the fragments I consumed. That is what keeps the
release pull request mergeable without anyone editing it. A pull request based
on the release branch sidesteps that three ways:

- **It runs almost no CI.** The test matrix, the compat suite, the changelog
  gate and the breaking-change hold all run on pull requests into
  `$DEFAULT_BRANCH` only. Code merged here would reach a release less checked
  than anything else in the repository.
- **Its fragment is never folded.** I fold when `$DEFAULT_BRANCH` moves into the
  release branch. A fragment that arrives on the branch itself trips nothing,
  rides into `$DEFAULT_BRANCH` with the release commit, and fails the release —
  `Changelog fragments remain in .changelog`.
- **It merges under someone else's review.** Its diff is against the release
  branch, so the code reaches `$DEFAULT_BRANCH` carrying the release pull
  request's approval rather than its own.

**What to do**

Rebase onto `$DEFAULT_BRANCH`, dropping the release commits this branch picked
up, and point the pull request there:

```sh
git rebase --onto origin/$DEFAULT_BRANCH origin/$BASE_BRANCH
git push --force-with-lease
gh pr edit --base $DEFAULT_BRANCH
```

The push is what clears this check: retargeting on its own leaves it red,
because a base change does not re-trigger the run that set it.

**Keep the changelog fragment exactly as it is.** While the release pull request
is open I fold it into `## [$RELEASE_VERSION]` on the next push to
`$DEFAULT_BRANCH`, so the change still ships in $RELEASE_VERSION if it merges
before the release does. Nothing about being on the wrong branch makes the
fragment wrong.

**If it truly has to be in this release** and cannot go through
`$DEFAULT_BRANCH` as its own pull request, push the commit onto the release
branch itself. It is then part of the release pull request — reviewed with it,
checked by its CI — and its release note is written straight into the
`## [$RELEASE_VERSION]` section rather than left as a fragment. That is rare,
and it should stay rare.
