#!/bin/sh
# go.sh — run the Go toolchain, using the native `go` when available and
# falling back to Docker. The two paths produce identical results. Use this
# everywhere instead of `go` directly so the same scripts work on every OS.
#
# Usage:
#   scripts/go.sh test ./internal/state/...       # go test
#   scripts/go.sh test -race ./internal/state/     # any go subcommand
#   scripts/go.sh vet ./...                        # go vet
#   scripts/go.sh run ./cmd/verify                 # go run <pkg>
#   scripts/go.sh shell                            # interactive shell
#
# Docker fallback details: image from OVERCAST_GO_IMAGE (default: the
# devcontainer's, read from .devcontainer/Dockerfile — see lib/go-image.sh);
# module and build caches live in named Docker volumes. git is not usable
# inside the container for worktree checkouts (the .git file points at a host
# path). The fallback is CPU-capped the same way scripts/docker-go.sh is
# (OVERCAST_GO_CPUS / OVERCAST_GO_TEST_P). The native path is not: `docker run
# --cpus` has nothing to bound there, and a host toolchain is the user's own to
# schedule.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && (pwd -W 2>/dev/null || pwd))

if [ "$#" -eq 0 ]; then
    echo "usage: $0 <go-subcommand and args> | shell" >&2
    exit 2
fi

if command -v go >/dev/null 2>&1; then
    if [ "$1" = "shell" ]; then
        echo "go.sh: native go shell — just run bash" >&2
        exit 2
    fi
    exec go "$@"
fi

# ---- Docker fallback ----------------------------------------------------------
# Image resolution lives in lib/go-image.sh, shared with scripts/docker-go.sh.
. "$script_dir/lib/go-image.sh"
IMAGE="$GO_IMAGE"
MOD_CACHE_VOLUME="${OVERCAST_GO_MOD_CACHE:-overcast-go-mod-cache}"
BUILD_CACHE_VOLUME="${OVERCAST_GO_BUILD_CACHE:-overcast-go-build-cache}"

# CPU bound: uncapped, the container takes the whole machine. See
# scripts/docker-go.sh's "CPU bound" section for why all three of --cpus,
# GOMAXPROCS and -p are needed and how the defaults are derived; the code is
# shared in lib/go-cpu-bound.sh.
. "$script_dir/lib/go-cpu-bound.sh"

cpus_flag=""
gomaxprocs_flag=""
if [ "$GO_CPUS" != "0" ]; then
    cpus_flag="--cpus=$GO_CPUS"
    gomaxprocs_flag="--env=GOMAXPROCS=$GO_CPUS"
fi

tty_flags=""
if [ -t 0 ] && [ -t 1 ]; then
    tty_flags="-it"
fi

# MSYS_NO_PATHCONV stops Git Bash from rewriting container paths.
# GIT_CONFIG_GLOBAL + safe.directory fixes "dubious ownership" for
# bind-mounted repos.
run() {
    # Word splitting on the flag variables is deliberate: each holds zero or
    # more whole docker flags, never a path.
    # shellcheck disable=SC2086
    MSYS_NO_PATHCONV=1 docker run --rm $tty_flags $cpus_flag $gomaxprocs_flag \
        -v "$repo_root:/src" \
        -v "$MOD_CACHE_VOLUME:/go/pkg/mod" \
        -v "$BUILD_CACHE_VOLUME:/root/.cache/go-build" \
        -e GOFLAGS=-buildvcs=false \
        -e GIT_CONFIG_GLOBAL=/tmp/gitconfig \
        -w /src \
        "$IMAGE" \
        sh -c "git config --global --add safe.directory /src 2>/dev/null; exec \"\$@\"" -- "$@"
}

if [ "$1" = "shell" ]; then
    run bash
    exit
fi

# Inject the default -p after the `test` subcommand only; `go -C dir test ...`
# is a supported form, so `test` is not always the first argument.
if [ "$GO_TEST_P" != "0" ] && ! has_p_flag "$@"; then
    case "$1" in
    test)
        shift
        set -- test -p "$GO_TEST_P" "$@"
        ;;
    -C)
        if [ "${3:-}" = "test" ]; then
            c_dir="$2"
            shift 3
            set -- -C "$c_dir" test -p "$GO_TEST_P" "$@"
        fi
        ;;
    -C=*)
        if [ "${2:-}" = "test" ]; then
            c_flag="$1"
            shift 2
            set -- "$c_flag" test -p "$GO_TEST_P" "$@"
        fi
        ;;
    esac
fi

run go "$@"
