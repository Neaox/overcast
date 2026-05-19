---
name: git-worktrees
description: "Use git worktrees for parallel multi-agent development. Use when: implementing a large feature that touches many files and other agents are or will be working simultaneously on the same repo. Not needed when working alone, making small changes, or editing only a few files."
argument-hint: "Branch name or feature description for the worktree"
---

# Git Worktrees — Parallel Agent Work in Overcast

Use git worktrees to give each agent its own isolated checkout of the repo so multiple agents can edit files, build, and run tests simultaneously without interfering with each other. All worktrees share a single `.git` object store, so branches, commits, and history stay in sync.

## When to Use

- **Use worktrees** when multiple agents will work at the same time on changes that touch overlapping or many files — large features, cross-cutting refactors, parallel service implementations.
- **Skip worktrees** when you are the only agent working, or making a small change to one or a few files. The overhead is not worth it.

## Decision Checklist

Before creating a worktree, confirm at least two of these are true:

- [ ] Other agents are (or will be) working on the same repo concurrently
- [ ] The change touches more than ~5 files across multiple packages
- [ ] You expect the work to take multiple steps with intermediate builds/tests
- [ ] The change might conflict with other in-progress work on `main`

If fewer than two apply, work directly in the main checkout.

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

If the checkout is mounted at a host-equivalent absolute path, create sibling worktrees normally. If the checkout is only mounted at an isolated container path and the new sibling would be container-local, stop and ask the user to reopen the repo with the worktree-aware devcontainer config.

### 2. Create the Worktree

From the main checkout or any existing Overcast worktree:

```bash
# Pick a descriptive branch name — use your task or feature as the suffix
git worktree add ../worktree-<name> -b <branch-name>
```

Examples:

```bash
git worktree add ../worktree-kinesis -b feat/kinesis-service
git worktree add ../worktree-s3-versioning -b feat/s3-versioning
git worktree add ../worktree-fix-sqs-visibility -b fix/sqs-visibility-timeout
```

**Rule:** Two worktrees cannot have the same branch checked out. Each agent must use a unique branch name.

**Placement:** Always create worktrees as siblings of the current checkout (i.e. `../worktree-<name>`), not inside the repo. Putting them inside would confuse `go test ./...` and other recursive tools.

**Devcontainer:** Open the new worktree as its own VS Code window and run `Dev Containers: Reopen in Container`. The generated compose project name includes the worktree folder name, so containers and the `web/node_modules` volume are isolated per worktree.

### 3. Install Dependencies

Go modules are cached globally (`~/go/pkg/mod`) and shared automatically — no action needed.

Node dependencies are gitignored and must be installed per worktree:

```bash
cd ../worktree-<name>

# Only if your work touches the web UI
cd web && npm install && cd ..

# Only if your work touches compat test suites
cd compat && npm install && cd ..
```

Skip these if your work is purely in Go.

### 4. Verify the Worktree Builds

```bash
cd ../worktree-<name>
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

If you need a running emulator (e.g. for manual testing or compat suites), you must avoid port collisions with other worktrees:

```bash
# Default port is 4566 — pick a unique port per worktree
OVERCAST_PORT=4567 OVERCAST_STATE=memory go run ./cmd/overcast -- serve
```

Or with Docker Compose — override the host port:

```bash
OVERCAST_PORT=4567 docker compose -f docker-compose.dev.yml up overcast
```

| Env var             | Default            | What to change                                    |
| ------------------- | ------------------ | ------------------------------------------------- |
| `OVERCAST_PORT`     | `4566`             | Unique port per worktree                          |
| `OVERCAST_DATA_DIR` | `~/.overcast/data` | Unique dir per worktree if using persistent state |
| `OVERCAST_STATE`    | `hybrid`           | Use `memory` to avoid data dir conflicts entirely |

**Recommended:** Use `OVERCAST_STATE=memory` in worktrees to avoid SQLite file contention entirely. Tests already do this automatically.

### 7. Commit Your Work

Commit from inside the worktree as normal:

```bash
cd ../worktree-<name>
git add -A
git commit -m "feat(kinesis): add PutRecord and GetRecords"
```

The commit is visible from the main checkout and all other worktrees immediately (they share the object store).

### 8. Merge Back

When your work is ready, merge into the target branch from the main checkout or target worktree:

```bash
git merge feat/kinesis-service
```

Or if you prefer rebase:

```bash
git rebase main feat/kinesis-service
git checkout main
git merge --ff-only feat/kinesis-service
```

### 9. Clean Up

Remove the worktree when done:

```bash
git worktree remove ../worktree-<name>

# Delete the branch if it's been merged
git branch -d feat/kinesis-service
```

List active worktrees to see what's still around:

```bash
git worktree list
```

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
| Creating worktree inside the repo                   | `go test ./...` picks up nested Go files                                  | Create as siblings: `../worktree-<name>`                              |
| Creating a worktree from a container-local checkout | The new checkout is not host-visible and may disappear with the container | Use the worktree-aware devcontainer or ask the user to reopen with it |
| Two worktrees on the same branch                    | Git forbids this                                                          | Use unique branch names per agent                                     |
| Forgetting `npm install` in `web/`                  | TypeScript/Vite builds fail                                               | Run it after creating the worktree                                    |
| Running two dev servers on port 4566                | Port already in use                                                       | Set `OVERCAST_PORT` to a unique value                                 |
| Editing shared SQLite data dir                      | Corrupt or conflicting state                                              | Use `OVERCAST_STATE=memory` or unique `OVERCAST_DATA_DIR`             |
| Deleting a worktree with uncommitted work           | Work is lost                                                              | Always commit or stash before removing                                |

---

## Quick Reference

```bash
# Create
git worktree add ../worktree-<name> -b <branch>

# List all worktrees
git worktree list

# Remove (after committing/merging)
git worktree remove ../worktree-<name>

# Run tests (safe in parallel across worktrees)
go test -count=1 ./tests/integration/<service>/

# Run dev server without port conflicts
OVERCAST_PORT=<unique> OVERCAST_STATE=memory go run ./cmd/overcast -- serve

# Install Node deps (only if needed)
cd web && npm install
```
