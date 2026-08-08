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

$image = if ($env:OVERCAST_GO_IMAGE) { $env:OVERCAST_GO_IMAGE } else { "golang:1.24-bookworm" }
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
# container, GOMAXPROCS has to be set by hand because container-aware
# GOMAXPROCS only arrived in Go 1.25 and this image is golang:1.24-bookworm,
# and -p bounds concurrent test binaries (it defaults to GOMAXPROCS, and each
# binary inherits GOMAXPROCS, so the default squares the parallelism).
#
# The counts are derived, never hardcoded -- `docker run --cpus=N` is rejected
# when N exceeds the CPUs the daemon reports. The daemon's own count is
# preferred over [Environment]::ProcessorCount because Docker Desktop's VM can
# have fewer CPUs than the host.
function Get-CpuTotal {
    $reported = $null
    try { $reported = (& docker info --format "{{.NCPU}}") } catch { $reported = $null }
    $parsed = 0
    if ($reported -and [int]::TryParse(($reported | Select-Object -First 1).ToString().Trim(), [ref]$parsed) -and $parsed -ge 1) {
        return $parsed
    }
    $hostCount = [Environment]::ProcessorCount
    if ($hostCount -ge 1) { return $hostCount }
    return 2
}

$goCpus = $env:OVERCAST_GO_CPUS
$goTestP = $env:OVERCAST_GO_TEST_P
if ($goCpus -eq "0") {
    # Explicit opt-out: no cap, and no -p either, unless -p was asked for.
    if (-not $goTestP) { $goTestP = "0" }
} elseif ((-not $goCpus) -or (-not $goTestP)) {
    $cpuTotal = Get-CpuTotal
    if (-not $goCpus) { $goCpus = [string][Math]::Max(1, [Math]::Floor($cpuTotal / 2)) }
    if (-not $goTestP) { $goTestP = [string][Math]::Max(1, [Math]::Floor($cpuTotal / 4)) }
}

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
