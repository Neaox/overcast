#!/usr/bin/env sh
# compat/dev.sh — compat dashboard with a hot-reloading UI.
#
# A thin wrapper. Every step — choosing free ports, starting a throwaway
# Overcast instance, waiting for it to become healthy, running Vite, opening a
# browser, tearing it all down — lives in cmd/compat; see cmd/compat/launch.go.
# compat/dev.ps1 is the Windows twin and drives the same code, so the behaviour
# is identical on every platform.
#
# Nothing here binds 4566 or 4567: those belong to your own Overcast instance
# (AGENTS.md § Reserved ports). Every port is probed at startup, so several
# sessions can run side by side.
#
# Usage:
#   compat/dev.sh                                   # all defaults
#   compat/dev.sh --endpoint http://localhost:4566  # use your own instance
#   compat/dev.sh --suite go-sdk                    # one suite
#   compat/dev.sh --port-base 4700                  # scan for ports from 4700
#   compat/dev.sh --overcast-host localhost.overcast.sh
#
# Prerequisites: Go 1.24+, Node.js 20+, and either a built bin/overcast or
# Docker — the last two only when compat has to start Overcast itself.
set -eu

cd "$(dirname "$0")/.."
exec go run ./cmd/compat --dev "$@"
