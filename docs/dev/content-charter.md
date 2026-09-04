# Docs content charter

The rules below apply to everything under `docs/` that Overcast publishes
(i.e. everything except `docs/plans/` and `docs/dev/` itself — see
`internal/docsindex`). They exist so a docs edit reads like it was written for
the person hitting the page, not for whoever reviewed the PR. Follow them the
same way whether you're a human or an agent; `scripts/docs-index.go --check`
mechanically enforces the rules that are checkable (1, 3, 9, and partially 2).

1. **One concern per page, under a length budget that proves it.** Massive info
   dumps are the failure mode this charter exists to stop, and splitting a dump
   into four dumps does not stop it. Every page:

   - has **one job**, from: *quickstart* (get to a working command fast, under
     150 words before the first code block), *task guide* (walk one workflow
     end to end), *reference* (exhaustive, table-first, scannable — never read
     start to finish), or *troubleshooting* (symptom → cause → fix, one per
     entry);
   - **opens with what the reader came for** — the command, the decision, the
     answer — then the table or the short list, then the exceptions;
   - keeps exhaustive detail (every flag, every field) in a **reference table
     or a `<details>` block**, never in paragraphs;
   - **links instead of repeating** what another page already says.

   A guide too big for one page becomes a short landing page — what it is, the
   three to five things most readers need, links — plus one sub-page per
   concern, mirroring the `docs/services/<key>/` layout. **6,000 characters of
   prose and 12,000 characters of page** is the mechanical floor under all of
   this; see "Where this is enforced" for the measure and the opt-out. If a
   rewrite still reads as a dump, the page is doing too much: split it again,
   or delete it.
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
7. **A service page follows the service page template.** Everything under
   `docs/services/` has a fixed shape — an answer for the first-hour reader
   above the fold, the deep reference on its own sub-page — because a service
   page is read by three people with different questions and only one of them
   scrolls. [service-doc-template.md](./service-doc-template.md) is the shape;
   `internal/docslint` enforces it in `make docs-check`.
8. **A diagram is warranted when** the doc describes a topology, a request
   path across three or more components, or a before/after config diff — and
   prose would need the reader to hold three or more spatial relationships
   in their head to follow it. Not warranted for a single API call, a single
   env var, or anything sequential enough that a numbered list already reads
   clearly.
9. **No LLM tells.** "This isn't a proxy — it's a full emulator" is a sentence
   that corrects an idea the reader never held; cut the negated half and state
   what the thing is. Same for "it's not about X", "seamless(ly)", "delve",
   "it's worth noting that", and the three-adjective slogan ("fast, simple and
   powerful"). `internal/docslint` fails the build on the fixed shapes; the
   allowlist beside it takes the rare genuine exception.

## Where this is enforced

- `scripts/docs-index.go --check` (wired into `make docs-check`, which CI
  runs) fails the build on a `docs/dev/`/`docs/plans/` reference in a
  published doc (rule 3) and on a frontmatter `description` over 220
  characters (a proxy for rule 2 — a long description is a reliable tell
  that the opening paragraph is doing too much before the first actionable
  thing).
- **The length budget (rule 1)**, on every published page, generated ones
  included: **6,000 characters of prose** and **12,000 characters of page**.
  Prose is what is read top to bottom — headings, paragraphs, list items,
  quotes. Page is all of that plus code blocks and tables. Both exclude the
  generated capability block, which is why an operations sub-page made
  entirely of it measures zero and passes on its own merits.

  Going over is allowed, once it is a decision. Put the reason in an HTML
  comment anywhere on the page:

  ```md
  <!-- docs-length-review: every environment variable in one table; splitting
       it by area would make readers guess which page a variable is on -->
  ```

  A marker with no real reason fails, and so does one left on a page that has
  since come under budget — the excuse cannot outlive its reason. Pages that
  predate the budget sit in `LengthBacklog` in `internal/docslint` at the size
  they were: they may shrink, never grow, and the entry is deleted (by failing
  the build) the moment the page comes inside the budget.

  **`docs/dev/` has a budget of its own**, `DevMaxProseChars` and
  `DevMaxPageChars`: **26,000 characters of prose** and **30,000 characters of
  page**, measured the same way, opted out of with the same marker, with the
  same shrink-only backlog. Bigger because these pages are a different kind of
  writing — a published page answers one question for somebody mid-task, a
  contributor page explains a mechanism to somebody about to change it — and
  applied at all because `docs/dev/` came to hold the four largest files in the
  repository. Nothing else in this charter is enforced there.
- **The house-style tells (rule 9)**, as fixed phrases and fixed shapes. The
  failure prints the line to paste into the allowlist beside the linter if the
  wording is genuinely right; an allowlist line that stops matching fails too.
- The same check runs `internal/docslint` over `docs/services/`: required
  sections, template section order, the generated capability block last, the
  fixed set of sub-page names, and rule 2 counted literally on a landing
  page's intro (rule 7).
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
