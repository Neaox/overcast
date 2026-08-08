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
# Docker fallback details: image from OVERCAST_GO_IMAGE (default
# golang:1.24-bookworm); module and build caches live in named Docker
# volumes. git is not usable inside the container for worktree checkouts
# (the .git file points at a host path). The fallback is CPU-capped the same
# way scripts/docker-go.sh is (OVERCAST_GO_CPUS / OVERCAST_GO_TEST_P). The
# native path is not: `docker run --cpus` has nothing to bound there, and a
# host toolchain is the user's own to schedule.
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
IMAGE="${OVERCAST_GO_IMAGE:-golang:1.24-bookworm}"
MOD_CACHE_VOLUME="${OVERCAST_GO_MOD_CACHE:-overcast-go-mod-cache}"
BUILD_CACHE_VOLUME="${OVERCAST_GO_BUILD_CACHE:-overcast-go-build-cache}"

# CPU bound: uncapped, the container takes the whole machine. See
# scripts/docker-go.sh's "CPU bound" section for why all three of --cpus,
# GOMAXPROCS and -p are needed and how the defaults are derived.
detect_cpus() {
    for candidate in \
        "$(docker info --format '{{.NCPU}}' 2>/dev/null || true)" \
        "$(nproc 2>/dev/null || true)" \
        "$(sysctl -n hw.ncpu 2>/dev/null || true)" \
        "${NUMBER_OF_PROCESSORS:-}"; do
        case "$candidate" in
        '' | *[!0-9]*) continue ;;
        esac
        if [ "$candidate" -ge 1 ]; then
            echo "$candidate"
            return 0
        fi
    done
    echo 2
}

GO_CPUS="${OVERCAST_GO_CPUS:-}"
GO_TEST_P="${OVERCAST_GO_TEST_P:-}"
if [ "$GO_CPUS" = "0" ]; then
    GO_TEST_P="${OVERCAST_GO_TEST_P:-0}"
elif [ -z "$GO_CPUS" ] || [ -z "$GO_TEST_P" ]; then
    cpu_total=$(detect_cpus)
    if [ -z "$GO_CPUS" ]; then
        GO_CPUS=$((cpu_total / 2))
        [ "$GO_CPUS" -ge 1 ] || GO_CPUS=1
    fi
    if [ -z "$GO_TEST_P" ]; then
        GO_TEST_P=$((cpu_total / 4))
        [ "$GO_TEST_P" -ge 1 ] || GO_TEST_P=1
    fi
fi

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

# has_p_flag — did the caller pass their own -p? Never override an explicit flag.
has_p_flag() {
    for arg in "$@"; do
        case "$arg" in
        -p | -p=* | --p | --p=*) return 0 ;;
        esac
    done
    return 1
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
