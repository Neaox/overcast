#!/usr/bin/env bash
# Claude Code/Codex PreToolUse hook: catch a hand-rolled CI poll loop before it runs.
#
# The anti-pattern is a `while`/`until` loop around `gh pr checks --json` on a
# `sleep` interval, usually wired to a per-iteration notifier. It costs a request
# per tick, fires whether or not anything changed, and turns "tell me when CI is
# done" into a running commentary of "N passed" messages that change no decision.
# `gh pr checks` already has `--watch`; scripts/pr-wait.sh wraps it.
#
# This warns, it does not block. A one-shot `gh pr checks <n>` is perfectly
# legitimate — reading the current state is not polling — so the trigger is the
# *combination* of a gh checks call with loop-and-sleep machinery. Blocking on a
# heuristic that narrow would cost more in false positives than it saves.
#
# Like commit-self-review-reminder.sh, it emits hookSpecificOutput because a
# plain echo from a PreToolUse hook reaches only the debug log. The text lands in
# the next model request, which is early enough: the agent can abandon the loop
# it just started and switch to the script.
#
# See AGENTS.md § Waiting on CI and the pull-request skill § After Opening.
set -euo pipefail

# The hook receives the tool call as JSON on stdin. Read it defensively: an
# unparseable payload must never fail the tool call.
payload=$(cat || true)

command=$(printf '%s' "$payload" | jq -r '.tool_input.command // empty' 2>/dev/null || true)
[ -n "$command" ] || exit 0

# Already doing it right? Say nothing.
printf '%s' "$command" | grep -q -- '--watch' && exit 0
printf '%s' "$command" | grep -q 'pr-wait' && exit 0

# Needs both halves: a checks query AND loop/sleep machinery.
printf '%s' "$command" | grep -Eq 'gh +pr +checks' || exit 0
printf '%s' "$command" | grep -Eq '(^|[^[:alnum:]_])(while|until|for)([^[:alnum:]_]|$)|sleep ' || exit 0

cat <<'JSON'
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "additionalContext": "This looks like a hand-rolled CI poll loop around `gh pr checks`. Don't. `gh pr checks` already has `--watch --fail-fast`, and `scripts/pr-wait.sh <pr>` wraps it: it blocks until every check settles, prints only the non-passing lines plus a tally, re-reads mergeStateStatus, and exits 0/1/2/8 (passed / failed / no checks will run / pending). Run it with the Bash tool and run_in_background: true, so you get exactly ONE completion notification instead of a message per interval. A poll loop costs a request per tick, fires whether or not anything changed, and produces a play-by-play that changes no decision — the user has objected to precisely this. Also: never pipe an exit-code-bearing gh command into another, since the pipeline reports the last command's status (that is how PR #410 merged over a failing compat check). See AGENTS.md § Waiting on CI. If you genuinely need a one-shot read of current state rather than a wait, drop the loop and just call `gh pr checks <n>` once."
  }
}
JSON
