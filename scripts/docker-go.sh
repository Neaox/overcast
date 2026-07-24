#!/bin/sh
# docker-go.sh — run Go toolchain commands in a Docker container, for
# development on machines without a host Go install (e.g. Windows outside the
# devcontainer). Works from Git Bash, macOS, and Linux; PowerShell users can
# use scripts/docker-go.ps1, which behaves identically.
#
# Usage:
#   scripts/docker-go.sh test ./internal/state/...        # go test
#   scripts/docker-go.sh test -race -count=10 ./internal/state/
#   scripts/docker-go.sh vet ./...                        # go vet
#   scripts/docker-go.sh build ./...                      # go build
#   scripts/docker-go.sh version                          # any go subcommand
#   scripts/docker-go.sh shell                            # interactive shell
#
# Details:
#   - Image defaults to the devcontainer's Go image; override with
#     OVERCAST_GO_IMAGE.
#   - Module and build caches live in named Docker volumes (the mod cache is
#     shared with the devcontainer), so repeated runs are fast.
#   - The repo is bind-mounted at /src. On Windows/macOS the first build is
#     slower than a native one because source reads cross the VM file-sharing
#     boundary; the build cache makes subsequent runs cheap. Tests that write
#     to t.TempDir() use the container's native filesystem, so timing-sensitive
#     tests are not distorted by the mount.
#   - git is not usable inside the container for worktree checkouts (the
#     .git file points at a host path); plain go commands don't need it.
#   - On native Linux the container runs as root, so files it creates in the
#     repo (rare for go test/vet/build) are root-owned; prefer a host Go
#     toolchain there, or chown afterwards.

set -eu

IMAGE="${OVERCAST_GO_IMAGE:-golang:1.24-bookworm}"
MOD_CACHE_VOLUME="${OVERCAST_GO_MOD_CACHE:-overcast-go-mod-cache}"
BUILD_CACHE_VOLUME="${OVERCAST_GO_BUILD_CACHE:-overcast-go-build-cache}"

# Repo root = parent of this script's directory. On Git Bash, pwd -W yields a
# Windows-style path that Docker Desktop accepts; elsewhere plain pwd is fine.
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && (pwd -W 2>/dev/null || pwd))

if [ "$#" -eq 0 ]; then
    echo "usage: $0 <go-subcommand and args> | shell" >&2
    exit 2
fi

tty_flags=""
if [ -t 0 ] && [ -t 1 ]; then
    tty_flags="-it"
fi

# MSYS_NO_PATHCONV stops Git Bash from rewriting container paths like /src
# into host paths. Harmless elsewhere.
# GOFLAGS=-buildvcs=false: the bind-mounted repo is owned by a different
# uid than the container's root, so git refuses to read it ("dubious
# ownership") and `go build` of main packages fails trying to stamp VCS
# info. Nothing built through this script needs VCS stamping.
run() {
    MSYS_NO_PATHCONV=1 docker run --rm $tty_flags \
        -v "$repo_root:/src" \
        -v "$MOD_CACHE_VOLUME:/go/pkg/mod" \
        -v "$BUILD_CACHE_VOLUME:/root/.cache/go-build" \
        -e GOFLAGS=-buildvcs=false \
        -w /src \
        "$IMAGE" "$@"
}

if [ "$1" = "shell" ]; then
    run bash
else
    run go "$@"
fi
