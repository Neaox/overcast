//go:build !nosqlite

package logs

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// openRawMigrationTestDB opens a *sql.DB directly against a fresh temp file,
// using the same DSN shape as internal/state/sqlite.go, then hand-creates the
// kv table (normally created by internal/state's own migration #1) so legacy
// fixture rows can be seeded BEFORE state.RunMigrations runs the full,
// globally-registered migration chain (internal/state's kv-table/auto_vacuum
// migrations plus this package's logs_events migrations, both registered via
// each package's own init()).
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

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// TestMigrateLogsEventBlobsToRows_convertsValidBlobsAndSkipsMalformed is the
// storage-plan.md 2.3 migration test: legacy logs:events blobs (multiple
// streams, multiple events each), a deliberately corrupt blob, and an
// orphaned blob with no matching logs:streams record all get handled
// correctly — valid blobs become rows and their kv rows are deleted; the
// corrupt/orphaned ones are skipped (not fatal, per CLAUDE.md's malformed-
// persisted-state isolation rule) and their kv rows are left alone.
func TestMigrateLogsEventBlobsToRows_convertsValidBlobsAndSkipsMalformed(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)
	ctx := context.Background()
	const region = "us-east-1"

	// Two valid streams in the same group, each with multiple events.
	streamAKey := serviceutil.RegionKey(region, streamKey("my-group", "stream-a"))
	streamBKey := serviceutil.RegionKey(region, streamKey("my-group", "stream-b"))
	seedKV(t, db, nsStreams, streamAKey, mustMarshal(t, LogStream{Name: "stream-a"}))
	seedKV(t, db, nsStreams, streamBKey, mustMarshal(t, LogStream{Name: "stream-b"}))

	eventsA := []LogEvent{
		{Timestamp: 1000, Message: "a-1", IngestionTime: 1000},
		{Timestamp: 1001, Message: "a-2", IngestionTime: 1001},
		{Timestamp: 1002, Message: "a-3", IngestionTime: 1002},
	}
	eventsB := []LogEvent{
		{Timestamp: 2000, Message: "b-1", IngestionTime: 2000},
	}
	seedKV(t, db, nsEvents, streamAKey, mustMarshal(t, storedEvents{Events: eventsA}))
	seedKV(t, db, nsEvents, streamBKey, mustMarshal(t, storedEvents{Events: eventsB}))

	// A stream whose events blob is corrupt JSON — must be skipped, not fatal.
	streamCKey := serviceutil.RegionKey(region, streamKey("my-group", "stream-c"))
	seedKV(t, db, nsStreams, streamCKey, mustMarshal(t, LogStream{Name: "stream-c"}))
	seedKV(t, db, nsEvents, streamCKey, `{not valid json`)

	// An orphaned events blob with no matching logs:streams record.
	orphanKey := serviceutil.RegionKey(region, streamKey("my-group", "ghost-stream"))
	seedKV(t, db, nsEvents, orphanKey, mustMarshal(t, storedEvents{Events: []LogEvent{{Timestamp: 3000, Message: "ghost", IngestionTime: 3000}}}))

	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// user_version reached the highest registered migration in this test
	// binary's registry (internal/state's own migrations + this package's).
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < migrationLogsEventsConversionVersion {
		t.Fatalf("user_version = %d, want >= %d", version, migrationLogsEventsConversionVersion)
	}

	// Valid blobs became rows, correctly attributed to their stream.
	assertMigratedEvents := func(group, stream string, want []LogEvent) {
		t.Helper()
		rows, err := db.QueryContext(ctx, `
			SELECT ts, seq, ingestion_ts, message FROM logs_events
			WHERE region = ? AND group_name = ? AND stream_name = ?
			ORDER BY ts, seq
		`, region, group, stream)
		if err != nil {
			t.Fatalf("query logs_events [%s/%s]: %v", group, stream, err)
		}
		defer rows.Close()
		var got []LogEvent
		var seqs []int64
		for rows.Next() {
			var e LogEvent
			var seq int64
			if err := rows.Scan(&e.Timestamp, &seq, &e.IngestionTime, &e.Message); err != nil {
				t.Fatalf("scan logs_events row: %v", err)
			}
			got = append(got, e)
			seqs = append(seqs, seq)
		}
		if len(got) != len(want) {
			t.Fatalf("[%s/%s] got %d events, want %d (%+v)", group, stream, len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("[%s/%s] event %d = %+v, want %+v", group, stream, i, got[i], want[i])
			}
			if seqs[i] != int64(i) {
				t.Fatalf("[%s/%s] event %d seq = %d, want %d", group, stream, i, seqs[i], i)
			}
		}
	}
	assertMigratedEvents("my-group", "stream-a", eventsA)
	assertMigratedEvents("my-group", "stream-b", eventsB)

	// The corrupt and orphaned blobs produced no rows.
	var corruptCount, orphanCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs_events WHERE stream_name = 'stream-c'`).Scan(&corruptCount); err != nil {
		t.Fatalf("count stream-c rows: %v", err)
	}
	if corruptCount != 0 {
		t.Fatalf("expected 0 rows migrated from corrupt blob, got %d", corruptCount)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs_events WHERE stream_name = 'ghost-stream'`).Scan(&orphanCount); err != nil {
		t.Fatalf("count ghost-stream rows: %v", err)
	}
	if orphanCount != 0 {
		t.Fatalf("expected 0 rows migrated from orphaned blob, got %d", orphanCount)
	}

	// Migrated kv rows are gone; skipped ones (corrupt, orphaned) and
	// untouched logs:streams rows remain.
	if kvRowExists(t, db, nsEvents, streamAKey) {
		t.Error("expected stream-a events blob deleted from kv after migration")
	}
	if kvRowExists(t, db, nsEvents, streamBKey) {
		t.Error("expected stream-b events blob deleted from kv after migration")
	}
	if !kvRowExists(t, db, nsEvents, streamCKey) {
		t.Error("expected corrupt blob left in kv (skipped, not deleted)")
	}
	if !kvRowExists(t, db, nsEvents, orphanKey) {
		t.Error("expected orphaned blob left in kv (skipped, not deleted)")
	}
	if !kvRowExists(t, db, nsStreams, streamAKey) || !kvRowExists(t, db, nsStreams, streamBKey) || !kvRowExists(t, db, nsStreams, streamCKey) {
		t.Error("expected logs:streams records untouched by the events migration")
	}
}

// TestMigrateLogsEventBlobsToRows_noLegacyData_isNoOp proves the migration is
// harmless (and doesn't error) on a fresh database with nothing to convert —
// the common case on every install after the first.
func TestMigrateLogsEventBlobsToRows_noLegacyData_isNoOp(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)
	ctx := context.Background()

	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs_events`).Scan(&count); err != nil {
		t.Fatalf("count logs_events: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows, got %d", count)
	}

	// Running again is a no-op (idempotent — nothing pending).
	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations (second run): %v", err)
	}
}
