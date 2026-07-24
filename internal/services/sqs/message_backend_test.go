//go:build !nosqlite

package sqs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// newTestMessageBackends returns one memMessageBackend and one
// sqlMessageBackend backed by a real, temp-dir-rooted *state.HybridStore, so
// tests can run the same assertions against both — the memory-mode parity
// requirement from docs/plans/storage-plan.md's graduation rule. Mirrors
// internal/services/cloudwatch/logs/event_backend_test.go's
// newTestBackends.
func newTestMessageBackends(t *testing.T) (mem messageBackend, sqlBackend messageBackend) {
	t.Helper()
	mem = newMemMessageBackend()

	dir := t.TempDir()
	hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() {
		if err := hybrid.Close(); err != nil {
			t.Logf("hybrid.Close: %v", err)
		}
	})
	backendStore := state.Unwrap(hybrid, serviceName)
	sqlBackend = newMessageBackendFor(backendStore)
	if _, ok := sqlBackend.(*sqlMessageBackend); !ok {
		t.Fatalf("expected sqlMessageBackend for a SQLite-backed store, got %T", sqlBackend)
	}
	return mem, sqlBackend
}

func newTestMessage(id, group, seq string, visibleAt time.Time) *Message {
	return &Message{
		MessageID:      id,
		Body:           "body-" + id,
		MD5OfBody:      "d41d8cd98f00b204e9800998ecf8427e",
		VisibleAfter:   visibleAt,
		MessageGroupId: group,
		SequenceNumber: seq,
	}
}

// TestMessageBackend_MemoryAndSQL_Parity runs the same put/get/delete/
// receive/count sequence against both backends and asserts identical
// externally-observable behavior — the "backend parity" test the storage-
// plan 3.10 report calls for.
func TestMessageBackend_MemoryAndSQL_Parity(t *testing.T) {
	mem, sqlBackend := newTestMessageBackends(t)

	for name, b := range map[string]messageBackend{"memory": mem, "sql": sqlBackend} {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "my-queue"
			now := time.UnixMilli(1_700_000_000_000).UTC()

			// Empty queue reads back empty, non-nil results.
			msgs, err := b.listMessages(ctx, region, queue)
			if err != nil {
				t.Fatalf("listMessages (empty): %v", err)
			}
			if len(msgs) != 0 {
				t.Fatalf("listMessages (empty) = %v, want empty", msgs)
			}
			blocked, err := b.blockedGroups(ctx, region, queue, now)
			if err != nil {
				t.Fatalf("blockedGroups (empty): %v", err)
			}
			if len(blocked) != 0 {
				t.Fatalf("blockedGroups (empty) = %v, want empty", blocked)
			}

			// Put three standard messages, one visible, one not-yet-visible
			// (delayed), one already received (in-flight).
			visible := newTestMessage("m-visible", "", "", now.Add(-time.Second))
			delayed := newTestMessage("m-delayed", "", "", now.Add(time.Hour))
			inflight := newTestMessage("m-inflight", "", "", now.Add(time.Minute))
			for _, m := range []*Message{visible, delayed, inflight} {
				if err := b.putMessage(ctx, region, queue, m); err != nil {
					t.Fatalf("putMessage(%s): %v", m.MessageID, err)
				}
			}

			// getMessage round-trips exactly.
			got, found, err := b.getMessage(ctx, region, queue, "m-visible")
			if err != nil || !found {
				t.Fatalf("getMessage(m-visible): found=%v err=%v", found, err)
			}
			if got.Body != visible.Body {
				t.Fatalf("getMessage(m-visible).Body = %q, want %q", got.Body, visible.Body)
			}

			// getMessage on a missing ID reports not-found, not an error.
			_, found, err = b.getMessage(ctx, region, queue, "does-not-exist")
			if err != nil {
				t.Fatalf("getMessage(missing): unexpected error %v", err)
			}
			if found {
				t.Fatalf("getMessage(missing): found = true, want false")
			}

			// receiveCandidates (standard, unordered) returns only the
			// visible one.
			candidates, err := b.receiveCandidates(ctx, region, queue, now, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates: %v", err)
			}
			if len(candidates) != 1 || candidates[0].MessageID != "m-visible" {
				t.Fatalf("receiveCandidates = %v, want just m-visible", candidates)
			}

			// countMessages: 1 visible, 3 total.
			visibleCount, total, err := b.countMessages(ctx, region, queue, now)
			if err != nil {
				t.Fatalf("countMessages: %v", err)
			}
			if visibleCount != 1 || total != 3 {
				t.Fatalf("countMessages = (%d, %d), want (1, 3)", visibleCount, total)
			}

			// deleteMessage removes exactly one.
			if err := b.deleteMessage(ctx, region, queue, "m-delayed"); err != nil {
				t.Fatalf("deleteMessage: %v", err)
			}
			if _, found, _ := b.getMessage(ctx, region, queue, "m-delayed"); found {
				t.Fatalf("m-delayed still found after deleteMessage")
			}
			_, total, _ = b.countMessages(ctx, region, queue, now)
			if total != 2 {
				t.Fatalf("countMessages total after delete = %d, want 2", total)
			}

			// deleteQueueMessages clears everything for the queue.
			if err := b.deleteQueueMessages(ctx, region, queue); err != nil {
				t.Fatalf("deleteQueueMessages: %v", err)
			}
			msgs, err = b.listMessages(ctx, region, queue)
			if err != nil {
				t.Fatalf("listMessages after deleteQueueMessages: %v", err)
			}
			if len(msgs) != 0 {
				t.Fatalf("listMessages after deleteQueueMessages = %v, want empty", msgs)
			}

			// debugDeleteAll clears every queue.
			if err := b.putMessage(ctx, region, queue, visible); err != nil {
				t.Fatalf("re-seed for debugDeleteAll: %v", err)
			}
			if err := b.debugDeleteAll(ctx); err != nil {
				t.Fatalf("debugDeleteAll: %v", err)
			}
			msgs, _ = b.listMessages(ctx, region, queue)
			if len(msgs) != 0 {
				t.Fatalf("listMessages after debugDeleteAll = %v, want empty", msgs)
			}
		})
	}
}

// TestMessageBackend_FIFOOrdering_Parity proves both backends deliver FIFO
// candidates in sequence-number order, identical to each other and to the
// pre-graduation full-scan-then-sort behavior.
func TestMessageBackend_FIFOOrdering_Parity(t *testing.T) {
	mem, sqlBackend := newTestMessageBackends(t)

	for name, b := range map[string]messageBackend{"memory": mem, "sql": sqlBackend} {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "fifo-queue.fifo"
			now := time.UnixMilli(1_700_000_000_000).UTC()

			// Insert out of sequence order to prove the backend sorts, not
			// just returns insertion order.
			msgs := []*Message{
				newTestMessage("m-3", "group-a", "3", now.Add(-time.Second)),
				newTestMessage("m-1", "group-a", "1", now.Add(-time.Second)),
				newTestMessage("m-2", "group-a", "2", now.Add(-time.Second)),
			}
			for _, m := range msgs {
				if err := b.putMessage(ctx, region, queue, m); err != nil {
					t.Fatalf("putMessage(%s): %v", m.MessageID, err)
				}
			}

			got, err := b.receiveCandidates(ctx, region, queue, now, 10, true)
			if err != nil {
				t.Fatalf("receiveCandidates: %v", err)
			}
			if len(got) != 3 {
				t.Fatalf("receiveCandidates returned %d messages, want 3", len(got))
			}
			wantOrder := []string{"m-1", "m-2", "m-3"}
			for i, want := range wantOrder {
				if got[i].MessageID != want {
					t.Fatalf("candidate %d = %s, want %s (full order: %v)", i, got[i].MessageID, want, candidateIDs(got))
				}
			}
		})
	}
}

func candidateIDs(msgs []*Message) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.MessageID
	}
	return ids
}

// TestMessageBackend_BlockedGroups_Parity proves both backends identify a
// FIFO group as blocked exactly when it has an invisible message, and never
// for standard (empty-group) messages.
func TestMessageBackend_BlockedGroups_Parity(t *testing.T) {
	mem, sqlBackend := newTestMessageBackends(t)

	for name, b := range map[string]messageBackend{"memory": mem, "sql": sqlBackend} {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "fifo-queue.fifo"
			now := time.UnixMilli(1_700_000_000_000).UTC()

			visibleA := newTestMessage("a-visible", "group-a", "1", now.Add(-time.Second))
			invisibleA := newTestMessage("a-invisible", "group-a", "2", now.Add(time.Hour))
			visibleB := newTestMessage("b-visible", "group-b", "1", now.Add(-time.Second))
			noGroupInvisible := newTestMessage("no-group", "", "1", now.Add(time.Hour))

			for _, m := range []*Message{visibleA, invisibleA, visibleB, noGroupInvisible} {
				if err := b.putMessage(ctx, region, queue, m); err != nil {
					t.Fatalf("putMessage(%s): %v", m.MessageID, err)
				}
			}

			blocked, err := b.blockedGroups(ctx, region, queue, now)
			if err != nil {
				t.Fatalf("blockedGroups: %v", err)
			}
			if !blocked["group-a"] {
				t.Errorf("expected group-a blocked (has invisible message)")
			}
			if blocked["group-b"] {
				t.Errorf("expected group-b NOT blocked (all visible)")
			}
			if len(blocked) != 1 {
				t.Errorf("blockedGroups = %v, want exactly {group-a}", blocked)
			}
		})
	}
}

// TestMessageBackend_VisibleAtBoundary_Parity pins the exact boundary
// semantics of visibility: a message becomes visible AT its VisibleAfter
// instant (not strictly after) — matching Message.IsVisible's
// !clk.Now().Before(m.VisibleAfter) contract — for both receiveCandidates
// and countMessages, on both backends.
func TestMessageBackend_VisibleAtBoundary_Parity(t *testing.T) {
	mem, sqlBackend := newTestMessageBackends(t)

	for name, b := range map[string]messageBackend{"memory": mem, "sql": sqlBackend} {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, queue = "us-east-1", "boundary-queue"
			boundary := time.UnixMilli(1_700_000_000_000).UTC()

			msg := newTestMessage("m-1", "", "", boundary)
			if err := b.putMessage(ctx, region, queue, msg); err != nil {
				t.Fatalf("putMessage: %v", err)
			}

			// One millisecond before the boundary: not yet visible.
			before := boundary.Add(-time.Millisecond)
			candidates, err := b.receiveCandidates(ctx, region, queue, before, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates(before): %v", err)
			}
			if len(candidates) != 0 {
				t.Fatalf("receiveCandidates(before boundary) = %v, want none visible yet", candidates)
			}
			visibleCount, _, err := b.countMessages(ctx, region, queue, before)
			if err != nil {
				t.Fatalf("countMessages(before): %v", err)
			}
			if visibleCount != 0 {
				t.Fatalf("countMessages(before boundary) visible = %d, want 0", visibleCount)
			}

			// Exactly at the boundary: visible.
			candidates, err = b.receiveCandidates(ctx, region, queue, boundary, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates(at): %v", err)
			}
			if len(candidates) != 1 {
				t.Fatalf("receiveCandidates(at boundary) = %v, want exactly 1 visible", candidates)
			}
			visibleCount, _, err = b.countMessages(ctx, region, queue, boundary)
			if err != nil {
				t.Fatalf("countMessages(at): %v", err)
			}
			if visibleCount != 1 {
				t.Fatalf("countMessages(at boundary) visible = %d, want 1", visibleCount)
			}

			// One millisecond after: still visible.
			after := boundary.Add(time.Millisecond)
			candidates, err = b.receiveCandidates(ctx, region, queue, after, 10, false)
			if err != nil {
				t.Fatalf("receiveCandidates(after): %v", err)
			}
			if len(candidates) != 1 {
				t.Fatalf("receiveCandidates(after boundary) = %v, want exactly 1 visible", candidates)
			}
		})
	}
}

// TestSQLMessageBackend_ToleratesCorruptRows proves the SQL backend's
// list/receive/get paths follow CLAUDE.md's malformed-persisted-state rule:
// one row with an undecodable message_json must not fail the whole
// list/scan, and must report "not found" (not InternalError) for a direct
// getMessage on that row — see message_backend.go's getMessage/
// scanMessageRows doc comments. This is the SQL-backend-specific
// replacement for tests/integration/sqs's now-defunct
// TestPurgeQueue_messagesWithUnreadablePayloads, which used to inject
// corruption via the generic kv store before messages graduated to this
// dedicated table (see that test's updated doc comment in
// tests/integration/sqs/sqs_test.go).
func TestSQLMessageBackend_ToleratesCorruptRows(t *testing.T) {
	dir := t.TempDir()
	hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() { _ = hybrid.Close() })

	backendStore := state.Unwrap(hybrid, serviceName)
	backend, ok := newMessageBackendFor(backendStore).(*sqlMessageBackend)
	if !ok {
		t.Fatalf("expected sqlMessageBackend, got %T", newMessageBackendFor(backendStore))
	}
	if err := backend.init(); err != nil {
		t.Fatalf("init: %v", err)
	}

	ctx := context.Background()
	const region, queue = "us-east-1", "corrupt-queue"
	now := time.UnixMilli(1_700_000_000_000).UTC()

	// One healthy message.
	healthy := newTestMessage("healthy-1", "", "", now.Add(-time.Second))
	if err := backend.putMessage(ctx, region, queue, healthy); err != nil {
		t.Fatalf("putMessage(healthy): %v", err)
	}

	// One row with undecodable JSON, inserted directly (bypassing putMessage,
	// which always marshals a valid *Message) to simulate corruption that
	// reached the table some other way (e.g. manual DB edit, a future bug).
	insertCorruptRow(t, backend.db, region, queue, "corrupt-1", now.Add(-time.Second).UnixMilli())

	// listMessages: the healthy row is returned, the corrupt one is skipped
	// — not a fatal error for the whole list.
	msgs, err := backend.listMessages(ctx, region, queue)
	if err != nil {
		t.Fatalf("listMessages: unexpected error %v", err)
	}
	if len(msgs) != 1 || msgs[0].MessageID != "healthy-1" {
		t.Fatalf("listMessages = %v, want just healthy-1", candidateIDs(msgs))
	}

	// receiveCandidates: same isolation guarantee on the hot path.
	candidates, err := backend.receiveCandidates(ctx, region, queue, now, 10, false)
	if err != nil {
		t.Fatalf("receiveCandidates: unexpected error %v", err)
	}
	if len(candidates) != 1 || candidates[0].MessageID != "healthy-1" {
		t.Fatalf("receiveCandidates = %v, want just healthy-1", candidateIDs(candidates))
	}

	// getMessage on the corrupt row directly: reported as not-found (so the
	// caller — sqsStore.getMessage — maps it to ReceiptHandleIsInvalid, an
	// AWS-shaped error), never InternalError for one bad record.
	_, found, err := backend.getMessage(ctx, region, queue, "corrupt-1")
	if err != nil {
		t.Fatalf("getMessage(corrupt): unexpected error %v, want nil (mapped to not-found)", err)
	}
	if found {
		t.Fatalf("getMessage(corrupt): found = true, want false")
	}

	// deleteQueueMessages doesn't care about payload validity — it's a blind
	// ranged delete — so it must remove the corrupt row too.
	if err := backend.deleteQueueMessages(ctx, region, queue); err != nil {
		t.Fatalf("deleteQueueMessages: %v", err)
	}
	var remaining int
	if err := backend.db.QueryRow(`SELECT COUNT(*) FROM sqs_messages WHERE region = ? AND queue_name = ?`, region, queue).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining rows after deleteQueueMessages = %d, want 0 (including the corrupt one)", remaining)
	}
}

// insertCorruptRow inserts a sqs_messages row whose message_json cannot be
// JSON-decoded, bypassing every normal write path.
func insertCorruptRow(t *testing.T, db *sql.DB, region, queue, messageID string, visibleAt int64) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO sqs_messages (region, queue_name, message_id, visible_at, message_group_id, sequence_number, message_json)
		VALUES (?, ?, ?, ?, '', 0, ?)
	`, region, queue, messageID, visibleAt, `{not valid json`); err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}
}

// TestSQLMessageBackend_RestartPersistence proves messages survive a
// process restart through the dedicated table — close the HybridStore,
// reopen a fresh one against the same data directory, and confirm the
// message is still there and still receivable.
func TestSQLMessageBackend_RestartPersistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	const region, queue = "us-east-1", "restart-queue"
	now := time.UnixMilli(1_700_000_000_000).UTC()

	func() {
		hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
		if err != nil {
			t.Fatalf("NewHybridStore (first open): %v", err)
		}
		defer hybrid.Close()

		backend := newMessageBackendFor(state.Unwrap(hybrid, serviceName))
		msg := newTestMessage("survivor", "", "", now.Add(-time.Second))
		if err := backend.putMessage(ctx, region, queue, msg); err != nil {
			t.Fatalf("putMessage: %v", err)
		}
		// Force the write to reach SQLite before closing — Close() itself
		// also flushes, but being explicit here documents the intent.
		if err := state.Flush(ctx, hybrid); err != nil {
			t.Fatalf("flush: %v", err)
		}
	}()

	hybrid2, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore (reopen): %v", err)
	}
	defer hybrid2.Close()
	if err := hybrid2.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	backend2 := newMessageBackendFor(state.Unwrap(hybrid2, serviceName))
	msg, found, err := backend2.getMessage(ctx, region, queue, "survivor")
	if err != nil {
		t.Fatalf("getMessage after restart: %v", err)
	}
	if !found {
		t.Fatalf("message did not survive restart")
	}
	if msg.Body != "body-survivor" {
		t.Fatalf("restarted message body = %q, want body-survivor", msg.Body)
	}

	candidates, err := backend2.receiveCandidates(ctx, region, queue, now, 10, false)
	if err != nil {
		t.Fatalf("receiveCandidates after restart: %v", err)
	}
	if len(candidates) != 1 || candidates[0].MessageID != "survivor" {
		t.Fatalf("receiveCandidates after restart = %v, want just survivor", candidateIDs(candidates))
	}
}
