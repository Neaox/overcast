//go:build !nosqlite

package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
	_ "modernc.org/sqlite" // Pure-Go SQLite driver — no CGO, works on all platforms
)

// SQLiteStore is the persistent Store implementation.
// State survives process restarts, stored in a single SQLite file under DataDir.
//
// The core schema is a single key-value table — deliberately simple. We
// don't need relational features; we need durable K/V storage with prefix
// scanning. Other packages may add their own dedicated tables to the same
// file (see SQLiteDBProvider); every schema change, including the kv table
// itself, is applied by the PRAGMA user_version-based migration runner in
// migrate.go.
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

	// dbPath is the on-disk path to db's backing file, used by the
	// migration runner to name/write its pre-migration backup file.
	dbPath string

	// log is optional and used only for migration-runner diagnostics (see
	// RunMigrations) — SQLiteStore has no other structured logging today.
	log *zap.Logger

	// ready is closed once the background migration finishes (success or
	// failure). migrateErr captures the result.
	ready      chan struct{}
	migrateErr error

	// maintenanceCancel/maintenanceWG govern the background routine-
	// maintenance loop (3.5: passive WAL checkpoint + conditional
	// incremental vacuum, see runSQLitePragmaMaintenance in maintenance.go).
	// Mirrors HybridStore's cancel+WaitGroup goroutine lifecycle so Close
	// can stop it deterministically before closing db.
	maintenanceCancel context.CancelFunc
	maintenanceWG     sync.WaitGroup
}

// NewSQLiteStore opens (or creates) the SQLite database at dataDir/overcast.db
// with PRAGMA synchronous=NORMAL — writes are durable across OS crashes.
// The data directory is created if it doesn't exist.
//
// Returns immediately; the schema migration runs in a background goroutine.
// The first call into any DB-touching method blocks until the migration
// completes (or returns the migration error if it failed).
func NewSQLiteStore(dataDir string) (*SQLiteStore, error) {
	return NewSQLiteStoreWithLogger(dataDir, nil)
}

// NewSQLiteStoreWithLogger is NewSQLiteStore with structured migration-runner
// diagnostics (see RunMigrations) when logger is non-nil.
func NewSQLiteStoreWithLogger(dataDir string, logger *zap.Logger) (*SQLiteStore, error) {
	return newSQLiteStoreFile(filepath.Join(dataDir, "overcast.db"), "NORMAL", logger)
}

// NewSQLiteStoreWAL opens (or creates) the SQLite database at dataDir/overcast.db
// with PRAGMA synchronous=OFF for maximum write throughput. The OS may buffer
// writes and a power loss between writes can corrupt data — acceptable for
// local-only emulation but not for production use.
//
// Returns immediately; the schema migration runs in a background goroutine
// (see NewSQLiteStore for details).
func NewSQLiteStoreWAL(dataDir string) (*SQLiteStore, error) {
	return newSQLiteStoreFile(filepath.Join(dataDir, "overcast.db"), "OFF", nil)
}

// newSQLiteStoreFile is the shared constructor. syncMode is the SQLite
// PRAGMA synchronous value: "FULL", "NORMAL", or "OFF". logger may be nil.
func newSQLiteStoreFile(dbPath, syncMode string, logger *zap.Logger) (*SQLiteStore, error) {
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
	// inside RunMigrations (migrate.go), invoked from runMigrate below in
	// the background.
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous="+syncMode)
	if err != nil {
		return nil, fmt.Errorf("sqlite store: open %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	markSQLitePhase("sql.Open")

	s := &SQLiteStore{
		db:     db,
		dbPath: dbPath,
		log:    logger,
		ready:  make(chan struct{}),
	}
	go s.runMigrate()

	maintCtx, maintCancel := context.WithCancel(context.Background())
	s.maintenanceCancel = maintCancel
	s.maintenanceWG.Add(1)
	go s.runMaintenance(maintCtx)

	return s, nil
}

// runMaintenance periodically runs routine SQLite housekeeping (3.5) against
// the persistent-mode database, entirely off the request path. SQLiteStore
// has no size-triggered burst concerns the way HybridStore's pending overlay
// does — its writes go straight to SQLite — so a fixed default interval is
// used rather than adding a second, SQLiteStore-specific config knob
// alongside HybridStore's OVERCAST_HYBRID_MAINTENANCE_INTERVAL; see
// docs/storage-plan.md item 3.5 for the reasoning.
func (s *SQLiteStore) runMaintenance(ctx context.Context) {
	defer s.maintenanceWG.Done()
	ticker := time.NewTicker(defaultMaintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case <-s.ready:
			default:
				continue // migration still running; nothing to maintain yet
			}
			if s.migrateErr != nil {
				continue // migration failed — DB may be unusable, don't poke it
			}
			runSQLitePragmaMaintenance(ctx, s.db, s.log, "sqlite maintenance")
		case <-ctx.Done():
			return
		}
	}
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
	if err := RunMigrations(context.Background(), s.db, s.dbPath, s.log); err != nil {
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

// NotReady implements state.NotReadyReporter: true only while the background
// schema migration is still in flight (s.ready not yet closed). Unlike
// HybridStore, SQLiteStore has no memory-backed fallback for an in-flight
// migration — every method blocks on ensureReady until it finishes — so this
// is what lets middleware.NotReady turn that indefinite per-request block
// into a single fast, explicit "not ready yet" response instead.
func (s *SQLiteStore) NotReady() bool {
	select {
	case <-s.ready:
		return false
	default:
		return true
	}
}

// DebugMetrics implements state.DebugMetricsReporter (storage-plan.md item
// 3.6). SQLiteStore writes synchronously and has no pending log or
// background seed, so FlushHistory/SeedDurationMillis/PendingLogBytes stay
// at their zero values — only NamespaceRowCounts (opt-in, see
// DebugMetricsOptions) applies to this backend.
func (s *SQLiteStore) DebugMetrics(ctx context.Context, opts DebugMetricsOptions) DebugMetrics {
	m := DebugMetrics{Mode: "persistent"}
	if !opts.IncludeNamespaceRowCounts {
		return m
	}
	if err := s.ensureReady(ctx); err != nil {
		return m
	}
	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		return m
	}
	counts := make(map[string]int, len(namespaces))
	for _, ns := range namespaces {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kv WHERE namespace = ?`, ns).Scan(&n); err == nil {
			counts[ns] = n
		}
	}
	m.NamespaceRowCounts = counts
	return m
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

func (s *SQLiteStore) ListNamespaces(ctx context.Context) ([]string, error) {
	if err := s.ensureReady(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT namespace FROM kv ORDER BY namespace`)
	if err != nil {
		return nil, fmt.Errorf("sqlite list namespaces: %w", err)
	}
	defer rows.Close()

	var namespaces []string
	for rows.Next() {
		var namespace string
		if err := rows.Scan(&namespace); err != nil {
			return nil, fmt.Errorf("sqlite list namespaces scan: %w", err)
		}
		namespaces = append(namespaces, namespace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite list namespaces rows: %w", err)
	}
	if namespaces == nil {
		namespaces = []string{}
	}
	return namespaces, nil
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

// ScanPage returns up to limit key-value pairs whose keys start with prefix,
// in key order, starting strictly after startAfter — see Store.ScanPage for
// the full contract. Shares its query-building and page-trimming logic
// (scanPageQuery / finalizeScanPage) with HybridStore's raw SQLite helpers
// in hybrid.go, which run the identical query shape against a different
// *sql.DB (the dedicated read pool) — see hybridSQLiteRawScanPage.
func (s *SQLiteStore) ScanPage(ctx context.Context, namespace, prefix, startAfter string, limit int) ([]KV, string, error) {
	if err := s.ensureReady(ctx); err != nil {
		return nil, "", err
	}
	query, args := scanPageQuery(namespace, prefix, startAfter, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("sqlite scan page [%s/%s* after %q]: %w", namespace, prefix, startAfter, err)
	}
	defer rows.Close()

	pairs, err := collectScanPageRows(rows)
	if err != nil {
		return nil, "", err
	}
	return finalizeScanPage(pairs, limit)
}

// scanPageQuery builds the SQL and bind args for a ScanPage query, shared by
// SQLiteStore.ScanPage and HybridStore's hybridSQLiteRawScanPage so the two
// SQLite-backed implementations can never drift on key-range semantics. When
// limit > 0 it fetches one extra row (LIMIT limit+1) so the caller can tell
// whether another page follows without a second round trip — the same
// LIMIT-plus-one truncation-detection trick already used elsewhere in this
// codebase (e.g. CloudWatch Logs' debugScan) — see finalizeScanPage for the
// other half.
func scanPageQuery(namespace, prefix, startAfter string, limit int) (string, []any) {
	query := `SELECT key, value FROM kv WHERE namespace = ?`
	args := []any{namespace}
	if prefix != "" {
		query += ` AND key >= ?`
		args = append(args, prefix)
		if upper := prefixUpperBound(prefix); upper != "" {
			query += ` AND key < ?`
			args = append(args, upper)
		}
	}
	if startAfter != "" {
		query += ` AND key > ?`
		args = append(args, startAfter)
	}
	query += ` ORDER BY key`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit+1)
	}
	return query, args
}

// collectScanPageRows drains a ScanPage query's *sql.Rows into a []KV. The
// caller remains responsible for closing rows.
func collectScanPageRows(rows *sql.Rows) ([]KV, error) {
	var pairs []KV
	for rows.Next() {
		var kv KV
		if err := rows.Scan(&kv.Key, &kv.Value); err != nil {
			return nil, fmt.Errorf("sqlite scan page row: %w", err)
		}
		pairs = append(pairs, kv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite scan page rows: %w", err)
	}
	return pairs, nil
}

// finalizeScanPage trims a scanPageQuery result (which holds one extra row
// beyond limit when limit > 0, per the LIMIT limit+1 trick) down to at most
// limit rows and derives nextKey from the *last kept* row, if the extra row
// proved a next page exists. nextKey must be the last row this page actually
// returns, not the trimmed-off row itself — startAfter is exclusive (see
// Store.ScanPage), so using the trimmed-off row's own key as nextKey would
// cause the next call to skip it.
func finalizeScanPage(pairs []KV, limit int) ([]KV, string, error) {
	nextKey := ""
	if limit > 0 && len(pairs) > limit {
		pairs = pairs[:limit]
		nextKey = pairs[limit-1].Key
	}
	if pairs == nil {
		pairs = []KV{}
	}
	return pairs, nextKey, nil
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
	if s.maintenanceCancel != nil {
		s.maintenanceCancel()
		s.maintenanceWG.Wait()
	}
	return s.db.Close()
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
