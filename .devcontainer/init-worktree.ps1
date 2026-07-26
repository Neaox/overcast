param(
    [Parameter(Mandatory=$true)]
    [string]$workspace
)

# OVERCAST_DEV_VOLUME_ROOT (optional, per-developer opt-in) -- see the header
# comment in init-worktree.sh. All validation lives in the shell script so the
# two entry points cannot drift; this only has to (a) reject a Windows-style path,
# which the shell script could only report as "not absolute", and (b) forward the
# variable into WSL verbatim.
$volumeRoot = $env:OVERCAST_DEV_VOLUME_ROOT
if (-not [string]::IsNullOrWhiteSpace($volumeRoot)) {
    if ($volumeRoot -match '^[A-Za-z]:[\\/]' -or $volumeRoot.StartsWith('\\')) {
        Write-Error @"
OVERCAST_DEV_VOLUME_ROOT is set to a Windows path:

    $volumeRoot

Docker resolves the bind device inside the Linux VM, so it needs a Linux path,
and a Windows drive reaches that VM over 9p -- whose fsync latency is frequently
WORSE than the spinning disk you are trying to escape. Translating this path for
you would quietly make things slower, so this is an error rather than a warning.

Use a directory under /mnt/wsl/ (the WSL VM's own filesystem), or attach a second
VHDX stored on the fast disk:

    wsl --mount --vhd D:\path\to\overcast-volumes.vhdx --bare

Or unset OVERCAST_DEV_VOLUME_ROOT to fall back to ordinary Docker named volumes.
See CONTRIBUTING.md section "Dev container volumes on a fast disk".
"@
        exit 1
    }

    # WSLENV forwards the variable into the WSL side. No path-translation flag:
    # the value is already a Linux path and must be passed through untouched.
    if ([string]::IsNullOrEmpty($env:WSLENV)) {
        $env:WSLENV = 'OVERCAST_DEV_VOLUME_ROOT'
    }
    elseif ($env:WSLENV -notmatch '(^|:)OVERCAST_DEV_VOLUME_ROOT(/|:|$)') {
        $env:WSLENV = "$($env:WSLENV):OVERCAST_DEV_VOLUME_ROOT"
    }
}

# Convert a Windows path into WSL path. Works when this runs from Windows PowerShell.
# `-e` matters: without it wsl.exe re-parses the argument and eats the backslashes,
# so `wslpath -u` receives "E:devovercast" and fails.
$wslPath = wsl -e wslpath -u "$workspace" 2>$null
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($wslPath)) {
    Write-Error "Failed to convert workspace path to WSL path: $workspace"
    exit 1
}

$wslPath = $wslPath.Trim()

# Run the existing init script inside WSL bash.
& wsl --cd "$wslPath" bash .devcontainer/init-worktree.sh
exit $LASTEXITCODE
