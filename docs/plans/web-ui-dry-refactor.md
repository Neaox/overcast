# Web UI — DRY refactor and page-archetype componentisation

> Status: audit 2026-07-27; backlog largely open as of 2026-08-23. Landed so far: P7 (clipboard
> kernel, marked inline), Archetype E's virtualized-list kernel (2026-08-18), the dead-code
> deletions struck through in §5/§6, and P3's `ResourceTable` (`components/ui/resource-table.tsx`),
> landed 2026-08-22 (#1200 wave 1) with 8 of the 12 Archetype-C index pages converted: `kms`, `ssm`,
> `appsync`, `stepfunctions`, `secretsmanager`, `ses`, `eks`, `apigateway/api-keys-page`.
> `ResourceListPage` also gained a `description` passthrough (the prose subtitle every one of those
> pages needed) as part of the same change. **2026-08-23 (#1200 wave 2):** `ResourceListSection`
> (`components/ui/resource-list-section.tsx`) — the Archetype-C tab-strip variant described in §1 —
> was built, and the remaining four pages converted: `apigateway/usage-plans-page` (its main list
> plus the nested plan-keys sub-table, the first real caller of `ResourceTable`'s
> `variant="embedded"`), `eventbridge-page` (2 tabs), `iam-page` (4 resource tabs — Users, Roles,
> Policies, Groups; the Simulator tab is not a resource list and is untouched), and `ec2-dashboard`
> (5 tabs — Instances, VPCs, Security Groups, Elastic IPs, NAT Gateways). `sts-page` stays bespoke
> per this section's own recommendation. **All 12 pages in the Archetype C table are now resolved**
> (11 converted + `sts-page` deliberately bespoke) — #1200 is closed. **2026-08-23 (#1203):** P9's
> URL-binding half landed — `useFilterSearchParam` (`hooks/use-filter-search-param.ts`) wires
> `ResourceListFilter` to the route's `q` search param (deep-linkable, debounced, `replace: true`,
> same contract Request Traces' filters use) — applied to the five single-list pages that had a
> filter box (kms, ssm, appsync, stepfunctions, cognito) and to the tabbed `iam-page`/
> `eventbridge-page` from #1200 wave 2 above, which also gained a `tab` search param so the selected
> tab deep-links too (switching tabs clears `q`, matching `TabPanel`'s existing unmount-on-switch
> behaviour). `ResourceTable`/`QueryListState` gained `isFiltered`/`onClearFilter` in the same
> change, so the empty state distinguishes "nothing exists" from "nothing matches" without each page
> hand-rolling the ternary. The client-side matching consolidation (`useResourceFilter` itself) is
> still open — see P9. **2026-08-23 (#1327 wave A):** `ResourceTable`'s row model is now TanStack
> Table v9 (`useTable`), so sorting, column visibility and optional pagination/virtualization are
> one engine shared by every list instead of four ad-hoc sort states; `hooks/use-sort-search-param.ts`
> deep-links the sort as `?sort=name` / `?sort=-name` (JSON:API's leading-dash form) the way
> `useFilterSearchParam` deep-links `q`.
> **2026-08-23 (#1327 waves B/C — CloudFront):** the six CloudFront list pages (`distribution-list`, `continuous-deployment-policy-list`, `fle-config-list`, `fle-profile-list`, `key-group-list`, `realtime-log-config-list`) and the `distribution-detail` Origins / Origin Groups / Invalidations sub-tables are on `ResourceTable`, with `?sort=` deep-linked from `routes/cloudfront/*`; `monitoring-subscription-panel` and the distribution's Configuration grid stay bespoke as label/value views, each with the CONTRIBUTING § Tables reason in a comment.
> See P3 for the feature set, the state-ownership split and the bundle numbers; #1327's waves B–D
> move the remaining 55 bespoke `<Table>` sites onto it. **2026-08-23 (#1327 waves B/C, compute
> family):** `lambda/function-list`, `lambda/layer-list`, `ecs/cluster-list`,
> `autoscaling/group-list` and `applications/application-list` are on `ResourceTable`, as are the
> sub-tables in `lambda/layer-detail`, `ecs/cluster-detail` (5), `applications/application-detail`,
> `ec2/instance-detail` (2) and `ec2/vpc-detail` (7) via `variant="embedded"` — 24 bespoke
> `<Table>` sites in all. Sort is local state on every one of them: none of these five routes
> validates search params today, and adding `sort` would mean editing route files outside this
> change's fence (see the PR for the follow-up). Two gaps surfaced: `ResourceTable` has no slot
> after its empty state (Lambda's `RegionElsewhereNotice` has to sit there, so that page embeds the
> table in its own card) and no per-row class hook (Auto Scaling's selected-row tint became a
> chevron column, the `usage-plans-page` shape).
> **2026-08-23 (#1327 waves B/C — edge/identity):** `apigateway/api-list` (both tabs),
> `waf/web-acl-list` and `cognito-page` are on `ResourceTable`, plus 14 `variant="embedded"`
> sub-tables across `apigateway/http-api-detail` (routes, integrations, stages, authorizers),
> `apigateway/rest-api-detail` (stages, deployments, authorizers), `appsync/api-detail` (data
> sources, resolvers, functions, API keys, schema types) and `cognito-pool-detail` (users, group
> members); `waf/index` and `cognito/index` gained the `sort` search param (Cognito's next to its
> existing `q`). Two tables stay bespoke with a reason at the call site: `rest-api-detail`'s
> resource tree expands a row into per-method `<TableRow>`s and `ResourceTable` has no row-expansion
> concept (the same limitation §5 records for EC2), and `cognito-pool-detail`'s user-attribute grid
> is an editable name/value grid, not a resource list.
>
> **2026-08-23 (#1327 waves B/C — data family):** the six data-service list pages (`dynamodb/table-list`,
> `rds/instance-list`, `elasticache/cluster-list`, `efs/file-system-list`, `s3/bucket-list`,
> `ecr/repository-list`, each with a `sort` search param) and three detail sub-tables
> (`ecr/repository-detail`'s Images, `dynamodb/table-detail`'s GSI and LSI listings, `variant="embedded"`)
> are converted; `dynamodb/table-detail`'s Items table (row selection + row expansion + `LastEvaluatedKey`
> paging), its Key Schema grid, and `rds/instance-detail`'s Events feed (needs a per-row tone
> `ResourceTable` does not have) stay bespoke with the reason at the call site. None of the other scaffold
> components exist yet —
> no `status-badge.tsx`, `resource-detail-page.tsx`,
> `timestamp.tsx`, `resource-form-dialog.tsx`, `use-resource-filter.ts`, `SectionHeading`, or
> `Tab asChild` — so P2, P4–P6, P8, P10–P13 remain to do. **2026-08-23 (#1101, P1):** P1 is
> substantially landed. Its component turned out to already exist as
> `components/ui/definition-card.tsx` (`DefinitionCard`/`DefinitionList`/`Definition`, built in
> #362), so P1 became adoption rather than construction: those names are P1 — no `DetailField`
> alias — the component gained `copyable` over P7's clipboard kernel, and the five attribute grids
> the #1327 waves had left bespoke (`applications/application-detail`,
> `cloudfront/distribution-detail`'s Configuration tab, `cloudfront/monitoring-subscription-panel`,
> `ecs/cluster-detail`'s primary-deployment `<dl>`, `waf/web-acl-detail`'s local `Info`) now render
> through it, as does Cognito's — whose `DetailRow` was the last local detail-row definition in the
> tree, and whose deletion takes that ratchet to a hard zero. See P1 for the decision, the
> conversion list, and the grids deliberately left alone (API Gateway's stat tiles, Cognito's
> editable attribute form, `stack-diagnostics`' facts dump, `https-section`'s `StatusRow`).
> CONTRIBUTING § "Attribute grids" now states the rule alongside § Tables. Nothing on P1 is owed. **2026-08-23 (#1201):** the §6 dead-code
> follow-ups from the #1200 waves are resolved — `Separator` (0 users) is deleted; `TableEmpty` and
> the `Card` subcomponents turned out to have gained real callers since the original audit (see §6)
> and are kept; `RowAction`'s `tone="neutral"` is confirmed to be the default rather than an explicit
> literal anyone passes. All six §7 eslint guardrail rules
> (`no-local-detail-row`, `prefer-button-busy`, `no-raw-spinner-in-content`, `prefer-shared-formatter`,
> `prefer-use-resource-mutation`, `no-duplicate-class-cluster`) and all five original `global.test.ts` count
> ratchets (`<Spinner>`, `disabled={…isPending}`, local `DetailRow`, raw `useMutation(`, `<Loader2`,
> and since #1101 hand-written `<dt>`)
> are landed — see §7 for baselines. **2026-08-23 (#1202):** §4 items 1, 2, 4, 5, 6 (EC2 colours,
> `RawStateLink`, detail-field typography, `SectionLabel`, copy confirmation) are decided — see each
> item's **Decided** paragraph. Companion to
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
| A key/value grid (`grid-cols-2 gap-x-8 …` + local `DetailRow`) | 13 at audit; **0 hand-rolled after P1** — every one renders through `Definition`, and no local `DetailRow`/`InfoRow`/`MetaRow` definition survives anywhere in `features/**` |
| A tab strip | 13 |
| One or more sub-tables | 20 |
| `<ConfirmDialog>` for a destructive action | 12 |

There is **no shared detail scaffold at all**, which is exactly why the two systemic defects live
here: 205 `<Spinner>` sites across 88 files, and 14 hand-rolled `DetailRow`/`InfoRow`/`MetaRow`
definitions. (The detail-row half is resolved by P1 — one local definition survives, and it is
fenced behind an open PR.)

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
        <DefinitionCard>
          <Definition label="Key ID" value={k.metadata?.KeyId} copyable />
          <Definition label="ARN" value={<ArnText arn={k.metadata?.Arn} />} copyable={k.metadata?.Arn} />
          <Definition label="State" value={<StatusBadge status={k.metadata?.KeyState} />} />
          <Definition label="Created" value={<Timestamp value={k.metadata?.CreationDate} />} />
        </DefinitionCard>
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
| `kms/kms-page.tsx` | 201 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `ssm/ssm-page.tsx` | 310 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `appsync/appsync-page.tsx` | 179 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `stepfunctions/stepfunctions-page.tsx` | 181 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `secretsmanager/secrets-manager-page.tsx` | 273 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `ses/ses-page.tsx` | 230 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `eks/eks-page.tsx` | 219 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `apigateway/api-keys-page.tsx` | 223 | 0 | 1 | **Converted 2026-08-22** (#1200) |
| `apigateway/usage-plans-page.tsx` | 366 | 0 | 2 | A + one nested sub-table — **converted 2026-08-23** (#1200 wave 2) |
| `eventbridge/eventbridge-page.tsx` | 324 | 1 | 2 | A ×2 behind tabs — **converted 2026-08-23** (#1200 wave 2) |
| `iam/iam-page.tsx` | 574 | 1 | 4 | A ×4 behind tabs — **converted 2026-08-23** (#1200 wave 2) |
| `ec2/ec2-dashboard.tsx` | 1260 | 1 | 5 | A ×5 behind tabs — **converted 2026-08-23** (#1200 wave 2) |
| `sts/sts-page.tsx` | 90 | 0 | 0 | Genuinely different — a single read-only identity card |

So the "third archetype" is really **A + tab strip**: a `<ResourceListSection>` (the list-page body
without its own page header) composed N times inside a tabbed page. That is one small addition to
Archetype A, not a new abstraction. Only `sts-page.tsx` is genuinely outside — and at 90 lines it
should stay bespoke.

**`ResourceListSection` (built 2026-08-23, #1200 wave 2):** `components/ui/resource-list-section.tsx`.
Takes `actions` (the control row — filter, refresh, create — rendered above the body, the tab-local
analogue of `ResourceListPage`'s header actions minus a title) and `children` (typically a
`<ResourceTable variant="embedded">`, but deliberately untyped beyond `ReactNode` — EC2's instance
state-filter chip row and the usage-plan/IAM-group expanded-detail blocks are extra content passed
alongside the table, not a second prop). One adaptation surfaced during the conversion: IAM's Groups
tab previously expanded a member list as an extra table row inserted inline (via a `flatMap` trick);
`ResourceTable` has no row-expansion concept, so it now renders the expanded group's members in a
bordered block below the table — the same pattern `usage-plans-page`'s plan-keys expansion already
used. Functionally identical (toggle, membership list), position differs (bottom of the list rather
than inline under the clicked row). Two conditional-delete cases (EventBridge's undeletable
`default` bus, EC2's undeletable default VPC) kept a hand-rolled `rowActions` button instead of
`ResourceTable`'s `onDelete`, which has no per-row-disable hook — extending the shared kernel for a
two-instance edge case was judged worse than the small duplication.

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
4. `features/<svc>/components/<svc>-detail.tsx` — `<ResourceDetailPage>` + `<DefinitionCard>`.
   **~60 lines.**
5. `features/<svc>/components/create-<svc>-dialog.tsx` — `<ResourceFormDialog>` + a zod schema.
   **~30 lines.**
6. Two 7-line route files.

**~200 lines, of which ~90 are service-specific data**, against ~600–800 today.

---

## 2. Prioritised extraction backlog

Ranked by (call sites collapsed × risk reduced) ÷ effort, with unblocking weighted up.

### P1 — the shared definition table — **S** — unblocks B1, B2 — **substantially landed 2026-08-23**

**The component is `components/ui/definition-card.tsx`, and its names are `DefinitionCard` /
`DefinitionList` / `Definition`.** This section originally specified a `DetailFields`/`DetailField`
pair "to be built on `FieldLabel`". It was already built, under a different name, in #362 — same
contract, same reasons: the label in the `fieldLabel` spec from `lib/typography.ts` (so a
detail-page label and a column header read alike), the value **mono by default** with
`variant="prose"` as the marked exception, absence rendering as an em dash, and a container-query
grid (1 → 2 → 3 columns at `@3xl`/`@5xl`) so the shape follows the card rather than the viewport.

**Decision: adopt those names; do not alias.** Introducing `DetailFields`/`DetailField` as thin
wrappers would create exactly the second name for one idea that this plan exists to remove, and
would have to be threaded through 24 files that already import the originals. One component, one
name. The plan's original API sketch is superseded by:

```tsx
<DefinitionCard>                                       {/* or <DefinitionList> when not in a card */}
  <Definition label="ARN" value={…} copyable full />    {/* label = fieldLabel spec; value mono */}
  <Definition label="Comment" value={…} variant="prose" />
</DefinitionCard>
```

On `FieldLabel` (`primitives.tsx:14`, still zero users): the single source of truth is the
`fieldLabel` *token*, which both `FieldLabel` and `Definition` consume. `Definition` cannot render
`FieldLabel` itself — a definition list's label has to be a `<dt>`, and `FieldLabel` is a `<span>` —
so the token, not the component, is what makes the spec inheritable. `FieldLabel`'s own fate stays a
§6 dead-code question, not a P1 blocker.

**Added in this change** (the one gap between the built component and this section's spec):
`copyable`, on `Definition`. It puts the shared inline `CopyButton` (P7's kernel) beside the value
and names it after the label, lowercasing every non-acronym word so the toast reads "Copied domain
name" and "Copied ARN" rather than title case. `copyable={text}` supplies the clipboard text when
the value is a node (an `<ArnText>`, a badge). An absent value never gets a control — the em dash is
not the value.

**Converted (this change):** `applications/application-detail` (attribute card, and the em-dashed
Description row now stays visible when unset instead of vanishing), `cloudfront/distribution-detail`
(the Configuration tab, off a two-column `<Table>`), `cloudfront/monitoring-subscription-panel`
(likewise), `ecs/cluster-detail` (the primary-deployment `<dl>`, the last hand-rolled `<dt>` on a
detail page), and `waf/web-acl-detail` (its local `Info` tile component — a 14th local detail-row
definition that `no-local-detail-row` never saw, because the rule matches only the three names
`DetailRow|InfoRow|MetaRow`; deleted here, and widening the rule is a §7 follow-up).

**Already converted before this change**, by the #1200/#1327 waves and earlier: `cloudformation/
stack-detail`, `dynamodb/table-detail`, `ec2/instance-detail`, `ec2/vpc-detail`, `ecs/task-detail`,
`eventbridge/event-bus-detail`, `kms/kms-key-detail`, `lambda/configuration-tab`,
`lambda/function-overview`, `mail/message-detail`, `pipes/pipe-detail`, `rds/instance-detail`,
`s3/config-section`, `s3/object-preview-dialog`, `secretsmanager/secret-detail`,
`secretsmanager/secret-rotation-card`, `sqs/queue-detail`, `ssm/ssm-parameter-detail`,
`sts/sts-page`. Every `<div className="flex flex-col gap-0.5">` block this section named is gone;
`grep '<dt' src` outside `definition-card.tsx` now returns one file (see below).

**Second pass — done in the same change, once #1377 merged mid-flight:**
`cognito/cognito-pool-detail`'s local `DetailRow` was `Definition` plus a hand-rolled copy button,
so `copyable` made it redundant: 17 call sites became `Definition` and the wrapper is deleted. That
is the **last** `no-local-detail-row` hit, and `global.test.ts`'s ratchet on those three names is now
a hard `expect(0)` rather than a ceiling. API Gateway's two summary grids
(`http-api-detail`, `rest-api-detail`) keep their tile layout — see below — but their eight labels
now take the shared `fieldLabel` token instead of restating `font-mono text-xs text-fg-muted`.

Nothing is owed on P1 after this. The remaining bespoke `<Table>`s in the wave PRs still open
(#1380, #1381, #1388) are resource lists, which is #1327's scope, not this one's — no attribute grid
is fenced behind them.

**Deliberately not converted**, with reasons, so the next reader can tell a decision from an
oversight:

- API Gateway's four-up summary tiles on `http-api-detail` and `rest-api-detail`. They are **stat
  tiles**, not an attribute grid: the resource and stage counts are drawn at `text-2xl`, which is the
  point of the tile, and `Definition` would flatten them to a 12px mono value. Only the label token
  moved. A shared stat tile is a separate extraction (`metrics/stat-pill` is the nearest existing
  one) and belongs to whoever takes it, not to P1.
- `cognito`'s user *attribute* grid — editable name/value rows, so it is a form, not a definition
  list; #1327's wave notes reached the same conclusion for the same reason.
- `cloudformation/stack-diagnostics`'s `FactsBody` — a `grid-cols-[max-content_1fr]` dump whose
  labels legitimately repeat within one section. It is dense diagnostic output, not a detail page's
  attribute grid, and its `max-content` label column is the point.
- `settings/https-section`'s `StatusRow` — a `justify-between` row with the control right-aligned.
  That is a settings affordance, not a label/value pair; `Definition` would pull the control back
  against the label.
- DynamoDB's Key Schema grid, which #1327's wave notes list here: it is a genuine three-column
  table (attribute × key type × data type) over a list of key attributes, so it belongs to #1327,
  not to P1.

**Guardrail:** `distribution-detail.test.tsx` now asserts the Configuration tab renders `<dt>`s in
the `fieldLabel` spec with mono `<dd>`s and named copy controls — a page-level test, so hand-rolling
the grid back into the page fails CI rather than only lint-warning.

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

### P3 — `ResourceTable` — **M** — **LANDED 2026-08-22** (#1200), re-engined on TanStack Table v9 2026-08-23 (#1327 wave A); still unblocks P5, P9

- **Collapses:** the state-branch + table body of all 12 index pages in §1's Archetype C table —
  8 converted in #1200 wave 1 (2026-08-22), the remaining 4 (`apigateway/usage-plans-page`,
  `eventbridge-page`, `iam-page`, `ec2-dashboard`) in #1200 wave 2 (2026-08-23), which also shipped
  the `ResourceListSection` companion (see §1 Archetype C). **Not yet applied to** the 24
  already-converted list pages or the ~33 files rendering a bare `<Table>` outside
  `ResourceListCard` — that adoption sweep is still open (tracked in #1101, not #1200, which scoped
  to the unconverted index pages only).
- **Shipped API** (`components/ui/resource-table.tsx`): `query: {data, isLoading, error}`,
  `columns: ResourceTableColumn<T>[]` (`header`, `cell`, `headerClassName?`, `cellClassName?`,
  `prose?` — routes the cell through `TableCellProse` instead of the mono default), `rowKey`,
  `onRowClick?`, `noun`, `emptyIcon?/emptyTitle?/emptyDescription?/emptyAction?`, `errorTitle?`,
  `rowActions?`, `variant?: "card" | "embedded"`. One deliberate deviation from the plan's original
  sketch: `onDelete` is **caller-controlled** (`target`, `onRequest`, `onOpenChange`, `mutation`,
  `getId`, `label`, `noun`, plus `title?`/`description?`/`confirmLabel?`/`actionLabel?` overrides)
  rather than owning `deleteTarget` state itself — `useResourceMutation`'s `onSuccess` is what
  clears the target today, and that callback lives on the page. `rowTo`/`select?`/`filter?` from
  the original sketch were **not** built: no converted page needed row-level `<Link>` typing
  against the router's route tree (row click handlers are caller-supplied closures instead, which
  sidesteps that entirely), and none of the 8 pages use bulk selection or an in-table filter.
- **Explicitly an extension of `ResourceListCard`, not a competitor** — per wave-2's instruction.
  Sub-tables get `<ResourceTable variant="embedded">` (no card surface) — first exercised by
  `apigateway/usage-plans-page`'s nested plan-keys table (#1200 wave 2, 2026-08-23), and now the
  default choice for every `ResourceTable` composed inside a `ResourceListSection` tab body too.
- **2026-08-23 — the row model is now TanStack Table v9** (#1327 wave A). `@tanstack/react-table`
  had been a dependency since the initial commit and had never been imported; `ResourceTable` gave
  the pages a consistent *shape* but no sorting, no column visibility and no pagination, so the
  four ad-hoc sort states in the tree were the only sorting the app had. `useTable` now owns the
  order and membership of rows and columns. It renders nothing: `Table`, `TableRow`, `TableHead`,
  `TableCell`/`TableCellProse`, `QueryListState`, `EmptyState`, `ResourceListCard` and
  `ConfirmDialog` are unchanged, and the loading / empty / filtered-empty / error branch still runs
  before the table is built. Three of v9's sixteen stock features are registered —
  `rowSortingFeature`, `columnVisibilityFeature`, `rowPaginationFeature` — because
  `tableFeatures()` is the tree-shaking boundary; `stockFeatures` (all sixteen) and the deprecated
  `useLegacyTable` are deliberately unused. Measured: the shared `resource-table` chunk went
  1.1 kB → 13.6 kB gzip, against 27.9 kB for the same table built on `stockFeatures`.
  - **API added, all optional** — `sortValue` on a column makes it sortable and supplies the value
    to order by (a bare `sortable: true` cannot work: `cell` returns a `ReactNode`, which has no
    ordering); `sortFn`, `id`, `hideable`, `defaultHidden` per column; `sort`/`onSortChange`/
    `defaultSort`, `pageSize`, `columnToggle`, `virtualize` on the table. The one signature change
    is `ResourceTable<T>` → `ResourceTable<T extends RowData>` (v9's `Record<string, any> |
    Array<any>`), which every caller already satisfied.
  - **Row actions stay outside the column model** — chrome, not data: never sorted, never hidden,
    and keeping them out is what makes the columns menu correct without an exclusion list.
  - **State ownership** — sorting is the page's when it wants it, via `hooks/use-sort-search-param.ts`
    (`?sort=name` ascending, `?sort=-name` descending — JSON:API's leading-dash convention, so the
    common case is just the column's name), the twin of `useFilterSearchParam`'s `q`, undebounced because
    a header click is a decision, not a keystroke; wired on the four single-list pages that already
    validate `q` (kms, ssm, appsync, stepfunctions). The tabbed pages (iam, eventbridge, ec2) keep
    sorting in local state — one `sort` token cannot name which of several tables under one route
    it applies to. Column visibility is local state (a viewing preference, not part of what a
    shared link means) and reuses `CheckboxFilterDropdown` rather than a second dropdown.
  - **Virtualization is composition, not a feature** — v9 ships none, and its guide is explicit that
    virtualization is a rendering strategy rather than a table feature. `virtualize` therefore
    windows `getRowModel().rows` with the `@tanstack/react-virtual` Archetype E's kernel already
    uses, using spacer rows so the real `<table>` keeps laying out the columns. That is what lets
    #1327's Wave D evaluate `log-search-results.tsx` without the two virtualizers competing.
  - **Still not built:** `rowTo`, `select?`, `filter?`. Selection is now one feature import
    (`rowSelectionFeature`) away rather than a rewrite, but nothing needs it yet; filtering stays
    at the page level, where `q` already lives.

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
  prop; `ArnText` should use `<CopyButton>` too. **Done for P1 (2026-08-23):** `Definition`'s
  `copyable` renders `<CopyButton tone="inline">` and derives its noun from the label.

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

> **URL-binding half landed 2026-08-23 (#1203).** `hooks/use-filter-search-param.ts`
> (`useFilterSearchParam`) two-way binds a filter box to the route's `q` search param — debounced
> commit, `replace: true`, same contract as Request Traces' filters — wired into every page that
> already called `ResourceListFilter`: the five single-list pages (kms, ssm, appsync,
> stepfunctions, cognito) and the two tabbed pages from #1200 wave 2 (`iam-page`'s four resource
> tabs, `eventbridge-page`'s two), which additionally gained a `tab` search param so the selected
> tab itself deep-links — switching tabs clears `q` in the same navigation, matching `TabPanel`'s
> existing behaviour of unmounting (and thereby resetting) every tab but the selected one.
> `ResourceTable`/`QueryListState` also gained `isFiltered`/`onClearFilter`, so the empty state
> reads "no matching {noun}" + a clear-filter action instead of each page computing
> `filter ? … : …` by hand. **Not yet wired:** `apigateway/usage-plans-page` (its `apiId`/`planId`
> search params predate this and are a one-way "open on this plan" deep link, not a live two-way
> filter binding) and `ec2-dashboard` (no filter box today). The client-side matching consolidation
> below (`useResourceFilter` itself, and the ~14 pages that still hand-roll a search input outside
> `ResourceListFilter` entirely) is still open — the counts below predate this change and have not
> been re-audited.

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
  ├── A: DONE (#1101) — components/ui/definition-card.tsx already was P1; it gained
  │      `copyable`, and the last five hand-rolled attribute grids were converted
  ├── B: DONE (#1200) — components/ui/resource-table.tsx (P3), plus a `description`
  │      passthrough on `ResourceListPage`
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
  ├── P: convert the 9 unconverted index pages to Archetype A (§1 Archetype C) —
  │      **DONE** (#1200): 8/9 in wave 1 (2026-08-22), the remaining `apigateway/usage-plans-page`
  │      + the three tabbed pages (`eventbridge`, `iam`, `ec2-dashboard`, via the new
  │      `ResourceListSection`) in wave 2 (2026-08-23). `sts-page` stays bespoke as planned.
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

   **Decided 2026-08-23:** already in code — `features/ec2/components/instance-state-badge.tsx`:
   `running → success`, `pending` / `stopping` / `shutting-down` → `warning`,
   `stopped → default` (neutral), `terminated → danger`. Colour answers "does this need me?", not
   lifecycle position: a stopped instance is a state the developer chose, reversible, costs nothing —
   neutral. Terminated is irreversible and final, the one row a developer scanning a list must not
   miss — danger. Transitional states are bounded waits — warning. P6's shared status→tone table
   generalises this rule (reversible rest = neutral; irreversible end = danger; bounded transition =
   warning; serving = success) rather than re-deciding per service.

2. **`RawStateLink` on 4 of 24 list pages.** Either every list page gets it or none does. **Product
   call.** Recommendation: make it a prop of `ResourceListPage` defaulted **on**, since the Raw State
   Debugger is a first-class emulator feature and its absence on 20 pages looks like an oversight
   rather than a decision. Tradeoff: five extra pixels of chrome on every page.

   **Decided 2026-08-23:** on every list *and* detail page, built into the scaffolds, debug-gated.
   `ResourceListPage` and `ResourceDetailPage` both render it by default; opting out is an explicit
   `rawState={false}` (no known case). It already returns `null` unless the Raw State Debugger is
   enabled, so it costs ordinary users zero chrome. The link is the escape hatch from "what the
   console shows" to "what the emulator holds" — a developer wants it exactly where the resource they
   are staring at is, and a debug affordance is only trustworthy when it is in the same place, with
   the same icon, on every page. Today it is on 4 of 24 list pages and 4 of ~25 detail pages, which
   reads as forgetfulness, not choice.

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

   **Decided 2026-08-23:** mono by default, `variant="prose"` escape hatch — identical to
   `TableCell`/`TableCellProse`. Already `DefinitionCard`'s contract
   (`components/ui/definition-card.tsx`); P1's `DetailField` adopts the same spec (or `DefinitionCard`
   *is* P1 — the implementer decides, one primitive survives). **Settled 2026-08-23 by P1:
   `DefinitionCard` *is* P1**; there is no `DetailField`. A detail page's values are machine
   output — ARNs, ids, timestamps, counts, sizes — which mono aligns and makes diffable, and the same
   value must read the same in the list cell and on the detail page. Prose (a description someone
   typed) is the marked exception; the ~30 description fields that flip take `variant="prose"` in the
   adoption sweep.

5. **`SectionLabel` (10px/.16em uppercase) vs the de-facto `font-mono text-sm font-medium` heading
   used 56 times.** Two specs for "names a group of things", and the declared one has zero users.
   **Product call.** Recommendation: ship P11 as `SectionHeading` matching today's rendering, then
   let design decide in one flip rather than letting the drift continue.

   **Decided 2026-08-23:** `SectionLabel` is retired; `SectionHeading` = the de-facto
   `font-mono text-sm font-medium`, colour `text-fg`. The typographic argument decides it, not the
   usage count: the 10px-uppercase spec is the *field-label / column-header* spec. A section heading
   rendered in that spec sits at the same visual weight as the field labels directly beneath it,
   flattening the hierarchy — the reader cannot tell which label owns which. The heading has to sit
   one step above the field label; 14px/medium does, and it is also what 65 call sites already
   render. Hierarchy after this: page title → `SectionHeading` (14px mono medium, fg) → field
   label / column header (10px uppercase tracked, fg-subtle) → value. P11 ships `SectionHeading`; the
   9 `SectionLabel` uses (secretsmanager ×2, stepfunctions ×7) migrate; `SectionLabel` is deleted from
   `primitives.tsx`. The 10px uppercase token stays reserved for labels and headers.

6. **Copy-confirmation copy.** Three strings today: "Copied!", "Copied to clipboard", "API key value
   copied". **Recommendation: `Copied <noun>` with the noun supplied by the caller**, since
   `useCopyToClipboard` will know it anyway.

   **Decided 2026-08-23:** already in code — `Copied <noun>`, bare `Copied` when no noun
   (`hooks/use-clipboard.ts:59`): success-toned toast, sentence case, no exclamation mark (the
   console's voice is calm). The noun matters because a detail page has several copyables — ARN, id,
   URL — and "Copied!" does not tell the developer *which* one they just got. Remaining "Copied to
   clipboard"/"Copied!" stragglers fall to the P7 adoption sweep.

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
  mutation. It should adopt `Definition` and stop there. **Done** — it has since.

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
| ~~`Separator`~~ | ~~`ui/primitives.tsx:203`~~ | deleted (#1201) — 0 users, confirmed by grep across `src/` |
| `FieldLabel` | `ui/primitives.tsx:14` | 0 — still. **P1 (#1101) resolved this differently:** `Definition`'s label must be a `<dt>`, and `FieldLabel` is a `<span>`, so the *token* `fieldLabel` in `lib/typography.ts` is what both consume and what makes the spec inheritable. The wrapper component itself remains unused; deleting it is a separate call. |
| `SectionLabel` | `ui/primitives.tsx:23` | 0 — see decision 5 |
| `TableEmpty` | `ui/table.tsx:96` | **1 (#1201 re-check)** — `rds/components/instance-detail.tsx`. No longer dead; kept. |
| `CardHeader`, `CardTitle`, `CardDescription`, `CardFooter` | `ui/card.tsx` | **3 files (#1201 re-check)** — `ui/definition-card.tsx`, `autoscaling/group-list.tsx`, `settings/settings-page.tsx`. No longer dead; kept. |
| `ComboboxCompact` | `ui/combobox.tsx` | 1 (`region-select.tsx`) — arguably fine |
| ~~`useEventSource` + `usePageUnloading`~~ | ~~`hooks/`~~ | deleted — 139 dead lines removed |
| ~~`debugClipboard`~~ | ~~`features/debug/clipboard.ts`~~ | deleted — folded into P7 |
| `ResourceListFilter` | `ui/resource-list-page.tsx:117` | **8 files (#1201 re-check)** — the #1200 waves adopted it (appsync, cognito, eventbridge, iam, kms, ssm, stepfunctions pages + its own module). No longer "1, while 14 hand-roll it"; left as-is, nothing to remove. |
| `RowAction` `tone` prop | `ui/resource-list-page.tsx:159` | **Verified (#1201):** `tone="danger"` is used explicitly at 20+ call sites; `tone="neutral"` as an explicit literal has zero matches anywhere in `src/` — every neutral-toned `RowAction` gets there by omitting the prop and taking the `defaultVariants: { tone: "neutral" }` fallback. Nothing to remove: the neutral variant is live, just never spelled out. |

---

## 7. Guardrails

Both existing mechanisms can be extended, and both should be — the audit's clearest lesson is that
extraction without enforcement decays within a release.

### The eslint plugin (`web/eslint-plugin-classnames/`) — landed 2026-08-23 (#1201)

Twelve rules now, all `warn`, all about hygiene the new scaffolds should own. It is an ESLint
flat-config plugin with a `rules/` directory — adding rules is a file plus one line in `index.js`.
(It keeps that shape, but nothing runs it under ESLint any more: oxlint's JS-plugin host loads it
since #1368, and #1330 step 3 removed ESLint from the repo entirely.)
All six proposed additions landed, each proven against the real tree (`pnpm lint`, 2026-08-23) rather
than a synthetic fixture — none of the six has a dedicated `RuleTester` unit test, matching the
original six, which don't have one either:

- **`no-local-detail-row`** — flags a local `function DetailRow|InfoRow|MetaRow` (function
  declaration or `const` arrow) in `src/features/**`. **1 real hit**: `cognito-pool-detail.tsx`'s
  `DetailRow`. **Zero after P1 (#1101)** — Cognito's was `Definition` plus a hand-rolled copy
  button, which `copyable` made redundant, so it is deleted and the matching ratchet is a hard
  `expect(0)`. **P1 also found the rule's blind spot:** it matches three component *names*, so
  `waf/web-acl-detail`'s `Info` — the same pattern, a 14th local definition — never tripped it, and
  neither does an inline `<dl>`. P1 covers the shape instead with a new `global.test.ts` ratchet on
  hand-written `<dt>` elements (see below). Widening the rule's name list, or flipping it to
  `error` now that it has no hits, is a cheap follow-up.
- **`prefer-button-busy`** — flags a JSX `<Button>` whose `disabled` expression mentions `isPending`
  or whose children contain `<Spinner>`. **132 real hits.**
- **`no-raw-spinner-in-content`** — flags `<Spinner>` not inside a `<Button>`, `<Badge>`, or a
  `toast(...)` call. **114 real hits** (mostly the centred full-page loading spinner pattern —
  `<div className="flex items-center justify-center py-32"><Spinner className="h-6 w-6" /></div>` —
  across route files and detail pages).
- **`prefer-shared-formatter`** — flags `toLocaleString`/`toLocaleDateString`/`toLocaleTimeString`
  and a local `function`/`const format(Bytes|Date|Duration)` outside `src/lib/format.ts`. **77 real
  hits.**
- **`prefer-use-resource-mutation`** — flags `useMutation(` in `src/features/**`. **78 real hits.**
  The "allowlist" the original brief asked for is a plain
  `// eslint-disable-next-line classnames/prefer-use-resource-mutation -- <reason>` — no second
  config surface needed, since a disable comment already requires and preserves a reason.
- **`no-duplicate-class-cluster`** — cross-file by nature (a single file's AST can't answer "is this
  common"), so it reads the whole `src/` tree itself with `node:fs` on first use and caches the
  result for the rest of the lint process, rather than relying on the linter's own per-file,
  order-dependent traversal. Default thresholds (4-token sliding window, present in >15 files) were
  tuned against the current distribution — lower thresholds surfaced thousands of hits on generic
  3-token combinations like `flex items-center gap-2`. **155 real hits**, topped by
  `rounded-lg border border-border bg-bg-elevated` (18 files) and `flex w-full flex-col gap-4`
  (32 files, the widest cluster found). The `font-mono text-sm font-medium text-fg` cluster the
  original audit named is still present and has *grown* from 56 to 65 raw occurrences since
  2026-07-27 — direct evidence for why this rule needed to exist.

566 total warnings across all twelve rules today (0 errors) — expected: these encode a backlog, not
a regression, and existing call sites are `warn` intentionally while P1/P2/P4/P5/P8/P10 (which this
plan still lists as open) migrate them one wave at a time. Flip the whole plugin to `error` once that
backlog lands — a mixed severity is what let 392 raw palette classes accumulate in the first place.

### The style test (`web/src/styles/global.test.ts`) — ratchets landed 2026-08-23 (#1201)

Already walked every `.ts`/`.tsx` file and failed the build on colour utilities whose `--color-*`
root is not declared in `global.css`'s `@theme` block; now also carries a
`describe("DRY-refactor ratchets …")` block with the five ratchets this section asked for, baselined
2026-08-23 (the day #1200 waves 1-2 finished) rather than at the original 2026-07-27 audit — the
numbers moved in both directions as #1200 landed and other work continued in parallel:

- `<Spinner>` element count ≤ **209** (was 205 at the original audit).
- `disabled={…isPending…}` count ≤ **131** (was 120).
- local `DetailRow`/`InfoRow`/`MetaRow` definitions in `features/**` ≤ **1** (was 14 — 13 already
  resolved by other work between the audit and #1201).
- raw `useMutation(` in `features/**` ≤ **80** (was 72).
- `<Loader2` outside `ui/primitives.tsx` ≤ **9**, **not** the hard `expect(0)` this section
  originally proposed: 9 real sites remain (`connection-dialog.tsx`, `global-search.tsx`,
  `combobox.tsx` ×2, `cloudformation/stack-detail.tsx` ×2, `cloudformation/stack-list.tsx`,
  `routes/debug/traces/$requestId.tsx` ×2) and a literal 0 would fail on landing. A hard zero was
  always contingent on P4 (the `Button.busy` rollout) having already absorbed every raw `Loader2`
  site, and P4 hasn't landed — this ceiling holds today's count and should drop to a true
  `expect(0)` once P4 does.

**2026-08-23 (#1101, P1):** a sixth ratchet — hand-written `<dt>` elements in `src/features/**`
≤ **1**. It is the shape-level counterpart to `no-local-detail-row`'s name-level check, and the one
permitted hit is `cloudformation/stack-diagnostics`' `FactsBody`, named in the test's comment.
Test files are excluded: a page test asserting the shared component's markup legitimately names the
element it expects.

A ratchet test is strictly better than a lint rule for the migration period: it permits the existing
sites, forbids new ones, and its failure message names the file+line that grew the count.

### Convention

Add to `web/AGENTS.md`, under "Component conventions":

> Before writing a page, check `components/ui/` for a scaffold. A list page is
> `ResourceListPage` + `ResourceTable`; a detail page is `ResourceDetailPage` + `DefinitionCard`; a
> create flow is `ResourceFormDialog`. If a scaffold nearly fits, extend the scaffold — do not fork
> it into `features/`. A component defined inside a `features/` file must be specific to that
> service; anything a second service would want belongs in `components/ui/`.

And, given what this audit found, a second line:

> If you find yourself writing `useMutation`, `navigator.clipboard`, `toLocaleString`, a status→variant
> ternary, or a `flex flex-col gap-0.5` label/value pair — stop. All five already exist.
