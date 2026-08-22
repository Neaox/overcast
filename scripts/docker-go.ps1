# docker-go.ps1 -- run Go toolchain commands in a Docker container, for
# development on Windows without a host Go install and outside the
# devcontainer. PowerShell twin of scripts/docker-go.sh; see that script's
# header comment for behavior details (caches, the CPU bound, bind-mount
# performance, git limitations). The two scripts must stay behaviorally
# identical.
#
# Usage:
#   scripts\docker-go.ps1 test ./internal/state/...
#   scripts\docker-go.ps1 test -race -count=10 ./internal/state/
#   scripts\docker-go.ps1 vet ./...
#   scripts\docker-go.ps1 shell
#
# CPU use is capped to half the available cores so a long test run leaves the
# machine usable. Override with OVERCAST_GO_CPUS / OVERCAST_GO_TEST_P;
# OVERCAST_GO_CPUS=0 restores the old unbounded behaviour.

$ErrorActionPreference = "Stop"

# Image resolution lives in lib\go-image.ps1 (OVERCAST_GO_IMAGE, else the
# devcontainer's FROM line in .devcontainer\Dockerfile); it sets $goImage.
. "$PSScriptRoot\lib\go-image.ps1"
$image = $goImage
$modCache = if ($env:OVERCAST_GO_MOD_CACHE) { $env:OVERCAST_GO_MOD_CACHE } else { "overcast-go-mod-cache" }
$buildCache = if ($env:OVERCAST_GO_BUILD_CACHE) { $env:OVERCAST_GO_BUILD_CACHE } else { "overcast-go-build-cache" }

$repoRoot = Split-Path -Parent $PSScriptRoot

if ($args.Count -eq 0) {
    Write-Error "usage: docker-go.ps1 <go-subcommand and args> | shell"
    exit 2
}

# ---- CPU bound --------------------------------------------------------------
#
# Rationale in full lives in scripts/docker-go.sh. In short: --cpus caps the
# container, GOMAXPROCS is set by hand because container-aware GOMAXPROCS only
# arrived in Go 1.25 and OVERCAST_GO_IMAGE can still name an older image (on
# 1.25+ it merely restates the automatic default), and -p bounds concurrent
# test binaries (it defaults to GOMAXPROCS, and each binary inherits
# GOMAXPROCS, so the default squares the parallelism).
#
# The counts are derived, never hardcoded -- `docker run --cpus=N` is rejected
# when N exceeds the CPUs the daemon reports. The detection and the derivation
# live in lib\go-cpu-bound.ps1, shared with scripts\container-test.ps1; it sets
# $goCpus and $goTestP.
. "$PSScriptRoot\lib\go-cpu-bound.ps1"

# GOFLAGS=-buildvcs=false: see docker-go.sh's comment -- git inside the
# container refuses the bind-mounted repo ("dubious ownership"), which
# fails VCS stamping during `go build` of main packages.
$dockerArgs = @(
    "run", "--rm",
    "-v", "${repoRoot}:/src",
    "-v", "${modCache}:/go/pkg/mod",
    "-v", "${buildCache}:/root/.cache/go-build",
    "-e", "GOFLAGS=-buildvcs=false",
    "-w", "/src"
)
if ($goCpus -ne "0") {
    $dockerArgs += @("--cpus=$goCpus", "--env=GOMAXPROCS=$goCpus")
}
# Only attach a TTY for interactive sessions; plain command output pipes
# cleanly without one.
if ([Environment]::UserInteractive -and -not [Console]::IsOutputRedirected) {
    $dockerArgs += "-it"
}
$dockerArgs += $image

if ($args[0] -eq "shell") {
    $dockerArgs += "bash"
} else {
    $goArgs = @($args)
    # Inject the default -p directly after the `test` subcommand, and only when
    # the caller has not passed their own. `go -C dir test ...` is a supported
    # form, so `test` is not always the first argument.
    $hasP = @($goArgs | Where-Object { $_ -eq "-p" -or $_ -like "-p=*" -or $_ -eq "--p" -or $_ -like "--p=*" }).Count -gt 0
    if ($goTestP -ne "0" -and -not $hasP) {
        $at = -1
        if ($goArgs[0] -eq "test") { $at = 1 }
        elseif ($goArgs[0] -eq "-C" -and $goArgs.Count -ge 3 -and $goArgs[2] -eq "test") { $at = 3 }
        elseif ($goArgs[0] -like "-C=*" -and $goArgs.Count -ge 2 -and $goArgs[1] -eq "test") { $at = 2 }
        if ($at -ge 0) {
            $goArgs = @($goArgs[0..($at - 1)]) + @("-p", $goTestP) + @($goArgs | Select-Object -Skip $at)
        }
    }
    $dockerArgs += "go"
    $dockerArgs += $goArgs
}

& docker @dockerArgs
exit $LASTEXITCODE
