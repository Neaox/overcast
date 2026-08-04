# pr-wait.ps1 -- block until a pull request's checks settle, then report once,
# with the failure detail already fetched.
#
# PowerShell twin of scripts/pr-wait.sh; see that script's header for the full
# rationale (why not a poll loop, why never pipe gh, the output caps). The two
# must stay behaviorally identical.
#
# Usage:
#   scripts\pr-wait.ps1             # the PR for the current branch
#   scripts\pr-wait.ps1 586         # a specific PR
#
# Exit codes: 0 all passed, 1 a check failed, 2 no checks will run, 8 pending
# (or the head moved while watching, making the result stale).

$ErrorActionPreference = "Stop"

# Diagnostics that precede a deliberate exit code go through here, never
# Write-Error: "Stop" promotes Write-Error to a *terminating* error, which
# unwinds before the `exit` on the next line and hands the caller PowerShell's
# generic failure status instead. Every documented non-zero code (2 and 8) was
# reported as -1 that way, which is exactly what the callers switch on.
function Write-Stderr($message) { [Console]::Error.WriteLine($message) }

# gh's stderr must not be an exception. PowerShell 5.1 wraps every line a native
# command *redirects* to stderr in a NativeCommandError, and "Stop" then makes
# it terminating -- so the ordinary outcomes this script is built to handle
# killed it instead: "no checks reported" (the state the wait loop exists to sit
# through), a 404 from the annotations endpoint, a job with no failed-step log.
# Unredirected stderr is untouched, which is why the opening `gh pr view` was
# unaffected and this went unnoticed. The exit code is the signal; the stderr is
# evidence.
#
# The two helpers below take the redirect, so "Continue" is scoped to the call
# that needs it -- assigning $ErrorActionPreference in a function shadows the
# script's value for that function only, and it is restored on return.

# Capture both streams to $path; return gh's exit status.
function Invoke-GhToLog($ghArgs, $path) {
    $ErrorActionPreference = "Continue"
    & gh @ghArgs > $path 2>&1
    return $LASTEXITCODE
}

# Capture stdout as a value, discarding stderr; return $null if gh failed, so
# callers test the result rather than the status.
function Invoke-GhCapture($ghArgs) {
    $ErrorActionPreference = "Continue"
    $out = & gh @ghArgs 2>$null
    if ($LASTEXITCODE -ne 0) { return $null }
    return $out
}

$maxJobs        = if ($env:PR_WAIT_MAX_JOBS) { [int]$env:PR_WAIT_MAX_JOBS } else { 3 }
$maxAnnotations = if ($env:PR_WAIT_MAX_ANNOTATIONS) { [int]$env:PR_WAIT_MAX_ANNOTATIONS } else { 20 }
$maxLogLines    = if ($env:PR_WAIT_MAX_LOG_LINES) { [int]$env:PR_WAIT_MAX_LOG_LINES } else { 40 }
$appearTimeout  = if ($env:PR_WAIT_APPEAR_TIMEOUT) { [int]$env:PR_WAIT_APPEAR_TIMEOUT } else { 180 }

$pr = if ($args.Count -gt 0) { $args[0] } else { $null }

$viewArgs = @("pr", "view")
if ($pr) { $viewArgs += $pr }
$viewArgs += @("--json", "number,state,mergeStateStatus,headRefOid")

$stateJson = & gh @viewArgs
if ($LASTEXITCODE -ne 0 -or -not $stateJson) {
    Write-Stderr "pr-wait: no pull request found$(if ($pr) { " for '$pr'" })"
    exit 2
}
$view = $stateJson | ConvertFrom-Json
$headSha = $view.headRefOid

if ($view.state -ne "OPEN") {
    Write-Output "pr-wait: PR #$($view.number) is $($view.state) -- nothing to wait for."
    exit 0
}

# A conflicting PR dispatches no workflows at all, so --watch would sit there
# until it timed out. See AGENTS.md, "Stacked pull requests".
if ($view.mergeStateStatus -eq "CONFLICTING") {
    Write-Stderr "pr-wait: PR #$($view.number) is CONFLICTING -- GitHub dispatches no checks on it. Rebase or merge main, then run this again."
    exit 2
}

# Best effort: without it the annotations fetch below is skipped, which is not
# worth aborting a settled run over.
$repo = Invoke-GhCapture @("repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
$log = New-TemporaryFile

try {
    Write-Output "pr-wait: PR #$($view.number) at $($headSha.Substring(0,7)) (merge state: $($view.mergeStateStatus)) -- waiting for checks to settle..."

    # Wait for checks to exist: a push GitHub has not picked up yet reports none,
    # and calling --watch on nothing just burns the timeout.
    $waited = 0
    while ($true) {
        $probe = Invoke-GhToLog @("pr", "checks", $view.number) $log.FullName
        if ($probe -eq 0) { break }
        if (-not (Select-String -Path $log.FullName -Pattern "no checks reported" -Quiet)) { break }
        if ($waited -ge $appearTimeout) {
            Write-Stderr "pr-wait: no checks appeared on PR #$($view.number) within ${appearTimeout}s. If the PR is not conflicting, the workflows may be filtered out for these paths."
            exit 2
        }
        Start-Sleep -Seconds 10
        $waited += 10
    }

    # --fail-fast returns at the first failure. Redirect rather than pipe, so
    # the status is gh's own and not the last stage's.
    $status = Invoke-GhToLog @("pr", "checks", $view.number, "--watch", "--fail-fast") $log.FullName

    $lines = Get-Content $log.FullName
    # Watch mode redraws the WHOLE table into the log on every refresh, so the
    # log holds one line per check per refresh -- a 20-minute run reported "2356
    # checks passed" for 42 real ones. And gh lists a check once per triggering
    # workflow event, so a name can legitimately repeat within a single draw.
    # Dedupe on the check name (field 1), which collapses both: the elapsed-time
    # column differs between redraws, so deduping whole lines does not.
    $passed = @($lines | Where-Object { $_ -match "`tpass`t" } |
        ForEach-Object { ($_ -split "`t")[0] } | Sort-Object -Unique).Count
    $failing = @($lines | Where-Object { $_ -match "`t(fail|cancel)`t" } |
        Group-Object { ($_ -split "`t")[0] } | ForEach-Object { $_.Group[0] })

    if ($failing.Count -gt 0) {
        Write-Output "pr-wait: PR #$($view.number) -- FAILING checks:"
        $failing | ForEach-Object { Write-Output $_ }
        Write-Output ""

        # Fetch the evidence so the next step is reading it, not fetching it.
        $detailed = 0
        $seenJobs = @{}
        foreach ($line in $failing) {
            if ($detailed -ge $maxJobs) { break }
            $name = ($line -split "`t")[0]
            $url = ([regex]::Match($line, 'https://\S+')).Value
            if ($url -notmatch '/job/(\d+)') {
                Write-Output "--- ${name}: no job log (not a GitHub Actions check) -- $url"
                continue
            }
            $jobId = $Matches[1]
            # Two checks can share a job (a matrix leg reported under two names).
            if ($seenJobs.ContainsKey($jobId)) { continue }
            $seenJobs[$jobId] = $true
            $detailed++

            Write-Output "=== $name -- annotations ==="
            # The sh twin shapes this with --jq. Not here: Windows PowerShell
            # 5.1 does not escape the quotes inside an argument when it builds
            # a native command line, so that filter reaches gh in pieces and it
            # bails with "unknown shorthand flag: ')'". Shaping the JSON in
            # PowerShell needs no quoting to survive at all. Same output.
            $annJson = if ($repo) {
                Invoke-GhCapture @("api", "repos/$repo/check-runs/$jobId/annotations")
            }
            if ($annJson) {
                # Assign, then enumerate the variable. ConvertFrom-Json hands
                # PS 5.1's pipeline the decoded array as ONE object, so
                # filtering it inline tested the array itself (truthy) and
                # printed every annotation's fields member-enumerated onto a
                # single line: ".github .github .github:7 6 12  ...".
                $annotations = $annJson | ConvertFrom-Json
                $shown = 0
                foreach ($a in @($annotations)) {
                    if ($a.annotation_level -ne "failure") { continue }
                    if ($shown -ge $maxAnnotations) { break }
                    $shown++
                    $annPath = if ($a.path) { $a.path } else { "-" }
                    $annLine = if ($a.start_line) { $a.start_line } else { 0 }
                    Write-Output "  ${annPath}:${annLine}  $($a.message)"
                }
            }

            Write-Output "=== $name -- failing step, last $maxLogLines lines ==="
            $stepLog = Invoke-GhCapture @("run", "view", "--job", $jobId, "--log-failed")
            if ($stepLog) {
                $stepLog | Select-Object -Last $maxLogLines | ForEach-Object { Write-Output $_ }
            } else {
                Write-Output "  (no failed-step log available)"
            }
            Write-Output "    full log: $url"
            Write-Output ""
        }
    }

    if ($failing.Count -gt 0) {
        # --fail-fast returns at the first failure, so the other checks were
        # still running: a low "passed" count is not a second problem to chase.
        Write-Output "pr-wait: stopped at the first failure -- $passed had passed by then, the rest were still running. gh exit status $status"
    } else {
        Write-Output "pr-wait: all $passed checks passed; gh exit status $status"
    }

    # The merge state is what actually gates a merge: a green run can still
    # leave UNSTABLE behind a failing non-required check.
    $finalJson = & gh pr view $view.number --json state,mergeStateStatus,headRefOid
    if ($finalJson) {
        $final = $finalJson | ConvertFrom-Json
        Write-Output "pr-wait: state=$($final.state) mergeState=$($final.mergeStateStatus)"
        if ($final.headRefOid -ne $headSha) {
            Write-Stderr "pr-wait: head moved while watching ($($headSha.Substring(0,7)) -> $($final.headRefOid.Substring(0,7))) -- this result is stale; run again."
            exit 8
        }
    }

    exit $status
} finally {
    Remove-Item $log.FullName -ErrorAction SilentlyContinue
}
