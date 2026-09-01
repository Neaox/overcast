---
name: stacked-prs
description: "Build and land a chain of dependent pull requests, where each PR targets the branch below it rather than main. Use when: a change is too large for one PR, a second piece of work needs code from a PR that has not merged yet, several agents are building on one another's branches, or a PR is blocked because its dependency is still open."
compatibility: opencode
metadata:
  audience: contributors
  workflow: review
  languages: "go,typescript,markdown"
argument-hint: "The branches or PR numbers to stack, bottom first"
license: MIT
---

# Stacked Pull Requests — Overcast

A stack is two or more pull requests in this repository where each targets the
branch below it: the bottom PR targets `main`, the next targets the bottom
PR's branch, and so on. GitHub understands stacks natively — it shows the chain
on each PR, runs CI on **every** layer rather than only the bottom, and
rebases and retargets the branches above when the bottom merges.

Read [pull-request](../pull-request/SKILL.md) for PR hygiene and the changelog
decision, and [git-worktrees](../git-worktrees/SKILL.md) for the isolated
checkouts parallel agents work in. This skill covers only what stacking adds.

---

## When to stack

Stack when the second piece of work genuinely needs code from the first:

- A follow-up needs a package the open PR introduces (Pipes needed
  `internal/eventtarget` from the EventBridge PR; Auto Scaling needed
  `internal/alarmaction` from the CloudWatch alarms PR).
- One change is large enough that a reviewer would rather read it in two
  passes, and the halves have a natural order.
- Two agents are working the same area and the second must not duplicate the
  first's abstraction.

**Do not stack** work that is merely adjacent. Two PRs touching different
services are independent even if both regenerate `internal/capabilities/all.gen.go` —
that is a rebase conflict, not a dependency, and stacking makes it worse by
forcing an order on merges that do not need one.

---

## The four ways to build a stack

Pick by where the branches already are.

### 1. `gh stack` — the CLI, for work you are starting now

The extension tracks the chain locally and creates every PR in one step:

```sh
gh extension install github/gh-stack   # once per machine

gh stack init                  # start a stack on top of the default branch
gh stack add feat/second-part  # add a branch above the current one
gh stack submit                # push every branch and create/update the PRs
```

Day-to-day commands: `gh stack view` prints the chain, `gh stack sync` brings
local branches back in line with the remote after anything merges,
`gh stack rebase` replays the stack, `gh stack modify` restructures it, and
`gh stack checkout` takes a stack number, a PR number, a PR URL or a branch
name. Navigation is `gh stack up` / `down` / `top` / `bottom` / `trunk` /
`switch`. `gh stack merge` merges the whole stack, `gh stack unstack` dissolves
it locally and on GitHub.

### 2. `gh stack init` over branches that already exist

Turn a set of branches you already pushed into a stack, bottom first:

```sh
gh stack init branch1 branch2 branch3
```

### 3. `gh stack link` — for PRs that are already open

The common case in this repo: two agents finished independently and the
dependency only became clear afterwards. `gh stack link` joins existing PRs
into a stack **on GitHub, without local tracking** — nothing is rebased or
force-pushed, so it is safe to run against another agent's branch.

### 4. The web UI, REST, GraphQL and webhooks

Opening a PR whose base is another PR's branch creates a stack; the UI shows
the membership. The REST and GraphQL APIs and webhook payloads carry stack
information, which is how automation should read a stack rather than inferring
one from base-branch names. Stacks are **not** supported in GitHub Desktop.

---

## Rules that are not optional here

**Merge bottom-up.** GitHub enforces the order, and the order is the point: the
bottom PR carries the code everything above it compiles against.

**Never delete a base branch that has children.** `gh pr merge --delete-branch`
on a PR that something is stacked on closes the child PR outright and deletes
its head branch with it. GitHub will not retarget a *closed* PR, so recovery
means fetching `refs/pull/<n>/head`, rebasing onto `main` and opening a
replacement — which loses the review thread. Merge the bottom without
`--delete-branch` and let GitHub retarget the stack, then clean branches up
afterwards.

**Never rebase or force-push a branch you do not own while its PR is open.**
Resolving another agent's conflicts rewrites their commit under them, and
diverges the moment they push their own resolution. If a stack is blocked
because a lower PR conflicts with `main`, the owner of the *lower* PR fixes it
— ask, do not fix it for them.

**After anything in the stack merges, sync — do not hand-rebase.** GitHub has
already rebased the branches above. `gh stack sync` (or `git fetch` and reset
the local branch to its remote) picks that up. A local `git rebase` at that
point replays commits the server already dropped, which is what forces the
`git rebase --skip` / `--onto` dance every time this repo squash-merges a base.

**A PR with no checks at all is conflicting, not queued.** GitHub dispatches no
workflows on a `CONFLICTING` PR. Inside a stack, CI runs on every layer, so
silence from a mid-stack PR means either it or something below it needs a
rebase — check `gh pr view <n> --json mergeStateStatus` before waiting.

---

## Conflicts in generated files

A stack that touches capability declarations conflicts in the committed
generated sources. Resolve them by **regenerating**, never by hand-merging:

```sh
./scripts/docker-go.sh run -tags dev ./cmd/capgen --generate
./scripts/docker-go.sh run -tags dev ./cmd/capgen --write-docs
./scripts/docker-go.sh run ./cmd/tsgen --write
```

A stack of docs branches no longer conflicts on anything generated: the docs
navigation and search index are derived at runtime from the embedded docs
(`internal/docsindex`), so only the Markdown two branches both edited can
conflict — and that is a real conflict, with a real decision in it.

`docs/plans/*.md` conflicts are usually a **union**, not a choice: each item's
row records its own status, so both sides' rows survive.

See [Generated files](../../../AGENTS.md#generated-files) for the full list and
their owning commands.

---

## Merging — `gh pr merge` does not work on a stack

A PR that belongs to a stack is refused by both `gh pr merge` (GraphQL) and the
plain REST merge endpoint, with `403 Merging stacked PRs via this endpoint is
not supported. Use the asynchronous merge endpoint instead.` This contradicts
the merge instructions everywhere else in the repo, and the error gives no hint
that stack membership is the reason.

Merge the bottom PR with the asynchronous endpoint, then poll the UUID it hands
back until it reports `merged`:

```sh
gh api -X PUT repos/overcast-sh/overcast/pulls/<n>/merge-async -f merge_method=squash
# => {"status":"pending","details":{"uuid":"…","expected_head_sha":"…"}}

gh api repos/overcast-sh/overcast/pulls/<n>/merge-async/<uuid>
# => {"status":"merged","details":{"sha":"…"}}
```

It takes `commit_title`, `commit_message`, `sha` (guard against the head moving)
and `merge_action` as well. Results are retained for 24 hours. `gh stack merge`
merges a whole stack in one go and is the better choice when every layer is
ready; the endpoint above is for landing one layer at a time, which is what this
repo does so each squash lands as its own reviewed commit.

Auto-merge and stacks do not mix: `gh stack link` refuses a PR that has
auto-merge enabled (`cannot be added to a stack: it has auto-merge enabled`).
Turn it off with `gh pr merge <n> --disable-auto` before linking, and merge that
layer by hand afterwards.

## Reviewing and landing a stack

- Each PR in the stack still answers the changelog question on its own — see
  [.changelog/README.md](../../../.changelog/README.md).
- Say in each PR body which PR it sits on and that it must merge after it. The
  stack UI shows the chain, but the sentence survives being read in a diff or
  an email.
- A mid-stack PR's diff includes everything below it until those merge. That is
  expected; do not try to strip it.
- Verify the top of the stack the way you would verify any branch —
  `make check`, or the `scripts/docker-go.sh` equivalents when there is no host
  Go toolchain.
