#!/usr/bin/env bash
# verify-changed.sh — run the checks CI runs, scoped to what this branch changed.
#
# Exists because build+vet+tests is a weaker gate than CI: `Lint` (golangci-lint)
# is its own required job, and web/ has a typecheck that is easy to run in a way
# that silently passes (see web/tsconfig.json). Both have shipped broken commits.
#
# Each check's result is cached against a content fingerprint of that check's own
# inputs, so re-running with nothing touched in between is a no-op instead of
# another few minutes of linting. The fingerprint covers the exact files the
# check reads (honouring .gitignore) *and* the command that was run, so a cache
# hit only ever stands in for a like-for-like run: change a linted file, the
# pinned tool version, or the set of commands, and the check runs again. Passes
# are cached per check, so touching web/ does not invalidate the Go result.
#
# Usage:
#   scripts/verify-changed.sh             run the outstanding checks
#   scripts/verify-changed.sh --force     run them even if the cache says they passed
#   scripts/verify-changed.sh --record go record a pass for `go` (or `web`) that you
#                                         ran yourself — see `make lint-go`
#
# Exit codes: 0 = nothing to do or everything passed, 2 = a check failed.
# 2 is what a Claude Code PreToolUse hook treats as "block", with stderr shown.

set -uo pipefail

root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0

force=0
record=""
while [ $# -gt 0 ]; do
  case $1 in
  --force) force=1 ;;
  --record)
    if [ $# -lt 2 ]; then
      echo "verify-changed: --record needs a check name ('go' or 'web')" >&2
      exit 2
    fi
    record=$2
    shift
    ;;
  *)
    echo "verify-changed: unknown argument: $1" >&2
    exit 2
    ;;
  esac
  shift
done

# ---- Cache -----------------------------------------------------------------
# Lives in the git dir so it is per-worktree and never shows up in `git status`.
cache_file="$(git rev-parse --git-dir)/verify-changed.cache"

_hash() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | cut -d' ' -f1
  else
    return 1 # no hasher: callers see an empty fingerprint and skip the cache
  fi
}

# _fingerprint <salt> <pathspec>... — content hash of one check's inputs.
# Hashes working-tree content rather than the index, so staging state is
# irrelevant, and lists paths with git so .gitignore is honoured for free.
# The salt is the command being cached: it makes a stale cache entry written by
# a different set of checks miss rather than wrongly hit.
_fingerprint() {
  local salt=$1
  shift
  local list deleted
  list=$(git ls-files -c -o --exclude-standard -- "$@" | LC_ALL=C sort -u) || return 0
  deleted=$(git ls-files -d -- "$@")
  if [ -n "$deleted" ]; then
    list=$(printf '%s\n' "$list" | grep -Fxv -f <(printf '%s\n' "$deleted"))
  fi
  [ -z "$list" ] && return 0
  {
    printf '%s\n' "$salt" "$list"
    printf '%s\n' "$list" | git hash-object --stdin-paths
  } | _hash
}

_cache_get() { # <key> — prints the cached fingerprint, if any
  [ -f "$cache_file" ] || return 0
  awk -v k="$1" '$1 == k { print $2 }' "$cache_file"
}

_cache_put() { # <key> <fingerprint>; a blank fingerprint drops the entry
  local tmp="$cache_file.$$"
  {
    if [ -f "$cache_file" ]; then awk -v k="$1" '$1 != k' "$cache_file"; fi
    if [ -n "$2" ]; then printf '%s %s\n' "$1" "$2"; fi
  } >"$tmp" 2>/dev/null
  # Losing the cache only ever costs a re-run, so failures here stay quiet.
  mv -f "$tmp" "$cache_file" 2>/dev/null || rm -f "$tmp"
}

# ---- What each check reads, and what it runs -------------------------------
# `golangci-lint run ./...` covers the root module only, so the nested modules
# under compat/suites/ are not inputs to it.
_golangci_version() {
  local ver
  ver=$(sed -n 's/^GOLANGCI_LINT_VERSION := //p' Makefile)
  printf '%s' "${ver:-v2.8.0}"
}

_fp_go() {
  _fingerprint "golangci-lint $(_golangci_version) run ./..." \
    '*.go' go.mod go.sum .golangci.yml ':(exclude)compat/suites/*'
}

# The build-tag sets CI runs the suite under (test.yml, the `build-tags`
# matrix). Vetting each of them is what makes tag-gated code visible here at
# all: golangci-lint, `go vet ./...` and `go test ./...` all use the default
# build context, so a file behind `//go:build dev` or `nosqlite` is not
# compiled by any of them — a syntax error in one passes every local check and
# fails only in CI.
#
# Setting `build-tags` in .golangci.yml is not the fix. Those files come in
# pairs with `//go:build !dev` halves (registry_dev.go / registry_prod.go and
# friends), so naming the tag drops the other half from analysis: the blind
# spot moves rather than closing.
#
# Vet rather than test: this stays a compile-and-vet gate, matching the
# script's charter of not running the suite. Every set carries `slim`, so none
# of them needs a built web/dist.
_ci_build_tags='slim slim,nosqlite slim,dev'

_fp_tags() {
  _fingerprint "go vet -tags [$_ci_build_tags] ./..." \
    '*.go' go.mod go.sum ':(exclude)compat/suites/*'
}

_fp_web() {
  _fingerprint 'pnpm run typecheck && pnpm run lint' 'web/'
}

if [ -n "$record" ]; then
  case $record in
  go) _cache_put golangci-lint "$(_fp_go)" ;;
  web) _cache_put web "$(_fp_web)" ;;
  *)
    echo "verify-changed: --record takes 'go' or 'web', got: $record" >&2
    exit 2
    ;;
  esac
  exit 0
fi

base=$(git merge-base HEAD origin/main 2>/dev/null) || base=""
if [ -z "$base" ]; then
  # No origin/main to compare against (fresh clone, detached CI checkout).
  # Nothing sensible to scope to, so stay out of the way.
  exit 0
fi

changed=$(git diff --name-only "$base"...HEAD; git diff --name-only; git ls-files --others --exclude-standard)
[ -z "$changed" ] && exit 0

go_changed=$(printf '%s\n' "$changed" | grep -E '\.go$' | head -1)
web_changed=$(printf '%s\n' "$changed" | grep -E '^web/src/|^web/[^/]*\.(ts|json)$' | head -1)

failed=""
skipped=""
cached=""

# ---- Go: golangci-lint -----------------------------------------------------
if [ -n "$go_changed" ]; then
  fp=$(_fp_go)
  if [ "$force" -eq 0 ] && [ -n "$fp" ] && [ "$(_cache_get golangci-lint)" = "$fp" ]; then
    cached="$cached golangci-lint"
  else
    pkg="github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(_golangci_version)"
    go_failed=""
    if command -v golangci-lint >/dev/null 2>&1; then
      golangci-lint run ./... || go_failed=" golangci-lint"
    elif command -v go >/dev/null 2>&1; then
      go run "$pkg" run ./... || go_failed=" golangci-lint"
    elif command -v docker >/dev/null 2>&1 && [ -x scripts/docker-go.sh ]; then
      scripts/docker-go.sh run "$pkg" run ./... || go_failed=" golangci-lint"
    else
      skipped="$skipped golangci-lint(no go/docker)"
      fp="" # never ran, so there is no pass to cache and none to invalidate
    fi
    failed="$failed$go_failed"
    if [ -n "$fp" ]; then
      if [ -z "$go_failed" ]; then _cache_put golangci-lint "$fp"; else _cache_put golangci-lint ""; fi
    fi
  fi
fi

# ---- Go: vet under CI's build tags -----------------------------------------
if [ -n "$go_changed" ]; then
  fp=$(_fp_tags)
  if [ "$force" -eq 0 ] && [ -n "$fp" ] && [ "$(_cache_get vet-tags)" = "$fp" ]; then
    cached="$cached vet-tags"
  else
    tags_failed=""
    for tagset in $_ci_build_tags; do
      if command -v go >/dev/null 2>&1; then
        go vet -tags "$tagset" ./... || tags_failed="$tags_failed vet(-tags $tagset)"
      elif command -v docker >/dev/null 2>&1 && [ -x scripts/docker-go.sh ]; then
        scripts/docker-go.sh vet -tags "$tagset" ./... || tags_failed="$tags_failed vet(-tags $tagset)"
      else
        skipped="$skipped vet-tags(no go/docker)"
        fp="" # never ran, so there is no pass to cache and none to invalidate
        break
      fi
    done
    failed="$failed$tags_failed"
    if [ -n "$fp" ]; then
      if [ -z "$tags_failed" ]; then _cache_put vet-tags "$fp"; else _cache_put vet-tags ""; fi
    fi
  fi
fi

# ---- Web: typecheck + lint -------------------------------------------------
if [ -n "$web_changed" ]; then
  fp=$(_fp_web)
  if [ "$force" -eq 0 ] && [ -n "$fp" ] && [ "$(_cache_get web)" = "$fp" ]; then
    cached="$cached web"
  elif command -v pnpm >/dev/null 2>&1 && [ -d web/node_modules ]; then
    # Never `tsc --noEmit`: web/tsconfig.json is a solution-style config and
    # compiles zero files. `pnpm run typecheck` runs the real projects.
    web_failed=""
    (cd web && pnpm run typecheck) || web_failed="$web_failed pnpm-typecheck"
    (cd web && pnpm run lint) || web_failed="$web_failed pnpm-lint"
    failed="$failed$web_failed"
    if [ -n "$fp" ]; then
      if [ -z "$web_failed" ]; then _cache_put web "$fp"; else _cache_put web ""; fi
    fi
  else
    skipped="$skipped web(no pnpm or node_modules)"
  fi
fi

if [ -n "$cached" ]; then
  echo "verify-changed: already green, nothing changed since:$cached (--force to re-run)" >&2
fi

if [ -n "$skipped" ]; then
  echo "verify-changed: could not run:$skipped — CI will still check these." >&2
fi

if [ -n "$failed" ]; then
  cat >&2 <<EOF
verify-changed: FAILED:$failed

These are required CI jobs. Fix them before pushing, or re-run the push if you
have a reason to override. Full gate: \`make check\` (fmt vet lint test).
EOF
  exit 2
fi

exit 0
