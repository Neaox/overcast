//go:build !nosqlite

package sqs

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// openRawMigrationTestDB opens a *sql.DB directly against a fresh temp file,
// then hand-creates the kv table (normally created by internal/state's own
// migration #1) so legacy fixture rows can be seeded BEFORE
// state.RunMigrations runs the full, globally-registered migration chain —
// mirrors internal/services/cloudwatch/logs/migrations_test.go's
// openRawMigrationTestDB exactly.
func openRawMigrationTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open raw sqlite %q: %v", dbPath, err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS kv (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			PRIMARY KEY (namespace, key)
		)
	`); err != nil {
		t.Fatalf("create kv table: %v", err)
	}
	return db, dbPath
}

func seedKV(t *testing.T, db *sql.DB, namespace, key, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`, namespace, key, value); err != nil {
		t.Fatalf("seed kv [%s/%s]: %v", namespace, key, err)
	}
}

func kvRowExists(t *testing.T, db *sql.DB, namespace, key string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM kv WHERE namespace = ? AND key = ?`, namespace, key).Scan(&n); err != nil {
		t.Fatalf("check kv row [%s/%s]: %v", namespace, key, err)
	}
	return n > 0
}

func mustMarshalMsg(t *testing.T, m *Message) string {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return string(raw)
}

// TestMigrateSQSMessageBlobsToRows_convertsValidBlobsAndSkipsMalformed is the
// storage-plan.md 3.10 migration test: legacy sqs:messages blobs (multiple
// queues, FIFO and standard), a deliberately corrupt blob, and a
// malformed-key row all get handled correctly — valid blobs become rows and
// their kv rows are deleted; the corrupt/malformed ones are skipped (not
// fatal, per CLAUDE.md's malformed-persisted-state isolation rule) and their
// kv rows are left alone.
func TestMigrateSQSMessageBlobsToRows_convertsValidBlobsAndSkipsMalformed(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)
	ctx := context.Background()
	const region = "us-east-1"

	visibleAt := time.UnixMilli(1_700_000_000_000).UTC()

	// A standard-queue message.
	stdMsg := &Message{
		MessageID:    "std-1",
		Body:         "hello standard",
		MD5OfBody:    "d41d8cd98f00b204e9800998ecf8427e",
		VisibleAfter: visibleAt,
	}
	stdKey := serviceutil.RegionKey(region, messageKey("standard-queue", stdMsg.MessageID))
	seedKV(t, db, nsMessages, stdKey, mustMarshalMsg(t, stdMsg))

	// A FIFO-queue message with a sequence number, to prove the conversion
	// preserves group/sequence structural columns.
	fifoMsg := &Message{
		MessageID:      "fifo-1",
		Body:           "hello fifo",
		MD5OfBody:      "d41d8cd98f00b204e9800998ecf8427e",
		VisibleAfter:   visibleAt,
		MessageGroupId: "group-a",
		SequenceNumber: "42",
	}
	fifoKey := serviceutil.RegionKey(region, messageKey("fifo-queue.fifo", fifoMsg.MessageID))
	seedKV(t, db, nsMessages, fifoKey, mustMarshalMsg(t, fifoMsg))

	// A blob that fails to JSON-decode — must be skipped, not fatal.
	corruptKey := serviceutil.RegionKey(region, messageKey("standard-queue", "corrupt-1"))
	seedKV(t, db, nsMessages, corruptKey, `{not valid json`)

	// A key that doesn't split into exactly 3 parts — must be skipped.
	badKey := "malformed-key-with-no-slashes"
	seedKV(t, db, nsMessages, badKey, mustMarshalMsg(t, &Message{MessageID: "whatever"}))

	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < migrationSQSMessagesConversionVersion {
		t.Fatalf("user_version = %d, want >= %d", version, migrationSQSMessagesConversionVersion)
	}

	// The standard message became a row with the expected structural columns.
	var gotVisibleAt, gotSeq int64
	var gotGroup, gotJSON string
	if err := db.QueryRow(`
		SELECT visible_at, message_group_id, sequence_number, message_json
		FROM sqs_messages WHERE region = ? AND queue_name = ? AND message_id = ?
	`, region, "standard-queue", "std-1").Scan(&gotVisibleAt, &gotGroup, &gotSeq, &gotJSON); err != nil {
		t.Fatalf("query migrated standard message: %v", err)
	}
	if gotVisibleAt != visibleAt.UnixMilli() {
		t.Errorf("visible_at = %d, want %d", gotVisibleAt, visibleAt.UnixMilli())
	}
	if gotGroup != "" {
		t.Errorf("message_group_id = %q, want empty for standard queue", gotGroup)
	}
	if gotSeq != 0 {
		t.Errorf("sequence_number = %d, want 0 for standard queue", gotSeq)
	}
	var decoded Message
	if err := json.Unmarshal([]byte(gotJSON), &decoded); err != nil {
		t.Fatalf("unmarshal migrated message_json: %v", err)
	}
	if decoded.Body != stdMsg.Body {
		t.Errorf("migrated body = %q, want %q", decoded.Body, stdMsg.Body)
	}

	// The FIFO message preserved its group and sequence number.
	if err := db.QueryRow(`
		SELECT message_group_id, sequence_number
		FROM sqs_messages WHERE region = ? AND queue_name = ? AND message_id = ?
	`, region, "fifo-queue.fifo", "fifo-1").Scan(&gotGroup, &gotSeq); err != nil {
		t.Fatalf("query migrated fifo message: %v", err)
	}
	if gotGroup != "group-a" {
		t.Errorf("message_group_id = %q, want group-a", gotGroup)
	}
	if gotSeq != 42 {
		t.Errorf("sequence_number = %d, want 42", gotSeq)
	}

	// The corrupt and malformed-key rows produced no sqs_messages rows.
	var corruptCount, badKeyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqs_messages WHERE message_id = 'corrupt-1'`).Scan(&corruptCount); err != nil {
		t.Fatalf("count corrupt rows: %v", err)
	}
	if corruptCount != 0 {
		t.Fatalf("expected 0 rows migrated from corrupt blob, got %d", corruptCount)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqs_messages WHERE message_id = 'whatever'`).Scan(&badKeyCount); err != nil {
		t.Fatalf("count bad-key rows: %v", err)
	}
	if badKeyCount != 0 {
		t.Fatalf("expected 0 rows migrated from malformed-key row, got %d", badKeyCount)
	}

	// Migrated kv rows are gone; skipped ones (corrupt, malformed key) remain.
	if kvRowExists(t, db, nsMessages, stdKey) {
		t.Error("expected standard message blob deleted from kv after migration")
	}
	if kvRowExists(t, db, nsMessages, fifoKey) {
		t.Error("expected fifo message blob deleted from kv after migration")
	}
	if !kvRowExists(t, db, nsMessages, corruptKey) {
		t.Error("expected corrupt blob left in kv (skipped, not deleted)")
	}
	if !kvRowExists(t, db, nsMessages, badKey) {
		t.Error("expected malformed-key row left in kv (skipped, not deleted)")
	}
}

// TestMigrateSQSMessageBlobsToRows_noLegacyData_isNoOp proves the migration
// is harmless (and doesn't error) on a fresh database with nothing to
// convert — the common case on every install after the first.
func TestMigrateSQSMessageBlobsToRows_noLegacyData_isNoOp(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)
	ctx := context.Background()

	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqs_messages`).Scan(&count); err != nil {
		t.Fatalf("count sqs_messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows, got %d", count)
	}

	// Running again is a no-op (idempotent — nothing pending).
	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations (second run): %v", err)
	}
}

// TestSQSMessagesTable_indexExists confirms the crucial
// (region, queue_name, visible_at) index storage-plan.md 3.10 calls for is
// actually created by the migration.
func TestSQSMessagesTable_indexExists(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)
	ctx := context.Background()

	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_sqs_messages_visible'`).Scan(&name)
	if err != nil {
		t.Fatalf("expected idx_sqs_messages_visible to exist: %v", err)
	}
}
