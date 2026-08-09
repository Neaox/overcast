---
name: issue-coordination
description: "Coordinate a batch of GitHub issues across parallel agents — select, fence, dispatch, review, and merge. Use when: picking up several issues at once, deciding what a group of sub-agents should work on, splitting a backlog across writers, deciding whether two pieces of work can run in parallel or must be stacked, or acting as the root agent that reviews sub-agent commits and lands their PRs."
compatibility: opencode
metadata:
  audience: contributors
  workflow: github-issues
  languages: "go,typescript,markdown"
argument-hint: "The issue numbers or a label query to select the batch from"
license: MIT
---

# Issue Coordination — Overcast

Use this skill when you are about to put **more than one writer** on this
repository at the same time: several sub-agents, or one agent while another's
branch is still open. It covers choosing the batch and shaping it so the pieces
do not collide.

Read [github-issue-lifecycle](../github-issue-lifecycle/SKILL.md) for labels and
issue hygiene, [git-worktrees](../git-worktrees/SKILL.md) for the isolated
checkout every writer needs, and [stacked-prs](../stacked-prs/SKILL.md) for
landing a chain once you decide work must be ordered. This skill covers only the
selection and the collision analysis between them.

**The whole point is that the analysis comes _before_ the dispatch.** Sending
three agents out and then working out what they share is how two of them end up
rewriting the same function from different premises. Once an agent is running,
your only remaining tools are a scope fence or a discarded branch.

---

## 1. Filter the candidates for actionability

Priority order gets you a shortlist; it does not tell you what an agent can
actually finish. Before anything else, drop the issues that are blocked no
matter how good the agent is.

Read each candidate's **definition of done**, not its title. Reject or defer:

- **Work whose DoD requires evidence you cannot obtain.** "Capture raw real-AWS
  HTTP responses", "record the CloudTrail request CloudFormation issued",
  `needs-aws-verification` — an agent with no AWS account cannot close these,
  and it will either stall or, worse, infer the answer and present it as
  captured. #648 and #657 are the standing examples.
- **Issues labelled `needs-info` or with an unanswered question in the thread.**
  Answer it first or leave it.
- **Anything already in flight.** Check open PRs *and* registered worktrees —
  `gh pr list` plus `git worktree list --porcelain`. A locked worktree with a
  task in its reason is someone's live claim.

What survives is the batch you are actually allowed to parallelise.

---

## 2. Map the collision surface at function granularity

**Repo-level and file-level maps are both too coarse to be useful here.**
`internal/services/cloudformation/provisioner.go` is over five thousand lines;
"they both touch provisioner.go" tells you nothing about whether two agents
conflict. Two issues can share a file and never meet, or share no file and still
collide through a data model.

For each issue in the batch, locate the **functions and types it must change**
and write them down. `grep` for the symbols the issue names; read the failure
branches it describes. Then build the grid — issues down one axis, symbols
across:

| Surface | #663 | #658 | #662 | #639 |
|---|---|---|---|---|
| `UpdateStack` request parsing | ✅ | ✅ | — | — |
| `updateStackResourcesCtx` failure branches | ✅ | — | — | — |
| `rollbackUpdate` | — | ✅ | — | ✅ |
| `rollbackToStable` | — | — | ✅ | — |
| `rollbackInPlaceUpdate` | — | — | ✅ | ✅ |
| `StackResource` persisted fields | ⚠️ | — | ⚠️ | — |
| every `resourceUpdater` implementation | — | — | — | ✅ wide |

Any column with two ticks in a row is a decision you owe.

---

## 3. Classify each collision — the third kind is the dangerous one

**Textual.** Two issues edit the same lines. Git shows you a conflict, someone
resolves it, nothing is lost. This is the cheapest kind and it is not worth
reorganising a batch to avoid.

**Structural.** Two issues restructure the same function from different
premises. Git may or may not flag it. The merge compiles; the behaviour is
whatever the second rebase happened to produce. Worth avoiding.

**Semantic — two agents independently inventing the same new thing.** Neither
touches the other's lines. Git merges both cleanly. Nothing fails to compile,
no test goes red, and the repository now has two names for one concept, or one
field that two code paths interpret differently. **No tool will tell you.**

The worked example: #663 needed dirty-update state to survive a restart, and
#662 needed "attempted/previous property state persisted to retry after
restart". Today that state is an in-memory `map[string]bool` local to
`updateStackResourcesCtx`. Both issues require persisting it; neither says so in
words the other would match. Two agents would have produced two fields, both
plausible, silently disagreeing about what "dirty" means.

Find these by asking, for every pair: **is there one new concept both of these
issues have to invent?** New persisted field, new interface method, new error
sentinel, new helper. If yes, that concept belongs to exactly one issue, and the
other consumes it.

---

## 4. Choose a disposition for each pair

**Parallel** — no shared symbols. Dispatch freely, one worktree each. Different
services almost always qualify; a shared regenerated file (`all.gen.go`) is a
rebase, not a dependency, and does not justify ordering.

**Parallel with a fence** — shared file, disjoint functions. Dispatch both, but
send each agent an explicit ownership boundary first (§5).

**Stack** — one issue's work is the foundation the other builds on, or they
share an invented concept. Order them and target each branch at the one below;
see [stacked-prs](../stacked-prs/SKILL.md). Put the widest diff **last**: an
issue that touches every implementation of an interface conflicts with
everything by surface area alone, so it should rebase onto finished work rather
than have finished work rebase onto it.

**Defer** — the pair is entangled enough that splitting costs more than
sequencing. Say so and hold the second issue.

Bias toward stacking when the shared surface is a data model, and toward
fencing when it is merely a file. The cost of a wrong "parallel" call is two
half-right implementations of one idea; the cost of a wrong "stack" call is
wall-clock.

### Generated files are a fourth case, and they resolve differently

`internal/capabilities/all.gen.go`, `internal/docssearch/index.gen.jsonl`,
`web/src/docs-nav.gen.ts` and `internal/awsapi/manifest.gen.go` are committed
build output. Two agents whose work regenerates the same one will conflict, but
that conflict is **not** a dependency and must never be hand-merged or
hand-resolved — it is fixed by re-running the owning command (`make docs-index`,
`make generate-caps`, `make generate-aws-operations`) after the rebase. Do not
stack work merely because two branches both regenerate a table; do warn each
agent that the file is generated, so nobody resolves it by hand and commits a
plausible-looking file that no command would produce.

### How many at once

Size the batch by how much you can *review*, not by how much can run. Every
returning diff needs a real read, an independent verification, and a merge-order
decision from you. Three is comfortable; beyond that the coordinator becomes the
bottleneck and starts rubber-stamping, which defeats the point of reviewing at
all. If a batch must be larger, stagger the dispatch so the returns arrive
spread out rather than together.

---

## 5. Write the brief: the fence, plus the constraints every agent needs

### The fence

A fenced agent needs all of this, in its opening brief — not as a correction
afterwards:

- **What it owns**, named as functions and files with line references.
- **What it must not touch**, named the same way, with the issue number that
  owns each one. An agent that knows `rollbackUpdate` belongs to #658 will route
  around it; one told only "stay focused" will not.
- **The adjacent temptation, called out explicitly.** If the fix it is writing
  sits four lines from a bug another issue owns, it *will* see that bug. Tell it
  whose it is.
- **The shared concept and who defines it.** If it must introduce the field, say
  so, require it minimal, additive and `omitempty` so old records decode to the
  zero value, require it named for the concept rather than the issue, and
  **require the exact name, type and JSON tag back in its report** so you can
  hand the same contract to the next agent instead of letting them design a
  second one.
- **Permission to stop.** "If you believe you need something on the reserved
  list, stop and ask rather than widening." An agent that silently widens is a
  merge conflict you find out about at review time.

### The constraints every brief carries, fenced or not

These are not about overlap; they are the things a dispatched agent gets wrong
by default, in this repository, every time they are left out.

- **Its worktree, by absolute path, with a verification step.** Make the first
  action `git -C <path> rev-parse --show-toplevel --abbrev-ref HEAD`, and state
  that every path it reads or writes lives under that root. An agent handed a
  task but not a checkout will edit the primary one, where other agents are
  working.
- **Never `git stash`.** The stash stack is shared by every worktree in the
  repository, so a stash in one is a stash the others can drop or restore.
- **Foreground execution only.** An agent that backgrounds a command and then
  stops to "wait for the notification" deadlocks — it has no turn in which to
  receive one. Tell it to run commands in the foreground and, where it must
  wait, to poll in a loop inside the same command.
- **The environment's own traps**, named rather than discovered: ports 4566 and
  4567 belong to the user's instance; Docker image tags are per-branch and get
  cleaned up; anything the host genuinely cannot do (here, a host-mode Lambda
  invoke) should be stated so the agent verifies through tests instead of
  burning an hour proving the machine is broken.
- **Its verification gate, spelled out** — the exact `gofmt` / `go test` /
  `go build` / `go vet` (including the tag sets) / `golangci-lint` commands, and
  which `make` targets apply. "Verify your work" produces a `go build` and a
  claim.
- **Commit, then stop.** No push, no PR, no merge. Worktree clean, branch local.
- **What to report**: the failing-test-first evidence, the forks it resolved and
  which way it went, the verification commands with their results, and what it
  deliberately left out. A report without the forks is the one that hides the
  decisions nobody reviewed.

**One install owner per batch.** If the work touches `web/`, exactly one agent
may run an install; concurrent installs in a shared checkout corrupt
`node_modules`. Name that agent in every brief that needs the dependency tree.

---

## 6. The root agent is the coordinator, and does not implement

The agent holding this skill runs the batch; it does not write the code. Its
job is selection, dispatch, review, integration and merge order. Keeping it out
of the editors is what lets it hold the whole board — an agent that starts
implementing has stopped coordinating.

**The division of labour:**

| Sub-agent | Root agent |
|---|---|
| Works in its own locked worktree | Owns the worktrees it created, and their cleanup |
| Writes the failing test, implements, verifies | Re-verifies rather than trusting the report |
| **Commits — and stops there** | Reviews the commit, pushes, opens the PR |
| Never pushes, never opens a PR, never merges | Merges, in the order it judges right |

**Review before push, every time.** A returning agent's report is a claim; its
diff is the fact. Read the diff end to end as AGENTS.md § Self-review requires,
and re-run the verification yourself — the cheapest high-value check is
reverting the production files and confirming the new tests actually fail
against the old code, because "failing test first" is the claim most easily
made and least often true. Confirm the fence held by diffing the reserved
symbols.

**Send work back rather than fixing it yourself.** If the review finds a
workflow problem — a missing changelog fragment, an unreviewed hunk, a scope
breach, a conflict with a sibling branch — return it to the agent that wrote
it. It still has the context; you would be reconstructing it. A resumed agent
keeps its transcript, so the correction costs far less than the rediscovery.
Fixing it yourself also destroys the signal: the agent never learns its brief
was ambiguous, and the next brief repeats it.

**Merge order is the root agent's call.** Nothing obliges you to merge in the
order the PRs opened or the order they went green. Merge to minimise the rebase
burden on what is still open: the branch that others must rebase onto goes
first, the widest diff last. When a stack is involved, bottom-up, and never
`--delete-branch` a base that still has children.

**Check the release window before merging anything.** If `VERSION` on `main`
names an untagged version, the repository is mid-release and only changes needed
to get that release out may merge — `scripts/release-candidate-check.sh`
printing `true` is the definition. Run it on `main`, not on a release branch,
where it reports the branch rather than the window. A batch of ordinary fixes
waits.

**A merged PR frees a slot, not the whole batch.** When an agent's PR merges,
that worktree is done — verify integration from the PR's merged state (this
repo squash-merges, so the branch SHA never appears in `main`), remove the
worktree, delete the branch, and only then select the next issue for that slot.
Re-run the surface map before dispatching into it: `main` has moved, and the
map you built at the start of the batch is now a prediction about code that has
since changed.

### What to do with what the work turns up

**Tangential findings become GitHub issues, not larger diffs.** An agent that
notices a second bug while fixing the first must file it, not fold it in. The
skill's whole premise — that overlapping diffs are what cost you — fails the
moment a fix quietly grows to cover its neighbours. File it with enough detail
that someone can pick it up cold, reference it from the PR body, and move on.
`updateStackResourcesCtx`'s cancellation branch is the worked example: same bug
class as #663, deliberately left, worth its own issue.

**Unless it blocks.** If the tangential thing makes the assigned work
impossible to finish or impossible to test, it is not tangential — it is part
of the job. Fix it in place, say so plainly in the PR body, and explain why it
could not be separated.

**Regressions are fixed immediately, always.** A change that breaks something
that used to work outranks everything else in the batch: pause the dispatch,
fix it, and only then resume. This is the one case where "file an issue and
move on" is the wrong answer — a regression left standing while three agents
build on top of it is a much more expensive problem an hour later. It applies
whether the regression came from a batch PR, from `main` underneath you, or
from your own merge order.

---

## 7. Standing rules for the batch

- **One writer per worktree, always.** Every write-capable agent gets its own
  worktree and branch under the resolved worktree root, locked with
  `git worktree lock --reason "owner=…; task=…; claimed=…"`. Read-only agents
  may share. This is repo policy, not a preference — see AGENTS.md § Worktree
  policy.
- **Never let two agents share an install or a generated file regeneration.**
  Concurrent installs in one checkout corrupt `node_modules`; assign a single
  owner if the batch needs one at all.
- **Re-check the map when an agent reports back.** A returning agent's actual
  diff is the fact; your pre-dispatch map was a prediction. If it took a symbol
  you had assigned elsewhere, fix the assignment before dispatching the next
  one.
- **Record the disposition where the next person will find it.** A comment on
  the deferred issue naming what it is waiting for costs one line and saves the
  next agent the whole analysis.

---

## Checklist

- [ ] Blocked-by-evidence and `needs-info` issues dropped from the batch
- [ ] Open PRs and locked worktrees checked for work already in flight
- [ ] Symbol-level surface map built for every candidate, before dispatch
- [ ] Every shared surface classified textual / structural / semantic
- [ ] Every pair given a disposition: parallel, fenced, stacked, or deferred
- [ ] Shared concepts assigned to exactly one owner, with the contract reported back
- [ ] Widest diff scheduled last
- [ ] One locked worktree and branch per write-capable agent
- [ ] Fences written into the opening briefs, not sent as corrections
- [ ] Batch sized to what can actually be reviewed, not to what can run
- [ ] Every brief carries the worktree path, the no-stash and foreground rules, the verification gate, and commit-then-stop
- [ ] Every returning diff reviewed and re-verified by the root agent before it is pushed
- [ ] Failing-test-first confirmed by experiment, not accepted as a claim
- [ ] Workflow problems and conflicts sent back to the authoring agent, not patched by the coordinator
- [ ] Release window checked on `main` before any merge
- [ ] Merge order chosen to minimise rebases: shared foundation first, widest diff last
- [ ] Generated-file conflicts resolved by regenerating, never by hand
- [ ] Worktree removed and branch deleted once the PR is confirmed merged, before the slot is refilled
- [ ] Surface map rebuilt against the new `main` before dispatching into a freed slot
- [ ] Tangential findings filed as issues and referenced from the PR; only blockers folded in
- [ ] Any regression fixed immediately, before the batch continues
