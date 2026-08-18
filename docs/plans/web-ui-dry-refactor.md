# Web UI — DRY refactor and page-archetype componentisation

> Status: audit complete (2026-07-27), no code changed. Companion to
> [web-ui-polish-wave-2.md](./web-ui-polish-wave-2.md), which owns the *visual* backlog; this file
> owns the *structural* one. Where the two overlap (the detail-field component, the generic table
> wrapper, the spinner rollout, busy buttons) this document supersedes the wave-2 wording with a
> sequenced, sized plan. The palette collapse stays in
> [palette-categorical-tokens.md](./palette-categorical-tokens.md) and is out of scope here.
>
> Audit method: ripgrep over `web/src` (469 `.ts`/`.tsx` files, ~54,700 lines of non-test `.tsx`).
> `npx jscpd` was **not** run — it is not installed and the brief forbids installing anything.
> Every count below is a literal grep result, reproducible from the commands implied by each
> `file:line` citation. Two files (`components/ui/table.tsx`, `eventbridge/event-bus-detail.tsx`)
> were observed mid-edit by the concurrent typography agent; `TableCellProse` and the mono
> `TableCell` default are treated as landed.

---

## The thesis

Most Overcast screens are the same screen. Twenty-four list pages already share
`ResourceListPage`/`ResourceListCard`, and they collapsed to a genuinely uniform shape — but the
sharing stopped at the page frame. Everything *inside* the frame (the loading branch, the table,
the create dialog, the row actions, the status pill) is still copied per service, and the 25 detail
pages and 12 "index" pages never got a frame at all.

The measurable consequence: adding a new AWS service today means writing roughly **160 lines of
list page + 190 lines of detail page + 100 lines of create dialog + a per-service status badge and
a per-service `DetailRow`** — of which perhaps 40 lines are actually service-specific. The target
of this plan is that the same job is **one `columns` array, one `fields` array, one `zod` schema,
and a registry entry — about 90 lines total**, with the frame, the states, the chrome and the
keyboard contracts inherited.

The single biggest structural fact in the audit: **abstractions that already exist are routinely
bypassed.** `useResourceMutation` is used in 52 files while 21 files hand-roll the identical
`useMutation` + `invalidateQueries` + two toasts; `RefreshAction` is used on 24 list pages while 32
other files hand-roll `RefreshCw` + `animate-spin`; `Button.busy` is used by exactly two components
while 120 sites hand-roll `disabled={isPending}` + `<Spinner>`. Extraction alone will not fix that
— see [Guardrails](#5-guardrails).

---

## 1. Page archetypes

### Archetype A — Resource list page (24 instances, converted; 9 more unconverted)

**Converted instances** (import `ResourceListPage`): apigateway/api-list, applications/application-list,
cloudformation/stack-list, cloudfront ×5 (distribution, key-group, fle-config, fle-profile,
realtime-log-config, continuous-deployment-policy), cloudwatch/logs/log-group-list,
cognito/cognito-page, dynamodb/table-list, ecr/repository-list, ecs/cluster-list,
elasticache/cluster-list, kinesis/stream-list, lambda/function-list, lambda/layer-list,
msk/cluster-list, pipes/pipe-list, rds/instance-list, s3/bucket-list, sns/topic-list, sqs/queue-list.

**What legitimately varies:** the query, the columns, the row icon, the resource noun, the create
form's fields, whether rows are clickable, whether there is a selection column.

**What varies only accidentally** (evidence):

| Accidental variation | Count | Evidence |
| --- | --- | --- |
| `RawStateLink` present in the action cluster | 4 of 24 | present: s3, sns, sqs, dynamodb; absent from the other 20 |
| `ServiceDocsButton` present | 18 of 24 | absent: cloudfront ×5, lambda/layer-list |
| The `isLoading \|\| xs.length === 0 ? <QueryListState …/> : <Table>…` branch, re-typed per page | 24 | e.g. `sns/topic-list.tsx:74-91`, `kinesis/stream-list.tsx:90-…` |
| Status→`Badge` variant mapping, re-written per service | 20 defs | table in §1.5 below |
| Delete flow: `useState<string>()` + `ConfirmDialog` + `useResourceMutation` | 44 `deleteTarget` declarations across 36 files | `grep "const \[deleteTarget"` |
| Filter idiom `q ? xs.filter(x => (x.Name ?? "").toLowerCase().includes(q.toLowerCase())) : xs` | 52 occurrences, 24 files | top: events-page 6, pipe-list 4, mail-page 4, iam-page 4 |
| `ResourceListFilter` (the shared filter) actually used | 1 of the 18 filtering pages | only `cognito/cognito-page.tsx` |

**Composition API after the work** — `ResourceListPage` gains a `<ResourceTable>` child that owns
the state branch, the header row, the body and the row-action column:

```tsx
export function TopicList() {
  const q = useQuery(snsTopicsQueryOptions())
  const del = useResourceMutation({ options: deleteTopicMutationOptions(), invalidateKeys: [snsKeys.topics()], successTitle: "Topic deleted" })

  return (
    <ResourceListPage service="sns" title="SNS Topics" count={q.data?.length} query={q}
                      create={<CreateTopicDialog />}>
      <ResourceTable
        query={q}
        noun="topics"
        emptyIcon={Bell}
        rowKey={(t) => t.TopicArn}
        rowTo={(t) => ({ to: "/sns/$topic", params: { topic: shortName(t) } })}
        columns={[
          { header: "Name", cell: (t) => <ResourceName icon={Bell} name={shortName(t)} /> },
          { header: "ARN",  cell: (t) => <ArnText arn={t.TopicArn} /> },
        ]}
        onDelete={{ mutation: del, label: (t) => shortName(t), noun: "topic" }}
      />
    </ResourceListPage>
  )
}
```

`sns/topic-list.tsx` goes from **161 → ~45 lines**; `kinesis/stream-list.tsx` **186 → ~55**;
`msk/cluster-list.tsx` **327 → ~120** (it also contains its create dialog).

### Archetype B — Resource detail page (25 instances, zero scaffold)

All 25 `features/**/*-detail.tsx`. The shape is remarkably uniform:

| Element | Instances / 25 |
| --- | --- |
| `<PageHeader>` at the top | 24 |
| `<ApplicationOwnershipBanner>` immediately after | 22 |
| `flex w-full flex-col gap-4` root | 22 |
| Hand-rolled `if (isLoading) return <div …><Spinner/></div>` | 15 |
| Hand-rolled "not found" empty state | 7 |
| A key/value grid (`grid-cols-2 gap-x-8 …` + local `DetailRow`) | 13 |
| A tab strip | 13 |
| One or more sub-tables | 20 |
| `<ConfirmDialog>` for a destructive action | 12 |

There is **no shared detail scaffold at all**, which is exactly why the two systemic defects live
here: 205 `<Spinner>` sites across 88 files, and 14 hand-rolled `DetailRow`/`InfoRow`/`MetaRow`
definitions.

**What legitimately varies:** the fields, the tabs, the sub-tables, the action cluster, whether the
page is single-resource or a dashboard over several.

**What varies only accidentally:** the loading treatment (spinner size varies `h-5`/`h-6`,
padding varies `py-12`/`py-24`/`py-32`), the not-found copy, the refresh control (see §1.5), the
detail-row markup, and whether the label is `<dt>`/`<dd>` or `<span>`/`<span>` (5 vs 9 of the 14).

**Composition API after the work:**

```tsx
export function KmsKeyDetail({ keyId }: { keyId: string }) {
  const q = useQuery(kmsKeyDetailQueryOptions(keyId))
  return (
    <ResourceDetailPage
      query={q}
      title={(k) => k.metadata?.KeyId ?? keyId}
      notFound={{ noun: "key", icon: Key, backTo: "/kms" }}
      ownership={(k) => [k.metadata?.Arn, k.metadata?.KeyId]}
      actions={(k) => <><RefreshAction …/><KmsStateActions k={k} /></>}
    >
      {(k) => (
        <DetailFields>
          <DetailField label="Key ID" value={k.metadata?.KeyId} mono />
          <DetailField label="ARN" value={<ArnText arn={k.metadata?.Arn} />} copyable />
          <DetailField label="State" value={<StatusBadge status={k.metadata?.KeyState} />} />
          <DetailField label="Created" value={<Timestamp value={k.metadata?.CreationDate} />} />
        </DetailFields>
      )}
    </ResourceDetailPage>
  )
}
```

`kms/kms-key-detail.tsx` **196 → ~70 lines**; `ecs/task-detail.tsx` **190 → ~70**;
`eventbridge/event-bus-detail.tsx` **183 → ~60**.

### Archetype C — "Service index pages" are **not** a third archetype

The wave-2 backlog lists 12 pages as needing "their own layout decision first". Measured, **nine of
them have zero tabs and exactly one table** — they are Archetype A instances that were simply never
converted:

| Page | LOC | Tab strips | `<Table>` | Verdict |
| --- | --- | --- | --- | --- |
| `kms/kms-page.tsx` | 201 | 0 | 1 | Archetype A, unconverted |
| `ssm/ssm-page.tsx` | 310 | 0 | 1 | Archetype A, unconverted |
| `appsync/appsync-page.tsx` | 179 | 0 | 1 | Archetype A, unconverted |
| `stepfunctions/stepfunctions-page.tsx` | 181 | 0 | 1 | Archetype A, unconverted |
| `secretsmanager/secrets-manager-page.tsx` | 273 | 0 | 1 | Archetype A, unconverted |
| `ses/ses-page.tsx` | 230 | 0 | 1 | Archetype A, unconverted |
| `eks/eks-page.tsx` | 219 | 0 | 1 | Archetype A, unconverted |
| `apigateway/api-keys-page.tsx` | 223 | 0 | 1 | Archetype A, unconverted |
| `apigateway/usage-plans-page.tsx` | 366 | 0 | 2 | A + one nested sub-table |
| `eventbridge/eventbridge-page.tsx` | 324 | 1 | 2 | A ×2 behind tabs |
| `iam/iam-page.tsx` | 574 | 1 | 4 | A ×4 behind tabs |
| `ec2/ec2-dashboard.tsx` | 1260 | 1 | 5 | A ×5 behind tabs |
| `sts/sts-page.tsx` | 90 | 0 | 0 | Genuinely different — a single read-only identity card |

So the "third archetype" is really **A + tab strip**: a `<ResourceListSection>` (the list-page body
without its own page header) composed N times inside a tabbed page. That is one small addition to
Archetype A, not a new abstraction. Only `sts-page.tsx` is genuinely outside — and at 90 lines it
should stay bespoke.

### Archetype D — Create/edit dialog (14 bespoke + 1 shared, used 4×)

`CreateResourceDialog` (`components/create-resource-dialog.tsx`) exists and is used by exactly four
pages (appsync, eventbridge, iam, stepfunctions) — it only supports a single `name` field, so
everyone else copies it. There are 29 `useForm(` call sites and 98 `<DialogContent>` across 55
files.

The three near-identical single-field dialogs, side by side:

| File | LOC | Busy treatment |
| --- | --- | --- |
| `components/create-resource-dialog.tsx:96` | 107 | `<Spinner className="mr-1.5" /> Creating…` |
| `sns/components/create-topic-dialog.tsx:81` | 88 | `{isPending ? <Spinner className="h-4 w-4"/> : "Create"}` |
| `apigateway/components/create-rest-api-dialog.tsx:97` | 105 | `{isPending && <Spinner className="mr-2 h-3.5 w-3.5"/>} Create` |

Beyond the field set, they differ in nothing except those three renderings of the same busy state
and the presence/absence of `e.stopPropagation()`. Meanwhile the dialog primitive already ships
`DialogKeyHint`, `DialogIcon`, `onPrimaryAction` and `DialogHeader icon=` — used by **exactly one
component**, `ConfirmDialog`. So 54 of 55 dialog-bearing files silently lack the advertised
`⏎ to create · esc to cancel` contract.

**Composition API after the work** — a `<ResourceFormDialog schema fields …>` driven by the zod
schema, so `create-topic-dialog.tsx` becomes ~25 lines and `create-rest-api-dialog.tsx` ~30.

### Archetype E — Virtualized unbounded list (added 2026-08-18)

The screens that render a server-paginated listing through `@tanstack/react-virtual`: the
CloudWatch log viewers (where the techniques were developed and measured —
[logs-view-performance.md](./logs-view-performance.md)), the S3 object browser (retrofitted
2026-08-18), the debug state browser, the event console. What they share is not a component but a
**kernel of three mechanisms**, extracted as:

| Module | What it owns |
| --- | --- |
| `hooks/use-load-more-at-edge.ts` | Fetch the next page as the user nears the list edge, keyed on the **page token** — a boolean-keyed effect stalls when a fast response commits no fetching-state render, and an effect depending on `getVirtualItems()` re-runs every scroll frame. The hook's docblock carries the full race reasoning. |
| `lib/stable-row-key.ts` | WeakMap object-identity keys for rows with no natural identity (log events). Rows that *have* natural string identity use it directly — the S3 browser keys rows by prefix/key/key+versionId (`browserRowKey` in `features/s3/object-browser.ts`); index keys remount and re-measure every row on a sort flip or filter keystroke. |
| `lib/highlight-code.ts` | Prism highlighting behind a ~400-entry LRU with a ~100 KB size cap, keyed on (language, text). A cache hit returns the **identical** string, so an unchanged `dangerouslySetInnerHTML` leaves the DOM untouched — no style recalc and no MutationObserver records (the logs plan's §2b mutation-budget reasoning). `lib/format-body.ts` routes through it; callers cap what they highlight (the logs work measured Prism's frame budget falling around ~70 KiB). |

**What stays feature-local**, per the logs plan's deliberately-declined consolidations (its Phase
5): pin-to-bottom/unread logic (sort-direction-aware in one surface, prepend-anchored in another —
one hook serving both needs more configuration than either has code), the row renderings
themselves (a memoized row component per feature, fed primitives and identity-stable references,
is the pattern; a shared row is not), and the fetched+tailed merge pipeline (live-tail is a logs
concern). The same judgement applies here: the S3 browser's scan-cap drain, its folder/object/
version rows and its search-term highlighting stayed in `features/s3/`.

**Follow-up closed (2026-08-18):** the logs feature now runs on the kernel — `logEventKey`
re-exports `stableRowKey` (the name kept because "the key of a log event" is what call sites
mean), `highlightJSON` delegates to `highlightCode(text, "json")`, and the viewer's forward
edge-fetch uses `useLoadMoreAtEdge`. One inline effect deliberately remains: the backward
expansion captures its prepend-anchor snapshot atomically with scheduling the fetch, and
modelling a before-fetch callback in the hook would cost more than the duplication — the same
judgement as the pin-to-bottom logic above, recorded in the effect's comment.

### What "add a new AWS service page" looks like afterwards

1. One entry in `lib/service-registry.ts` (icon, colour, route, category) — already the single
   source of truth for sidebar, dashboard, search and map. **~12 lines, unchanged today.**
2. `features/<svc>/data.ts` — key factory + `queryOptions`/`mutationOptions`. **~40 lines**, already
   well-factored (136 query factories / 179 mutation factories across 34 files, all following the
   AGENTS.md pattern). Do not abstract further.
3. `features/<svc>/components/<svc>-list.tsx` — `<ResourceListPage>` + `<ResourceTable columns>`.
   **~45 lines.**
4. `features/<svc>/components/<svc>-detail.tsx` — `<ResourceDetailPage>` + `<DetailFields>`.
   **~60 lines.**
5. `features/<svc>/components/create-<svc>-dialog.tsx` — `<ResourceFormDialog>` + a zod schema.
   **~30 lines.**
6. Two 7-line route files.

**~200 lines, of which ~90 are service-specific data**, against ~600–800 today.

---

## 2. Prioritised extraction backlog

Ranked by (call sites collapsed × risk reduced) ÷ effort, with unblocking weighted up.

### P1 — `DetailField` / `DetailFields` — **S** — unblocks B1, B2

- **Collapses:** 14 local component definitions and ~180 call sites.
- **Files:** `cloudformation/stack-detail.tsx:570`, `cognito/cognito-pool-detail.tsx:2170`,
  `ec2/instance-detail.tsx:490`, `ec2/vpc-detail.tsx:799`, `ecs/task-detail.tsx:178`,
  `eventbridge/event-bus-detail.tsx:168`, `kms/kms-key-detail.tsx:181`, `rds/instance-detail.tsx:323`,
  `secretsmanager/components/secret-detail.tsx:274`, `ssm/ssm-parameter-detail.tsx:274`,
  `sts/sts-page.tsx:75`, plus two `MetaRow`s the wave-2 list missed
  (`mail/message-detail.tsx:222`, `s3/components/object-preview-dialog.tsx:141`) and the inline
  `<div className="flex flex-col gap-0.5">` blocks that bypass even the local component
  (`kms-key-detail.tsx:134`, `ec2/instance-detail.tsx:136`).
- **API:**
  ```tsx
  <DetailFields columns={2 | 3}>                       // owns grid-cols-2 gap-x-8 gap-y-3 md:grid-cols-3
    <DetailField label="ARN" value={…} mono copyable /> // label = FieldLabel spec; value mono by default
  </DetailFields>
  ```
- **Why first:** it is the smallest change with the widest reach, it makes the typography spec
  inheritable (today the label is `text-xs text-fg-muted` in 12 places, never the `fieldLabel`
  spec), and `ResourceDetailPage` (P2) wants it as a child.
- **Note:** `FieldLabel` already exists in `primitives.tsx:14` with the right 9px/.14em spec and has
  **zero** external users. `DetailField` should be built on it, not beside it.

### P2 — `ResourceDetailPage` — **M** — depends on P1; unblocks P4

- **Collapses:** 25 detail pages' frame — 24 `PageHeader` invocations, 22
  `ApplicationOwnershipBanner`, 22 root divs, **15 hand-rolled loading branches**, 7 not-found
  states, and (via `QueryListState`'s skeleton) most of the 205 `<Spinner>` sites.
- **Files:** all 25 `features/**/*-detail.tsx`, plus the 10 route-level guards that duplicate the
  same thing one layer up (`routes/rds/$instance.tsx:41-48`, `routes/sqs/$queue.tsx`,
  `routes/s3/$bucket.tsx`, `routes/ecs/$cluster.tsx`, `routes/ec2/$instanceId.tsx`,
  `routes/ec2/vpc.$vpcId.tsx`, `routes/lambda/$name.tsx`, `routes/appsync/$apiId.tsx`,
  `routes/cloudfront/$distributionId.tsx`, `routes/docs.tsx`).
- **API:** `query`, `title`, `meta`, `status`, `actions`, `ownership`, `notFound: {noun, icon,
  backTo}`, `children: (data) => ReactNode`. Loading routes through `SkeletonRows`/`SkeletonCards`,
  never a content-area spinner — this is the wave-2 "one component, pages inherit — not 208 edits"
  item, made concrete.
- **Highest risk reduced of any item:** it is the only way the skeleton treatment, the reduced-motion
  rule and the cold-boot state ever reach detail pages.

### P3 — `ResourceTable` — **M** — depends on nothing; unblocks P5, P9

- **Collapses:** the state-branch + table body of all 24 converted list pages, the 9 unconverted
  index pages, and the ~33 files that render a `<Table>` outside `ResourceListCard` (105 `<Table>`
  sites total; 150 sites of the bespoke `rounded-* border border-border` surface).
- **API:** `query`, `columns: Column<T>[]`, `rowKey`, `rowTo`, `noun`, `emptyIcon`, `emptyAction`,
  `select?`, `onDelete?`, `filter?`. Owns `QueryListState`, `ResourceListCard`, `TableHead`
  treatment, the row-action column, and the empty/error copy.
- **Explicitly an extension of `ResourceListCard`, not a competitor** — per wave-2's instruction.
  Sub-tables get `<ResourceTable variant="embedded">` (no card surface), which is what the 33
  bypassing files need.

### P4 — Route the busy-button contract through `Button.busy` — **S** — depends on nothing

- **Collapses:** 120 `disabled={…isPending}` sites and 72 `isPending && <Spinner>` renderings.
- **Files:** everywhere; concentrated in the 14 create dialogs and the 12 `ConfirmDialog` callers'
  neighbours.
- **`Button` already implements this** (`button.tsx:89`, `busy` + `busyLabel` + `BlinkingCursor`,
  never dimmed) and it is used by **two** components: `ConfirmDialog` and `RefreshAction`. This is a
  pure adoption task with a lint rule attached (see G3), not a design task.
- **Also folds in** the 7 raw `<Loader2>` renderings that bypass `Spinner`'s size clamp:
  `layout/global-search.tsx:418`, `ui/combobox.tsx:392` and `:491`,
  `cloudformation/stack-detail.tsx:373` and `:431`, and one wave-2 missed:
  `cloudformation/stack-list.tsx:142`.

### P5 — `ResourceFormDialog` — **M** — depends on P4

- **Collapses:** 14 bespoke create/edit dialogs and 29 `useForm(` sites down to a schema + a field
  list; brings the `⏎ to create · esc to cancel` contract (`DialogKeyHint`, `onPrimaryAction`) from
  1 file to ~50.
- **Files:** `apigateway/create-{http,rest}-api-dialog.tsx`, `sns/create-topic-dialog.tsx`,
  `kinesis/create-stream-dialog.tsx`, `cognito/create-pool-dialog.tsx`,
  `dynamodb/create-table-dialog.tsx`, `cloudformation/{create,update}-stack-dialog.tsx`, plus the
  inline dialogs in `s3/bucket-list.tsx:218`, `ecr/repository-list.tsx:215`,
  `ecs/cluster-list.tsx:231`, `elasticache/cluster-list.tsx:237`, `msk/cluster-list.tsx:232`,
  `rds/instance-list.tsx:468`, `sqs/queue-list.tsx:340`, `pipes/pipe-list.tsx:186`,
  `kms/kms-page.tsx:61`, `ssm/ssm-page.tsx:83`, `ses/ses-page.tsx:65`, `eks/eks-page.tsx:163`,
  `secretsmanager/secrets-manager-page.tsx:65`, `cloudfront/distribution-list.tsx:240`.
- **Supersedes `CreateResourceDialog`**, which becomes `<ResourceFormDialog>` with a one-field
  schema. Keep the old name as a thin alias for its 4 callers or migrate them in the same PR.

### P6 — `StatusBadge` + a shared status→tone table — **S** — depends on nothing

- **Collapses:** 20 status-mapping functions. Worse than duplication: **two of them disagree.**
  `ec2/ec2-dashboard.tsx:118` maps `terminated → default, stopped → danger`;
  `ec2/instance-detail.tsx:499` maps `terminated → danger, stopped → default`. The same instance
  shows a different colour on the list and on its own page.
- **Files:** `cloudformation/utils.ts:12,22`, `cognito/cognito-pool-detail.tsx:2209`,
  `ec2/ec2-dashboard.tsx:102,118`, `ec2/instance-detail.tsx:499`, `ec2/vpc-detail.tsx:122,808`,
  `ecs/cluster-detail.tsx:671`, `ecs/cluster-list.tsx:199`, `ecs/task-detail.tsx:166`,
  `elasticache/cluster-list.tsx:189`, `msk/cluster-list.tsx:185`, `kinesis/stream-list.tsx:34`,
  `kinesis/stream-detail.tsx:41`, `pipes/pipe-list.tsx:445`, `rds/instance-list.tsx:261`,
  `rds/instance-detail.tsx:344`, `map/topology-nodes.tsx:609`,
  `cloudwatch/cloudwatch-dashboard.tsx:62`, `apigateway/rest-api-detail.tsx:1287`.
- **API:** `<StatusBadge status={s} />` over a case-insensitive default table
  (`ACTIVE|AVAILABLE|RUNNING|ENABLED|*_COMPLETE → success`,
  `CREATING|PENDING|UPDATING|MODIFYING|*_IN_PROGRESS → warning`,
  `FAILED|DELETING|*_FAILED|ROLLBACK_COMPLETE → danger`, else `default`), with a per-service
  `overrides` escape hatch for the genuinely different cases (`pipes` uses `accent` for
  CREATING/UPDATING; CloudFormation's suffix matching in `cloudformation/utils.ts` is already the
  right shape and should feed the table rather than be replaced).

### P7 — `useCopyToClipboard` + `<CopyButton>` — **S** — **LANDED**

Landed ahead of the rest of the plan, because it turned out to be a live bug rather than a tidy-up:
`navigator.clipboard` is gated on a secure context, so **every** copy button in the app did nothing
at all when Overcast was served over plain HTTP from anything other than `localhost` —
`http://localhost.overcast.sh`, a LAN address, a container hostname. Silently: no error, no toast.

- **Collapsed:** all 13 `navigator.clipboard.writeText` sites across 11 files, including the 4
  identical three-line closures (`ec2/instance-detail.tsx` ×2, `ecs/task-detail.tsx`,
  `rds/instance-detail.tsx`) and the five in `cognito/cognito-pool-detail.tsx`. A local
  `CopyButton` in `cloudwatch/logs/log-events-viewer.tsx` was shadowing the name and is gone.
- **Shipped as three pieces:**
  - `lib/clipboard.ts` — the platform primitive. Prefers the async Clipboard API, falls back to a
    hidden `<textarea>` + `document.execCommand("copy")` where it is absent or rejects. The
    textarea is mounted beside the focused element rather than on `document.body` so a Radix focus
    scope does not yank focus and drop the selection mid-copy. Also exposes
    `canReadClipboardText()`, since *reading* has no fallback at all.
  - `hooks/use-clipboard.ts` — `useCopyToClipboard()`: success **and** failure toasts, plus the
    transient `copied` flag. For copy triggers that are not icon buttons (the labelled Key/Value/
    Link controls on the debug page).
  - `components/ui/copy-button.tsx` — `<CopyButton value noun tone>`, the default. `tone="inline"`
    is the bare glyph beside a value; `control` is the ghost icon button.
- **Decision 6 resolved** as recommended: `Copied {noun}` with the noun from the caller, which also
  supplies the accessible name (`Copy {noun}`) that these icon-only buttons never had.
- `features/debug/clipboard.ts` (the 5-line test seam) is deleted — folded in, not kept.
- **Guardrail landed early too**, in `lib/clipboard.test.ts` rather than the style test: a direct
  `navigator.clipboard.*()` call anywhere in `src/` outside `lib/clipboard.ts` fails the build. Not
  a ratchet — a hard zero, because any new direct call reintroduces the insecure-context bug.
- **First new affordance on the shared component:** each row in `ui/event-console.tsx` now copies
  its whole event envelope as pretty JSON, hover-revealed so the dense console stays readable. The
  envelope is built by one `eventEnvelope()` helper shared with the expanded view, so what is
  copied is exactly what was on screen.
- **Still unblocks** the wave-2 "copy-to-clipboard on identifiers" QoL item and P1's `copyable`
  prop; `DetailField` and `ArnText` should use `<CopyButton>` when they land.

### P8 — Adopt `useResourceMutation` in the 21 files that bypass it — **M** — depends on nothing

- **Collapses:** 72 raw `useMutation({…onSuccess: invalidate+toast, onError: toast})` blocks into
  the hook that already exists (`hooks/use-resource-mutation.ts`, 52 adopters).
- **Files:** apigateway/{api-keys-page, http-api-detail, rest-api-detail, usage-plans-page},
  cloudwatch/logs/{log-group-detail, log-group-list}, cognito/cognito-pool-detail,
  dynamodb/table-detail, ec2/instance-detail, kinesis/stream-detail, kms/kms-page,
  lambda/{create-wizard, layer-detail, layer-list}, s3/{bucket-config, bucket-detail},
  secretsmanager/{secret-detail, secrets-manager-page}, sns/publish-dialog, sqs/{queue-detail,
  queue-list}, ssm/{ssm-page, ssm-parameter-detail}.
- **Risk reduced:** every bypass is a place where the error toast can silently differ or be absent.
- **Pure mechanical adoption. Highest lines-removed-per-thought of anything in this list.**

### P9 — `useResourceFilter` + wire up `ResourceListFilter` — **S** — depends on P3

- **Collapses:** 52 `toLowerCase().includes()` filter expressions in 24 files, 18 `useState("")`
  filter declarations, and ~14 hand-rolled search inputs that duplicate `ResourceListFilter`
  (`kms/kms-page.tsx:99-107`, `eventbridge/event-bus-detail.tsx:90-98`, and the rest of the
  unconverted index pages) — `ResourceListFilter` has **one** caller today.
- **API:** `const { query, setQuery, items } = useResourceFilter(all, (x) => [x.Name, x.Arn])`.

### P10 — `Timestamp` component + collapse the local formatters — **S** — depends on nothing

- **Collapses:** 10 local time formatters and 52 raw `toLocale*` calls in 32 files.
- **The abstraction already exists and is ignored:** `lib/format.ts` exports `formatDate` (19
  importers) and `formatBytes` — yet `formatBytes` is **re-implemented four times**
  (`debug/debug-page.tsx:843`, `metrics/metrics-page.tsx:36`, `sqs/queue-detail.tsx:1030`,
  `ui/event-summary.ts:97`) and timestamps are re-implemented at `components/logs/log-viewer.tsx:27`,
  `ui/event-console.tsx:127`, `cloudwatch/cloudwatch-dashboard.tsx:28`,
  `cloudwatch/logs/log-events-viewer.tsx:32`, `cloudwatch/logs/log-group-detail.tsx:47`,
  `cloudwatch/logs/log-group-list.tsx:47`, `map/lambda-instance-node.tsx:53`,
  `map/lambda-invocations-drawer.tsx:44`, `map/log-stream-peek.tsx:63`.
- **API:** `formatDate` (absolute), `formatRelative` (`3m ago`), `formatDuration`, `formatBytes` in
  `lib/format.ts`; `<Timestamp value={…} relative? title? />` renders mono with the absolute value
  in a `title` attribute. The log-viewer variants are legitimately different (millisecond
  precision, no date) — give them a `precision="ms"` option rather than a separate function.

### P11 — `SectionHeading` — **S** — depends on nothing

- **Collapses:** 56 occurrences of the literal string `font-mono text-sm font-medium text-fg` used
  as a sub-section heading on detail pages.
- Note `SectionLabel` exists at `primitives.tsx:23` (mono 10px/.16em uppercase) with **zero**
  external users — a different spec for a different job. Decide whether the 56 sites should become
  `SectionLabel` (a real visual change, product call) or a new `SectionHeading` matching today's
  look. Recommend the latter first, then a single deliberate flip if design wants it.

### P12 — `Tab asChild` + retire the hand-rolled tab strips — **S** — depends on nothing

- **Collapses:** 9 hand-rolled tab strips into the existing `Tabs`/`TabList`/`Tab` primitive (11
  adopters, full roving-tabindex and arrow-key support that the copies lack):
  `apigateway/api-list.tsx:117`, `apigateway/http-api-detail.tsx:290`,
  `apigateway/rest-api-detail.tsx:507`, `appsync/api-detail.tsx:115`,
  `dynamodb/table-detail.tsx:475`, `kinesis/stream-detail.tsx:185`, `mail/message-detail.tsx:245`,
  `sqs/queue-detail.tsx:476,493`, `cloudwatch/logs/time-range-filter.tsx:195,204`.
- **Blocker to fix first:** `s3/components/bucket-tabs.tsx` is route-linked (`<Link>` per tab), which
  `Tab` cannot express — add `asChild` to `Tab`. `map/log-stream-peek.tsx:254` is a drawer-local
  strip with a different visual weight; leave it (see §4).

### P13 — `useNotFoundRedirect` — **S** — depends on P2

- **Collapses:** the 10 route-level guards listed under P2, each ~25 lines of
  `useQuery({retry:false})` + `useEffect(navigate + toast)` + a spinner. Only worth doing *after*
  P2, because most of them should move into `ResourceDetailPage.notFound` and disappear rather than
  be abstracted.

### Deferred / low value

- `head: ({params}) => ({meta:[{title: \`… — Overcast\`}]})` appears in 76 route files. A
  `serviceTitle("ECR")` helper saves 1 line each and costs an indirection. **Not worth it.**
- Pagination: only 6 files touch `nextToken`/`fetchNextPage` and they page different things
  (DynamoDB items, S3 keys, log events). ~~**Not yet a pattern.**~~ Partially superseded: the
  *near-edge auto-fetch* half became a pattern and is extracted (Archetype E's
  `useLoadMoreAtEdge`); the query definitions themselves stay per-feature, as before.
- "N of M" counters: 2 real sites (`debug-page.tsx:343`, `dynamodb/table-detail.tsx:676`). **No.**

---

## 3. Sequencing and file ownership

Agents run concurrently, so ownership is stated by **file glob**, and no two parallel tracks may
touch the same glob.

```
Wave 0 (all parallel — new files only, no call-site edits)
  ├── A: create components/ui/detail-fields.tsx        (P1)   owns: components/ui/detail-fields.tsx
  ├── B: create components/ui/resource-table.tsx       (P3)   owns: components/ui/resource-table.tsx
  ├── C: create components/ui/status-badge.tsx         (P6)   owns: components/ui/status-badge.tsx
  ├── D: DONE — lib/clipboard.ts + hooks/use-clipboard.ts + ui/copy-button.tsx (P7), call sites migrated
  ├── E: extend lib/format.ts + ui/timestamp.tsx       (P10)  owns: lib/format.ts, components/ui/timestamp.tsx
  └── F: add `asChild` to ui/tabs.tsx                  (P12)  owns: components/ui/tabs.tsx

Wave 1 (needs Wave 0 A; single owner of components/ui/resource-detail-page.tsx)
  └── G: create ResourceDetailPage                     (P2)   owns: components/ui/resource-detail-page.tsx,
                                                                    components/ui/primitives.tsx

Wave 2 (call-site migration — partition by FEATURE DIRECTORY, one agent per group)
        Each agent applies P1+P2+P4+P6+P7+P8+P10+P11 to its own directories in one pass.
        This is the key sequencing decision: migrating a file once for eight extractions is
        far cheaper and far less merge-prone than eight passes over the same file.
  ├── H: features/{ec2,ecs,eks}/**
  ├── I: features/{apigateway,appsync,cloudfront}/**
  ├── J: features/{cognito,iam,kms,secretsmanager,ssm,sts}/**
  ├── K: features/{s3,dynamodb,rds,elasticache}/**
  ├── L: features/{sqs,sns,kinesis,msk,pipes,eventbridge,stepfunctions,ses,mail}/**
  ├── M: features/{cloudformation,cloudwatch,lambda,applications}/**
  └── N: features/{debug,metrics,map,events,dashboard,docs}/**   (P8+P10+P11 only — these are
                                                                  not resource pages; see §4)

Wave 3 (needs Wave 2 in the affected directories)
  ├── O: ResourceFormDialog + migrate the 14 create dialogs   (P5)
  ├── P: convert the 9 unconverted index pages to Archetype A (§1 Archetype C)
  └── Q: useResourceFilter rollout                            (P9)

Wave 4
  └── R: useNotFoundRedirect / fold route guards into ResourceDetailPage (P13)
         owns: src/routes/**   — must be last; it is the only track touching routes/
```

**Hard rules for concurrent agents:**

- `components/ui/*` is written **only** in Waves 0 and 1. Wave-2 agents may not edit it; if a
  primitive is wrong they file it, they do not patch it.
- No Wave-2 agent may touch a directory outside its group, including for a one-line import fix.
- `lib/service-registry.ts`, `lib/nav-services.ts` and `src/routeTree.gen.ts` are off-limits to
  everyone in Waves 0–3.
- Wave 2 must land the P4 busy-button change **in the same commit** as the file's other edits — a
  separate pass over the same 88 files is the most collision-prone thing this plan could do.
- The concurrent typography pass owns `components/ui/table.tsx` today. Wave 0 track B must rebase
  onto its `TableCell`/`TableCellProse` result rather than re-deriving cell typography.

---

## 4. Consistency decisions needed

1. **EC2 instance-state colours contradict each other.** `ec2-dashboard.tsx:118` says
   `stopped → danger, terminated → default`; `instance-detail.tsx:499` says the reverse. **Technical
   call, decide now: `stopped → default` (a stopped instance is fine, just idle),
   `terminated → danger`.** Fold into P6's default table.

2. **`RawStateLink` on 4 of 24 list pages.** Either every list page gets it or none does. **Product
   call.** Recommendation: make it a prop of `ResourceListPage` defaulted **on**, since the Raw State
   Debugger is a first-class emulator feature and its absence on 20 pages looks like an oversight
   rather than a decision. Tradeoff: five extra pixels of chrome on every page.

3. **Busy control: spinner or blinking cursor?** Wave 2's design says cursor, `Button.busy`
   implements cursor, and 192 call sites still render a spinner. **Already decided by the design —
   this is only an adoption problem.** Flagged here because P4 will visibly change ~120 buttons in
   one wave and someone should expect that.

4. **Detail-field value typography: mono or sans?** All 14 local `DetailRow`s render a **sans**
   value unless `mono` is passed, so timestamps, counts and IDs are sans on every detail page — while
   `TableCell` just flipped to mono-by-default for exactly the same content. **Recommendation: mono
   by default with a `prose` escape hatch**, mirroring `TableCell`/`TableCellProse`. Tradeoff: ~30
   description fields will flip and need auditing. **Product-adjacent** — it is the same call
   wave-2 flagged for `TableCell`, and the two should be answered identically.

5. **`SectionLabel` (10px/.16em uppercase) vs the de-facto `font-mono text-sm font-medium` heading
   used 56 times.** Two specs for "names a group of things", and the declared one has zero users.
   **Product call.** Recommendation: ship P11 as `SectionHeading` matching today's rendering, then
   let design decide in one flip rather than letting the drift continue.

6. **Copy-confirmation copy.** Three strings today: "Copied!", "Copied to clipboard", "API key value
   copied". **Recommendation: `Copied <noun>` with the noun supplied by the caller**, since
   `useCopyToClipboard` will know it anyway.

7. **`Tab` needs `asChild`, or route-linked tabs need their own component.** `s3/bucket-tabs.tsx`
   navigates between sibling routes; `Tabs` is state-driven. **Technical call: add `asChild` to
   `Tab`** — the alternative is a second tab component, which is how the nine hand-rolled strips
   happened in the first place.

8. **Should `ResourceDetailPage` own the not-found redirect, or the route?** Ten routes do it today,
   seven detail components do it themselves. **Recommendation: the component**, because the
   component knows the noun and the back-link, and because a route-level guard forces a second
   `useQuery` with `retry: false` over the same key.

---

## 5. Do-not-extract list

- **`features/<svc>/data.ts` key factories.** 34 files, 136 query factories, 179 mutation factories,
  all following the AGENTS.md shape. They *look* like boilerplate, but a `createResourceKeys()`
  generator would erase the literal `as const` tuples that give TanStack Query its key inference,
  and the per-service shapes genuinely differ (`ebKeys.rules()` vs `apigwKeys.restApis()` vs
  `s3Keys.objects(bucket, prefix)`). **Leave alone.**

- **`services/api/<svc>.ts`.** 32 files wrapping AWS SDK v3 commands. The apparent repetition is the
  SDK's own surface; abstracting over it would reintroduce the hand-rolled-fetch layer AGENTS.md
  explicitly bans.

- **`features/map/**`.** `topology-nodes.tsx` (1811 lines), `lambda-instance-node.tsx`,
  `log-stream-peek.tsx`, `map-minimap.tsx`. Its `fmtAgo`/`fmtDuration`/`fmtRemaining` helpers, its
  tab strip, its status pill and its 47 raw palette classes all look like duplicates of the
  app-wide versions, but the map is a force-directed canvas with per-message-state colour encoding
  that wave-2 already declared a non-goal. **Only P8 (`useResourceMutation`) and dead-code removal
  should touch it.**

- **`features/debug/debug-page.tsx` and `features/metrics/**`.** Developer tools with a deliberately
  denser table than the resource pages (912 and 887 lines). Forcing them through `ResourceTable`
  would either bloat `ResourceTable`'s prop surface or thin the debugger. **Take `formatBytes` and
  the local timestamp formatter from them (P10) and nothing else.**

- **`sts/sts-page.tsx` (90 lines).** A single read-only identity card, no list, no table, no
  mutation. It should adopt `DetailField` and stop there.

- **The per-service create *forms*.** P5 extracts the dialog frame and the submit/busy/keyboard
  contract. It must **not** try to generate the fields — `cognito/create-pool-dialog.tsx`,
  `dynamodb/create-table-dialog.tsx`, `lambda/create-wizard.tsx` (994 lines, multi-step) and
  `cloudformation/create-stack-dialog.tsx` have genuinely different field graphs with cross-field
  validation. A schema-driven field renderer is the classic over-abstraction here.

- ~~**`useEventStream` vs `useEventSource`.**~~ Done. These were *not* two solutions to one problem —
  `use-event-stream.ts` is the SharedWorker-backed singleton and `use-event-source.ts` was a
  raw-EventSource hook with zero callers. Both it and its only consumer `use-page-unloading.ts` are
  deleted (139 lines); the SharedWorker client and worker remain the only EventSource owners.

- **`ConfirmDialog` vs `ResourceFormDialog`.** Keep them separate. `ConfirmDialog` has no form, has
  a deliberate focus override (`confirm-dialog.tsx:66-73` — auto-focusing the destructive button is
  worse) and a different keyboard contract. Merging them would put a destructive-action special case
  inside the create path.

- **The 7-line route files.** 44 of them are already minimal passthroughs. Nothing to gain.

---

## 6. Dead and over-abstracted code found

Removing these is part of the work, not a separate task — an unused export is an invitation to
build a parallel one.

| Symbol | Location | External users |
| --- | --- | --- |
| `Separator` | `ui/primitives.tsx:203` | 0 |
| `FieldLabel` | `ui/primitives.tsx:14` | 0 — **but P1 should adopt it rather than delete it** |
| `SectionLabel` | `ui/primitives.tsx:23` | 0 — see decision 5 |
| `TableEmpty` | `ui/table.tsx:96` | 0 |
| `CardHeader`, `CardTitle`, `CardDescription`, `CardFooter` | `ui/card.tsx` | 0 (only `Card` + `CardContent` are used, 11 files) |
| `ComboboxCompact` | `ui/combobox.tsx` | 1 (`region-select.tsx`) — arguably fine |
| ~~`useEventSource` + `usePageUnloading`~~ | ~~`hooks/`~~ | deleted — 139 dead lines removed |
| ~~`debugClipboard`~~ | ~~`features/debug/clipboard.ts`~~ | deleted — folded into P7 |
| `ResourceListFilter` | `ui/resource-list-page.tsx:117` | 1, while 14 files hand-roll the same input |
| `RowAction` `tone` prop | `ui/resource-list-page.tsx:159` | `tone="danger"` used; verify `neutral` is ever passed explicitly |

---

## 7. Guardrails

Both existing mechanisms can be extended, and both should be — the audit's clearest lesson is that
extraction without enforcement decays within a release.

### The eslint plugin (`web/eslint-plugin-classnames/`)

Six rules today, all `warn`, all about `cn()` hygiene. It is an ESLint flat-config plugin with a
`rules/` directory — adding rules is a file plus one line in `index.js`. Proposed additions:

- **`no-local-detail-row`** — flag a local `function DetailRow|InfoRow|MetaRow` in
  `src/features/**`. Directly prevents P1's regression. *(Trivial: a `FunctionDeclaration` name
  check.)*
- **`prefer-button-busy`** — flag a JSX `<Button>` whose `disabled` expression mentions `isPending`
  or whose children contain `<Spinner>`. Enforces P4. *(Moderate: JSX attribute + child scan.)*
- **`no-raw-spinner-in-content`** — flag `<Spinner>` that is not inside a `<Button>`, `<Badge>` or
  toast. Encodes the 5b rule ("14–16px, chips and toasts only") that today lives only in a docstring.
- **`prefer-shared-formatter`** — flag `toLocaleString`/`toLocaleDateString`/`toLocaleTimeString`
  and any local `function format(Bytes|Date|Duration)` outside `src/lib/format.ts`. Enforces P10 and
  would have caught all four `formatBytes` copies.
- **`prefer-use-resource-mutation`** — flag `useMutation(` in `src/features/**`. Enforces P8. Needs
  an allowlist comment for the handful of mutations that genuinely want custom `onError`.
- **`no-duplicate-class-cluster`** — a generic rule that fails when a literal class string over N
  tokens appears in more than M files. Would have surfaced `font-mono text-sm font-medium text-fg`
  (56 sites) and `grid grid-cols-2 gap-x-8 gap-y-3` (17 sites) before they were 56 and 17. Highest
  leverage of the six, and the only one that catches *future* clusters rather than known ones.

Set new rules to `warn` alongside the existing six, and flip the whole plugin to `error` once the
backlog lands — a mixed severity is what let 392 raw palette classes accumulate.

### The style test (`web/src/styles/global.test.ts`)

129 lines; already walks every `.ts`/`.tsx` file and fails the build on colour utilities whose
`--color-*` root is not declared in `global.css`'s `@theme` block. It is the right place for
assertions that are counts rather than lint diagnostics, because it can encode a **ratchet**:

- `expect(countOf("<Spinner")).toBeLessThanOrEqual(N)` with `N` decremented per wave. Cheap,
  unambiguous, and it makes the 205→~20 spinner reduction a build-enforced fact rather than a hope.
- The same ratchet for `disabled={…isPending}` (120), local `DetailRow` definitions (14), raw
  `useMutation(` in `features/**` (72), and `<Loader2` (7).
- A hard `expect(0)` for symbols that must never return: `<Loader2` outside `ui/primitives.tsx`.
  (The equivalent rule for `navigator.clipboard` shipped with P7 and lives in
  `lib/clipboard.test.ts`, next to the module it protects, sharing the tree walk via
  `test/source-files.ts`.)

A ratchet test is strictly better than a lint rule for the migration period: it permits the existing
sites, forbids new ones, and its failure message can name the file that grew the count.

### Convention

Add to `web/AGENTS.md`, under "Component conventions":

> Before writing a page, check `components/ui/` for a scaffold. A list page is
> `ResourceListPage` + `ResourceTable`; a detail page is `ResourceDetailPage` + `DetailFields`; a
> create flow is `ResourceFormDialog`. If a scaffold nearly fits, extend the scaffold — do not fork
> it into `features/`. A component defined inside a `features/` file must be specific to that
> service; anything a second service would want belongs in `components/ui/`.

And, given what this audit found, a second line:

> If you find yourself writing `useMutation`, `navigator.clipboard`, `toLocaleString`, a status→variant
> ternary, or a `flex flex-col gap-0.5` label/value pair — stop. All five already exist.
