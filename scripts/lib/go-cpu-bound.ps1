# go-cpu-bound.ps1 -- the CPU bound shared by the PowerShell Go-in-Docker entry
# points. Dot-source this, never run it directly:
#
#   . "$PSScriptRoot\lib\go-cpu-bound.ps1"
#
# It defines Get-CpuTotal and sets $goCpus / $goTestP in the caller's scope from
# OVERCAST_GO_CPUS / OVERCAST_GO_TEST_P. "0" for $goCpus means no bound (the
# pre-cap behaviour); "0" for $goTestP means never inject a -p.
#
# The rationale -- why --cpus, GOMAXPROCS and -p are all needed, and why the
# numbers are derived rather than hardcoded -- is in scripts/docker-go.sh's
# "CPU bound" section. POSIX twin: lib/go-cpu-bound.sh. Keep them in step.

# Get-CpuTotal -- the daemon's count is preferred over [Environment]::ProcessorCount
# because `docker run --cpus=N` is validated against it, and Docker Desktop's VM
# can have fewer CPUs than the host. Falls back to the host count, then to a
# conservative 2.
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
