package dynamodb

// itemBackend is the DynamoDB-specific storage layer for items.
//
// Items are indexed directly by (tableName, hashKey, sortKey) — mirroring
// DynamoDB's actual storage model — which gives:
//
//   - GetItem:        O(1) / O(log n) — single map lookup or indexed SQL row read
//   - Query by hash:  O(k) — loads only the items in one partition
//   - Full Scan:      O(n) — always a full table scan (unavoidable)
//   - DeleteItem:     O(1) / O(log n) — single map delete or indexed SQL delete
//
// Two implementations are provided:
//
//   memItemBackend  — in-process nested maps, zero JSON serialisation overhead
//   sqlItemBackend  — SQLite table with a (table_name, hash_key, sort_key) primary key
//
// The appropriate backend is chosen at startup based on the state.Store type.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
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
}

type debugItemRecord struct {
	TableName string
	HashKey   string
	SortKey   string
	Item      Item
}

// ---------------------------------------------------------------------------
// memItemBackend — zero-serialisation in-process store
// ---------------------------------------------------------------------------
//
// Data layout:
//
//	tables[tableName][hashKey][sortKey] = Item
//
// A single RWMutex protects the whole store. Per-table locking would improve
// throughput under concurrent multi-table workloads, but the emulator's target
// use case (one dev/CI process) doesn't justify the added complexity.

type memItemBackend struct {
	mu     sync.RWMutex
	tables map[string]map[string]map[string]Item
}

func newMemItemBackend() *memItemBackend {
	return &memItemBackend{tables: make(map[string]map[string]map[string]Item)}
}

func (b *memItemBackend) put(_ context.Context, tableName, hashKey, sortKey string, item Item) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.tables[tableName] == nil {
		b.tables[tableName] = make(map[string]map[string]Item)
	}
	if b.tables[tableName][hashKey] == nil {
		b.tables[tableName][hashKey] = make(map[string]Item)
	}
	b.tables[tableName][hashKey][sortKey] = item
	return nil
}

func (b *memItemBackend) get(_ context.Context, tableName, hashKey, sortKey string) (Item, bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	partition := b.tables[tableName][hashKey]
	if partition == nil {
		return nil, false, nil
	}
	item, ok := partition[sortKey]
	return item, ok, nil
}

func (b *memItemBackend) remove(_ context.Context, tableName, hashKey, sortKey string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	partition := b.tables[tableName][hashKey]
	if partition == nil {
		return nil
	}
	delete(partition, sortKey)
	if len(partition) == 0 {
		delete(b.tables[tableName], hashKey)
	}
	return nil
}

func (b *memItemBackend) queryByHash(_ context.Context, tableName, hashKey string) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	partition := b.tables[tableName][hashKey]
	if len(partition) == 0 {
		return []Item{}, nil
	}
	items := make([]Item, 0, len(partition))
	for _, item := range partition {
		items = append(items, item)
	}
	return items, nil
}

func (b *memItemBackend) scanAll(_ context.Context, tableName string) ([]Item, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	table := b.tables[tableName]
	if len(table) == 0 {
		return []Item{}, nil
	}
	var items []Item
	for _, partition := range table {
		for _, item := range partition {
			items = append(items, item)
		}
	}
	return items, nil
}

func (b *memItemBackend) count(_ context.Context, tableName string) (int64, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var n int64
	for _, partition := range b.tables[tableName] {
		n += int64(len(partition))
	}
	return n, nil
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

	table := b.tables[tableName]
	if len(table) == 0 {
		return []Item{}, nil
	}
	var items []Item
	for _, partition := range table {
		for _, item := range partition {
			av, ok := item[ttlAttr]
			if !ok {
				continue
			}
			ts, ok := parseTTLValue(av)
			if !ok || ts == 0 || ts > cutoffUnix {
				continue
			}
			items = append(items, item)
		}
	}
	if items == nil {
		return []Item{}, nil
	}
	return items, nil
}

func (b *memItemBackend) debugScan(_ context.Context) ([]debugItemRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var records []debugItemRecord
	for tableName, table := range b.tables {
		for hashKey, partition := range table {
			for sortKey, item := range partition {
				records = append(records, debugItemRecord{TableName: tableName, HashKey: hashKey, SortKey: sortKey, Item: item})
			}
		}
	}
	if records == nil {
		return []debugItemRecord{}, nil
	}
	return records, nil
}

func (b *memItemBackend) debugDeleteAll(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tables = make(map[string]map[string]map[string]Item)
	return nil
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
//   - DeleteAll:     table_name equality delete

type sqlItemBackend struct {
	dbFn func() *sql.DB
	db   *sql.DB
	once sync.Once
	err  error // set by init; sticky
}

// newSQLItemBackend returns a backend that lazily resolves the *sql.DB and
// creates the items table on first use. Deferring DB resolution avoids
// blocking startup when the underlying store opens SQLite asynchronously.
func newSQLItemBackend(dbFn func() *sql.DB) *sqlItemBackend {
	return &sqlItemBackend{dbFn: dbFn}
}

func (b *sqlItemBackend) init() error {
	b.once.Do(func() {
		b.db = b.dbFn()
		if b.db == nil {
			b.err = fmt.Errorf("dynamodb: sqlite DB unavailable")
			return
		}
		const schema = `
		CREATE TABLE IF NOT EXISTS dynamodb_items (
			table_name  TEXT NOT NULL,
			hash_key    TEXT NOT NULL,
			sort_key    TEXT NOT NULL DEFAULT '',
			item_json   TEXT NOT NULL,
			PRIMARY KEY (table_name, hash_key, sort_key)
		);`
		if _, err := b.db.Exec(schema); err != nil {
			b.err = fmt.Errorf("dynamodb: migrate items table: %w", err)
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
