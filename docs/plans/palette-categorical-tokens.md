# Palette: categorical colour tokens

> Status: requirements, not started. This is phase 2 of the palette work; phase 1 (collapsing
> semantic hues onto the existing tokens) is separate and can land first.

The web UI uses ~389 raw Tailwind palette classes (`text-emerald-300`, `bg-purple-400`,
`text-cyan-300`, …) instead of design-system tokens. They divide by purpose, and the two halves
need opposite treatments:

- **State** — success, warning, danger expressed in eight different hues (amber 42, emerald 31,
  red 27, yellow 16, teal 14, orange 11, green 11, rose 5). These collapse onto the existing
  `--success` / `--warning` / `--danger` tokens. That is **phase 1**: mechanical, low risk, not
  covered by this document.
- **Identity** — colours that distinguish one *thing* from another (blue 27, purple 23, sky 20,
  indigo 15, cyan 15, violet 5, pink 4, slate 3). Collapsing these onto one accent would destroy
  the information they carry. They need a defined, theme-aware categorical palette, which is what
  this document specifies.

## Why it matters

Raw Tailwind hues are theme-blind. Every design-system token resolves through a CSS variable with
a distinct light and dark value tuned for contrast against that theme's surfaces; `text-purple-400`
is one fixed value rendered identically on `#F4F8FC` and `#10161D`. So today's categorical colours
are, at best, tuned for one theme and tolerated in the other. A guard already exists for a related
failure — `web/src/styles/global.test.ts` fails the build on undeclared colour utilities, added
after `bg-bg-card` silently rendered transparent — but it cannot catch a *declared* Tailwind hue
being used where a token belongs.

## Where the categorical colours live

The important structural finding: they are mostly assigned through **lookup tables**, not scattered
literals. That makes this tractable — change the table, not the call sites.

| Source | What the colour encodes | Notes |
| --- | --- | --- |
| `web/src/lib/service-registry.ts` | Service identity | **35 services**, each with a `color` and a `bg` (e.g. `color: "text-orange-400"`, `bg: "bg-orange-400/10"`). Consumed by the sidebar, dashboard cards, command palette and topology map — so this one table drives most of the app's categorical colour. |
| `web/src/features/map/topology-nodes.tsx` | Node/service type on the topology map | Largest single file (~48 usages). Also carries message-state colours that are **not** categorical — see Non-goals. |
| `web/src/components/ui/event-console.tsx` | Event source / level | ~40 usages; a `Record<string, string>` map at line 92. |
| `web/src/features/metrics/startup-timeline.tsx` | Startup phase | ~12 usages, one colour per phase. |
| `web/src/features/map/lambda-instance-node.tsx`, `igw-node.tsx` | Node types | ~25 usages combined. |

## Requirements

1. **Define a categorical ramp as tokens.** N slots, declared in `web/src/styles/global.css`
   alongside the existing semantic tokens, each with a distinct light value and dark value, and
   exposed as Tailwind utilities via the `@theme` block the way `--color-accent` etc. already are.
2. **Every slot must be legible in both themes** against the surfaces it actually appears on —
   `--bg`, `--bg-elevated`, and for the map, node fills. Verify contrast rather than eyeballing;
   record the ratios.
3. **Adjacent slots must be distinguishable from each other**, including for the most common forms
   of colour-vision deficiency. A topology map where two service types read as the same colour is
   the failure this work exists to prevent.
4. **Assignment must stay stable.** A given service must keep its colour across sessions and
   releases; users navigate by colour memory. A hash-to-slot scheme is acceptable only if it is
   deterministic and documented; an explicit mapping is preferable.
5. **The ramp must be bounded and reused.** With 35 services and a smaller ramp, services share
   slots — that is fine and expected, but the sharing rule must be deliberate (e.g. grouped by
   service category so related services sit near each other) rather than incidental.
6. **Colour must not be the sole carrier of meaning.** Wherever a categorical colour distinguishes
   items, an icon, label or shape must also distinguish them. This is an accessibility requirement,
   not a nicety.
7. **One source of truth.** `service-registry.ts` should reference ramp slots, not hex or Tailwind
   hues, so the sidebar, dashboard, palette and map cannot drift apart.
8. **Guard against regression.** Extend the existing `global.test.ts` approach with a test that
   fails when a raw Tailwind hue class appears in `web/src` outside an agreed allowlist, so the
   389 cannot quietly become 400.

## Open decisions

These need a human call before implementation starts.

1. **Ramp size.** How many slots? Fewer is more coherent and easier to keep legible; more reduces
   collisions across 35 services and the map's node types. A useful starting point is 8–12.
2. **Derivation.** Brand-derived (hues sampled around the Overcast blues) reads as one family and
   stays on-brand, but risks slots that are hard to tell apart. Purpose-built for maximum
   distinguishability is more legible on a dense topology map but will look less like the brand.
   The design canvas may already imply an answer — artboards **3a** (Logs) and **3c** (System Map)
   both show these surfaces and should be checked before choosing.
3. **Do the map's node colours belong to the same ramp as the sidebar's service colours?** They
   encode the same concept (service identity) so sharing is attractive, but the map renders them as
   large fills where the sidebar renders small glyphs, and those have different contrast needs.
4. **Naming.** `--cat-1…n` is honest but opaque; semantic-ish names (`--cat-compute`,
   `--cat-storage`, …) are self-documenting but bake in a taxonomy that may not survive new
   services.

## Acceptance criteria

- No raw Tailwind palette class remains in `web/src` outside the agreed allowlist, enforced by a test.
- Every categorical colour resolves through a token with distinct light and dark values.
- Contrast ratios recorded for each slot against each surface it is used on.
- The topology map, event console, startup timeline, sidebar, dashboard and command palette all
  agree on a service's colour.
- Screenshots of the map and event console in both themes, before and after.

## Non-goals

- **Do not flatten the topology map's message-state colours.** They encode state (in-flight,
  delivered, failed), not identity, and belong with the semantic tokens in phase 1.
- Do not change iconography, node geometry or layout — this is colour only.
- Do not introduce a charting palette for sparklines here; the design uses `--oc-accent` for all
  six sparklines, so they are already consistent and out of scope.
