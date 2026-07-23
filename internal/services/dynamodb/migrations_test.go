//go:build !nosqlite

package dynamodb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// openRawMigrationTestDB opens a *sql.DB directly against a fresh temp file,
// using the same DSN shape as internal/state/sqlite.go, then hand-creates the
// kv table (normally created by internal/state's own migration #1) so this
// package's migrations can be exercised directly against the raw DB, the
// same way internal/services/cloudwatch/logs/migrations_test.go does for the
// logs_events table.
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

// TestRunMigrations_freshDatabase_createsDynamoDBTables is the storage-plan.md
// 3.9 core test: a brand-new database reaches a user_version at or past this
// package's migrations and ends up with both dedicated tables present and
// usable.
func TestRunMigrations_freshDatabase_createsDynamoDBTables(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)
	ctx := context.Background()

	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < migrationDynamoDBStreamsTableVersion {
		t.Fatalf("user_version = %d, want >= %d", version, migrationDynamoDBStreamsTableVersion)
	}

	if _, err := db.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES ('T', 'h', 's', '{}')`); err != nil {
		t.Fatalf("insert into dynamodb_items after migration: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dynamodb_stream_records (table_name, event_name, keys_json, created_at) VALUES ('T', 'INSERT', '{}', 1)`); err != nil {
		t.Fatalf("insert into dynamodb_stream_records after migration: %v", err)
	}
}

// TestRunMigrations_preExistingBareDynamoDBTables_adoptsCleanly simulates a
// database whose dynamodb_items/dynamodb_stream_records tables were created
// by the OLD sync.Once-guarded CREATE TABLE IF NOT EXISTS path (predating
// this migration runner) — user_version was never touched by that path, so
// it starts at 0 for these tables even though the tables (and data) already
// exist. Adopting such a database must not error and must not lose data,
// matching internal/state/migrate_test.go's
// TestRunMigrations_preExistingBareKVDatabase_adoptsCleanly pattern.
func TestRunMigrations_preExistingBareDynamoDBTables_adoptsCleanly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")

	setup, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open raw sqlite %q: %v", dbPath, err)
	}
	if _, err := setup.Exec(`
		CREATE TABLE IF NOT EXISTS kv (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			PRIMARY KEY (namespace, key)
		)
	`); err != nil {
		t.Fatalf("legacy create kv table: %v", err)
	}
	// The exact old bare DDL that used to live in item_store.go's
	// sqlItemBackend.init() and stream_store.go's sqlStreamBackend.init().
	if _, err := setup.Exec(`
		CREATE TABLE IF NOT EXISTS dynamodb_items (
			table_name  TEXT NOT NULL,
			hash_key    TEXT NOT NULL,
			sort_key    TEXT NOT NULL DEFAULT '',
			item_json   TEXT NOT NULL,
			PRIMARY KEY (table_name, hash_key, sort_key)
		)
	`); err != nil {
		t.Fatalf("legacy create dynamodb_items table: %v", err)
	}
	if _, err := setup.Exec(createStreamRecordsTable); err != nil {
		t.Fatalf("legacy create dynamodb_stream_records table: %v", err)
	}
	if _, err := setup.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES ('Music', 'artist-1', '', '{"pk":{"S":"artist-1"}}')`); err != nil {
		t.Fatalf("legacy insert into dynamodb_items: %v", err)
	}
	if _, err := setup.Exec(`INSERT INTO dynamodb_stream_records (table_name, event_name, keys_json, created_at) VALUES ('Music', 'INSERT', '{"pk":{"S":"artist-1"}}', 1000)`); err != nil {
		t.Fatalf("legacy insert into dynamodb_stream_records: %v", err)
	}
	var legacyVersion int
	if err := setup.QueryRow(`PRAGMA user_version`).Scan(&legacyVersion); err != nil {
		t.Fatalf("read legacy user_version: %v", err)
	}
	if legacyVersion != 0 {
		t.Fatalf("legacy db user_version = %d, want 0", legacyVersion)
	}
	setup.Close()

	// Reopen fresh (as the runner would on next startup) and migrate.
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("reopen raw sqlite %q: %v", dbPath, err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations on legacy db: %v", err)
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < migrationDynamoDBStreamsTableVersion {
		t.Fatalf("user_version after adoption = %d, want >= %d", version, migrationDynamoDBStreamsTableVersion)
	}

	var itemJSON string
	if err := db.QueryRow(`SELECT item_json FROM dynamodb_items WHERE table_name = 'Music' AND hash_key = 'artist-1' AND sort_key = ''`).Scan(&itemJSON); err != nil {
		t.Fatalf("legacy item row missing after migration: %v", err)
	}
	if itemJSON != `{"pk":{"S":"artist-1"}}` {
		t.Fatalf("legacy item_json = %q, want unchanged", itemJSON)
	}

	var eventName string
	if err := db.QueryRow(`SELECT event_name FROM dynamodb_stream_records WHERE table_name = 'Music'`).Scan(&eventName); err != nil {
		t.Fatalf("legacy stream record missing after migration: %v", err)
	}
	if eventName != "INSERT" {
		t.Fatalf("legacy stream record event_name = %q, want INSERT", eventName)
	}

	// Running again is idempotent — nothing pending, no error, data intact.
	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations (second run): %v", err)
	}
}

// TestSQLItemBackend_DeferredDBResolution_DoesNotBlockConstruction proves the
// lazy-dbFn-resolution behavior newSQLItemBackend documents still holds after
// removing the CREATE TABLE IF NOT EXISTS from init(): dbFn is not invoked at
// construction time, only on first backend method call, and the table it
// eventually resolves against was created by the migration runner rather
// than by the backend itself.
func TestSQLItemBackend_DeferredDBResolution_DoesNotBlockConstruction(t *testing.T) {
	var called bool
	var resolvedDB *sql.DB
	dbFn := func() *sql.DB {
		called = true
		return resolvedDB
	}

	backend := newSQLItemBackend(dbFn)
	if called {
		t.Fatal("newSQLItemBackend must not call dbFn during construction")
	}

	// Point dbFn at a real, migrated database now, simulating the DB
	// becoming available only once the backing store's background
	// open+migrate completes.
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
	resolvedDB = hybrid.DB() // blocks until seed/migrate completes, per DB()'s doc comment

	ctx := context.Background()
	item := Item{"pk": attrValue{"S": "v1"}}
	if err := backend.put(ctx, "T", "h", "", item); err != nil {
		t.Fatalf("put (first call resolves dbFn): %v", err)
	}
	if !called {
		t.Fatal("expected dbFn to be resolved on first backend method call")
	}

	got, found, err := backend.get(ctx, "T", "h", "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found {
		t.Fatal("expected item to be found")
	}
	if got["pk"]["S"] != "v1" {
		t.Fatalf("unexpected item: %#v", got)
	}
}

// TestSQLStreamBackend_DeferredDBResolution_DoesNotBlockConstruction mirrors
// TestSQLItemBackend_DeferredDBResolution_DoesNotBlockConstruction for the
// stream backend.
func TestSQLStreamBackend_DeferredDBResolution_DoesNotBlockConstruction(t *testing.T) {
	var called bool
	var resolvedDB *sql.DB
	dbFn := func() *sql.DB {
		called = true
		return resolvedDB
	}

	backend := newSQLStreamBackend(dbFn)
	if called {
		t.Fatal("newSQLStreamBackend must not call dbFn during construction")
	}

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
	resolvedDB = hybrid.DB()

	ctx := context.Background()
	rec := &StreamRecord{EventName: "INSERT", Keys: Item{"pk": attrValue{"S": "v1"}}, CreatedAt: 1000}
	if err := backend.append(ctx, "T", rec); err != nil {
		t.Fatalf("append (first call resolves dbFn): %v", err)
	}
	if !called {
		t.Fatal("expected dbFn to be resolved on first backend method call")
	}
	if rec.SequenceNumber == 0 {
		t.Fatal("expected a non-zero assigned SequenceNumber")
	}

	latest, err := backend.latest(ctx, "T")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest != rec.SequenceNumber {
		t.Fatalf("latest = %d, want %d", latest, rec.SequenceNumber)
	}
}

// TestNewItemBackendFor_TableAlreadyExists_ViaHybridStore is an end-to-end
// check that the item backend built the normal way (Service.New's path,
// mirrored here) works against a fresh HybridStore whose dynamodb_items
// table now only ever gets created by the migration runner, never by the
// backend's own init().
func TestNewItemBackendFor_TableAlreadyExists_ViaHybridStore(t *testing.T) {
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

	backend := newItemBackendFor(state.Unwrap(hybrid, serviceName))
	if _, ok := backend.(*sqlItemBackend); !ok {
		t.Fatalf("expected sqlItemBackend for a SQLite-backed store, got %T", backend)
	}

	ctx := context.Background()
	item := Item{"pk": attrValue{"S": "artist-1"}}
	if err := backend.put(ctx, "Music", "artist-1", "", item); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, found, err := backend.get(ctx, "Music", "artist-1", "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !found || got["pk"]["S"] != "artist-1" {
		t.Fatalf("unexpected get result: found=%v item=%#v", found, got)
	}
}
