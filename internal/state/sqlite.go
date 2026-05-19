//go:build !nosqlite

package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver — no CGO, works on all platforms
)

// SQLiteStore is the persistent Store implementation.
// State survives process restarts, stored in a single SQLite file under DataDir.
//
// Schema is a single key-value table — deliberately simple. We don't need
// relational features; we need durable K/V storage with prefix scanning.
//
// The file path is: <DataDir>/overcast.db.
//
// Construction is non-blocking: `sql.Open` is cheap (it does not connect or
// migrate), and the schema migration runs in a background goroutine. The
// first DB-touching method (Get/Set/Delete/List/Scan/DB/loadAll) blocks on
// a ready channel until the migration finishes. This keeps the
// modernc/sqlite cold-start cost (~200–300ms for the first CREATE TABLE
// when the driver initialises its parser) off the critical startup path —
// the same approach used by HybridStore. Quick-start aware.
type SQLiteStore struct {
	db *sql.DB

	// ready is closed once the background migration finishes (success or
	// failure). migrateErr captures the result.
	ready      chan struct{}
	migrateErr error
}

// NewSQLiteStore opens (or creates) the SQLite database at dataDir/overcast.db
// with PRAGMA synchronous=NORMAL — writes are durable across OS crashes.
// The data directory is created if it doesn't exist.
//
// Returns immediately; the schema migration runs in a background goroutine.
// The first call into any DB-touching method blocks until the migration
// completes (or returns the migration error if it failed).
func NewSQLiteStore(dataDir string) (*SQLiteStore, error) {
	return newSQLiteStoreFile(filepath.Join(dataDir, "overcast.db"), "NORMAL")
}

// NewSQLiteStoreWAL opens (or creates) the SQLite database at dataDir/overcast.db
// with PRAGMA synchronous=OFF for maximum write throughput. The OS may buffer
// writes and a power loss between writes can corrupt data — acceptable for
// local-only emulation but not for production use.
//
// Returns immediately; the schema migration runs in a background goroutine
// (see NewSQLiteStore for details).
func NewSQLiteStoreWAL(dataDir string) (*SQLiteStore, error) {
	return newSQLiteStoreFile(filepath.Join(dataDir, "overcast.db"), "OFF")
}

// newSQLiteStoreFile is the shared constructor. syncMode is the SQLite
// PRAGMA synchronous value: "FULL", "NORMAL", or "OFF".
func newSQLiteStoreFile(dbPath, syncMode string) (*SQLiteStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sqlite store: create data dir %q: %w", dir, err)
	}

	markSQLitePhase("mkdir")

	// modernc pure-Go driver — no CGO required, works on Mac/Linux/Windows.
	// WAL journal mode + a connection pool of 1 writer is the standard
	// SQLite configuration for concurrent access.
	//
	// sql.Open does NOT establish a connection or touch the database file —
	// it only validates the driver name and DSN. The first real connection
	// (and therefore the modernc driver's lazy parser/codegen init) happens
	// inside migrate() below, which we run in the background.
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous="+syncMode)
	if err != nil {
		return nil, fmt.Errorf("sqlite store: open %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	markSQLitePhase("sql.Open")

	s := &SQLiteStore{
		db:    db,
		ready: make(chan struct{}),
	}
	go s.runMigrate()
	return s, nil
}

// runMigrate executes the schema migration in the background. The first
// caller into ensureReady blocks on the ready channel until this finishes.
//
// Concurrency contract: the write to s.migrateErr is safe without a mutex
// because `defer close(s.ready)` runs after the assignment, and channel
// close synchronizes-with the receive in ensureReady (Go memory model:
// "the closing of a channel happens before a receive that returns because
// the channel is closed"). Do not add a mutex — it would be redundant.
func (s *SQLiteStore) runMigrate() {
	defer close(s.ready)
	defer markSQLitePhase("migrate") // always emit so the profiler shows the failure case too
	if err := migrate(s.db); err != nil {
		s.migrateErr = fmt.Errorf("sqlite store: migrate: %w", err)
		// Surface the error eagerly. Without this log a failed migration is
		// invisible until the first kv operation runs — which may be never
		// for daemons that only use service-specific tables (e.g. DynamoDB).
		fmt.Fprintf(os.Stderr, "overcast: sqlite migration failed: %v\n", s.migrateErr)
	}
}

// ensureReady blocks until the background schema migration has completed
// and returns its error (nil on success). All public DB-touching methods
// must call this before issuing queries. Honours ctx so a request with a
// short deadline can bail out without waiting on the full migration
// (~200–300 ms cold start with modernc/sqlite).
func (s *SQLiteStore) ensureReady(ctx context.Context) error {
	select {
	case <-s.ready:
		return s.migrateErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// migrate creates the schema if it doesn't already exist.
// We use IF NOT EXISTS so this is idempotent — safe to call on every startup.
func migrate(db *sql.DB) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS kv (
		namespace TEXT NOT NULL,
		key       TEXT NOT NULL,
		value     TEXT NOT NULL,
		PRIMARY KEY (namespace, key)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// Drop the redundant index that duplicates the PRIMARY KEY exactly.
	_, _ = db.Exec(`DROP INDEX IF EXISTS idx_kv_ns_key`)
	return nil
}

func (s *SQLiteStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	if err := s.ensureReady(ctx); err != nil {
		return "", false, err
	}
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM kv WHERE namespace = ? AND key = ?`,
		namespace, key,
	).Scan(&value)

	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite get [%s/%s]: %w", namespace, key, err)
	}
	return value, true, nil
}

func (s *SQLiteStore) Set(ctx context.Context, namespace, key, value string) error {
	if err := s.ensureReady(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		// INSERT OR REPLACE is SQLite's upsert — insert if not exists, replace if exists.
		`INSERT OR REPLACE INTO kv (namespace, key, value) VALUES (?, ?, ?)`,
		namespace, key, value,
	)
	if err != nil {
		return fmt.Errorf("sqlite set [%s/%s]: %w", namespace, key, err)
	}
	return nil
}

func (s *SQLiteStore) Delete(ctx context.Context, namespace, key string) error {
	if err := s.ensureReady(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM kv WHERE namespace = ? AND key = ?`,
		namespace, key,
	)
	if err != nil {
		return fmt.Errorf("sqlite delete [%s/%s]: %w", namespace, key, err)
	}
	return nil
}

func (s *SQLiteStore) DeletePrefix(ctx context.Context, namespace, prefix string) error {
	if err := s.ensureReady(ctx); err != nil {
		return err
	}
	var err error
	if prefix == "" {
		_, err = s.db.ExecContext(ctx,
			`DELETE FROM kv WHERE namespace = ?`,
			namespace,
		)
	} else {
		upper := prefixUpperBound(prefix)
		if upper == "" {
			_, err = s.db.ExecContext(ctx,
				`DELETE FROM kv WHERE namespace = ? AND key >= ?`,
				namespace, prefix,
			)
		} else {
			_, err = s.db.ExecContext(ctx,
				`DELETE FROM kv WHERE namespace = ? AND key >= ? AND key < ?`,
				namespace, prefix, upper,
			)
		}
	}
	if err != nil {
		return fmt.Errorf("sqlite delete prefix [%s/%s*]: %w", namespace, prefix, err)
	}
	return nil
}

func (s *SQLiteStore) List(ctx context.Context, namespace, prefix string) ([]string, error) {
	if err := s.ensureReady(ctx); err != nil {
		return nil, err
	}
	var rows *sql.Rows
	var err error

	if prefix == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT key FROM kv WHERE namespace = ? ORDER BY key`,
			namespace,
		)
	} else {
		upper := prefixUpperBound(prefix)
		if upper == "" {
			rows, err = s.db.QueryContext(ctx,
				`SELECT key FROM kv WHERE namespace = ? AND key >= ? ORDER BY key`,
				namespace, prefix,
			)
		} else {
			rows, err = s.db.QueryContext(ctx,
				`SELECT key FROM kv WHERE namespace = ? AND key >= ? AND key < ? ORDER BY key`,
				namespace, prefix, upper,
			)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite list [%s/%s*]: %w", namespace, prefix, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("sqlite list scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite list rows: %w", err)
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

// Scan returns all key-value pairs whose keys start with prefix in a single
// query — prefer this over List+Get when you need both keys and values.
func (s *SQLiteStore) Scan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	if err := s.ensureReady(ctx); err != nil {
		return nil, err
	}
	var rows *sql.Rows
	var err error

	if prefix == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT key, value FROM kv WHERE namespace = ? ORDER BY key`,
			namespace,
		)
	} else {
		upper := prefixUpperBound(prefix)
		if upper == "" {
			rows, err = s.db.QueryContext(ctx,
				`SELECT key, value FROM kv WHERE namespace = ? AND key >= ? ORDER BY key`,
				namespace, prefix,
			)
		} else {
			rows, err = s.db.QueryContext(ctx,
				`SELECT key, value FROM kv WHERE namespace = ? AND key >= ? AND key < ? ORDER BY key`,
				namespace, prefix, upper,
			)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite scan [%s/%s*]: %w", namespace, prefix, err)
	}
	defer rows.Close()

	var pairs []KV
	for rows.Next() {
		var kv KV
		if err := rows.Scan(&kv.Key, &kv.Value); err != nil {
			return nil, fmt.Errorf("sqlite scan row: %w", err)
		}
		pairs = append(pairs, kv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite scan rows: %w", err)
	}
	if pairs == nil {
		pairs = []KV{}
	}
	return pairs, nil
}

// DB returns the underlying *sql.DB so that service-specific stores can add
// their own tables to the same database file. Blocks until the background
// schema migration has finished.
//
// If the migration failed, the returned *sql.DB is still non-nil (sql.Open
// itself succeeded) but service-specific schema setup against it will
// likely fail. The migration error is logged eagerly by runMigrate so it
// is visible in the daemon log even if no kv operation is ever issued.
// Callers must not close the returned DB — call Close() on the SQLiteStore.
func (s *SQLiteStore) DB() *sql.DB {
	<-s.ready
	return s.db
}

func (s *SQLiteStore) Close() error {
	// Wait for the background migration to finish before closing — otherwise
	// the db handle could be closed mid-CREATE-TABLE.
	<-s.ready
	return s.db.Close()
}

// nsRow is a row from the kv table with its namespace included.
// Used by HybridStore to seed the in-memory layer on startup.
type nsRow struct {
	Namespace string
	Key       string
	Value     string
}

// loadAll returns every row in the kv table.
// This is only called once at HybridStore startup and is not part of the Store
// interface — it intentionally loads all data into memory in one pass.
func (s *SQLiteStore) loadAll(ctx context.Context) ([]nsRow, error) {
	if err := s.ensureReady(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT namespace, key, value FROM kv ORDER BY namespace, key`)
	if err != nil {
		return nil, fmt.Errorf("sqlite loadAll: %w", err)
	}
	defer rows.Close()

	var result []nsRow
	for rows.Next() {
		var r nsRow
		if err := rows.Scan(&r.Namespace, &r.Key, &r.Value); err != nil {
			return nil, fmt.Errorf("sqlite loadAll scan: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite loadAll rows: %w", err)
	}
	return result, nil
}

// prefixUpperBound returns the exclusive upper bound for a prefix range query.
// For example, "us-east-1/" → "us-east-10" (the byte after '/' is '0').
// Returns "" if no upper bound exists (all bytes are 0xFF).
func prefixUpperBound(prefix string) string {
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
	return "" // all 0xFF bytes — no upper bound exists
}
