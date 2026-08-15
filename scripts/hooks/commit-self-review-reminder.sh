#!/usr/bin/env bash
# Claude Code/Codex PreToolUse hook: remind the agent to self-review its own commit.
#
# This runs no checks and blocks nothing — scripts/verify-changed.sh is the gate, this is
# the nudge. It exists because a plain `echo` from a PreToolUse hook reaches nobody: on
# exit 0, stdout that is not JSON goes to the debug log only, not the transcript and not
# the model. Emitting hookSpecificOutput.additionalContext is the one way to put text in
# the agent's context without blocking the tool call.
#
# additionalContext lands in the *next* model request, so the reminder arrives just after
# the commit rather than ahead of it. That is why the text asks for a review of HEAD and
# points at `git commit --amend` as the remedy: the commit is cheap to correct while the
# branch is still yours, and an amend keeps the history coherent for the reviewer.
#
# See AGENTS.md § Self-review the diff before committing or pushing.
#
# # Why this reads the command rather than trusting the hook condition
#
# The settings.json entry carries `if: "Bash(git commit *)"`, and that ought to
# be the whole story. It was not: with the condition written `Bash(git commit*)`
# — no space, matching neither the `cmd *` nor the `cmd:*` form used everywhere
# else in this repo — the hook ran on every Bash call the agent made. `ls`,
# `gh pr view` and `grep` all came back carrying a reminder to amend a commit
# that did not exist, and a read-only subagent doing nothing but searching was
# told to `git commit --amend` and `git push --force-with-lease`. It declined,
# and said so, which is the only reason it was noticed.
#
# So the gate lives here too. What this hook emits is imperative text that lands
# in an agent's context, and that should not depend on a condition parsing the
# way its author expected.
#
# The default is silence. Where verify-changed.sh fails toward doing its work —
# the cost of a needless run is some CPU — this one fails toward saying nothing,
# because the cost of a needless run is instructions appearing in an agent that
# never asked for them.
set -uo pipefail

payload=$(cat 2>/dev/null || true)
command=$(printf '%s' "$payload" | jq -r '.tool_input.command // empty' 2>/dev/null || true)
[ -n "$command" ] || exit 0

# Match `git commit` at the start of a command segment, allowing Git's global
# options in between (`git -C dir commit`). Mirrors reject-worktree-stash.sh.
git_global_option='(-[cC][[:space:]]+[^[:space:];&|()]+|--(git-dir|work-tree|namespace|config-env)(=[^[:space:];&|()]+|[[:space:]]+[^[:space:];&|()]+)|-[^[:space:];&|()]+)'
printf '%s' "$command" |
	grep -Eq "(^|[;&|()][[:space:]]*)git[[:space:]]+(${git_global_option}[[:space:]]+)*commit([[:space:];&|()]|$)" || exit 0

cat <<'JSON'
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "additionalContext": "Self-review this commit (AGENTS.md § Self-review the diff): read `git show --stat HEAD` and `git show HEAD` end to end and judge each hunk on whether it earns its place in this change. Look for debug leftovers (stray prints, commented-out code, t.Skip, scratch files), dead ends from abandoned approaches, helpers duplicated instead of reused from serviceutil, churn that nets to nothing, unrelated edits swept in by `git add .`, and stale comments or plan docs. Check completeness too: both state.Store implementations, provisioner.go registration, the .changelog/ fragment, the failing-test-first. This matters most on long sessions, where your memory of the branch is a summary and the diff is the fact. If you find something, fix it and `git commit --amend` so the branch stays coherent — do not leave it for the reviewer or narrate it as a known issue. Amending is fine even after pushing a branch you own and nobody shares, with `git push --force-with-lease`."
  }
}
JSON
