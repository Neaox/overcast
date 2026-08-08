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
#   - CPU use is capped to half the available cores so a long test run leaves
#     the machine usable. Override with OVERCAST_GO_CPUS / OVERCAST_GO_TEST_P;
#     OVERCAST_GO_CPUS=0 restores the old unbounded behaviour. See the CPU
#     bound section below.
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

# ---- CPU bound ---------------------------------------------------------------
#
# Left alone the container helps itself to the whole machine: a `go test`
# through this script peaked at 2269% CPU — 22.7 of 24 cores — and held the box
# there until it finished. (Measurement conditions in CONTRIBUTING.md § "No
# local Go toolchain?".) Three knobs are needed, because each bounds something
# different:
#
#   --cpus      caps what the container may consume. Nothing else does.
#   GOMAXPROCS  must be set explicitly. Container-aware GOMAXPROCS — the runtime
#               reading the cgroup CPU quota — arrived in Go 1.25, and this image
#               is golang:1.24-bookworm, whose runtime still sees every host
#               core. Without it the container spawns host-wide parallelism into
#               a fraction of a machine and thrashes on context switches instead
#               of running slower cleanly. Setting it stays correct on 1.25+: an
#               explicit GOMAXPROCS just disables the automatic default in favour
#               of the same number.
#   -p          bounds concurrent test *binaries*, and defaults to GOMAXPROCS.
#               Each binary inherits GOMAXPROCS from the environment, so leaving
#               -p at its default puts cpus × cpus runnable Ps into a cpus-CPU
#               quota. Halving again keeps the quota busy without that
#               multiplication.
#
# Defaults are derived from the detected core count, never hardcoded:
# `docker run --cpus=N` is rejected outright when N exceeds the CPUs the daemon
# reports, so a fixed 8 would break every run on a 4-core machine. Half the
# cores for --cpus/GOMAXPROCS is fast enough to be worth using and quiet enough
# that the machine stays usable while a suite runs; a quarter for -p follows
# from the multiplication above. Both clamp to at least 1.
#
# Overrides: OVERCAST_GO_CPUS (0 = no cap, no GOMAXPROCS and no -p — the old
# behaviour), OVERCAST_GO_TEST_P (0 = never inject -p).

# detect_cpus — how many CPUs the container may be given. The Docker daemon's
# count comes first: that is the number --cpus is validated against, and on
# Docker Desktop the VM can have fewer CPUs than the host. Host detection is the
# fallback — nproc (Linux, Git Bash), sysctl (macOS), NUMBER_OF_PROCESSORS
# (Windows) — and a conservative 2 if nothing answers. Costs one `docker info`
# per invocation (~0.5s), which is small next to starting the container; set
# OVERCAST_GO_CPUS and OVERCAST_GO_TEST_P to skip it entirely.
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
    # Explicit opt-out: no cap, and no -p either, unless -p was asked for.
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

# MSYS_NO_PATHCONV stops Git Bash from rewriting container paths like /src
# into host paths. Harmless elsewhere.
# GOFLAGS=-buildvcs=false: the bind-mounted repo is owned by a different
# uid than the container's root, so git refuses to read it ("dubious
# ownership") and `go build` of main packages fails trying to stamp VCS
# info. Nothing built through this script needs VCS stamping.
run() {
    # Word splitting on the flag variables is deliberate: each holds zero or
    # more whole docker flags, never a path.
    # shellcheck disable=SC2086
    MSYS_NO_PATHCONV=1 docker run --rm $tty_flags $cpus_flag $gomaxprocs_flag \
        -v "$repo_root:/src" \
        -v "$MOD_CACHE_VOLUME:/go/pkg/mod" \
        -v "$BUILD_CACHE_VOLUME:/root/.cache/go-build" \
        -e GOFLAGS=-buildvcs=false \
        -w /src \
        "$IMAGE" "$@"
}

# has_p_flag — did the caller pass their own -p? Silently overriding an explicit
# flag would be worse than not capping at all.
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

# Inject the default -p directly after the `test` subcommand. Only for `test`:
# it is a build flag other subcommands accept too, but this cap is about test
# binaries. `go -C dir test ...` is a supported form (compat/AGENTS.md uses it),
# so `test` is not always the first argument.
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
