---
name: git-worktrees
description: "Use git worktrees for every mutating Overcast task. Synchronize remote and local main first, isolate each writer in a task-owned worktree under the portable or clone-local configured root, and clean it up after handoff."
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

Then complete [Synchronize Remote and Local Main](#2-synchronize-remote-and-local-main)
before creating the task worktree. Do not create a task worktree from a stale ref.

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

### 2. Synchronize Remote and Local Main

Resolve the primary checkout first, then update its local `main` before deriving
the task branch. This is a required part of worktree creation, not an optional
freshness check.

PowerShell:

```powershell
$commonDir = git rev-parse --path-format=absolute --git-common-dir
$primaryRoot = Split-Path $commonDir -Parent

if ((git -C $primaryRoot branch --show-current) -ne 'main') {
    throw 'primary checkout must be on main before creating a task worktree'
}
if (git -C $primaryRoot status --short) {
    throw 'primary checkout must be clean before updating main'
}

git -C $primaryRoot fetch origin --prune
git -C $primaryRoot pull --ff-only origin main

$localOnly, $remoteOnly = (git -C $primaryRoot rev-list --left-right --count main...origin/main) -split '\s+'
if ($remoteOnly -ne '0') {
    throw "local main is still $remoteOnly commit(s) behind origin/main"
}
```

POSIX:

```bash
common_dir=$(git rev-parse --path-format=absolute --git-common-dir)
primary_root=$(dirname "$common_dir")

test "$(git -C "$primary_root" branch --show-current)" = main || {
  echo 'primary checkout must be on main before creating a task worktree' >&2
  exit 1
}
test -z "$(git -C "$primary_root" status --short)" || {
  echo 'primary checkout must be clean before updating main' >&2
  exit 1
}

git -C "$primary_root" fetch origin --prune
git -C "$primary_root" pull --ff-only origin main

set -- $(git -C "$primary_root" rev-list --left-right --count main...origin/main)
test "$2" -eq 0 || {
  echo "local main is still $2 commit(s) behind origin/main" >&2
  exit 1
}
```

The comparison may report local-only commits in the first column. Keep them:
they are part of the contributor's latest local `main`, and the task worktree
must include both those commits and the fetched remote changes. A non-zero
second column means remote changes are still missing. If the pull cannot
fast-forward because local and remote `main` diverged, stop rather than merging,
rebasing, resetting, or falling back to one side without the user's direction.
Likewise, stop when the primary checkout has uncommitted changes: a new worktree
cannot inherit them safely, so the owner must commit, move, or otherwise resolve
them first.

### 3. Create the Worktree

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
git worktree add $worktreePath -b <branch-name> main
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
git worktree add "$worktree_path" -b <branch-name> main
git worktree lock --reason 'owner=<agent-or-user>; task=<task>; claimed=<ISO-8601>' "$worktree_path"
```

Example:

```powershell
$worktreePath = Join-Path $worktreeRoot 'codex-kinesis-service'
git worktree add $worktreePath -b codex/kinesis-service main
git worktree lock --reason 'owner=codex:<session>; task=kinesis-service; claimed=<ISO-8601>' $worktreePath
```

**Rule:** Two worktrees cannot have the same branch checked out. Each agent must use a unique branch name.

**Naming:** Use a unique, descriptive task name and the repository's required `codex/` branch
prefix unless the user specifies another branch.

**Placement:** Always create worktrees under the resolved worktree root, not inside the repo.
Putting them inside would confuse `go test ./...` and other recursive tools.

**Devcontainer:** Open the new worktree as its own VS Code window and run `Dev Containers: Reopen in Container`. The generated compose project name includes the worktree folder name, so containers and the `web/node_modules` volume are isolated per worktree.

### 4. Install Dependencies

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

### 5. Verify the Worktree Builds

```powershell
Set-Location '<worktree-path>'
go build ./...
go vet ./...
```

Fix any issues before starting work. You should see zero errors — worktrees start from the same commit as the source branch.

### 6. Do Your Work

**First: your shell does not start in your worktree.** See
[Your working directory is not your worktree](#your-working-directory-is-not-your-worktree)
— this is the one thing that has actually caused agents to write into each
other's checkouts, and it defeats every other rule on this page.

Work normally — edit files, write tests, build, iterate. The worktree is a fully independent checkout. All standard project workflows apply:

```bash
# Run targeted tests (fast, no -race — good for iteration)
go test -count=1 ./tests/integration/s3/
go test -count=1 ./internal/services/kinesis/

# Run full test suite before finishing
go test -race -count=1 -timeout=120s ./...
```

**Tests are fully isolated.** Each test creates its own `httptest.Server` on a random OS-assigned port with a fresh `MemoryStore`. Multiple worktrees can run `go test` simultaneously with zero port conflicts.

### 7. Running a Dev Server (If Needed)

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

### 8. Commit Your Work

Commit from inside the worktree as normal:

```powershell
Set-Location '<worktree-path>'
git add <specific-files>
git commit -m "feat(kinesis): add PutRecord and GetRecords"
```

The commit is visible from the main checkout and all other worktrees immediately (they share the object store).

### 9. Publish and Verify the Squash Merge

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

### 10. Mandatory Cleanup

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

## Your working directory is not your worktree

Isolation is a property of the **checkout**, not of the process editing it. A
sub-agent given its own worktree still runs its tools in whatever directory the
session started in — usually the parent's worktree. Nothing enforces the
one-writer-per-worktree rule at the filesystem level; it holds only if every
path you touch actually points where you think.

This is not theoretical. Three agents in a single batch of eight (2026-08-11)
wrote or read outside their worktree. All three self-reported, and no work was
lost, but the isolation did not catch any of them.

### The two mechanisms

**1. The shell starts elsewhere.** The Bash tool's working directory persists
between calls, so one `cd` early does not protect a command that runs after
something else has reset it. One agent ran an entire verification sweep against
the parent's worktree and noticed only when `git commit` reported the wrong
branch — one step short of committing another agent's work onto its own branch.

**2. `Set-Location` and `cd` do not fix .NET or Python.** .NET keeps its own
current directory, independent of PowerShell's. So a relative path passed to a
.NET file API resolves against the parent's worktree *even after* `Set-Location`
succeeds:

```powershell
Set-Location F:\path\to\my-worktree
[IO.File]::WriteAllText("internal/services/x/service.go", $text)  # writes ELSEWHERE
```

That one silently edited a file in another agent's checkout. Python's
`pathlib.Path("relative/path")` has the same trap via the process working
directory, unless the interpreter inherited a `cd` in the same shell invocation.

### Rules

- **Prefix every command** with `cd <absolute worktree path>` (Bash) or
  `Set-Location <absolute worktree path>` (PowerShell). Every command, not once
  at the start.
- **Prefer the Read / Edit / Write tools** for file content. They take absolute
  paths and cannot be ambiguous.
- **Never pass a relative path to a .NET or Python file API.** Absolute only —
  or `os.chdir` first, in the same process.
- **Run `git rev-parse --show-toplevel` and read the answer** before any
  `git add`, `git commit`, `git rm` or `git checkout`. It costs nothing and it
  is the check that caught two of the three incidents.
- **The parent verifies its own tree afterwards.** After a sub-agent finishes,
  run `git rev-parse HEAD`, `git status --porcelain` and a diff against the
  remote in your own worktree before trusting anything it reports. Both writes
  were caught by the agent that made them, not by the agent that owned the tree.

### Write it into the brief

A sub-agent cannot read this file before its first tool call. Every
write-capable brief must state the absolute worktree path and these rules
inline, and must ask the agent to report a suspected stray write plainly rather
than quietly reverting it — the owner of the other tree needs to verify it, not
be told it was handled.

---

## Common Mistakes

| Mistake                                             | Why it fails                                                              | Fix                                                                   |
| --------------------------------------------------- | ------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Branching before synchronizing `main`               | The task omits remote or committed local changes                          | Fast-forward local `main`, then branch from `main`                     |
| Creating worktree inside the repo                   | `go test ./...` picks up nested Go files                                  | Use the resolved external worktree root                               |
| Creating a worktree from a container-local checkout | The new checkout is not host-visible and may disappear with the container | Use the worktree-aware devcontainer or ask the user to reopen with it |
| Two worktrees on the same branch                    | Git forbids this                                                          | Use unique branch names per agent                                     |
| Forgetting `pnpm install` in `web/`                 | TypeScript/Vite builds fail                                               | Run it after creating the worktree                                    |
| Running two dev servers on port 4566                | Port already in use                                                       | Set `OVERCAST_PORT` to a unique value                                 |
| Editing shared SQLite data dir                      | Corrupt or conflicting state                                              | Use `OVERCAST_STATE=memory` or unique `OVERCAST_DATA_DIR`             |
| Sharing a worktree between write-capable agents     | Agents overwrite or commit one another's changes                         | Give every writer a unique branch and worktree                        |
| Running `git stash` in a linked worktree            | The shared stash can be applied or dropped by another agent              | Make a temporary WIP commit or move the changes explicitly            |
| Assuming a tool starts in your worktree             | Shell tools start in the parent session's directory, not the checkout    | Prefix every command with an absolute `cd` / `Set-Location`           |
| Relative path in a .NET or Python file API          | .NET and Python keep their own cwd, unchanged by `Set-Location`          | Absolute paths only, or the Read/Edit/Write tools                     |
| Trusting a sub-agent's "my tree is clean"           | Two stray writes were caught by the writer, never by the tree's owner    | Verify `git status --porcelain` and a remote diff in your own tree     |
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

# Synchronize committed remote and local main changes
if ((git -C $primaryRoot branch --show-current) -ne 'main') {
    throw 'primary checkout must be on main before creating a task worktree'
}
if (git -C $primaryRoot status --short) {
    throw 'primary checkout must be clean before updating main'
}
git -C $primaryRoot fetch origin --prune
git -C $primaryRoot pull --ff-only origin main
$localOnly, $remoteOnly = (git -C $primaryRoot rev-list --left-right --count main...origin/main) -split '\s+'
if ($remoteOnly -ne '0') {
    throw "local main is still $remoteOnly commit(s) behind origin/main"
}

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
git worktree add $worktreePath -b <branch> main
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
test "$(git -C "$primary_root" branch --show-current)" = main || {
  echo 'primary checkout must be on main before creating a task worktree' >&2
  exit 1
}
test -z "$(git -C "$primary_root" status --short)" || {
  echo 'primary checkout must be clean before updating main' >&2
  exit 1
}
git -C "$primary_root" fetch origin --prune
git -C "$primary_root" pull --ff-only origin main
set -- $(git -C "$primary_root" rev-list --left-right --count main...origin/main)
test "$2" -eq 0 || {
  echo "local main is still $2 commit(s) behind origin/main" >&2
  exit 1
}

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
git worktree add "$worktree_path" -b <branch> main
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
