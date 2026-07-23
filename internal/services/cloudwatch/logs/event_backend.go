package logs

// eventBackend is the CloudWatch Logs-specific storage layer for events.
//
// Events are indexed directly by (region, group, stream, ts, seq) — mirroring
// the logs_events SQL table's primary key — which gives:
//
//   - appendEvents: O(1) amortized per event — a batched INSERT, never a
//     rewrite of prior events (unlike the old one-blob-per-stream design).
//   - getEvents:    an indexed ORDER BY range scan on (region, group, stream),
//     already sorted — no in-process re-sort needed.
//   - deleteStream: an indexed ranged DELETE.
//   - deleteGroup:  an indexed ranged DELETE across every stream in the group
//     (uses the same (region, group_name, ts) index FilterLogEvents would
//     need for a future group-wide scan — see migrations.go).
//
// Two implementations are provided, mirroring
// internal/services/dynamodb/item_store.go's itemBackend split:
//
//	memEventBackend — in-process map of region/group/stream → []LogEvent
//	sqlEventBackend — SQLite logs_events table (state.SQLiteDBProvider)
//
// The appropriate backend is chosen at startup based on the state.Store type
// (see newEventBackendFor, called from newLogsStore after state.Unwrap).

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Neaox/overcast/internal/state"
)

// eventBackend is the interface every CloudWatch Logs event store must
// implement. All methods are region-scoped since the same group/stream name
// can exist independently in multiple regions.
type eventBackend interface {
	// appendEvents persists events for one stream as a single batched write.
	// events is assumed already sorted ascending by Timestamp (the caller,
	// logsStore's eventCache, maintains that invariant).
	appendEvents(ctx context.Context, region, group, stream string, events []LogEvent) error

	// getEvents returns every persisted event for a stream, sorted ascending
	// by Timestamp. Returns an empty (non-nil) slice when the stream has no
	// persisted events.
	getEvents(ctx context.Context, region, group, stream string) ([]LogEvent, error)

	// deleteStream removes every persisted event for one stream.
	deleteStream(ctx context.Context, region, group, stream string) error

	// deleteGroup removes every persisted event for every stream in a group.
	deleteGroup(ctx context.Context, region, group string) error

	// debugScan returns up to limit raw event rows for
	// /_debug/state/logs:events, ordered deterministically. When limit <= 0,
	// scan is unbounded (used by tests; callers serving HTTP responses
	// should always pass a positive limit — see Service.DebugStateKeys). The
	// second return value reports whether more rows exist beyond limit.
	debugScan(ctx context.Context, limit int) (records []debugEventRecord, truncated bool, err error)

	// debugDeleteAll removes every persisted event, for /_debug/reset.
	debugDeleteAll(ctx context.Context) error
}

// debugEventRecord is one row surfaced by debugScan.
type debugEventRecord struct {
	Region    string
	Group     string
	Stream    string
	Timestamp int64
	Seq       int64
	Message   string
}

// ---------------------------------------------------------------------------
// memEventBackend — zero-serialisation in-process store (memory-mode parity)
// ---------------------------------------------------------------------------

type memEventBackend struct {
	mu      sync.RWMutex
	streams map[string][]LogEvent // key: memEventKey(region, group, stream)
}

func newMemEventBackend() *memEventBackend {
	return &memEventBackend{streams: make(map[string][]LogEvent)}
}

// memEventKey builds an opaque, collision-free map key for one stream. Uses
// NUL as a separator (never a legal character in AWS resource names) so
// group/stream names containing "/" can't collide with each other — nothing
// ever parses this key back apart, unlike migrations.go's kv-key handling.
func memEventKey(region, group, stream string) string {
	return region + "\x00" + group + "\x00" + stream
}

func splitMemEventKey(key string) (region, group, stream string) {
	parts := strings.SplitN(key, "\x00", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

func (b *memEventBackend) appendEvents(_ context.Context, region, group, stream string, events []LogEvent) error {
	if len(events) == 0 {
		return nil
	}
	key := memEventKey(region, group, stream)
	b.mu.Lock()
	defer b.mu.Unlock()
	merged := append(b.streams[key], events...)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Timestamp < merged[j].Timestamp })
	b.streams[key] = merged
	return nil
}

func (b *memEventBackend) getEvents(_ context.Context, region, group, stream string) ([]LogEvent, error) {
	key := memEventKey(region, group, stream)
	b.mu.RLock()
	defer b.mu.RUnlock()
	src := b.streams[key]
	out := make([]LogEvent, len(src))
	copy(out, src)
	return out, nil
}

func (b *memEventBackend) deleteStream(_ context.Context, region, group, stream string) error {
	key := memEventKey(region, group, stream)
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.streams, key)
	return nil
}

func (b *memEventBackend) deleteGroup(_ context.Context, region, group string) error {
	prefix := region + "\x00" + group + "\x00"
	b.mu.Lock()
	defer b.mu.Unlock()
	for k := range b.streams {
		if strings.HasPrefix(k, prefix) {
			delete(b.streams, k)
		}
	}
	return nil
}

func (b *memEventBackend) debugScan(_ context.Context, limit int) ([]debugEventRecord, bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	keys := make([]string, 0, len(b.streams))
	for k := range b.streams {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var records []debugEventRecord
	truncated := false
outer:
	for _, k := range keys {
		region, group, stream := splitMemEventKey(k)
		for seq, e := range b.streams[k] {
			if limit > 0 && len(records) >= limit {
				truncated = true
				break outer
			}
			records = append(records, debugEventRecord{
				Region: region, Group: group, Stream: stream,
				Timestamp: e.Timestamp, Seq: int64(seq), Message: e.Message,
			})
		}
	}
	return records, truncated, nil
}

func (b *memEventBackend) debugDeleteAll(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.streams = make(map[string][]LogEvent)
	return nil
}

// ---------------------------------------------------------------------------
// sqlEventBackend — dedicated logs_events SQLite table
// ---------------------------------------------------------------------------
//
// Schema is created by a registered migration (migrations.go), not here —
// see storage-plan.md item 2.3 and internal/state/migrate.go's Migration doc
// comment. By the time dbFn() returns a non-nil *sql.DB, the migration
// runner has already run (state.SQLiteDBProvider.DB() blocks on it — see
// SQLiteStore.DB / HybridStore.DB), so logs_events is guaranteed to exist.

type sqlEventBackend struct {
	dbFn func() *sql.DB
	db   *sql.DB
	once sync.Once
	err  error // set by init; sticky
}

// newSQLEventBackend returns a backend that lazily resolves the *sql.DB on
// first use. Deferring DB resolution avoids blocking startup when the
// underlying store opens SQLite asynchronously.
func newSQLEventBackend(dbFn func() *sql.DB) *sqlEventBackend {
	return &sqlEventBackend{dbFn: dbFn}
}

func (b *sqlEventBackend) init() error {
	b.once.Do(func() {
		b.db = b.dbFn()
		if b.db == nil {
			b.err = fmt.Errorf("cloudwatch logs: sqlite DB unavailable")
		}
	})
	return b.err
}

func (b *sqlEventBackend) appendEvents(ctx context.Context, region, group, stream string, events []LogEvent) error {
	if len(events) == 0 {
		return nil
	}
	if err := b.init(); err != nil {
		return err
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cloudwatch logs append events [%s/%s/%s]: begin tx: %w", region, group, stream, err)
	}
	defer tx.Rollback() //nolint:errcheck // best-effort; no-op after a successful Commit.

	// seq must be unique (and ideally insertion-ordered) within
	// (region, group, stream, ts). A per-stream monotonic counter, seeded
	// from the current max, is cheap (one indexed scalar lookup) compared to
	// the old design's full-history rewrite, and correct regardless of
	// whether this stream pre-existed (from the migration or an earlier
	// process) or is brand new.
	var nextSeq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq), -1) + 1 FROM logs_events WHERE region = ? AND group_name = ? AND stream_name = ?`,
		region, group, stream,
	).Scan(&nextSeq); err != nil {
		return fmt.Errorf("cloudwatch logs append events [%s/%s/%s]: next seq: %w", region, group, stream, err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO logs_events (region, group_name, stream_name, ts, seq, ingestion_ts, message)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("cloudwatch logs append events [%s/%s/%s]: prepare: %w", region, group, stream, err)
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.ExecContext(ctx, region, group, stream, e.Timestamp, nextSeq, e.IngestionTime, e.Message); err != nil {
			return fmt.Errorf("cloudwatch logs append event [%s/%s/%s]: %w", region, group, stream, err)
		}
		nextSeq++
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cloudwatch logs append events [%s/%s/%s]: commit: %w", region, group, stream, err)
	}
	return nil
}

func (b *sqlEventBackend) getEvents(ctx context.Context, region, group, stream string) ([]LogEvent, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx, `
		SELECT ts, ingestion_ts, message FROM logs_events
		WHERE region = ? AND group_name = ? AND stream_name = ?
		ORDER BY ts, seq
	`, region, group, stream)
	if err != nil {
		return nil, fmt.Errorf("cloudwatch logs get events [%s/%s/%s]: %w", region, group, stream, err)
	}
	defer rows.Close()

	var events []LogEvent
	for rows.Next() {
		var e LogEvent
		if err := rows.Scan(&e.Timestamp, &e.IngestionTime, &e.Message); err != nil {
			return nil, fmt.Errorf("cloudwatch logs get events [%s/%s/%s]: scan: %w", region, group, stream, err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudwatch logs get events [%s/%s/%s]: rows: %w", region, group, stream, err)
	}
	if events == nil {
		return []LogEvent{}, nil
	}
	return events, nil
}

func (b *sqlEventBackend) deleteStream(ctx context.Context, region, group, stream string) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM logs_events WHERE region = ? AND group_name = ? AND stream_name = ?`,
		region, group, stream,
	); err != nil {
		return fmt.Errorf("cloudwatch logs delete stream events [%s/%s/%s]: %w", region, group, stream, err)
	}
	return nil
}

func (b *sqlEventBackend) deleteGroup(ctx context.Context, region, group string) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM logs_events WHERE region = ? AND group_name = ?`,
		region, group,
	); err != nil {
		return fmt.Errorf("cloudwatch logs delete group events [%s/%s]: %w", region, group, err)
	}
	return nil
}

func (b *sqlEventBackend) debugScan(ctx context.Context, limit int) ([]debugEventRecord, bool, error) {
	if err := b.init(); err != nil {
		return nil, false, err
	}
	query := `SELECT region, group_name, stream_name, ts, seq, message FROM logs_events
		ORDER BY region, group_name, stream_name, ts, seq`
	args := []any{}
	if limit > 0 {
		// Fetch one extra row to detect truncation without a separate COUNT(*).
		query += ` LIMIT ?`
		args = append(args, limit+1)
	}
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("cloudwatch logs debug scan: %w", err)
	}
	defer rows.Close()

	var records []debugEventRecord
	for rows.Next() {
		var r debugEventRecord
		if err := rows.Scan(&r.Region, &r.Group, &r.Stream, &r.Timestamp, &r.Seq, &r.Message); err != nil {
			return nil, false, fmt.Errorf("cloudwatch logs debug scan: row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("cloudwatch logs debug scan: rows: %w", err)
	}
	truncated := false
	if limit > 0 && len(records) > limit {
		records = records[:limit]
		truncated = true
	}
	return records, truncated, nil
}

func (b *sqlEventBackend) debugDeleteAll(ctx context.Context) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx, `DELETE FROM logs_events`); err != nil {
		return fmt.Errorf("cloudwatch logs debug delete all: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Backend selection
// ---------------------------------------------------------------------------

// newEventBackendFor selects the right eventBackend based on the store type:
//   - SQLiteDBProvider → sqlEventBackend (dedicated indexed table in the same DB file)
//   - anything else    → memEventBackend (in-process map, memory-mode parity)
//
// Callers must pass a store already resolved with state.Unwrap (see
// newLogsStore) — a *state.NamespacedStore never implements SQLiteDBProvider
// itself, so passing one through unresolved always falls back to the memory
// backend (the same interface-erasure hazard state.Unwrap exists to guard
// against — see internal/services/dynamodb/service.go's newItemBackendFor).
func newEventBackendFor(store state.Store) eventBackend {
	if provider, ok := store.(state.SQLiteDBProvider); ok {
		return newSQLEventBackend(provider.DB)
	}
	return newMemEventBackend()
}
