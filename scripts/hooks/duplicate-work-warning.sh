#!/usr/bin/env bash
# Claude Code/Codex PreToolUse hook: say something before a branch becomes a
# second pull request for an issue somebody else has already claimed.
#
# On 2026-08-22 two agents worked issue #1325 in parallel. The second finished a
# complete fix — root cause, mock-clock regressions red-before/green-after, 20x
# under -race — pushed, and opened PR #1351 with auto-merge armed, 25 minutes
# after PR #1331 had already merged the same fix. GitHub reporting the branch as
# conflicting was the first sign. Auto-merge is what makes that expensive: a
# duplicate can merge itself while nobody is looking.
#
# This warns, it does not block, for the reason a maintainer gave when it was
# proposed: a pull request may reference an issue to contribute a piece of it,
# or to cite it, without claiming the whole thing. scripts/issue-claim.sh keeps
# those apart — only a closing reference (GitHub's own closingIssuesReferences,
# populated solely by a Closes/Fixes keyword) counts as a claim — but "someone
# else already closes this issue" is still a judgement about two changes being
# the same change, and that judgement belongs to whoever can read both diffs.
# Blocking would also need an escape hatch, and an escape hatch on a heuristic
# is a thing agents learn to reach for.
#
# Emitting hookSpecificOutput rather than echoing: a plain echo from a
# PreToolUse hook reaches only the debug log. This lands in the next model
# request, which is in time to matter — the push may already have happened, but
# the pull request has not, and closing a branch is cheaper than un-merging one.
#
# See AGENTS.md § Claiming an issue.
set -euo pipefail

# Drain the tool-call payload. Nothing here needs it — the branch name carries
# the issue number — but leaving it unread risks EPIPE on the writer.
cat >/dev/null 2>&1 || true

repo_root=$(git rev-parse --show-toplevel 2>/dev/null || true)
[ -n "$repo_root" ] || exit 0
[ -x "$repo_root/scripts/issue-claim.sh" ] || [ -f "$repo_root/scripts/issue-claim.sh" ] || exit 0

# --check never mutates: a hook must not label an issue as a side effect of a
# push. Claiming is a deliberate act at pickup time, not a thing that happens to
# you. stderr carries the report; stdout here is reserved for the hook protocol.
set +e
report=$(bash "$repo_root/scripts/issue-claim.sh" --check 2>&1)
status=$?
set -e

# 0 covers clear, not-an-issue-branch, and could-not-check. Only 3 is a finding.
[ "$status" -eq 3 ] || exit 0

# jq is what issue-claim.sh needs to have got this far, so it is present.
jq -n --arg report "$report" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    additionalContext: (
      "Before this branch becomes a pull request, read this — it looks like work someone else has already claimed:\n\n"
      + $report
      + "\n\nA PR listed above declares it CLOSES this issue (a Closes/Fixes keyword, not a passing mention), and it is open or already merged. That is the shape of the #1325 collision: two agents, two complete fixes for one window, the second opened with auto-merge armed 25 minutes after the first had merged.\n\nDo this before opening or updating a PR:\n1. Read the other PR. `gh pr view <n>` and `gh pr diff <n>`.\n2. Decide whether your change is the same change. If it is, do not open a PR. Close the branch and report what you found — including anything of yours worth keeping.\n3. If the other PR has merged, check whether your work still applies to main at all: `git checkout origin/main -- <the file you changed>` and run only your new tests. A test that passes against main is not a regression test, it is a duplicate.\n4. If your change IS genuinely distinct — a different window, a follow-up, a piece the other PR left — say so explicitly in your PR body, naming the other PR and what it does not cover. Then carry on; this is a warning, not a veto.\n\nIf you are the author of the PR listed above and this is the same effort continuing, ignore this."
    )
  }
}'
