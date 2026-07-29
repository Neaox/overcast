# compat/run.ps1 — compat dashboard with a pre-built (non-HMR) UI (Windows twin
# of compat/run.sh). Both wrappers hand straight over to cmd/compat, so they
# behave identically; see cmd/compat/launch.go for what actually happens.
#
# For a hot-reloading UI use compat\dev.ps1 instead.
#
# Nothing here binds 4566 or 4567: those belong to your own Overcast instance
# (AGENTS.md § Reserved ports). Every port is probed at startup.
#
# Usage:
#   compat\run.ps1
#   compat\run.ps1 --port 8888
#   compat\run.ps1 --endpoint http://localhost:4566
#
# Prerequisites: Go 1.24+, Node.js 20+, and either a built bin\overcast.exe or
# Docker — the last two only when compat has to start Overcast itself.
$ErrorActionPreference = "Stop"

Set-Location (Split-Path -Parent $PSScriptRoot)
& go run ./cmd/compat --serve --interactive --build-ui --open @args
exit $LASTEXITCODE
