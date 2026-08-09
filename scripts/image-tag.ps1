# image-tag.ps1 -- PowerShell twin of image-tag.sh: print the Docker tag this
# checkout's images should be built under. See the .sh header for the full
# reasoning; the two must stay behaviorally identical, and
# scripts/image_tag_test.py drives both.
#
# Usage:
#   scripts/image-tag.ps1
#   $env:OVERCAST_IMAGE_TAG = 'foo'; scripts/image-tag.ps1   # validated override
#
# What each case becomes:
#   main                                  -> main
#   claude/handover-documentation-b7930d  -> claude-handover-documentation-b7930d
#   Feature/ADD-Thing                     -> feature-add-thing
#   detached HEAD at abc123def456         -> detached-abc123def456
#   not a git repo / no commits yet       -> local
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

function ConvertTo-DockerTag([string]$Value) {
    $t = $Value.ToLowerInvariant() -replace '[^a-z0-9._-]', '-' -replace '^[.-]+', ''
    if ($t.Length -gt 128) { $t = $t.Substring(0, 128) }
    return $t
}

if ($env:OVERCAST_IMAGE_TAG) {
    # An explicit tag is validated, never quietly rewritten: a caller who asked
    # for one thing and silently got another has no way to notice.
    if ($env:OVERCAST_IMAGE_TAG -notmatch '^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$') {
        [Console]::Error.WriteLine("image-tag.ps1: OVERCAST_IMAGE_TAG='$($env:OVERCAST_IMAGE_TAG)' is not a valid Docker tag.")
        [Console]::Error.WriteLine('  A tag matches [A-Za-z0-9_][A-Za-z0-9._-]{0,127}.')
        exit 2
    }
    Write-Output $env:OVERCAST_IMAGE_TAG
    exit 0
}

# Both git calls here are allowed to fail -- no branch, no commit, no repo at
# all -- so neither may be run under $ErrorActionPreference = 'Stop'. Windows
# PowerShell 5.1 wraps each stderr line of a *native* command in an ErrorRecord
# when it is redirected, so `git ... 2>$null` on a directory that is not a repo
# raises NativeCommandError and kills the script, which is how the "outside a
# git repo" case failed before this helper existed.
function Get-GitOutput([string[]]$GitArgs) {
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'SilentlyContinue'
    try {
        # Not piped into `Select-Object -First 1`: that stops the upstream
        # command early, which leaves $LASTEXITCODE at -1 even when git
        # succeeded, and every branch would then read as "no repo".
        $out = & git @GitArgs 2>$null
        if ($LASTEXITCODE -ne 0) { return '' }
        if ($out -is [array]) { $out = $out[0] }
        return [string]$out
    } finally {
        $ErrorActionPreference = $previous
        $global:LASTEXITCODE = 0
    }
}

# symbolic-ref rather than `rev-parse --abbrev-ref HEAD`: the latter prints the
# literal string "HEAD" on a detached checkout, which would sanitise to a
# perfectly valid tag that every detached worktree on the machine shares -- the
# exact collision this script exists to prevent, wearing a disguise.
$branch = Get-GitOutput @('symbolic-ref', '--quiet', '--short', 'HEAD')

if ($branch) {
    $tag = ConvertTo-DockerTag $branch
} else {
    $sha = Get-GitOutput @('rev-parse', '--short=12', 'HEAD')
    if ($sha) { $tag = ConvertTo-DockerTag "detached-$sha" } else { $tag = '' }
}

if (-not $tag) { $tag = 'local' }
Write-Output $tag
