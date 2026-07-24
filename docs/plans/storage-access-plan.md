# Storage access patterns — standardization plan

> **Status:** in progress — A1/A2 (Kinesis), A3 (DynamoDB), A5 (CloudWatch metrics) done; A4 (Logs) and A6 (S3) in flight; A7 design-gated. Audit completed 2026-07-24 (agent sweep of every service's storage reads/writes; evidence file:line below).
> **Scope:** how services *consume* `internal/state` and the dedicated SQL tables — read shapes, write shapes, and the shared helpers they should go through. The storage *layer* itself is done ([storage-plan.md](./storage-plan.md), Phases 1–3); this plan governs usage on top of it.
> **Relationship to other plans:** [storage-plan.md](./storage-plan.md) items 3.3/3.10/3.12 stay there (benchmark-gated layer work; 3.10's SQS table is cross-referenced by A-items here, not duplicated). Wire-level pagination fidelity (ignored `Limit`/`NextToken` params, truncation-flag correctness) is tracked in the pagination plan — items here note when they *enable* a pagination fix but the wire contract itself is that plan's acceptance criterion.
> **Audience:** any contributor or agent. [CONTRIBUTING.md](../../CONTRIBUTING.md) and [AGENTS.md](../../AGENTS.md) rules apply throughout (failing test first, `clock.Clock`, scoped verification, benchmark discipline per [storage-test-plan.md](./storage-test-plan.md)).

---

## The fidelity principle

Everything here is behind `state.Store` or a per-service backend interface — invisible to the AWS wire. The one rule that keeps it that way:

> Push **structural** predicates into storage: time ranges, key ranges, limits, cursors — semantics that are exact and testable at the storage boundary.
> Keep **behavioral** semantics in handlers: AWS filter-pattern matching, metric math, ordering contracts, response shaping — the code that already implements and tests AWS behavior. Never re-implement these in SQL (`LIKE` is not a CloudWatch filter pattern).

Wire-format tests and the compat suite are the parity net for every item; a storage-access change that requires touching a wire-format test is mis-scoped.

## The boundedness rule (anti-shoehorn, part 1)

Scan-everything-and-filter-in-Go is the **correct** pattern for bounded namespaces and must not be "fixed":

- **Bounded by resource count** (queues, tables, functions, alarms, rules, repositories, task definitions — dozens to low thousands, created by humans/IaC): keep the simple shape. Adding cursors here is complexity without benefit.
- **Unbounded / data-plane** (events, messages, datapoints, records, objects, items — created by workload traffic): these are the only legitimate targets for the patterns below.

Every item in this plan states its classification. The keep-as-is register at the bottom exists so "not converted" is always a documented decision, never an oversight.

## The rule of two (anti-shoehorn, part 2)

A helper graduates to `serviceutil` when its **second** consumer appears, not before. A proposed generic that requires consumers to contort their data to fit it is evidence the abstraction is wrong, not the consumers. Corollary: every pattern below has exactly **one** reusable implementation home; an item is not done if it privately reimplements machinery a shared helper provides.

---

## The patterns

| # | Pattern | One-line rule | Reusable home | In-repo precedent |
|---|---------|---------------|---------------|-------------------|
| P1 | **Key-as-index** | Encode the query dimension (time, sequence) as a fixed-width sortable key suffix; read with prefix + key range (`ScanPage`/`startAfter`) instead of scan-and-decode-everything | `serviceutil` sortable-suffix helper (see M2) | CFN events `uniqueSuffix` (1.9); metricdata `…/<UnixNano>` keys |
| P2 | **Structural pushdown** | Dedicated-table reads take range + limit parameters in SQL; never fetch full history when the API has a window | Per-backend by nature; the convention is a `…Range(ctx, …, startTs, endTs, limit)` method on the narrow backend interface | DynamoDB Streams `GetRecords`: `WHERE table_name=? AND id>? ORDER BY id ASC LIMIT ?` ([stream_store.go:177-183](../../internal/services/dynamodb/stream_store.go)) — **the model implementation** |
| P3 | **Cursor-in-token** | AWS continuation tokens are opaque by contract — encode a storage cursor in them; never materialize-then-slice unbounded data | `serviceutil` token codec (see M1) | `ScanPage`'s `startAfter`/`nextKey` contract |
| P4 | **Overlay/buffer merge** | Any pushdown must preserve read-your-writes through write buffers and pending overlays | Per-buffer today (rule of two: extract `MergeSorted[T]` only when a second consumer appears) | `hybridScanPageMerged` ([hybrid.go](../../internal/state/hybrid.go)); logs `mergeEventsSorted` |
| P5 | **Scan, not List+Get** | One round trip for list reads (done branch-wide in 3.1 — standing rule for new code) | n/a | storage-plan 3.1 |
| P6 | **Narrow backend interfaces** | Services own small consumer-side interfaces (`eventBackend`, `itemBackend`); never widen `state.Store` for one service's need | n/a (SOLID convention) | logs/dynamodb backends |

## Shared machinery items

### M1 — `serviceutil` pagination-token codec
One codec for opaque continuation tokens: encode/decode a storage cursor (string key or small struct), tolerant decode that maps garbage tokens to the caller-supplied AWS error. Written once, used by every item below that emits tokens and by the pagination plan's fidelity fixes. **Do first** — it is the DRY chokepoint; five handlers inventing token encoding is the failure mode this plan exists to prevent.

### M2 — promote the sortable-suffix helper to `serviceutil`
`uniqueSuffix` exists twice by deliberate copy (Lambda [store.go:142-149](../../internal/services/lambda/store.go), CFN [store.go:115-118](../../internal/services/cloudformation/store.go)); Kinesis's A1 fix is a third consumer. Rule of two is satisfied; promote once, with the fixed-width/zero-padding requirement documented (lexicographic order must equal numeric order — 19-digit nanos hold until 2286).

---

## Items

### A1 — Kinesis: stored per-shard sequence counter  **[✅ done — correctness + hot write path]**

**Evidence.** Every `PutRecord` calls `listRecords` — a full-shard `Scan` + JSON decode + sort — solely so `nextSeqNo` can use `len(existing)` as the next counter ([typed_logic.go:254](../../internal/services/kinesis/typed_logic.go), [store.go:243-246](../../internal/services/kinesis/store.go)); `PutRecords` repeats this **per record in the batch** ([typed_logic.go:284](../../internal/services/kinesis/typed_logic.go)). O(n) per write, O(n²) to fill a shard.

**Worse than perf:** `len(existing)` regresses when records are ever removed (retention, stream recreation with residue), so a fresh sequence number can collide with a surviving key and **silently overwrite a record**. Note the seq format itself is fine — `"49%019d%010d"` is fixed-width and sorts correctly; only the counter derivation is broken.

**Change.** Persist a monotonic per-shard counter (in the shard/stream record, or a dedicated counter key) incremented under the service's existing write path; never derive from record count. `PutRecords` allocates a contiguous block in one read-modify-write.

**Tests.** Failing first: put → delete a record (or simulate trim) → put again → no key collision, strictly increasing seqs. Benchmark: `PutRecord` ns/op and allocs/op flat vs. records-in-shard (0 / 1k / 10k preloaded), per [storage-test-plan.md](./storage-test-plan.md) discipline (allocs/op is the signal).

**Patterns:** P1 (keys already comply), M2 consumer. Unbounded/data-plane. **Accept when** the benchmark is flat and the collision test passes.

### A2 — Kinesis: `GetRecords` range read instead of full-shard scan  **[✅ done]**

**Evidence.** `getRecordsTyped` honors `Limit` and the shard-iterator cursor only by slicing the result of a full-shard `listRecords` ([typed_logic.go:332-339](../../internal/services/kinesis/typed_logic.go), [store.go:164-184](../../internal/services/kinesis/store.go)).

**Change.** Keys are `<region>/<stream>/<shard>/<seq>` with sortable seqs (per A1): read with `ScanPage(nsRecords, shardPrefix, startAfter=iteratorSeq, limit)` — copy the DynamoDB Streams `GetRecords` shape. Keep the defensive sort until A1 has been released long enough that pre-A1 non-contiguous data is implausible, then drop it.

**Tests.** Iterator-resume correctness (no skips/duplicates across pages — mirror the ScanPage no-dup/no-gap suites); benchmark GetRecords vs shard depth, flat. **Depends on** A1. Patterns: P1, P3 (iterator is already the cursor). Unbounded. **Done (2026-07-24, branch fix/kinesis-seq-and-range-reads):** persisted per-shard counter (failing-first: two puts after a delete produced identical seq numbers pre-fix); PutRecord flat at 31 allocs/op across 0/1k/10k depth; GetRecords flat 1k→10k. Bonus: the JSON1.1 handlers — the default SDK wire path — duplicated the buggy logic and now delegate to the typed implementations (~140 duplicate lines removed). SplitShard/MergeShards share the duplication pattern (flagged, out of scope).

### A3 — DynamoDB: `Scan` pages at the item store instead of full-table reads  **[✅ done — with pagination G2/G5]**

**Evidence.** The `Scan` handler fetches the **entire table** every call via `scanItems` → `itemBackend.scanAll` ([handler.go:578](../../internal/services/dynamodb/handler.go), [store.go:345-346](../../internal/services/dynamodb/store.go)), then applies `ExclusiveStartKey`/`Limit` by in-memory slice ([handler.go:635,651](../../internal/services/dynamodb/handler.go)). A `Limit: 1` Scan costs the same as reading the whole table. DynamoDB `Query`/`Scan` is the most-exercised pagination contract in AWS client code.

**Change.** Add `scanPage(ctx, table, exclusiveStartKey, limit)` to `itemBackend` (both implementations — memory keyset slice, SQL keyset `WHERE (hash_key, sort_key) > (?, ?) ORDER BY … LIMIT ?` on the existing PK). The handler's `LastEvaluatedKey` maps 1:1 onto the keyset cursor — no token codec needed (DynamoDB's cursor is structured, not opaque). Filter expressions stay in the handler (behavioral); note AWS semantics: `Limit` counts *evaluated* items pre-filter, which keyset paging reproduces naturally.

**Tests.** Failing first: parity suite run against both backends — full pagination walk equals `scanAll` result, no dups/gaps; `Limit`-counts-evaluated-not-matched semantics pinned. Benchmark: `Scan Limit=25` flat vs table size. Patterns: P2, P6. Unbounded. **This is the highest-leverage single item for typical users.**

**Done (2026-07-24, branch fix/dynamodb-scan-paging-cursors):** `itemBackend.scanPage` on both backends — SQL is a row-value keyset (`(hash_key, sort_key) > (?, ?)` LIMIT-bounded) on the existing PK, no new index; the memory backend was rewritten from nested maps to one ordered tree per table (`tidwall/btree`, same library as `state.MemoryStore`), which also turned `queryByHash` into a bounded seek. Scope: the plain-Scan fast path only — GSI scan and parallel segments stay on `scanAll` until A7's ordered secondary-index structure exists (they still get G2's positional cursor). Backend parity pinned by `TestItemBackend_ScanPage_ParityWithScanAll` across page sizes 1/2/3/100 on both backends. Memory-backend benchmark: allocs/op flat at 6 for 2k and 8k item tables. SQL-backend flatness is structural (same LIMIT-bounded PK range-read shape as Kinesis `stream_store.go` GetRecords, this codebase's model implementation); clean numbers weren't obtainable this session under concurrent-agent Docker load — capture at the next quiet-machine benchmark pass.

### A4 — CloudWatch Logs: range + limit pushdown for `GetLogEvents`/`FilterLogEvents`

**Evidence.** Both fetch each stream's **full history** via `getEvents` and window in memory ([typed_logic.go:345-378, 380-449](../../internal/services/cloudwatch/logs/typed_logic.go)); `req.Limit`/`req.NextToken` are never referenced; `idx_logs_events_group (region, group_name, ts)` was built for exactly this and is unused ([migrations.go:75](../../internal/services/cloudwatch/logs/migrations.go)).

**Change.** Extend `eventBackend` (P6) with `getEventsRange(ctx, region, group, stream, startTs, endTs, limit, direction)` and `getGroupEventsRange(ctx, region, group, streamPrefix, startTs, endTs, limit)` — FilterLogEvents becomes **one** indexed query for the whole group instead of N per-stream full reads. Merge each stream's write buffer into the result (P4 — time-window the buffer too). `CompileFilter` matching, interleaving, and `searchedLogStreams` shaping stay in the handler (behavioral). Memory backend gets the same methods (slice window).

**Wire fidelity** (Limit/NextToken/forward-backward token semantics) is specified and accepted in the **pagination plan** — this item is its storage prerequisite; land them together. Supersedes background task chip `task_9f61ae0c`.

**Tests.** Range-correctness vs the current full-scan behavior (property: same events for same window); buffer-visibility (event ingested < debounce ago appears in a filter over its window); benchmark FilterLogEvents vs group size, flat at fixed window. Patterns: P2, P4, P6, M1 consumer. Unbounded.

### A5 — CloudWatch metrics: key-range time windows for datapoint reads  **[✅ done]**

**Evidence.** Keys are `<ns>/<metric>/<dims>/<UnixNano>` — sortable ([service.go:209](../../internal/services/cloudwatch/service.go)) — yet `GetMetricStatistics` ([service.go:1094](../../internal/services/cloudwatch/service.go)), `GetMetricData` (1207), the alarm evaluator (1625, background, every tick), and `listMetricDataPoints` (232-253) all `Scan` the full prefix and decode every retained point to filter by time.

**Change.** Read `[prefix+startNano, prefix+endNano]` via `ScanPage`'s `startAfter` (no interface change) or a bounded key-range `Scan`; decode only in-window points. The retention sweep can also parse the key suffix and skip decoding values entirely. No wire change at all — pure P1 harvest of a key design that already exists.

**Tests.** Window-equality property vs current behavior; benchmark GetMetricStatistics + one alarm-eval tick vs points-in-retention, flat at fixed window. Patterns: P1. Unbounded. **Done (2026-07-24, branch fix/cloudwatch-metric-key-ranges):** ScanPage range walk with exclusive-predecessor start cursor; oracle property test proves window equality with the old full-scan behavior; boundary semantics pinned inclusive-both-ends; allocs/op flat at 226 regardless of out-of-window history (0/2k/8k preloaded); sweep now key-suffix-driven, zero value decodes.

### A6 — S3: `ListObjects` pages internally instead of materializing the bucket

**Evidence.** Handlers honor `MaxKeys`/`ContinuationToken`/`StartAfter`/`Delimiter` correctly — but against an in-memory copy of the **whole bucket** from `listObjects`' full `Scan` ([handler_bucket.go:421-551, Scan at 448](../../internal/services/s3/handler_bucket.go); [store.go:323-341](../../internal/services/s3/store.go)).

**Change — not a 1:1 ScanPage swap (audit's explicit shoehorn warning).** With a delimiter, many keys collapse into one CommonPrefix before a page fills, so the handler must **loop internal `ScanPage` fetches until it has `MaxKeys` *effective* entries** (keys + common prefixes), carrying the cursor across internal pages. Delimiter/common-prefix collapse logic stays in the handler unchanged; only the source of keys becomes incremental. Bounds memory by page size instead of bucket size; V1 (`Marker`) gets the same treatment.

**Tests.** Existing ListObjectsV2 wire tests unchanged (the fidelity net); new: pagination walk over a delimiter-heavy bucket equals current full-scan output exactly; benchmark `MaxKeys=100` flat vs bucket size. Patterns: P3 (token already exists — S3's ContinuationToken can wrap the storage cursor via M1), P2. Unbounded.

### A7 — DynamoDB: GSI `Query` secondary index  **[design-first, gated]**

**Evidence.** Query on a GSI falls back to full-table scan + in-memory hash filter ([handler.go:817-825](../../internal/services/dynamodb/handler.go)) — the only Query path not partition-scoped (base-table Query is already fine, [handler.go:848-865](../../internal/services/dynamodb/handler.go)).

**Gate.** Fixing it properly means a real secondary-index structure in the item store (rows or an index table maintained on write), not a pagination change — a design of its own, with write-amplification and backfill questions. Write the short design (index shape, maintenance on Put/Update/Delete, backfill migration via the runner) **before** any code; treat like storage-plan's benchmark-gated items: only proceed if a workload actually exercises GSI queries at depth (the emulator's typical GSI tables are small).

---

## Keep-as-is register (documented decisions, not oversights)

All bounded-by-resource-count; the simple scan-and-filter shape is correct here. Wire-level pagination gaps in these (ignored `MaxResults` etc.) belong to the pagination plan, which may still choose in-memory slice pagination for them — that is the right tool for bounded data.

- **DynamoDB base-table `Query`** — already partition-scoped, fine ([handler.go:848](../../internal/services/dynamodb/handler.go)).
- **ECS `ListTasks`** family/status filtering — dimensions aren't key-encodable (many-to-many via task-def ARN); typical cluster sizes make this moot ([handler_tasks.go:456-489](../../internal/services/ecs/handler_tasks.go)).
- **Logs `DescribeLogGroups`/`DescribeLogStreams`**, **CloudWatch `DescribeAlarms`**, **Lambda `ListFunctions`**, **ECR listings**, **SNS/SES/EventBridge listings**, **Route53 `ListResourceRecordSets`**, **S3 `ListMultipartUploads`** — bounded metadata.
- **S3 `ListParts`** — borderline (10k parts max in real AWS); revisit only if the pagination plan's fidelity work lands a limit there anyway.
- **SQS receive** — tracked as [storage-plan.md](./storage-plan.md) 3.10, benchmark-gated there; not duplicated here.

## Suggested ordering

1. **M1 + M2** (shared machinery — small, unblocks everything).
2. **A1** (correctness hazard on a hot write path) then **A2** (its read-side twin).
3. **A3** (highest user leverage) and **A5** (cheapest win) — independent, parallelizable.
4. **A4** together with the pagination plan's logs items.
5. **A6**.
6. **A7** only behind its design gate.

Each item: failing test first; before/after benchmark recorded per [storage-test-plan.md](./storage-test-plan.md) (container-native FS, exclusive machine, allocs/op as the deterministic signal); wire-format tests untouched; CHANGELOG per its rules (perf entries need measurement conditions).
