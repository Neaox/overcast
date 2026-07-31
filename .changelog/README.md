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
  below.
- **Areas** — optional service or area slugs, primary first: `sqs`,
  `cloudformation`, `web`, `router`, `state`, `ci`, `docs`, `compat`, … The
  primary sorts related entries next to each other at assembly time. Leave it
  out for a change with no natural area.
- **Prose** — the bullet text, in the house style (see "Changelog Management" in
  [.agents/skills/pull-request/SKILL.md](../.agents/skills/pull-request/SKILL.md)).
  An indented line continues the entry above it, which is how the long ones are
  written.

File name: `.changelog/YYYYMMDD-<slug>.md`, UTC date and a lowercase slug —
`changelog.py new` derives it from the branch.

## Breaking changes

Almost nothing is breaking, so almost nothing has to say so. A marker demanded
on every line would become a reflex, and a reflex is indistinguishable from
forgetting — so the friction is spent in one place only:

| Category | Unmarked means | To say otherwise |
| --- | --- | --- |
| `-` Removed | **breaking** | `-.` for something that never shipped |
| everything else | not breaking | `+!`, `~!`, `*!` … |

On top of that, prose that *reads* like a break — "now requires", "now
rejects", "no longer accepts", "renamed", "default is now", … — forces an
explicit marker either way. The list is deliberately narrow: it names input and
output contracts, not behaviour, because "no longer &lt;misbehaves&gt;" is just how
a bug fix is written. That is not an accusation, it is a refusal to guess, and
it is what
catches the breaking change nobody would have marked: validation that now
rejects input it used to accept, or a default that quietly moved, filed under
Fixed like any other improvement.

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

## Release time

Release prep runs:

```sh
python3 scripts/changelog.py assemble <version>
```

which prints a draft `## [<version>]` section — entries grouped by category,
then area, with breaking ones flagged `**BREAKING**` and their migration notes
attached. Curate that draft into `CHANGELOG.md` per the house style — merge
same-area entries into single bullets, tighten prose — then `git rm` every
fragment file (everything here except this README).
`scripts/check-release-changelog.py` fails the release while any fragment
remains.
