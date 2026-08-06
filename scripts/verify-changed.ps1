# verify-changed.ps1 — thin platform wrapper that delegates to the Go implementation.
# All logic lives in cmd/verify/main.go so behaviour is identical on every OS.
param(
    [switch]$Force,
    [string]$Record
)

$root = git rev-parse --show-toplevel 2>$null
if (-not $root) { exit 0 }
Set-Location -LiteralPath $root

$args = @()
if ($Force) { $args += '-force' }
if ($Record) { $args += '-record'; $args += $Record }

$proc = Start-Process go -ArgumentList (@('run', './cmd/verify') + $args) -NoNewWindow -Wait -PassThru
exit $proc.ExitCode
