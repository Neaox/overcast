#!/usr/bin/env bash
# Claude Code/Codex PreToolUse hook: block git stash inside linked worktrees.
#
# Git stores one stash stack in the shared repository rather than one per
# worktree. In a multi-agent repository, an entry created by one checkout can
# therefore be applied or dropped by another. Exit 2 blocks the tool call in
# both Claude Code and Codex.
set -uo pipefail

payload=$(cat || true)
command=$(printf '%s' "$payload" | jq -r '.tool_input.command // empty' 2>/dev/null || true)
[ -n "$command" ] || exit 0

# Match the stash subcommand at the start of a shell command segment. Permit
# Git global options before it without treating an argument such as the ref in
# `git show stash` as a subcommand.
git_global_option='(-[cC][[:space:]]+[^[:space:];&|()]+|--(git-dir|work-tree|namespace|config-env)(=[^[:space:];&|()]+|[[:space:]]+[^[:space:];&|()]+)|-[^[:space:];&|()]+)'
printf '%s' "$command" | grep -Eq "(^|[;&|()][[:space:]]*)git[[:space:]]+(${git_global_option}[[:space:]]+)*stash([[:space:];&|()]|$)" || exit 0

worktree_cwd=$(printf '%s' "$payload" | jq -r '.tool_input.workdir // .cwd // empty' 2>/dev/null || true)
[ -n "$worktree_cwd" ] || worktree_cwd=$PWD

git_dir=$(git -C "$worktree_cwd" rev-parse --path-format=absolute --git-dir 2>/dev/null) || exit 0
common_dir=$(git -C "$worktree_cwd" rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || exit 0
[ "$git_dir" != "$common_dir" ] || exit 0

echo "git stash is blocked in linked worktrees because the stash stack is shared across every worktree. Prefer a temporary WIP commit (amend or squash it before review), or move the changes explicitly to the intended task branch/worktree." >&2
exit 2
