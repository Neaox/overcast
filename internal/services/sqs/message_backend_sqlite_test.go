//go:build !nosqlite

package sqs

// The sqlMessageBackend-only half of message_backend_test.go, split out
// because both tests reach past the messageBackend interface into the SQLite
// table itself — raw row inserts, a *sql.DB row count, and a close/reopen
// restart cycle. Under -tags nosqlite, state.NewHybridStore is a stub that
// always errors (internal/state/sqlite_hybrid_nosqlite.go), so there is no SQL
// backend to exercise. The parity tests in message_backend_test.go stay
// untagged and degrade to memory-only instead — see newTestMessageBackends and
// tests/AGENTS.md § Build-tag-sensitive tests.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/state"
)

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
