//go:build !nosqlite

package state

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Migration is one versioned, ordered schema/data change applied to the
// shared overcast.db SQLite file. Version must be unique across the whole
// binary and migrations run in ascending Version order regardless of
// registration order, so package init() order does not matter.
//
// Version numbering is a reserved-range convention, not auto-assigned, so
// packages can pick a version without coordinating with every other
// package's source at once:
//
//   - 1-9:   internal/state core (kv table, auto_vacuum). Only 1 and 2 are
//     used today — see migrationKVTableVersion and migrationAutoVacuumVersion
//     below.
//   - 10-19: reserved for the CloudWatch Logs events table (storage-plan.md
//     Phase 2 item 2.3), not yet registered.
//   - 20+:   free for future dedicated tables (e.g. the DynamoDB retrofit in
//     storage-plan.md item 3.9). Claim the next unused decade and document it
//     here when a package registers into it.
//
// There are no down-migrations. The runner takes a file-copy backup of
// overcast.db before applying the first pending migration (see
// backupBeforeMigration) — restoring that backup file is the documented
// rollback story; there is no automated restore tooling.
type Migration struct {
	// Version is this migration's position in PRAGMA user_version terms.
	// Must be unique across every call to RegisterMigration in the binary.
	Version int

	// Name is a short, human-readable label used in log lines and error
	// messages — it is not used to order or identify the migration, Version
	// is.
	Name string

	// Up applies the migration's schema/data change. It runs inside a
	// transaction the runner opens and commits together with the
	// PRAGMA user_version bump for this migration — if Up returns an error,
	// the whole transaction (including the version bump) rolls back, so a
	// failed migration never leaves user_version pointing past a change that
	// didn't actually apply.
	Up func(ctx context.Context, tx *sql.Tx) error

	// AfterCommit, if set, runs immediately after Up's transaction commits
	// (and user_version has advanced to Version) — for statements SQLite
	// refuses to run inside a transaction. VACUUM is the motivating case: it
	// opens its own internal transaction and errors with "cannot VACUUM from
	// within a transaction" if issued through tx.ExecContext. AfterCommit
	// runs against the raw *sql.DB instead, right after the wrapping
	// transaction commits, still gated by the same pending-migration check
	// as every other migration.
	//
	// An AfterCommit failure is still reported as a failed migration (the
	// runner returns an error naming this migration and stops before running
	// any later ones), but because Up's transaction already committed and
	// user_version already advanced, a restart will not retry Up or
	// AfterCommit for this version — only Up's effects are guaranteed
	// idempotent/transactional; AfterCommit is best-effort and its own
	// statements should be safe to have partially applied (VACUUM is: it
	// either finishes or SQLite leaves the database in its pre-VACUUM
	// state).
	AfterCommit func(ctx context.Context, db *sql.DB) error
}

// migrationKVTableVersion is the first migration ever registered: it
// recreates (idempotently) the bare `CREATE TABLE IF NOT EXISTS kv` schema
// that predates this runner, so that a database created by the old bare
// migrate() function — which never touched PRAGMA user_version, leaving it
// at 0 — adopts version 1 transparently on first open with the runner, with
// no data loss and no special-casing at call sites.
const migrationKVTableVersion = 1

// migrationAutoVacuumVersion enables incremental auto-vacuum (storage-plan.md
// Phase 2 item 2.4). See its Up/AfterCommit split below for why VACUUM can't
// run inside the same transaction as the PRAGMA that requests the mode.
const migrationAutoVacuumVersion = 2

func init() {
	RegisterMigration(Migration{
		Version: migrationKVTableVersion,
		Name:    "create kv table",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				CREATE TABLE IF NOT EXISTS kv (
					namespace TEXT NOT NULL,
					key       TEXT NOT NULL,
					value     TEXT NOT NULL,
					PRIMARY KEY (namespace, key)
				)
			`); err != nil {
				return fmt.Errorf("create kv table: %w", err)
			}
			// Drop the redundant index that duplicates the PRIMARY KEY
			// exactly — carried over from the pre-runner migrate() so
			// databases that already have it still get it cleaned up.
			if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_kv_ns_key`); err != nil {
				return fmt.Errorf("drop idx_kv_ns_key: %w", err)
			}
			return nil
		},
	})

	RegisterMigration(Migration{
		Version: migrationAutoVacuumVersion,
		Name:    "enable incremental auto_vacuum",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			// Setting the mode is a normal page-1 header write and is safe
			// (and transactional) inside the wrapping transaction. It has no
			// effect on an existing non-empty database until VACUUM runs,
			// which is why this migration also has an AfterCommit step.
			if _, err := tx.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
				return fmt.Errorf("set auto_vacuum: %w", err)
			}
			return nil
		},
		AfterCommit: func(ctx context.Context, db *sql.DB) error {
			// VACUUM cannot run inside a transaction (SQLite: "cannot VACUUM
			// from within a transaction"), so it runs here, against the raw
			// *sql.DB, after Up's transaction (which set the pragma and
			// advanced user_version) has already committed.
			if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
				return fmt.Errorf("vacuum: %w", err)
			}
			return nil
		},
	})
}

var (
	registeredMigrations []Migration
	registeredVersions   = map[int]string{}
)

// RegisterMigration adds m to the set of migrations applied by RunMigrations.
// Call this from an init() function in any package that owns a piece of the
// shared schema (e.g. a future internal/services/cloudwatch/logs package
// registering its events-table migration) — internal/state never imports
// those packages, so this is the standard Go registry pattern
// (database/sql drivers use the same trick).
//
// Panics on a nil Up func or a duplicate Version — both are programmer
// errors caught at binary startup (via the registering package's own
// init()), not runtime conditions calling code should handle.
func RegisterMigration(m Migration) {
	if m.Up == nil {
		panic(fmt.Sprintf("state: migration %d (%s) registered with a nil Up func", m.Version, m.Name))
	}
	migrationsMu.Lock()
	defer migrationsMu.Unlock()
	if existing, ok := registeredVersions[m.Version]; ok {
		panic(fmt.Sprintf("state: duplicate migration version %d: already registered as %q, cannot register %q", m.Version, existing, m.Name))
	}
	registeredVersions[m.Version] = m.Name
	registeredMigrations = append(registeredMigrations, m)
}

// migrationsMu guards registeredMigrations/registeredVersions. Registration
// itself only ever happens single-threaded during package init() in real
// usage, but tests register additional migrations directly (see
// migrate_test.go), so this stays safe under `go test -race`.
var migrationsMu sync.Mutex

// RunMigrations applies every migration registered via RegisterMigration
// that is newer than db's current PRAGMA user_version, in ascending Version
// order, and takes a pre-migration backup when at least one migration is
// pending (see backupBeforeMigration). Call this from the same background
// goroutine that already performs schema setup off the request path (see
// SQLiteStore.runMigrate) — never from a request-handling goroutine.
//
// dbPath is the on-disk path to the SQLite file backing db; it is used only
// to name and write the pre-migration backup file. Pass "" to skip backups
// (e.g. an in-memory database in a test that doesn't care about the backup
// path).
//
// log is optional — pass nil to disable logging. When nothing is pending
// (the common case on every startup after the first), RunMigrations does not
// log at Info level, to avoid startup noise.
func RunMigrations(ctx context.Context, db *sql.DB, dbPath string, log *zap.Logger) error {
	migrationsMu.Lock()
	list := make([]Migration, len(registeredMigrations))
	copy(list, registeredMigrations)
	migrationsMu.Unlock()
	return runMigrations(ctx, db, dbPath, log, list)
}

// runMigrations is the registry-independent core of RunMigrations, split out
// so tests can exercise the runner's mechanics (backup, transaction wrapping,
// failure handling) against a private, disposable migration list instead of
// mutating the shared package-level registry — a broken or duplicate-version
// migration registered globally for one test would otherwise apply to every
// other test's fresh database for the rest of the process.
func runMigrations(ctx context.Context, db *sql.DB, dbPath string, log *zap.Logger, list []Migration) error {
	sorted := make([]Migration, len(list))
	copy(sorted, list)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })

	current, err := readUserVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("state: read user_version: %w", err)
	}

	var pending []Migration
	highest := current
	for _, m := range sorted {
		if m.Version > current {
			pending = append(pending, m)
		}
		if m.Version > highest {
			highest = m.Version
		}
	}
	if len(pending) == 0 {
		logMigrateDebug(log, "sqlite migrations up to date", zap.Int("version", current))
		return nil
	}

	names := make([]string, len(pending))
	for i, m := range pending {
		names[i] = m.Name
	}
	logMigrateInfo(log, "sqlite migrations pending",
		zap.Int("count", len(pending)),
		zap.Strings("migrations", names),
		zap.Int("from_version", current),
		zap.Int("to_version", highest))

	if dbPath != "" {
		hasSchema, err := databaseHasTables(ctx, db)
		if err != nil {
			return fmt.Errorf("state: check existing schema: %w", err)
		}
		// A database with no tables yet has nothing worth protecting — this is
		// the common "brand new install" / "fresh test tempdir" case, and
		// skipping the backup here avoids an unnecessary wal_checkpoint +
		// whole-file copy (each its own fsync) on every first-ever open, not
		// just on genuine upgrades of a pre-existing, populated database.
		if hasSchema {
			backupPath, err := backupBeforeMigration(ctx, db, dbPath, current)
			if err != nil {
				return fmt.Errorf("state: pre-migration backup: %w", err)
			}
			logMigrateInfo(log, "sqlite pre-migration backup created", zap.String("path", backupPath))
		}
	}

	for _, m := range pending {
		start := time.Now()
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("state: migration %d (%s) failed: %w", m.Version, m.Name, err)
		}
		logMigrateInfo(log, "sqlite migration applied",
			zap.Int("version", m.Version),
			zap.String("name", m.Name),
			zap.Duration("elapsed", time.Since(start)))
	}

	logMigrateInfo(log, "sqlite migrations complete", zap.Int("version", highest))
	return nil
}

// applyMigration runs one migration's Up func and its user_version bump
// inside a single transaction, commits, then runs AfterCommit (if set)
// against the raw *sql.DB. See Migration.AfterCommit for why that split
// exists.
func applyMigration(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := m.Up(ctx, tx); err != nil {
		tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
		return fmt.Errorf("up: %w", err)
	}
	// PRAGMA user_version does not support bind parameters; m.Version is a
	// compile-time-registered int, never user input.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
		tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
		return fmt.Errorf("set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if m.AfterCommit != nil {
		if err := m.AfterCommit(ctx, db); err != nil {
			return fmt.Errorf("after-commit: %w", err)
		}
	}
	return nil
}

// readUserVersion reads the database's current schema version.
func readUserVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// databaseHasTables reports whether db already has at least one table —
// the signal used to decide whether a pre-migration backup is worth taking
// (see runMigrations). A cheap sqlite_master query; no fsync involved.
func databaseHasTables(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// backupBeforeMigration checkpoints the WAL into the main database file
// (making a plain file copy consistent) and copies it to
// "<dbPath>.bak-v<fromVersion>" before any pending migration runs. Returns
// the backup file's path. This runs on the already-open *sql.DB before the
// runner's own migration goroutine has published readiness to any other
// caller, so there are no concurrent writers to race against.
func backupBeforeMigration(ctx context.Context, db *sql.DB, dbPath string, fromVersion int) (string, error) {
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return "", fmt.Errorf("wal_checkpoint(TRUNCATE): %w", err)
	}
	backupPath := fmt.Sprintf("%s.bak-v%d", dbPath, fromVersion)
	if err := copyFile(dbPath, backupPath); err != nil {
		return "", fmt.Errorf("copy %q to %q: %w", dbPath, backupPath, err)
	}
	return backupPath, nil
}

// copyFile makes a byte-for-byte copy of src at dst, overwriting dst if it
// already exists (e.g. a re-attempted migration run after an earlier
// migration failed at the same fromVersion).
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()

	if _, err = io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err = out.Sync(); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	return nil
}

func logMigrateDebug(log *zap.Logger, msg string, fields ...zap.Field) {
	if log != nil {
		log.Debug(msg, fields...)
	}
}

func logMigrateInfo(log *zap.Logger, msg string, fields ...zap.Field) {
	if log != nil {
		log.Info(msg, fields...)
	}
}
