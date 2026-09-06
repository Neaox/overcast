# go-toolchain.ps1 -- the Go toolchain pin shared by the PowerShell Go-in-
# Docker entry point. Dot-source this, never run it directly:
#
#   . "$PSScriptRoot\lib\go-toolchain.ps1"
#
# It sets $goToolchain in the caller's scope: go.mod's `toolchain` line
# verbatim (e.g. "go1.27.0"), or, if that line is absent, its `go` line with a
# "go" prefix added (e.g. "go1.25.0"). Empty if go.mod has neither.
#
# Why this exists, and why the fix is GOTOOLCHAIN rather than a newer image,
# is written out in lib/go-toolchain.sh's header -- short version: `go run
# <pkg>@<version>` resolves its toolchain from the *invoked module's* go.mod,
# not this repo's, so on an image whose Go is older than this repo's pin,
# `make lint-go`-style invocations silently build the tool with the image's
# older Go and the tool then refuses to run against this repo's newer pin.
# POSIX twin: keep them in step.
#
# A caller-set GOTOOLCHAIN always wins; the wrapper only fills in a default.
# Go downloads a pinned toolchain into GOMODCACHE (golang.org/toolchain@...),
# which the wrapper already mounts as a named volume, so the first run pays
# the download once and every later run reuses it.

function Get-GoModToolchain {
    $repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
    $gomod = Join-Path $repoRoot "go.mod"
    if (-not (Test-Path -LiteralPath $gomod)) { return $null }
    $lines = Get-Content -LiteralPath $gomod
    foreach ($line in $lines) {
        if ($line -match '^toolchain\s+(\S+)') { return $Matches[1] }
    }
    foreach ($line in $lines) {
        if ($line -match '^go\s+([0-9]\S*)') { return "go$($Matches[1])" }
    }
    return $null
}

$goToolchain = Get-GoModToolchain

# ---- Image-vs-pin drift warning ----------------------------------------------
#
# See lib/go-toolchain.sh for the full rationale. The image lagging the pin
# needs no warning (GOTOOLCHAIN just fetches the pin on demand); the image
# running *ahead* of go.mod does, because GOTOOLCHAIN=<pin> would then force
# every `go run` back down to the older pinned toolchain instead of using the
# newer one already in the image. Static version-number comparison only --
# never a `docker run` just to ask the image its own version.
function Warn-IfGoImageAheadOfToolchain {
    param([string]$Image, [string]$Pin)
    if ($Image -notmatch '^golang:([0-9]+)\.([0-9]+)') { return }
    $imageMajor = [int]$Matches[1]
    $imageMinor = [int]$Matches[2]
    if ($Pin -notmatch '^go([0-9]+)\.([0-9]+)') { return }
    $pinMajor = [int]$Matches[1]
    $pinMinor = [int]$Matches[2]
    if ($imageMajor -gt $pinMajor -or ($imageMajor -eq $pinMajor -and $imageMinor -gt $pinMinor)) {
        Write-Warning ("docker-go: image $Image ships Go $imageMajor.$imageMinor, newer than " +
            "the go.mod-pinned toolchain $Pin -- GOTOOLCHAIN=$Pin will force every 'go run' to " +
            "fetch and use the older pin instead. Bump go.mod's toolchain line to match.")
    }
}

if ($goToolchain) {
    Warn-IfGoImageAheadOfToolchain -Image $image -Pin $goToolchain
}
