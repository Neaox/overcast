# docker-go.ps1 — run Go toolchain commands in a Docker container, for
# development on Windows without a host Go install and outside the
# devcontainer. PowerShell twin of scripts/docker-go.sh; see that script's
# header comment for behavior details (caches, bind-mount performance, git
# limitations). The two scripts must stay behaviorally identical.
#
# Usage:
#   scripts\docker-go.ps1 test ./internal/state/...
#   scripts\docker-go.ps1 test -race -count=10 ./internal/state/
#   scripts\docker-go.ps1 vet ./...
#   scripts\docker-go.ps1 shell

$ErrorActionPreference = "Stop"

$image = if ($env:OVERCAST_GO_IMAGE) { $env:OVERCAST_GO_IMAGE } else { "golang:1.24-bookworm" }
$modCache = if ($env:OVERCAST_GO_MOD_CACHE) { $env:OVERCAST_GO_MOD_CACHE } else { "overcast-go-mod-cache" }
$buildCache = if ($env:OVERCAST_GO_BUILD_CACHE) { $env:OVERCAST_GO_BUILD_CACHE } else { "overcast-go-build-cache" }

$repoRoot = Split-Path -Parent $PSScriptRoot

if ($args.Count -eq 0) {
    Write-Error "usage: docker-go.ps1 <go-subcommand and args> | shell"
    exit 2
}

$dockerArgs = @(
    "run", "--rm",
    "-v", "${repoRoot}:/src",
    "-v", "${modCache}:/go/pkg/mod",
    "-v", "${buildCache}:/root/.cache/go-build",
    "-w", "/src"
)
# Only attach a TTY for interactive sessions; plain command output pipes
# cleanly without one.
if ([Environment]::UserInteractive -and -not [Console]::IsOutputRedirected) {
    $dockerArgs += "-it"
}
$dockerArgs += $image

if ($args[0] -eq "shell") {
    $dockerArgs += "bash"
} else {
    $dockerArgs += "go"
    $dockerArgs += $args
}

& docker @dockerArgs
exit $LASTEXITCODE
