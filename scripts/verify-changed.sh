#!/usr/bin/env bash
# verify-changed.sh — thin platform wrapper. All logic lives in cmd/verify/main.go.
set -euo pipefail
root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0
exec scripts/go.sh run ./cmd/verify "$@"
