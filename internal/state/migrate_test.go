//go:build !nosqlite

package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openRawSQLite opens a *sql.DB directly against path using the same DSN
// shape as sqlite.go, without going through SQLiteStore/RunMigrations — used
// to set up "pre-existing database" fixtures and to inspect a database file
// after the runner has touched it.
func openRawSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open raw sqlite %q: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustUserVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	v, err := readUserVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("readUserVersion: %v", err)
	}
	return v
}

// ---- runner mechanics (private list, isolated from the global registry) --

func TestRunMigrations_freshDatabase_reachesHighestVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")
	db := openRawSQLite(t, dbPath)

	if err := runMigrations(context.Background(), db, dbPath, nil, registeredMigrationsSnapshot(t)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	got := mustUserVersion(t, db)
	if got != migrationAutoVacuumVersion {
		t.Fatalf("user_version = %d, want %d", got, migrationAutoVacuumVersion)
	}

	// The kv table must exist and be usable.
	if _, err := db.Exec(`INSERT INTO kv (namespace, key, value) VALUES ('ns', 'k', 'v')`); err != nil {
		t.Fatalf("insert into kv after migration: %v", err)
	}
}

func TestRunMigrations_preExistingBareKVDatabase_adoptsCleanly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")

	// Simulate a database created by the OLD bare migrate(): create the kv
	// table directly, never touch user_version, and write a row.
	setup := openRawSQLite(t, dbPath)
	if _, err := setup.Exec(`
		CREATE TABLE IF NOT EXISTS kv (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			PRIMARY KEY (namespace, key)
		)
	`); err != nil {
		t.Fatalf("legacy create table: %v", err)
	}
	if _, err := setup.Exec(`INSERT INTO kv (namespace, key, value) VALUES ('legacy-ns', 'legacy-key', 'legacy-value')`); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	if got := mustUserVersion(t, setup); got != 0 {
		t.Fatalf("legacy db user_version = %d, want 0", got)
	}
	setup.Close()

	// Now open it fresh (as the runner would on next startup) and migrate.
	db := openRawSQLite(t, dbPath)
	if err := runMigrations(context.Background(), db, dbPath, nil, registeredMigrationsSnapshot(t)); err != nil {
		t.Fatalf("runMigrations on legacy db: %v", err)
	}

	if got := mustUserVersion(t, db); got != migrationAutoVacuumVersion {
		t.Fatalf("user_version after adoption = %d, want %d", got, migrationAutoVacuumVersion)
	}

	var value string
	if err := db.QueryRow(`SELECT value FROM kv WHERE namespace = 'legacy-ns' AND key = 'legacy-key'`).Scan(&value); err != nil {
		t.Fatalf("legacy row missing after migration: %v", err)
	}
	if value != "legacy-value" {
		t.Fatalf("legacy row value = %q, want %q", value, "legacy-value")
	}
}

func TestRunMigrations_idempotent_noOpOnSecondRun(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")

	// Seed a pre-existing table so the first run actually takes a backup
	// (databaseHasTables gates it on "there is something worth protecting") —
	// otherwise this test would trivially see zero backups either way and
	// wouldn't exercise the "no new backup on the no-op re-run" behavior.
	setup := openRawSQLite(t, dbPath)
	if _, err := setup.Exec(`
		CREATE TABLE IF NOT EXISTS kv (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			PRIMARY KEY (namespace, key)
		)
	`); err != nil {
		t.Fatalf("legacy create table: %v", err)
	}
	setup.Close()

	db := openRawSQLite(t, dbPath)
	list := registeredMigrationsSnapshot(t)
	if err := runMigrations(context.Background(), db, dbPath, nil, list); err != nil {
		t.Fatalf("first runMigrations: %v", err)
	}
	firstVersion := mustUserVersion(t, db)

	// A second run must be a no-op: no error, same version, no new backup
	// file for the (now non-existent) pending set.
	if err := runMigrations(context.Background(), db, dbPath, nil, list); err != nil {
		t.Fatalf("second runMigrations: %v", err)
	}
	if got := mustUserVersion(t, db); got != firstVersion {
		t.Fatalf("user_version changed on idempotent re-run: %d -> %d", firstVersion, got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	backups := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".db" && matchesBackupName(e.Name()) {
			backups++
		}
	}
	if backups != 1 {
		t.Fatalf("expected exactly 1 backup file after fresh migration + no-op re-run, got %d: %v", backups, entries)
	}
}

func matchesBackupName(name string) bool {
	return strings.HasPrefix(name, "overcast.db.bak-v")
}

func TestRunMigrations_backupFile_isValidPreMigrationSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")

	setup := openRawSQLite(t, dbPath)
	if _, err := setup.Exec(`
		CREATE TABLE IF NOT EXISTS kv (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			PRIMARY KEY (namespace, key)
		)
	`); err != nil {
		t.Fatalf("legacy create table: %v", err)
	}
	if _, err := setup.Exec(`INSERT INTO kv (namespace, key, value) VALUES ('ns', 'k', 'pre-migration')`); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	setup.Close()

	db := openRawSQLite(t, dbPath)
	if err := runMigrations(context.Background(), db, dbPath, nil, registeredMigrationsSnapshot(t)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	backupPath := dbPath + ".bak-v0"
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected backup file %q: %v", backupPath, err)
	}

	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer backupDB.Close()

	var value string
	if err := backupDB.QueryRow(`SELECT value FROM kv WHERE namespace = 'ns' AND key = 'k'`).Scan(&value); err != nil {
		t.Fatalf("read pre-migration row from backup: %v", err)
	}
	if value != "pre-migration" {
		t.Fatalf("backup row value = %q, want %q", value, "pre-migration")
	}
	// The backup was taken before migration 1 ran, so it must not yet have
	// user_version advanced.
	if got := mustUserVersion(t, backupDB); got != 0 {
		t.Fatalf("backup user_version = %d, want 0 (pre-migration snapshot)", got)
	}
}

// TestRunMigrations_freshDatabase_skipsBackup locks in a deliberate
// optimization beyond the base "backup whenever a migration is pending"
// rule: a database with no tables yet (the common brand-new-install / fresh
// test tempdir case) has nothing worth protecting, so runMigrations skips
// the wal_checkpoint + whole-file copy entirely for it — only a database
// that already has schema/data pays that cost. See databaseHasTables.
func TestRunMigrations_freshDatabase_skipsBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")
	db := openRawSQLite(t, dbPath)

	if err := runMigrations(context.Background(), db, dbPath, nil, registeredMigrationsSnapshot(t)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if _, err := os.Stat(dbPath + ".bak-v0"); !os.IsNotExist(err) {
		t.Fatalf("expected no backup file for a fresh database, stat err: %v", err)
	}
}

func TestRunMigrations_failedMigration_leavesVersionUnchangedAndReturnsNamedError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")
	db := openRawSQLite(t, dbPath)

	failing := Migration{
		Version: 3,
		Name:    "deliberately broken migration",
		Up: func(_ context.Context, tx *sql.Tx) error {
			return errors.New("boom")
		},
	}
	list := append(registeredMigrationsSnapshot(t), failing)

	err := runMigrations(context.Background(), db, dbPath, nil, list)
	if err == nil {
		t.Fatal("expected an error from a failing migration")
	}
	msg := err.Error()
	for _, want := range []string{"3", "deliberately broken migration", "boom"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not identify the failed migration (missing %q)", msg, want)
		}
	}

	// migrations 1 and 2 (which come before the broken version 3) should
	// have committed, but version 3 must not be recorded.
	if got := mustUserVersion(t, db); got != migrationAutoVacuumVersion {
		t.Fatalf("user_version = %d, want %d (failed migration must not advance past its own version)", got, migrationAutoVacuumVersion)
	}
}

func TestRunMigrations_failedMigration_pendingLaterMigrationsNeverRun(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")
	db := openRawSQLite(t, dbPath)

	var laterRan bool
	failing := Migration{
		Version: 3,
		Name:    "broken",
		Up: func(_ context.Context, tx *sql.Tx) error {
			return errors.New("boom")
		},
	}
	later := Migration{
		Version: 4,
		Name:    "should never run",
		Up: func(_ context.Context, tx *sql.Tx) error {
			laterRan = true
			return nil
		},
	}
	list := append(registeredMigrationsSnapshot(t), failing, later)

	if err := runMigrations(context.Background(), db, dbPath, nil, list); err == nil {
		t.Fatal("expected an error")
	}
	if laterRan {
		t.Fatal("a migration after a failed one must not run")
	}
}

// ---- auto_vacuum (migration #2) -------------------------------------------

func TestRunMigrations_autoVacuum_freshDB_readsBackIncremental(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")
	db := openRawSQLite(t, dbPath)

	if err := runMigrations(context.Background(), db, dbPath, nil, registeredMigrationsSnapshot(t)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	var mode int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum mode = %d, want 2 (incremental)", mode)
	}
}

func TestRunMigrations_autoVacuum_upgradedFromPreRunnerState_readsBackIncremental(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")

	setup := openRawSQLite(t, dbPath)
	if _, err := setup.Exec(`
		CREATE TABLE IF NOT EXISTS kv (
			namespace TEXT NOT NULL,
			key       TEXT NOT NULL,
			value     TEXT NOT NULL,
			PRIMARY KEY (namespace, key)
		)
	`); err != nil {
		t.Fatalf("legacy create table: %v", err)
	}
	if _, err := setup.Exec(`INSERT INTO kv (namespace, key, value) VALUES ('ns', 'k', 'v')`); err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	setup.Close()

	db := openRawSQLite(t, dbPath)
	if err := runMigrations(context.Background(), db, dbPath, nil, registeredMigrationsSnapshot(t)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	var mode int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum mode = %d, want 2 (incremental)", mode)
	}
}

// ---- RegisterMigration / global registry -----------------------------------

func TestRegisterMigration_duplicateVersionPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected RegisterMigration to panic on a duplicate version")
		}
	}()
	// migrationKVTableVersion (1) is already registered by this package's
	// own init() — registering it again must panic without corrupting the
	// registry (RegisterMigration checks-then-appends under the same lock).
	RegisterMigration(Migration{
		Version: migrationKVTableVersion,
		Name:    "duplicate of the real migration 1",
		Up:      func(_ context.Context, _ *sql.Tx) error { return nil },
	})
}

func TestRegisterMigration_nilUpPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected RegisterMigration to panic on a nil Up func")
		}
	}()
	RegisterMigration(Migration{Version: 987654, Name: "nil up"})
}

// TestRunMigrations_usesGlobalRegistry exercises the public entry point
// (RunMigrations, backed by the real package-level registry) end to end,
// separately from the runMigrations(..., list) mechanics tests above.
func TestRunMigrations_usesGlobalRegistry(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "overcast.db")
	db := openRawSQLite(t, dbPath)
	defer db.Close()

	if err := RunMigrations(context.Background(), db, dbPath, nil); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if got := mustUserVersion(t, db); got != migrationAutoVacuumVersion {
		t.Fatalf("user_version = %d, want %d", got, migrationAutoVacuumVersion)
	}
}

// TestRunMigrations_perStoreOverrideDirs proves the runner works correctly
// when invoked against independent store instances backed by different
// files (as happens for OVERCAST_STATE_<SVC> override directories, each
// with their own <DataDir>/<svc>/overcast.db) — each gets its own
// independent version tracking and backup.
func TestRunMigrations_perStoreOverrideDirs(t *testing.T) {
	base := t.TempDir()
	dirA := filepath.Join(base, "svc-a")
	dirB := filepath.Join(base, "svc-b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPathA := filepath.Join(dirA, "overcast.db")
	dbPathB := filepath.Join(dirB, "overcast.db")

	// Seed both with a pre-existing table so each takes its own backup
	// (databaseHasTables gates backups on there being something to protect).
	for _, p := range []string{dbPathA, dbPathB} {
		setup := openRawSQLite(t, p)
		if _, err := setup.Exec(`
			CREATE TABLE IF NOT EXISTS kv (
				namespace TEXT NOT NULL,
				key       TEXT NOT NULL,
				value     TEXT NOT NULL,
				PRIMARY KEY (namespace, key)
			)
		`); err != nil {
			t.Fatalf("legacy create table %q: %v", p, err)
		}
		setup.Close()
	}

	dbA := openRawSQLite(t, dbPathA)
	dbB := openRawSQLite(t, dbPathB)

	if err := RunMigrations(context.Background(), dbA, dbPathA, nil); err != nil {
		t.Fatalf("migrate store A: %v", err)
	}
	if err := RunMigrations(context.Background(), dbB, dbPathB, nil); err != nil {
		t.Fatalf("migrate store B: %v", err)
	}

	if got := mustUserVersion(t, dbA); got != migrationAutoVacuumVersion {
		t.Fatalf("store A user_version = %d, want %d", got, migrationAutoVacuumVersion)
	}
	if got := mustUserVersion(t, dbB); got != migrationAutoVacuumVersion {
		t.Fatalf("store B user_version = %d, want %d", got, migrationAutoVacuumVersion)
	}
	if _, err := os.Stat(dbPathA + ".bak-v0"); err != nil {
		t.Fatalf("store A missing its own backup: %v", err)
	}
	if _, err := os.Stat(dbPathB + ".bak-v0"); err != nil {
		t.Fatalf("store B missing its own backup: %v", err)
	}
}

// ---- SQLiteStore integration (end-to-end through runMigrate) --------------

func TestSQLiteStore_wiresMigrationRunner(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.Close()

	if err := s.ensureReady(context.Background()); err != nil {
		t.Fatalf("ensureReady: %v", err)
	}

	got := mustUserVersion(t, s.db)
	if got != migrationAutoVacuumVersion {
		t.Fatalf("user_version = %d, want %d", got, migrationAutoVacuumVersion)
	}

	if err := s.Set(context.Background(), "ns", "k", "v"); err != nil {
		t.Fatalf("Set after migration: %v", err)
	}
}

// registeredMigrationsSnapshot returns a copy of the real, production
// migration list (kv table + auto_vacuum) for tests that want the actual
// registered migrations without depending on global-registry mutation
// ordering between test functions.
func registeredMigrationsSnapshot(t *testing.T) []Migration {
	t.Helper()
	migrationsMu.Lock()
	defer migrationsMu.Unlock()
	out := make([]Migration, len(registeredMigrations))
	copy(out, registeredMigrations)
	return out
}
