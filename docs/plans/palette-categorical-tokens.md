# Palette: categorical colour tokens

> Status: **complete** (2026-08-23). The ramp shipped with the design-system rollout (PR #314,
> 2026-07-27): ten `--cat-1…10` slots in `web/src/styles/global.css`, one hue per slot at uniform
> OKLCH lightness, distinct light and dark values, and `global.test.ts` failing on any undeclared
> `cat-` slot. This document's own work — migrating the remaining call sites onto tokens and adding
> the regression guard — landed on top of it: 429 raw Tailwind palette classes across 47 files went
> to **zero**, phase 1 (state hues onto the semantic tokens) went with them, and requirement 8's
> allowlist test now fails the build on the first one that comes back. The open decisions below are
> answered in place.

The web UI used ~429 raw Tailwind palette classes (`text-emerald-300`, `bg-purple-400`,
`text-cyan-300`, …) instead of design-system tokens. They divided by purpose, and the two halves
needed opposite treatments:

- **State** — success, warning, danger expressed in eight different hues. These collapse onto the
  existing `--success` / `--warning` / `--danger` / `--accent` tokens. That is **phase 1**:
  mechanical, low risk, and done in the same change as phase 2 because the allowlist test cannot be
  set at zero while either half is outstanding.
- **Identity** — colours that distinguish one *thing* from another. Collapsing these onto one accent
  would destroy the information they carry. They need a defined, theme-aware categorical palette,
  which is what this document specifies.

## Why it matters

Raw Tailwind hues are theme-blind. Every design-system token resolves through a CSS variable with
a distinct light and dark value tuned for contrast against that theme's surfaces; `text-purple-400`
is one fixed value rendered identically on `#F4F8FC` and `#10161D`. So the categorical colours were,
at best, tuned for one theme and tolerated in the other. A guard already existed for a related
failure — `web/src/styles/global.test.ts` fails the build on undeclared colour utilities, added
after `bg-bg-card` silently rendered transparent — but it could not catch a *declared* Tailwind hue
being used where a token belongs. That is what requirement 8's test adds.

## Where the categorical colours lived

The important structural finding: they were mostly assigned through **lookup tables**, not scattered
literals. That is what made this tractable — change the table, not the call sites.

| Source | What the colour encodes | Outcome |
| --- | --- | --- |
| `web/src/lib/service-registry.ts` | Service identity | 36 entries × `color`/`bg`/`border` + the `hex` field, all now ramp slots. This one table drives the sidebar, dashboard cards, command palette and topology map. |
| `web/src/features/map/topology-nodes.tsx` | Node/service type on the topology map | Largest single file. Its message-state colours went to semantic tokens, not the ramp — see Non-goals. |
| `web/src/components/ui/event-console.tsx` | Event source / level | Already done before this change. |
| `web/src/features/metrics/startup-timeline.tsx` | Startup phase | One slot per phase. |
| `web/src/features/map/*-node.tsx`, `map-theme.ts` | Node types, edge types | Edges now point at the same slot their owning service does. |

## Requirements — how each was met

1. **Define a categorical ramp as tokens.** Ten `--cat-1…10` slots in `global.css`, exposed through
   the `@theme` block as `text-cat-*` / `bg-cat-*` / `border-cat-*` / `shadow-cat-*`. Shipped in #314.
2. **Every slot legible in both themes.** Recorded below, both themes, including on a chip tinted
   with the slot's own colour — the surface the migration newly put text on.
3. **Adjacent slots distinguishable.** Closest pair is orange/amber at 3.4× the Oklab
   just-noticeable difference in light, 3.7× (rose/red) in dark.
4. **Assignment stable.** The rule preserves each service's established hue to within 15° — see
   decision 4.
5. **Ramp bounded and reused deliberately.** See decision 3.
6. **Colour never the sole carrier.** Every surface showing a service colour shows its Lucide icon
   and its label beside it; the map adds the `letter` pill. No slot-sharing pair is separated by
   colour alone.
7. **One source of truth.** `service-registry.ts` holds slots, not hues. `map-theme.ts` derives from
   it, and `EDGE_THEME` points each edge at its owning service's slot — so `cfn-export` moved from
   indigo to CloudFormation's cyan, which is what "all surfaces agree" actually requires.
8. **Guard against regression.** `global.test.ts` → *"raw Tailwind palette classes"*: scans `src/`
   with comments stripped, fails on any raw hue outside an allowlist, and the allowlist is empty.

## Decisions

These needed a human call before implementation. Answered:

1. **Ramp size — ten.** Settled by #314 and left alone. Ten is enough that the topology map's
   common node types never collide, and few enough that every slot stays legible at one lightness.
2. **Derivation — purpose-built, not brand-derived.** Also settled by #314: an even hue wheel at
   uniform OKLCH lightness. Brand-derived blues could not have separated 35 services on a dense map.
3. **Map fills and sidebar glyphs share one ramp — yes.** They encode the same concept, and
   requirement 7 is unsatisfiable if they do not. The contrast worry (large fills vs small glyphs)
   turned out not to bind: the map draws its fills as `/5`–`/20` tints of the slot with the slot
   colour as text on top, which is the same relationship the sidebar has, and both clear AA (below).
4. **Naming and assignment — numeric slots, nearest-hue assignment.** `--cat-1…10` stays: a
   semantic taxonomy (`--cat-compute`) would bake in a grouping that new services break. A service's
   slot is **the slot whose OKLCH hue is nearest the colour that service already had** — nothing
   else. That rule is deterministic, order-independent, and moves no service more than 15°, which is
   what keeps colour memory intact (requirement 4: users navigate by "S3 is the orange one"). Two
   consequences worth stating:
   - **Near-neutral colours are not identities.** STS was `slate-300`, chroma 0.02 — below any
     slot's. It takes `fg-muted` rather than being pushed onto a hue it never had.
   - **Sharing is what users already saw.** 35 services over 10 slots forces sharing, and
     nearest-hue makes the sharing inherit today's: ECR, EventBridge and Secrets Manager were all
     reds and are all `--cat-1`; Pipes and Kinesis were both cyans and are both `--cat-6`.

The full assignment lives in the comment above `SERVICES` in `web/src/lib/service-registry.ts`,
beside the table it describes, rather than being restated here where it would drift.

## Recorded contrast

Each slot as **text**, against the surfaces it appears on, WCAG 2.1 contrast ratio. The last two
columns are the slot on a chip tinted with its own colour — the case the migration introduced, and
the tint strengths call sites actually use.

#### light (`L=0.5`)

| slot | page bg | card | on own /15 (card) | on own /20 (card) |
| --- | ---: | ---: | ---: | ---: |
| `--cat-1` | 6.07 | 6.48 | 5.12 | 4.72 |
| `--cat-2` | 5.83 | 6.22 | 4.99 | 4.62 |
| `--cat-3` | 5.62 | 5.99 | 4.85 | 4.50 |
| `--cat-4` | 5.30 | 5.66 | 4.58 | 4.25 |
| `--cat-5` | 5.33 | 5.69 | 4.58 | 4.24 |
| `--cat-6` | 5.45 | 5.81 | 4.67 | 4.33 |
| `--cat-7` | 5.66 | 6.04 | 4.82 | 4.45 |
| `--cat-8` | 5.95 | 6.35 | 5.11 | 4.73 |
| `--cat-9` | 6.08 | 6.49 | 5.19 | 4.80 |
| `--cat-10` | 6.12 | 6.53 | 5.17 | 4.77 |

#### dark (`L=0.75`)

| slot | page bg | card | on own /15 (card) | on own /20 (card) |
| --- | ---: | ---: | ---: | ---: |
| `--cat-1` | 7.70 | 6.82 | 5.31 | 4.81 |
| `--cat-2` | 7.89 | 6.99 | 5.40 | 4.89 |
| `--cat-3` | 8.19 | 7.26 | 5.53 | 4.98 |
| `--cat-4` | 8.59 | 7.61 | 5.68 | 5.09 |
| `--cat-5` | 8.71 | 7.72 | 5.78 | 5.18 |
| `--cat-6` | 8.50 | 7.54 | 5.67 | 5.09 |
| `--cat-7` | 8.20 | 7.27 | 5.45 | 4.89 |
| `--cat-8` | 7.89 | 6.99 | 5.29 | 4.77 |
| `--cat-9` | 7.69 | 6.82 | 5.23 | 4.73 |
| `--cat-10` | 7.64 | 6.77 | 5.26 | 4.76 |

Also measured, not tabulated: `--bg-subtle` (light 5.02–5.79, dark 7.15–8.16) and `--accent-muted`
row hover (light 4.88–5.63, dark 5.83–6.65).

**Every slot clears AA (4.5) as normal text on every surface, in both themes.** On its own tint the
light theme is tighter, and that margin decided three call sites rather than being noted after the
fact:

- At `/20` slots 4–7 fall to 4.24–4.45, so no call site puts text on a `/20` tint of those. The
  `/20` chips in use are `--cat-3` (4.50) and `--cat-9` (4.80), both on `--bg-elevated`.
- Two chips sat on the map canvas rather than a card, where the same tint loses another ~0.3 — the
  ESM-filter FAB's count badge and the collapsed stack node's item count. Both now use
  `bg-bg-elevated` with a `border-cat-*/40` hairline, which reads as a badge and puts the text back
  on a surface the table above measures (5.81–7.54).
- The "jump to latest" pill in the log peek was a solid fill with contrasting ink, and a tint could
  not reproduce that: `text-cat-9` on a `/30` hover tint is 3.83:1. It uses `bg-accent` /
  `text-fg-on-accent`, which is the token pair built for a filled control.

One `/25` tint remains — the ESM-filter FAB's own hover state, 3.94:1 — and its content is a Lucide
glyph rather than text, so the 3:1 non-text threshold applies. Every number here is reproducible
from the declarations in `global.css`; the light `/20` margin is the reason to think before putting
text on a heavier tint.

## Non-goals — held

- **The topology map's message-state colours were not flattened onto the ramp.** They encode state
  (in-flight, delivered, delayed, failed) and went to `--warning` / `--success` / `--accent` /
  `--danger` with the rest of phase 1.
- Iconography, node geometry and layout are untouched. Colour only.
- No charting palette for sparklines: the design uses `--accent` for all six and they stay
  consistent.

## Follow-ups this change did not take

- **Solid status pills became tinted ones.** `bg-emerald-600 text-white` on the map's SQS/RDS badges
  had a 3.8:1 white-on-fill ratio; they now use the house `bg-<token>/15 text-<token>` idiom that
  `components/ui/badge.tsx` already defines, which both fixes the ratio and stops the map inventing
  its own badge look. That is a visible change to those pills, deliberate, and the only one in this
  work that is not a like-for-like colour swap.
- **`--success` has no `-muted` pair** while `--danger` and `--warning` do. Nothing here needed one
  (`/15` covers it), but the asymmetry is real and belongs to the semantic palette, not this ramp.
