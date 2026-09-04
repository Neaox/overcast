#!/bin/sh
# pr-merge-base.sh — the commit a PR's own changes should be diffed against.
#
# Prints `git merge-base <base-ref> HEAD`: the point HEAD (a PR branch, or
# GitHub's refs/pull/N/merge test-merge of it onto the base) actually
# diverged from <base-ref>. Pass the base branch's *live* tip — e.g.
# `origin/main` after a full (`fetch-depth: 0`) checkout — never a sha out of
# a `pull_request` event payload's `base.sha`.
#
# That sha is a snapshot of the base branch taken when the event fired, and it
# does not move: a workflow re-run replays the very same payload no matter how
# far the base branch has moved since, or how many other PRs merged to it in
# the meantime. A three-dot diff from a stale base.sha therefore mixes those
# PRs' files into what should be a diff scoped to this PR's own commits (see
# .github/workflows/changelog-required.yml and the issue that traced this:
# https://github.com/overcast-sh/overcast/issues/813). Recomputing the merge
# base against the live branch instead tracks wherever the base has actually
# moved to by the time this runs, on both checkout shapes CI can produce:
#
#   * refs/pull/N/merge (the default `pull_request` checkout): a synthetic
#     merge commit whose first parent already *is* the base's current tip, so
#     the merge base lands exactly there.
#   * a plain head-ref checkout (no merge preview, e.g. a conflicted PR): the
#     merge base is the true point this branch forked from the base, which is
#     the same answer the three-dot diff was trying, and failing, to give.
#
# Never `HEAD^1`: that also names the base tip, but only on the first shape
# above. On the second it is just this branch's own previous commit, so it
# would silently answer the wrong question the day the checkout shape changes.
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: pr-merge-base.sh <base-ref>" >&2
  exit 2
fi

git merge-base "$1" HEAD
