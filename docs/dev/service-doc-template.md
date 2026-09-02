# Service page template

Every `docs/services/<key>.md` follows the shape below. `make docs-check`
enforces the structure (`internal/docslint`); this page says what goes in each
section and why. Read [the content charter](./content-charter.md) first — this
is that charter applied to one page shape.

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

**Required:** `Quick start`, `Operations`, `Related`. The rest are optional —
write one when there is something to say, not to fill the outline. Sections you
do write must stay in the order above, and headings outside this vocabulary
(`## Queue URLs and endpoint resolution`) go anywhere before the generated
block.

`## Operations` and everything between the markers belongs to `cmd/capgen`.
Regenerate with `make docs`; never hand-edit it.

## Sub-pages

Long-form material moves into `docs/services/<key>/`, which holds these four
files and no others:

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
