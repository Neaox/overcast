# Web UI Polish — Wave 2

> Status: outstanding work after the design-2a rollout and the first polish wave.

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
- **Disabled services are greyed with a "Disabled" pill** even though 6a draws all disabled services identically. Deliberate product improvement over the artboard.

## Outstanding Work

### Loading and connection states (highest value — systemic)

- Build the static skeleton treatment from artboard 5b and route `QueryListState` (`web/src/components/ui/primitives.tsx`) through it, replacing the centred spinner used by every list. There are ~325 spinner sites today and zero skeletons. Specs: table skeleton = 5 rows, column-1 bar widths `72/58/81/44/66%`, all bars `h 9px / r 3px / fill --oc-line`, opacity ladder `1.0 / 0.7 / 0.55`, column-3 fixed 46px, row padding 10px (the artboard's own 12px contradicts its real rows — use 10px). Footer: centred, padding 10px, mono 11px muted, `loading log groups` + 8px gap + a 6×12 `--oc-cursor` block.
- Card skeletons: 3-up, gap 8px; card r10 / padding 12px / gap 12px; 30×30 r6 tile at full opacity; 34×8 meta bar at 0.6; title bar 10px high at `62/48/70%`; subtitle 8px high, fixed 56%, opacity 0.55.
- Metric "first sample pending": mono 24px/700 in `--oc-muted`, literal em dash + 4px gap + 7×20 cursor, caption sans 11px `waiting for first sample`, sparkline slot `110px × 0` with `1px dashed --oc-line` on the baseline.
- Idle event tail (connected-and-empty, distinct from an empty state): accent `>_` + `--oc-body` `tail --follow events`, mono 11px, 8px gap; second line `waiting for activity` + 6×12 cursor.
- Busy controls: `Creating` — 32px, accent fill, mono 12/700, label and cursor in `--fg-on-accent`, `cursor: default`, **no dimming**. `Refreshing` — 32px, outlined, mono 12 regular, accent-glow cursor, icon dropped. Progress chip — 30px, 14px loader, `deploying stack · 2 of 5`, track 90×4 r2 accent-soft, unrounded accent fill, bare `40%` label.
- Honour the design's `hint-placeholder-count` attribute for skeleton row counts rather than hardcoding.
- Implement artboard 5a, the cold-boot / unreachable state. This is a **missing third state**, not a restyle: `app-shell.tsx` gates on `isConfigured()` (configuration, not liveness) and `connection-dialog.tsx` probes `/_health` then discards the result in a `catch {}`. So "configured but unreachable" currently renders nothing. Bare centred column on `--oc-bg`, 16px gaps, no card; hero is `OvercastBranding.Loader` at 72px with `aria-label="Connecting"` (not the Mark); copy `connecting to localhost:4566` (mono 13, `--oc-text`) over `Reading emulator state — first boot can take a few seconds.` (sans 12, muted). After 5s a retry affordance appears reading `still working after 5s · retry`, 7×12px padding, 13px clock icon, all one muted colour — the artboard does not style `retry` as a control, so make the whole chip the button. Topbar while disconnected shows brand + amber dot + host only: no breadcrumb, search, region select or theme toggle.
- Add `prefers-reduced-motion` support. Nothing in the app respects it today.
- **Finish the loading-affordance rollout — audited 2026-07-27, three gaps remain.** The skeleton
  work reached list pages because they share `QueryListState`; these did not:
  - **Detail pages still use content-area spinners.** 208 `<Spinner>` usages remain, concentrated
    in `cognito/cognito-pool-detail.tsx` (15), `ecs/cluster-detail.tsx` (11), `ec2/ec2-dashboard.tsx`
    (11), `sqs/queue-detail.tsx` (10), `apigateway/rest-api-detail.tsx` (9). 5b restricts spinners to
    14-16px in chips and toasts, never a content area. Detail pages each hand-roll their loading
    branch, so the fix is a shared wrapper analogous to `QueryListState` — one component, pages
    inherit — not 208 edits.
  - **Busy buttons use a spinner where the design uses a blinking cursor.** The prevailing pattern is
    `{isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}`; 5b specifies label + cursor
    (`Creating ▍`, `Refreshing ▍`), solid accent fill, `cursor: default`, and **no dimming**. Apply it
    in `Button`/`CreateAction`/`RefreshAction` so call sites inherit it.
  - **Four sites bypass the `Spinner` component**, importing lucide's `Loader2` directly and so
    escaping its size clamp: `layout/global-search.tsx:417`, `ui/combobox.tsx:391` and `:484`,
    `cloudformation/stack-detail.tsx:380` and `:438`. Route them through `Spinner`.

### Dialogs and toasts

- Dialog anatomy (4a): header band with a 30×30 icon tile, body, and a footer band on `--oc-bg`. Current dialogs are a flat `p-6 rounded-xl`.
- Wire the `⏎ to create · esc to cancel` contract. The hint is displayed in the design and the app shows similar affordances, but the keyboard handlers are not implemented — an advertised affordance that does nothing.
- Toasts (4b): tinted card with a 3px left accent bar and a leading icon. Add the **pending** variant, which does not exist today — slow uploads and deploys currently surface no feedback at all.

### Visual consistency

- Collapse raw Tailwind palette usage to the semantic roles. 266 raw hues across the app: `topology-nodes.tsx` 69, `event-console.tsx` 41, `metrics-page.tsx` 14. The design uses five roles, and all six sparklines are `--oc-accent`. Excludes the map's message-state colours — see Non-goals.
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

- Unify the three renderings of the advisory component (health drawer, Metrics & Health card, and the 1b variant) into one `<Advisory density="compact" | "full">`. Sentence case always; use the external-link icon, never a `→`.
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

- Copy-to-clipboard on identifiers. The ARN column is 320px and truncates, with no way to get the full value out.
- Deep-linkable state for filters and selected tabs, matching what the Raw State Debugger already does.
- Empty states audited per page — several lists fall back to a generic message.

## Open decisions

- **The `organizations` emulation tier.** `tiers.ts` defines `stub` as "all operations return 501", but `organizations` answers `DescribeOrganization` with a 200, so `stub` is false by that definition — while `partial` ("core operations work") overstates a single hardcoded op with no stored state. Recommendation: keep `stub` and reword the definition to something like "registered but effectively unimplemented — at most a hardcoded discovery response", rather than adding a fifth tier for one service. The string is user-facing in the dashboard tooltip and the compatibility docs.
- **`docs/README.md`'s coverage categories versus runtime tiers.** Two taxonomies with overlapping words: the docs file EKS under "IaC/discovery-oriented stub" while `tiers.go` classifies it `partial`. Decide whether they should be reconciled or explicitly separated.
- **Per-page filter placement.** The redesign puts the filter in the topbar search slot (the placeholder becomes `Filter log groups…`), giving that control two jobs. Wave 1 deliberately left per-page filters in place. Decide before building more filters.

## Non-goals

- Do not flatten the topology map's message-state colours during the palette collapse — they encode state, not decoration.
- Do not copy artboard 3c's hand-placed geometry; the map is force-directed.
- Do not build the health drawer before the pages it double-polls.
