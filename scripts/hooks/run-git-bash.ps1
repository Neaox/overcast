param(
    [Parameter(Mandatory = $true)]
    [string]$ScriptPath
)

$ErrorActionPreference = 'Stop'

function Add-BashCandidates {
    param(
        [System.Collections.Generic.List[string]]$Candidates,
        [string]$StartPath
    )

    if (-not $StartPath) {
        return
    }

    $cursor = $StartPath
    if (-not (Test-Path -LiteralPath $cursor -PathType Container)) {
        $cursor = Split-Path -Parent $cursor
    }

    for ($depth = 0; $depth -lt 5 -and $cursor; $depth++) {
        $Candidates.Add((Join-Path $cursor 'bash.exe'))
        $Candidates.Add((Join-Path $cursor 'bin\bash.exe'))
        $Candidates.Add((Join-Path $cursor 'usr\bin\bash.exe'))
        $cursor = Split-Path -Parent $cursor
    }
}

$candidates = [System.Collections.Generic.List[string]]::new()
if ($env:GIT_BASH) {
    $candidates.Add($env:GIT_BASH)
}

$gitCommand = Get-Command git.exe -ErrorAction Stop
Add-BashCandidates -Candidates $candidates -StartPath $gitCommand.Source

$gitExecPath = (& $gitCommand.Source --exec-path).Trim()
Add-BashCandidates -Candidates $candidates -StartPath $gitExecPath

$gitBash = $candidates |
    Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } |
    Select-Object -Unique -First 1

if (-not $gitBash) {
    throw 'Could not locate Git for Windows Bash from the active git.exe. Set GIT_BASH to its absolute path.'
}

$env:OVERCAST_HOOK_SCRIPT = (Resolve-Path -LiteralPath $ScriptPath).Path.Replace('\', '/')
$payload = [Console]::In.ReadToEnd()

$payload | & $gitBash -lc 'bash "$OVERCAST_HOOK_SCRIPT"'
exit $LASTEXITCODE
