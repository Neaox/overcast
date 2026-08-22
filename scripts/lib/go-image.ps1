# go-image.ps1 -- the Go image shared by the PowerShell Go-in-Docker entry
# points. Dot-source this, never run it directly:
#
#   . "$PSScriptRoot\lib\go-image.ps1"
#
# It sets $goImage in the caller's scope: OVERCAST_GO_IMAGE when set, else the
# image .devcontainer\Dockerfile builds FROM, else the literal fallback below.
# Why it is read from the Dockerfile rather than hardcoded -- and why the
# fallback must stay equal to that file's FROM line -- is written out in
# lib/go-image.sh. POSIX twin: keep them in step.

$goImageFallback = "golang:1.26-bookworm"

# Get-DevcontainerGoImage -- the image named by the first FROM line of
# .devcontainer\Dockerfile, minus any --platform=... flags before it and any
# "AS stage" after it. $null if the file is unreadable, has no FROM, or the
# reference is not literal (an ${ARG}, say). $PSScriptRoot here is this file's
# own directory, scripts\lib, so the repo root is two levels up.
function Get-DevcontainerGoImage {
    $repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
    $dockerfile = Join-Path $repoRoot ".devcontainer\Dockerfile"
    if (-not (Test-Path -LiteralPath $dockerfile)) { return $null }
    foreach ($line in (Get-Content -LiteralPath $dockerfile)) {
        if ($line -match '^FROM\s+(?:--\S+\s+)*(\S+)') {
            if ($Matches[1] -like '*$*') { return $null }
            return $Matches[1]
        }
    }
    return $null
}

$goImage = if ($env:OVERCAST_GO_IMAGE) { $env:OVERCAST_GO_IMAGE } else { Get-DevcontainerGoImage }
if (-not $goImage) { $goImage = $goImageFallback }
