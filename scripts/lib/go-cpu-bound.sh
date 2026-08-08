# shellcheck shell=sh
# go-cpu-bound.sh — the CPU bound shared by every Go-in-Docker entry point.
#
# Source this, never execute it. It defines detect_cpus and has_p_flag, then
# sets two variables from OVERCAST_GO_CPUS / OVERCAST_GO_TEST_P:
#
#   GO_CPUS    what the container may consume, and the GOMAXPROCS to match.
#              "0" means no bound — the pre-cap behaviour.
#   GO_TEST_P  the `go test -p` to use. "0" means never inject one.
#
# Why all three of --cpus, GOMAXPROCS and -p are needed, and why the numbers are
# derived rather than hardcoded, is written out once in scripts/docker-go.sh's
# "CPU bound" section; CONTRIBUTING.md § "No local Go toolchain?" has the
# measurements. Consumers: scripts/docker-go.sh, scripts/go.sh,
# scripts/container-test.sh. The PowerShell twin is lib/go-cpu-bound.ps1 — the
# two must stay behaviorally identical.

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
