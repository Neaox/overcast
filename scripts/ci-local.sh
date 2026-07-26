#!/usr/bin/env bash
# ci-local.sh — run the CI pipeline locally, in dependency order, without a
# host Go toolchain.
#
# Mirrors .github/workflows/test.yml, including the ordering CI encodes with
# `needs: web` + the web-dist artifact:
#
#   docs-index → web lint → typecheck → vitest → SPA build → BFF build
#              → go vet → go build → go test
#
# Two hard dependencies drive that order:
#   - web/src/docs-index.gen.ts and internal/docssearch/index.gen.go are
#     gitignored build artifacts; the web typecheck and the Go build both
#     import them, so the docs index goes first.
#   - embed.go has `//go:embed all:web/dist`, so every non-slim Go build, vet
#     and test needs a built SPA. The web stages run before the Go stages.
#
# Every Go command goes through scripts/docker-go.sh, so this works on a
# machine with Docker but no Go installed (e.g. Windows outside the
# devcontainer). Pass --host-go to use a host `go` instead.
#
# Usage:
#   bash scripts/ci-local.sh                # full pipeline (web + Go)
#   bash scripts/ci-local.sh --web-only     # skip vet/build/go-test
#   bash scripts/ci-local.sh --go-only      # skip web stages (needs web/dist)
#   bash scripts/ci-local.sh --full         # Go tests: -race over ./... (slow)
#   bash scripts/ci-local.sh --host-go      # use host `go`, not Docker
#   bash scripts/ci-local.sh --no-lock      # allow a concurrent run (see below)
#
# Only one run per worktree at a time. Concurrent runs share web/dist, the
# generated docs index and node_modules/.vite, so they corrupt each other and
# produce failures belonging to neither caller. A second run refuses to start
# and names the one already going. --no-lock opts out; you almost never want it.
#
# Environment equivalents (flags win):
#   OVERCAST_CI_SCOPE=go|web|all   same as --go-only / --web-only
#   OVERCAST_CI_FULL=1             same as --full
#   OVERCAST_CI_HOST_GO=1          same as --host-go
#   OVERCAST_CI_NO_LOCK=1          same as --no-lock
#
# Node dependencies are never installed for you: run `npm ci` in web/ once.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)

scope="${OVERCAST_CI_SCOPE:-all}"
full="${OVERCAST_CI_FULL:-0}"
host_go="${OVERCAST_CI_HOST_GO:-0}"
no_lock="${OVERCAST_CI_NO_LOCK:-0}"

# Print the header comment block (line 2 up to the first non-comment line).
usage() {
    awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$0"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --go-only) scope=go ;;
        --web-only) scope=web ;;
        --all) scope=all ;;
        --full) full=1 ;;
        --host-go) host_go=1 ;;
        --no-lock) no_lock=1 ;;
        -h | --help)
            usage
            exit 0
            ;;
        *)
            echo "ci-local: unknown option '$1' (try --help)" >&2
            exit 2
            ;;
    esac
    shift
done

case "$scope" in
    go | web | all) ;;
    *)
        echo "ci-local: invalid scope '$scope' (expected go, web, or all)" >&2
        exit 2
        ;;
esac

# ─── single-run lock ─────────────────────────────────────────────────────────
#
# The lock lives in the git dir rather than the working tree, so it is
# per-worktree (each linked worktree has its own) and can never show up in
# `git status` or be committed by accident.
#
# `mkdir` is the lock primitive, not flock: it is atomic on every filesystem we
# support and needs no flock binary, which Git Bash on Windows does not
# reliably provide. The PID goes in a file inside the directory so a crashed
# run leaves something we can identify rather than a lock nobody can clear.

lock_dir=""

release_lock() {
    [ -n "$lock_dir" ] && rm -rf "$lock_dir"
}

# pid_alive <pid> — portable liveness check. Git Bash reports MSYS PIDs, which
# is what $$ gives us there too, so kill -0 compares like with like.
pid_alive() {
    kill -0 "$1" 2>/dev/null
}

acquire_lock() {
    local git_dir stale_pid stale_started
    git_dir=$(cd "$ROOT" && git rev-parse --git-dir 2>/dev/null) || return 0
    lock_dir="$(cd "$ROOT" && cd "$git_dir" && pwd)/ci-local.lock"

    if ! mkdir "$lock_dir" 2>/dev/null; then
        stale_pid=$(cat "$lock_dir/pid" 2>/dev/null || echo "")
        stale_started=$(cat "$lock_dir/started" 2>/dev/null || echo unknown)
        if [ -n "$stale_pid" ] && pid_alive "$stale_pid"; then
            lock_dir=""  # not ours — the trap must not delete it
            echo "ci-local: another run is already active in this worktree" >&2
            echo "  pid:     $stale_pid" >&2
            echo "  started: $stale_started" >&2
            echo "" >&2
            echo "  Concurrent runs share web/dist, the generated docs index and" >&2
            echo "  node_modules/.vite, so they corrupt each other. Wait for that run," >&2
            echo "  or pass --no-lock if you are certain they cannot overlap." >&2
            exit 1
        fi
        # The holder is gone — a crash or a kill. Reclaim it.
        echo "ci-local: clearing stale lock from pid ${stale_pid:-unknown}"
        rm -rf "$lock_dir"
        mkdir "$lock_dir" 2>/dev/null || die "could not create lock at $lock_dir"
    fi

    echo "$$" > "$lock_dir/pid"
    date -u +"%Y-%m-%dT%H:%M:%SZ" > "$lock_dir/started"
    trap release_lock EXIT INT TERM
}

if [ "$no_lock" != "1" ]; then
    acquire_lock
fi

# ─── stage plumbing ──────────────────────────────────────────────────────────
#
# Every stage command is written `<cmd> || fail`. That is deliberate rather
# than a `trap … ERR`: without `set -E` an ERR trap never fires for a failure
# inside a shell function, and with `set -E` it fires twice for a subshell.
# An explicit `|| fail` reports the stage exactly once, everywhere.

current_stage="startup"
started_at=$(date +%s)

stage() {
    current_stage="$1"
    echo ""
    echo "── ${current_stage} ──────────────────────────────────────────"
}

fail() {
    echo "" >&2
    echo "✗ ci-local FAILED in stage: ${current_stage}" >&2
    echo "  fix that stage, then re-run (see --help for narrower scopes)." >&2
    exit 1
}

die() {
    echo "ci-local: $1" >&2
    exit 1
}

# go_cmd <go-subcommand> [args...] — host go, or Docker via docker-go.sh.
go_cmd() {
    if [ "$host_go" = "1" ]; then
        (cd "$ROOT" && go "$@")
    else
        bash "$ROOT/scripts/docker-go.sh" "$@"
    fi
}

# web_cmd <command> [args...] — run in web/.
web_cmd() {
    (cd "$ROOT/web" && "$@")
}

# ─── preflight ───────────────────────────────────────────────────────────────

stage "preflight"
# Go is required in every scope — --web-only still generates the docs index.
if [ "$host_go" = "1" ]; then
    command -v go >/dev/null 2>&1 || die "--host-go given but 'go' is not on PATH"
    echo "go: $(go version)"
else
    command -v docker >/dev/null 2>&1 || die "docker is required (or pass --host-go)"
    docker info >/dev/null 2>&1 || die "docker is installed but not running"
    echo "go: via scripts/docker-go.sh (no host toolchain needed)"
fi

if [ "$scope" != "go" ]; then
    command -v npm >/dev/null 2>&1 || die "npm is required for the web stages (or pass --go-only)"
    [ -d "$ROOT/web/node_modules" ] ||
        die "web/node_modules is missing — run 'npm ci' in web/ first"
    echo "node: $(node --version)"
fi

# --go-only skips the SPA build, but embed.go still needs its output.
if [ "$scope" = "go" ] && [ ! -d "$ROOT/web/dist" ]; then
    die "web/dist is missing and --go-only skips the SPA build.
  embed.go has '//go:embed all:web/dist', so go vet/build/test cannot run
  without it. Run 'make ci-local-web' (or 'make build-web') first."
fi

# ─── docs index (must precede every other stage) ─────────────────────────────

stage "docs-index"
go_cmd run ./scripts/docs-index.go --write-index --write-go-index || fail

# ─── Web (produces web/dist, which the Go stages embed) ──────────────────────

if [ "$scope" != "go" ]; then
    stage "web-lint"
    web_cmd npm run lint || fail

    # Not `npx tsc --noEmit`: web/tsconfig.json is a solution-style config with
    # "files": [] and only project references, so it compiles zero files and
    # always passes. npm run typecheck covers app + node projects explicitly.
    stage "web-typecheck"
    web_cmd npm run typecheck || fail

    stage "web-test"
    web_cmd npm run test || fail

    # `npm run build` is deliberately not used here: its docs:index prestep
    # shells out to a host `go`, which this script exists to avoid. The
    # docs-index stage above already produced those artifacts through
    # docker-go.sh, and typecheck already ran as its own stage.
    stage "web-build"
    web_cmd env VITE_BUNDLED=true npx vite build || fail

    stage "web-build-server"
    web_cmd npm run build:server || fail
fi

# ─── Go ──────────────────────────────────────────────────────────────────────

if [ "$scope" != "web" ]; then
    stage "go-vet"
    go_cmd vet ./... || fail

    stage "go-build"
    go_cmd build ./... || fail

    if [ "$full" = "1" ]; then
        stage "go-test (full: -race, ./...)"
        go_cmd test -race -count=1 -timeout=1200s ./... || fail
    else
        stage "go-test (unit: ./internal/...)"
        go_cmd test -count=1 -timeout=900s ./internal/... || fail
    fi
fi

# ─── done ────────────────────────────────────────────────────────────────────

elapsed=$(($(date +%s) - started_at))
echo ""
echo "✓ ci-local passed (scope=${scope}, ${elapsed}s)"
