---
name: git-worktrees
description: "Use git worktrees for every mutating Overcast task. Each write-capable agent or sub-agent must work in its own task-owned worktree under the portable or clone-local configured worktree root, and the creating agent must clean it up after handoff."
argument-hint: "Branch name or feature description for the worktree"
---

# Git Worktrees — Isolated Agent Work in Overcast

Use git worktrees to give every write-capable agent its own isolated checkout. This is mandatory
for mutating work, even for a single agent or a small change. All worktrees share a single `.git`
object store, so branches, commits, and history stay in sync.

## Mandatory Rule

Use a dedicated worktree for every task that mutates the repository. The only exceptions are:

- The agent is already running inside a dedicated task worktree.
- The task is strictly read-only.
- The user explicitly asks the agent to edit the primary checkout.

Before editing, inspect the current context:

```powershell
git rev-parse --show-toplevel
git branch --show-current
git worktree list
```

Resolve the worktree root from the primary checkout, not the current linked worktree. The portable
default is `<primary-checkout-parent>/.worktrees/<repo-name>`. A contributor may override it for
their clone with an absolute `git config --local overcast.worktreeRoot <path>` value. Never commit
a machine-specific absolute path.

Every write-capable sub-agent needs a unique worktree and branch. Read-only sub-agents may share a
checkout. Never allow two agents that may write files to share one checkout; if isolation cannot be
created, run their work sequentially instead.

### Claim active worktrees

Use Git's cross-platform worktree lock metadata as an advisory claim and cleanup guard:

```text
git worktree lock --reason "owner=<agent-or-user>; task=<task>; claimed=<ISO-8601>" <worktree-path>
git worktree list --porcelain
```

Use a stable agent session or task identifier when the environment provides one. You may append
`host=<hostname>; pid=<pid>` for diagnosis, but neither value proves liveness: PIDs are reused and
cannot be checked reliably across hosts or containers. Git does not enforce the named owner and a
worktree lock does not prevent another process from editing files. It prevents normal worktree
move/removal and protects the registration from pruning, so all agents must still follow the
one-writer-per-worktree rule. Only the claimant, or a user who has confirmed the claim is stale,
may run `git worktree unlock <worktree-path>`.

Never run `git stash` in a linked worktree. The stash stack belongs to the shared repository, not
to an individual worktree, so another agent can accidentally apply or drop the entry. The
repository's Claude and Codex `PreToolUse` hooks block this command in linked worktrees. Prefer a
temporary WIP commit that you amend or squash before review, or move the changes explicitly to the
intended task branch or worktree.

---

## End-to-End Workflow

### 1. Confirm Worktree-Aware Devcontainer Support

Overcast's devcontainer is worktree-aware. It runs `.devcontainer/init-worktree.sh` on the host before container creation, generates `.devcontainer/docker-compose.yaml`, and mounts Git metadata at the same absolute path Git records in worktree `.git` pointer files.

Agents running inside an existing devcontainer must still verify the current checkout is host-mounted before creating sibling worktrees:

```bash
pwd
git rev-parse --show-toplevel
git worktree list
```

If the checkout is mounted at a host-equivalent absolute path, create worktrees under the resolved
worktree root normally. If that root is not host-visible and the new worktree would be
container-local, stop and ask the user to reopen the repo with the worktree-aware devcontainer
config.

### 2. Create the Worktree

From the main checkout or any existing Overcast worktree:

```powershell
$commonDir = git rev-parse --path-format=absolute --git-common-dir
$primaryRoot = Split-Path $commonDir -Parent
$worktreeRoot = git config --local --get overcast.worktreeRoot
if ($worktreeRoot -and -not [IO.Path]::IsPathRooted($worktreeRoot)) {
    throw 'overcast.worktreeRoot must be an absolute path'
}
if (-not $worktreeRoot) {
    $repoName = Split-Path $primaryRoot -Leaf
    $worktreeRoot = Join-Path (Split-Path $primaryRoot -Parent) (Join-Path '.worktrees' $repoName)
}
New-Item -ItemType Directory -Force -Path $worktreeRoot | Out-Null
$worktreePath = Join-Path $worktreeRoot '<task-name>'
git worktree add $worktreePath -b <branch-name> <base-ref>
git worktree lock --reason 'owner=<agent-or-user>; task=<task>; claimed=<ISO-8601>' $worktreePath
```

POSIX shells use the same resolution rule:

```bash
common_dir=$(git rev-parse --path-format=absolute --git-common-dir)
primary_root=$(dirname "$common_dir")
worktree_root=$(git config --local --get overcast.worktreeRoot || true)
case "$worktree_root" in
  ''|/*|[A-Za-z]:[\\/]*) ;;
  *) echo 'overcast.worktreeRoot must be an absolute path' >&2; exit 1 ;;
esac
if [ -z "$worktree_root" ]; then
  worktree_root="$(dirname "$primary_root")/.worktrees/$(basename "$primary_root")"
fi
mkdir -p "$worktree_root"
worktree_path="$worktree_root/<task-name>"
git worktree add "$worktree_path" -b <branch-name> <base-ref>
git worktree lock --reason 'owner=<agent-or-user>; task=<task>; claimed=<ISO-8601>' "$worktree_path"
```

Example:

```powershell
$worktreePath = Join-Path $worktreeRoot 'codex-kinesis-service'
git worktree add $worktreePath -b codex/kinesis-service origin/main
git worktree lock --reason 'owner=codex:<session>; task=kinesis-service; claimed=<ISO-8601>' $worktreePath
```

**Rule:** Two worktrees cannot have the same branch checked out. Each agent must use a unique branch name.

**Naming:** Use a unique, descriptive task name and the repository's required `codex/` branch
prefix unless the user specifies another branch.

**Placement:** Always create worktrees under the resolved worktree root, not inside the repo.
Putting them inside would confuse `go test ./...` and other recursive tools.

**Devcontainer:** Open the new worktree as its own VS Code window and run `Dev Containers: Reopen in Container`. The generated compose project name includes the worktree folder name, so containers and the `web/node_modules` volume are isolated per worktree.

### 3. Install Dependencies

Go modules are cached globally (`~/go/pkg/mod`) and shared automatically — no action needed.

Node dependencies are gitignored and must be installed per worktree:

```powershell
Set-Location '<worktree-path>'

# Only if your work touches the web UI (pnpm reuses its shared store,
# so this is cheap even in a fresh worktree)
Push-Location web
pnpm install
Pop-Location

# Only if your work touches compat test suites (still npm)
Push-Location compat
npm install
Pop-Location
```

POSIX shell:

```bash
cd '<worktree-path>'
(cd web && pnpm install)       # Only if the task touches the web UI
(cd compat && npm install)     # Only if the task touches compat suites
```

Skip these if your work is purely in Go.

### 4. Verify the Worktree Builds

```powershell
Set-Location '<worktree-path>'
go build ./...
go vet ./...
```

Fix any issues before starting work. You should see zero errors — worktrees start from the same commit as the source branch.

### 5. Do Your Work

Work normally — edit files, write tests, build, iterate. The worktree is a fully independent checkout. All standard project workflows apply:

```bash
# Run targeted tests (fast, no -race — good for iteration)
go test -count=1 ./tests/integration/s3/
go test -count=1 ./internal/services/kinesis/

# Run full test suite before finishing
go test -race -count=1 -timeout=120s ./...
```

**Tests are fully isolated.** Each test creates its own `httptest.Server` on a random OS-assigned port with a fresh `MemoryStore`. Multiple worktrees can run `go test` simultaneously with zero port conflicts.

### 6. Running a Dev Server (If Needed)

If you need a running emulator (e.g. for manual testing or compat suites), you must avoid port collisions with other worktrees.

POSIX shell:

```bash
# Default port is 4566 — pick a unique port per worktree
OVERCAST_PORT=4567 OVERCAST_STATE=memory go run ./cmd/overcast -- serve
```

PowerShell:

```powershell
$env:OVERCAST_PORT = '4567'
$env:OVERCAST_STATE = 'memory'
go run ./cmd/overcast -- serve
```

Or with Docker Compose — override the host port:

POSIX shell:

```bash
OVERCAST_PORT=4567 docker compose -f docker-compose.dev.yml up overcast
```

PowerShell:

```powershell
$env:OVERCAST_PORT = '4567'
docker compose -f docker-compose.dev.yml up overcast
```

| Env var             | Default            | What to change                                    |
| ------------------- | ------------------ | ------------------------------------------------- |
| `OVERCAST_PORT`     | `4566`             | Unique port per worktree                          |
| `OVERCAST_DATA_DIR` | `~/.overcast/data` | Unique dir per worktree if using persistent state |
| `OVERCAST_STATE`    | `hybrid`           | Use `memory` to avoid data dir conflicts entirely |

**Recommended:** Use `OVERCAST_STATE=memory` in worktrees to avoid SQLite file contention entirely. Tests already do this automatically.

### 7. Commit Your Work

Commit from inside the worktree as normal:

```powershell
Set-Location '<worktree-path>'
git add <specific-files>
git commit -m "feat(kinesis): add PutRecord and GetRecords"
```

The commit is visible from the main checkout and all other worktrees immediately (they share the object store).

### 8. Publish and Verify the Squash Merge

Push the branch, open a PR, and use the repository's squash-merge workflow. Squash merge creates a
new commit, so the worktree branch's commit SHA normally never appears in `main`. Do not use
`git merge-base --is-ancestor`, `git branch --merged`, or a search for the branch SHA to decide
whether cleanup is safe.

Verify the PR itself instead:

```powershell
gh pr view <pr-number> --json state,mergedAt,mergeCommit
```

Only treat the work as integrated when the PR reports `MERGED` and provides the squash
`mergeCommit`. If needed, fetch `origin/main` and verify that squash commit, not the branch tip.

### 9. Mandatory Cleanup

The agent that created a worktree owns its cleanup, including worktrees created for sub-agents.
When the task is complete or handed off, first ensure its commits are recoverable from another
checkout (pushed or integrated), then run cleanup from a different checkout:

```text
git -C '<worktree-path>' status --short
# If this task claimed the worktree:
git worktree unlock '<worktree-path>'
git worktree remove '<worktree-path>'
git worktree prune
```

The status output must be empty before removal. Never use force removal to discard dirty or
uncommitted work. After the PR's squash merge is verified, delete the local branch with
`git branch -D <branch-name>`; the safe `-d` form rejects squash-merged branches because their
original commits are not ancestors of `main`. Never force-delete based only on a missing commit—PR
metadata must prove integration. Report any retained worktree and why it was not safe to remove.
Do not clean up worktrees created by another task, user, or agent.

List active worktrees and their claim reasons to see what's still around:

```bash
git worktree list --porcelain
```

#### Strategic housekeeping

- Remove completed or merged task worktrees promptly; they are not useful warm caches.
- Retain at most three worktrees, repo-wide, solely for paused and unmerged tasks that are likely
  to resume. Each retained worktree must be clean, pushed, locked, and have the pause reason in its
  lock reason.
- Skip every locked or dirty worktree during general cleanup. Never decide that a claim is stale
  from its age or PID alone; confirm with its owner or the user.
- For each unlocked, clean candidate, identify its branch and PR. With squash merge, require the PR
  to report `MERGED` and a `mergeCommit`; branch ancestry is not proof.
- Do not automatically delete the oldest worktree to enforce the limit. If more than three paused
  worktrees remain, ask which task should be retired.
- `git worktree prune` only removes stale administrative records for worktree paths that are
  already gone. It is safe housekeeping, but it does not replace the ownership and merge checks
  required before `git worktree remove`.

---

## What's Shared vs. Isolated

| Resource                             | Shared?                   | Notes                                                                                                       |
| ------------------------------------ | ------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `.git` object store                  | Shared                    | Commits, branches, history visible everywhere                                                               |
| Go module cache (`~/go/pkg/mod`)     | Shared                    | Safe — read-only after download                                                                             |
| Go build cache (`~/.cache/go-build`) | Shared                    | Safe, but can cause lock contention under heavy parallel builds                                             |
| `bin/` (compiled binaries)           | Isolated                  | Gitignored — each worktree builds its own                                                                   |
| `web/node_modules/`                  | Isolated in devcontainers | Named volume includes the worktree folder name; host-only checkouts use gitignored per-worktree directories |
| `compat/node_modules/`               | Isolated                  | Gitignored — `npm install` per worktree                                                                     |
| `go.sum`                             | Shared                    | Tracked by git — automatic                                                                                  |
| Test state (`MemoryStore`)           | Isolated                  | Each test creates its own in-process store                                                                  |
| SQLite data (`~/.overcast/data`)     | Shared by default         | Override `OVERCAST_DATA_DIR` if running dev servers                                                         |
| Port `4566`                          | Shared by default         | Override `OVERCAST_PORT` if running dev servers                                                             |

## Avoiding Go Build Cache Contention

If multiple worktrees run heavy parallel builds simultaneously and you see flaky build failures or lock errors:

```bash
# Set a per-worktree build cache
export GOCACHE=$(pwd)/.cache/go-build
```

This is rarely needed — the default shared cache handles concurrent reads well. Only do this if you observe actual contention.

---

## Common Mistakes

| Mistake                                             | Why it fails                                                              | Fix                                                                   |
| --------------------------------------------------- | ------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Creating worktree inside the repo                   | `go test ./...` picks up nested Go files                                  | Use the resolved external worktree root                               |
| Creating a worktree from a container-local checkout | The new checkout is not host-visible and may disappear with the container | Use the worktree-aware devcontainer or ask the user to reopen with it |
| Two worktrees on the same branch                    | Git forbids this                                                          | Use unique branch names per agent                                     |
| Forgetting `pnpm install` in `web/`                 | TypeScript/Vite builds fail                                               | Run it after creating the worktree                                    |
| Running two dev servers on port 4566                | Port already in use                                                       | Set `OVERCAST_PORT` to a unique value                                 |
| Editing shared SQLite data dir                      | Corrupt or conflicting state                                              | Use `OVERCAST_STATE=memory` or unique `OVERCAST_DATA_DIR`             |
| Sharing a worktree between write-capable agents     | Agents overwrite or commit one another's changes                         | Give every writer a unique branch and worktree                        |
| Running `git stash` in a linked worktree            | The shared stash can be applied or dropped by another agent              | Make a temporary WIP commit or move the changes explicitly            |
| Deleting a worktree with uncommitted work           | Work is lost                                                              | Retain it and report why cleanup is blocked                           |
| Looking for a branch SHA after squash merge         | Squash merge creates a different commit on `main`                         | Verify the PR's `MERGED` state and `mergeCommit`                      |
| Forgetting cleanup after integration                | Stale worktrees consume disk and confuse ownership                        | Remove it, delete the verified merged branch, and prune metadata      |

---

## Quick Reference

PowerShell:

```powershell
# Resolve the portable or clone-local configured root
$commonDir = git rev-parse --path-format=absolute --git-common-dir
$primaryRoot = Split-Path $commonDir -Parent
$worktreeRoot = git config --local --get overcast.worktreeRoot
if ($worktreeRoot -and -not [IO.Path]::IsPathRooted($worktreeRoot)) {
    throw 'overcast.worktreeRoot must be an absolute path'
}
if (-not $worktreeRoot) {
    $worktreeRoot = Join-Path (Split-Path $primaryRoot -Parent) `
        (Join-Path '.worktrees' (Split-Path $primaryRoot -Leaf))
}
New-Item -ItemType Directory -Force -Path $worktreeRoot | Out-Null

# Create
$worktreePath = Join-Path $worktreeRoot '<task-name>'
git worktree add $worktreePath -b <branch> <base-ref>
git worktree lock --reason 'owner=<agent-or-user>; task=<task>; claimed=<ISO-8601>' $worktreePath

# List all worktrees
git worktree list

# Remove (after committing/merging)
git worktree unlock $worktreePath
git worktree remove $worktreePath
git worktree prune

# Verify squash integration, then delete the pre-squash branch
gh pr view <pr-number> --json state,mergedAt,mergeCommit
git branch -D <branch>

# Run tests (safe in parallel across worktrees)
go test -count=1 ./tests/integration/<service>/

# Run dev server without port conflicts
$env:OVERCAST_PORT = '<unique>'
$env:OVERCAST_STATE = 'memory'
go run ./cmd/overcast -- serve

# Install Node deps (only if needed)
Push-Location web
pnpm install
Pop-Location
```

POSIX shell:

```bash
common_dir=$(git rev-parse --path-format=absolute --git-common-dir)
primary_root=$(dirname "$common_dir")
worktree_root=$(git config --local --get overcast.worktreeRoot || true)
case "$worktree_root" in
  ''|/*|[A-Za-z]:[\\/]*) ;;
  *) echo 'overcast.worktreeRoot must be an absolute path' >&2; exit 1 ;;
esac
if [ -z "$worktree_root" ]; then
  worktree_root="$(dirname "$primary_root")/.worktrees/$(basename "$primary_root")"
fi

worktree_path="$worktree_root/<task-name>"
mkdir -p "$worktree_root"
git worktree add "$worktree_path" -b <branch> <base-ref>
git worktree lock --reason 'owner=<agent-or-user>; task=<task>; claimed=<ISO-8601>' "$worktree_path"
git worktree list

# After the work is committed and recoverable
git worktree unlock "$worktree_path"
git worktree remove "$worktree_path"
git worktree prune

# After gh confirms the PR was squash-merged
gh pr view <pr-number> --json state,mergedAt,mergeCommit
git branch -D <branch>

# Run tests (safe in parallel across worktrees)
go test -count=1 ./tests/integration/<service>/

# Run dev server without port conflicts
OVERCAST_PORT=<unique> OVERCAST_STATE=memory go run ./cmd/overcast -- serve

# Install Node deps (only if needed)
(cd web && pnpm install)
```
