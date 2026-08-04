# compat/dev.ps1 -- compat dashboard with a hot-reloading UI (Windows twin of
# compat/dev.sh). Both wrappers hand straight over to cmd/compat, so they
# behave identically; see cmd/compat/launch.go for what actually happens.
#
# Nothing here binds 4566 or 4567: those belong to your own Overcast instance
# (AGENTS.md, "Reserved ports"). Every port is probed at startup.
#
# Usage:
#   compat\dev.ps1
#   compat\dev.ps1 --suite go-sdk
#   compat\dev.ps1 --endpoint http://localhost:4566
#
# Prerequisites: Go 1.24+, Node.js 20+, and either a built bin\overcast.exe or
# Docker -- the last two only when compat has to start Overcast itself.
$ErrorActionPreference = "Stop"

Set-Location (Split-Path -Parent $PSScriptRoot)
& go run ./cmd/compat --dev @args
exit $LASTEXITCODE
