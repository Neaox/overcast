package sqs

// message_backend.go is the SQS-specific storage layer for messages
// (docs/plans/storage-plan.md item 3.10 — graduated after the qualifying
// benchmark tripped the plan's gate: listMessages' full-queue Scan+decode per
// poll grows linearly with queue depth on both memory and hybrid backends).
//
// Messages are indexed by (region, queue_name, message_id) with a
// (region, queue_name, visible_at) index — mirroring the CloudWatch Logs
// event_backend.go / DynamoDB item_store.go split — which gives:
//
//   - receiveCandidates: an indexed range scan (visible_at <= now) instead of
//     a full-queue Scan + JSON decode of every message per poll.
//   - blockedGroups:     an indexed scan for the FIFO group-locking check,
//     without decoding full message bodies.
//   - countMessages:     a COUNT(*)-shaped query instead of decoding every
//     message just to tally ApproximateNumberOfMessages.
//   - putMessage/getMessage/deleteMessage: O(1) keyed access instead of a
//     Get/Set through the generic kv layer.
//
// Two implementations are provided, mirroring
// internal/services/dynamodb/item_store.go's itemBackend split and
// internal/services/cloudwatch/logs/event_backend.go's eventBackend split:
//
//	memMessageBackend — in-process map of region/queue → messageID → *Message
//	sqlMessageBackend — SQLite sqs_messages table (state.SQLiteDBProvider)
//
// The appropriate backend is chosen at startup based on the state.Store type
// (see newMessageBackendFor, called from newSQSStore after state.Unwrap).
//
// Trade-off accepted per storage-plan.md's "Settled decisions" graduation
// rule: SendMessage's write (putMessage) forfeits HybridStore's async
// pending-log write path on hybrid/persistent — every send becomes a
// synchronous SQLite INSERT instead of an in-memory append that gets batched
// later. Measured in send_bench_test.go: see that file's doc comment for the
// recorded before/after cost (kv-hybrid-async vs dedicated-table-sync) and
// its load-dependent caveat. This is the accepted cost of the receive-path
// win — storage-plan.md's graduation rule requires accepting it, not hiding
// it.
//
// sqs:dedup and queue metadata (sqs:queues, sqs:purge, sqs:receive-attempts)
// deliberately stay in the generic kv store — the storage-access-plan.md
// audit already classed them "row-shaped and fine" (small, non-unbounded,
// no query the key order can't already serve), so graduating them would add
// dual-backend complexity with no measured benefit. Only sqs:messages
// (unbounded, high-frequency, and the one namespace the benchmark actually
// implicated) graduates.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// messageBackend is the narrow interface every SQS message store must
// implement. All methods are region-scoped since the same queue name can
// exist independently in multiple regions (mirrors eventBackend's region
// scoping in the CloudWatch Logs template).
type messageBackend interface {
	// putMessage inserts or replaces one message row. Used by SendMessage,
	// ReceiveMessage's in-flight mutation, ChangeMessageVisibility, DLQ
	// moves, and ReceiveRequestAttemptId replay — anywhere the current
	// design already re-persists a full Message after mutating it in place.
	putMessage(ctx context.Context, region, queueName string, msg *Message) error

	// getMessage returns one message by ID. found is false when no row
	// exists, or when the row exists but its JSON payload is undecodable
	// (CLAUDE.md's malformed-persisted-state rule: prefer a modeled
	// not-found error over InternalError for one bad record on a
	// single-resource read — the caller already maps "not found" to
	// ReceiptHandleIsInvalid, an AWS-shaped error appropriate for an
	// unreadable message too).
	getMessage(ctx context.Context, region, queueName, messageID string) (msg *Message, found bool, err error)

	// deleteMessage removes one message by ID. A no-op (not an error) if the
	// message doesn't exist.
	deleteMessage(ctx context.Context, region, queueName, messageID string) error

	// deleteQueueMessages removes every message for one queue in one ranged
	// operation (PurgeQueue, DeleteQueue).
	deleteQueueMessages(ctx context.Context, region, queueName string) error

	// listMessages returns every message in the queue, decoded, in no
	// particular order. Used by non-hot-path consumers that genuinely need
	// the full set regardless of visibility: PeekMessages (dev-only
	// inspection endpoint), StartMessageMoveTask (DLQ redrive processes the
	// whole DLQ), and the background visibility-transition watcher. Not the
	// receive path — see receiveCandidates.
	listMessages(ctx context.Context, region, queueName string) ([]*Message, error)

	// receiveCandidates returns up to limit messages visible at or before
	// now. When fifo is true, results are ordered by sequence_number
	// ascending (ties broken by message_id) since FIFO delivery order must
	// be exact. When fifo is false (standard queues, which AWS gives no
	// ordering guarantee for), no ordering is requested — letting the SQL
	// backend skip an unnecessary sort and answer straight off the
	// (region, queue_name, visible_at) index, which is the difference
	// between a flat and a sort-bounded-by-visible-count curve; see
	// sqlMessageBackend.receiveCandidates. This is the ReceiveMessage hot
	// path storage-plan.md 3.10 exists to fix.
	//
	// Callers must be prepared to see fewer than `limit` results even when
	// more visible messages exist beyond a group-blocked/duplicate-group
	// prefix — see selectVisibleMessages' retry-with-larger-limit loop,
	// which is how FIFO's group-locking is reconciled with a bounded SQL
	// fetch without re-implementing AWS ordering/grouping semantics in SQL.
	receiveCandidates(ctx context.Context, region, queueName string, now time.Time, limit int, fifo bool) ([]*Message, error)

	// blockedGroups returns the set of distinct non-empty MessageGroupIds
	// that currently have at least one invisible (in-flight or delayed)
	// message. FIFO's per-group locking blocks an ENTIRE group while any one
	// of its messages is invisible — see the FIFO semantics analysis in this
	// package's storage-plan.md 3.10 report. Returns an empty, non-nil map
	// for a standard queue or an empty queue.
	blockedGroups(ctx context.Context, region, queueName string, now time.Time) (map[string]bool, error)

	// countMessages returns (visible, total) message counts for
	// ApproximateNumberOfMessages / ApproximateNumberOfMessagesNotVisible
	// (= total - visible).
	countMessages(ctx context.Context, region, queueName string, now time.Time) (visible, total int, err error)

	// debugScan returns up to limit raw message rows for
	// /_debug/state/sqs:messages, ordered deterministically. limit <= 0
	// means unbounded (tests only — HTTP callers always pass a positive
	// limit, see Service.DebugStateKeys). The second return value reports
	// whether more rows exist beyond limit.
	debugScan(ctx context.Context, limit int) (records []debugMessageRecord, truncated bool, err error)

	// debugDeleteAll removes every persisted message, for /_debug/reset.
	debugDeleteAll(ctx context.Context) error
}

// debugMessageRecord is one row surfaced by debugScan.
type debugMessageRecord struct {
	Region    string
	Queue     string
	MessageID string
	VisibleAt int64
	RawJSON   string // the message's canonical JSON payload
}

func debugMessageKey(r debugMessageRecord) string {
	return r.Region + "/" + r.Queue + "/" + r.MessageID
}

// cloneMessage deep-copies msg via a JSON round trip. Both backends store
// (mem) or reconstitute (sql) messages this way so callers never alias a
// pointer another goroutine or a later putMessage call might mutate —
// mirroring the isolation the generic state.Store's JSON-string values gave
// for free before this graduation.
func cloneMessage(msg *Message) (*Message, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("clone message: marshal: %w", err)
	}
	var out Message
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("clone message: unmarshal: %w", err)
	}
	return &out, nil
}

// parseSequenceNumber parses Message.SequenceNumber (a decimal string,
// unpadded — see handler_message.go's strconv.FormatInt(h.seqNum.Add(1), 10))
// into an int64 for ordering. Returns 0 for standard-queue messages (which
// never set SequenceNumber) or an unparsable value, both of which sort
// first — harmless since standard queues have no ordering contract and a
// genuinely unparsable FIFO sequence number would indicate corrupted data
// already caught elsewhere.
func parseSequenceNumber(s string) int64 {
	if s == "" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// ---------------------------------------------------------------------------
// memMessageBackend — in-process store (memory-mode parity)
// ---------------------------------------------------------------------------

type memMessageBackend struct {
	mu     sync.RWMutex
	queues map[string]map[string]*Message // key: memQueueKey(region, queue) -> messageID -> *Message
}

func newMemMessageBackend() *memMessageBackend {
	return &memMessageBackend{queues: make(map[string]map[string]*Message)}
}

// memQueueKey builds an opaque, collision-free map key for one queue. SQS
// queue names cannot contain "/" (AWS restricts them to alphanumerics,
// hyphens, and underscores), so a plain separator is unambiguous — unlike
// CloudWatch Logs' group/stream names, nothing here needs the NUL-separator
// treatment event_backend.go's memEventKey uses.
func memQueueKey(region, queueName string) string {
	return region + "/" + queueName
}

func (b *memMessageBackend) putMessage(_ context.Context, region, queueName string, msg *Message) error {
	clone, err := cloneMessage(msg)
	if err != nil {
		return err
	}
	key := memQueueKey(region, queueName)
	b.mu.Lock()
	defer b.mu.Unlock()
	q, ok := b.queues[key]
	if !ok {
		q = make(map[string]*Message)
		b.queues[key] = q
	}
	q[clone.MessageID] = clone
	return nil
}

func (b *memMessageBackend) getMessage(_ context.Context, region, queueName, messageID string) (*Message, bool, error) {
	key := memQueueKey(region, queueName)
	b.mu.RLock()
	defer b.mu.RUnlock()
	q, ok := b.queues[key]
	if !ok {
		return nil, false, nil
	}
	msg, ok := q[messageID]
	if !ok {
		return nil, false, nil
	}
	clone, err := cloneMessage(msg)
	if err != nil {
		return nil, false, err
	}
	return clone, true, nil
}

func (b *memMessageBackend) deleteMessage(_ context.Context, region, queueName, messageID string) error {
	key := memQueueKey(region, queueName)
	b.mu.Lock()
	defer b.mu.Unlock()
	if q, ok := b.queues[key]; ok {
		delete(q, messageID)
	}
	return nil
}

func (b *memMessageBackend) deleteQueueMessages(_ context.Context, region, queueName string) error {
	key := memQueueKey(region, queueName)
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.queues, key)
	return nil
}

func (b *memMessageBackend) listMessages(_ context.Context, region, queueName string) ([]*Message, error) {
	key := memQueueKey(region, queueName)
	b.mu.RLock()
	defer b.mu.RUnlock()
	q := b.queues[key]
	out := make([]*Message, 0, len(q))
	for _, msg := range q {
		clone, err := cloneMessage(msg)
		if err != nil {
			return nil, err
		}
		out = append(out, clone)
	}
	return out, nil
}

func (b *memMessageBackend) receiveCandidates(_ context.Context, region, queueName string, now time.Time, limit int, fifo bool) ([]*Message, error) {
	key := memQueueKey(region, queueName)
	b.mu.RLock()
	q := b.queues[key]
	candidates := make([]*Message, 0, len(q))
	for _, msg := range q {
		if !messageVisibleAt(msg, now) {
			continue
		}
		candidates = append(candidates, msg)
	}
	b.mu.RUnlock()

	// Standard queues have no ordering contract — skip the sort entirely
	// (matches the SQL backend's index-only path for the same case).
	if fifo {
		sort.SliceStable(candidates, func(i, j int) bool {
			si := parseSequenceNumber(candidates[i].SequenceNumber)
			sj := parseSequenceNumber(candidates[j].SequenceNumber)
			if si != sj {
				return si < sj
			}
			return candidates[i].MessageID < candidates[j].MessageID
		})
	}
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}

	out := make([]*Message, len(candidates))
	for i, msg := range candidates {
		clone, err := cloneMessage(msg)
		if err != nil {
			return nil, err
		}
		out[i] = clone
	}
	return out, nil
}

func (b *memMessageBackend) blockedGroups(_ context.Context, region, queueName string, now time.Time) (map[string]bool, error) {
	key := memQueueKey(region, queueName)
	b.mu.RLock()
	defer b.mu.RUnlock()
	blocked := make(map[string]bool)
	for _, msg := range b.queues[key] {
		if msg.MessageGroupId == "" {
			continue
		}
		if !messageVisibleAt(msg, now) {
			blocked[msg.MessageGroupId] = true
		}
	}
	return blocked, nil
}

func (b *memMessageBackend) countMessages(_ context.Context, region, queueName string, now time.Time) (visible, total int, err error) {
	key := memQueueKey(region, queueName)
	b.mu.RLock()
	defer b.mu.RUnlock()
	q := b.queues[key]
	total = len(q)
	for _, msg := range q {
		if messageVisibleAt(msg, now) {
			visible++
		}
	}
	return visible, total, nil
}

func (b *memMessageBackend) debugScan(_ context.Context, limit int) ([]debugMessageRecord, bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	keys := make([]string, 0, len(b.queues))
	for k := range b.queues {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var records []debugMessageRecord
	truncated := false
outer:
	for _, k := range keys {
		region, queueName, _ := strings.Cut(k, "/")
		ids := make([]string, 0, len(b.queues[k]))
		for id := range b.queues[k] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if limit > 0 && len(records) >= limit {
				truncated = true
				break outer
			}
			msg := b.queues[k][id]
			raw, err := json.Marshal(msg)
			if err != nil {
				return nil, false, fmt.Errorf("sqs debug scan: marshal [%s/%s/%s]: %w", region, queueName, id, err)
			}
			records = append(records, debugMessageRecord{
				Region: region, Queue: queueName, MessageID: id,
				VisibleAt: msg.VisibleAfter.UnixMilli(), RawJSON: string(raw),
			})
		}
	}
	return records, truncated, nil
}

func (b *memMessageBackend) debugDeleteAll(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queues = make(map[string]map[string]*Message)
	return nil
}

// messageVisibleAt reports whether msg is visible at the given instant —
// the same rule as Message.IsVisible(clk), inlined here so the backend can
// evaluate visibility against an already-resolved "now" without threading a
// full clock.Clock through the messageBackend interface (the backend only
// ever needs one instant per call; the caller — sqsStore — is what talks to
// clock.Clock).
func messageVisibleAt(msg *Message, now time.Time) bool {
	return !now.Before(msg.VisibleAfter)
}

// ---------------------------------------------------------------------------
// sqlMessageBackend — dedicated sqs_messages SQLite table
// ---------------------------------------------------------------------------
//
// Schema is created by a registered migration (migrations.go), not here —
// see storage-plan.md item 3.10 and internal/state/migrate.go's Migration
// doc comment. By the time dbFn() returns a non-nil *sql.DB, the migration
// runner has already run (state.SQLiteDBProvider.DB() blocks on it — see
// SQLiteStore.DB / HybridStore.DB), so sqs_messages is guaranteed to exist.

type sqlMessageBackend struct {
	dbFn func() *sql.DB
	db   *sql.DB
	once sync.Once
	err  error // set by init; sticky
}

// newSQLMessageBackend returns a backend that lazily resolves the *sql.DB on
// first use. Deferring DB resolution avoids blocking startup when the
// underlying store opens SQLite asynchronously.
func newSQLMessageBackend(dbFn func() *sql.DB) *sqlMessageBackend {
	return &sqlMessageBackend{dbFn: dbFn}
}

func (b *sqlMessageBackend) init() error {
	b.once.Do(func() {
		b.db = b.dbFn()
		if b.db == nil {
			b.err = fmt.Errorf("sqs: sqlite DB unavailable")
		}
	})
	return b.err
}

func (b *sqlMessageBackend) putMessage(ctx context.Context, region, queueName string, msg *Message) error {
	if err := b.init(); err != nil {
		return err
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("sqs put message [%s/%s/%s]: marshal: %w", region, queueName, msg.MessageID, err)
	}
	seqNum := parseSequenceNumber(msg.SequenceNumber)
	visibleAt := msg.VisibleAfter.UnixMilli()
	_, err = b.db.ExecContext(ctx, `
		INSERT INTO sqs_messages (region, queue_name, message_id, visible_at, message_group_id, sequence_number, message_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (region, queue_name, message_id) DO UPDATE SET
			visible_at = excluded.visible_at,
			message_group_id = excluded.message_group_id,
			sequence_number = excluded.sequence_number,
			message_json = excluded.message_json
	`, region, queueName, msg.MessageID, visibleAt, msg.MessageGroupId, seqNum, string(raw))
	if err != nil {
		return fmt.Errorf("sqs put message [%s/%s/%s]: %w", region, queueName, msg.MessageID, err)
	}
	return nil
}

func (b *sqlMessageBackend) getMessage(ctx context.Context, region, queueName, messageID string) (*Message, bool, error) {
	if err := b.init(); err != nil {
		return nil, false, err
	}
	var raw string
	err := b.db.QueryRowContext(ctx,
		`SELECT message_json FROM sqs_messages WHERE region = ? AND queue_name = ? AND message_id = ?`,
		region, queueName, messageID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("sqs get message [%s/%s/%s]: %w", region, queueName, messageID, err)
	}
	var msg Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		// CLAUDE.md malformed-persisted-state rule: a single undecodable
		// record on a named-resource read maps to "not found" (the caller
		// turns that into ReceiptHandleIsInvalid), not InternalError.
		return nil, false, nil
	}
	return &msg, true, nil
}

func (b *sqlMessageBackend) deleteMessage(ctx context.Context, region, queueName, messageID string) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM sqs_messages WHERE region = ? AND queue_name = ? AND message_id = ?`,
		region, queueName, messageID,
	); err != nil {
		return fmt.Errorf("sqs delete message [%s/%s/%s]: %w", region, queueName, messageID, err)
	}
	return nil
}

func (b *sqlMessageBackend) deleteQueueMessages(ctx context.Context, region, queueName string) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx,
		`DELETE FROM sqs_messages WHERE region = ? AND queue_name = ?`,
		region, queueName,
	); err != nil {
		return fmt.Errorf("sqs delete queue messages [%s/%s]: %w", region, queueName, err)
	}
	return nil
}

func (b *sqlMessageBackend) listMessages(ctx context.Context, region, queueName string) ([]*Message, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT message_json FROM sqs_messages WHERE region = ? AND queue_name = ?`,
		region, queueName,
	)
	if err != nil {
		return nil, fmt.Errorf("sqs list messages [%s/%s]: %w", region, queueName, err)
	}
	defer rows.Close()
	return scanMessageRows(rows, region, queueName)
}

// receiveCandidates is the SQL implementation of the ReceiveMessage hot
// path. The `fifo` flag controls whether an ORDER BY is requested:
//
//   - Standard queues (fifo=false): no ORDER BY at all. The query answers
//     straight off idx_sqs_messages_visible (region, queue_name, visible_at)
//     and can stop as soon as LIMIT rows are found — this is what keeps the
//     acceptance benchmark's curve flat vs. queue depth for standard queues
//     (see receive_bench_test.go).
//   - FIFO queues (fifo=true): ORDER BY sequence_number is correctness-
//     required (delivery order must be exact — see the FIFO semantics
//     analysis in this package's storage-plan.md 3.10 report), and SQLite
//     cannot satisfy both the visible_at filter and a sequence_number sort
//     from a single index, so it must sort every row matching the WHERE
//     clause before applying LIMIT. This is a real, documented limitation:
//     the FIFO path is bounded by the count of currently-visible messages in
//     the queue, not by `limit` — still a large win over the pre-3.10 design
//     (which scanned and JSON-decoded every message, visible or not), but
//     not perfectly flat in the adversarial "everything visible" shape the
//     benchmark also measures.
func (b *sqlMessageBackend) receiveCandidates(ctx context.Context, region, queueName string, now time.Time, limit int, fifo bool) ([]*Message, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	query := `
		SELECT message_json FROM sqs_messages
		WHERE region = ? AND queue_name = ? AND visible_at <= ?
	`
	if fifo {
		query += ` ORDER BY sequence_number ASC, message_id ASC`
	}
	args := []any{region, queueName, now.UnixMilli()}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqs receive candidates [%s/%s]: %w", region, queueName, err)
	}
	defer rows.Close()
	return scanMessageRows(rows, region, queueName)
}

// scanMessageRows decodes every row of a `SELECT message_json ...` result
// set, skipping (and logging) any row whose JSON payload is undecodable —
// CLAUDE.md's malformed-persisted-state rule for multi-row reads: one
// corrupt record must not fail the whole list/scan.
func scanMessageRows(rows *sql.Rows, region, queueName string) ([]*Message, error) {
	var out []*Message
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("sqs scan message row [%s/%s]: %w", region, queueName, err)
		}
		var msg Message
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			// Skip and continue — do not fail the whole list for one bad row.
			continue
		}
		out = append(out, &msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqs message rows [%s/%s]: %w", region, queueName, err)
	}
	if out == nil {
		out = []*Message{}
	}
	return out, nil
}

func (b *sqlMessageBackend) blockedGroups(ctx context.Context, region, queueName string, now time.Time) (map[string]bool, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx, `
		SELECT DISTINCT message_group_id FROM sqs_messages
		WHERE region = ? AND queue_name = ? AND visible_at > ? AND message_group_id != ''
	`, region, queueName, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("sqs blocked groups [%s/%s]: %w", region, queueName, err)
	}
	defer rows.Close()

	blocked := make(map[string]bool)
	for rows.Next() {
		var group string
		if err := rows.Scan(&group); err != nil {
			return nil, fmt.Errorf("sqs blocked groups scan [%s/%s]: %w", region, queueName, err)
		}
		blocked[group] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqs blocked groups rows [%s/%s]: %w", region, queueName, err)
	}
	return blocked, nil
}

func (b *sqlMessageBackend) countMessages(ctx context.Context, region, queueName string, now time.Time) (visible, total int, err error) {
	if err := b.init(); err != nil {
		return 0, 0, err
	}
	err = b.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN visible_at <= ? THEN 1 ELSE 0 END), 0),
			COUNT(*)
		FROM sqs_messages WHERE region = ? AND queue_name = ?
	`, now.UnixMilli(), region, queueName).Scan(&visible, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("sqs count messages [%s/%s]: %w", region, queueName, err)
	}
	return visible, total, nil
}

func (b *sqlMessageBackend) debugScan(ctx context.Context, limit int) ([]debugMessageRecord, bool, error) {
	if err := b.init(); err != nil {
		return nil, false, err
	}
	query := `SELECT region, queue_name, message_id, visible_at, message_json FROM sqs_messages
		ORDER BY region, queue_name, message_id`
	args := []any{}
	if limit > 0 {
		// Fetch one extra row to detect truncation without a separate COUNT(*).
		query += ` LIMIT ?`
		args = append(args, limit+1)
	}
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("sqs debug scan: %w", err)
	}
	defer rows.Close()

	var records []debugMessageRecord
	for rows.Next() {
		var r debugMessageRecord
		if err := rows.Scan(&r.Region, &r.Queue, &r.MessageID, &r.VisibleAt, &r.RawJSON); err != nil {
			return nil, false, fmt.Errorf("sqs debug scan: row: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("sqs debug scan: rows: %w", err)
	}
	truncated := false
	if limit > 0 && len(records) > limit {
		records = records[:limit]
		truncated = true
	}
	return records, truncated, nil
}

func (b *sqlMessageBackend) debugDeleteAll(ctx context.Context) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, err := b.db.ExecContext(ctx, `DELETE FROM sqs_messages`); err != nil {
		return fmt.Errorf("sqs debug delete all: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Backend selection
// ---------------------------------------------------------------------------

// newMessageBackendFor selects the right messageBackend based on the store
// type:
//   - SQLiteDBProvider → sqlMessageBackend (dedicated indexed table in the same DB file)
//   - anything else    → memMessageBackend (in-process map, memory-mode parity)
//
// Callers must pass a store already resolved with state.Unwrap (see
// newSQSStore) — a *state.NamespacedStore never implements SQLiteDBProvider
// itself, so passing one through unresolved always falls back to the memory
// backend (the same interface-erasure hazard state.Unwrap exists to guard
// against — see internal/services/dynamodb/service.go's newItemBackendFor
// doc comment).
func newMessageBackendFor(store state.Store) messageBackend {
	if provider, ok := store.(state.SQLiteDBProvider); ok {
		return newSQLMessageBackend(provider.DB)
	}
	return newMemMessageBackend()
}
