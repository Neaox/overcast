#!/usr/bin/env bash
# verify-changed.sh — thin platform wrapper that delegates to the Go implementation.
# All logic lives in cmd/verify/main.go so behaviour is identical on every OS.
set -euo pipefail
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0
exec go run ./cmd/verify "$@"
