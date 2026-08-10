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
# The scoped test run below is the one exception: it is not fingerprinted and
# not cached, so it runs every time a .go file changed.
#
# Usage:
#   scripts/verify-changed.sh             run the outstanding checks
#   scripts/verify-changed.sh --force     run them even if the cache says they passed
#   scripts/verify-changed.sh --record go record a pass for `go` (or `web`) that you
#                                         ran yourself — see `make lint-go`
#
# Exit codes: 0 = nothing to do or everything passed, 2 = a check failed.
# 2 is what the Claude Code and Codex PreToolUse hooks treat as "block", with stderr shown.

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
# matrix). Compiling under each of them is what makes tag-gated code visible
# here at all: golangci-lint, `go vet ./...` and `go test ./...` all use the
# default build context, so a file behind `//go:build dev` or `nosqlite` is not
# compiled by any of them — a syntax error in one passes every local check and
# fails only in CI.
#
# Setting `build-tags` in .golangci.yml is not the fix. Those files come in
# pairs with `//go:build !dev` halves (registry_dev.go / registry_prod.go and
# friends), so naming the tag drops the other half from analysis: the blind
# spot moves rather than closing.
#
# `go test -run='^$'` rather than `go vet`: it compiles the test files too,
# which vet does not, so an undefined helper or a stale signature in a
# `_test.go` behind one of these tags is caught here instead of in CI. No test
# actually runs — the pattern matches no name — but this is not a cheap
# substitution: building the test binaries costs about 3.8x what vetting them
# did (12.1s -> 46.0s per set on a 5900X, so roughly 140s for the three rather
# than 36s), and it is most of what a Go push spends its time on. Every set
# carries `slim`, so none of them needs a built web/dist.
_ci_build_tags='slim slim,nosqlite slim,dev'

_fp_tags() {
  _fingerprint "go test -run _ -tags [$_ci_build_tags] ./..." \
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

# ---- CPU bound, native path ------------------------------------------------
# scripts/go.sh caps only its Docker fallback, reasoning that a host toolchain
# is the user's own to schedule. That left this gate unbounded as soon as a host
# Go existed: three full-repo test-compiles default to -p=GOMAXPROCS=NumCPU and
# saturate every core for the duration. Honour the same two knobs the Docker
# entry points use, so a machine that wants the bound can ask for it.
#
# Unset means uncapped — the behaviour every other machine has today. This only
# ever does something when someone opts in, so it changes nobody's gate by
# default. "0" is the explicit no-cap spelling, matching lib/go-cpu-bound.sh.
go_test_p=""
if [ -n "${OVERCAST_GO_CPUS:-}" ] && [ "${OVERCAST_GO_CPUS}" != "0" ]; then
  export GOMAXPROCS="$OVERCAST_GO_CPUS"
fi
if [ -n "${OVERCAST_GO_TEST_P:-}" ] && [ "${OVERCAST_GO_TEST_P}" != "0" ]; then
  go_test_p="-p $OVERCAST_GO_TEST_P"
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
# The two checks below reach the root module only: `golangci-lint run ./...`
# and `go test ./...` both stop at a nested go.mod, and both fingerprints
# already exclude compat/suites/ to say so. Scheduling them for a change
# confined to a suite would lint and re-compile code that change cannot have
# touched, and then report "already green" against a fingerprint that never
# moved — a cache hit standing in for a run that was never relevant.
root_go_changed=$(printf '%s\n' "$changed" | grep -E '\.go$' | grep -Ev '^compat/suites/' | head -1)
web_changed=$(printf '%s\n' "$changed" | grep -E '^web/src/|^web/[^/]*\.(ts|json)$' | head -1)

failed=""
skipped=""
cached=""

# ---- Go: golangci-lint -----------------------------------------------------
if [ -n "$root_go_changed" ]; then
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

# ---- Go: compile packages and tests under CI's build tags ------------------
if [ -n "$root_go_changed" ]; then
  fp=$(_fp_tags)
  if [ "$force" -eq 0 ] && [ -n "$fp" ] && [ "$(_cache_get vet-tags)" = "$fp" ]; then
    cached="$cached vet-tags"
  else
    tags_failed=""
    for tagset in $_ci_build_tags; do
      if command -v go >/dev/null 2>&1; then
        # shellcheck disable=SC2086 # $go_test_p is a whole flag pair or empty
        go test -run='^$' -count=1 $go_test_p -tags "$tagset" ./... || tags_failed="$tags_failed test-compile(-tags $tagset)"
      elif command -v docker >/dev/null 2>&1 && [ -x scripts/docker-go.sh ]; then
        scripts/docker-go.sh test -run='^$' -count=1 -tags "$tagset" ./... || tags_failed="$tags_failed test-compile(-tags $tagset)"
      else
        skipped="$skipped test-compile(no go/docker)"
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

# ---- Go: run the tests of every module this branch touched ------------------
# One `go test ./...` per owning module, from inside that module.
#
# The repository is not one module. compat/suites/{go-sdk,cli,cdk} each carry
# their own go.mod, and a root-module invocation matches nothing inside them:
# this step used to hand `go test` a path like `compat/suites/go-sdk/...`,
# which the root module answers with "no packages to test" and a non-zero exit.
# So touching a compat suite failed the gate instead of running that suite's
# own tests — and those tests are the ones that stop a compat run reporting a
# result for a test that never executed (impl-key resolution, duplicate
# registration, dependency ordering within a group). CI missed them for the
# same reason; test.yml's "Compat suite unit tests" job is the other half.
#
# `go -C dir` rather than a subshell `cd`: it is the form scripts/docker-go.sh
# also understands — it injects the -p cap after the subcommand for
# `-C dir test` — so the Docker fallback stays equivalent to the host path.
if [ -n "$go_changed" ]; then
  mod_dirs=$(for f in $(printf '%s\n' "$changed" | grep '\.go$'); do
    d=$(dirname "$f")
    if [ -f "$d" ]; then d="."; fi
    while [ ! -f "$d/go.mod" ] && [ "$d" != "." ]; do d=$(dirname "$d"); done
    [ -f "$d/go.mod" ] && printf '%s\n' "$d"
  done | sort -u)
  for mod in $mod_dirs; do
    label=$mod
    [ "$label" = "." ] && label="root"
    # Only the default tag set: the tag pass above already compiled every
    # package of the root module, and its tests, under all three sets, so what
    # is left here is whether they pass. The suite modules declare no build
    # tags at all, which makes `-tags slim` a no-op there rather than a
    # different build.
    if command -v go >/dev/null 2>&1; then
      # shellcheck disable=SC2086 # $go_test_p is a whole flag pair or empty
      go -C "$mod" test -count=1 $go_test_p -tags slim ./... || failed="$failed test-run($label)"
    elif command -v docker >/dev/null 2>&1 && [ -x scripts/docker-go.sh ]; then
      scripts/docker-go.sh -C "$mod" test -count=1 -tags slim ./... || failed="$failed test-run($label)"
    else
      skipped="$skipped test-run(no go/docker)"
      break
    fi
  done
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
