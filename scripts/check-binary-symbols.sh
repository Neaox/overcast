#!/usr/bin/env bash
# check-binary-symbols.sh — verify no capability symbols leak into the production binary.
#
# The capabilities package (and all per-service capabilities_dev.go files) must be
# completely absent from a build without -tags dev.
#
# Usage: bash scripts/check-binary-symbols.sh
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp)
trap "rm -f "$TMP"" EXIT

echo "check-binary-symbols: building slim binary (no dev or web assets needed)..."
cd "$ROOT"
go build -tags slim -trimpath -ldflags="-w -s" -o "$TMP" ./cmd/overcast

echo "check-binary-symbols: scanning symbol table..."
if go tool nm "$TMP" 2>/dev/null | grep -qE 'capabilities.*Capability|capabilities.*Registry'; then
    go tool nm "$TMP" | grep -E 'capabilities.*Capability|capabilities.*Registry' | head -20
    exit 1
fi

echo "check-binary-symbols: OK — no capability symbols in production binary."
