#!/bin/sh
# container-test.sh — run the Go test suite inside docker-compose.dev.yml's
# `test` service, CPU-capped the same way scripts/docker-go.sh is.
#
# Usage:
#   scripts/container-test.sh                                      # whole suite
#   scripts/container-test.sh -race -count=1 -timeout=900s ./internal/...
#
# With no arguments the service's own default command runs. With arguments it
# runs `go test <arguments>` instead.
#
# Why a wrapper rather than a static `cpus:` in the Compose file: the cap has to
# be derived from the host at run time (`docker run --cpus=N` is rejected when N
# exceeds the CPUs the daemon reports, so a hardcoded number breaks smaller
# machines), a Compose file can only carry a literal or a ${VAR}, and
# `docker compose run` has no --cpus flag of its own. So this computes the
# numbers and exports them for Compose to substitute. The service's comments in
# docker-compose.dev.yml explain what each one bounds.
#
# PowerShell twin: scripts/container-test.ps1. Keep the two in step.

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

. "$script_dir/lib/go-cpu-bound.sh"

# OVERCAST_GO_CPUS drives the service's `cpus:` and GOMAXPROCS;
# OVERCAST_GO_TEST_P drives GOFLAGS=-p=N. GO_TEST_P spells "no -p" as 0, but
# `go test -p 0` is an error, so Compose is handed an empty value instead —
# ${VAR:+...} then expands to nothing.
export OVERCAST_GO_CPUS="$GO_CPUS"
if [ "$GO_TEST_P" = "0" ]; then
    export OVERCAST_GO_TEST_P=""
else
    export OVERCAST_GO_TEST_P="$GO_TEST_P"
fi

# Run from the repo root so the Compose project name matches what
# `docker compose -f docker-compose.dev.yml ...` produces when a contributor
# runs it by hand. A relative -f also sidesteps Git Bash's path rewriting.
cd "$script_dir/.."

if [ "$#" -eq 0 ]; then
    exec docker compose -f docker-compose.dev.yml run --rm test
fi

exec docker compose -f docker-compose.dev.yml run --rm test go test "$@"
