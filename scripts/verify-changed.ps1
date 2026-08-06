# verify-changed.ps1 -- thin platform wrapper. All logic lives in cmd/verify/main.go.
param([switch]$Force, [string]$Record)

$root = git rev-parse --show-toplevel 2>$null
if (-not $root) { exit 0 }
Set-Location -LiteralPath $root

$verifyArgs = @('run', './cmd/verify')
if ($Force) { $verifyArgs += '-force' }
if ($Record) { $verifyArgs += '-record'; $verifyArgs += $Record }

& powershell -ExecutionPolicy Bypass -File .\scripts\go.ps1 @verifyArgs
exit $LASTEXITCODE
