#!/bin/sh
# image-tag.sh — print the Docker tag this checkout's images should be built
# under. The PowerShell twin is image-tag.ps1; the two must stay behaviorally
# identical, and scripts/image_tag_test.py drives both.
#
# Why a per-branch tag. `make docker-console` used to hardcode
# `docker build -t overcast:dev .`, so every worktree on the machine built into
# one tag. Two agents working in parallel would overwrite each other's image
# between the build and the `docker run`, and the failure is silent: the
# container starts, serves somebody else's branch, and the screenshot or the
# manual test that follows is evidence about the wrong code. A tag derived from
# the branch gives each worktree its own image and makes the mix-up impossible
# rather than unlikely.
#
# Usage:
#   scripts/image-tag.sh              # sanitised current branch name
#   OVERCAST_IMAGE_TAG=foo scripts/image-tag.sh   # explicit override, validated
#
# What each case becomes:
#   main                                  -> main
#   claude/handover-documentation-b7930d  -> claude-handover-documentation-b7930d
#   Feature/ADD-Thing                     -> feature-add-thing
#   detached HEAD at abc123def456         -> detached-abc123def456
#   not a git repo / no commits yet       -> local
#
# Sanitising rules, from Docker's own grammar for a tag ([\w][\w.-]{0,127}):
# everything outside [a-z0-9._-] becomes '-', a leading '.' or '-' is dropped
# (Docker forbids both), and the result is cut to 128 characters. The
# lowercasing is stricter than Docker requires — a *tag* may contain uppercase,
# only a *repository name* may not — and is done anyway for two reasons: the
# same string gets pasted into a repository name sooner or later, and two
# branches differing only in case would otherwise produce two images that are
# indistinguishable in `docker images`.
set -eu

sanitise() {
    printf '%s' "$1" |
        tr '[:upper:]' '[:lower:]' |
        sed -e 's/[^a-z0-9._-]/-/g' -e 's/^[.-]*//' |
        cut -c1-128
}

if [ -n "${OVERCAST_IMAGE_TAG:-}" ]; then
    # An explicit tag is validated, never quietly rewritten: a caller who asked
    # for one thing and silently got another has no way to notice.
    case "$OVERCAST_IMAGE_TAG" in
    [!A-Za-z0-9_]* | *[!A-Za-z0-9._-]*)
        echo "image-tag.sh: OVERCAST_IMAGE_TAG='$OVERCAST_IMAGE_TAG' is not a valid Docker tag." >&2
        echo "  A tag matches [A-Za-z0-9_][A-Za-z0-9._-]{0,127}." >&2
        exit 2
        ;;
    esac
    printf '%s\n' "$OVERCAST_IMAGE_TAG"
    exit 0
fi

# symbolic-ref rather than `rev-parse --abbrev-ref HEAD`: the latter prints the
# literal string "HEAD" on a detached checkout, which would sanitise to a
# perfectly valid tag that every detached worktree on the machine shares — the
# exact collision this script exists to prevent, wearing a disguise.
branch=$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)
if [ -n "$branch" ]; then
    tag=$(sanitise "$branch")
else
    sha=$(git rev-parse --short=12 HEAD 2>/dev/null || true)
    tag=$([ -n "$sha" ] && sanitise "detached-$sha" || echo "")
fi

[ -n "$tag" ] || tag=local
printf '%s\n' "$tag"
