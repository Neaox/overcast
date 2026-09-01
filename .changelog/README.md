# Changelog fragments

`CHANGELOG.md`'s `[Unreleased]` section stays **empty** between releases. Every
release-note-worthy change is recorded here instead, as an entry line in a
fragment file — **one file per PR**, at a unique path, so concurrent PRs can
never merge-conflict over the changelog. At release time the entries are
curated into the new versioned section of `CHANGELOG.md` and the files are
deleted (see [RELEASE.md](../RELEASE.md)).

Every line carries its own category, scope and compatibility marker, so a file
is a container rather than a grouping — grouping happens once, at release time,
with the whole release visible.

CI runs `python3 scripts/changelog.py check` on every push and PR: it lints
every fragment here and fails if `[Unreleased]` gained content.

## Entry grammar

```
<+|-|~|*|section>[!|.] [area[/area...]] <prose>
```

```
+ [sqs] long polling on `ReceiveMessage`
~ [web] the connection toast replaced the topbar banner
* [lambda] cold-start prefill no longer double-counts init time
```

- **Kind** — `+` Added, `-` Removed, `~` Changed, `*` Fixed. Any section may be
  spelled out instead, which is how `deprecated` and `security` are written.
  Choose the category for what the change *is*, not for the service it touches:
  a `[sqs]` entry still belongs under Fixed if it fixes behaviour.
- **Marker** — `!` breaking, `.` explicitly not breaking. Usually omitted; see
  below. It goes **immediately after the kind, before the area** — `~! [sqs] …`,
  never `~ [sqs]! …`. The other order is rejected: it used to parse as prose,
  quietly costing the entry both its marker and its area.
- **Areas** — optional service or area slugs, primary first: `sqs`,
  `cloudformation`, `web`, `router`, `state`, `ci`, `docs`, `compat`, … The
  primary sorts related entries next to each other at assembly time. Leave it
  out for a change with no natural area.
- **Prose** — the bullet text, in the house style (see "Changelog Management" in
  [.agents/skills/pull-request/SKILL.md](../.agents/skills/pull-request/SKILL.md)).
  The first line is a standalone summary sentence, capped at 160 chars (see
  "Leading with a summary" below); an indented line adds detail beneath it,
  which is how the long ones are written.

File name: `.changelog/YYYYMMDD-<slug>.md`, UTC date and a lowercase slug —
`changelog.py new` derives it from the branch.

## Leading with a summary

Release notes are scanned, not read start to finish, so the first line of
every entry has to work alone: a **standalone summary sentence**, a soft cap
of 160 chars (`[area]`/`**BREAKING**` counted — they occupy width in the
rendered bullet too). `changelog.py check` fails a line over the cap with
where to put the rest.

Detail beyond the summary goes on **indented continuation lines**, one per
line, exactly the mechanism a breaking entry already uses for `migration:`
(see below) — each renders as its own indented line under the same bullet,
never merged into the summary. Nothing is lost, it just moves down:

```
+ [sqs] long polling on `ReceiveMessage` honours `WaitTimeSeconds`.
  requests capped at 20s to avoid tying up daemon workers indefinitely.
```

versus a first line that makes a reader hunt for the point:

```
+ [sqs] long polling on `ReceiveMessage` now honours `WaitTimeSeconds`, capped at 20s to avoid tying up daemon workers indefinitely so a slow poller cannot starve the connection pool the rest of the service shares
```

The PR number stays the pointer to full detail; the summary and its
continuation lines are what release notes actually show.

## Breaking changes

Almost nothing is breaking, so almost nothing has to say so. A marker demanded
on every line would become a reflex, and a reflex is indistinguishable from
forgetting — so the friction is spent in one place only:

| Category | Unmarked means | To say otherwise |
| --- | --- | --- |
| `-` Removed | **breaking** | `-.` for something that never shipped |
| everything else | not breaking | `+!`, `~!`, `*!` … |

On top of that, prose that *reads* like a break — "now requires", "now rejects",
"is rejected", "refuses", "no longer accepts", "renamed", "default is now", … —
forces an explicit marker either way. The list is deliberately narrow: it names
input and output contracts, not behaviour, because "no longer
&lt;misbehaves&gt;" is just how a bug fix is written. Present tense only, for the
same reason: "was rejected" and "rejected every real AWS SDK" describe the bug an
entry is *fixing*. A bare status code is not a hint either — "still return 501"
is how an Added entry says what it does not cover yet. That is not an accusation,
it is a refusal to guess, and it is what catches the breaking change nobody would
have marked: validation that now rejects input it used to accept, or a default
that quietly moved, filed under Fixed like any other improvement.

**A breaking entry needs a `migration:` line** saying what a user has to do:

```
-! [state] the v1 on-disk layout
  migration: run `overcast state export` before upgrading, then import after
```

That keeps `!` a deliberate act rather than a checkbox, and the release notes
need the instructions anyway.

When deciding, ask whether existing code, config, or **stored state** keeps
working unchanged — not whether the change feels large. See "What counts as a
breaking change" in [CHANGELOG.md](../CHANGELOG.md). The ones that do not look
breaking: a newly *required* field or config key (an addition), stricter
validation, a changed default, and state or config formats old data cannot
survive.

**While in alpha the marker does not move the version** — the version is an
incrementing prerelease counter either way. Mark it so users are *told* what
broke and handed a migration, not to trigger version arithmetic. Marking
honestly now is also what lets the versioning rules be applied mechanically
from 1.0 onwards.

From 1.0 the marker gains one more consequence: a marked entry holds its PR
while a minor or patch release is being prepared, because that release promises
nothing in it breaks. The hold clears itself when the release goes out, and the
bot's comment says what the alternatives are. See "Breaking Changes During A
Release Window" in [RELEASE.md](../RELEASE.md). Nothing about it fires below
1.0.

## Cross-cutting changes

A change spanning several services stays **one** entry naming them all, primary
first — never one entry per service, which just produces half a description in
two places:

```
+ [efs/ecs] live-mode mounts now honour access-point root directories
```

This matches how released sections write them
(`**Networking (client-facing URLs) / AppSync / API Gateway**`). Where no
service is primary, use a concept area — `networking`, `tls`, `release`.

## Writing them

`scripts/changelog.py new` appends correctly-formatted entries to this PR's
fragment file, so neither the filename nor the syntax is yours to get right.
Run it with no arguments and it prompts:

```sh
python3 scripts/changelog.py new
```

Each line is checked before it is accepted, so an unknown kind, a malformed
line, or an unmarked hint phrase is caught as you type rather than at commit
time. Install `prompt_toolkit` and the prompt gains syntax highlighting,
completion for area slugs (drawn from `internal/services/`, so it is never out
of date), and a status bar reading back how the line parsed — `Added | sqs |
compatible` — which makes the compatibility marker visible rather than
inferred:

```sh
pip install prompt_toolkit
```

It is entirely optional; `changelog.py` itself stays stdlib-only because CI
runs it on every push, and the plain prompt works without it.

Entries can also come from arguments, which is what agents use:

```sh
python3 scripts/changelog.py new \
  -e '+ [sqs] long polling on `ReceiveMessage`' \
  -e '*! [state] the v1 on-disk layout is gone' \
  -e '  migration: export before upgrading, then import after'
```

The file is named after the current branch and **appended to**, so later
commits on the same branch land alongside the earlier entries. `--dry-run`
shows the result without writing; `-f <file>` (or `-f -` for stdin) takes a
longer set from a file; `--name` overrides the slug.

Write entries for your own change only. Do not edit other PRs' fragments, and
do not pre-aggregate per service — that curation happens once, at release time.

## What merits a fragment

Only changes that affect shipped artifacts or release notes: runtime/service
behaviour, AWS compatibility, config/env vars, Docker or binary packaging,
release process, measured performance changes, or user-facing docs guidance.
Skip fragments for CI-only changes, test-only changes, local tooling, internal
refactors, or cleanup that does not affect shipped artifacts.

**`compat/` changes never get a fragment on their own.** The compat suites are
an external observer of the emulator: they decide nothing about what is
released and change no behaviour a user can observe, however large the diff.
The `compat` scope exists for the other direction — a change to **runtime
code** made so that a compat test passes (an emulator fix the suites caught).
That is a real behaviour change and gets a fragment describing the runtime
effect, not the test.

No fragment does not mean no record: compat changes are described fully in the
commit message and pull request like any other change — the fragment is only
the release-notes feed, and compat work has no release-notes audience.

## When a change needs no fragment

Say so on the pull request. The `Changelog entry` check fails any PR that adds
nothing here, because the two cases it cannot tell apart are exactly the ones
that matter: a change that needed no release note, and a change whose release
note was forgotten. Both are an empty `.changelog/` diff, and only the author
knows which. So the answer is asked for rather than assumed:

```
/no-changelog CI-only: pins the release action to a digest, nothing shipped changes
```

The reason is required, and it is kept. A bare acknowledgement would become a
reflex, and a reflex is indistinguishable from forgetting — the same reasoning
that keeps the breaking-change marker off every line. What is wanted is not the
tick but the sentence a reviewer reads instead of guessing.

- `/needs-changelog` puts the question back. Do that yourself if later commits
  on the PR add something users would want to read about: the waiver covers the
  PR, not the commit it was written on.
- Only the repository owner, an organisation member or a collaborator can
  waive. An outside contributor should say in a comment why no fragment is
  needed and ask a maintainer to run the command.
- The comment is read when the check runs, so commenting before CI has got
  there works — the check comes back green first time rather than going red and
  being cleared.

### Areas that are never asked

A PR is passed without a word when **every** file it touches is somewhere whose
contents cannot reach a user: `compat/` and `cmd/compat/`, `tests/` and test
files anywhere (`*_test.go`, `*_test.py`, `*.test.tsx`, `*.spec.ts`),
`docs/plans/` and `docs/dev/`, `.agents/`, `.claude/`, `.vscode/`,
`.devcontainer/`, this README, the contributor and agent guidance
(`AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `.github/copilot-instructions.md`),
and local tooling (`.golangci.yml`, `.air.toml`, `opencode.json`, `.mcp.json`,
`.codex`, `.gitignore`, `.gitattributes`).

Every file: one path outside them puts the whole PR back in scope, because that
path is the one that might ship. The list is deliberately short of things it
could plausibly hold — `.github/workflows/` builds and publishes the artifacts,
`scripts/` runs the release, published `docs/` is user-facing guidance, and
`Makefile`, `Dockerfile` and `.dockerignore` decide what an artifact contains.
A wrong entry there would ship a change with no note and nobody asked; a missing
one costs a comment.

### During a release window

**Never edit `CHANGELOG.md` to answer this** — with one narrow exception, the
last row of the table below. That file belongs to the release PR. While one is
open, `release-prep.yml` merges `main` into its branch on every push, so a
second hand in the same section does not merely conflict — the bot aborts the
merge and the release PR stops refreshing itself until somebody untangles it.
Fragments exist so that concurrent PRs never meet in one file, and the check
does not accept a `CHANGELOG.md` edit in place of one.

The exception exists only once the release PR has **merged**, which is exactly
when that bot stops running and there is no longer a second hand to collide
with. See [RELEASE.md § Breaking Changes During A Release
Window](../RELEASE.md#breaking-changes-during-a-release-window), Row 2b.

Which answer *is* right depends on where the release has got to:

| While | Do | Because |
| --- | --- | --- |
| the release PR is **open** | add a fragment exactly as usual | on every push to `main` the bot merges `main` into the release branch, folds your entries into the `## [x.y.z]` section and deletes the consumed fragments. Nothing is needed from you |
| it has **merged**, tag not out | wait for the tag, or waive and add the fragment afterwards | there is no release PR left to fold into, and `check-release-changelog.py` fails the release while any unconsumed fragment remains |
| it has **merged**, the release **failed**, and your PR is the fix | write the bullet straight into the `## [x.y.z]` section, add no fragment, waive with that reason | both Row 2 answers are circular here — waiting for the tag waits on your own PR, and a fragment would fail the release you are unblocking. The code ships in *this* release, so its note belongs in *this* section |

An open release PR does not close the window for release notes; it is the one
window where the answer is *most* automatic. Add the fragment, and it lands in
the release that is going out. The only thing that can then hold your PR is a
**breaking** entry while a non-`0.x` release is in flight — see the section
below — and that holds the merge, not the note: the fragment is still the right
thing to have written.

The release PR itself is exempt and says nothing about it. It consumes
fragments rather than adding one, so it is recognised by shape — `VERSION` and
`CHANGELOG.md` changed, nothing outside those and this directory, the version
untagged, a non-empty section present — and passed without a comment. Push a
code change onto the release branch and it is asked like any other PR, because
that change ships. That PR is also the one exception to the rule above: it is
the hand that owns `CHANGELOG.md`, so its note goes into the `## [x.y.z]`
section directly, a fragment there having nothing left to be folded into and
failing the very release it is cutting.

Note the difference between *pushing onto* the release branch and *opening a PR
into* it. The first is the release PR carrying one more commit, reviewed and
checked along with it. The second is refused outright by the `Release branch
base` check: it would merge without any of the gates on this page, which run on
PRs into `main` only, and its fragment would never be folded — folding happens
when `main` moves into the release branch, and a fragment arriving on the branch
itself moves nothing. Rebase onto `main` and retarget it. The fragment is right
as written, and the bot folds it into the release that is going out.

Only the second row is awkward, and it is meant to be rare: `main` in that state
should be taking only what is needed to get the release out. If a change that
belongs in the notes has to merge anyway, waive with a reason that says the
fragment is coming — it will land in the next release's section describing
something that shipped in the previous one, and whoever curates it needs to know
that.

The policy is the section above; the check only enforces that somebody applied
it on purpose. [scripts/changelog-required.py](../scripts/changelog-required.py)
holds the reasoning, and
[.github/workflows/changelog-required.yml](../.github/workflows/changelog-required.yml)
the wiring.

## Release time

Release prep runs:

```sh
python3 scripts/changelog.py assemble <version>
```

which prints a draft `## [<version>]` section — entries grouped by category,
then area, breaking entries first within each so a scanner hits
`**BREAKING**` on the first bullet rather than buried further down, with
their migration notes attached. Curate that draft into `CHANGELOG.md` per the
house style — merge same-area entries into single bullets, tighten prose —
then `git rm` every fragment file (everything here except this README).
`scripts/check-release-changelog.py` fails the release while any fragment
remains.

`assemble` and `fold` both parse fragments the same way `check` does, so a
summary over the cap fails release prep too, not only the PR-time check — the
lint runs wherever a fragment is read, whether that is a push to a PR or
release prep folding it into `## [<version>]`. It never runs against
`CHANGELOG.md` itself: a summary that shipped before this convention, or
whose wording was tightened by hand during curation, is not re-checked.
