param(
    [Parameter(Mandatory=$true)]
    [string]$workspace
)

# Convert a Windows path into WSL path. Works when this runs from Windows PowerShell.
$wslPath = wsl wslpath -u "$workspace" 2>$null
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($wslPath)) {
    Write-Error "Failed to convert workspace path to WSL path: $workspace"
    exit 1
}

$wslPath = $wslPath.Trim()

# Run the existing init script inside WSL bash.
wsl bash -lc "cd '$wslPath' && bash .devcontainer/init-worktree.sh"
