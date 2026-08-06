# go.ps1 - run the Go toolchain, using native go when available, Docker otherwise.
# Usage: scripts\go.ps1 test .\internal\state\...  |  scripts\go.ps1 shell

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $scriptDir

if ($args.Count -eq 0) {
    Write-Error "usage: go.ps1 <go-subcommand and args> | shell"
    exit 2
}

$nativeGo = Get-Command go -ErrorAction SilentlyContinue
if ($nativeGo) {
    if ($args[0] -eq "shell") {
        Write-Error "go.ps1: native go shell - just run bash"
        exit 2
    }
    & go @args
    exit $LASTEXITCODE
}

$image = if ($env:OVERCAST_GO_IMAGE) { $env:OVERCAST_GO_IMAGE } else { "golang:1.24-bookworm" }
$modCache = if ($env:OVERCAST_GO_MOD_CACHE) { $env:OVERCAST_GO_MOD_CACHE } else { "overcast-go-mod-cache" }
$buildCache = if ($env:OVERCAST_GO_BUILD_CACHE) { $env:OVERCAST_GO_BUILD_CACHE } else { "overcast-go-build-cache" }

$dockerArgs = @(
    "run", "--rm",
    "-v", "${repoRoot}:/src",
    "-v", "${modCache}:/go/pkg/mod",
    "-v", "${buildCache}:/root/.cache/go-build",
    "-e", "GOFLAGS=-buildvcs=false",
    "-e", "GIT_CONFIG_GLOBAL=/tmp/gitconfig",
    "-w", "/src"
)
if ([Environment]::UserInteractive -and -not [Console]::IsOutputRedirected) {
    $dockerArgs += "-it"
}
$dockerArgs += $image

$safeDir = 'git config --global --add safe.directory /src 2>/dev/null; exec "$@"'
$entrypoint = @("sh", "-c", $safeDir, "--")

if ($args[0] -eq "shell") {
    $entrypoint += "bash"
} else {
    $entrypoint += "go"
    $entrypoint += $args
}

& docker @dockerArgs @entrypoint
exit $LASTEXITCODE
