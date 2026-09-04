# Overcast installer for Windows (PowerShell 5.1 and later).
#
#   irm https://overcast.sh/install.ps1 | iex
#
# Downloads a release binary for this machine from GitHub Releases, verifies
# it against the release's SHA256SUMS, puts it in a per-user directory and adds
# that directory to the user PATH. It never asks a question and never elevates.
# `irm | iex` cannot pass parameters, so every choice is also an
# OVERCAST_INSTALL_* environment variable:
#
#   $env:OVERCAST_INSTALL_FLAVOR = "slim"; irm https://overcast.sh/install.ps1 | iex
#
# With the file on disk the parameters work directly:
#
#   .\install.ps1 -Version v0.0.1-alpha.40 -Dir C:\tools\overcast -NoModifyPath
#
# Everything is a function and Main runs last, so a download cut off part-way
# executes nothing. The Linux/macOS counterpart is install.sh; install/README.md
# in the Overcast repository is the reference for both.

[CmdletBinding()]
# The parameters are read inside the functions below; the analyzer only
# looks for uses in the param block's own scope. Write-Host is deliberate: this
# is an installer talking to the person who ran it, not a cmdlet in a pipeline.
[Diagnostics.CodeAnalysis.SuppressMessageAttribute("PSReviewUnusedParameter", "", Justification = "used inside functions")]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute("PSAvoidUsingWriteHost", "", Justification = "interactive installer output")]
param(
    [string]$Version = $env:OVERCAST_INSTALL_VERSION,
    [string]$Dir = $env:OVERCAST_INSTALL_DIR,
    [string]$Flavor = $env:OVERCAST_INSTALL_FLAVOR,
    [switch]$Slim,
    [switch]$Both,
    [switch]$NoModifyPath = ($env:OVERCAST_INSTALL_MODIFY_PATH -eq "0"),
    [switch]$DryRun = ($env:OVERCAST_INSTALL_DRY_RUN -eq "1"),
    [switch]$Uninstall = ($env:OVERCAST_INSTALL_UNINSTALL -eq "1")
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

# Replaced with the release tag when the script is attached to a release and
# when the website serves it (install/bake.py). Left empty in the repository,
# where "latest" is resolved through the GitHub API instead.
$BakedVersion = ""

$Repo = "overcast-sh/overcast"
$BaseUrl = if ($env:OVERCAST_INSTALL_BASE_URL) { $env:OVERCAST_INSTALL_BASE_URL } else { "https://github.com/$Repo/releases/download" }
$ReleasesApi = if ($env:OVERCAST_INSTALL_RELEASES_API) { $env:OVERCAST_INSTALL_RELEASES_API } else { "https://api.github.com/repos/$Repo/releases?per_page=30" }
$ReleasesPage = "https://github.com/$Repo/releases"
$Retries = if ($env:OVERCAST_INSTALL_RETRIES) { [int]$env:OVERCAST_INSTALL_RETRIES } else { 4 }

function Write-Note([string]$Message) {
    Write-Host $Message
}

function Fail([string]$Message) {
    throw "install.ps1: $Message"
}

function Get-Flavor {
    if ($Both) { return "both" }
    if ($Slim) { return "slim" }
    if (-not $Flavor) { return "full" }
    if ($Flavor -notin @("full", "slim", "both")) { Fail "OVERCAST_INSTALL_FLAVOR / -Flavor must be full, slim or both (got '$Flavor')" }
    return $Flavor
}

function Get-BinaryList([string]$Which) {
    switch ($Which) {
        "full" { return @("overcast") }
        "slim" { return @("overcastd") }
        "both" { return @("overcast", "overcastd") }
    }
}

function Get-Platform {
    $os = if ($env:OVERCAST_INSTALL_OS) { $env:OVERCAST_INSTALL_OS } else { "windows" }
    if ($os -ne "windows") { Fail "OVERCAST_INSTALL_OS must be windows for this installer" }

    $arch = $env:OVERCAST_INSTALL_ARCH
    if (-not $arch) {
        $native = $env:PROCESSOR_ARCHITEW6432
        if (-not $native) { $native = $env:PROCESSOR_ARCHITECTURE }
        switch -Regex ($native) {
            "^ARM64$" {
                # No arm64 Windows build ships yet. Windows 11 on ARM runs the
                # amd64 binary under its x64 emulation, so that is what gets
                # installed, and the output says so.
                Write-Note "note: this is an ARM64 Windows machine; installing the amd64 binary, which runs under x64 emulation"
                $arch = "amd64"
            }
            "^AMD64$" { $arch = "amd64" }
            default { Fail "unsupported architecture '$native'. Overcast ships for 64-bit Windows; see $ReleasesPage" }
        }
    }
    if ($arch -ne "amd64" -and $arch -ne "arm64") { Fail "OVERCAST_INSTALL_ARCH must be amd64 or arm64" }
    return "windows-$arch"
}

function Initialize-Http {
    # PowerShell 5.1 negotiates TLS 1.0 by default on older Windows builds,
    # which GitHub rejects. Add 1.2 without removing anything already enabled.
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch {
        # Newer runtimes manage this themselves.
        $null = $_
    }
}

# Download a URL to a file, retried with backoff. A zero-byte body counts as a
# failure: GitHub's asset CDN hands those back now and then. A 404 is final:
# the release or asset is not there, and waiting will not change that.
function Get-Download([string]$Url, [string]$OutFile) {
    $attempt = 1
    while ($true) {
        if (Test-Path -LiteralPath $OutFile) { Remove-Item -LiteralPath $OutFile -Force }
        try {
            Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing -Headers @{ "User-Agent" = "overcast-install" } | Out-Null
        } catch {
            $response = $null
            try { $response = $_.Exception.Response } catch { $response = $null }
            if ($response -and ([int]$response.StatusCode) -eq 404) {
                if (Test-Path -LiteralPath $OutFile) { Remove-Item -LiteralPath $OutFile -Force }
                return $false
            }
        }
        # -Force: the partial file is dot-prefixed, which pwsh on Linux and
        # macOS treats as hidden and otherwise refuses to return.
        if ((Test-Path -LiteralPath $OutFile) -and (Get-Item -LiteralPath $OutFile -Force).Length -gt 0) {
            return $true
        }
        if ($attempt -ge $Retries) {
            if (Test-Path -LiteralPath $OutFile) { Remove-Item -LiteralPath $OutFile -Force }
            return $false
        }
        $delay = $attempt * 2
        Write-Warning "download failed (attempt $attempt of $Retries), retrying in ${delay}s: $Url"
        Start-Sleep -Seconds $delay
        $attempt++
    }
}

# Newest release as GitHub lists them. `releases/latest` is not used: it only
# ever answers with a stable release, and returns 404 while every Overcast
# release is a prerelease.
function Get-LatestVersion {
    try {
        $releases = Invoke-RestMethod -Uri $ReleasesApi -UseBasicParsing -Headers @{ "User-Agent" = "overcast-install" }
    } catch {
        Fail "could not fetch the release list from $ReleasesApi`nCheck your network, or pin a release: -Version <tag> (see $ReleasesPage)"
    }
    $first = @($releases) | Select-Object -First 1
    if (-not $first -or -not $first.tag_name) { Fail "no releases found at $ReleasesApi" }
    return [string]$first.tag_name
}

function Resolve-Version {
    $v = $Version
    if (-not $v) { $v = $BakedVersion }
    if (-not $v) { $v = Get-LatestVersion }
    if ($v -notmatch "^v") { $v = "v$v" }
    return $v
}

function Get-ExpectedHash([string]$SumsFile, [string]$Asset) {
    foreach ($line in Get-Content -LiteralPath $SumsFile) {
        if ($line -match "^([0-9a-fA-F]{64}) [ *]?(.+)$" -and $Matches[2] -eq $Asset) {
            return $Matches[1].ToLowerInvariant()
        }
    }
    return $null
}

function Test-Checksum([string]$File, [string]$Asset, [string]$SumsFile, [string]$Tag) {
    $expected = Get-ExpectedHash $SumsFile $Asset
    if (-not $expected) { Fail "SHA256SUMS for $Tag has no entry for $Asset" }
    # .NET rather than Get-FileHash: on Windows PowerShell 5.1 that cmdlet is a
    # script function loaded from Microsoft.PowerShell.Utility.psm1, and a
    # 5.1 process started from pwsh inherits a PSModulePath without it.
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $stream = [System.IO.File]::OpenRead($File)
    try {
        $actual = ([System.BitConverter]::ToString($sha.ComputeHash($stream)) -replace "-", "").ToLowerInvariant()
    } finally {
        $stream.Dispose()
        $sha.Dispose()
    }
    if ($actual -ne $expected) {
        Fail "checksum mismatch for $Asset`n  expected: $expected`n  actual:   $actual`nThe download may be corrupt or tampered with. Retry; if it persists, report it at https://github.com/$Repo/issues"
    }
}

function Get-InstalledVersion([string]$Exe) {
    if (-not (Test-Path -LiteralPath $Exe)) { return $null }
    try {
        $out = & $Exe --version 2>$null
        if ($out -is [array]) { $out = $out[0] }
        if ($out -match "^[a-z]+ version (.+)$") { return $Matches[1].Trim() }
    } catch {
        # A binary that does not run here reads as an unknown version.
        $null = $_
    }
    return $null
}

function Get-UserPathList {
    $current = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $current) { return @() }
    return @($current.Split(";") | ForEach-Object { $_.Trim() } | Where-Object { $_ })
}

function Test-OnUserPath([string]$Directory) {
    $wanted = $Directory.TrimEnd("\")
    foreach ($entry in Get-UserPathList) {
        if ($entry.TrimEnd("\") -ieq $wanted) { return $true }
    }
    return $false
}

function Add-ToUserPath([string]$Directory) {
    if (Test-OnUserPath $Directory) { return }
    $entries = @(Get-UserPathList) + @($Directory)
    # .NET broadcasts WM_SETTINGCHANGE for the User scope, so terminals opened
    # after this see the new PATH without a sign-out.
    [Environment]::SetEnvironmentVariable("Path", ($entries -join ";"), "User")
}

function Remove-FromUserPath {
    [CmdletBinding(SupportsShouldProcess)]
    param([string]$Directory)
    if (-not (Test-OnUserPath $Directory)) { return }
    if (-not $PSCmdlet.ShouldProcess($Directory, "remove from the user PATH")) { return }
    $wanted = $Directory.TrimEnd("\")
    $entries = @(Get-UserPathList | Where-Object { $_.TrimEnd("\") -ine $wanted })
    [Environment]::SetEnvironmentVariable("Path", ($entries -join ";"), "User")
}

function Test-OnSessionPath([string]$Directory) {
    $wanted = $Directory.TrimEnd("\")
    foreach ($entry in @($env:Path -split ";")) {
        if ($entry.Trim().TrimEnd("\") -ieq $wanted) { return $true }
    }
    return $false
}

function Install-One([string]$Name, [string]$Platform, [string]$Tag, [string]$SumsFile, [string]$InstallDir) {
    $asset = "$Name-$Platform.exe"
    $url = "$BaseUrl/$Tag/$asset"
    $target = Join-Path $InstallDir "$Name.exe"

    $current = Get-InstalledVersion $target
    if ($current -and ("v$current" -eq $Tag)) {
        Write-Note "$Name $Tag is already installed at $target"
        return
    }

    if ($DryRun) {
        Write-Note "would download $url"
        Write-Note "would verify it against $BaseUrl/$Tag/SHA256SUMS"
        if ($current) { Write-Note "would replace $target (v$current) with $Tag" } else { Write-Note "would install $target" }
        return
    }

    # Download next to the destination so the final move stays on one volume.
    $partial = Join-Path $InstallDir ".$Name.download.$PID"
    try {
        Write-Note "downloading $asset $Tag"
        if (-not (Get-Download $url $partial)) {
            Fail "could not download $url`nCheck that $Tag exists at $ReleasesPage and ships a $asset asset."
        }
        Test-Checksum $partial $asset $SumsFile $Tag
        try {
            Move-Item -LiteralPath $partial -Destination $target -Force
        } catch {
            Fail "could not replace $target - if Overcast is running, stop it first (overcast stop) and re-run. $($_.Exception.Message)"
        }
    } finally {
        if (Test-Path -LiteralPath $partial) { Remove-Item -LiteralPath $partial -Force }
    }

    if ($current) { Write-Note "upgraded $Name v$current -> $Tag ($target)" } else { Write-Note "installed $Name $Tag to $target" }
}

function Uninstall-Overcast([string]$InstallDir) {
    $removed = $false
    foreach ($name in @("overcast", "overcastd")) {
        $target = Join-Path $InstallDir "$name.exe"
        if (Test-Path -LiteralPath $target) {
            if ($DryRun) {
                Write-Note "would remove $target"
            } else {
                Remove-Item -LiteralPath $target -Force
                Write-Note "removed $target"
            }
            $removed = $true
        }
    }
    if (-not $removed) { Write-Note "nothing to remove in $InstallDir" }
    if (-not $NoModifyPath) {
        if ($DryRun) {
            if (Test-OnUserPath $InstallDir) { Write-Note "would remove $InstallDir from the user PATH" }
        } else {
            if (Test-OnUserPath $InstallDir) {
                Remove-FromUserPath $InstallDir
                Write-Note "removed $InstallDir from the user PATH"
            }
        }
    }
}

function Main {
    $installDir = $Dir
    if (-not $installDir) { $installDir = Join-Path $env:LOCALAPPDATA "Programs\overcast\bin" }
    $installDir = [IO.Path]::GetFullPath($installDir)

    if ($Uninstall) {
        Uninstall-Overcast $installDir
        return
    }

    Initialize-Http
    # Invoke-WebRequest's progress bar makes a large download take minutes on
    # PowerShell 5.1. Function scope, so the caller's session keeps its own.
    $ProgressPreference = "SilentlyContinue"
    $platform = Get-Platform
    $tag = Resolve-Version
    $which = Get-Flavor

    if ($DryRun) { Write-Note "platform $platform, release $tag, directory $installDir" }

    $sums = $null
    if (-not $DryRun) {
        New-Item -ItemType Directory -Force -Path $installDir | Out-Null
        $sums = Join-Path ([IO.Path]::GetTempPath()) "overcast-SHA256SUMS-$PID"
        if (-not (Get-Download "$BaseUrl/$tag/SHA256SUMS" $sums)) {
            Fail "could not download SHA256SUMS for $tag from $BaseUrl`nCheck that the release exists at $ReleasesPage"
        }
    }

    try {
        foreach ($name in Get-BinaryList $which) {
            Install-One $name $platform $tag $sums $installDir
        }
    } finally {
        if ($sums -and (Test-Path -LiteralPath $sums)) { Remove-Item -LiteralPath $sums -Force }
    }

    if ($NoModifyPath) {
        if (-not (Test-OnSessionPath $installDir)) {
            Write-Note ""
            Write-Note "$installDir is not on your PATH. Add it for this session with:"
            Write-Note "  `$env:Path = `"$installDir;`$env:Path`""
        }
    } elseif ($DryRun) {
        if (-not (Test-OnUserPath $installDir)) { Write-Note "would add $installDir to the user PATH" }
    } else {
        if (-not (Test-OnUserPath $installDir)) {
            Add-ToUserPath $installDir
            Write-Note "added $installDir to the user PATH"
        }
        if (-not (Test-OnSessionPath $installDir)) {
            $env:Path = "$installDir;$env:Path"
        }
    }

    # @(...) keeps a one-element result an array; unwrapped, [0] is the first letter.
    $first = @(Get-BinaryList $which)[0]
    Write-Note ""
    Write-Note "Next:"
    Write-Note "  $first serve            # run the emulator"
    Write-Note "  $first status           # check it"
    Write-Note "  $first env | iex        # point AWS tools in this session at it"
}

Main
