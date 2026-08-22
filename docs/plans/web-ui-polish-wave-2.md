# Web UI Polish — Wave 2

> Status: outstanding work after the design-2a rollout and the first polish wave. Pruned
> 2026-08-21: the static-skeleton treatment (`components/ui/skeleton.tsx`, routed through
> `QueryListState`), card skeletons, the cold-boot/unreachable state (`connecting-screen.tsx` +
> `connection-gate.tsx`; the disconnected bar later became a reconnecting toast, PR #376),
> `prefers-reduced-motion` support, the dialog anatomy (header band, icon tile, footer), the
> pending toast variant, and copy-to-clipboard on identifiers (PR #361) have all landed and
> their items were deleted below. 2026-08-23 (#1203): the "Quality of life" bullets below on
> deep-linkable filter state and audited empty-state messaging landed for the scaffold plus every
> page that already had a filter box — the five single-list index pages (AppSync, Cognito, KMS,
> SSM, Step Functions) and, since they also converted to `ResourceTable`/`ResourceListFilter` in
> #1200 wave 2, the two tabbed pages (IAM's four resource tabs, EventBridge's two) — which
> additionally gained a `tab` search param, so tab-state deep-linking is no longer wholly open
> either; see the narrowed wording in place. Pages that still lack a filter box (`ec2-dashboard`)
> or bind a search param one-way rather than live (`apigateway/usage-plans-page`'s `apiId`/`planId`)
> remain open. The structural extractions (detail field, table wrapper, busy
> buttons, spinner rollout) are sequenced in
> [web-ui-dry-refactor.md](./web-ui-dry-refactor.md), which supersedes this file's wording
> where they overlap. **2026-08-23 (#1202):** all three "Open decisions" are decided — see each
> bullet's **Decided** paragraph; the topbar-search-slot filter redesign is dropped in favour of
> the inline placement already in place.

Wave 1 delivered the app shell (228px sidebar, 52px topbar, breadcrumbs excluding the current page), brand tokens and JetBrains Mono in both themes, the tiered dashboard, context-aware search, the command palette rebuilt against artboard 6a, a shared list-page scaffold covering 24 list pages, and a repo-wide typography pass. This file covers what is left, sourced from the design canvas (`Overcast Web UI.dc.html`, turns 1-6) and from issues found while implementing wave 1.

Keep this file limited to work that is still outstanding. Delete items as they land.

## Decisions already made

These were settled during wave 1 — do not re-litigate them while implementing:

- **Skeletons are static.** The design states verbatim: *"skeletons are static. Nothing shimmers."* Flat `--oc-line` bars with an opacity ladder — no `animate-pulse`, no shimmer. An earlier backlog recommended animation; that recommendation is superseded.
- **The blinking cursor is the only sanctioned motion** (`.oc-cursor-blink`, 1.1s steps). Under `prefers-reduced-motion: reduce` it must go **solid, not hidden**.
- **Spinners are 14-16px, chips and toasts only.** Never in a content area — that is what skeletons are for.
- **Dashboard sections group by implementation tier only.** Enabled/disabled is a per-entry concern, never a section.
- **Availability greying keys off the health service list, never the emulation tier.** A service missing a tier entry reports `stub` while being fully live.
- **The sidebar's pinned group is called "pinned", not "in use"** — artboard 6a still says "in use"; that is the inaccuracy we deliberately fixed.
- ~~**Disabled services are greyed with a "Disabled" pill**~~ — removed along with `OVERCAST_SERVICES`; every service always runs, so there is no disabled state to draw.

## Outstanding Work

### Loading and connection states (highest value — systemic)

- Metric "first sample pending": mono 24px/700 in `--oc-muted`, literal em dash + 4px gap + 7×20 cursor, caption sans 11px `waiting for first sample`, sparkline slot `110px × 0` with `1px dashed --oc-line` on the baseline.
- Idle event tail (connected-and-empty, distinct from an empty state): accent `>_` + `--oc-body` `tail --follow events`, mono 11px, 8px gap; second line `waiting for activity` + 6×12 cursor.
- Busy controls: `Creating` — 32px, accent fill, mono 12/700, label and cursor in `--fg-on-accent`, `cursor: default`, **no dimming**. `Refreshing` — 32px, outlined, mono 12 regular, accent-glow cursor, icon dropped. Progress chip — 30px, 14px loader, `deploying stack · 2 of 5`, track 90×4 r2 accent-soft, unrounded accent fill, bare `40%` label.
- Honour the design's `hint-placeholder-count` attribute for skeleton row counts rather than hardcoding (`QueryListState` has a `loadingCount` prop; verify the hint attribute is what feeds it).
- **Finish the loading-affordance rollout — audited 2026-07-27, three gaps remain.** The skeleton
  work reached list pages because they share `QueryListState`; these did not:
  - **Detail pages still use content-area spinners.** 227 `<Spinner>` usages across 101 files
    remain (re-counted 2026-08-21; list pages now inherit skeletons via `QueryListState`, detail
    pages do not), concentrated
    in `cognito/cognito-pool-detail.tsx` (15), `ecs/cluster-detail.tsx` (11), `ec2/ec2-dashboard.tsx`
    (11), `sqs/queue-detail.tsx` (10), `apigateway/rest-api-detail.tsx` (9). 5b restricts spinners to
    14-16px in chips and toasts, never a content area. Detail pages each hand-roll their loading
    branch, so the fix is a shared wrapper analogous to `QueryListState` — one component, pages
    inherit — not 208 edits.
  - **Busy buttons use a spinner where the design uses a blinking cursor.** The prevailing pattern is
    `{isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}`; 5b specifies label + cursor
    (`Creating ▍`, `Refreshing ▍`), solid accent fill, `cursor: default`, and **no dimming**. Apply it
    in `Button`/`CreateAction`/`RefreshAction` so call sites inherit it.
  - **Several sites bypass the `Spinner` component**, importing lucide's `Loader2` directly and
    so escaping its size clamp (re-audited 2026-08-21): `ui/combobox.tsx`,
    `cloudformation/stack-detail.tsx`, `cloudformation/stack-list.tsx`,
    `layout/connection-dialog.tsx`, `routes/debug/traces/$requestId.tsx` (global-search has
    since been fixed). Route them through `Spinner`.

### Dialogs and toasts

- Roll out the `⏎ to create · esc to cancel` contract. The mechanism landed — `dialog.tsx` ships
  `DialogKeyHint`, `DialogIcon` and `onPrimaryAction` — but as of 2026-08-21 only `ConfirmDialog`
  uses it; the ~50 other dialog-bearing files still lack the advertised affordance. The rollout is
  owned by the DRY plan's P5 (`ResourceFormDialog`).

### Visual consistency

- Collapse raw Tailwind palette usage to the semantic roles. Partially done as of 2026-08-21:
  the event console and the ANSI ramp now resolve through the `--cat-*` identity tokens, but
  ~330 raw hue classes remain (`service-registry.ts` 108, `topology-nodes.tsx` 50,
  `metrics-page.tsx` 10, `startup-timeline.tsx` 10). The identity half is specified in
  [palette-categorical-tokens.md](./palette-categorical-tokens.md). Excludes the map's
  message-state colours — see Non-goals.
- **Unify the modal scrims.** Five overlays, four different treatments, and the shared one is the
  odd man out. Audited 2026-07-27:

  | site | ink | blur | z |
  | --- | --- | --- | --- |
  | `components/ui/dialog.tsx:32` (the shared `DialogOverlay`) | `rgba(9,16,22,0.62)` | `backdrop-blur-sm` | 50 |
  | `components/layout/global-search.tsx:619` (command palette) | `rgba(9,16,22,0.62)` | none | 50 |
  | `features/map/lambda-invocations-drawer.tsx:188` | `bg-black/30` | none | 40 |
  | `features/map/topology-nodes.tsx:985` | `bg-black/20` | `backdrop-blur-[1px]` | 60 |
  | `features/map/log-stream-peek.tsx:149` | none (transparent, `pointer-events-none`) | none | 60 |

  Two things to settle rather than just aligning the values. First, **artboard 4a specifies the
  scrim with no blur**, so the shared `DialogOverlay` having `backdrop-blur-sm` is the actual
  deviation — the command palette, which hardcodes the same ink without blur, is the one matching
  the design. Decide whether the blur goes or the design note is wrong, then make the survivor the
  single definition. Second, the palette duplicates the ink literal instead of using
  `DialogOverlay`; once the treatment is settled every consumer should reuse the component so a
  sixth variant cannot appear.

  Keep the scrim **theme-fixed** — a dark wash over a light page is correct, and the design fixes it
  across both themes deliberately. This is a consistency job, not a light-mode legibility one; the
  two map scrims were left alone during the event-console fix for exactly that reason. The
  transparent `log-stream-peek` overlay is likely intentional (a peek, not a modal) but should be
  confirmed rather than assumed. The z-index ladder (40/50/60) also wants a look while someone is
  in there.

- Unify the renderings of the advisory component (health drawer, Metrics & Health card, the 1b variant, the inline warning in `pipes/components/pipe-list.tsx:280`, and `PlaceholderSourceNotice` in `lambda/components/code-tab.tsx`) into one `<Advisory density="compact" | "full">`. Sentence case always; use the external-link icon, never a `→`. The last two deliberately copy the existing inline-warning classes rather than adding another variant, so they should fall out of this sweep for free.
- Fix five viewport-height calculations that assume the old 16px page padding and are now 16px short, so they can overflow: `mail/mail-page.tsx:159` `h-[calc(100vh-10rem)]`, `debug/debug-page.tsx:281,311,738` `max-h-[calc(100vh-14rem)]`, `s3/components/bucket-detail.tsx:294` `max-h-[calc(100vh-220px)]`.

### Typography follow-ups

The correctness pass of 2026-07-27 landed the systemic half: `lib/typography.ts` now owns the two
label specs (`fieldLabel` 9px/`.14em`, `sectionLabel` 10px/`.16em`) and every shared primitive reads
from it; `TableCell` is mono by default with `TableCellProse` as the sans exception; `Badge`,
`Button`, `Tabs`, `Input`/`Select`/`Textarea`, `Label`, `FormField`, `EmptyState` and `PageHeader`
were brought to the canvas's sizes and weights. Still outstanding:

- `components/layout/connection-dialog.tsx` — endpoint URL and region field labels, and the endpoint value itself.
- **Extract a shared detail-field component.** `DetailRow`/`InfoRow` is hand-rolled 12 times — `cloudformation/stack-detail.tsx:576`, `cognito/cognito-pool-detail.tsx:2169`, `ec2/instance-detail.tsx:490`, `ec2/vpc-detail.tsx:806`, `ecs/task-detail.tsx:178`, `eventbridge/event-bus-detail.tsx:175`, `kms/kms-key-detail.tsx:181`, `rds/instance-detail.tsx:323`, `secretsmanager/components/secret-detail.tsx:274`, `ssm/ssm-parameter-detail.tsx:274`, `sts/sts-page.tsx:75`. They render a mono label over a **sans value**, so timestamps, counts and identifiers on every detail page are still sans (see `cloudwatch/logs/components/log-group-detail.tsx:202-213`). Fix it in one extracted component, not twelve edits — same lesson as the spinner rollout: the sweep reaches only what shares a component. Have the label use `fieldLabel`.
- **Extract a generic table wrapper carrying the app-wide defaults.** `ResourceListCard` in
  `components/ui/resource-list-page.tsx` already does this for *list pages* — card surface, header
  strip, column-header treatment — but tables elsewhere (detail-page sub-tables, dialogs, the debug
  and metrics views) bypass it and re-specify their own chrome, which is how they drift. A wrapper
  that owns the surface, header treatment, body typography, row hover and empty/loading state would
  make those defaults inheritable everywhere rather than only on list pages. Body typography is now
  inheritable via `TableCell`, so scope this to surface, hover and empty/loading state, and prefer
  extending `ResourceListCard` over introducing a second wrapper that competes with it.
- **Migrate the remaining hand-rolled `<table>`s onto the primitives.** `s3/bucket-detail.tsx` and
  `s3/put-object.tsx` were brought to spec in place, and `lambda/versions-tab.tsx`,
  `lambda/triggers-tab.tsx` and `cloudwatch/cloudwatch-dashboard.tsx` had the specs applied to their
  raw `<th>`/`<td>`, but none of them use `TableHead`/`TableCell`, so they will drift again. The
  markdown tables in `routes/docs.tsx` and `docs/service-docs-modal.tsx` are prose and must stay
  sans — exclude them.
- **The map's micro-labels are still off-spec.** `map/topology-nodes.tsx`, `map/lambda-instance-node.tsx`
  and `logs/log-viewer.tsx` carry ~20 uppercase mono chips and node labels at `tracking-wide`/`widest`
  and 7–10px, constrained by node geometry rather than by the label specs. Decide whether the map gets
  an explicit third spec or is brought onto the existing two, alongside the palette-collapse work.

### Pages and features

- Bring Metrics & Health to artboard 3d: the 5-up stat row, a uniform 3-column grid, mono-26 values, 130×30 sparklines.
- Add S3 bulk selection. Artboard 3b has a checkbox column and 4b's confirm reads "Delete 2 objects"; `s3/components/bucket-detail.tsx` is single-select today. CloudWatch log groups already have the pattern, and `SelectCheckbox` exists in the list-page scaffold.
- Consider extending the list-page scaffold to the service index pages that are not list-shaped and were skipped: iam, kms, ssm, ses, secretsmanager, stepfunctions, eventbridge, appsync, eks, sts, ec2, plus apigateway's `api-keys-page` and `usage-plans-page`. These are tabbed/dashboard layouts and need their own layout decision first.

### Quality of life

- ~~Deep-linkable state for filters and selected tabs~~ — landed 2026-08-23 (#1203):
  `useFilterSearchParam` (`hooks/use-filter-search-param.ts`) two-way binds `ResourceListFilter` to
  the route's `q` search param — debounced commit, `replace: true` so typing never spams
  Back/Forward — following the same contract Request Traces' filters already use. Wired into the
  five single-list index pages that had a filter box (AppSync, Cognito, KMS, SSM, Step Functions)
  and, since #1200 wave 2 converted them to the same `ResourceTable`/`ResourceListFilter` shape,
  the two tabbed pages (`iam-page`'s Users/Roles/Policies/Groups tabs, `eventbridge-page`'s
  Buses/Rules tabs) — both of which also gained a `tab` search param, so the selected tab
  deep-links too; switching tabs clears `q` in the same navigation, matching `TabPanel`'s existing
  behaviour of unmounting every tab but the selected one. **Still open:** pages that filter
  client-side but have no filter box yet at all (`ec2-dashboard`; see P9 in
  `web-ui-dry-refactor.md`), and `apigateway/usage-plans-page`'s `apiId`/`planId` params, which
  predate this and are a one-way "open on this plan" link rather than a live filter binding.
- ~~Empty states audited per page~~ — landed 2026-08-23 (#1203): `ResourceTable`/`QueryListState`
  gained `isFiltered`/`onClearFilter`, so "nothing exists yet" (create CTA) and "nothing matches
  the filter" (clear-filter action, "No matching {noun}" copy) are distinct by construction instead
  of each page hand-writing the `filter ? … : …` ternary — and a load error still wins over both
  (checked first, regardless of `isFiltered`). Applied to the seven pages above. The remaining
  Archetype-A pages that use `ResourceTable`/`QueryListState` without a filter box already got the
  true-empty/error distinction for free from the primitive; none were found falling back to a bare
  generic message.

## Open decisions

- **The `organizations` emulation tier.** `tiers.ts` defines `stub` as "all operations return 501", but `organizations` answers `DescribeOrganization` with a 200, so `stub` is false by that definition — while `partial` ("core operations work") overstates a single hardcoded op with no stored state. Recommendation: keep `stub` and reword the definition to something like "registered but effectively unimplemented — at most a hardcoded discovery response", rather than adding a fifth tier for one service. The string is user-facing in the dashboard tooltip and the compatibility docs.

  **Decided 2026-08-23:** `organizations` stays `stub`; the *definition* of stub is reworded, no
  fifth tier. Wording: *"Stub — registered so discovery works: at most a hardcoded, stateless answer
  to the service's describe call; every other operation returns 501 Not Implemented."* This is what
  `internal/router/tiers.go` already says ("a minimal discovery or control-plane subset");
  `docs/README.md`'s "all operations return 501" is the stale one and is updated to match. A developer
  reads the tier to answer "can I point my code at this?"; the boundary that matters is "nothing you
  can rely on" vs "core operations work". A tier for one service adds a category to learn with no
  decision value. The same sentence is used in the dashboard tier tooltip.

- **`docs/README.md`'s coverage categories versus runtime tiers.** Two taxonomies with overlapping words: the docs file EKS under "IaC/discovery-oriented stub" while `tiers.go` classifies it `partial`. Decide whether they should be reconciled or explicitly separated.

  **Decided 2026-08-23:** one taxonomy, `tiers.go` is the source of truth. `docs/README.md`'s
  "Coverage tier" column uses the runtime tier names (`full` / `partial` / `inert` / `stub`) and the
  "Service emulation tiers" table is the only definition of them; free-text categories like
  "IaC/discovery-oriented stub" become the tier plus a sentence in the notes column. The column must
  be derived from `router.ServiceTiers` (generated by the docs tooling, or at minimum guarded by a
  test the way `TestServiceTiers_coverEveryRegisteredService` guards the map) so the EKS-style drift
  (docs "stub", runtime `partial`) cannot recur. A developer who sees `partial` in the console or
  `/_overcast/health` must find `partial`, with the same meaning, in the docs. Tracked by a follow-up
  issue (implementation, not decision).

- **Per-page filter placement.**

  **Decided 2026-08-23:** per-page filters live inline in the list header; the topbar slot is global
  search only. Decided for inline — where `ResourceListFilter` and the `q` search param (#1203)
  already are. Proximity: the filter acts on the table directly beneath it, so that is where the hand
  and eye go; and the topbar search is ⌘K *global* search across services and resources — giving that
  one control two jobs (sometimes scoped to the page, sometimes global) breaks the developer's model
  of what typing there does. The redesign's "filter in the topbar search slot" item is dropped; P9
  (`useResourceFilter`) proceeds on the inline placement.

## Non-goals

- Do not flatten the topology map's message-state colours during the palette collapse — they encode state, not decoration.
- Do not copy artboard 3c's hand-placed geometry; the map is force-directed.
- Do not build the health drawer before the pages it double-polls.
