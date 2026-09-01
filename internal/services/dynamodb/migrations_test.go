//go:build !nosqlite

package dynamodb

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/state"
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

// TestMigration_ReencodeNumericKeys_reencodesExistingRows is item 4's
// evidence: a database with pre-existing dynamodb_items rows for a
// Number-sort-keyed table, written before migration 22 existed (so
// hash_key/sort_key hold raw, unencoded decimal text), must come out of
// RunMigrations with those rows re-encoded — an ORDER BY sort_key query
// (exactly what sqlItemBackend.scanPage/scanAll issue) now returns numeric
// order, not lexicographic order on the old raw text.
func TestMigration_ReencodeNumericKeys_reencodesExistingRows(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS dynamodb_items (
			table_name  TEXT NOT NULL,
			hash_key    TEXT NOT NULL,
			sort_key    TEXT NOT NULL DEFAULT '',
			item_json   TEXT NOT NULL,
			PRIMARY KEY (table_name, hash_key, sort_key)
		)
	`); err != nil {
		t.Fatalf("create dynamodb_items table: %v", err)
	}

	// A Number-sort-keyed table ("scores"), pre-existing rows with raw
	// (unencoded) decimal sort_key text — exactly what putItem wrote before
	// resolveStorageKeys started encoding Number-typed key components.
	scoresSchema := `{"TableName":"scores","KeySchema":[{"AttributeName":"game","KeyType":"HASH"},{"AttributeName":"score","KeyType":"RANGE"}],"AttributeDefinitions":[{"AttributeName":"game","AttributeType":"S"},{"AttributeName":"score","AttributeType":"N"}],"TableStatus":"ACTIVE"}`
	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`, "dynamodb:tables", "us-east-1/scores", scoresSchema); err != nil {
		t.Fatalf("insert scores table schema: %v", err)
	}
	scoreRows := []struct{ sort, json string }{
		{"50", `{"game":{"S":"g1"},"score":{"N":"50"}}`},
		{"5", `{"game":{"S":"g1"},"score":{"N":"5"}}`},
		{"10", `{"game":{"S":"g1"},"score":{"N":"10"}}`},
	}
	for _, r := range scoreRows {
		if _, err := db.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES (?, ?, ?, ?)`,
			"scores", "g1", r.sort, r.json); err != nil {
			t.Fatalf("insert scores row (sort=%s): %v", r.sort, err)
		}
	}

	// A pure-String-keyed table ("users") — must be a byte-for-byte no-op.
	usersSchema := `{"TableName":"users","KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],"TableStatus":"ACTIVE"}`
	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`, "dynamodb:tables", "us-east-1/users", usersSchema); err != nil {
		t.Fatalf("insert users table schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES (?, ?, ?, ?)`,
		"users", "alice", "", `{"id":{"S":"alice"}}`); err != nil {
		t.Fatalf("insert users row: %v", err)
	}

	ctx := context.Background()
	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < migrationDynamoDBReencodeNumericKeysVersion {
		t.Fatalf("user_version = %d, want >= %d", version, migrationDynamoDBReencodeNumericKeysVersion)
	}

	// Then: a plain ORDER BY sort_key (exactly what scanAll/scanPage issue)
	// now returns the scores in NUMERIC order (5, 10, 50), not the
	// lexicographic order the raw text would have given ("10", "5", "50").
	// The full chain also runs the region-qualification migration (24), so
	// post-chain rows are addressed by "<region>/<name>" — the key
	// dynamoStore.tableKey mints — not by the bare name they were seeded under.
	rows, err := db.Query(`SELECT item_json FROM dynamodb_items WHERE table_name = 'us-east-1/scores' ORDER BY hash_key, sort_key`)
	if err != nil {
		t.Fatalf("query re-encoded rows: %v", err)
	}
	defer rows.Close()
	var gotOrder []string
	for rows.Next() {
		var itemJSON string
		if err := rows.Scan(&itemJSON); err != nil {
			t.Fatalf("scan re-encoded row: %v", err)
		}
		var item struct {
			Score struct {
				N string `json:"N"`
			} `json:"score"`
		}
		if err := json.Unmarshal([]byte(itemJSON), &item); err != nil {
			t.Fatalf("unmarshal item_json %q: %v", itemJSON, err)
		}
		gotOrder = append(gotOrder, item.Score.N)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate re-encoded rows: %v", err)
	}
	want := []string{"5", "10", "50"}
	if len(gotOrder) != len(want) {
		t.Fatalf("got %d rows, want %d: %v", len(gotOrder), len(want), gotOrder)
	}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Fatalf("ORDER BY sort_key after migration = %v, want numeric order %v", gotOrder, want)
		}
	}

	// And: the stored sort_key values are no longer the raw "5"/"10"/"50" —
	// confirms this test actually exercised the re-encoding, not a no-op.
	var rawSortKey string
	if err := db.QueryRow(`SELECT sort_key FROM dynamodb_items WHERE table_name = 'us-east-1/scores' AND hash_key = 'g1' AND json_extract(item_json, '$.score.N') = '5'`).Scan(&rawSortKey); err != nil {
		t.Fatalf("read re-encoded sort_key for score=5: %v", err)
	}
	if rawSortKey == "5" {
		t.Fatal("sort_key for score=5 is still the raw \"5\" — migration did not re-encode it")
	}

	// Then: the pure-String-keyed table's row is untouched (no-op).
	var usersHashKey string
	if err := db.QueryRow(`SELECT hash_key FROM dynamodb_items WHERE table_name = 'us-east-1/users'`).Scan(&usersHashKey); err != nil {
		t.Fatalf("read users row: %v", err)
	}
	if usersHashKey != "alice" {
		t.Fatalf("users hash_key = %q, want unchanged \"alice\" (String-keyed table must be a no-op)", usersHashKey)
	}

	// Running again is idempotent.
	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations (second run): %v", err)
	}
}

// TestMigration_CreateIndexEntriesTable_BackfillsExistingGSIs is
// dynamodb-gsi-design.md section 3's migration-time backfill: a table whose
// schema already declares a GSI (created, or given the GSI via UpdateTable,
// before this migration ever ran) must come out of RunMigrations with
// dynamodb_index_entries populated for every existing item that satisfies
// the GSI's key — not silently start the new structure out empty despite
// having qualifying data. Also covers the isolation requirement: a
// malformed table-schema record and a malformed item row must each be
// skipped rather than failing the whole migration (CLAUDE.md's isolation
// rule).
func TestMigration_CreateIndexEntriesTable_BackfillsExistingGSIs(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS dynamodb_items (
			table_name  TEXT NOT NULL,
			hash_key    TEXT NOT NULL,
			sort_key    TEXT NOT NULL DEFAULT '',
			item_json   TEXT NOT NULL,
			PRIMARY KEY (table_name, hash_key, sort_key)
		)
	`); err != nil {
		t.Fatalf("create dynamodb_items table: %v", err)
	}

	// "Orders" already declares a GSI (gsi-customer, hash=customerId) —
	// simulating a table that had a GSI before this migration existed.
	ordersSchema := `{"TableName":"Orders","KeySchema":[{"AttributeName":"orderId","KeyType":"HASH"}],"AttributeDefinitions":[{"AttributeName":"orderId","AttributeType":"S"},{"AttributeName":"customerId","AttributeType":"S"}],"TableStatus":"ACTIVE","GlobalSecondaryIndexes":[{"IndexName":"gsi-customer","KeySchema":[{"AttributeName":"customerId","KeyType":"HASH"}],"Projection":{"ProjectionType":"ALL"}}]}`
	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`, nsTables, "us-east-1/Orders", ordersSchema); err != nil {
		t.Fatalf("insert Orders table schema: %v", err)
	}

	// A malformed table-schema record — must be skipped, not fail the migration.
	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`, nsTables, "us-east-1/Broken", `{not valid json`); err != nil {
		t.Fatalf("insert malformed table schema: %v", err)
	}

	// A table with no GSIs — must be a no-op (never even read its items).
	usersSchema := `{"TableName":"Users","KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],"TableStatus":"ACTIVE"}`
	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`, nsTables, "us-east-1/Users", usersSchema); err != nil {
		t.Fatalf("insert Users table schema: %v", err)
	}

	// Orders items: two dense (satisfy the GSI key), one sparse (no
	// customerId — must get no index row), one malformed JSON row (must be
	// skipped, not fail the migration).
	ordersItems := []struct{ hash, json string }{
		{"o1", `{"orderId":{"S":"o1"},"customerId":{"S":"c1"}}`},
		{"o2", `{"orderId":{"S":"o2"},"customerId":{"S":"c2"}}`},
		{"o3", `{"orderId":{"S":"o3"}}`}, // sparse — no customerId
		{"o4", `{not valid item json`},   // malformed — must be isolated
	}
	for _, r := range ordersItems {
		if _, err := db.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES (?, ?, ?, ?)`,
			"Orders", r.hash, "", r.json); err != nil {
			t.Fatalf("insert Orders item (hash=%s): %v", r.hash, err)
		}
	}

	ctx := context.Background()
	if err := state.RunMigrations(ctx, db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < migrationDynamoDBIndexEntriesTableVersion {
		t.Fatalf("user_version = %d, want >= %d (malformed records must not block later migrations)", version, migrationDynamoDBIndexEntriesTableVersion)
	}

	// dynamodb_index_entries exists and has exactly the 2 dense items'
	// rows — not 3 (the sparse item), not 4 (the malformed item didn't
	// silently become a row), not 0 (the migration didn't just skip
	// everything on the first error it hit).
	var n int
	// Region-qualified by migration 24, which runs after this backfill.
	if err := db.QueryRow(`SELECT COUNT(*) FROM dynamodb_index_entries WHERE table_name = 'us-east-1/Orders' AND index_name = 'gsi-customer'`).Scan(&n); err != nil {
		t.Fatalf("count dynamodb_index_entries rows: %v", err)
	}
	if n != 2 {
		t.Fatalf("dynamodb_index_entries row count for Orders/gsi-customer = %d, want 2 (o1, o2 only — not the sparse o3 or malformed o4)", n)
	}

	var indexHashes []string
	rows, err := db.Query(`SELECT index_hash FROM dynamodb_index_entries WHERE table_name = 'us-east-1/Orders' AND index_name = 'gsi-customer' ORDER BY index_hash`)
	if err != nil {
		t.Fatalf("query index_hash values: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan index_hash: %v", err)
		}
		indexHashes = append(indexHashes, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_hash rows: %v", err)
	}
	want := []string{"c1", "c2"}
	if len(indexHashes) != len(want) {
		t.Fatalf("index_hash values = %v, want %v", indexHashes, want)
	}
	for i := range want {
		if indexHashes[i] != want[i] {
			t.Fatalf("index_hash values = %v, want %v", indexHashes, want)
		}
	}

	// Users (no GSIs) contributed nothing.
	var usersN int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dynamodb_index_entries WHERE table_name = 'Users'`).Scan(&usersN); err != nil {
		t.Fatalf("count Users index entries: %v", err)
	}
	if usersN != 0 {
		t.Fatalf("Users (no GSIs) must contribute 0 index entries, got %d", usersN)
	}

	// Running again is idempotent.
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

// TestMigration_RegionQualifyTableKeys_rewritesPreExistingRows is issue #673's
// storage-compatibility test: a database written before item/index/stream
// storage was region-scoped must come out the other side with its data still
// reachable — under the region its table was actually created in.
func TestMigration_RegionQualifyTableKeys_rewritesPreExistingRows(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)

	// Given: a pre-#673 database — table descriptors already carry a region
	// prefix, but the three dedicated tables key on the bare table name.
	seedPreRegionSchema(t, db)

	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`,
		"dynamodb:tables", "eu-west-1/orders",
		`{"TableName":"orders","KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],`+
			`"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],"TableStatus":"ACTIVE"}`,
	); err != nil {
		t.Fatalf("insert orders schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES (?, ?, ?, ?)`,
		"orders", "o1", "", `{"id":{"S":"o1"}}`); err != nil {
		t.Fatalf("insert orders item: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dynamodb_index_entries
		(table_name, index_name, index_hash, index_sort, base_hash, base_sort, item_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"orders", "by-status", "OPEN", "", "o1", "", `{"id":{"S":"o1"},"status":{"S":"OPEN"}}`); err != nil {
		t.Fatalf("insert orders index entry: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dynamodb_stream_records
		(table_name, event_name, keys_json, new_image_json, old_image_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"orders", "INSERT", `{"id":{"S":"o1"}}`, `null`, `null`, 1); err != nil {
		t.Fatalf("insert orders stream record: %v", err)
	}

	// And: an orphan row whose table name no descriptor declares.
	if _, err := db.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES (?, ?, ?, ?)`,
		"ghost", "g1", "", `{"id":{"S":"g1"}}`); err != nil {
		t.Fatalf("insert orphan item: %v", err)
	}

	// When: the migration chain runs
	if err := state.RunMigrations(context.Background(), db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < migrationDynamoDBRegionQualifyKeysVersion {
		t.Fatalf("user_version = %d, want >= %d", version, migrationDynamoDBRegionQualifyKeysVersion)
	}

	// Then: every keyspace is addressed by the region-qualified key the
	// running code now mints, so the data is still reachable from eu-west-1.
	for _, tc := range []struct{ table, where string }{
		{"dynamodb_items", `table_name = 'eu-west-1/orders'`},
		{"dynamodb_index_entries", `table_name = 'eu-west-1/orders'`},
		{"dynamodb_stream_records", `table_name = 'eu-west-1/orders'`},
	} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + tc.table + ` WHERE ` + tc.where).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tc.table, err)
		}
		if n != 1 {
			t.Errorf("%s WHERE %s: got %d rows, want 1", tc.table, tc.where, n)
		}
	}

	// And: nothing is left under the bare name.
	var stale int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dynamodb_items WHERE table_name = 'orders'`).Scan(&stale); err != nil {
		t.Fatalf("count stale rows: %v", err)
	}
	if stale != 0 {
		t.Errorf("dynamodb_items still has %d rows under the bare name 'orders'", stale)
	}

	// And: the orphan is preserved verbatim rather than dropped or guessed at
	// — unreachable, but not lost (AGENTS.md's malformed-persisted-state rule).
	var orphan int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dynamodb_items WHERE table_name = 'ghost'`).Scan(&orphan); err != nil {
		t.Fatalf("count orphan rows: %v", err)
	}
	if orphan != 1 {
		t.Errorf("orphan row count = %d, want 1 (left untouched)", orphan)
	}
}

// TestMigration_RegionQualifyTableKeys_sameNameInTwoRegions pins the one case
// the pre-#673 layout makes unrecoverable: two regions declared the same table
// name and therefore shared one set of rows. Nothing on disk says which write
// came from where, so the rows go to the lexicographically first region,
// deterministically, and the other region starts empty.
func TestMigration_RegionQualifyTableKeys_sameNameInTwoRegions(t *testing.T) {
	db, dbPath := openRawMigrationTestDB(t)
	seedPreRegionSchema(t, db)

	schema := `{"TableName":"shared","KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],` +
		`"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],"TableStatus":"ACTIVE"}`
	for _, key := range []string{"us-west-2/shared", "eu-west-1/shared"} {
		if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES (?, ?, ?)`,
			"dynamodb:tables", key, schema); err != nil {
			t.Fatalf("insert %s schema: %v", key, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO dynamodb_items (table_name, hash_key, sort_key, item_json) VALUES (?, ?, ?, ?)`,
		"shared", "s1", "", `{"id":{"S":"s1"}}`); err != nil {
		t.Fatalf("insert shared item: %v", err)
	}

	if err := state.RunMigrations(context.Background(), db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var gotKey string
	if err := db.QueryRow(`SELECT table_name FROM dynamodb_items`).Scan(&gotKey); err != nil {
		t.Fatalf("read migrated key: %v", err)
	}
	if gotKey != "eu-west-1/shared" {
		t.Errorf("table_name = %q, want %q (lexicographically first declaring region)", gotKey, "eu-west-1/shared")
	}
}

// seedPreRegionSchema creates the three dedicated tables with the schema a
// pre-#673 database would already have, so a migration test can insert rows
// under bare table names before the chain runs.
func seedPreRegionSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS dynamodb_items (
			table_name TEXT NOT NULL, hash_key TEXT NOT NULL, sort_key TEXT NOT NULL DEFAULT '',
			item_json TEXT NOT NULL, PRIMARY KEY (table_name, hash_key, sort_key))`,
		`CREATE TABLE IF NOT EXISTS dynamodb_index_entries (
			table_name TEXT NOT NULL, index_name TEXT NOT NULL, index_hash TEXT NOT NULL,
			index_sort TEXT NOT NULL DEFAULT '', base_hash TEXT NOT NULL, base_sort TEXT NOT NULL DEFAULT '',
			item_json TEXT NOT NULL,
			PRIMARY KEY (table_name, index_name, index_hash, index_sort, base_hash, base_sort))`,
		createStreamRecordsTable,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("seed pre-region schema: %v", err)
		}
	}
}
