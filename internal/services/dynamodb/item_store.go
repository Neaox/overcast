package dynamodb

// itemBackend is the DynamoDB-specific storage layer for items.
//
// Items are indexed directly by (tableName, hashKey, sortKey) — mirroring
// DynamoDB's actual storage model.
//
// tableName here is a *region-qualified* table key ("<region>/<name>"), minted
// by dynamoStore.tableKey and never by a backend. A DynamoDB table is a
// regional resource, so two same-named tables in different regions must not
// share item rows or index entries (issue #673); folding the region into the
// key the backends already partition on is what keeps them apart without every
// method here having to grow a region parameter. Backends treat the value as
// an opaque string — they neither parse nor construct it.
//
// The layout gives:
//
//   - GetItem:        O(1) / O(log n) — single map lookup or indexed SQL row read
//   - Query by hash:  O(k) — loads only the items in one partition
//   - Full Scan:      O(n) — always a full table scan (unavoidable; scanAll)
//   - Scan pages:      O(log n + limit) — scanPage (storage-access-plan.md A3)
//   - DeleteItem:     O(1) / O(log n) — single map delete or indexed SQL delete
//
// Two implementations are provided:
//
//   memItemBackend  — an in-process ordered tree per table (tidwall/btree,
//                     the same library internal/state/memory.go uses for
//                     MemoryStore), zero JSON serialisation overhead
//   sqlItemBackend  — SQLite table with a (table_name, hash_key, sort_key) primary key
//
// The appropriate backend is chosen at startup based on the state.Store type.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/tidwall/btree"
)

// itemBackend is the interface every DynamoDB item store must implement.
type itemBackend interface {
	// put stores (or overwrites) an item at (table, hash, sort).
	// sortKey is "" for hash-only tables.
	put(ctx context.Context, tableName, hashKey, sortKey string, item Item) error

	// get retrieves an item. Returns (nil, false, nil) when not found.
	get(ctx context.Context, tableName, hashKey, sortKey string) (Item, bool, error)

	// remove deletes an item. Returns nil if the item did not exist.
	remove(ctx context.Context, tableName, hashKey, sortKey string) error

	// queryByHash returns all items in a partition (same hash key), in sort-key order.
	queryByHash(ctx context.Context, tableName, hashKey string) ([]Item, error)

	// scanAll returns every item in a table.
	scanAll(ctx context.Context, tableName string) ([]Item, error)

	// scanPage returns up to limit items ordered by (hashKey, sortKey), strictly
	// after (afterHash, afterSort) when hasAfter is true, or starting from the
	// beginning of the table when hasAfter is false. This is a keyset page
	// (`WHERE (hash_key, sort_key) > (?, ?) ORDER BY hash_key, sort_key LIMIT ?`
	// on the SQL backend; an ordered-tree seek on the memory backend) — cost is
	// proportional to limit, not to table size, unlike scanAll.
	//
	// The cursor is positional, not an identity lookup: (afterHash, afterSort)
	// need not name a row that still exists. A deleted "last returned item"
	// still resolves to the correct resume point because the comparison is a
	// key-order predicate, not an equality match — this is what lets
	// pagination-plan.md G2's duplicate-delivery fix and storage-access-plan.md
	// A3's paging share one implementation.
	scanPage(ctx context.Context, tableName string, hasAfter bool, afterHash, afterSort string, limit int) ([]Item, error)

	// count returns the number of items in a table without loading item values.
	count(ctx context.Context, tableName string) (int64, error)

	// deleteAll removes every item from a table (called on DeleteTable).
	deleteAll(ctx context.Context, tableName string) error

	// scanExpiredTTL returns items whose TTL attribute (a Number containing a
	// Unix epoch timestamp in seconds) is > 0 and <= cutoffUnix. This allows
	// the sweeper to fetch only expired items instead of scanning every item.
	scanExpiredTTL(ctx context.Context, tableName, ttlAttr string, cutoffUnix int64) ([]Item, error)

	// debugScan returns raw item rows for /_debug/state/dynamodb:items.
	debugScan(ctx context.Context) ([]debugItemRecord, error)

	// debugDeleteAll removes all item rows for debug reset operations.
	debugDeleteAll(ctx context.Context) error

	// ---- GSI index-entry maintenance (docs/plans/dynamodb-gsi-design.md §3) --
	//
	// These methods are storage-only plumbing: as of this phase, no read path
	// (Query/Scan) consults them yet — scanTyped/queryTyped still use the
	// full-scan fallback unconditionally (phase 3 wires that up). They exist
	// so PutItem/UpdateItem/DeleteItem/BatchWriteItem can keep a real ordered
	// index structure in sync on every write, proven correct in isolation
	// before anything reads from it.

	// putWithIndexMutations stores (or overwrites) the base item at (table,
	// hash, sort) — identical to put — and atomically applies mutations to
	// the table's GSI index entries in the same operation (one *sql.Tx on
	// the SQL backend; the same mutex critical section on the memory
	// backend). mutations is typically produced by diffIndexMutations.
	putWithIndexMutations(ctx context.Context, tableName, hashKey, sortKey string, item Item, mutations []indexMutation) error

	// removeWithIndexMutations deletes the base item at (table, hash, sort)
	// — identical to remove — and atomically applies mutations to the
	// table's GSI index entries in the same operation.
	removeWithIndexMutations(ctx context.Context, tableName, hashKey, sortKey string, mutations []indexMutation) error

	// applyIndexMutations applies index-entry mutations without touching any
	// base item — used for GSI backfill (UpdateTable adding a GSI to a
	// table that already has items, and migration-time backfill for GSIs
	// declared on tables created before this feature existed). All
	// mutations are applied in one transaction on the SQL backend.
	applyIndexMutations(ctx context.Context, tableName string, mutations []indexMutation) error

	// scanIndexAll returns every entry stored for (table, index), ordered by
	// the composite key (indexHash, indexSort, baseHash, baseSort). Each
	// Item is the index's own projected attribute set (§3), not the base
	// item.
	scanIndexAll(ctx context.Context, tableName, indexName string) ([]Item, error)

	// queryIndexByHash returns entries for (table, index) sharing indexHash,
	// ordered by (indexSort, baseHash, baseSort).
	queryIndexByHash(ctx context.Context, tableName, indexName, indexHash string) ([]Item, error)

	// scanIndexPage returns up to limit entries for (table, index), ordered
	// by the composite key (indexHash, indexSort, baseHash, baseSort),
	// strictly after (afterIndexHash, afterIndexSort, afterBaseHash,
	// afterBaseSort) when hasAfter is true, or starting from the beginning
	// of the index when hasAfter is false. This is the GSI-Scan analogue of
	// scanPage — a keyset page over the index's own ordered structure
	// (dynamodb-gsi-design.md §4), used by scanTyped's whole-index GSI Scan
	// (no IndexName hash-equality predicate, no parallel segments). Same
	// positional-cursor contract as scanPage: the cursor need not name a row
	// that still exists (pagination-plan.md G2's fix, generalized to index
	// reads).
	scanIndexPage(ctx context.Context, tableName, indexName string, hasAfter bool, afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort string, limit int) ([]Item, error)

	// countIndexEntries returns the number of entries stored for (table, index).
	countIndexEntries(ctx context.Context, tableName, indexName string) (int64, error)

	// deleteAllIndexEntriesForTable removes every index row for every GSI on
	// tableName (called on DeleteTable, alongside deleteAll).
	deleteAllIndexEntriesForTable(ctx context.Context, tableName string) error

	// deleteAllIndexEntriesForIndex removes every index row for one
	// (table, index) (called when a GSI is removed via UpdateTable, so a
	// later GSI recreated under the same name doesn't inherit stale rows).
	deleteAllIndexEntriesForIndex(ctx context.Context, tableName, indexName string) error
}

// indexMutationOp identifies whether an indexMutation upserts or deletes one
// index row.
type indexMutationOp int

const (
	indexMutationUpsert indexMutationOp = iota
	indexMutationDelete
)

// indexMutation describes one change to a single GSI index row, keyed by the
// composite key (indexHash, indexSort, baseHash, baseSort) — see
// indexCompositeKey. item is only meaningful for indexMutationUpsert (the
// index's own projected attribute set, per idx.Projection — see
// projectForIndex); indexMutationDelete only needs the key. Produced by
// diffIndexMutations (write-path maintenance) and buildIndexMutation
// (backfill).
type indexMutation struct {
	indexName            string
	op                   indexMutationOp
	indexHash, indexSort string
	baseHash, baseSort   string
	item                 Item
}

type debugItemRecord struct {
	TableName string
	HashKey   string
	SortKey   string
	Item      Item
}

// itemCompositeKey builds the ordered map key for one item: hashKey and
// sortKey concatenated with a NUL separator so lexicographic string order on
// the composite key matches DynamoDB's (hashKey, sortKey) tuple order — the
// same separator convention internal/state/memory.go's storeKey uses, for the
// same reason (AWS resource/attribute values are always printable UTF-8, so
// NUL never appears inside a real key).
func itemCompositeKey(hashKey, sortKey string) string {
	return hashKey + "\x00" + sortKey
}

// ---------------------------------------------------------------------------
// memItemBackend — zero-serialisation in-process store, ordered by key
// ---------------------------------------------------------------------------
//
// Data layout:
//
//	tables[tableName] = ordered tree of itemCompositeKey(hashKey, sortKey) -> Item
//
// Using an ordered tree (rather than the nested maps this backend used
// before storage-access-plan.md A3) is what makes scanPage an O(log n+limit)
// seek instead of an O(n) sort-then-slice: the tree keeps items in
// (hashKey, sortKey) order at all times, so a page starting after any cursor
// — including one whose exact item has since been deleted — is a single
// bounded Ascend from that position (mirrors state.MemoryStore.ScanPage's
// btree seek, storage-access-plan.md pattern P1).
//
// A single RWMutex protects the whole store. Per-table locking would improve
// throughput under concurrent multi-table workloads, but the emulator's target
// use case (one dev/CI process) doesn't justify the added complexity.

type memItemBackend struct {
	mu     sync.RWMutex
	tables map[string]*btree.Map[string, Item]

	// indexes holds one ordered tree per (table, GSI) pair — see
	// docs/plans/dynamodb-gsi-design.md §3. Populated lazily, same
	// convention as tables. Protected by the same mu as tables: base-item
	// and index-tree mutations for one write share a single lock
	// acquisition, which is what makes putWithIndexMutations/
	// removeWithIndexMutations atomic on this backend for free.
	indexes map[indexKey]*btree.Map[string, Item]
}

// indexKey identifies one (table, GSI) pair's index tree.
type indexKey struct{ table, index string }

// indexCompositeKey builds the ordered map key for one GSI index row:
// indexHash and indexSort (the index's own key, possibly shared by many base
// items — AWS's GameTitle/TopScore example explicitly allows this) followed
// by baseHash and baseSort as a uniqueness tiebreak, NUL-separated exactly
// like itemCompositeKey. No two rows in an index tree ever compare equal,
// which is what makes a position-based cursor well-defined even when many
// items share the same index key (dynamodb-gsi-design.md §3/§4 — the cursor
// upgrade itself is phase 3, but the ordering guarantee is established here).
func indexCompositeKey(indexHash, indexSort, baseHash, baseSort string) string {
	return indexHash + "\x00" + indexSort + "\x00" + baseHash + "\x00" + baseSort
}

func newMemItemBackend() *memItemBackend {
	return &memItemBackend{
		tables:  make(map[string]*btree.Map[string, Item]),
		indexes: make(map[indexKey]*btree.Map[string, Item]),
	}
}

func (b *memItemBackend) put(ctx context.Context, tableName, hashKey, sortKey string, item Item) error {
	return b.putWithIndexMutations(ctx, tableName, hashKey, sortKey, item, nil)
}

func (b *memItemBackend) putWithIndexMutations(_ context.Context, tableName, hashKey, sortKey string, item Item, mutations []indexMutation) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	tree := b.tables[tableName]
	if tree == nil {
		tree = &btree.Map[string, Item]{}
		b.tables[tableName] = tree
	}
	tree.Set(itemCompositeKey(hashKey, sortKey), item)
	b.applyIndexMutationsLocked(tableName, mutations)
	return nil
}

func (b *memItemBackend) get(_ context.Context, tableName, hashKey, sortKey string) (Item, bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.tables[tableName]
	if tree == nil {
		return nil, false, nil
	}
	item, ok := tree.Get(itemCompositeKey(hashKey, sortKey))
	return item, ok, nil
}

func (b *memItemBackend) remove(ctx context.Context, tableName, hashKey, sortKey string) error {
	return b.removeWithIndexMutations(ctx, tableName, hashKey, sortKey, nil)
}

func (b *memItemBackend) removeWithIndexMutations(_ context.Context, tableName, hashKey, sortKey string, mutations []indexMutation) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if tree := b.tables[tableName]; tree != nil {
		tree.Delete(itemCompositeKey(hashKey, sortKey))
	}
	b.applyIndexMutationsLocked(tableName, mutations)
	return nil
}

// applyIndexMutationsLocked applies index mutations to b.indexes. Callers
// must hold b.mu (write lock) — shared by putWithIndexMutations,
// removeWithIndexMutations, and applyIndexMutations (backfill), so every
// caller gets the same all-mutations-under-one-lock atomicity.
func (b *memItemBackend) applyIndexMutationsLocked(tableName string, mutations []indexMutation) {
	for _, m := range mutations {
		k := indexKey{table: tableName, index: m.indexName}
		switch m.op {
		case indexMutationUpsert:
			tree := b.indexes[k]
			if tree == nil {
				tree = &btree.Map[string, Item]{}
				b.indexes[k] = tree
			}
			tree.Set(indexCompositeKey(m.indexHash, m.indexSort, m.baseHash, m.baseSort), m.item)
		case indexMutationDelete:
			if tree := b.indexes[k]; tree != nil {
				tree.Delete(indexCompositeKey(m.indexHash, m.indexSort, m.baseHash, m.baseSort))
			}
		}
	}
}

func (b *memItemBackend) applyIndexMutations(_ context.Context, tableName string, mutations []indexMutation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applyIndexMutationsLocked(tableName, mutations)
	return nil
}

func (b *memItemBackend) scanIndexAll(_ context.Context, tableName, indexName string) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.indexes[indexKey{table: tableName, index: indexName}]
	if tree == nil {
		return []Item{}, nil
	}
	items := make([]Item, 0, tree.Len())
	tree.Scan(func(_ string, item Item) bool {
		items = append(items, item)
		return true
	})
	return items, nil
}

func (b *memItemBackend) queryIndexByHash(_ context.Context, tableName, indexName, indexHash string) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.indexes[indexKey{table: tableName, index: indexName}]
	if tree == nil {
		return []Item{}, nil
	}
	prefix := indexHash + "\x00"
	var items []Item
	tree.Ascend(prefix, func(key string, item Item) bool {
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		items = append(items, item)
		return true
	})
	if items == nil {
		return []Item{}, nil
	}
	return items, nil
}

// scanIndexPage implements the itemBackend contract via a single Ascend seek
// to the cursor position in the (table, index) tree, then collects up to
// limit entries — the index-tree analogue of scanPage (see its doc comment
// for the seek technique; identical here, just against b.indexes instead of
// b.tables).
func (b *memItemBackend) scanIndexPage(_ context.Context, tableName, indexName string, hasAfter bool, afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort string, limit int) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.indexes[indexKey{table: tableName, index: indexName}]
	if tree == nil {
		return []Item{}, nil
	}

	afterKey := ""
	if hasAfter {
		afterKey = indexCompositeKey(afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort)
	}

	var items []Item
	tree.Ascend(afterKey, func(key string, item Item) bool {
		if hasAfter && key <= afterKey {
			return true // seeked to the cursor itself (or before it); keep advancing
		}
		if limit > 0 && len(items) >= limit {
			return false
		}
		items = append(items, item)
		return true
	})
	if items == nil {
		items = []Item{}
	}
	return items, nil
}

func (b *memItemBackend) countIndexEntries(_ context.Context, tableName, indexName string) (int64, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.indexes[indexKey{table: tableName, index: indexName}]
	if tree == nil {
		return 0, nil
	}
	return int64(tree.Len()), nil
}

func (b *memItemBackend) deleteAllIndexEntriesForTable(_ context.Context, tableName string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for k := range b.indexes {
		if k.table == tableName {
			delete(b.indexes, k)
		}
	}
	return nil
}

func (b *memItemBackend) deleteAllIndexEntriesForIndex(_ context.Context, tableName, indexName string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.indexes, indexKey{table: tableName, index: indexName})
	return nil
}

func (b *memItemBackend) queryByHash(_ context.Context, tableName, hashKey string) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.tables[tableName]
	if tree == nil {
		return []Item{}, nil
	}
	prefix := hashKey + "\x00"
	var items []Item
	tree.Ascend(prefix, func(key string, item Item) bool {
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		items = append(items, item)
		return true
	})
	if items == nil {
		return []Item{}, nil
	}
	return items, nil
}

func (b *memItemBackend) scanAll(_ context.Context, tableName string) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.tables[tableName]
	if tree == nil {
		return []Item{}, nil
	}
	items := make([]Item, 0, tree.Len())
	tree.Scan(func(_ string, item Item) bool {
		items = append(items, item)
		return true
	})
	return items, nil
}

// scanPage implements the itemBackend contract via a single Ascend seek to
// the cursor position, then collects up to limit items — see
// state.MemoryStore.ScanPage for the identical technique on the generic
// key/value store.
func (b *memItemBackend) scanPage(_ context.Context, tableName string, hasAfter bool, afterHash, afterSort string, limit int) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.tables[tableName]
	if tree == nil {
		return []Item{}, nil
	}

	afterKey := ""
	if hasAfter {
		afterKey = itemCompositeKey(afterHash, afterSort)
	}

	var items []Item
	tree.Ascend(afterKey, func(key string, item Item) bool {
		if hasAfter && key <= afterKey {
			return true // seeked to the cursor itself (or before it); keep advancing
		}
		if limit > 0 && len(items) >= limit {
			return false
		}
		items = append(items, item)
		return true
	})
	if items == nil {
		items = []Item{}
	}
	return items, nil
}

func (b *memItemBackend) count(_ context.Context, tableName string) (int64, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.tables[tableName]
	if tree == nil {
		return 0, nil
	}
	return int64(tree.Len()), nil
}

func (b *memItemBackend) deleteAll(_ context.Context, tableName string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.tables, tableName)
	return nil
}

func (b *memItemBackend) scanExpiredTTL(_ context.Context, tableName, ttlAttr string, cutoffUnix int64) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tree := b.tables[tableName]
	if tree == nil {
		return []Item{}, nil
	}
	var items []Item
	tree.Scan(func(_ string, item Item) bool {
		av, ok := item[ttlAttr]
		if !ok {
			return true
		}
		ts, ok := parseTTLValue(av)
		if !ok || ts == 0 || ts > cutoffUnix {
			return true
		}
		items = append(items, item)
		return true
	})
	if items == nil {
		return []Item{}, nil
	}
	return items, nil
}

func (b *memItemBackend) debugScan(_ context.Context) ([]debugItemRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var records []debugItemRecord
	for tableName, tree := range b.tables {
		tree.Scan(func(key string, item Item) bool {
			hashKey, sortKey := splitItemCompositeKey(key)
			records = append(records, debugItemRecord{TableName: tableName, HashKey: hashKey, SortKey: sortKey, Item: item})
			return true
		})
	}
	if records == nil {
		return []debugItemRecord{}, nil
	}
	return records, nil
}

func (b *memItemBackend) debugDeleteAll(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tables = make(map[string]*btree.Map[string, Item])
	b.indexes = make(map[indexKey]*btree.Map[string, Item])
	return nil
}

// splitItemCompositeKey is the inverse of itemCompositeKey: given
// "hashKey\x00sortKey" it returns ("hashKey", "sortKey"). Only ever called
// with keys this package created via itemCompositeKey.
func splitItemCompositeKey(composite string) (hashKey, sortKey string) {
	i := strings.IndexByte(composite, '\x00')
	if i < 0 {
		return composite, ""
	}
	return composite[:i], composite[i+1:]
}

// ---------------------------------------------------------------------------
// sqlItemBackend — dedicated SQLite table with a proper composite primary key
// ---------------------------------------------------------------------------
//
// Schema:
//
//	dynamodb_items (
//	    table_name  TEXT  NOT NULL,
//	    hash_key    TEXT  NOT NULL,
//	    sort_key    TEXT  NOT NULL DEFAULT '',
//	    item_json   TEXT  NOT NULL,
//	    PRIMARY KEY (table_name, hash_key, sort_key)
//	)
//
// The PRIMARY KEY B-tree makes these operations efficient:
//
//   - GetItem:       point lookup on all 3 key columns
//   - QueryByHash:   range scan on (table_name, hash_key) prefix
//   - ScanAll:       range scan on (table_name) prefix
//   - ScanPage:      row-value range scan `(hash_key, sort_key) > (?, ?)` on
//     the same PK, LIMIT-bounded (storage-access-plan.md A3;
//     "the model implementation" pattern from stream_store.go's GetRecords)
//   - DeleteAll:     table_name equality delete

type sqlItemBackend struct {
	dbFn func() *sql.DB
	db   *sql.DB
	once sync.Once
	err  error // set by init; sticky
}

// newSQLItemBackend returns a backend that lazily resolves the *sql.DB on
// first use. Deferring DB resolution avoids blocking startup when the
// underlying store opens SQLite asynchronously — dbFn (typically
// state.SQLiteDBProvider.DB) blocks until the store's background open and
// migration have completed, so by the time it returns here the
// dynamodb_items table already exists (created by the migration registered
// in migrations.go, storage-plan.md item 3.9) without this backend having to
// create it itself.
func newSQLItemBackend(dbFn func() *sql.DB) *sqlItemBackend {
	return &sqlItemBackend{dbFn: dbFn}
}

// init resolves b.db from dbFn exactly once. Schema setup for
// dynamodb_items happens via the migration runner (migrations.go), not
// here — see newSQLItemBackend's doc comment for why that ordering is safe.
func (b *sqlItemBackend) init() error {
	b.once.Do(func() {
		b.db = b.dbFn()
		if b.db == nil {
			b.err = fmt.Errorf("dynamodb: sqlite DB unavailable")
		}
	})
	return b.err
}

func (b *sqlItemBackend) put(ctx context.Context, tableName, hashKey, sortKey string, item Item) error {
	if err := b.init(); err != nil {
		return err
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("dynamodb put: marshal item: %w", err)
	}
	_, err = b.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO dynamodb_items (table_name, hash_key, sort_key, item_json)
		 VALUES (?, ?, ?, ?)`,
		tableName, hashKey, sortKey, string(raw),
	)
	if err != nil {
		return fmt.Errorf("dynamodb put [%s/%s/%s]: %w", tableName, hashKey, sortKey, err)
	}
	return nil
}

// putWithIndexMutations is put plus atomic GSI index-row maintenance
// (dynamodb-gsi-design.md §3 "Atomicity within existing boundaries"). When
// mutations is empty (the overwhelmingly common case — a table with no
// GSIs) this delegates straight to put with no *sql.Tx overhead at all,
// preserving today's zero-GSI write cost exactly. Only a table with at least
// one GSI pays for a transaction, and then only because the transaction is
// what makes the base-item write and every index-row write succeed or fail
// together — this codebase's first explicit multi-statement *sql.Tx for
// item writes (previously, all ExecContext calls here were single bare
// statements relying on SQLite's own single-writer serialization).
func (b *sqlItemBackend) putWithIndexMutations(ctx context.Context, tableName, hashKey, sortKey string, item Item, mutations []indexMutation) error {
	if len(mutations) == 0 {
		return b.put(ctx, tableName, hashKey, sortKey, item)
	}
	if err := b.init(); err != nil {
		return err
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("dynamodb put: marshal item: %w", err)
	}

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("dynamodb put [%s/%s/%s]: begin tx: %w", tableName, hashKey, sortKey, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed; only matters on the error paths below

	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO dynamodb_items (table_name, hash_key, sort_key, item_json)
		 VALUES (?, ?, ?, ?)`,
		tableName, hashKey, sortKey, string(raw),
	); err != nil {
		return fmt.Errorf("dynamodb put [%s/%s/%s]: %w", tableName, hashKey, sortKey, err)
	}
	if err := applyIndexMutationsTx(ctx, tx, tableName, mutations); err != nil {
		return fmt.Errorf("dynamodb put [%s/%s/%s]: %w", tableName, hashKey, sortKey, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("dynamodb put [%s/%s/%s]: commit: %w", tableName, hashKey, sortKey, err)
	}
	return nil
}

func (b *sqlItemBackend) get(ctx context.Context, tableName, hashKey, sortKey string) (Item, bool, error) {
	if err := b.init(); err != nil {
		return nil, false, err
	}
	var raw string
	err := b.db.QueryRowContext(ctx,
		`SELECT item_json FROM dynamodb_items
		 WHERE table_name = ? AND hash_key = ? AND sort_key = ?`,
		tableName, hashKey, sortKey,
	).Scan(&raw)

	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("dynamodb get [%s/%s/%s]: %w", tableName, hashKey, sortKey, err)
	}
	var item Item
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return nil, false, fmt.Errorf("dynamodb get: unmarshal: %w", err)
	}
	return item, true, nil
}

func (b *sqlItemBackend) remove(ctx context.Context, tableName, hashKey, sortKey string) error {
	if err := b.init(); err != nil {
		return err
	}
	_, err := b.db.ExecContext(ctx,
		`DELETE FROM dynamodb_items
		 WHERE table_name = ? AND hash_key = ? AND sort_key = ?`,
		tableName, hashKey, sortKey,
	)
	if err != nil {
		return fmt.Errorf("dynamodb delete [%s/%s/%s]: %w", tableName, hashKey, sortKey, err)
	}
	return nil
}

// removeWithIndexMutations is remove plus atomic GSI index-row maintenance —
// see putWithIndexMutations's doc comment for the zero-GSI fast path and the
// atomicity rationale, both of which apply identically here.
func (b *sqlItemBackend) removeWithIndexMutations(ctx context.Context, tableName, hashKey, sortKey string, mutations []indexMutation) error {
	if len(mutations) == 0 {
		return b.remove(ctx, tableName, hashKey, sortKey)
	}
	if err := b.init(); err != nil {
		return err
	}

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("dynamodb delete [%s/%s/%s]: begin tx: %w", tableName, hashKey, sortKey, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed; only matters on the error paths below

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM dynamodb_items
		 WHERE table_name = ? AND hash_key = ? AND sort_key = ?`,
		tableName, hashKey, sortKey,
	); err != nil {
		return fmt.Errorf("dynamodb delete [%s/%s/%s]: %w", tableName, hashKey, sortKey, err)
	}
	if err := applyIndexMutationsTx(ctx, tx, tableName, mutations); err != nil {
		return fmt.Errorf("dynamodb delete [%s/%s/%s]: %w", tableName, hashKey, sortKey, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("dynamodb delete [%s/%s/%s]: commit: %w", tableName, hashKey, sortKey, err)
	}
	return nil
}

// applyIndexMutationsTx applies index mutations within an already-open
// transaction — shared by putWithIndexMutations, removeWithIndexMutations,
// and applyIndexMutations (backfill, which opens its own single Tx for the
// whole batch rather than one per mutation).
func applyIndexMutationsTx(ctx context.Context, tx *sql.Tx, tableName string, mutations []indexMutation) error {
	for _, m := range mutations {
		switch m.op {
		case indexMutationUpsert:
			raw, err := json.Marshal(m.item)
			if err != nil {
				return fmt.Errorf("marshal index entry [%s/%s]: %w", tableName, m.indexName, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO dynamodb_index_entries
				 (table_name, index_name, index_hash, index_sort, base_hash, base_sort, item_json)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				tableName, m.indexName, m.indexHash, m.indexSort, m.baseHash, m.baseSort, string(raw),
			); err != nil {
				return fmt.Errorf("upsert index entry [%s/%s]: %w", tableName, m.indexName, err)
			}
		case indexMutationDelete:
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM dynamodb_index_entries
				 WHERE table_name = ? AND index_name = ? AND index_hash = ? AND index_sort = ? AND base_hash = ? AND base_sort = ?`,
				tableName, m.indexName, m.indexHash, m.indexSort, m.baseHash, m.baseSort,
			); err != nil {
				return fmt.Errorf("delete index entry [%s/%s]: %w", tableName, m.indexName, err)
			}
		}
	}
	return nil
}

func (b *sqlItemBackend) queryByHash(ctx context.Context, tableName, hashKey string) ([]Item, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT item_json FROM dynamodb_items
		 WHERE table_name = ? AND hash_key = ?
		 ORDER BY sort_key`,
		tableName, hashKey,
	)
	if err != nil {
		return nil, fmt.Errorf("dynamodb query [%s/%s]: %w", tableName, hashKey, err)
	}
	defer rows.Close()
	return scanItemRows(rows)
}

func (b *sqlItemBackend) scanAll(ctx context.Context, tableName string) ([]Item, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT item_json FROM dynamodb_items
		 WHERE table_name = ?
		 ORDER BY hash_key, sort_key`,
		tableName,
	)
	if err != nil {
		return nil, fmt.Errorf("dynamodb scan [%s]: %w", tableName, err)
	}
	defer rows.Close()
	return scanItemRows(rows)
}

// scanPage is the SQL half of storage-access-plan.md A3: a single indexed
// query bounded by LIMIT, using the existing (table_name, hash_key, sort_key)
// primary key — no new index, no full-table read. The row-value comparison
// `(hash_key, sort_key) > (?, ?)` is a positional predicate: it has no
// opinion on whether a row with exactly that key ever existed, which is
// exactly the semantic pagination-plan.md G2 wants (a deleted cursor item
// must not restart pagination from page 1).
func (b *sqlItemBackend) scanPage(ctx context.Context, tableName string, hasAfter bool, afterHash, afterSort string, limit int) ([]Item, error) {
	if err := b.init(); err != nil {
		return nil, err
	}

	var rows *sql.Rows
	var err error
	if hasAfter {
		rows, err = b.db.QueryContext(ctx,
			`SELECT item_json FROM dynamodb_items
			 WHERE table_name = ? AND (hash_key, sort_key) > (?, ?)
			 ORDER BY hash_key, sort_key
			 LIMIT ?`,
			tableName, afterHash, afterSort, limit,
		)
	} else {
		rows, err = b.db.QueryContext(ctx,
			`SELECT item_json FROM dynamodb_items
			 WHERE table_name = ?
			 ORDER BY hash_key, sort_key
			 LIMIT ?`,
			tableName, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("dynamodb scanPage [%s]: %w", tableName, err)
	}
	defer rows.Close()
	return scanItemRows(rows)
}

func (b *sqlItemBackend) count(ctx context.Context, tableName string) (int64, error) {
	if err := b.init(); err != nil {
		return 0, err
	}
	var n int64
	err := b.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dynamodb_items WHERE table_name = ?`,
		tableName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dynamodb count [%s]: %w", tableName, err)
	}
	return n, nil
}

func (b *sqlItemBackend) deleteAll(ctx context.Context, tableName string) error {
	if err := b.init(); err != nil {
		return err
	}
	_, err := b.db.ExecContext(ctx,
		`DELETE FROM dynamodb_items WHERE table_name = ?`,
		tableName,
	)
	if err != nil {
		return fmt.Errorf("dynamodb deleteAll [%s]: %w", tableName, err)
	}
	return nil
}

// applyIndexMutations applies a batch of index mutations in a single
// transaction, with no accompanying base-item write — used for GSI backfill
// (UpdateTable adding a GSI to a table that already has items). One Tx for
// the whole batch, not one per mutation, so a backfill either fully lands or
// fully rolls back.
func (b *sqlItemBackend) applyIndexMutations(ctx context.Context, tableName string, mutations []indexMutation) error {
	if len(mutations) == 0 {
		return nil
	}
	if err := b.init(); err != nil {
		return err
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("dynamodb applyIndexMutations [%s]: begin tx: %w", tableName, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if err := applyIndexMutationsTx(ctx, tx, tableName, mutations); err != nil {
		return fmt.Errorf("dynamodb applyIndexMutations [%s]: %w", tableName, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("dynamodb applyIndexMutations [%s]: commit: %w", tableName, err)
	}
	return nil
}

// scanIndexAll returns every entry for (table, index), ordered by the
// composite key (index_hash, index_sort, base_hash, base_sort) — the SQL
// analogue of memItemBackend's index-tree Scan.
func (b *sqlItemBackend) scanIndexAll(ctx context.Context, tableName, indexName string) ([]Item, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT item_json FROM dynamodb_index_entries
		 WHERE table_name = ? AND index_name = ?
		 ORDER BY index_hash, index_sort, base_hash, base_sort`,
		tableName, indexName,
	)
	if err != nil {
		return nil, fmt.Errorf("dynamodb scanIndexAll [%s/%s]: %w", tableName, indexName, err)
	}
	defer rows.Close()
	return scanItemRows(rows)
}

// queryIndexByHash returns entries for (table, index) sharing indexHash,
// ordered by (index_sort, base_hash, base_sort) — the row-value range scan
// shape §3 specifies for a GSI Query (Option A).
func (b *sqlItemBackend) queryIndexByHash(ctx context.Context, tableName, indexName, indexHash string) ([]Item, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT item_json FROM dynamodb_index_entries
		 WHERE table_name = ? AND index_name = ? AND index_hash = ?
		 ORDER BY index_sort, base_hash, base_sort`,
		tableName, indexName, indexHash,
	)
	if err != nil {
		return nil, fmt.Errorf("dynamodb queryIndexByHash [%s/%s]: %w", tableName, indexName, err)
	}
	defer rows.Close()
	return scanItemRows(rows)
}

// scanIndexPage is the SQL half of scanIndexPage: a single indexed
// row-value-range query bounded by LIMIT against dynamodb_index_entries'
// primary key, mirroring scanPage's shape against dynamodb_items (see its
// doc comment) but walking the 4-column composite key instead of 2.
func (b *sqlItemBackend) scanIndexPage(ctx context.Context, tableName, indexName string, hasAfter bool, afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort string, limit int) ([]Item, error) {
	if err := b.init(); err != nil {
		return nil, err
	}

	var rows *sql.Rows
	var err error
	if hasAfter {
		rows, err = b.db.QueryContext(ctx,
			`SELECT item_json FROM dynamodb_index_entries
			 WHERE table_name = ? AND index_name = ?
			   AND (index_hash, index_sort, base_hash, base_sort) > (?, ?, ?, ?)
			 ORDER BY index_hash, index_sort, base_hash, base_sort
			 LIMIT ?`,
			tableName, indexName, afterIndexHash, afterIndexSort, afterBaseHash, afterBaseSort, limit,
		)
	} else {
		rows, err = b.db.QueryContext(ctx,
			`SELECT item_json FROM dynamodb_index_entries
			 WHERE table_name = ? AND index_name = ?
			 ORDER BY index_hash, index_sort, base_hash, base_sort
			 LIMIT ?`,
			tableName, indexName, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("dynamodb scanIndexPage [%s/%s]: %w", tableName, indexName, err)
	}
	defer rows.Close()
	return scanItemRows(rows)
}

func (b *sqlItemBackend) countIndexEntries(ctx context.Context, tableName, indexName string) (int64, error) {
	if err := b.init(); err != nil {
		return 0, err
	}
	var n int64
	err := b.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dynamodb_index_entries WHERE table_name = ? AND index_name = ?`,
		tableName, indexName,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("dynamodb countIndexEntries [%s/%s]: %w", tableName, indexName, err)
	}
	return n, nil
}

func (b *sqlItemBackend) deleteAllIndexEntriesForTable(ctx context.Context, tableName string) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM dynamodb_index_entries WHERE table_name = ?`,
		tableName,
	); err != nil {
		return fmt.Errorf("dynamodb deleteAllIndexEntriesForTable [%s]: %w", tableName, err)
	}
	return nil
}

func (b *sqlItemBackend) deleteAllIndexEntriesForIndex(ctx context.Context, tableName, indexName string) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM dynamodb_index_entries WHERE table_name = ? AND index_name = ?`,
		tableName, indexName,
	); err != nil {
		return fmt.Errorf("dynamodb deleteAllIndexEntriesForIndex [%s/%s]: %w", tableName, indexName, err)
	}
	return nil
}

func (b *sqlItemBackend) scanExpiredTTL(ctx context.Context, tableName, ttlAttr string, cutoffUnix int64) ([]Item, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	// Use json_extract to filter in SQLite — only matching rows are
	// deserialised, making this O(expired) instead of O(all items).
	rows, err := b.db.QueryContext(ctx,
		`SELECT item_json FROM dynamodb_items
		 WHERE table_name = ?
		   AND json_extract(item_json, '$.' || ? || '.N') IS NOT NULL
		   AND CAST(json_extract(item_json, '$.' || ? || '.N') AS INTEGER) > 0
		   AND CAST(json_extract(item_json, '$.' || ? || '.N') AS INTEGER) <= ?`,
		tableName, ttlAttr, ttlAttr, ttlAttr, cutoffUnix,
	)
	if err != nil {
		return nil, fmt.Errorf("dynamodb scanExpiredTTL [%s]: %w", tableName, err)
	}
	defer rows.Close()
	return scanItemRows(rows)
}

// scanItemRows decodes a result set of (item_json) rows into Items.
func scanItemRows(rows *sql.Rows) ([]Item, error) {
	var items []Item
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("dynamodb: scan row: %w", err)
		}
		var item Item
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("dynamodb: unmarshal item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dynamodb: rows error: %w", err)
	}
	if items == nil {
		return []Item{}, nil
	}
	return items, nil
}

func (b *sqlItemBackend) debugScan(ctx context.Context) ([]debugItemRecord, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT table_name, hash_key, sort_key, item_json FROM dynamodb_items ORDER BY table_name, hash_key, sort_key`,
	)
	if err != nil {
		return nil, fmt.Errorf("dynamodb items debug scan: %w", err)
	}
	defer rows.Close()

	var records []debugItemRecord
	for rows.Next() {
		var record debugItemRecord
		var raw string
		if err := rows.Scan(&record.TableName, &record.HashKey, &record.SortKey, &raw); err != nil {
			return nil, fmt.Errorf("dynamodb items debug scan row: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &record.Item); err != nil {
			return nil, fmt.Errorf("dynamodb items debug decode: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dynamodb items debug scan rows: %w", err)
	}
	if records == nil {
		return []debugItemRecord{}, nil
	}
	return records, nil
}

func (b *sqlItemBackend) debugDeleteAll(ctx context.Context) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx, `DELETE FROM dynamodb_items`); err != nil {
		return fmt.Errorf("dynamodb items debug delete all: %w", err)
	}
	return nil
}
