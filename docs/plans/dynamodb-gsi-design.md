# DynamoDB GSI ordered secondary-index structure — design (storage-access-plan.md A7)

> **Status:** design-first, gated per [storage-access-plan.md A7](./storage-access-plan.md#a7--dynamodb-gsi-query-secondary-index) — no code in this PR. Reviewer verdict requested at the bottom.
> **Author's framing:** this document proposes a real ordered index structure to replace the current GSI/parallel-scan full-table-read fallback, mirroring the base-table design [A3](./storage-access-plan.md#a3--dynamodb-scan-pages-at-the-item-store-instead-of-full-table-reads--done--with-pagination-g2g5) already shipped, plus the pagination fixes [G2](./pagination-plan.md#g2--dynamodb-queryscan-cursor-resolution-by-position-not-item-identity--done--with-a3-and-g5) already gave the base table.

---

## 1. Problem statement + current cost

### What A3 already fixed (base table only)

[storage-access-plan.md A3](./storage-access-plan.md#a3--dynamodb-scan-pages-at-the-item-store-instead-of-full-table-reads--done--with-pagination-g2g5) gave the **plain Scan** and **base-table Query** paths real storage-level pushdown:

- `itemBackend.scanPage` — an O(log n + limit) keyset page on both backends: `memItemBackend`'s per-table `btree.Map[string, Item]` ordered by `itemCompositeKey(hashKey, sortKey)` ([item_store.go:65-240](../../internal/services/dynamodb/item_store.go)), and `sqlItemBackend`'s row-value range query on the existing `(table_name, hash_key, sort_key)` primary key ([item_store.go:479-508](../../internal/services/dynamodb/item_store.go)).
- `itemBackend.queryByHash` — an O(k) partition read, used by base-table `Query` ([handler.go:927](../../internal/services/dynamodb/handler.go), `scanItemsByHashKey` at [store.go:405-411](../../internal/services/dynamodb/store.go)).
- [G2](./pagination-plan.md#g2--dynamodb-queryscan-cursor-resolution-by-position-not-item-identity--done--with-a3-and-g5)'s position-based cursor resolution (`resolveCursorPosition`, [handler.go:1641-1669](../../internal/services/dynamodb/handler.go)) so a deleted "last returned item" doesn't restart pagination from page 1.

**None of this applies once `IndexName` is set, or once a Scan requests more than one parallel segment.** There is no ordered storage structure for a secondary index today — GSI Query, GSI Scan, and parallel-scan segments all still read the entire table into memory on every call:

| Path | Evidence | What it does |
|---|---|---|
| GSI Scan | [handler.go:660-731](../../internal/services/dynamodb/handler.go), specifically the `else` branch at 660 and the comment at 661-666 | `h.store.scanItems(ctx, req.TableName)` — full `scanAll` — then filters by index hash-key presence (672-681), sorts the **entire filtered set** by `(hashKeyName, sortKeyName)` in Go (686-698), slices for parallel segments (701-718), then applies `ExclusiveStartKey` by position (721-722) and `Limit` (726-729). |
| Parallel scan (`TotalSegments > 1`), no index | Same branch, same evidence | Even a plain parallel Scan (no `IndexName`) falls into this branch (`req.TotalSegments <= 1` is required for the A3 fast path at line 644) — full scan + full sort, then a positional slice per segment. |
| GSI Query | [handler.go:892-912](../../internal/services/dynamodb/handler.go) | `allItems, aerr := h.store.scanItems(ctx, req.TableName)` (894) then a linear scan comparing every item's index hash-key attribute against the query value (898-911) — O(table size) per call regardless of how selective the index query is. |

Contrast with base-table Query, which is already correctly partition-scoped: [handler.go:925-941](../../internal/services/dynamodb/handler.go) calls `scanItemsByHashKey` (an O(k) partition read) instead of a table scan. **A GSI Query with an equally selective index hash key pays for a full table scan where the equivalent base-table Query pays for one partition read.** This is the gap A7 exists to close, per the audit note at [storage-access-plan.md:119](./storage-access-plan.md): "Query on a GSI falls back to full-table scan + in-memory hash filter — the only Query path not partition-scoped."

### Why this is a design item, not a pagination change (recap of the gate)

The A7 gate text is explicit: "Fixing it properly means a real secondary-index structure in the item store (rows or an index table maintained on write), not a pagination change — a design of its own, with write-amplification and backfill questions." A pushdown fix at the read boundary (A3/A5's pattern) has nothing to push down onto here — there is no ordered-by-index-key data to seek into. The data has to actually exist in index order somewhere, which means new storage and new write-path responsibilities. That's this document.

---

## 2. AWS semantics inventory

Sources: [Using Global Secondary Indexes in DynamoDB](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/GSI.html) (developer guide), [Query API reference](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html).

### Sparse indexes

> "A global secondary index only tracks data items where its key attributes actually exist." — if an item doesn't have the index's key attribute(s), DynamoDB "would not propagate this item to" the index. A `Query`/`Scan` on the index returns fewer items than the base table would for the same predicate, because items missing the index key are simply absent from the index — not merely filtered.

Overcast's current fallback already implements the *filtering* half of this correctly (GSI Scan excludes items lacking the index hash key: [handler.go:672-681](../../internal/services/dynamodb/handler.go)), but does it by re-deriving sparseness on every call from the full item set. In an index-structure world, sparseness is a **write-time decision** (skip the write when the key is absent — see §3), not a **read-time filter**.

### Projections (ALL / KEYS_ONLY / INCLUDE)

Existing `Projection` type ([store.go:68-71](../../internal/services/dynamodb/store.go)) already models `ProjectionType` and `NonKeyAttributes`. Per the AWS doc: `KEYS_ONLY` returns only the table's primary key plus the index key; `INCLUDE` adds the specified non-key attributes; `ALL` duplicates every base-table attribute into the index. AWS is explicit that **"global secondary index queries cannot fetch attributes from the parent table"** — a `Query`/`Scan` on a GSI can only return what's actually projected, never a superset fetched by falling back to the base item.

**Overcast's current code violates this today**: because there's no separate index storage, `scanTyped`/`queryTyped`'s GSI paths read full base-table items and apply `ProjectionExpression`/`Select` afterward ([handler.go:768-780](../../internal/services/dynamodb/handler.go), [1019-1032](../../internal/services/dynamodb/handler.go)) — a `KEYS_ONLY` or `INCLUDE`-projected GSI Query in Overcast can currently return attributes real AWS would refuse to return (because the base item always has them available in memory, and the projection is applied as a display-time filter rather than enforced by storage). This is a real fidelity gap the new design must close, not just an efficiency one: **projection enforcement belongs in the read path per the fidelity principle** ("behavioral" semantics stay in the handler), but it must operate on the *index's stored attribute set*, not the base item, once index storage exists — otherwise `ALL_PROJECTED_ATTRIBUTES` / `KEYS_ONLY` continues to silently return more than AWS would.

### GSI eventual consistency — fidelity position

AWS: "When you put or delete items in a table, the global secondary indexes on that table are updated in an eventually consistent fashion... your applications need to anticipate and handle situations where a query on a global secondary index returns results that are not up to date." The `Query` API additionally states **`ConsistentRead=true` against a GSI returns `ValidationException`** — strongly consistent reads are categorically unsupported on GSIs in real AWS.

**Position: not worth emulating staleness, keep immediate consistency — precedent already set.** [docs/services/dynamodb.md:28](../services/dynamodb.md) already documents this exact decision for the *current* fallback: "real DynamoDB GSIs are eventually consistent; the emulator is immediately consistent — items are visible in GSI queries the instant they are written." That line predates this design and should carry forward unchanged: Overcast is explicitly "not a performance testing tool... no latency emulation" ([AGENTS.md § Non-goals](../../AGENTS.md#non-goals--decision-guide-for-agents)), and a single-process, non-replicated store has no natural staleness window to model — introducing an artificial delay would be manufacturing a divergence, not removing one, and no client-observable behavior depends on it (code correctly written against real AWS already retries/tolerates staleness; code that isn't is already the kind of divergence-sensitive user AWS itself warns about). **Overcast currently does not even parse `ConsistentRead`** (confirmed: no reference to the field anywhere in `internal/services/dynamodb`) — every read is, in effect, always "strongly consistent" including on GSIs, which is the opposite direction of the real gap (AWS forbids `ConsistentRead=true` on a GSI query with a `ValidationException`). **Open item for this design to decide (see §7):** whether to add `ConsistentRead` parsing solely to reject `true` on GSI queries with `ValidationException` — cheap, wire-visible, and closer to AWS than silently ignoring the parameter, but it's a request-validation fix orthogonal to the index structure itself and could land as its own small item either before or independent of A7.

### `LastEvaluatedKey` shape for index queries

AWS's Query guide (and the existing in-repo comment, which already got this right) requires index-query pagination cursors to carry **both** the table's primary key and the index's key attributes, because an index key alone may not be unique (see next section) and the base item still needs unambiguous re-fetch. Overcast already implements this correctly for the *current* fallback: `extractItemKeysWithIndex` ([handler.go:1687-1706](../../internal/services/dynamodb/handler.go)) returns `extractItemKeys` (table hash+sort) plus `indexHashKeyName`/`indexSortKeyName` from the active index. **This contract carries forward unchanged** — the new index structure's cursor is a superset of the same four values (see §4), so no wire-visible change is needed here, only a cheaper way to resolve the cursor's position.

### Key type constraints — the real problem

GSI/LSI key attributes are constrained to top-level `String`, `Number`, or `Binary` scalars (`AttributeDef.AttributeType` already models `"S" | "N" | "B"`, [store.go:121-124](../../internal/services/dynamodb/store.go)). Per the Query reference: **"If the data type of the sort key is Number, the results are returned in numeric order; otherwise, the results are returned in order of UTF-8 bytes."**

**This is not satisfied anywhere in the current codebase, not just for GSIs.** `extractKeyValue` ([store.go:270-278](../../internal/services/dynamodb/store.go)) returns the attribute's raw scalar string regardless of type (`{"N": "50"}` → `"50"`), and every ordering comparison built on top of it is a **string comparison**:

- `resolveCursorPosition` ([handler.go:1641-1669](../../internal/services/dynamodb/handler.go)) — `itemHash > cursorHash` etc. are Go string comparisons.
- `queryTyped`'s stable-sort-by-sort-key ([handler.go:951-955](../../internal/services/dynamodb/handler.go)) — `sort.Slice` comparing `extractKeyValue(...)` strings.
- `scanTyped`'s GSI/parallel-scan total-order sort ([handler.go:691-698](../../internal/services/dynamodb/handler.go)) — same pattern.
- The base-table storage key itself: `memItemBackend`'s `itemCompositeKey(hashKey, sortKey)` ([item_store.go:92-100](../../internal/services/dynamodb/item_store.go)) and `sqlItemBackend`'s `ORDER BY hash_key, sort_key` ([item_store.go:460-508](../../internal/services/dynamodb/item_store.go)) both compare the raw string form of a Number sort key.

Concretely: a table with a Number sort key holding values `5, 10, 50` today paginates/sorts in the order `10, 5, 50` (lexicographic on `"10" < "5" < "50"`), not the AWS-correct numeric order `5, 10, 50`. **This is a pre-existing divergence in the already-shipped A3/G2 work, not something A7 introduces** — but A7 cannot ignore it, because a new ordered index structure keyed the same naive way would bake the same bug into a second place, and GSI sort keys are exactly as likely to be Number-typed as base-table ones (arguably more likely — GSIs are commonly built for range/leaderboard-style queries over numeric attributes, the `TopScore` example in AWS's own docs).

**Proposal: an order-preserving numeric encoding, used by the new index key AND retrofitted to the base-table composite key.** Sketch (not implementation): for a `Number`-typed key component, instead of storing the raw decimal string, encode a fixed-format, memcmp-comparable byte/string sequence:

1. A sign marker byte (`'-'` < `'0'` < `'+'`, or equivalent) so negatives sort before positives.
2. A biased, fixed-width exponent (normalize the value to scientific notation `d.dddd × 10^e`; DynamoDB numbers support up to 38 significant digits and an exponent range of roughly -130..126, so the bias/width must cover that range) so magnitude order is preserved lexicographically.
3. The normalized mantissa digits, complemented (e.g. 9's-complement per digit) when the sign is negative, so that more-negative numbers sort first among negatives.

This is the same family of technique as HBase's `OrderedBytes`/CockroachDB's ordered key encoding for numeric types — well-trodden, but nontrivial to get exactly right for DynamoDB's full numeric range (arbitrary precision decimal, not IEEE float), and deserves its own failing-test-first implementation and dedicated review rather than being folded silently into A7's diff. **Recommendation:** land it as a small standalone `serviceutil`-or-package-local helper (`encodeOrderableNumber(s string) (string, error)`) with its own property tests (round-trip, ordering matches `big.Rat` comparison, edge cases: `0`, `-0`, very large/small exponents) **before** the index structure work in §3 depends on it, and use it in exactly two places on landing — the new GSI index key encoding, and `itemCompositeKey`/`ORDER BY sort_key` for the base table — satisfying the rule of two immediately rather than waiting for a third consumer. This is called out explicitly as a **separate, prerequisite PR** in the phasing (§7), not silently bundled into A7, because it changes on-disk/in-memory key bytes for the *existing* base-table structure and needs its own scoped test/benchmark pass independent of GSI work. A background-task flag has been raised separately for the base-table half of this bug so it isn't lost if A7 stalls (see the note at the end of this document).

---

## 3. Proposed structure

### Memory backend: per-(table, index) ordered tree

Add one `*btree.Map[string, Item]` per `(tableName, indexName)` pair, alongside the existing per-table item tree, inside (or next to) `memItemBackend`:

```go
type memItemBackend struct {
    mu      sync.RWMutex
    tables  map[string]*btree.Map[string, Item]            // existing: base-table items
    indexes map[indexKey]*btree.Map[string, Item]           // new: one tree per (table, index)
}

type indexKey struct{ table, index string }
```

**Composite key** (this is the "unique key" requirement the task calls out, since index key values are explicitly *not* unique in DynamoDB — the `GameTitle`/`TopScore` example in the AWS doc shows three items sharing the same index key):

```
indexHash \x00 indexSort \x00 baseHash \x00 baseSort
```

using the same NUL-separator convention as `itemCompositeKey` ([item_store.go:92-100](../../internal/services/dynamodb/item_store.go)) and `internal/state/memory.go`'s `storeKey`, for the same justification already documented there (AWS resource/attribute values are printable UTF-8; NUL never appears). Ordering the tree by this full 4-tuple gives:

- **Correct index order** — items sharing an index key sort by their base key as a tiebreak, which is an implementation detail AWS doesn't define ("within that set of data, the items are in no particular order") — any deterministic tiebreak is compliant.
- **A well-defined position-based cursor** for G2/A3-style pagination (see §4) — no two rows in the tree ever compare equal, so "the position after the cursor" is always unambiguous even when many items share the index key.

Numeric index-key components use the §2 order-preserving encoding before being placed in the composite key; the *value stored in the tree* (`Item`) is unchanged — only the seek key changes shape, exactly as `itemCompositeKey` today stores the raw `Item` under a derived ordering key.

The value stored per index row is **only the index's projected attribute set** (§2's projection-fidelity requirement) — not the full base item. `ProjectionType: ALL` stores a full copy (this is what real AWS does too — "an `ALL` projection results in the largest possible secondary index," "your account is charged for storage... also for storage of attributes in any global secondary indexes"), `KEYS_ONLY` stores just the table PK + index key, `INCLUDE` stores the PK + index key + the named attributes.

### SQL backend: dedicated table vs. expression indexes vs. computed columns

Three options considered:

**Option A — `dynamodb_index_entries` table (recommended).**

```sql
CREATE TABLE IF NOT EXISTS dynamodb_index_entries (
    table_name   TEXT NOT NULL,
    index_name   TEXT NOT NULL,
    index_hash   TEXT NOT NULL,   -- order-preserving-encoded if Number
    index_sort   TEXT NOT NULL DEFAULT '',
    base_hash    TEXT NOT NULL,
    base_sort    TEXT NOT NULL DEFAULT '',
    item_json    TEXT NOT NULL,   -- only the projected attribute set
    PRIMARY KEY (table_name, index_name, index_hash, index_sort, base_hash, base_sort)
)
```

Query becomes a row-value range scan on the PK — identical shape to `sqlItemBackend.scanPage`'s `(hash_key, sort_key) > (?, ?)` pattern, this codebase's established "model implementation" (per A3's own doc comment, [item_store.go:472-478](../../internal/services/dynamodb/item_store.go)):

```sql
SELECT item_json, base_hash, base_sort FROM dynamodb_index_entries
WHERE table_name = ? AND index_name = ? AND index_hash = ?
  AND (index_sort, base_hash, base_sort) > (?, ?, ?)
ORDER BY index_sort, base_hash, base_sort
LIMIT ?
```

**Option B — SQLite expression indexes on `dynamodb_items`**, e.g. `CREATE INDEX ON dynamodb_items (table_name, json_extract(item_json, '$.GameTitle.S'), json_extract(item_json, '$.TopScore.N'))`. Rejected: (1) the index attribute name is only known per-table at `CreateTable`/`UpdateTable` time, so this requires **dynamically generated `CREATE INDEX` DDL per table+index**, which the migration system isn't built for (migrations are static, versioned, registered at `init()` — not a mechanism for runtime-created, per-tenant schema objects) and which would need its own lifecycle (create on GSI add, drop on GSI delete or table delete) outside the migration runner entirely. (2) `json_extract` on a `TEXT` column can't apply the order-preserving numeric encoding from §2 — SQLite's `json_extract(...,'$.TopScore.N')` returns the *stored decimal string*, so `ORDER BY` on the expression index would reproduce the exact lexicographic-numeric bug §2 diagnoses, just moved into SQL. (3) Sparse-index semantics (items missing the key are absent) fall out"free" from a real WHERE-driven query in Option A but need a `WHERE json_extract(...) IS NOT NULL` condition threaded through every query against an expression index, plus the index only helps when SQLite's planner chooses to use it, which is an internal decision this codebase can't control the way it controls a first-class PK layout.

**Option C — computed/generated columns on `dynamodb_items`** (`ALTER TABLE ... ADD COLUMN gsi1_hash TEXT GENERATED ALWAYS AS (...) STORED`, with a plain index on the generated column). Rejected: SQLite's generated columns are fixed at table-creation/`ALTER TABLE` time per **column**, but DynamoDB tables can have an unbounded number of GSIs added over the table's lifetime (via `UpdateTable`) — this would require an `ALTER TABLE ADD COLUMN` per GSI, on the *shared* `dynamodb_items` table used by every DynamoDB table in the store, which conflates one table's schema evolution with every other table's storage, and still can't apply the order-preserving numeric transform inside a generated-column expression using only SQLite's built-in functions (no user-defined functions are registered against `modernc.org/sqlite` in this codebase's stores today).

**Recommendation: Option A.** It reuses the exact pattern A3 already validated (row-value keyset range scan on a purpose-built PK), keeps sparse-index semantics as a simple absence-of-row (never write a row when the index key is missing — see write path below), and keeps the numeric encoding entirely inside the value written to `index_hash`/`index_sort` rather than fighting SQLite's expression/generated-column machinery. New migration: **version 22** (`migrationDynamoDBIndexEntriesTableVersion`), next available slot in the DynamoDB 20-29 range per [internal/state/migrate.go:32-34](../../internal/state/migrate.go) (20 and 21 are already used by `dynamodb_items`/`dynamodb_stream_records`; see [migrations.go:43-46](../../internal/services/dynamodb/migrations.go)).

### Write-path maintenance

AWS's own cost model (`GSI.ThroughputConsiderations.Writes` in the same doc) is the exact write-amplification decision table to implement:

| Base-table write | Index write |
|---|---|
| New item defines the indexed attribute(s) | **1 write** — insert into index |
| Update changes an indexed key attribute's value (A→B) | **2 writes** — delete old index row, insert new index row |
| Update deletes a previously-defined indexed attribute | **1 write** — delete index row |
| Item never had the indexed attribute, still doesn't | **0 writes** — sparse, nothing to do |
| Update changes only a *projected* (non-key) attribute | **1 write** — update the stored projection copy in place |
| Update changes an attribute neither keyed nor projected | **0 writes** — index entry unchanged |

Mapped onto the existing handlers, per-index, for every GSI on the table (a table with N GSIs pays this once per GSI per write, matching AWS):

- **`PutItem`** ([handler.go:402-469](../../internal/services/dynamodb/handler.go)): today, `oldItem` is only fetched `if table.streamEnabled() || req.ReturnValues == "ALL_OLD"` (line 449). **Extend that condition to include `len(table.GlobalSecondaryIndexes) > 0`** — a table with GSIs must always read the old item on Put to compute the diff table above, exactly the same read the stream path already needs and already gates on table configuration rather than paying for it on every table. After `putItem` succeeds, diff `oldItem` vs `req.Item` per-GSI and issue the corresponding index delete/insert.
- **`UpdateItem`** ([handler_update.go:48-160](../../internal/services/dynamodb/handler_update.go)): **no new read is needed** — `existing` is already unconditionally fetched at line 59 (upsert semantics require it regardless of streams/GSIs), so index maintenance here is pure downstream diffing of `existing` vs the post-`applyUpdateExpression` `item`, at zero extra read cost.
- **`DeleteItem`** ([handler.go:540-599](../../internal/services/dynamodb/handler.go)): `oldItem` is already fetched unconditionally before the delete (line 548, needed for `ConditionExpression`/`ALL_OLD` regardless) — same zero-extra-read situation. Every GSI whose key the old item satisfied gets one index-row delete.
- **`BatchWriteItem`** ([handler.go:1521-1580](../../internal/services/dynamodb/handler.go)): same shape as Put/Delete per sub-operation; `oldItem` fetches already happen per-request at lines 1559/1571 conditioned on stream state — extend the same way as PutItem.

**Atomicity within existing boundaries.** Neither backend has cross-table-and-index transactional writes today for any existing feature (stream records are appended as a logically-separate side effect of the same handler call, not inside one DB transaction with the item write). This design does not raise the bar: on the SQL backend, the base-item `INSERT OR REPLACE` and the index row `INSERT`/`DELETE`(s) should be wrapped in **one `*sql.Tx`** per write operation (new capability for `sqlItemBackend`, which currently issues bare `ExecContext` calls with no explicit transaction) so that a crash between the two never leaves an index row pointing at a since-vanished base item — this is strictly better than the current stream-record situation, not a downgrade, and costs nothing extra on the already-serialized single-writer SQLite path. On the memory backend, the existing single `sync.RWMutex` already serializes all base-item and (new) index-tree mutations under one lock acquisition per write, so atomicity is free.

### Sparse-index write rule

Simple and matches AWS exactly: **before writing an index row, check the item has a value for the index's hash key** (and sort key, if the index has one — AWS requires *both* to be present for propagation, since a composite index key with a missing sort key component is exactly as absent as a missing hash key). If absent, skip the index write entirely (delete any pre-existing index row for that base item, if this is an update that removed the attribute — the "Update deletes indexed attribute" row above). This reuses the same key-presence check the current fallback already does at read time ([handler.go:672-681](../../internal/services/dynamodb/handler.go)) — the design simply moves it from "filter every read" to "decide once per write."

### Backfill semantics

**`CreateTable` with GSIs defined up front:** trivial — a brand-new table has zero items, so "backfill" is an empty no-op; every subsequent write populates the index going forward. No special-case code needed beyond the write-path maintenance above.

**`UpdateTable` adding a GSI to a table that may already have items — does Overcast support this today?** **Yes, already**, at metadata level only: [handler.go:1108-1115](../../internal/services/dynamodb/handler.go)'s `GlobalSecondaryIndexUpdates[].Create` branch appends the new `SecondaryIndex` to `table.GlobalSecondaryIndexes` and marks it `IndexStatus: "ACTIVE"` immediately — there is no backfill today because there is no index storage to backfill into (the GSI Query/Scan fallback just re-derives everything from the base table on every call, so a "new" GSI added via `UpdateTable` works immediately for free). **Once real index storage exists, this call site must synchronously backfill**: scan the table's existing items (a bounded, one-time cost — see the boundedness note in §5) and insert index rows for every item that satisfies the new index's sparse-write rule, before marking `IndexStatus: "ACTIVE"` and returning. This keeps the existing "mark ACTIVE immediately" behavior — which is already a documented, precedented emulator simplification: `CreateTable` does the same thing for the table itself (`TableStatus: "ACTIVE"` set directly at [handler.go:234](../../internal/services/dynamodb/handler.go), never `"CREATING"`) — rather than introducing a new `CREATING`/backfill-in-progress state that nothing else in this codebase models. Real AWS's GSI backfill is asynchronous and can take a long time on large tables (`IndexStatus: "CREATING"` until it completes); Overcast's synchronous, backfill-then-ACTIVE approach is an intentional, documented divergence in the same spirit as the table-creation precedent — acceptable specifically because A7's own gate restricts this work to "the emulator's typical GSI tables are small."

---

## 4. Read-path integration

### Routing

`scanTyped`'s branch condition at [handler.go:644](../../internal/services/dynamodb/handler.go) (`scanIdx == nil && req.TotalSegments <= 1`) gains a new first-class case: `scanIdx != nil` alone (no parallel segments) routes to an index-keyed equivalent of `scanItemsPage` — a new `scanIndexPage(ctx, table, indexName, exclusiveStartKey, limit)` that seeks the `(table, index)` tree/table directly instead of falling through to the existing full-scan-and-sort branch. **Parallel scan segments (`TotalSegments > 1`), with or without an index, are explicitly not addressed by this design** — see §5.

`queryTyped`'s `req.IndexName != ""` branch at [handler.go:892-912](../../internal/services/dynamodb/handler.go) is replaced with an index-hash partition read: a new `scanIndexByHash(ctx, table, indexName, hashVal)` mirroring `scanItemsByHashKey`'s O(k) shape, giving GSI `Query` the same partition-scoped cost as base-table `Query` already has — closing the exact gap §1 identifies. The sort-key condition (`kc.sortCond`), `FilterExpression`, `ProjectionExpression`, `Select`, `ScanIndexForward`, and `Limit` handling downstream of the item-collection step are **unchanged** — they already operate on whatever `matched []Item` came from the collection step, so swapping the collection step's data source is a localized change per the fidelity principle (behavioral logic untouched; only the storage-access step changes).

### Cursor extension

The index cursor generalizes A3/G2's positional cursor to a 4-tuple: `(indexHash, indexSort, baseHash, baseSort)` — exactly the composite key from §3's tree/table layout. `extractItemKeysWithIndex` ([handler.go:1690-1706](../../internal/services/dynamodb/handler.go)) already assembles the AWS-visible `LastEvaluatedKey` shape (base PK + index key) from an item plus its index — **no wire-format change**; only the *internal* resolution of "where does this cursor sit in the index" changes, from G2's O(n) `resolveCursorPosition` linear scan-with-comparison to a direct O(log n) seek into the new ordered structure, the same upgrade A3 gave the base table.

### Position-based semantics carry over

G2's headline fix — a cursor naming a since-deleted item still resolves to the correct resume point because the comparison is positional (`key-order`), not identity (`item still exists`) — applies unchanged to the index cursor: seeking to `(indexHash, indexSort, baseHash, baseSort)` in the ordered index structure and taking the next entry is positional by construction, identical in spirit to `memItemBackend.scanPage`'s `tree.Ascend(afterKey, ...)` ([item_store.go:211-240](../../internal/services/dynamodb/item_store.go)) and `sqlItemBackend.scanPage`'s row-value predicate ([item_store.go:479-508](../../internal/services/dynamodb/item_store.go)).

### Projection filtering location

Per the fidelity principle ("keep behavioral semantics in handlers"), *which* attributes a client is allowed to request (`ProjectionExpression`, `Select=ALL_PROJECTED_ATTRIBUTES` vs `SPECIFIC_ATTRIBUTES`) stays exactly where it is today — evaluated in the handler via `compileProjection`/`applyProjection` ([handler.go:768-780](../../internal/services/dynamodb/handler.go), [1019-1032](../../internal/services/dynamodb/handler.go)). What changes is **what's available to project from**: the handler now receives index rows already narrowed to the index's own `Projection` (§3), so `applyProjection`/`Select` operate on a smaller candidate attribute set for GSI reads — the storage layer enforces "can't fetch attributes from the parent table" (a structural, storage-boundary fact per the fidelity principle's own framing — "push structural predicates into storage"), while the handler still owns the AWS-expression-language semantics of *which subset* of that set the client asked for. This is the fix for the projection-fidelity gap identified in §2.

---

## 5. What deliberately stays out

- **LSIs get no new structure.** A Local Secondary Index shares the base table's hash key by definition — an LSI Query is already expressible as a partition-scoped read (`scanItemsByHashKey`, the same O(k) primitive base-table Query already uses) filtered/sorted by the LSI's sort key, with no need for a *separately hash-partitioned* ordered structure the way a GSI needs. Real AWS also forbids adding/removing LSIs after table creation (`updateTableTyped` has no `LocalSecondaryIndexUpdates` handling today, matching AWS), so there's no backfill question either. **Today's code routes LSI Query through the exact same full-table-scan branch as GSI** ([handler.go:892](../../internal/services/dynamodb/handler.go) doesn't distinguish `findIndex`'s GSI vs LSI branch, [store.go:147-159](../../internal/services/dynamodb/store.go)) — this is a real, cheaper-to-fix inefficiency, but it's a *routing* fix (send LSI queries down the existing partition-scoped path with an extra sort-key filter) not a *new-structure* fix, and is called out here as explicitly separable follow-up work, not bundled into A7.
- **Byte-size page caps.** AWS's real Query/Scan cap is ~1 MB of accumulated item size per page; Overcast's existing `dynamoDefaultPageLimit = 1000` item-count heuristic ([handler.go:1594-1623](../../internal/services/dynamodb/handler.go)) is an already-accepted stand-in (documented as such when A3/G2 landed — "true accumulated-byte accounting deferred... nothing tracks item byte size on this path yet"). The index read path inherits the same heuristic unchanged; adding real byte accounting is orthogonal to A7 and would apply equally to the base-table path that doesn't have it today.
- **Parallel scan segments over an index or over the base table.** Segmenting an *ordered* structure correctly (each segment gets a contiguous, non-overlapping key range, not an arbitrary post-hoc slice of an already-materialized sorted list) is its own design question — real DynamoDB defines segment boundaries by internal partition/hash-space division, which Overcast has no equivalent physical concept of. `TotalSegments > 1` (with or without `IndexName`) keeps using today's full-scan-then-slice fallback after this design lands; it is explicitly out of scope here per the boundedness rule's spirit — a correct fix needs its own design pass on what a "segment" even means over `tidwall/btree`/SQLite storage, not a corollary of the GSI index structure.
- **Anything the boundedness rule would exclude.** N/A here — GSI item volume is unbounded/data-plane by nature (workload-created items, not human/IaC-created resource metadata), squarely inside the boundedness rule's "legitimate target" category, same classification as the base table's A3.

---

## 6. Test + benchmark plan

Mirrors the two-backend, failing-first discipline `item_store_test.go`/`item_store_bench_test.go` already established for A3 ([internal/services/dynamodb/item_store_test.go](../../internal/services/dynamodb/item_store_test.go), [item_store_bench_test.go](../../internal/services/dynamodb/item_store_bench_test.go)).

**Parity-with-full-scan property tests, both backends** (new `index_store_test.go`, run via `newTestItemBackends`-style table-driven helper over both `memItemBackend`/`sqlItemBackend` — or their index-store equivalents):

- Walking the new index structure to exhaustion for a given index-hash value returns exactly the item set the *current* full-scan-and-filter fallback returns for the same Query, for a fixture with a mix of dense and sparse items — no dups, no gaps. This is the direct analogue of `TestItemBackend_ScanPage_ParityWithScanAll`.
- Same property for GSI Scan (whole-index walk) vs. today's scan-all-and-filter-by-key-presence fallback.

**Sparse/projection/cursor edge tests:**

- Put an item missing the index key → confirm zero index rows are created; put a second item that has the key → confirm exactly one index row exists (not two, not zero).
- Update an item to remove a previously-present index key → confirm the stale index row is deleted (the "Update deletes indexed attribute" write-amplification case from §3's table) — this is the sparse-index equivalent of A1's "no stale/colliding data on removal" class of bug and deserves the same failing-first rigor A1 used for Kinesis sequence numbers.
- Update an item's indexed key value A→B → confirm exactly one old-key row is gone and one new-key row exists (the "2 writes" case), and that a page walk spanning the transition shows neither a duplicate nor a gap.
- `KEYS_ONLY`/`INCLUDE` projected GSI Query returns exactly the projected attribute set, never a base-table attribute outside the projection — this is the regression test for the projection-fidelity gap identified in §2 (a test that would **fail against today's fallback**, which is the point: it's a correctness fix bundled with the perf fix).
- G2-style cursor-survives-deletion test, generalized to the 4-tuple index cursor: page 1 of a GSI Query, delete the last-returned item (or an item that shared its index key), page 2 must show no duplicates and no gaps — direct analogue of `TestItemBackend_ScanPage_CursorSurvivesDeletedItem`.
- Order-preserving numeric encoding: round-trip test (`decode(encode(n)) == n` for representative values including 0, negative, very large/small magnitude) plus an ordering test comparing encoded-string order against `big.Rat`/`big.Float` numeric order for a generated set of pairs — this is the encoding helper's own test suite, landed in its prerequisite PR (§7) before any index-structure test depends on it.

**Benchmark shape** (mirrors `item_store_bench_test.go`'s conventions — preload via the backend's `put`/index-equivalent write method directly, not through the HTTP handler, so setup cost stays linear and only the timed loop measures the read path; `b.ReportAllocs()` as the signal, not wall-clock):

```go
func BenchmarkDynamoDB_GSIQueryPageLimit25_Memory_{0,2000,8000}Items(b *testing.B)
func BenchmarkDynamoDB_GSIQueryPageLimit25_SQL_{0,500,1500}Items(b *testing.B)
```

Preload N items spread across many *distinct* index-hash values (so a single GSI Query only ever touches a small, page-sized slice of the index regardless of table size — the flat-curve property being proven), then benchmark a `Limit=25` GSI Query for one index-hash value across table sizes. Flat allocs/op across preload sizes is the accept criterion, identical framing to A3's own acceptance bar ("Scan Limit=25 flat vs table size"). Per this session's constraints, **no benchmark is run here** — this is the specified shape for whoever implements A7 to execute under the storage-test-plan.md discipline (container-native FS, exclusive machine).

**Failing-first strategy:** every test above should be written and confirmed to fail against the *current* fallback implementation before any index-structure code exists — several of them already fail today (the projection-fidelity test, the numeric-ordering test) which is useful evidence this design is fixing real bugs, not just adding an optimization.

---

## 7. Effort estimate + phasing

**Phasing — independently green at each step:**

1. **Prerequisite: order-preserving numeric encoding helper.** Small, self-contained, own test suite (§2/§6). Lands first because both the base-table retrofit and the new index key encoding depend on it, and it's the one piece of this design touching *existing* on-disk/in-memory key bytes — it needs isolated benchmark/correctness attention, not to be a footnote inside a larger diff. **Effort: small (1-2 days).**
2. **Index-structure-first:** `dynamodb_index_entries` table + migration (version 22), `memItemBackend` index-tree additions, write-path maintenance (Put/Update/Delete/BatchWrite diffing, sparse-write rule, backfill-on-`UpdateTable`-Create), all covered by the §6 parity/sparse/projection/cursor tests — **but read paths (`scanTyped`/`queryTyped`) keep calling the existing full-scan fallback.** The new structure is built and proven correct in isolation (a table that's *never queried* through it yet still exercises every write-path test), independently green: existing behavior is byte-for-byte unchanged from a client's perspective, this phase is purely additive storage plumbing. **Effort: medium (roughly A3-sized — A3 itself was "done" in one session per its landing note, but A7 has strictly more surface: two write paths' worth of diffing logic plus the encoding dependency). Estimate: 3-5 days.**
3. **Read-path switch:** route `scanTyped`'s single-index-no-parallel-segments case and `queryTyped`'s `IndexName != ""` case onto the new structure (§4), delete the now-dead full-scan GSI branches, land the §6 benchmarks. Independently green: this phase is a pure swap of the read source behind already-tested storage; if the index-structure phase's tests pass, this phase's risk is purely "did we wire the right method call," not "is the data correct." **Effort: small-medium (1-3 days), plus a benchmark pass per storage-test-plan.md discipline (needs a quiet machine — flag ahead of time, per the A3 precedent that had to defer its own SQL-backend numbers for exactly this reason).**
4. **Follow-up, explicitly separable (not blocking A7 acceptance):** LSI query routing fix (§5), `ConsistentRead=true`-on-GSI `ValidationException` (§2), parallel-scan-over-ordered-structure design (§5).

**Risks:**

- **Write-amplification regression on tables with many GSIs.** A table with N GSIs now pays up to 2N extra index writes per base-table write (worst case: every GSI's key changed). This mirrors AWS's own documented cost model exactly (§3's table is copied from AWS's own throughput-considerations doc), so it's not a *divergence* risk, but it is a *performance* risk worth a benchmark specifically for it (PutItem/UpdateItem ns/op and allocs/op vs. GSI count, at fixed item size) — not currently in this document's benchmark list; flag for the implementer to add.
- **The numeric-encoding prerequisite touching existing base-table key bytes.** Changing `itemCompositeKey`/`ORDER BY sort_key`'s comparison semantics for Number-typed sort keys is a **behavior change** for any existing table with a Number sort key (pagination order changes from lexicographic to numeric) — this is a bug fix per AWS fidelity, but it's the kind of change that should be called out prominently in `CHANGELOG.md` when it lands (its own PR, per the phasing above), since a client that had (incorrectly) come to depend on the old ordering would observe a difference. Not a reason to avoid the fix — AWS fidelity wins per this repo's stated principles — but worth flagging loudly rather than bundling quietly into A7's diff.
- **`sqlItemBackend` gaining explicit transactions for the first time.** Low risk technically (SQLite already serializes writes on this codebase's single-writer model), but it's new code path (`*sql.Tx` wrapping) that didn't exist before and deserves its own focused test (crash/error mid-transaction leaves neither the item nor a partial index row committed).
- **Backfill cost on `UpdateTable`-added GSIs for a table that already has many items.** Bounded by the A7 gate's own premise ("the emulator's typical GSI tables are small"), but if that premise is wrong for some user's workload, a synchronous backfill blocks the `UpdateTable` HTTP response for the scan duration — acceptable per the gate's stated scope, but the reviewer should confirm this tradeoff explicitly (see the accept/reject question below) rather than it being assumed.

**The accept/reject question for the reviewer:**

> Approve **Option A** (dedicated `dynamodb_index_entries` table, migration version 22, per-(table,index) memory tree keyed `indexHash\x00indexSort\x00baseHash\x00baseSort`) as the index structure, with the order-preserving numeric encoding as a **separate prerequisite PR** landed before it — or specify which piece needs a different call: (a) the SQL table-vs-expression-index-vs-generated-column choice in §3, (b) the numeric-encoding scheme's necessity/design in §2, (c) the synchronous-backfill-on-UpdateTable behavior in §3, or (d) the phasing/scope boundary in §5 (LSI routing, parallel scan, `ConsistentRead` validation deliberately deferred).

---

*A background task has been flagged separately (not part of this document, no code changed here) for the pre-existing base-table lexicographic-numeric-sort-key bug identified in §2, so it has its own tracking independent of whether A7 is accepted, deferred, or reshaped.*
