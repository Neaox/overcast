//go:build !nosqlite

package state

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	msqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// HybridStore serves all reads from an in-memory map (memory speed) and
// asynchronously flushes writes to SQLite at a configurable interval.
//
// On startup it opens the SQLite file AND seeds the in-memory layer from it
// in a background goroutine. The constructor returns immediately. Reads use
// indexed SQLite fallback until seeding completes, then switch to memory;
// writes are accepted immediately and take precedence over loaded state. DB()
// blocks only until the SQLite handle is ready. This keeps full-table restore
// and the modernc SQLite driver's cold-start cost off the startup path.
//
// Accepted writes are appended to a pending log before returning, then
// batch-flushed to SQLite. This protects against process crashes before the
// next SQLite flush without forcing a full SQLite transaction on every
// service mutation. The pending log's fsync policy is configurable (see
// HybridOptions.SyncMode); a size/count-triggered early flush keeps the log
// and the in-memory overlay bounded during write bursts (see
// HybridOptions.DirtyEntryThreshold / DirtyByteThreshold).
//
// If the persistent backend cannot be opened, migrated, or seeded, the store
// degrades to memory-only rather than failing every subsequent request: see
// degradeToMemoryOnly.
type HybridStore struct {
	mem *MemoryStore

	// sqlite is nil until the background loader has opened the DB. Publication
	// is synchronized by sqliteReady (written once before the first close of
	// sqliteReady, never mutated afterward — see the constructor/seed comments
	// for the happens-before argument this relies on).
	sqlite *SQLiteStore

	// sqliteRead is a dedicated read-only connection pool opened on the same
	// database file as sqlite, used for TierCached reads and seed queries so
	// they never queue behind an in-flight flush transaction on the single
	// writer connection (WAL mode allows concurrent readers alongside the
	// writer). Like sqlite, it is written once before the first close of
	// sqliteReady and never mutated afterward. May stay nil if opening it
	// failed — readDB() falls back to the writer connection in that case.
	sqliteRead *sql.DB

	// sqliteDegraded is set when the persistent backend becomes permanently
	// unusable for this process lifetime (open/migrate/seed failure). Unlike
	// sqlite/sqliteRead, this can flip from false to true *after* sqliteReady
	// has already been published (a failure partway through the seed loop),
	// so concurrent readers of it need real synchronization — hence atomic
	// rather than a plain bool guarded by the one-time-publication trick.
	sqliteDegraded atomic.Bool

	dataDir     string
	pendingPath string
	pendingFile *os.File

	mu      sync.Mutex
	flushMu sync.Mutex

	// dirty/flushing are a fast per-key read cache derived from pendingOps:
	// "namespace\x00key" → the latest explicit Set/Delete for that key.
	// dirty holds writes accepted since the last flush-steal; flushing holds
	// the batch currently being written to SQLite by an in-flight flush.
	dirty    map[string]dirtyEntry
	flushing map[string]dirtyEntry

	// tombstones/flushingTombstones mirror dirty/flushing for DeletePrefix:
	// a prefix tombstone shadows any covered key whose dirty/flushing entry
	// has a lower sequence number (see resolvePendingLocked).
	tombstones         []prefixTombstone
	flushingTombstones []prefixTombstone

	// pendingOps is the ordered, not-yet-flushed operation log — the
	// authoritative source for what a flush executes against SQLite (in
	// order) and for what compaction rewrites to the pending log file.
	// dirty/flushing/tombstones are derived read caches over the same
	// operations, kept incrementally in sync for O(1)-ish point reads.
	pendingOps []hybridPendingOp
	nextSeq    int64

	// dirtyApproxBytes is an approximate running total of unflushed payload
	// size, used only to trigger an early flush (see dirtyThresholdExceededLocked).
	// It intentionally does not need to be exact.
	dirtyApproxBytes    int64
	dirtyEntryThreshold int
	dirtyByteThreshold  int64

	// flushSignal is a coalesced (buffered, size 1) non-blocking trigger the
	// run loop selects on alongside its ticker, so a burst that crosses a
	// dirty threshold gets flushed promptly without waiting for the next
	// interval tick. Callers never flush synchronously themselves — they only
	// signal.
	flushSignal chan struct{}

	flushInterval time.Duration
	syncMode      WALSyncMode
	syncInterval  time.Duration
	log           *zap.Logger

	// maintenanceInterval controls runMaintenance's ticker (3.5: passive WAL
	// checkpoint + conditional incremental vacuum). The loop is always
	// started; it no-ops on each tick while the store is still seeding or
	// has degraded to memory-only (see runMaintenance).
	maintenanceInterval time.Duration

	// sqliteReady is closed when SQLite is open and migrated. loaded is closed
	// when the background SQLite -> memory seed finishes.
	sqliteReady chan struct{}
	loaded      chan struct{}
	loadErr     error // guarded by mu
	health      PersistentHealth

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// dirtyEntry holds a pending write (value set) or a tombstone (deleted), plus
// the sequence number it was recorded at so resolvePendingLocked can order it
// against prefix tombstones.
type dirtyEntry struct {
	value   string
	deleted bool
	seq     int64
}

// prefixTombstone records a DeletePrefix accepted into the overlay but not
// yet flushed. Any key with this prefix whose own dirty/flushing entry has a
// lower seq reads as deleted; a later Set for that key (higher seq) survives.
type prefixTombstone struct {
	seq       int64
	namespace string
	prefix    string
}

// hybridPendingOp is one entry in the ordered, not-yet-flushed operation log.
// key holds the literal key for walSet/walDelete or the prefix for
// walDeletePrefix; value is only meaningful for walSet.
type hybridPendingOp struct {
	seq       int64
	op        walOp
	namespace string
	key       string
	value     string
}

// HybridOptions configures HybridStore durability and burst-flush behavior.
// The zero value is not directly usable for SyncMode validation purposes —
// use NewHybridStore/NewHybridStoreWithLogger for the documented defaults, or
// leave fields unset when calling NewHybridStoreWithOptions to get the same
// defaults (interval sync at 100ms, 10,000-entry / 8 MiB dirty thresholds).
type HybridOptions struct {
	// FlushInterval is how often the background loop flushes dirty writes to
	// SQLite on a timer, independent of the size-triggered early flush.
	FlushInterval time.Duration

	// SyncMode controls how the pending log file is fsync'd: WALSyncAlways
	// syncs inline on every append, WALSyncInterval (default) syncs on a
	// timer, WALSyncNever relies on the OS page cache plus the sync always
	// performed on Close.
	SyncMode WALSyncMode

	// SyncInterval is used only when SyncMode is WALSyncInterval. Defaults to
	// 100ms.
	SyncInterval time.Duration

	// DirtyEntryThreshold triggers an out-of-band flush signal once this many
	// unflushed pending-log operations have accumulated, ahead of the next
	// FlushInterval tick. Defaults to 10,000. A value <= 0 disables this
	// trigger (byte threshold still applies unless it is also disabled).
	DirtyEntryThreshold int

	// DirtyByteThreshold triggers an out-of-band flush once the approximate
	// byte size of unflushed writes exceeds this many bytes. Defaults to
	// 8 MiB. A value <= 0 disables this trigger.
	DirtyByteThreshold int64

	// MaintenanceInterval controls how often the background loop runs
	// routine SQLite housekeeping (3.5 in docs/storage-plan.md): a passive
	// WAL checkpoint plus a conditional incremental vacuum. Never runs on
	// the request path. Defaults to 5 minutes. A value <= 0 falls back to
	// the default rather than disabling the loop — unlike the dirty
	// thresholds above, there's no useful "off" state for routine
	// maintenance, so this intentionally doesn't support disabling it via a
	// non-positive value.
	MaintenanceInterval time.Duration
}

const (
	defaultHybridSyncInterval        = 100 * time.Millisecond
	defaultHybridDirtyEntryThreshold = 10000
	defaultHybridDirtyByteThreshold  = 8 << 20 // 8 MiB

	hybridReadPoolMaxOpenConns = 4
	hybridBusyTimeoutMillis    = 5000
)

// NewHybridStore creates a HybridStore backed by a SQLite file in dataDir,
// using default durability and burst-flush settings (interval sync at 100ms,
// 10,000-entry / 8 MiB dirty thresholds). SQLite is opened and existing data
// loaded in a background goroutine; the constructor returns immediately.
// Reads fall back to SQLite until the cache is loaded. Writes are accepted
// right away and take precedence over loaded state.
func NewHybridStore(dataDir string, flushInterval time.Duration) (*HybridStore, error) {
	return NewHybridStoreWithOptions(dataDir, HybridOptions{FlushInterval: flushInterval}, nil)
}

// NewHybridStoreWithLogger creates a HybridStore and emits structured timing
// diagnostics for startup seeding and flushes when logger is non-nil. Uses
// default durability and burst-flush settings — see NewHybridStoreWithOptions
// to override them.
func NewHybridStoreWithLogger(dataDir string, flushInterval time.Duration, logger *zap.Logger) (*HybridStore, error) {
	return NewHybridStoreWithOptions(dataDir, HybridOptions{FlushInterval: flushInterval}, logger)
}

// NewHybridStoreWithOptions creates a HybridStore with full control over
// durability and burst-flush behavior. Zero-valued fields in opts fall back
// to the documented defaults (see HybridOptions).
func NewHybridStoreWithOptions(dataDir string, opts HybridOptions, logger *zap.Logger) (*HybridStore, error) {
	if opts.SyncMode == "" {
		opts.SyncMode = WALSyncInterval
	}
	if err := validateWALSyncMode(opts.SyncMode); err != nil {
		return nil, err
	}
	if opts.SyncInterval <= 0 {
		opts.SyncInterval = defaultHybridSyncInterval
	}
	if opts.DirtyEntryThreshold == 0 {
		opts.DirtyEntryThreshold = defaultHybridDirtyEntryThreshold
	}
	if opts.DirtyByteThreshold == 0 {
		opts.DirtyByteThreshold = defaultHybridDirtyByteThreshold
	}
	if opts.MaintenanceInterval <= 0 {
		opts.MaintenanceInterval = defaultMaintenanceInterval
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("hybrid store: create data dir %q: %w", dataDir, err)
	}
	bgCtx, cancel := context.WithCancel(context.Background())
	pendingPath := filepath.Join(dataDir, hybridPendingWALFileName)
	hs := &HybridStore{
		mem:                 NewMemoryStore(),
		dataDir:             dataDir,
		pendingPath:         pendingPath,
		dirty:               make(map[string]dirtyEntry, 64),
		flushing:            make(map[string]dirtyEntry, 64),
		dirtyEntryThreshold: opts.DirtyEntryThreshold,
		dirtyByteThreshold:  opts.DirtyByteThreshold,
		flushSignal:         make(chan struct{}, 1),
		flushInterval:       opts.FlushInterval,
		syncMode:            opts.SyncMode,
		syncInterval:        opts.SyncInterval,
		maintenanceInterval: opts.MaintenanceInterval,
		log:                 logger,
		sqliteReady:         make(chan struct{}),
		loaded:              make(chan struct{}),
		health: PersistentHealth{
			Mode:    "hybrid",
			Healthy: true,
		},
		cancel: cancel,
	}
	if err := hs.replayPendingLog(); err != nil {
		cancel()
		return nil, err
	}
	pendingFile, err := os.OpenFile(pendingPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("hybrid store: open pending log %q: %w", pendingPath, err)
	}
	hs.pendingFile = pendingFile

	hs.wg.Add(1)
	go hs.seedFromSQLite()

	hs.wg.Add(1)
	go hs.run(bgCtx)

	if hs.syncMode == WALSyncInterval {
		hs.wg.Add(1)
		go hs.runPendingSync(bgCtx)
	}

	hs.wg.Add(1)
	go hs.runMaintenance(bgCtx)

	return hs, nil
}

// seedFromSQLite opens the SQLite DB and loads all TierHot rows into the
// in-memory layer. Keys that were written (dirty) before the seed completes
// are skipped so concurrent writes always win over persisted state.
//
// Any failure — open, migrate, or a per-namespace query/scan — degrades the
// store to memory-only (see degradeToMemoryOnly) rather than poisoning every
// future TierHot read/write, per the storage plan's "degrade, don't poison"
// policy. A per-row scan-decode failure is narrower still: it skips just that
// row and keeps loading the rest, per the project's malformed-persisted-state
// rule.
func (s *HybridStore) seedFromSQLite() {
	defer s.wg.Done()
	defer close(s.loaded)
	sqliteReadyClosed := false
	defer func() {
		if !sqliteReadyClosed {
			close(s.sqliteReady)
		}
	}()
	seedStart := time.Now()
	s.logDebug("hybrid seed started", zap.String("data_dir", s.dataDir))

	openStart := time.Now()
	sq, err := NewSQLiteStoreWithLogger(s.dataDir, s.log)
	if err != nil {
		s.degradeToMemoryOnly(fmt.Errorf("hybrid store: open sqlite: %w", err), 0, 0, seedStart)
		return
	}
	s.sqlite = sq
	// Keep hybrid SQLite WRITES queued through one connection. SQLite allows
	// one writer at a time; a wider write pool turns concurrent flushes and
	// service-specific table writes into SQLITE_BUSY under CDK-heavy deploys.
	// Reads use a separate bounded pool (opened below) so TierCached lookups
	// never queue behind an in-flight flush transaction — WAL mode supports
	// concurrent readers alongside the single writer.
	s.sqlite.db.SetMaxOpenConns(1)
	s.logDebug("hybrid sqlite opened", zap.Duration("elapsed", time.Since(openStart)))

	if err := s.sqlite.ensureReady(context.Background()); err != nil {
		s.degradeToMemoryOnly(fmt.Errorf("hybrid store: sqlite ready: %w", err), 0, 0, seedStart)
		return
	}
	if _, err := s.sqlite.db.ExecContext(context.Background(),
		fmt.Sprintf("PRAGMA busy_timeout=%d", hybridBusyTimeoutMillis)); err != nil {
		s.logWarn("hybrid: set writer busy_timeout failed", zap.Error(err))
	}
	if err := s.openReadPool(); err != nil {
		// The read pool is a performance optimisation (1.2), not a
		// correctness requirement — fall back to sharing the writer
		// connection for reads instead of degrading the whole store.
		s.logWarn("hybrid: opening dedicated read pool failed; reads will share the writer connection", zap.Error(err))
	}
	close(s.sqliteReady)
	sqliteReadyClosed = true

	ctx := context.Background()
	queryStart := time.Now()
	seedNamespaces := hybridSeedNamespaceList

	// Snapshot current dirty keys — any key written during the load is newer
	// than what SQLite has.
	s.mu.Lock()
	dirtySnapshot := make(map[string]struct{}, len(s.dirty))
	for k := range s.dirty {
		dirtySnapshot[k] = struct{}{}
	}
	s.mu.Unlock()

	var loaded, skippedDirty, skippedRows int
	var bytesLoaded int64
	for _, namespace := range seedNamespaces {
		rows, err := s.readDB().QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ? ORDER BY key`, namespace)
		if err != nil {
			s.degradeToMemoryOnly(fmt.Errorf("hybrid store: query namespace %q: %w", namespace, err), loaded, skippedRows, seedStart)
			return
		}
		rowIndex := 0
		for rows.Next() {
			rowIndex++
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				// A sql.Rows scan failure here decodes two TEXT columns into
				// Go strings — it cannot be a JSON-shape problem (each
				// service's store.go owns that decoding later, lazily), only
				// driver/row-level corruption of this one record. Per the
				// project's malformed-persisted-state rule, skip the row and
				// keep loading the rest instead of aborting the whole seed.
				skippedRows++
				s.logWarn("hybrid seed row scan failed; skipping",
					zap.String("namespace", namespace),
					zap.Int("row_index", rowIndex),
					zap.Error(err))
				continue
			}
			composite := storeKey(namespace, key)
			if _, overwritten := dirtySnapshot[composite]; overwritten {
				skippedDirty++
				continue
			}
			s.mu.Lock()
			_, overwritten := s.dirty[composite]
			s.mu.Unlock()
			if overwritten {
				skippedDirty++
				continue
			}
			if err := s.mem.Set(ctx, namespace, key, value); err != nil {
				rows.Close()
				s.degradeToMemoryOnly(fmt.Errorf("hybrid store: seed memory: %w", err), loaded, skippedRows, seedStart)
				return
			}
			loaded++
			bytesLoaded += int64(len(namespace) + len(key) + len(value))
			if loaded%100000 == 0 {
				s.logInfo("hybrid seed progress",
					zap.Int("loaded", loaded),
					zap.Int64("bytes", bytesLoaded),
					zap.Duration("elapsed", time.Since(seedStart)))
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			s.degradeToMemoryOnly(fmt.Errorf("hybrid store: scan namespace %q rows: %w", namespace, err), loaded, skippedRows, seedStart)
			return
		}
		if err := rows.Close(); err != nil {
			s.degradeToMemoryOnly(fmt.Errorf("hybrid store: close namespace %q rows: %w", namespace, err), loaded, skippedRows, seedStart)
			return
		}
	}
	if skippedRows > 0 {
		s.logWarn("hybrid seed skipped corrupt rows",
			zap.Int("skipped_rows", skippedRows),
			zap.Int("loaded", loaded))
	}
	// A size-triggered flush signal (1.4) fired while s.loaded was still open
	// is a silent no-op in flushOnce (it defers to the next tick/signal
	// rather than blocking). Nudge once here so any threshold crossed during
	// seeding gets flushed promptly instead of waiting up to FlushInterval.
	s.signalFlush()
	s.logInfo("hybrid seed complete",
		zap.Int("loaded", loaded),
		zap.Int("skipped_dirty", skippedDirty),
		zap.Int("skipped_rows", skippedRows),
		zap.Strings("seed_namespaces", seedNamespaces),
		zap.Strings("lazy_namespaces", hybridLazyNamespaceList),
		zap.Int64("bytes", bytesLoaded),
		zap.Duration("query_and_seed", time.Since(queryStart)),
		zap.Duration("elapsed", time.Since(seedStart)))
}

// degradeToMemoryOnly marks the persistent backend unhealthy and disables all
// SQLite routing so every subsequent Get/List/Scan/flush serves from memory
// only, instead of the pre-1.11 behavior where any seed/open failure made
// every TierHot read/write return an error forever. loaded/skippedRows are
// best-effort progress counters for the log line.
func (s *HybridStore) degradeToMemoryOnly(err error, loaded, skippedRows int, seedStart time.Time) {
	s.setLoadErr(err)
	s.sqliteDegraded.Store(true)
	s.logWarn("hybrid store degraded to memory-only for this run; persisted state is unavailable until restart",
		zap.Error(err),
		zap.Int("loaded_before_failure", loaded),
		zap.Int("skipped_rows", skippedRows),
		zap.Duration("elapsed", time.Since(seedStart)))
}

// openReadPool opens the dedicated read-only connection pool used by 1.2 for
// TierCached reads and seed queries, on the same database file as the writer
// connection. Must only be called after the writer connection has been
// opened and migrated (s.sqlite non-nil and ready).
func (s *HybridStore) openReadPool() error {
	dbPath := filepath.Join(s.dataDir, "overcast.db")
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_synchronous=NORMAL&_pragma=busy_timeout(%d)", dbPath, hybridBusyTimeoutMillis)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open read pool: %w", err)
	}
	db.SetMaxOpenConns(hybridReadPoolMaxOpenConns)
	s.sqliteRead = db
	return nil
}

// readDB returns the connection pool used for TierCached reads and seed
// queries: the dedicated read pool from 1.2 when available, or the writer
// connection as a fallback (pre-1.2 behavior) if opening the read pool
// failed. Only valid to call once s.sqlite is assigned.
func (s *HybridStore) readDB() *sql.DB {
	if s.sqliteRead != nil {
		return s.sqliteRead
	}
	return s.sqlite.db
}

func (s *HybridStore) isLoaded() bool {
	select {
	case <-s.loaded:
		return true
	default:
		return false
	}
}

func (s *HybridStore) sqliteReadyNow() bool {
	select {
	case <-s.sqliteReady:
		return s.sqlite != nil && !s.sqliteDegraded.Load()
	default:
		return false
	}
}

// Get serves from memory after the initial seed completes. During seed it
// falls back to SQLite so persisted state stays visible without blocking on a
// full-table cache warmup. A degraded persistent backend (see
// degradeToMemoryOnly) never blocks a read — it simply serves from memory.
func (s *HybridStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	if shouldReadHybridNamespaceFromSQLite(namespace) {
		if entry, ok := s.pendingValue(namespace, key); ok {
			if entry.deleted {
				return "", false, nil
			}
			return entry.value, true, nil
		}
		if s.sqliteReadyNow() {
			return s.sqliteGet(ctx, namespace, key)
		}
		return s.mem.Get(ctx, namespace, key)
	}
	if s.isLoaded() {
		return s.mem.Get(ctx, namespace, key)
	}
	if entry, ok := s.pendingValue(namespace, key); ok {
		if entry.deleted {
			return "", false, nil
		}
		return entry.value, true, nil
	}
	if s.sqliteReadyNow() {
		return s.sqliteGet(ctx, namespace, key)
	}
	return s.mem.Get(ctx, namespace, key)
}

// Set publishes the dirty overlay before writing memory so lazy SQLite-backed
// reads cannot observe stale persisted state between the two updates.
func (s *HybridStore) Set(ctx context.Context, namespace, key, value string) error {
	s.mu.Lock()
	seq := s.allocSeqLocked()
	if err := s.appendPendingBatchLocked([]walEntry{{Op: walSet, Namespace: namespace, Key: key, Value: value}}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.applyOverlayLocked(seq, walSet, namespace, key, value)
	trigger := s.dirtyThresholdExceededLocked()
	s.mu.Unlock()
	if trigger {
		s.signalFlush()
	}
	return s.mem.Set(ctx, namespace, key, value)
}

// Delete publishes the tombstone before removing memory for the same reason as
// Set: the dirty overlay is what makes lazy reads linearizable with writes.
func (s *HybridStore) Delete(ctx context.Context, namespace, key string) error {
	s.mu.Lock()
	seq := s.allocSeqLocked()
	if err := s.appendPendingBatchLocked([]walEntry{{Op: walDelete, Namespace: namespace, Key: key}}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.applyOverlayLocked(seq, walDelete, namespace, key, "")
	trigger := s.dirtyThresholdExceededLocked()
	s.mu.Unlock()
	if trigger {
		s.signalFlush()
	}
	return s.mem.Delete(ctx, namespace, key)
}

// DeletePrefix publishes a single ranged tombstone instead of enumerating and
// tombstoning every matching key (the pre-1.8 behavior, O(n) log lines and
// O(n) flush statements for a namespace with many keys under prefix). Reads
// resolve the tombstone against per-key overlay entries by sequence number
// (see resolvePendingLocked) and flush executes it as one ranged DELETE (see
// hybridFlushDeletePrefix).
func (s *HybridStore) DeletePrefix(ctx context.Context, namespace, prefix string) error {
	s.mu.Lock()
	seq := s.allocSeqLocked()
	if err := s.appendPendingBatchLocked([]walEntry{{Op: walDeletePrefix, Namespace: namespace, Key: prefix}}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.applyOverlayLocked(seq, walDeletePrefix, namespace, prefix, "")
	trigger := s.dirtyThresholdExceededLocked()
	s.mu.Unlock()
	if trigger {
		s.signalFlush()
	}
	if deleter, ok := any(s.mem).(PrefixDeleter); ok {
		return deleter.DeletePrefix(ctx, namespace, prefix)
	}
	// MemoryStore always implements PrefixDeleter; this branch only guards
	// against a future mem implementation that doesn't.
	return nil
}

// List serves from memory after the initial seed completes. During seed it
// uses SQLite plus the dirty overlay to avoid first-request stalls.
func (s *HybridStore) List(ctx context.Context, namespace, prefix string) ([]string, error) {
	if shouldReadHybridNamespaceFromSQLite(namespace) {
		if s.sqliteReadyNow() {
			keys, err := s.sqliteList(ctx, namespace, prefix)
			if err != nil {
				return nil, err
			}
			return s.mergePendingKeys(namespace, prefix, keys), nil
		}
		keys, err := s.mem.List(ctx, namespace, prefix)
		if err != nil {
			return nil, err
		}
		return s.mergePendingKeys(namespace, prefix, keys), nil
	}
	if s.isLoaded() {
		return s.mem.List(ctx, namespace, prefix)
	}
	if s.sqliteReadyNow() {
		keys, err := s.sqliteList(ctx, namespace, prefix)
		if err != nil {
			return nil, err
		}
		return s.mergePendingKeys(namespace, prefix, keys), nil
	}
	keys, err := s.mem.List(ctx, namespace, prefix)
	if err != nil {
		return nil, err
	}
	return s.mergePendingKeys(namespace, prefix, keys), nil
}

func (s *HybridStore) ListNamespaces(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	var namespaces []string
	add := func(items []string) {
		for _, namespace := range items {
			if namespace == "" || seen[namespace] {
				continue
			}
			seen[namespace] = true
			namespaces = append(namespaces, namespace)
		}
	}

	memNamespaces, err := s.mem.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	add(memNamespaces)

	if s.sqliteReadyNow() {
		sqliteNamespaces, err := s.sqliteListNamespaces(ctx)
		if err != nil {
			return nil, err
		}
		add(sqliteNamespaces)
	}
	add(s.pendingNamespaces())

	sort.Strings(namespaces)
	if namespaces == nil {
		return []string{}, nil
	}
	return namespaces, nil
}

// Scan serves from memory after the initial seed completes. During seed it
// uses SQLite plus the dirty overlay to avoid first-request stalls.
func (s *HybridStore) Scan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	if shouldReadHybridNamespaceFromSQLite(namespace) {
		if s.sqliteReadyNow() {
			pairs, err := s.sqliteScan(ctx, namespace, prefix)
			if err != nil {
				return nil, err
			}
			return s.mergePendingPairs(namespace, prefix, pairs), nil
		}
		pairs, err := s.mem.Scan(ctx, namespace, prefix)
		if err != nil {
			return nil, err
		}
		return s.mergePendingPairs(namespace, prefix, pairs), nil
	}
	if s.isLoaded() {
		return s.mem.Scan(ctx, namespace, prefix)
	}
	if s.sqliteReadyNow() {
		pairs, err := s.sqliteScan(ctx, namespace, prefix)
		if err != nil {
			return nil, err
		}
		return s.mergePendingPairs(namespace, prefix, pairs), nil
	}
	pairs, err := s.mem.Scan(ctx, namespace, prefix)
	if err != nil {
		return nil, err
	}
	return s.mergePendingPairs(namespace, prefix, pairs), nil
}

// ScanPage is the paginated counterpart to Scan (3.2 in
// docs/storage-plan.md). Branching mirrors Scan/List exactly: once a TierHot
// namespace has finished seeding, memory alone is authoritative (every write
// updates mem synchronously inside Set/Delete/DeletePrefix, so there is
// nothing left to merge) and ScanPage delegates straight to
// MemoryStore.ScanPage's seek-based pagination. Every other case — a
// TierCached namespace, or a TierHot namespace still mid-seed — must merge a
// paginated base read (SQLite once ready, memory as a startup fallback)
// against the pending write overlay; see hybridScanPageMerged for how that
// merge preserves exact pagination correctness across the boundary between
// persisted/seeded state and not-yet-flushed writes.
func (s *HybridStore) ScanPage(ctx context.Context, namespace, prefix, startAfter string, limit int) ([]KV, string, error) {
	if shouldReadHybridNamespaceFromSQLite(namespace) {
		if s.sqliteReadyNow() {
			return s.hybridScanPageMerged(ctx, namespace, prefix, startAfter, limit, s.sqliteScanPage)
		}
		return s.hybridScanPageMerged(ctx, namespace, prefix, startAfter, limit, s.mem.ScanPage)
	}
	if s.isLoaded() {
		return s.mem.ScanPage(ctx, namespace, prefix, startAfter, limit)
	}
	if s.sqliteReadyNow() {
		return s.hybridScanPageMerged(ctx, namespace, prefix, startAfter, limit, s.sqliteScanPage)
	}
	return s.hybridScanPageMerged(ctx, namespace, prefix, startAfter, limit, s.mem.ScanPage)
}

// hybridScanPageBaseFunc fetches one page of raw, overlay-unaware results
// from whichever base source ScanPage selected — s.sqliteScanPage and
// s.mem.ScanPage both already satisfy this signature directly, since
// Store.ScanPage's shape is exactly what a "base source" needs to provide.
type hybridScanPageBaseFunc func(ctx context.Context, namespace, prefix, startAfter string, limit int) ([]KV, string, error)

// hybridScanPageMergeChunk is the page size hybridScanPageMerged requests
// from the base source per internal round trip. It is independent of the
// caller's requested limit — see hybridScanPageMerged.
const hybridScanPageMergeChunk = 1000

// hybridScanPageMerged pages through a base source (SQLite or memory) and
// merges the result with the pending write overlay, preserving exact
// pagination correctness across the boundary between persisted/seeded state
// and not-yet-flushed writes.
//
// Correctness approach: the overlay for any one namespace is bounded by the
// configured dirty-flush thresholds (HybridOptions.DirtyEntryThreshold /
// DirtyByteThreshold — a burst triggers an early flush well before the
// overlay grows large; see dirtyThresholdExceededLocked), never by the
// namespace's total size. That makes it always cheap to snapshot the
// *entire* relevant slice of the overlay up front (snapshotOverlayLocked)
// and hold it in memory for the duration of one ScanPage call, even though
// the base itself — the reason ScanPage/pagination exists at all — may be
// huge. The merge walks two sorted-by-key sources in lockstep: the overlay
// snapshot (fully available in memory) and the base (fetched incrementally
// in hybridScanPageMergeChunk-sized rounds via fetchBase), resolving every
// candidate key against the overlay snapshot (which also carries
// prefix-tombstone precedence — see resolveOverlayKey) before deciding
// whether it belongs on this page. This correctly handles every interesting
// case: a pending Set introducing a brand-new key inside the page's key
// range, a pending Delete or DeletePrefix removing a key the base still
// has, and an overlay entry landing exactly on a page boundary — nextKey is
// always the literal next candidate key the merge walk would have
// considered, so resuming from it on the next call can never skip or
// duplicate a key regardless of which source (base or overlay) it came
// from.
//
// This never holds s.mu while calling fetchBase — snapshotOverlayLocked
// takes a point-in-time copy up front and releases the lock before the walk
// begins, so a slow multi-page SQLite scan cannot block concurrent
// Set/Delete calls.
//
// Efficiency note: the worst case (a large not-yet-flushed DeletePrefix
// tombstoning a long run of base keys right at the start of the requested
// range, or a caller passing limit <= 0 for "no limit") may fetch many
// chunks from the base before returning — bounded by the total remaining
// size of the base, i.e. no worse than the unpaginated Scan this
// complements for large reads. The plan's escape hatch for this item
// explicitly allows falling back to a full mergePendingPairs-then-slice
// implementation if exact pagination proves too costly to get right; that
// fallback turned out unnecessary here because the overlay — not the base —
// is the only thing that needs to be held in full, and the overlay is
// already bounded by design.
func (s *HybridStore) hybridScanPageMerged(
	ctx context.Context,
	namespace, prefix, startAfter string,
	limit int,
	fetchBase hybridScanPageBaseFunc,
) ([]KV, string, error) {
	snap := s.snapshotOverlayLocked(namespace, prefix, startAfter)

	var result []KV
	overlayIdx := 0

	var baseBatch []KV
	basePos := 0
	baseCursor := startAfter
	baseHasMore := true

	for {
		if basePos >= len(baseBatch) && baseHasMore {
			batch, next, err := fetchBase(ctx, namespace, prefix, baseCursor, hybridScanPageMergeChunk)
			if err != nil {
				return nil, "", err
			}
			baseBatch = batch
			basePos = 0
			baseCursor = next
			baseHasMore = next != ""
		}

		baseAvail := basePos < len(baseBatch)
		overlayAvail := overlayIdx < len(snap.newKeys)
		if !baseAvail && !overlayAvail {
			return finalizeMergedScanPage(result, "")
		}

		var candidateKey, candidateBaseValue string
		fromBase, fromOverlay := false, false
		switch {
		case baseAvail && overlayAvail && baseBatch[basePos].Key == snap.newKeys[overlayIdx]:
			candidateKey = baseBatch[basePos].Key
			candidateBaseValue = baseBatch[basePos].Value
			fromBase, fromOverlay = true, true
		case baseAvail && (!overlayAvail || baseBatch[basePos].Key < snap.newKeys[overlayIdx]):
			candidateKey = baseBatch[basePos].Key
			candidateBaseValue = baseBatch[basePos].Value
			fromBase = true
		default:
			candidateKey = snap.newKeys[overlayIdx]
			fromOverlay = true
		}

		if limit > 0 && len(result) == limit {
			// candidateKey is the first item beyond this page — proof a next
			// page exists — but nextKey must be the *last included* item's
			// key (startAfter is exclusive), not candidateKey itself, or the
			// next call would skip candidateKey entirely.
			return finalizeMergedScanPage(result, result[len(result)-1].Key)
		}

		if resolved, ok := snap.resolve(namespace, candidateKey); ok {
			if !resolved.deleted {
				result = append(result, KV{Key: candidateKey, Value: resolved.value})
			}
		} else if fromBase {
			result = append(result, KV{Key: candidateKey, Value: candidateBaseValue})
		}

		if fromBase {
			basePos++
		}
		if fromOverlay {
			overlayIdx++
		}
	}
}

func finalizeMergedScanPage(result []KV, nextKey string) ([]KV, string, error) {
	if result == nil {
		result = []KV{}
	}
	return result, nextKey, nil
}

// hybridOverlaySnapshot is a point-in-time, lock-free-usable copy of the
// portion of HybridStore's pending overlay relevant to one namespace+prefix
// ScanPage call, built once under s.mu (see snapshotOverlayLocked) so
// hybridScanPageMerged's walk through the (potentially slow,
// multi-round-trip) base source never holds s.mu — see that function's doc
// comment for why this is safe.
type hybridOverlaySnapshot struct {
	dirty              map[string]dirtyEntry
	flushing           map[string]dirtyEntry
	tombstones         []prefixTombstone
	flushingTombstones []prefixTombstone

	// newKeys holds every key with an explicit dirty/flushing entry under
	// namespace+prefix, strictly after startAfter, sorted ascending. A
	// prefix tombstone alone never introduces a candidate key (it only
	// shadows existing ones — see forEachPendingCandidateKeyLocked, which
	// this mirrors), so tombstone-only coverage of a base key is instead
	// caught when resolve() is called against that base key directly during
	// the merge walk.
	newKeys []string
}

// resolve applies the exact same overlay precedence as
// HybridStore.resolvePendingLocked, against this snapshot's copied data
// instead of the live store — safe to call without s.mu held.
func (snap hybridOverlaySnapshot) resolve(namespace, key string) (dirtyEntry, bool) {
	return resolveOverlayKey(snap.dirty, snap.flushing, snap.tombstones, snap.flushingTombstones, namespace, key)
}

// snapshotOverlayLocked builds a hybridOverlaySnapshot for namespace+prefix,
// restricted to overlay entries newer than startAfter. Copies the full
// dirty/flushing maps and tombstone lists (bounded by the configured
// dirty-flush thresholds, not by namespace size — see
// hybridScanPageMerged's doc comment) rather than trying to filter
// client-side per source map, since a straightforward point-in-time copy is
// what makes it safe to release s.mu before hybridScanPageMerged's walk
// begins.
func (s *HybridStore) snapshotOverlayLocked(namespace, prefix, startAfter string) hybridOverlaySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := hybridOverlaySnapshot{
		dirty:              make(map[string]dirtyEntry, len(s.dirty)),
		flushing:           make(map[string]dirtyEntry, len(s.flushing)),
		tombstones:         append([]prefixTombstone(nil), s.tombstones...),
		flushingTombstones: append([]prefixTombstone(nil), s.flushingTombstones...),
	}
	for k, v := range s.dirty {
		snap.dirty[k] = v
	}
	for k, v := range s.flushing {
		snap.flushing[k] = v
	}

	seen := make(map[string]struct{})
	collect := func(m map[string]dirtyEntry) {
		for composite := range m {
			ns, key := splitStoreKey(composite)
			if ns != namespace || !strings.HasPrefix(key, prefix) {
				continue
			}
			if startAfter != "" && key <= startAfter {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			snap.newKeys = append(snap.newKeys, key)
		}
	}
	collect(s.dirty)
	collect(s.flushing)
	sort.Strings(snap.newKeys)
	return snap
}

// WaitReady blocks until the background SQLite open and migration has
// completed, satisfying state.ReadyAwaiter. Returns nil when the store
// is ready for reads, or ctx.Err() if the context is cancelled first.
// Callers that need to guarantee persisted data is visible (e.g. startup
// reload routines) should call this before performing a full Scan. Returns
// the seed error when the store degraded to memory-only (see
// degradeToMemoryOnly) — that is the correct signal for such callers, even
// though ordinary Get/List/Scan callers are unaffected.
func (s *HybridStore) WaitReady(ctx context.Context) error {
	select {
	case <-s.sqliteReady:
		return s.getLoadErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// NotReady implements state.NotReadyReporter: true only while the background
// schema migration is still in flight (sqliteReady not yet closed) —
// deliberately not gated on sqliteDegraded or isLoaded. A store that finished
// migrating and then degraded to memory-only (see degradeToMemoryOnly) is
// done with its startup phase and serves memory reads correctly forever
// after; that is an ongoing health condition (PersistentHealth), not a
// "still starting up" one, so NotReady must not keep reporting true for it.
// Likewise, the seed phase that follows a successful migration is not
// included here: once sqliteReady closes, Get/List/Scan already fall back to
// querying SQLite directly for anything not yet loaded into memory, so reads
// are accurate (just slower) — only the migration itself has a window where
// TierHot reads would otherwise silently return "not found" for data that
// exists once migration finishes.
func (s *HybridStore) NotReady() bool {
	select {
	case <-s.sqliteReady:
		return false
	default:
		return true
	}
}

// Flush synchronously persists all dirty writes accepted before this call. A
// no-op when the persistent backend is degraded to memory-only.
func (s *HybridStore) Flush(ctx context.Context) error {
	select {
	case <-s.loaded:
		if s.sqlite == nil || s.sqliteDegraded.Load() {
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.flush(ctx)
}

func (s *HybridStore) PersistentHealth() PersistentHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	health := s.health
	health.PendingWrites = len(s.dirty) + len(s.flushing) + len(s.tombstones) + len(s.flushingTombstones)
	return health
}

// DB returns the underlying *sql.DB, satisfying SQLiteDBProvider. Blocks
// until the background SQLite open has completed. Returns nil if the open
// failed — callers that hit this path should have already seen the load
// error propagate via a Get/List/Scan call. Unlike KV routing, this is not
// gated on sqliteDegraded: a KV-seed failure doesn't necessarily mean the
// connection is unusable for service-owned dedicated tables (e.g. DynamoDB).
func (s *HybridStore) DB() *sql.DB {
	<-s.sqliteReady
	if s.sqlite == nil {
		return nil
	}
	return s.sqlite.DB()
}

// Close stops the background goroutines, performs a final synchronous flush
// of all pending dirty entries to SQLite (skipped when the store is degraded
// to memory-only), then closes the database and read pool.
func (s *HybridStore) Close() error {
	s.cancel()
	s.wg.Wait()
	// After wg.Wait, seedFromSQLite has finished — sqlite is either fully
	// open or nil (open failed), and sqliteDegraded is stable.
	degraded := s.sqlite == nil || s.sqliteDegraded.Load()
	if degraded {
		loadErr := s.getLoadErr()
		var sqliteCloseErr error
		if s.sqlite != nil {
			sqliteCloseErr = s.sqlite.Close()
		}
		s.closeReadPool()
		pendingCloseErr := s.closePendingFile()
		if sqliteCloseErr != nil {
			return sqliteCloseErr
		}
		if pendingCloseErr != nil {
			return pendingCloseErr
		}
		return loadErr
	}
	if err := s.flush(context.Background()); err != nil {
		_ = s.sqlite.Close()
		s.closeReadPool()
		_ = s.closePendingFile()
		return fmt.Errorf("hybrid store: final flush: %w", err)
	}
	if err := s.sqlite.Close(); err != nil {
		s.closeReadPool()
		_ = s.closePendingFile()
		return err
	}
	s.closeReadPool()
	return s.closePendingFile()
}

func (s *HybridStore) closeReadPool() {
	if s.sqliteRead != nil {
		_ = s.sqliteRead.Close()
	}
}

// run is the background goroutine that flushes dirty entries on a timer or
// when signalled early by a dirty-threshold trigger (1.4).
func (s *HybridStore) run(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.flush(ctx); err != nil {
				s.logWarn("hybrid flush failed; will retry", zap.Error(err))
			}
		case <-s.flushSignal:
			if err := s.flush(ctx); err != nil {
				s.logWarn("hybrid flush failed; will retry", zap.Error(err))
			}
		case <-ctx.Done():
			return
		}
	}
}

// runPendingSync periodically fsyncs the pending log file when SyncMode is
// interval, mirroring WALStore.runPeriodicSync. always-mode syncs happen
// inline in appendPendingBatchLocked; never-mode relies on the OS plus the
// unconditional sync performed by closePendingFile on Close.
func (s *HybridStore) runPendingSync(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if s.pendingFile != nil {
				_ = s.pendingFile.Sync()
			}
			s.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

// runMaintenance is the background goroutine that runs routine SQLite
// housekeeping (3.5): a passive WAL checkpoint plus a conditional
// incremental vacuum, via the shared runSQLitePragmaMaintenance helper (see
// maintenance.go). Ticks that land while the store is still seeding or has
// permanently degraded to memory-only are silent no-ops — this must never
// run on the request path and must never error out the store; a failed
// pragma is logged and retried on the next tick.
func (s *HybridStore) runMaintenance(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.maintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !s.sqliteReadyNow() {
				continue // still seeding, or degraded to memory-only — nothing to maintain
			}
			runSQLitePragmaMaintenance(ctx, s.sqlite.db, s.log, "hybrid maintenance")
		case <-ctx.Done():
			return
		}
	}
}

// flush atomically steals the current pending ops (and their dirty/tombstone
// read-cache mirrors) and writes them to SQLite in a single transaction, in
// original order. Holding the mu lock only long enough to swap means new
// writes can accumulate immediately while the (potentially slow) SQLite
// writes proceed without blocking callers.
//
// If the background SQLite open has not completed yet, or the store has
// degraded to memory-only (see degradeToMemoryOnly), flush is a silent no-op;
// dirty entries remain in memory and in the pending log for the next flush
// attempt or restart replay. This intentionally does not retry-and-warn every
// tick once degraded — the failure was already logged loudly once.
func (s *HybridStore) flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	var err error
	for attempt := 0; attempt <= hybridSQLiteWriteRetryMax; attempt++ {
		err = s.flushOnce(ctx)
		if !shouldRetryHybridSQLiteWrite(err) || attempt == hybridSQLiteWriteRetryMax {
			return err
		}
		select {
		case <-time.After(time.Duration(attempt+1) * hybridSQLiteRetryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (s *HybridStore) flushOnce(ctx context.Context) error {
	start := time.Now()
	select {
	case <-s.loaded:
		if s.sqlite == nil || s.sqliteDegraded.Load() {
			return nil
		}
	default:
		return nil // SQLite not ready yet — defer flush
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if len(s.pendingOps) == 0 {
		s.mu.Unlock()
		return nil
	}
	toFlushOps := s.pendingOps
	s.pendingOps = nil
	toFlushDirty := s.dirty
	s.dirty = make(map[string]dirtyEntry, 64)
	s.flushing = toFlushDirty
	toFlushTombstones := s.tombstones
	s.tombstones = nil
	s.flushingTombstones = toFlushTombstones
	s.dirtyApproxBytes = 0
	s.mu.Unlock()

	committed := false
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if committed {
			s.flushing = nil
			s.flushingTombstones = nil
			return
		}
		// Put the failed batch back ahead of anything accumulated during the
		// attempt so the next flush retries in original order.
		s.pendingOps = append(toFlushOps, s.pendingOps...)
		for composite, entry := range toFlushDirty {
			if _, overwritten := s.dirty[composite]; !overwritten {
				s.dirty[composite] = entry
			}
		}
		s.tombstones = append(toFlushTombstones, s.tombstones...)
		s.flushing = nil
		s.flushingTombstones = nil
		s.recomputeDirtyApproxBytesLocked()
	}()

	tx, err := s.sqlite.db.BeginTx(ctx, nil)
	if err != nil {
		s.markPersistentError(err)
		return fmt.Errorf("hybrid flush begin tx: %w", err)
	}
	for _, op := range toFlushOps {
		switch op.op {
		case walSet:
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO kv (namespace, key, value) VALUES (?, ?, ?)`,
				op.namespace, op.key, op.value); err != nil {
				tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
				s.markPersistentError(err)
				return fmt.Errorf("hybrid flush set [%s/%s]: %w", op.namespace, op.key, err)
			}
		case walDelete:
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM kv WHERE namespace = ? AND key = ?`, op.namespace, op.key); err != nil {
				tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
				s.markPersistentError(err)
				return fmt.Errorf("hybrid flush delete [%s/%s]: %w", op.namespace, op.key, err)
			}
		case walDeletePrefix:
			if err := hybridFlushDeletePrefix(ctx, tx, op.namespace, op.key); err != nil {
				tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
				s.markPersistentError(err)
				return fmt.Errorf("hybrid flush delete prefix [%s/%s*]: %w", op.namespace, op.key, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		s.markPersistentError(err)
		return fmt.Errorf("hybrid flush commit: %w", err)
	}
	if err := s.compactPendingAfterFlush(); err != nil {
		s.markPersistentError(err)
		return err
	}
	committed = true
	s.markPersistentSuccess()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		s.logWarn("hybrid flush slow", zap.Int("ops", len(toFlushOps)), zap.Duration("elapsed", elapsed))
	} else {
		s.logDebug("hybrid flush complete", zap.Int("ops", len(toFlushOps)), zap.Duration("elapsed", elapsed))
	}
	return nil
}

// hybridFlushDeletePrefix issues a single ranged DELETE for a prefix
// tombstone inside the caller's transaction, mirroring
// SQLiteStore.DeletePrefix's key-range logic (kept local rather than calling
// into sqlite.go so the statement participates in the flush transaction).
func hybridFlushDeletePrefix(ctx context.Context, tx *sql.Tx, namespace, prefix string) error {
	var err error
	if prefix == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ?`, namespace)
		return err
	}
	upper := prefixUpperBound(prefix)
	if upper == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key >= ?`, namespace, prefix)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key >= ? AND key < ?`, namespace, prefix, upper)
	}
	return err
}

// allocSeqLocked returns the next monotonic sequence number for the pending
// overlay. Callers must hold s.mu.
func (s *HybridStore) allocSeqLocked() int64 {
	s.nextSeq++
	return s.nextSeq
}

// applyOverlayLocked updates the in-memory pending overlay (the dirty/
// tombstone read caches and the ordered pendingOps flush log) for one
// operation. It does not touch the on-disk pending log file — the write path
// (Set/Delete/DeletePrefix) durably appends first via appendPendingBatchLocked
// and replay relies on the file already containing the entry. Callers must
// hold s.mu.
func (s *HybridStore) applyOverlayLocked(seq int64, op walOp, namespace, key, value string) {
	switch op {
	case walSet:
		s.dirty[storeKey(namespace, key)] = dirtyEntry{value: value, seq: seq}
		s.dirtyApproxBytes += int64(len(namespace) + len(key) + len(value))
	case walDelete:
		s.dirty[storeKey(namespace, key)] = dirtyEntry{deleted: true, seq: seq}
		s.dirtyApproxBytes += int64(len(namespace) + len(key))
	case walDeletePrefix:
		s.tombstones = append(s.tombstones, prefixTombstone{seq: seq, namespace: namespace, prefix: key})
		s.dirtyApproxBytes += int64(len(namespace) + len(key))
	default:
		s.logWarn("hybrid pending entry has unknown op; ignoring",
			zap.String("op", string(op)), zap.String("namespace", namespace), zap.String("key", key))
		return
	}
	s.pendingOps = append(s.pendingOps, hybridPendingOp{seq: seq, op: op, namespace: namespace, key: key, value: value})
}

// dirtyThresholdExceededLocked reports whether the unflushed overlay has
// grown past the configured entry-count or approximate-byte thresholds (1.4).
// Callers must hold s.mu.
func (s *HybridStore) dirtyThresholdExceededLocked() bool {
	if s.dirtyEntryThreshold > 0 && len(s.pendingOps) >= s.dirtyEntryThreshold {
		return true
	}
	if s.dirtyByteThreshold > 0 && s.dirtyApproxBytes >= s.dirtyByteThreshold {
		return true
	}
	return false
}

// signalFlush sends a coalesced, non-blocking early-flush trigger to the run
// loop. Never performs the flush itself — the caller's goroutine must not
// block on or perform SQLite I/O here.
func (s *HybridStore) signalFlush() {
	select {
	case s.flushSignal <- struct{}{}:
	default:
	}
}

// recomputeDirtyApproxBytesLocked recomputes the approximate unflushed byte
// counter from scratch. Used only on the (rare) flush-failure restore path,
// where incremental tracking would otherwise drift. Callers must hold s.mu.
func (s *HybridStore) recomputeDirtyApproxBytesLocked() {
	var total int64
	for composite, entry := range s.dirty {
		ns, key := splitStoreKey(composite)
		total += int64(len(ns) + len(key) + len(entry.value))
	}
	for _, t := range s.tombstones {
		total += int64(len(t.namespace) + len(t.prefix))
	}
	s.dirtyApproxBytes = total
}

// resolvePendingLocked returns the effective pending-overlay state for
// namespace/key, applying prefix-tombstone precedence by sequence number: an
// explicit per-key write only wins over a covering tombstone if it happened
// after the tombstone (higher seq); otherwise the tombstone's deletion wins.
// This is what makes "Set(k1); DeletePrefix(p); Set(k2)" (both k1, k2 under
// p) resolve correctly: k1 reads deleted, k2 survives. Callers must hold
// s.mu.
//
// The actual precedence logic lives in the package-level resolveOverlayKey
// so ScanPage's merge walk (hybridScanPageMerged) can apply the identical
// rule against a point-in-time snapshot of dirty/flushing/tombstones without
// holding s.mu for the (potentially slow, multi-round-trip) duration of a
// paginated base scan — see hybridOverlaySnapshot.
func (s *HybridStore) resolvePendingLocked(namespace, key string) (dirtyEntry, bool) {
	return resolveOverlayKey(s.dirty, s.flushing, s.tombstones, s.flushingTombstones, namespace, key)
}

// latestPrefixTombstoneSeqLocked returns the highest sequence number among
// pending prefix tombstones (current and in-flight-flush generations) that
// cover key in namespace. Callers must hold s.mu.
func (s *HybridStore) latestPrefixTombstoneSeqLocked(namespace, key string) (int64, bool) {
	return latestPrefixTombstoneSeqIn(s.tombstones, s.flushingTombstones, namespace, key)
}

// resolveOverlayKey is the pure, lock-free core of resolvePendingLocked: it
// computes the effective pending-overlay state for namespace/key given
// explicit copies of the dirty/flushing maps and the current/in-flight-flush
// tombstone lists. Split out so hybridOverlaySnapshot.resolve (used by
// ScanPage's merge walk against a point-in-time snapshot, not live store
// state — see hybridScanPageMerged) shares the exact same precedence rule as
// the request-path resolvePendingLocked instead of risking the two
// implementations drifting apart.
func resolveOverlayKey(dirty, flushing map[string]dirtyEntry, tombstones, flushingTombstones []prefixTombstone, namespace, key string) (dirtyEntry, bool) {
	composite := storeKey(namespace, key)
	entry, hasEntry := dirty[composite]
	if !hasEntry {
		entry, hasEntry = flushing[composite]
	}
	tombSeq, hasTomb := latestPrefixTombstoneSeqIn(tombstones, flushingTombstones, namespace, key)
	if hasEntry && (!hasTomb || entry.seq > tombSeq) {
		return entry, true
	}
	if hasTomb {
		return dirtyEntry{deleted: true, seq: tombSeq}, true
	}
	return dirtyEntry{}, false
}

// latestPrefixTombstoneSeqIn returns the highest sequence number among
// tombstones (current and in-flight-flush generations, passed separately)
// that cover key in namespace. The pure core of
// HybridStore.latestPrefixTombstoneSeqLocked, also used directly by
// resolveOverlayKey against a snapshot copy.
func latestPrefixTombstoneSeqIn(tombstones, flushingTombstones []prefixTombstone, namespace, key string) (int64, bool) {
	best := int64(-1)
	found := false
	check := func(tombs []prefixTombstone) {
		for _, t := range tombs {
			if t.namespace != namespace || !strings.HasPrefix(key, t.prefix) {
				continue
			}
			if !found || t.seq > best {
				best = t.seq
				found = true
			}
		}
	}
	check(tombstones)
	check(flushingTombstones)
	return best, found
}

func (s *HybridStore) pendingValue(namespace, key string) (dirtyEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolvePendingLocked(namespace, key)
}

func (s *HybridStore) pendingNamespaces() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := map[string]bool{}
	var namespaces []string
	add := func(composite string, entry dirtyEntry) {
		if entry.deleted {
			return
		}
		namespace, _, ok := strings.Cut(composite, "\x00")
		if !ok || namespace == "" || seen[namespace] {
			return
		}
		seen[namespace] = true
		namespaces = append(namespaces, namespace)
	}
	for composite, entry := range s.dirty {
		add(composite, entry)
	}
	for composite, entry := range s.flushing {
		add(composite, entry)
	}
	return namespaces
}

func (s *HybridStore) setLoadErr(err error) {
	s.mu.Lock()
	s.loadErr = err
	s.markPersistentErrorLocked(err)
	s.mu.Unlock()
}

func (s *HybridStore) getLoadErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadErr
}

func (s *HybridStore) sqliteGet(ctx context.Context, namespace, key string) (string, bool, error) {
	value, found, err := hybridSQLiteRawGet(ctx, s.readDB(), namespace, key)
	if !shouldRetryHybridSQLiteRead(err) {
		if err != nil {
			s.markPersistentError(err)
		}
		return value, found, err
	}

	retryCtx, cancel := context.WithTimeout(context.Background(), hybridSQLiteReadRetryTimeout)
	defer cancel()
	retryValue, retryFound, retryErr := s.retrySQLiteGet(retryCtx, namespace, key)
	s.logHybridSQLiteRetry("get", retryErr, zap.String("namespace", namespace), zap.String("key", key), zap.Error(err))
	if retryErr != nil {
		s.markPersistentError(retryErr)
	} else {
		s.markPersistentSuccess()
	}
	return retryValue, retryFound, retryErr
}

func (s *HybridStore) sqliteList(ctx context.Context, namespace, prefix string) ([]string, error) {
	keys, err := hybridSQLiteRawList(ctx, s.readDB(), namespace, prefix)
	if !shouldRetryHybridSQLiteRead(err) {
		if err != nil {
			s.markPersistentError(err)
		}
		return keys, err
	}

	retryCtx, cancel := context.WithTimeout(context.Background(), hybridSQLiteReadRetryTimeout)
	defer cancel()
	retryKeys, retryErr := s.retrySQLiteList(retryCtx, namespace, prefix)
	s.logHybridSQLiteRetry("list", retryErr, zap.String("namespace", namespace), zap.String("prefix", prefix), zap.Error(err))
	if retryErr != nil {
		s.markPersistentError(retryErr)
	} else {
		s.markPersistentSuccess()
	}
	return retryKeys, retryErr
}

func (s *HybridStore) sqliteListNamespaces(ctx context.Context) ([]string, error) {
	namespaces, err := hybridSQLiteRawListNamespaces(ctx, s.readDB())
	if !shouldRetryHybridSQLiteRead(err) {
		if err != nil {
			s.markPersistentError(err)
		}
		return namespaces, err
	}

	retryCtx, cancel := context.WithTimeout(context.Background(), hybridSQLiteReadRetryTimeout)
	defer cancel()
	retryNamespaces, retryErr := s.retrySQLiteListNamespaces(retryCtx)
	s.logHybridSQLiteRetry("list namespaces", retryErr, zap.Error(err))
	if retryErr != nil {
		s.markPersistentError(retryErr)
	} else {
		s.markPersistentSuccess()
	}
	return retryNamespaces, retryErr
}

func (s *HybridStore) sqliteScan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	pairs, err := hybridSQLiteRawScan(ctx, s.readDB(), namespace, prefix)
	if !shouldRetryHybridSQLiteRead(err) {
		if err != nil {
			s.markPersistentError(err)
		}
		return pairs, err
	}

	retryCtx, cancel := context.WithTimeout(context.Background(), hybridSQLiteReadRetryTimeout)
	defer cancel()
	retryPairs, retryErr := s.retrySQLiteScan(retryCtx, namespace, prefix)
	s.logHybridSQLiteRetry("scan", retryErr, zap.String("namespace", namespace), zap.String("prefix", prefix), zap.Error(err))
	if retryErr != nil {
		s.markPersistentError(retryErr)
	} else {
		s.markPersistentSuccess()
	}
	return retryPairs, retryErr
}

// sqliteScanPage satisfies hybridScanPageBaseFunc, letting
// hybridScanPageMerged call it interchangeably with s.mem.ScanPage.
func (s *HybridStore) sqliteScanPage(ctx context.Context, namespace, prefix, startAfter string, limit int) ([]KV, string, error) {
	pairs, nextKey, err := hybridSQLiteRawScanPage(ctx, s.readDB(), namespace, prefix, startAfter, limit)
	if !shouldRetryHybridSQLiteRead(err) {
		if err != nil {
			s.markPersistentError(err)
		}
		return pairs, nextKey, err
	}

	retryCtx, cancel := context.WithTimeout(context.Background(), hybridSQLiteReadRetryTimeout)
	defer cancel()
	retryPairs, retryNextKey, retryErr := s.retrySQLiteScanPage(retryCtx, namespace, prefix, startAfter, limit)
	s.logHybridSQLiteRetry("scan page", retryErr,
		zap.String("namespace", namespace), zap.String("prefix", prefix),
		zap.String("start_after", startAfter), zap.Error(err))
	if retryErr != nil {
		s.markPersistentError(retryErr)
	} else {
		s.markPersistentSuccess()
	}
	return retryPairs, retryNextKey, retryErr
}

func (s *HybridStore) retrySQLiteGet(ctx context.Context, namespace, key string) (string, bool, error) {
	var lastErr error
	for {
		value, found, err := hybridSQLiteRawGet(ctx, s.readDB(), namespace, key)
		if !shouldRetryHybridSQLiteRead(err) {
			return value, found, err
		}
		lastErr = err
		if err := sleepHybridSQLiteRetry(ctx); err != nil {
			return "", false, fallbackRetryErr(lastErr, err)
		}
	}
}

func (s *HybridStore) retrySQLiteList(ctx context.Context, namespace, prefix string) ([]string, error) {
	var lastErr error
	for {
		keys, err := hybridSQLiteRawList(ctx, s.readDB(), namespace, prefix)
		if !shouldRetryHybridSQLiteRead(err) {
			return keys, err
		}
		lastErr = err
		if err := sleepHybridSQLiteRetry(ctx); err != nil {
			return nil, fallbackRetryErr(lastErr, err)
		}
	}
}

func (s *HybridStore) retrySQLiteListNamespaces(ctx context.Context) ([]string, error) {
	var lastErr error
	for {
		namespaces, err := hybridSQLiteRawListNamespaces(ctx, s.readDB())
		if !shouldRetryHybridSQLiteRead(err) {
			return namespaces, err
		}
		lastErr = err
		if err := sleepHybridSQLiteRetry(ctx); err != nil {
			return nil, fallbackRetryErr(lastErr, err)
		}
	}
}

func (s *HybridStore) retrySQLiteScan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	var lastErr error
	for {
		pairs, err := hybridSQLiteRawScan(ctx, s.readDB(), namespace, prefix)
		if !shouldRetryHybridSQLiteRead(err) {
			return pairs, err
		}
		lastErr = err
		if err := sleepHybridSQLiteRetry(ctx); err != nil {
			return nil, fallbackRetryErr(lastErr, err)
		}
	}
}

func (s *HybridStore) retrySQLiteScanPage(ctx context.Context, namespace, prefix, startAfter string, limit int) ([]KV, string, error) {
	var lastErr error
	for {
		pairs, nextKey, err := hybridSQLiteRawScanPage(ctx, s.readDB(), namespace, prefix, startAfter, limit)
		if !shouldRetryHybridSQLiteRead(err) {
			return pairs, nextKey, err
		}
		lastErr = err
		if err := sleepHybridSQLiteRetry(ctx); err != nil {
			return nil, "", fallbackRetryErr(lastErr, err)
		}
	}
}

// hybridSQLiteRawGet/List/ListNamespaces/Scan run the same queries as
// SQLiteStore's methods but against an arbitrary *sql.DB, so hybrid.go can
// route them through its dedicated read pool (1.2) instead of always going
// through the single-connection writer pool that SQLiteStore itself uses.

func hybridSQLiteRawGet(ctx context.Context, db *sql.DB, namespace, key string) (string, bool, error) {
	var value string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM kv WHERE namespace = ? AND key = ?`, namespace, key,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("hybrid sqlite get [%s/%s]: %w", namespace, key, err)
	}
	return value, true, nil
}

func hybridSQLiteRawList(ctx context.Context, db *sql.DB, namespace, prefix string) ([]string, error) {
	var rows *sql.Rows
	var err error
	if prefix == "" {
		rows, err = db.QueryContext(ctx, `SELECT key FROM kv WHERE namespace = ? ORDER BY key`, namespace)
	} else {
		upper := prefixUpperBound(prefix)
		if upper == "" {
			rows, err = db.QueryContext(ctx, `SELECT key FROM kv WHERE namespace = ? AND key >= ? ORDER BY key`, namespace, prefix)
		} else {
			rows, err = db.QueryContext(ctx, `SELECT key FROM kv WHERE namespace = ? AND key >= ? AND key < ? ORDER BY key`, namespace, prefix, upper)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("hybrid sqlite list [%s/%s*]: %w", namespace, prefix, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("hybrid sqlite list scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hybrid sqlite list rows: %w", err)
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func hybridSQLiteRawListNamespaces(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT namespace FROM kv ORDER BY namespace`)
	if err != nil {
		return nil, fmt.Errorf("hybrid sqlite list namespaces: %w", err)
	}
	defer rows.Close()

	var namespaces []string
	for rows.Next() {
		var namespace string
		if err := rows.Scan(&namespace); err != nil {
			return nil, fmt.Errorf("hybrid sqlite list namespaces scan: %w", err)
		}
		namespaces = append(namespaces, namespace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hybrid sqlite list namespaces rows: %w", err)
	}
	if namespaces == nil {
		namespaces = []string{}
	}
	return namespaces, nil
}

func hybridSQLiteRawScan(ctx context.Context, db *sql.DB, namespace, prefix string) ([]KV, error) {
	var rows *sql.Rows
	var err error
	if prefix == "" {
		rows, err = db.QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ? ORDER BY key`, namespace)
	} else {
		upper := prefixUpperBound(prefix)
		if upper == "" {
			rows, err = db.QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ? AND key >= ? ORDER BY key`, namespace, prefix)
		} else {
			rows, err = db.QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ? AND key >= ? AND key < ? ORDER BY key`, namespace, prefix, upper)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("hybrid sqlite scan [%s/%s*]: %w", namespace, prefix, err)
	}
	defer rows.Close()

	var pairs []KV
	for rows.Next() {
		var kv KV
		if err := rows.Scan(&kv.Key, &kv.Value); err != nil {
			return nil, fmt.Errorf("hybrid sqlite scan row: %w", err)
		}
		pairs = append(pairs, kv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("hybrid sqlite scan rows: %w", err)
	}
	if pairs == nil {
		pairs = []KV{}
	}
	return pairs, nil
}

// hybridSQLiteRawScanPage is hybridSQLiteRawScan's paginated counterpart. It
// shares its query shape with SQLiteStore.ScanPage via the scanPageQuery /
// collectScanPageRows / finalizeScanPage helpers in sqlite.go so the two
// SQLite-backed Store implementations can never drift on key-range
// semantics, while still running against an arbitrary *sql.DB (hybrid.go's
// dedicated read pool) like the other hybridSQLiteRaw* helpers.
func hybridSQLiteRawScanPage(ctx context.Context, db *sql.DB, namespace, prefix, startAfter string, limit int) ([]KV, string, error) {
	query, args := scanPageQuery(namespace, prefix, startAfter, limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("hybrid sqlite scan page [%s/%s* after %q]: %w", namespace, prefix, startAfter, err)
	}
	defer rows.Close()

	pairs, err := collectScanPageRows(rows)
	if err != nil {
		return nil, "", err
	}
	return finalizeScanPage(pairs, limit)
}

func sleepHybridSQLiteRetry(ctx context.Context) error {
	select {
	case <-time.After(hybridSQLiteRetryDelay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func fallbackRetryErr(lastErr, ctxErr error) error {
	if lastErr != nil {
		return lastErr
	}
	return ctxErr
}

func shouldRetryHybridSQLiteRead(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isSQLiteTransient(err)
}

func shouldRetryHybridSQLiteWrite(err error) bool {
	return isSQLiteTransient(err)
}

func isSQLiteTransient(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *msqlite.Error
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code()
		return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED || code == sqlite3.SQLITE_INTERRUPT
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "interrupted (9)") ||
		strings.Contains(msg, "sqlite_interrupt")
}

func (s *HybridStore) logHybridSQLiteRetry(op string, retryErr error, fields ...zap.Field) {
	fields = append(fields, zap.String("op", op))
	if retryErr != nil {
		fields = append(fields, zap.NamedError("retry_error", retryErr))
		s.logWarn("hybrid sqlite read retry failed", fields...)
		return
	}
	s.logDebug("hybrid sqlite read retry succeeded", fields...)
}

// replayPendingLog replays the on-disk pending log into memory before the
// store is published. An undecodable line that is not the final line is
// logged and skipped (with a summary count at the end) rather than aborting
// startup — only a truncated final line (the torn-write case from a crash
// mid-append) gets the original "ignore and stop" treatment, since anything
// after a torn write cannot exist.
func (s *HybridStore) replayPendingLog() error {
	data, err := os.ReadFile(s.pendingPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("hybrid store: read pending log replay %q: %w", s.pendingPath, err)
	}
	lines := bytes.Split(data, []byte{'\n'})
	ctx := context.Background()
	var skipped int
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry walEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			isFinal := i == len(lines)-1 && len(data) > 0 && data[len(data)-1] != '\n'
			if isFinal {
				s.logWarn("hybrid pending log has incomplete final entry; ignoring", zap.Error(err))
				break
			}
			skipped++
			s.logWarn("hybrid pending log entry undecodable; skipping",
				zap.Int("line", i+1), zap.Error(err))
			continue
		}
		s.applyPendingEntry(ctx, entry)
	}
	if skipped > 0 {
		s.logWarn("hybrid pending log replay skipped corrupt entries",
			zap.Int("skipped", skipped), zap.Int("total_lines", len(lines)))
	}
	return nil
}

// applyPendingEntry replays one pending-log entry into memory and the
// overlay during startup (single-threaded, before the store is published —
// locking s.mu here is harmless, not required for correctness, and kept only
// so applyOverlayLocked has one calling convention). An unknown op is logged
// and ignored instead of silently dropped.
func (s *HybridStore) applyPendingEntry(ctx context.Context, entry walEntry) {
	switch entry.Op {
	case walSet:
		_ = s.mem.Set(ctx, entry.Namespace, entry.Key, entry.Value)
	case walDelete:
		_ = s.mem.Delete(ctx, entry.Namespace, entry.Key)
	case walDeletePrefix:
		if deleter, ok := any(s.mem).(PrefixDeleter); ok {
			_ = deleter.DeletePrefix(ctx, entry.Namespace, entry.Key)
		}
	}
	s.mu.Lock()
	seq := s.allocSeqLocked()
	s.applyOverlayLocked(seq, entry.Op, entry.Namespace, entry.Key, entry.Value)
	s.mu.Unlock()
}

func (s *HybridStore) appendPendingBatchLocked(entries []walEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if s.pendingFile == nil {
		err := io.ErrClosedPipe
		s.markPersistentErrorLocked(err)
		return fmt.Errorf("hybrid store: append pending log: %w", err)
	}
	for _, entry := range entries {
		b, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("hybrid store: encode pending log entry: %w", err)
		}
		b = append(b, '\n')
		if _, err := s.pendingFile.Write(b); err != nil {
			s.markPersistentErrorLocked(err)
			return fmt.Errorf("hybrid store: append pending log: %w", err)
		}
	}
	if s.syncMode == WALSyncAlways {
		if err := s.pendingFile.Sync(); err != nil {
			s.markPersistentErrorLocked(err)
			return fmt.Errorf("hybrid store: sync pending log: %w", err)
		}
	}
	return nil
}

func (s *HybridStore) compactPendingAfterFlush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewritePendingLocked()
}

// rewritePendingLocked replaces the on-disk pending log with exactly the
// current pendingOps, in order — the ops that arrived while the flush this
// compaction follows was in flight. Writing from pendingOps (rather than the
// dirty/tombstone read caches) is what keeps a replay after a crash
// mid-compaction correctly ordered: a DeletePrefix must stay before any Set
// under that prefix that was accepted after it (see resolvePendingLocked /
// hybridFlushDeletePrefix for the read-side and flush-side halves of the same
// invariant).
func (s *HybridStore) rewritePendingLocked() error {
	tmpPath := s.pendingPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("hybrid store: create pending log tmp: %w", err)
	}
	enc := json.NewEncoder(tmp)
	for _, op := range s.pendingOps {
		wal := walEntry{Op: op.op, Namespace: op.namespace, Key: op.key}
		if op.op == walSet {
			wal.Value = op.value
		}
		if err := enc.Encode(wal); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("hybrid store: encode pending log tmp: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("hybrid store: sync pending log tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("hybrid store: close pending log tmp: %w", err)
	}
	if s.pendingFile != nil {
		if err := s.pendingFile.Close(); err != nil {
			return fmt.Errorf("hybrid store: close pending log: %w", err)
		}
		s.pendingFile = nil
	}
	if err := os.Rename(tmpPath, s.pendingPath); err != nil {
		file, openErr := os.OpenFile(s.pendingPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
		if openErr == nil {
			s.pendingFile = file
		}
		return fmt.Errorf("hybrid store: replace pending log: %w", err)
	}
	file, err := os.OpenFile(s.pendingPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("hybrid store: reopen pending log: %w", err)
	}
	s.pendingFile = file
	return nil
}

func (s *HybridStore) closePendingFile() error {
	s.mu.Lock()
	f := s.pendingFile
	s.pendingFile = nil
	s.mu.Unlock()
	if f == nil {
		return nil
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("hybrid store: sync pending log close: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("hybrid store: close pending log: %w", err)
	}
	return nil
}

func (s *HybridStore) markPersistentError(err error) {
	s.mu.Lock()
	s.markPersistentErrorLocked(err)
	s.mu.Unlock()
}

func (s *HybridStore) markPersistentErrorLocked(err error) {
	if err == nil {
		return
	}
	s.health.Healthy = false
	s.health.LastError = err.Error()
	s.health.LastErrorAt = time.Now().UTC()
}

func (s *HybridStore) markPersistentSuccess() {
	s.mu.Lock()
	s.health.Healthy = true
	s.health.LastError = ""
	s.health.LastErrorAt = time.Time{}
	s.health.LastSuccessAt = time.Now().UTC()
	s.mu.Unlock()
}

func (s *HybridStore) mergePendingKeys(namespace, prefix string, keys []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if resolved, ok := s.resolvePendingLocked(namespace, key); ok && resolved.deleted {
			continue
		}
		seen[key] = struct{}{}
	}
	s.forEachPendingCandidateKeyLocked(namespace, prefix, func(key string) {
		if resolved, ok := s.resolvePendingLocked(namespace, key); ok && !resolved.deleted {
			seen[key] = struct{}{}
		} else {
			delete(seen, key)
		}
	})
	merged := make([]string, 0, len(seen))
	for key := range seen {
		merged = append(merged, key)
	}
	sort.Strings(merged)
	return merged
}

func (s *HybridStore) mergePendingPairs(namespace, prefix string, pairs []KV) []KV {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		if resolved, ok := s.resolvePendingLocked(namespace, pair.Key); ok {
			if resolved.deleted {
				continue
			}
			seen[pair.Key] = resolved.value
			continue
		}
		seen[pair.Key] = pair.Value
	}
	s.forEachPendingCandidateKeyLocked(namespace, prefix, func(key string) {
		if resolved, ok := s.resolvePendingLocked(namespace, key); ok && !resolved.deleted {
			seen[key] = resolved.value
		} else {
			delete(seen, key)
		}
	})
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	merged := make([]KV, 0, len(keys))
	for _, key := range keys {
		merged = append(merged, KV{Key: key, Value: seen[key]})
	}
	return merged
}

// forEachPendingCandidateKeyLocked calls fn once for every key that has an
// explicit pending write (Set or Delete) in namespace under prefix — i.e.
// candidate keys that might not appear in a base List/Scan result because
// they were created (or last touched) after that base read ran. A prefix
// tombstone alone never introduces a candidate key; it only shadows existing
// ones, which resolvePendingLocked already accounts for when called against
// base keys in mergePendingKeys/mergePendingPairs. Callers must hold s.mu.
func (s *HybridStore) forEachPendingCandidateKeyLocked(namespace, prefix string, fn func(key string)) {
	seen := make(map[string]struct{})
	visit := func(m map[string]dirtyEntry) {
		for composite := range m {
			ns, key := splitStoreKey(composite)
			if ns != namespace || !strings.HasPrefix(key, prefix) {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			fn(key)
		}
	}
	visit(s.flushing)
	visit(s.dirty)
}

// Hybrid startup must never discover namespaces by scanning kv. On Windows
// bind-mounted data directories that can turn a tiny metadata seed into a
// multi-minute startup stall. Seed the explicit TierHot namespace list and keep
// every other namespace SQLite-backed with the dirty overlay.
var hybridSeedNamespaceList = hybridHotNamespaces()

var hybridSeedNamespaces = hybridNamespaceSet(hybridSeedNamespaceList)

var hybridLazyNamespaceList = []string{"*"}

const (
	hybridSQLiteReadRetryTimeout = 2 * time.Second
	hybridSQLiteWriteRetryMax    = 3
	hybridSQLiteRetryDelay       = 25 * time.Millisecond
	hybridPendingWALFileName     = "overcast.hybrid.pending.wal"
)

func shouldReadHybridNamespaceFromSQLite(namespace string) bool {
	_, seeded := hybridSeedNamespaces[namespace]
	return !seeded
}

func hybridHotNamespaces() []string {
	namespaces := make([]string, 0, len(namespaceTiers))
	for namespace, tier := range namespaceTiers {
		if tier == TierHot {
			namespaces = append(namespaces, namespace)
		}
	}
	sort.Strings(namespaces)
	return namespaces
}

func hybridNamespaceSet(namespaces []string) map[string]struct{} {
	set := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		set[namespace] = struct{}{}
	}
	return set
}

func (s *HybridStore) logDebug(msg string, fields ...zap.Field) {
	if s.log != nil {
		s.log.Debug(msg, fields...)
	}
}

func (s *HybridStore) logInfo(msg string, fields ...zap.Field) {
	if s.log != nil {
		s.log.Info(msg, fields...)
	}
}

func (s *HybridStore) logWarn(msg string, fields ...zap.Field) {
	if s.log != nil {
		s.log.Warn(msg, fields...)
	}
}
