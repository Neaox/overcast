# Service page template

Every `docs/services/<key>.md` follows the shape below. `make docs-check`
enforces the structure (`internal/docslint`); this page says what goes in each
section and why. Read [the content charter](./content-charter.md) first — this
is that charter applied to one page shape.

## Rule zero: one concern per page

Before the section order, before the status tokens: **a page does one thing, and
a page that reads as an info dump has failed however well it is organised.**

- Open with what the reader came for — the command, the decision, the answer.
- Then the table or the short list. Then the exceptions.
- Exhaustive detail (every flag, every field) goes in a reference table or a
  `<details>` block, never in paragraphs.
- Link instead of repeating what another page says.

`make docs-lint` puts a floor under it on **every** published page: 6,000
characters of prose, 12,000 characters of page, both excluding the generated
capability block — which is why a generated operations page, being one table,
passes without being named anywhere. Going over needs a stated reason:

```md
<!-- docs-length-review: <why this page is legitimately long> -->
```

The reason has to be one; a marker left on a page that has since come under
budget fails too. See the charter's rule 1 for the whole mechanism.

Splitting a dump into four dumps is not a split. If a rewrite still reads as
one, the page is doing too much: split it again, or delete it.

## The three readers

They arrive in this order, and each one stops reading when they have their
answer. **A reader must never scroll past content written for a later reader.**
That single rule is what the section order encodes.

| Reader           | Arrives asking                                           | Served by                                                 |
| ---------------- | -------------------------------------------------------- | --------------------------------------------------------- |
| **First hour**   | "Does S3 work here? What's the one command?"             | H1, status line, Quick start — the first screen, nothing else |
| **App developer** | "What behaviours can I rely on? What will bite me?"      | What works, Gotchas, `<key>/examples.md`                  |
| **Platform engineer** | "Exactly how does this diverge? What's the coverage?" | Differences from AWS, `<key>/limitations.md`, `<key>/operations.md` |

The first-hour reader is the one who leaves. Everything they need is above the
fold or the page has failed, however good the rest of it is.

## Page shape

```md
# <Service> — <AWS product name>

<One sentence: what this service is for in Overcast, and its one defining
constraint.>

**Status:** ✅ Supported

## Quick start

<The smallest thing that works. 15 lines maximum, CLI or SDK, copy-pasteable.>

## What works

<Implemented behaviours that matter — not the operation list. `| Area | Behaviour |`.>

## Differences from AWS

<Divergences, as `| Area | On AWS | Overcast |`. Long list? Move it to
<key>/limitations.md and link.>

## Gotchas

> [!WARNING]
> <The thing that costs an afternoon.>

<!-- BEGIN overcast:capabilities -->

## Operations

<Generated. Do not edit.>

<!-- END overcast:capabilities -->

## Related

- <Links out.>
```

**Quick start.** Open the code block with `export AWS_ENDPOINT_URL=http://localhost:4566`
and nothing else — credentials, region and per-language client setup belong to
[Using AWS SDKs and CLI](../sdk-cli.md). Shell blocks are ` ```bash `. End on the
call that proves it worked.

**Differences from AWS.** `| Area | On AWS | Overcast |`, AWS before Overcast.
Drop the middle column when the rows genuinely have no AWS half to state —
`| Area | Overcast |` — rather than inventing one.

**Related.** One link per bullet, in this order: this page's own sub-pages
(limitations, troubleshooting, examples), sibling service pages,
`[All service pages](./README.md)`, `[Service names and state
overrides](../configuration.md#service-names)`, guides, and the AWS API
reference last. Same-directory targets carry a `./` prefix.

**Every published page carries one**, not only a service landing page — a reader
arrives from search on whichever page matched, and the link footer is their only
route to the page they wanted. `internal/docslint` requires it everywhere except
a directory index (`README.md`), which is a list of links already, and checks the
two ends of the order above: own sub-pages first, links off the site last. The
middle of the list is a judgment and is left to you.

**Required:** `Quick start`, `Operations`, `Related`. The rest are optional —
write one when there is something to say, not to fill the outline. Sections you
do write must stay in the order above, and headings outside this vocabulary
(`## Queue URLs and endpoint resolution`) go anywhere before the generated
block.

`## Operations` and everything between the markers belongs to `cmd/capgen`.
Regenerate with `make docs`; never hand-edit it.

## Sub-pages

Long-form material moves into `docs/services/<key>/`. Four file names are fixed,
and mean the same thing for every service:

| File                | Written by | Holds                                                        |
| ------------------- | ---------- | ------------------------------------------------------------ |
| `operations.md`     | capgen     | The per-operation table. Generated; never hand-edited.       |
| `limitations.md`    | you        | The full divergence list, when it is too long for the landing page. |
| `troubleshooting.md` | you       | Symptom → cause → fix, one entry each.                        |
| `examples.md`       | you        | Worked end-to-end examples past the Quick start.              |

Create one only when the content exists. An empty sub-page is worse than no
sub-page: it costs a reader a click to learn nothing. Every hand-written
sub-page links back to `../<key>.md`, so a reader arriving from search knows
which service they are looking at.

### Concern pages

A directory may hold further pages named after one concern —
`lambda/concurrency.md`, `lambda/runtimes.md`, `ecs/scheduler.md`. Write one
**only when a canonical page would otherwise run past the length budget or
cover two subjects a reader looks for separately.** Lambda's limitations page
was five reference chapters under one H1; a platform engineer looking for the
concurrency ceiling had to scroll past logging to find it. Two pages a reader
has to read together are one page, and four dumps are not a split.

| Rule                          | Detail                                                                     |
| ----------------------------- | -------------------------------------------------------------------------- |
| Name                          | Lowercase words joined by single hyphens, after the concern: `dead-letter-queues.md`. |
| Not a respelling              | `limitation.md`, `troubleshoot.md`, `examples-advanced.md` are refused — the four fixed names keep their meaning. |
| Linked from the landing page  | From `## Related` or from the body. A page nothing links to is reachable from search and nowhere else. |
| `## Related` opens with `../<key>.md` | The canonical sub-pages are the reader's map of the directory; a concern page has to hand them back to it first. |
| Budget                        | The 6,000/12,000 budget applies per page, so a concern page that is itself a dump has not solved anything. |

`internal/docslint` enforces all five. The landing page's `## Related` still
lists `limitations`, `troubleshooting` and `examples` before the concern pages,
so the fixed names stay the first thing a reader sees.

Where the material belongs to a guide rather than to the service — EC2's
Docker-backing mechanics, say — put it under `docs/networking/` and link it.
A concern page is for what is genuinely about this one service.

**The link back goes inside the first sentence, not on a line of its own.**
`Back to [ECS](../ecs.md).` spends the one line every reader reads on the one
thing they already know. Say what the page holds and carry the link in that:

```md
The full divergence list behind [RDS](../rds.md).
Symptom, cause and fix for tasks that will not start behind [ECS](../ecs.md).
Worked setups past the [EFS quick start](../efs.md#quick-start).
```

`internal/docslint` rejects a first body line that is nothing but a link, with
or without a `Back to` in front of it. A line with a clause of its own passes,
including one that keeps the back-link at the end.

## Guides that outgrow one page

Anything outside `docs/services/` splits the same way, because the reason is the
same: a reader wants one concern, not the union of five.

```
docs/<guide>.md          landing: what it is, the 3–5 things most readers
                         need, links to the sub-pages. Nothing else.
docs/<guide>/<sub>.md    one concern each, named for the concern
```

Each sub-page links back to `../<guide>.md`, so a reader arriving from search
knows where they are. Two things have to happen for a new directory to reach
readers, and neither is automatic:

| Step | Why |
| ---- | --- |
| Add the directory to `embed.go`'s `//go:embed` pattern | The console serves docs from the embedded tree; a directory the pattern misses is indexed by `internal/docsindex` but 404s when opened. `internal/docsindex`'s corpus test fails on exactly this. |
| Add each new page to the website's `publicDocFiles` allowlist | Only `docs/cdk/**` and `docs/services/**` publish by prefix; everything else is listed by name. |

## Visual vocabulary

**Status line.** One of these, exactly, directly under the opening sentence:

| Token                       | Means                                                             |
| --------------------------- | ----------------------------------------------------------------- |
| `**Status:** ✅ Supported`   | Works for normal SDK and CLI use.                                 |
| `**Status:** ⚠️ Partial`     | Works with documented caveats or missing edge cases.               |
| `**Status:** 🧊 Inert`       | Accepted and answered correctly, but nothing happens as a result. |
| `**Status:** 🚧 WIP`         | Present, still moving.                                            |
| `**Status:** ❌ Unsupported` | Modelled but not implemented.                                      |

Same vocabulary as the per-operation table, so a reader learns it once.

The page-level choice is not a judgment call: it follows the service's coverage
tier, which `cmd/capgen` declares and the generated service index renders.
**Comprehensive → ✅ Supported; Core CRUD, Minimal and Stub → ⚠️ Partial.** A
page whose status disagrees with its tier is a bug in one of the two.

**Alerts.** GitHub alert syntax, rendered as callouts by both the console docs
modal and the website. One or two per page — a page of callouts is a page with
no emphasis at all.

| Alert            | Use for                                                       |
| ---------------- | ------------------------------------------------------------- |
| `> [!NOTE]`      | Context a reader needs but would not look for.                |
| `> [!TIP]`       | A shortcut or a better way to do what they are already doing. |
| `> [!IMPORTANT]` | Something they must do for the thing to work at all.          |
| `> [!WARNING]`   | Something that will cost them time.                            |
| `> [!CAUTION]`   | Something that loses data or state.                            |

**Tables over bullet walls.** Anything with more than three parallel items and
a shared shape is a table. One clause per cell — over ~25 words it is prose,
and belongs below the table.

**Code first.** A working command beats a paragraph describing one. Lead
sections with the example and explain underneath.

**Two sentences, then something actionable.** The intro budget from the charter,
enforced mechanically here: at most two sentences between the H1 and the first
heading, command, table or list.

## When you finish a service

Delete its entry from `RestructurePending` in `internal/docslint/docslint.go`.
Until you do, three rules stay waived for that page — Quick start required,
the intro budget, and no hand-maintained capability tables — and the linter
fails the build once a page satisfies all three, so the entry cannot outlive
its reason.
