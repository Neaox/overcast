# docker-go.ps1 -- run Go toolchain commands in a Docker container, for
# development on Windows without a host Go install and outside the
# devcontainer. PowerShell twin of scripts/docker-go.sh; see that script's
# header comment for behavior details (caches, bind-mount performance, git
# limitations). The two scripts must stay behaviorally identical.
#
# Usage:
#   scripts\docker-go.ps1 test ./internal/state/...
#   scripts\docker-go.ps1 test -race -count=10 ./internal/state/
#   scripts\docker-go.ps1 vet ./...
#   scripts\docker-go.ps1 run ./cmd/verify
#   scripts\docker-go.ps1 shell

$ErrorActionPreference = "Stop"

$image = if ($env:OVERCAST_GO_IMAGE) { $env:OVERCAST_GO_IMAGE } else { "golang:1.24-bookworm" }
$modCache = if ($env:OVERCAST_GO_MOD_CACHE) { $env:OVERCAST_GO_MOD_CACHE } else { "overcast-go-mod-cache" }
$buildCache = if ($env:OVERCAST_GO_BUILD_CACHE) { $env:OVERCAST_GO_BUILD_CACHE } else { "overcast-go-build-cache" }

$repoRoot = Split-Path -Parent $PSScriptRoot

if ($args.Count -eq 0) {
    Write-Error "usage: docker-go.ps1 <go-subcommand and args> | run <pkg> | shell"
    exit 2
}

# GOFLAGS=-buildvcs=false: see docker-go.sh's comment -- git inside the
# container refuses the bind-mounted repo ("dubious ownership"), which
# fails VCS stamping during `go build` of main packages.
# GIT_CONFIG_GLOBAL + --add safe.directory fixes `git diff/merge-base`
# calls inside the container for scripts that need git (verify-changed).
$dockerArgs = @(
    "run", "--rm",
    "-v", "${repoRoot}:/src",
    "-v", "${modCache}:/go/pkg/mod",
    "-v", "${buildCache}:/root/.cache/go-build",
    "-e", "GOFLAGS=-buildvcs=false",
    "-e", "GIT_CONFIG_GLOBAL=/tmp/gitconfig",
    "-w", "/src"
)
# Only attach a TTY for interactive sessions; plain command output pipes
# cleanly without one.
if ([Environment]::UserInteractive -and -not [Console]::IsOutputRedirected) {
    $dockerArgs += "-it"
}
$dockerArgs += $image

# Always mark /src safe for git inside the container so repo-level git
# calls (diff, merge-base, ls-files) don't fail with "dubious ownership".
# The repo is bind-mounted from the host and the container's root uid
# doesn't own the files.
$safeDir = 'git config --global --add safe.directory /src 2>/dev/null; exec "$@"'
$entrypoint = @("sh", "-c", $safeDir, "--")

if ($args[0] -eq "shell") {
    $entrypoint += "bash"
} elseif ($args[0] -eq "run") {
    # `docker-go.ps1 run <pkg>` — runs the Go binary at the given package
    # path (e.g. `docker-go.ps1 run ./cmd/verify`).
    $entrypoint += "go"
    $entrypoint += "run"
    $entrypoint += $args[1..$args.Count]
} else {
    $entrypoint += "go"
    $entrypoint += $args
}

& docker @dockerArgs @entrypoint
exit $LASTEXITCODE
