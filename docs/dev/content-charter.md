# Docs content charter

The rules below apply to everything under `docs/` that `scripts/docs-index.go`
publishes (i.e. everything except `docs/plans/` and `docs/dev/` itself — see
`isPublishedDocPath`). They exist so a docs edit reads like it was written for
the person hitting the page, not for whoever reviewed the PR. Follow them the
same way whether you're a human or an agent; `scripts/docs-index.go --check`
mechanically enforces the two rules that are checkable (3 and, partially, 2).

1. **Every doc has one job.** Pick it from: *quickstart* (get to a working
   command fast, under 150 words before the first code block), *task guide*
   (walk a workflow end to end), *reference* (exhaustive, table-first,
   scannable — not read start to finish), or *troubleshooting* (symptom →
   cause → fix, one per entry).
2. **Intro budget: 2 sentences before the first actionable thing** — a
   command, a table, or a linked next step. If a third sentence of
   throat-clearing feels necessary, cut it or move it after the actionable
   thing as a "why this works" aside.
3. **Never cite a file the public site doesn't publish.** No links into
   `docs/dev/**` or `docs/plans/**`, no bare `internal/` Go paths standing in
   for an explanation. If the reasoning behind a decision matters to a
   reader, inline the one sentence that matters — don't point at a paper
   trail they can't open. `scripts/docs-index.go --check` fails the build on
   a `docs/dev/` or `docs/plans/` reference in a published doc, both as a
   literal path fragment and as a Markdown link that resolves into either
   tree.
4. **Banned genres, anywhere in a published doc:** build-machinery narration
   ("fetched at build time", "generated from", "the source ref is");
   internal editorial-policy explanation ("stays out of the public site",
   "internal plans live in..."); process narration about the docs team's own
   review ("we checked every variable and found..."). All of it is true and
   none of it is for the reader.
5. **A table cell is not a paragraph.** If a cell needs more than ~25 words
   or a nested clause, it isn't table content — pull it into prose below the
   table with its own heading, and leave one clause in the cell.
6. **New doc → decide its layer before writing.** Quickstart/task guide is
   written for someone reading start to finish once. Reference is written
   for someone who will Ctrl+F it forever and never read it top to bottom.
   Don't write a reference table like a story or a guide like a spec.
7. **A diagram is warranted when** the doc describes a topology, a request
   path across three or more components, or a before/after config diff — and
   prose would need the reader to hold three or more spatial relationships
   in their head to follow it. Not warranted for a single API call, a single
   env var, or anything sequential enough that a numbered list already reads
   clearly.

## Where this is enforced

- `scripts/docs-index.go --check` (wired into `make docs-check`, which CI
  runs) fails the build on a `docs/dev/`/`docs/plans/` reference in a
  published doc (rule 3) and on a frontmatter `description` over 220
  characters (a proxy for rule 2 — a long description is a reliable tell
  that the opening paragraph is doing too much before the first actionable
  thing).
- Everything else here is a review-time judgment call, not a mechanical
  check — a linter that grows into a style-bible-as-code stops getting
  maintained the moment it produces its first annoying false positive.

## Where this is referenced from

- [CONTRIBUTING.md § Writing docs](../../CONTRIBUTING.md#writing-docs) — for
  human contributors, linked from the "how to add a service" checklist.
- [AGENTS.md § Writing published docs](../../AGENTS.md#writing-published-docs)
  — for agents, with the most-violated rules inlined so an agent doesn't
  need to open this file to catch the common mistakes.
- `website/AGENTS.md` — the website repo's own non-docs pages (home,
  downloads, compare, console) follow the same charter; that file points
  back here as the source of truth.
