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
# (the .git file points at a host path).
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

tty_flags=""
if [ -t 0 ] && [ -t 1 ]; then
    tty_flags="-it"
fi

# MSYS_NO_PATHCONV stops Git Bash from rewriting container paths.
# GIT_CONFIG_GLOBAL + safe.directory fixes "dubious ownership" for
# bind-mounted repos.
run() {
    MSYS_NO_PATHCONV=1 docker run --rm $tty_flags \
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
else
    run go "$@"
fi
