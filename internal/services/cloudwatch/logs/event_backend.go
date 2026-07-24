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
	"time"

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

	// deleteEventsOlderThan removes every persisted event for every stream in
	// a group whose Timestamp is strictly before cutoff. Group-scoped rather
	// than per-stream: the logs_events table's (region, group_name, ts) index
	// (see migrations.go) makes a group-wide ranged delete just as cheap as a
	// single-stream one, so there is no benefit to iterating streams
	// individually — see storage-plan.md 3.4 (RetentionInDays enforcement).
	deleteEventsOlderThan(ctx context.Context, region, group string, cutoff time.Time) error

	// debugScan returns up to limit raw event rows for
	// /_debug/state/logs:events, ordered deterministically. When limit <= 0,
	// scan is unbounded (used by tests; callers serving HTTP responses
	// should always pass a positive limit — see Service.DebugStateKeys). The
	// second return value reports whether more rows exist beyond limit.
	debugScan(ctx context.Context, limit int) (records []debugEventRecord, truncated bool, err error)

	// debugDeleteAll removes every persisted event, for /_debug/reset.
	debugDeleteAll(ctx context.Context) error

	// getEventsRange returns up to limit persisted events for one stream
	// within the inclusive window [startTs, endTs], ordered by the stream's
	// total (Timestamp, Seq) order — the same order the PRIMARY KEY/
	// idx_logs_events_group index already sorts by (see migrations.go).
	//
	// forward=true selects the EARLIEST matching events: results ascending,
	// starting strictly after `after` (when after.Valid) or from startTs
	// otherwise.
	// forward=false selects the LATEST matching events: results are still
	// returned in ascending order (AWS always returns events chronologically
	// regardless of paging direction), but the *selection* works backward
	// from endTs — strictly before `after` (when after.Valid) or up to
	// endTs otherwise.
	//
	// Implements the storage half of GetLogEvents' forward/backward paging
	// (docs/plans/pagination-plan.md G1; docs/plans/storage-access-plan.md
	// A4 — P2 structural pushdown). The full semantics additionally merge in
	// the stream's unflushed write buffer — see logsStore.getEventsRangeMerged
	// (P4 — overlay/buffer merge), not this method.
	getEventsRange(ctx context.Context, region, group, stream string, startTs, endTs int64, after eventCursor, limit int, forward bool) ([]RangedEvent, error)

	// getGroupEventsRange returns up to limit persisted events across every
	// stream in (region, group) whose name has streamPrefix (empty =
	// every stream), within the inclusive window [startTs, endTs], ordered
	// ascending by the group's total (Timestamp, StreamName, Seq) order,
	// starting strictly after `after` (when after.Valid) or from startTs
	// otherwise.
	//
	// This is what turns FilterLogEvents into ONE group-range query instead
	// of N per-stream full reads (docs/plans/storage-access-plan.md A4):
	// filter-pattern matching, arbitrary stream-name-set selection (as
	// opposed to a simple prefix), interleaving, and searchedLogStreams
	// shaping all stay in the handler (behavioral, per the fidelity
	// principle) — this method only pushes down the structural time-window
	// + limit predicate using idx_logs_events_group.
	getGroupEventsRange(ctx context.Context, region, group, streamPrefix string, startTs, endTs int64, after groupCursor, limit int) ([]GroupRangedEvent, error)
}

// RangedEvent is one persisted event returned by getEventsRange, tagged with
// its per-(region,group,stream) monotonic sequence number — the same
// tiebreaker sqlEventBackend already assigns at append time (the `seq`
// column) and memEventBackend now assigns too (see memStoredEvent). Callers
// building resumable cursors (GetLogEvents' f/·b/· tokens) need this to
// resume deterministically past events that share a millisecond timestamp,
// which is common under bursty writers (Lambda log batching, etc).
type RangedEvent struct {
	LogEvent
	Seq int64
}

// GroupRangedEvent is one persisted event returned by getGroupEventsRange,
// additionally tagged with the stream it came from — a single getEventsRange
// call already implies the stream, but a group-wide query spans many.
type GroupRangedEvent struct {
	LogEvent
	Seq        int64
	StreamName string
}

// eventCursor is an exclusive resume position within one stream's
// (Timestamp, Seq) total order, used by getEventsRange. Valid is false for
// "no cursor" — start from the window's natural edge (the beginning when
// reading forward, the end when reading backward).
type eventCursor struct {
	Valid     bool
	Timestamp int64
	Seq       int64
}

// groupCursor is eventCursor's group-wide analogue: a group-wide range read
// orders by (Timestamp, StreamName, Seq), so resuming past a tie needs the
// stream name too.
type groupCursor struct {
	Valid      bool
	Timestamp  int64
	StreamName string
	Seq        int64
}

// cursorAllows reports whether (ts, seq) lies on the far side of `after` in
// the direction implied by forward — i.e. whether it should be included in
// a getEventsRange/getGroupEventsRange result page continuing from that
// cursor. A zero-value (Valid == false) cursor allows everything (no
// resume position yet).
func cursorAllows(ts, seq int64, after eventCursor, forward bool) bool {
	if !after.Valid {
		return true
	}
	if forward {
		if ts != after.Timestamp {
			return ts > after.Timestamp
		}
		return seq > after.Seq
	}
	if ts != after.Timestamp {
		return ts < after.Timestamp
	}
	return seq < after.Seq
}

// groupCursorAllows is cursorAllows' group-wide analogue: ties are broken by
// (StreamName, Seq) instead of Seq alone, matching getGroupEventsRange's
// documented (Timestamp, StreamName, Seq) total order. Group-wide range
// reads are always forward-only (FilterLogEvents has no backward-paging
// concept), so there is no `forward` parameter to thread through.
func groupCursorAllows(ts int64, streamName string, seq int64, after groupCursor) bool {
	if !after.Valid {
		return true
	}
	if ts != after.Timestamp {
		return ts > after.Timestamp
	}
	if streamName != after.StreamName {
		return streamName > after.StreamName
	}
	return seq > after.Seq
}

// streamPrefixUpperBound returns the exclusive upper bound for a prefix
// range query — mirrors internal/state's identically-behaved (but
// unexported, and off-limits: this package must consume internal/state
// frozen) prefixUpperBound. Duplicated locally rather than promoted since
// only one consumer exists here (rule of two, storage-access-plan.md) and
// the two packages must not couple over an unexported helper.
// For example, "app/" -> "app0" (the byte after '/' is '0').
// Returns "" if no upper bound exists (all bytes are 0xFF).
func streamPrefixUpperBound(prefix string) string {
	if prefix == "" {
		return ""
	}
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xFF {
			b[i]++
			return string(b[:i+1])
		}
	}
	return ""
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

// memStoredEvent pairs a LogEvent with its per-stream monotonic sequence
// number, assigned once at append time and never reassigned — mirrors
// sqlEventBackend's `seq` column exactly, including the A1-style lesson that
// the counter must never be derived from len(existing) (retention deletes
// would then make a fresh seq collide with a surviving one).
type memStoredEvent struct {
	LogEvent
	seq int64
}

type memEventBackend struct {
	mu      sync.RWMutex
	streams map[string][]memStoredEvent // key: memEventKey(region, group, stream); kept sorted ascending by (Timestamp, seq)
	nextSeq map[string]int64            // key: memEventKey(region, group, stream); never derived from len(streams[key])
}

func newMemEventBackend() *memEventBackend {
	return &memEventBackend{
		streams: make(map[string][]memStoredEvent),
		nextSeq: make(map[string]int64),
	}
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
	seq := b.nextSeq[key]
	stored := make([]memStoredEvent, len(events))
	for i, e := range events {
		stored[i] = memStoredEvent{LogEvent: e, seq: seq}
		seq++
	}
	b.nextSeq[key] = seq
	merged := append(b.streams[key], stored...)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Timestamp != merged[j].Timestamp {
			return merged[i].Timestamp < merged[j].Timestamp
		}
		return merged[i].seq < merged[j].seq
	})
	b.streams[key] = merged
	return nil
}

func (b *memEventBackend) getEvents(_ context.Context, region, group, stream string) ([]LogEvent, error) {
	key := memEventKey(region, group, stream)
	b.mu.RLock()
	defer b.mu.RUnlock()
	src := b.streams[key]
	out := make([]LogEvent, len(src))
	for i, e := range src {
		out[i] = e.LogEvent
	}
	return out, nil
}

// getEventsRange implements the eventBackend interface method of the same
// name (see its doc comment there) against the in-process map. b.streams'
// per-stream slice is already sorted ascending by (Timestamp, seq), so both
// directions are a single linear scan with early termination.
func (b *memEventBackend) getEventsRange(_ context.Context, region, group, stream string, startTs, endTs int64, after eventCursor, limit int, forward bool) ([]RangedEvent, error) {
	key := memEventKey(region, group, stream)
	b.mu.RLock()
	defer b.mu.RUnlock()
	src := b.streams[key]

	if forward {
		out := make([]RangedEvent, 0)
		for _, e := range src {
			if e.Timestamp > endTs {
				break
			}
			if e.Timestamp < startTs {
				continue
			}
			if !cursorAllows(e.Timestamp, e.seq, after, true) {
				continue
			}
			out = append(out, RangedEvent{LogEvent: e.LogEvent, Seq: e.seq})
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return out, nil
	}

	// Backward: walk from the end, collecting the LAST `limit` matches, then
	// reverse to ascending order — AWS always returns events chronologically
	// ascending regardless of paging direction.
	var rev []RangedEvent
	for i := len(src) - 1; i >= 0; i-- {
		e := src[i]
		if e.Timestamp < startTs {
			break
		}
		if e.Timestamp > endTs {
			continue
		}
		if !cursorAllows(e.Timestamp, e.seq, after, false) {
			continue
		}
		rev = append(rev, RangedEvent{LogEvent: e.LogEvent, Seq: e.seq})
		if limit > 0 && len(rev) >= limit {
			break
		}
	}
	out := make([]RangedEvent, len(rev))
	for i, e := range rev {
		out[len(rev)-1-i] = e
	}
	return out, nil
}

// getGroupEventsRange implements the eventBackend interface method of the
// same name (see its doc comment there) against the in-process map: a
// k-way merge of every matching stream's already-sorted slice, each
// pre-positioned (via a starting index resolved once up front) past
// startTs/the cursor, so the merge loop itself never re-examines an
// already-excluded event.
func (b *memEventBackend) getGroupEventsRange(_ context.Context, region, group, streamPrefix string, startTs, endTs int64, after groupCursor, limit int) ([]GroupRangedEvent, error) {
	prefix := region + "\x00" + group + "\x00"
	b.mu.RLock()
	defer b.mu.RUnlock()

	type streamCursor struct {
		name   string
		events []memStoredEvent
		idx    int
	}
	var streams []streamCursor
	for key, events := range b.streams {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		_, _, streamName := splitMemEventKey(key)
		if streamPrefix != "" && !strings.HasPrefix(streamName, streamPrefix) {
			continue
		}
		// Position idx at the first event satisfying startTs/the cursor —
		// binary search on Timestamp>=startTs (monotonic since ascending),
		// then a short linear advance past any tied-at-cursor entries.
		startIdx := sort.Search(len(events), func(i int) bool { return events[i].Timestamp >= startTs })
		for startIdx < len(events) && !groupCursorAllows(events[startIdx].Timestamp, streamName, events[startIdx].seq, after) {
			startIdx++
		}
		streams = append(streams, streamCursor{name: streamName, events: events, idx: startIdx})
	}

	out := make([]GroupRangedEvent, 0)
	for {
		best := -1
		for si := range streams {
			cs := &streams[si]
			if cs.idx >= len(cs.events) || cs.events[cs.idx].Timestamp > endTs {
				continue
			}
			if best == -1 {
				best = si
				continue
			}
			a, bcur := cs.events[cs.idx], streams[best].events[streams[best].idx]
			if a.Timestamp != bcur.Timestamp {
				if a.Timestamp < bcur.Timestamp {
					best = si
				}
				continue
			}
			if cs.name != streams[best].name {
				if cs.name < streams[best].name {
					best = si
				}
				continue
			}
			if a.seq < bcur.seq {
				best = si
			}
		}
		if best == -1 {
			break
		}
		cs := &streams[best]
		e := cs.events[cs.idx]
		out = append(out, GroupRangedEvent{LogEvent: e.LogEvent, Seq: e.seq, StreamName: cs.name})
		cs.idx++
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (b *memEventBackend) deleteStream(_ context.Context, region, group, stream string) error {
	key := memEventKey(region, group, stream)
	b.mu.Lock()
	defer b.mu.Unlock()
	// Deliberately NOT clearing b.nextSeq[key] here: per-stream seq must stay
	// monotonic even across a delete+recreate of the same stream name (the
	// same A1 lesson as Kinesis's per-shard counter — deriving a fresh
	// counter from post-delete state risks colliding with, or invalidating,
	// any cursor token issued before the delete).
	delete(b.streams, key)
	return nil
}

func (b *memEventBackend) deleteGroup(_ context.Context, region, group string) error {
	prefix := region + "\x00" + group + "\x00"
	b.mu.Lock()
	defer b.mu.Unlock()
	for k := range b.streams {
		if strings.HasPrefix(k, prefix) {
			delete(b.streams, k) // see deleteStream: nextSeq is intentionally left intact
		}
	}
	return nil
}

func (b *memEventBackend) deleteEventsOlderThan(_ context.Context, region, group string, cutoff time.Time) error {
	cutoffMillis := cutoff.UnixMilli()
	prefix := region + "\x00" + group + "\x00"
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, events := range b.streams {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		kept := events[:0]
		for _, e := range events {
			if e.Timestamp >= cutoffMillis {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(b.streams, k)
			continue
		}
		b.streams[k] = kept
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
		for _, e := range b.streams[k] {
			if limit > 0 && len(records) >= limit {
				truncated = true
				break outer
			}
			records = append(records, debugEventRecord{
				Region: region, Group: group, Stream: stream,
				Timestamp: e.Timestamp, Seq: e.seq, Message: e.Message,
			})
		}
	}
	return records, truncated, nil
}

func (b *memEventBackend) debugDeleteAll(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.streams = make(map[string][]memStoredEvent)
	b.nextSeq = make(map[string]int64)
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

// getEventsRange implements the eventBackend interface method of the same
// name (see its doc comment there) as a single indexed range query against
// logs_events' PRIMARY KEY (region, group_name, stream_name, ts, seq) — the
// equality-matched region/group/stream prefix plus a ts range is exactly
// what that index serves, per storage-access-plan.md P2.
func (b *sqlEventBackend) getEventsRange(ctx context.Context, region, group, stream string, startTs, endTs int64, after eventCursor, limit int, forward bool) ([]RangedEvent, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	query := `SELECT ts, seq, ingestion_ts, message FROM logs_events
		WHERE region = ? AND group_name = ? AND stream_name = ? AND ts >= ? AND ts <= ?`
	args := []any{region, group, stream, startTs, endTs}
	if after.Valid {
		if forward {
			query += ` AND (ts > ? OR (ts = ? AND seq > ?))`
		} else {
			query += ` AND (ts < ? OR (ts = ? AND seq < ?))`
		}
		args = append(args, after.Timestamp, after.Timestamp, after.Seq)
	}
	if forward {
		query += ` ORDER BY ts ASC, seq ASC`
	} else {
		query += ` ORDER BY ts DESC, seq DESC`
	}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudwatch logs get events range [%s/%s/%s]: %w", region, group, stream, err)
	}
	defer rows.Close()

	var out []RangedEvent
	for rows.Next() {
		var e RangedEvent
		if err := rows.Scan(&e.Timestamp, &e.Seq, &e.IngestionTime, &e.Message); err != nil {
			return nil, fmt.Errorf("cloudwatch logs get events range [%s/%s/%s]: scan: %w", region, group, stream, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudwatch logs get events range [%s/%s/%s]: rows: %w", region, group, stream, err)
	}
	if !forward {
		// Query ran DESC (closest-to-cursor/most-recent first); reverse to
		// the AWS-documented chronological-ascending response order.
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	if out == nil {
		return []RangedEvent{}, nil
	}
	return out, nil
}

// getGroupEventsRange implements the eventBackend interface method of the
// same name (see its doc comment there) as a single indexed range query
// against idx_logs_events_group (region, group_name, ts) — the whole point
// of that index, built in migrations.go specifically for this query and
// previously unused (storage-access-plan.md A4's evidence).
func (b *sqlEventBackend) getGroupEventsRange(ctx context.Context, region, group, streamPrefix string, startTs, endTs int64, after groupCursor, limit int) ([]GroupRangedEvent, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	query := `SELECT ts, seq, ingestion_ts, message, stream_name FROM logs_events
		WHERE region = ? AND group_name = ? AND ts >= ? AND ts <= ?`
	args := []any{region, group, startTs, endTs}
	if streamPrefix != "" {
		query += ` AND stream_name >= ?`
		args = append(args, streamPrefix)
		if upper := streamPrefixUpperBound(streamPrefix); upper != "" {
			query += ` AND stream_name < ?`
			args = append(args, upper)
		}
	}
	if after.Valid {
		query += ` AND (ts > ? OR (ts = ? AND (stream_name > ? OR (stream_name = ? AND seq > ?))))`
		args = append(args, after.Timestamp, after.Timestamp, after.StreamName, after.StreamName, after.Seq)
	}
	query += ` ORDER BY ts ASC, stream_name ASC, seq ASC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cloudwatch logs get group events range [%s/%s]: %w", region, group, err)
	}
	defer rows.Close()

	var out []GroupRangedEvent
	for rows.Next() {
		var e GroupRangedEvent
		if err := rows.Scan(&e.Timestamp, &e.Seq, &e.IngestionTime, &e.Message, &e.StreamName); err != nil {
			return nil, fmt.Errorf("cloudwatch logs get group events range [%s/%s]: scan: %w", region, group, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudwatch logs get group events range [%s/%s]: rows: %w", region, group, err)
	}
	if out == nil {
		return []GroupRangedEvent{}, nil
	}
	return out, nil
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

func (b *sqlEventBackend) deleteEventsOlderThan(ctx context.Context, region, group string, cutoff time.Time) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM logs_events WHERE region = ? AND group_name = ? AND ts < ?`,
		region, group, cutoff.UnixMilli(),
	); err != nil {
		return fmt.Errorf("cloudwatch logs delete events older than cutoff [%s/%s]: %w", region, group, err)
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
