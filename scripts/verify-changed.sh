#!/usr/bin/env bash
# verify-changed.sh — run the checks CI runs, scoped to what this branch changed.
#
# Exists because build+vet+tests is a weaker gate than CI: `Lint` (golangci-lint)
# is its own required job, and web/ has a typecheck that is easy to run in a way
# that silently passes (see web/tsconfig.json). Both have shipped broken commits.
#
# Exit codes: 0 = nothing to do or everything passed, 2 = a check failed.
# 2 is what a Claude Code PreToolUse hook treats as "block", with stderr shown.

set -uo pipefail

root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0

base=$(git merge-base HEAD origin/main 2>/dev/null) || base=""
if [ -z "$base" ]; then
  # No origin/main to compare against (fresh clone, detached CI checkout).
  # Nothing sensible to scope to, so stay out of the way.
  exit 0
fi

changed=$(git diff --name-only "$base"...HEAD; git diff --name-only; git ls-files --others --exclude-standard)
[ -z "$changed" ] && exit 0

go_changed=$(printf '%s\n' "$changed" | grep -E '\.go$' | head -1)
web_changed=$(printf '%s\n' "$changed" | grep -E '^web/src/|^web/[^/]*\.(ts|json)$' | head -1)

failed=""
skipped=""

# ---- Go: golangci-lint -----------------------------------------------------
if [ -n "$go_changed" ]; then
  ver=$(sed -n 's/^GOLANGCI_LINT_VERSION := //p' Makefile)
  ver=${ver:-v2.8.0}
  pkg="github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$ver"

  if command -v golangci-lint >/dev/null 2>&1; then
    golangci-lint run ./... || failed="$failed golangci-lint"
  elif command -v go >/dev/null 2>&1; then
    go run "$pkg" run ./... || failed="$failed golangci-lint"
  elif command -v docker >/dev/null 2>&1 && [ -x scripts/docker-go.sh ]; then
    scripts/docker-go.sh run "$pkg" run ./... || failed="$failed golangci-lint"
  else
    skipped="$skipped golangci-lint(no go/docker)"
  fi
fi

# ---- Web: typecheck + lint -------------------------------------------------
if [ -n "$web_changed" ]; then
  if command -v pnpm >/dev/null 2>&1 && [ -d web/node_modules ]; then
    # Never `tsc --noEmit`: web/tsconfig.json is a solution-style config and
    # compiles zero files. `pnpm run typecheck` runs the real projects.
    (cd web && pnpm run typecheck) || failed="$failed pnpm-typecheck"
    (cd web && pnpm run lint) || failed="$failed pnpm-lint"
  else
    skipped="$skipped web(no pnpm or node_modules)"
  fi
fi

if [ -n "$skipped" ]; then
  echo "verify-changed: could not run:$skipped — CI will still check these." >&2
fi

if [ -n "$failed" ]; then
  cat >&2 <<EOF
verify-changed: FAILED:$failed

These are required CI jobs. Fix them before pushing, or re-run the push if you
have a reason to override. Full gate: \`make check\` (fmt vet lint test).
EOF
  exit 2
fi

exit 0
