#!/bin/sh
# pr-wait.sh — block until a pull request's checks settle, then report once,
# with the failure detail already fetched.
#
# Usage:
#   scripts/pr-wait.sh              # the PR for the current branch
#   scripts/pr-wait.sh 586          # a specific PR
#
# Exit codes:
#   0  every check passed
#   1  a check failed — its annotations and failing log lines are printed
#   2  the result is not worth acting on: the PR is CONFLICTING (before the
#      wait, or by the time it ended), closed, or no checks ever appeared
#   8  still pending when gh gave up
#
# Why this exists: `gh pr checks --watch` already does the waiting, in one call,
# with an exit code you can trust. Agents kept reimplementing it as a poll loop
# over `gh pr checks --json` — a request per interval, a notification per
# iteration whether or not anything changed, turning "tell me when CI is done"
# into a running commentary nobody asked for. There is one right way to wait.
#
# Run it in the BACKGROUND (Claude Code: Bash with run_in_background: true) so
# its single completion notification is the only thing the conversation gets.
#
# Three properties worth knowing:
#
#   Fails fast.      --fail-fast returns at the first failing check rather than
#                    waiting out the rest of a doomed run, so you start
#                    investigating minutes earlier.
#   Brings evidence. On failure it fetches each failing job's annotations and
#                    the tail of its failing step, so the first thing you read
#                    is the error rather than a URL to go fetch. Output is
#                    capped (see the limits below) — enough to diagnose, not
#                    enough to flood the conversation.
#   Re-runnable.     After you push a fix, run it again: it waits on the new
#                    head's run. If the head moves while it is watching, it says
#                    so, because the result it just collected is then stale.
#
# Deliberately never pipes gh's output into another command: a pipeline reports
# the LAST command's exit status, which is how PR #410 merged over a failing
# compat check on 2026-07-31.

set -eu

# Output caps. Generous enough to diagnose a normal failure, small enough that a
# pathological one cannot bury the conversation.
MAX_JOBS=${PR_WAIT_MAX_JOBS:-3}          # failing jobs detailed
MAX_ANNOTATIONS=${PR_WAIT_MAX_ANNOTATIONS:-20}
MAX_LOG_LINES=${PR_WAIT_MAX_LOG_LINES:-40}
# How long to keep looking for checks to appear. Right after a push GitHub has
# not dispatched anything yet, and `gh pr checks` reports "no checks".
APPEAR_TIMEOUT=${PR_WAIT_APPEAR_TIMEOUT:-180}
# GitHub computes mergeability asynchronously and reports UNKNOWN until it has;
# asking is what schedules the computation. Look again a couple of times before
# concluding nothing.
MERGEABILITY_TRIES=${PR_WAIT_MERGEABILITY_TRIES:-3}
MERGEABILITY_INTERVAL=${PR_WAIT_MERGEABILITY_INTERVAL:-2}

# summarize_log reads a `gh pr checks` (or --watch) log and prints
#   passed=<count of distinct checks that passed>
# followed by one line per distinct failing check. Split out from the main flow
# so scripts/pr-wait_test.py can exercise it without a network or a PR: this
# parsing has been wrong three separate ways (counting redraws, deduping on
# whole lines that differ only by elapsed time, and double-reporting a check
# listed under two workflow events).
summarize_log() {
    _log=$1
    _tab=$(printf '\t')
    # Watch mode redraws the WHOLE table on every refresh, so the log holds one
    # line per check per refresh, and gh lists a check once per triggering
    # workflow event. Dedupe on the check name (field 1) — the elapsed-time
    # column differs between redraws, so deduping whole lines does not.
    echo "passed=$(grep "${_tab}pass${_tab}" "$_log" | cut -f1 | sort -u | grep -c . || true)"
    grep -E "${_tab}(fail|cancel)${_tab}" "$_log" | awk -F"${_tab}" '!seen[$1]++' || true
}

if [ "${1:-}" = "--summarize-log" ]; then
    summarize_log "$2"
    exit 0
fi

# merge_verdict turns the two merge fields into the decision the guard below
# acts on: "conflicting", "unknown" (GitHub has not computed it yet), or "ok".
#
# Split out so scripts/pr-wait_test.py can exercise it without a live PR,
# because the field names are a trap and this guard was dead for exactly that
# reason. CONFLICTING is a value of `mergeable` (MERGEABLE / CONFLICTING /
# UNKNOWN). It is NOT a member of the MergeStateStatus enum — BEHIND, BLOCKED,
# CLEAN, DIRTY, DRAFT, HAS_HOOKS, UNKNOWN, UNSTABLE — which spells a conflict
# DIRTY. The guard tested mergeStateStatus for CONFLICTING, a value it can
# never hold, so it never fired: on 2026-08-04 PR #395 was genuinely
# conflicting, reported "merge state: DIRTY", sailed past, and went on to
# --watch a PR that dispatches no workflows at all.
#
# Both fields are tested. `mergeable` is the one that actually means this;
# DIRTY is the same fact seen from the other side, and either arriving first is
# enough to stop.
merge_verdict() {
    case "$1:$2" in
        CONFLICTING:*|*:DIRTY) echo conflicting ;;
        UNKNOWN:*|*:UNKNOWN)   echo unknown ;;
        *)                     echo ok ;;
    esac
}

if [ "${1:-}" = "--merge-verdict" ]; then
    merge_verdict "${2:-}" "${3:-}"
    exit 0
fi

pr="${1:-}"

# Resolve gh — native, Git Bash, WSL2, or bust.
#
# Every codepath runs a liveness probe after finding a binary: in WSL2 a
# Windows PE binary on /mnt/c/… may be found on PATH ("gh.exe" directly, or
# "gh" when interop is enabled) but still fail to execute — exit 126 — if the
# binfmt_misc WSLInterop handler is disabled (interop is off in .wslconfig).
# Without the probe the script catches the failure only later, inside a
# command substitution whose stderr is redirected to /dev/null, and reports
# the misleading "no pull request found for <n>" instead of naming the real
# problem.
#
# The probe uses || → $? rather than a bare capture because "set -e" would
# exit the script on the first failure before the capture line ever runs.
#
# Branch A — native gh (Linux, macOS, Git Bash where MSYS2 resolves gh → gh.exe).
if command -v gh >/dev/null 2>&1; then
  gh version >/dev/null 2>&1 || _gh_probe=$?
  if [ "${_gh_probe:-0}" -eq 126 ]; then
    echo "pr-wait: found 'gh' at '$(command -v gh)' but cannot execute it." >&2
    echo "pr-wait: in WSL2, install the Linux-native gh (https://cli.github.com)" >&2
    echo "pr-wait: or enable Windows interop in .wslconfig, or use scripts\\pr-wait.ps1 from PowerShell." >&2
    exit 2
  fi
else
  # Branch B — fall back to gh.exe (WSL2 with the Windows binary on PATH).
  if gh_exe="$(command -v gh.exe 2>/dev/null)"; then
    "$gh_exe" version >/dev/null 2>&1 || _gh_probe=$?
    if [ "${_gh_probe:-0}" -eq 126 ]; then
      echo "pr-wait: found gh.exe at '$gh_exe' but cannot execute it." >&2
      echo "pr-wait: in WSL2, install the Linux-native gh (https://cli.github.com)" >&2
      echo "pr-wait: or enable Windows interop in .wslconfig, or use scripts\\pr-wait.ps1 from PowerShell." >&2
      exit 2
    fi
    gh() { "$gh_exe" "$@"; }
  else
    echo "pr-wait: gh not found. Install it: https://cli.github.com" >&2
    exit 2
  fi
fi

# shellcheck disable=SC2086 # $pr is deliberately unquoted: empty = current branch
state_json=$(gh pr view $pr --json number,state,mergeable,mergeStateStatus,headRefOid 2>/dev/null) || {
    echo "pr-wait: no pull request found${pr:+ for $pr}" >&2
    exit 2
}
number=$(printf '%s' "$state_json" | jq -r .number)
state=$(printf '%s' "$state_json" | jq -r .state)
mergeable=$(printf '%s' "$state_json" | jq -r .mergeable)
merge_state=$(printf '%s' "$state_json" | jq -r .mergeStateStatus)
head_sha=$(printf '%s' "$state_json" | jq -r .headRefOid)

if [ "$state" != "OPEN" ]; then
    echo "pr-wait: PR #$number is $state — nothing to wait for."
    exit 0
fi

# A conflicting PR dispatches no workflows at all, so --watch would sit there
# until it timed out. See AGENTS.md § Stacked pull requests.
verdict=$(merge_verdict "$mergeable" "$merge_state")
tries=0
while [ "$verdict" = "unknown" ] && [ "$tries" -lt "$MERGEABILITY_TRIES" ]; do
    sleep "$MERGEABILITY_INTERVAL"
    tries=$((tries + 1))
    recheck=$(gh pr view "$number" --json mergeable,mergeStateStatus 2>/dev/null) || break
    mergeable=$(printf '%s' "$recheck" | jq -r .mergeable)
    merge_state=$(printf '%s' "$recheck" | jq -r .mergeStateStatus)
    verdict=$(merge_verdict "$mergeable" "$merge_state")
done

if [ "$verdict" = "conflicting" ]; then
    echo "pr-wait: PR #$number is CONFLICTING — GitHub dispatches no checks on it." >&2
    echo "pr-wait: rebase or merge main into the branch, then run this again." >&2
    exit 2
fi

repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)
log=$(mktemp)
# shellcheck disable=SC2064 # expand $log now, not at trap time
trap "rm -f '$log'" EXIT

# Both merge fields, because printing only mergeStateStatus is what let a
# CONFLICTING PR read as a merely-DIRTY one for as long as it did.
echo "pr-wait: PR #$number at ${head_sha%"${head_sha#???????}"} (mergeable: $mergeable, merge state: $merge_state) — waiting for checks to settle…"

# Wait for checks to exist. A push that has not been picked up yet reports none,
# and calling --watch on nothing just burns the timeout.
waited=0
while :; do
    if gh pr checks "$number" >"$log" 2>&1; then
        break
    fi
    # "no checks reported" is the only retryable failure here; anything else
    # (auth, network, bad PR) should surface immediately.
    grep -qi 'no checks reported' "$log" || break
    if [ "$waited" -ge "$APPEAR_TIMEOUT" ]; then
        echo "pr-wait: no checks appeared on PR #$number within ${APPEAR_TIMEOUT}s." >&2
        echo "pr-wait: if the PR is not conflicting, the workflows may be filtered out for these paths." >&2
        exit 2
    fi
    sleep 10
    waited=$((waited + 10))
done

# --fail-fast returns at the first failure. Redirect rather than pipe, so $? is
# gh's own status.
set +e
gh pr checks "$number" --watch --fail-fast >"$log" 2>&1
status=$?
set -e

# First line is "passed=<n>", the rest are the distinct failing checks.
# Deliberately not ${summary%%...} parameter expansion: command substitution
# strips trailing newlines, so $(printf '\n') is the empty string and the
# pattern silently becomes "*", which eats the whole value.
summary=$(summarize_log "$log")
passed=$(printf '%s\n' "$summary" | head -n 1)
passed=${passed#passed=}
failing=$(printf '%s\n' "$summary" | tail -n +2)

if [ -n "$failing" ]; then
    echo "pr-wait: PR #$number — FAILING checks:" >&2
    echo "$failing" >&2
    echo >&2

    # Fetch the evidence so the next step is reading it, not fetching it.
    detailed=0
    seen_jobs=""
    echo "$failing" | while IFS= read -r line; do
        [ "$detailed" -lt "$MAX_JOBS" ] || break
        url=$(printf '%s' "$line" | grep -oE 'https://[^[:space:]]+' | head -1)
        name=$(printf '%s' "$line" | cut -f1)
        case "$url" in
            *"/job/"*) job_id=${url##*/job/} ;;
            *) echo "--- $name: no job log (not a GitHub Actions check) — $url" >&2; continue ;;
        esac
        job_id=${job_id%%[!0-9]*}
        [ -n "$job_id" ] || continue
        # Two checks can share a job (a matrix leg reported under two names).
        case " $seen_jobs " in *" $job_id "*) continue ;; esac
        seen_jobs="$seen_jobs $job_id"
        detailed=$((detailed + 1))

        echo "=== $name — annotations ===" >&2
        if [ -n "$repo" ]; then
            gh api "repos/$repo/check-runs/$job_id/annotations" \
                --jq '.[] | select(.annotation_level == "failure") | "  \(.path // "-"):\(.start_line // 0)  \(.message)"' \
                2>/dev/null | head -n "$MAX_ANNOTATIONS" >&2 || true
        fi

        echo "=== $name — failing step, last $MAX_LOG_LINES lines ===" >&2
        gh run view --job "$job_id" --log-failed 2>/dev/null | tail -n "$MAX_LOG_LINES" >&2 || \
            echo "  (no failed-step log available)" >&2
        echo "    full log: $url" >&2
        echo >&2
    done
fi

if [ -n "$failing" ]; then
    # --fail-fast returns at the first failure, so the other checks were still
    # running: a low "passed" count here is not a second problem to chase.
    echo "pr-wait: stopped at the first failure — $passed had passed by then, the rest were still running. gh exit status $status"
else
    echo "pr-wait: all $passed checks passed; gh exit status $status"
fi

# The merge state is what actually gates a merge: a green run can still leave
# UNSTABLE behind a failing non-required check.
final=$(gh pr view "$number" --json state,mergeable,mergeStateStatus,headRefOid 2>/dev/null || true)
if [ -n "$final" ]; then
    now_state=$(printf '%s' "$final" | jq -r .state)
    now_mergeable=$(printf '%s' "$final" | jq -r .mergeable)
    now_merge_state=$(printf '%s' "$final" | jq -r .mergeStateStatus)
    now_sha=$(printf '%s' "$final" | jq -r .headRefOid)
    printf 'pr-wait: state=%s mergeable=%s mergeState=%s\n' \
        "$now_state" "$now_mergeable" "$now_merge_state"
    if [ "$now_sha" != "$head_sha" ]; then
        echo "pr-wait: head moved while watching (${head_sha%"${head_sha#???????}"} → ${now_sha%"${now_sha#???????}"}) — this result is stale; run again." >&2
        exit 8
    fi

    # The guard at the top only sees the PR as it was before the wait. A PR
    # goes conflicting mid-watch whenever main moves under it, and nothing so
    # far would say so: that does not touch this PR's head, so the stale-head
    # check above cannot fire, and it dispatches no new checks, so --watch
    # returns its usual green. The run then ended on a PR that will not merge
    # and reported success — with auto-merge armed, it simply never merges and
    # nothing tells you why.
    #
    # Only for a PR still OPEN. A PR that merged while we watched — the normal
    # happy ending, and what PR #603 did on 2026-08-04 — answers state=MERGED
    # with mergeState=UNKNOWN, which must not read as trouble.
    if [ "$now_state" = "OPEN" ]; then
        case $(merge_verdict "$now_mergeable" "$now_merge_state") in
            conflicting)
                echo "pr-wait: PR #$number went CONFLICTING while watching — main moved under it." >&2
                echo "pr-wait: the checks above ran against the old base; rebase or merge main, then run this again." >&2
                # Only when the checks themselves were fine. A real failure is
                # the more actionable signal and its evidence is already above,
                # so exit 1 stands and this stays a warning. Spelled as an if,
                # not an && chain: under `set -e` a chain whose test fails is
                # itself a failing command, and would exit 1 here rather than
                # falling through to the real status.
                if [ "$status" -eq 0 ] && [ -z "$failing" ]; then
                    exit 2
                fi
                ;;
        esac
    fi
fi

exit "$status"
