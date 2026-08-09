# container-test.ps1 -- run the Go test suite inside docker-compose.dev.yml's
# `test` service, CPU-capped the same way scripts\docker-go.ps1 is. PowerShell
# twin of scripts/container-test.sh; see that script's header for why the cap
# cannot be a literal in the Compose file. The two must stay behaviorally
# identical.
#
# Usage:
#   scripts\container-test.ps1
#   scripts\container-test.ps1 -race -count=1 -timeout=900s ./internal/...

$ErrorActionPreference = "Stop"

. "$PSScriptRoot\lib\go-cpu-bound.ps1"

# OVERCAST_GO_CPUS drives the service's `cpus:` and GOMAXPROCS;
# OVERCAST_GO_TEST_P drives GOFLAGS=-p=N. $goTestP spells "no -p" as 0, but
# `go test -p 0` is an error, so Compose is handed an empty value instead.
$env:OVERCAST_GO_CPUS = $goCpus
if ($goTestP -eq "0") {
    $env:OVERCAST_GO_TEST_P = ""
} else {
    $env:OVERCAST_GO_TEST_P = $goTestP
}

# Run from the repo root so the Compose project name matches a by-hand
# `docker compose -f docker-compose.dev.yml ...`.
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $composeArgs = @("compose", "-f", "docker-compose.dev.yml", "run", "--rm", "test")
    if ($args.Count -gt 0) {
        $composeArgs += @("go", "test")
        $composeArgs += $args
    }
    & docker @composeArgs
    exit $LASTEXITCODE
} finally {
    Pop-Location
}
