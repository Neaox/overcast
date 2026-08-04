#!/usr/bin/env bash
# Codex PreToolUse adapter for the repository's Claude Code shell hooks.
#
# Codex does not support Claude Code's per-handler `if` field, so this script
# dispatches by the Bash command in the hook payload and reuses the existing
# hook scripts. Unknown or malformed payloads are deliberately allowed.
set -uo pipefail

payload=$(cat || true)
command=$(printf '%s' "$payload" | jq -r '.tool_input.command // empty' 2>/dev/null || true)

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd) || exit 0
repo_root=$(git -C "$script_dir" rev-parse --show-toplevel 2>/dev/null) || exit 0

printf '%s' "$payload" | bash "$script_dir/reject-worktree-stash.sh"
status=$?
[ "$status" -eq 0 ] || exit "$status"

if printf '%s' "$command" | grep -Eq '^[[:space:]]*git[[:space:]]+commit([[:space:]]|$)'; then
  exec "$script_dir/commit-self-review-reminder.sh"
fi

if printf '%s' "$command" | grep -Eq '^[[:space:]]*git[[:space:]]+push([[:space:]]|$)'; then
  cd "$repo_root" || exit 0
  exec "$repo_root/scripts/verify-changed.sh"
fi

printf '%s' "$payload" | exec "$script_dir/pr-checks-poll-loop-warning.sh"
