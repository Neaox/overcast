#!/usr/bin/env sh
# compat/run.sh — compat dashboard with a pre-built (non-HMR) UI.
#
# A thin wrapper. Every step — building the UI, choosing free ports, starting a
# throwaway Overcast instance, waiting for it to become healthy, opening a
# browser, tearing it all down — lives in cmd/compat; see cmd/compat/launch.go.
# compat/run.ps1 is the Windows twin and drives the same code.
#
# Use this for a stable, production-like dashboard. For a hot-reloading UI use
# compat/dev.sh instead.
#
# Nothing here binds 4566 or 4567: those belong to your own Overcast instance
# (AGENTS.md § Reserved ports). Every port is probed at startup.
#
# Usage:
#   compat/run.sh                                   # all defaults
#   compat/run.sh --endpoint http://localhost:4566  # use your own instance
#   compat/run.sh --port 8888                       # preferred dashboard port
#
# Prerequisites: Go 1.24+, Node.js 20+, and either a built bin/overcast or
# Docker — the last two only when compat has to start Overcast itself.
set -eu

cd "$(dirname "$0")/.."
exec go run ./cmd/compat --serve --interactive --build-ui --open "$@"
