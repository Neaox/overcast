#!/usr/bin/env bash
# release-candidate-check.sh — is the checked-out VERSION an unreleased version?
#
# Prints "true" when no tag v<VERSION> exists on origin, "false" otherwise.
# This is the release-candidate predicate for CI: a PR is a release candidate
# exactly when the version it carries has not shipped. That covers both the PR
# that bumps VERSION (a new version has no tag yet) and a follow-up PR after a
# release workflow failed (VERSION already on main, still untagged) — where a
# branch-name convention like release/* would miss the second case entirely.
#
# Never exits non-zero: CI jobs hang skip-cascades off this answer, and an
# infrastructure hiccup must degrade to "not a candidate" with a warning, not
# take the build graph down. The next push simply re-evaluates.
set -u

version=$(tr -d '[:space:]' < VERSION 2>/dev/null || true)
if [ -z "$version" ]; then
  echo "::warning::release-candidate-check: VERSION file missing or empty; assuming not a release candidate" >&2
  echo "false"
  exit 0
fi

tag="v$version"
git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1
case $? in
  0) echo "false" ;; # tag exists: this version already shipped
  2) echo "true" ;;  # tag absent: unreleased — this build is a candidate
  *)
    echo "::warning::release-candidate-check: could not query origin tags for $tag; assuming not a release candidate" >&2
    echo "false"
    ;;
esac
exit 0
