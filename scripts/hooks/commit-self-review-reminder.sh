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
set -euo pipefail

cat <<'JSON'
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "additionalContext": "Self-review this commit (AGENTS.md § Self-review the diff): read `git show --stat HEAD` and `git show HEAD` end to end and judge each hunk on whether it earns its place in this change. Look for debug leftovers (stray prints, commented-out code, t.Skip, scratch files), dead ends from abandoned approaches, helpers duplicated instead of reused from serviceutil, churn that nets to nothing, unrelated edits swept in by `git add .`, and stale comments or plan docs. Check completeness too: both state.Store implementations, provisioner.go registration, the .changelog/ fragment, the failing-test-first. This matters most on long sessions, where your memory of the branch is a summary and the diff is the fact. If you find something, fix it and `git commit --amend` so the branch stays coherent — do not leave it for the reviewer or narrate it as a known issue. Amending is fine even after pushing a branch you own and nobody shares, with `git push --force-with-lease`."
  }
}
JSON
