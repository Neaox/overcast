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
// Accepted writes are appended to a small pending log before returning, then
// batch-flushed to SQLite. This protects against process crashes before the
// next SQLite flush without forcing a full SQLite transaction on every service
// mutation.
type HybridStore struct {
	mem *MemoryStore

	// sqlite is nil until the background loader has opened the DB. Publication is
	// synchronized by sqliteReady.
	sqlite      *SQLiteStore
	dataDir     string
	pendingPath string
	pendingFile *os.File

	mu            sync.Mutex
	flushMu       sync.Mutex
	dirty         map[string]dirtyEntry // "namespace\x00key" → entry (same format as storeKey)
	flushing      map[string]dirtyEntry // entries currently being written to SQLite
	flushInterval time.Duration
	log           *zap.Logger

	// sqliteReady is closed when SQLite is open and migrated. loaded is closed
	// when the background SQLite -> memory seed finishes.
	sqliteReady chan struct{}
	loaded      chan struct{}
	loadErr     error // guarded by mu
	health      PersistentHealth

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// dirtyEntry holds a pending write (value set) or a tombstone (deleted).
type dirtyEntry struct {
	value   string
	deleted bool
}

// NewHybridStore creates a HybridStore backed by a SQLite file in dataDir.
// SQLite is opened and existing data loaded in a background goroutine; the
// constructor returns immediately. Reads fall back to SQLite until the cache
// is loaded. Writes are accepted right away and take precedence over loaded
// state.
func NewHybridStore(dataDir string, flushInterval time.Duration) (*HybridStore, error) {
	return NewHybridStoreWithLogger(dataDir, flushInterval, nil)
}

// NewHybridStoreWithLogger creates a HybridStore and emits structured timing
// diagnostics for startup seeding and flushes when logger is non-nil.
func NewHybridStoreWithLogger(dataDir string, flushInterval time.Duration, logger *zap.Logger) (*HybridStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("hybrid store: create data dir %q: %w", dataDir, err)
	}
	bgCtx, cancel := context.WithCancel(context.Background())
	pendingPath := filepath.Join(dataDir, hybridPendingWALFileName)
	hs := &HybridStore{
		mem:           NewMemoryStore(),
		dataDir:       dataDir,
		pendingPath:   pendingPath,
		dirty:         make(map[string]dirtyEntry, 64),
		flushing:      make(map[string]dirtyEntry, 64),
		flushInterval: flushInterval,
		log:           logger,
		sqliteReady:   make(chan struct{}),
		loaded:        make(chan struct{}),
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
	return hs, nil
}

// seedFromSQLite opens the SQLite DB and loads all rows into the in-memory
// layer. Keys that were written (dirty) before the seed completes are
// skipped so concurrent writes always win over persisted state.
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
	sq, err := NewSQLiteStore(s.dataDir)
	if err != nil {
		err = fmt.Errorf("hybrid store: open sqlite: %w", err)
		s.setLoadErr(err)
		s.logWarn("hybrid seed failed", zap.Error(err), zap.Duration("elapsed", time.Since(seedStart)))
		return
	}
	s.sqlite = sq
	// Keep hybrid SQLite access queued through one connection. SQLite allows one
	// writer at a time; a wider pool turns concurrent flushes and service-specific
	// table writes into SQLITE_BUSY under CDK-heavy deploys.
	s.sqlite.db.SetMaxOpenConns(1)
	s.logDebug("hybrid sqlite opened", zap.Duration("elapsed", time.Since(openStart)))

	if err := s.sqlite.ensureReady(context.Background()); err != nil {
		err = fmt.Errorf("hybrid store: sqlite ready: %w", err)
		s.setLoadErr(err)
		s.logWarn("hybrid seed failed", zap.Error(err), zap.Duration("elapsed", time.Since(seedStart)))
		return
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

	var loaded, skipped int
	var bytes int64
	for _, namespace := range seedNamespaces {
		rows, err := s.sqlite.db.QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ? ORDER BY key`, namespace)
		if err != nil {
			err = fmt.Errorf("hybrid store: query namespace %q: %w", namespace, err)
			s.setLoadErr(err)
			s.logWarn("hybrid seed failed", zap.Error(err), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
			return
		}
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				err = fmt.Errorf("hybrid store: scan namespace %q: %w", namespace, err)
				s.setLoadErr(err)
				s.logWarn("hybrid seed failed", zap.Error(err), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
				return
			}
			composite := storeKey(namespace, key)
			if _, overwritten := dirtySnapshot[composite]; overwritten {
				skipped++
				continue
			}
			s.mu.Lock()
			_, overwritten := s.dirty[composite]
			s.mu.Unlock()
			if overwritten {
				skipped++
				continue
			}
			if err := s.mem.Set(ctx, namespace, key, value); err != nil {
				rows.Close()
				err = fmt.Errorf("hybrid store: seed memory: %w", err)
				s.setLoadErr(err)
				s.logWarn("hybrid seed failed", zap.Error(err), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
				return
			}
			loaded++
			bytes += int64(len(namespace) + len(key) + len(value))
			if loaded%100000 == 0 {
				s.logInfo("hybrid seed progress",
					zap.Int("loaded", loaded),
					zap.Int64("bytes", bytes),
					zap.Duration("elapsed", time.Since(seedStart)))
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			err = fmt.Errorf("hybrid store: scan namespace %q rows: %w", namespace, err)
			s.setLoadErr(err)
			s.logWarn("hybrid seed failed", zap.Error(err), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
			return
		}
		if err := rows.Close(); err != nil {
			err = fmt.Errorf("hybrid store: close namespace %q rows: %w", namespace, err)
			s.setLoadErr(err)
			s.logWarn("hybrid seed failed", zap.Error(err), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
			return
		}
	}
	s.logInfo("hybrid seed complete",
		zap.Int("loaded", loaded),
		zap.Int("skipped_dirty", skipped),
		zap.Strings("seed_namespaces", seedNamespaces),
		zap.Strings("lazy_namespaces", hybridLazyNamespaceList),
		zap.Int64("bytes", bytes),
		zap.Duration("query_and_seed", time.Since(queryStart)),
		zap.Duration("elapsed", time.Since(seedStart)))
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
		return s.sqlite != nil
	default:
		return false
	}
}

// Get serves from memory after the initial seed completes. During seed it
// falls back to SQLite so persisted state stays visible without blocking on a
// full-table cache warmup.
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
		if err := s.getLoadErr(); err != nil {
			return "", false, err
		}
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
	composite := storeKey(namespace, key)
	entry := dirtyEntry{value: value}
	s.mu.Lock()
	if err := s.appendPendingLocked(walEntry{Op: walSet, Namespace: namespace, Key: key, Value: value}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.dirty[composite] = entry
	s.mu.Unlock()
	if err := s.mem.Set(ctx, namespace, key, value); err != nil {
		return err
	}
	return nil
}

// Delete publishes the tombstone before removing memory for the same reason as
// Set: the dirty overlay is what makes lazy reads linearizable with writes.
func (s *HybridStore) Delete(ctx context.Context, namespace, key string) error {
	composite := storeKey(namespace, key)
	entry := dirtyEntry{deleted: true}
	s.mu.Lock()
	if err := s.appendPendingLocked(walEntry{Op: walDelete, Namespace: namespace, Key: key}); err != nil {
		s.mu.Unlock()
		return err
	}
	s.dirty[composite] = entry
	s.mu.Unlock()
	if err := s.mem.Delete(ctx, namespace, key); err != nil {
		return err
	}
	return nil
}

// DeletePrefix publishes tombstones before removing matching memory keys so
// lazy SQLite-backed reads cannot resurrect deleted persisted rows.
func (s *HybridStore) DeletePrefix(ctx context.Context, namespace, prefix string) error {
	keys, err := s.List(ctx, namespace, prefix)
	if err != nil {
		return err
	}
	s.mu.Lock()
	entries := make([]walEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, walEntry{Op: walDelete, Namespace: namespace, Key: key})
	}
	if err := s.appendPendingBatchLocked(entries); err != nil {
		s.mu.Unlock()
		return err
	}
	for _, key := range keys {
		s.dirty[storeKey(namespace, key)] = dirtyEntry{deleted: true}
	}
	s.mu.Unlock()
	if deleter, ok := any(s.mem).(PrefixDeleter); ok {
		if err := deleter.DeletePrefix(ctx, namespace, prefix); err != nil {
			return err
		}
	} else {
		for _, key := range keys {
			if err := s.mem.Delete(ctx, namespace, key); err != nil {
				return err
			}
		}
	}
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
		if err := s.getLoadErr(); err != nil {
			return nil, err
		}
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
		if err := s.getLoadErr(); err != nil {
			return nil, err
		}
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

// WaitReady blocks until the background SQLite open and migration has
// completed, satisfying state.ReadyAwaiter. Returns nil when the store
// is ready for reads, or ctx.Err() if the context is cancelled first.
// Callers that need to guarantee persisted data is visible (e.g. startup
// reload routines) should call this before performing a full Scan.
func (s *HybridStore) WaitReady(ctx context.Context) error {
	select {
	case <-s.sqliteReady:
		return s.getLoadErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Flush synchronously persists all dirty writes accepted before this call.
func (s *HybridStore) Flush(ctx context.Context) error {
	select {
	case <-s.loaded:
		if err := s.getLoadErr(); err != nil {
			return err
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
	health.PendingWrites = len(s.dirty) + len(s.flushing)
	return health
}

// DB returns the underlying *sql.DB, satisfying SQLiteDBProvider. Blocks
// until the background SQLite open has completed. Returns nil if the open
// failed — callers that hit this path should have already seen the load
// error propagate via a Get/List/Scan call.
func (s *HybridStore) DB() *sql.DB {
	<-s.sqliteReady
	if s.sqlite == nil {
		return nil
	}
	return s.sqlite.DB()
}

// Close stops the background flush goroutine, performs a final synchronous
// flush of all pending dirty entries to SQLite, then closes the database.
func (s *HybridStore) Close() error {
	s.cancel()
	s.wg.Wait()
	// After wg.Wait, seedFromSQLite has finished — sqlite is either fully
	// open or nil (open failed). If nil, there's nothing to flush/close.
	if s.sqlite == nil {
		loadErr := s.getLoadErr()
		if closeErr := s.closePendingFile(); closeErr != nil && loadErr == nil {
			return closeErr
		}
		return loadErr
	}
	if err := s.flush(context.Background()); err != nil {
		_ = s.sqlite.Close()
		_ = s.closePendingFile()
		return fmt.Errorf("hybrid store: final flush: %w", err)
	}
	if err := s.sqlite.Close(); err != nil {
		_ = s.closePendingFile()
		return err
	}
	return s.closePendingFile()
}

// run is the background goroutine that periodically flushes dirty entries.
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
		case <-ctx.Done():
			return
		}
	}
}

// flush atomically steals the current dirty map and writes all pending entries
// to SQLite in a single transaction. Holding the mu lock only long enough to
// swap the map means new writes can accumulate immediately while the
// (potentially slow) SQLite writes proceed without blocking callers.
//
// If the background SQLite open has not completed yet, background flush is a
// no-op; dirty entries remain in memory and in the pending log for the next
// flush attempt or restart replay.
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
		if s.sqlite == nil {
			return s.getLoadErr()
		}
	default:
		return nil // SQLite not ready yet — defer flush
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if len(s.dirty) == 0 {
		s.mu.Unlock()
		return nil
	}
	toFlush := s.dirty
	s.dirty = make(map[string]dirtyEntry, 64)
	s.flushing = toFlush
	s.mu.Unlock()
	committed := false
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for composite, entry := range toFlush {
			delete(s.flushing, composite)
			if committed {
				continue
			}
			if _, overwritten := s.dirty[composite]; !overwritten {
				s.dirty[composite] = entry
			}
		}
	}()

	tx, err := s.sqlite.db.BeginTx(ctx, nil)
	if err != nil {
		s.markPersistentError(err)
		return fmt.Errorf("hybrid flush begin tx: %w", err)
	}
	for composite, entry := range toFlush {
		ns, key := splitStoreKey(composite)
		if entry.deleted {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM kv WHERE namespace = ? AND key = ?`, ns, key); err != nil {
				tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
				s.markPersistentError(err)
				return fmt.Errorf("hybrid flush delete [%s/%s]: %w", ns, key, err)
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO kv (namespace, key, value) VALUES (?, ?, ?)`,
				ns, key, entry.value); err != nil {
				tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
				s.markPersistentError(err)
				return fmt.Errorf("hybrid flush set [%s/%s]: %w", ns, key, err)
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
		s.logWarn("hybrid flush slow", zap.Int("entries", len(toFlush)), zap.Duration("elapsed", elapsed))
	} else {
		s.logDebug("hybrid flush complete", zap.Int("entries", len(toFlush)), zap.Duration("elapsed", elapsed))
	}
	return nil
}

func (s *HybridStore) pendingValue(namespace, key string) (dirtyEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	composite := storeKey(namespace, key)
	entry, ok := s.dirty[composite]
	if ok {
		return entry, true
	}
	entry, ok = s.flushing[composite]
	return entry, ok
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
	value, found, err := s.sqlite.Get(ctx, namespace, key)
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
	keys, err := s.sqlite.List(ctx, namespace, prefix)
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
	namespaces, err := s.sqlite.ListNamespaces(ctx)
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
	pairs, err := s.sqlite.Scan(ctx, namespace, prefix)
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

func (s *HybridStore) retrySQLiteGet(ctx context.Context, namespace, key string) (string, bool, error) {
	var lastErr error
	for {
		value, found, err := s.sqlite.Get(ctx, namespace, key)
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
		keys, err := s.sqlite.List(ctx, namespace, prefix)
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
		namespaces, err := s.sqlite.ListNamespaces(ctx)
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
		pairs, err := s.sqlite.Scan(ctx, namespace, prefix)
		if !shouldRetryHybridSQLiteRead(err) {
			return pairs, err
		}
		lastErr = err
		if err := sleepHybridSQLiteRetry(ctx); err != nil {
			return nil, fallbackRetryErr(lastErr, err)
		}
	}
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
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry walEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			if i == len(lines)-1 && len(data) > 0 && data[len(data)-1] != '\n' {
				s.logWarn("hybrid pending log has incomplete final entry; ignoring", zap.Error(err))
				break
			}
			return fmt.Errorf("hybrid store: decode pending log: %w", err)
		}
		s.applyPendingEntry(ctx, entry)
	}
	return nil
}

func (s *HybridStore) applyPendingEntry(ctx context.Context, entry walEntry) {
	switch entry.Op {
	case walSet:
		_ = s.mem.Set(ctx, entry.Namespace, entry.Key, entry.Value)
		s.dirty[storeKey(entry.Namespace, entry.Key)] = dirtyEntry{value: entry.Value}
	case walDelete:
		_ = s.mem.Delete(ctx, entry.Namespace, entry.Key)
		s.dirty[storeKey(entry.Namespace, entry.Key)] = dirtyEntry{deleted: true}
	case walDeletePrefix:
		keys, _ := s.mem.List(ctx, entry.Namespace, entry.Key)
		for _, key := range keys {
			_ = s.mem.Delete(ctx, entry.Namespace, key)
			s.dirty[storeKey(entry.Namespace, key)] = dirtyEntry{deleted: true}
		}
	}
}

func (s *HybridStore) appendPendingLocked(entry walEntry) error {
	return s.appendPendingBatchLocked([]walEntry{entry})
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
	return nil
}

func (s *HybridStore) compactPendingAfterFlush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewritePendingLocked()
}

func (s *HybridStore) rewritePendingLocked() error {
	tmpPath := s.pendingPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("hybrid store: create pending log tmp: %w", err)
	}
	enc := json.NewEncoder(tmp)
	for composite, entry := range s.dirty {
		ns, key := splitStoreKey(composite)
		wal := walEntry{Namespace: ns, Key: key}
		if entry.deleted {
			wal.Op = walDelete
		} else {
			wal.Op = walSet
			wal.Value = entry.value
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
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		seen[key] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergePendingEntriesLocked(namespace, prefix, func(key string, entry dirtyEntry) {
		if entry.deleted {
			delete(seen, key)
			return
		}
		seen[key] = struct{}{}
	})
	merged := make([]string, 0, len(seen))
	for key := range seen {
		merged = append(merged, key)
	}
	sort.Strings(merged)
	return merged
}

func (s *HybridStore) mergePendingEntriesLocked(namespace, prefix string, merge func(string, dirtyEntry)) {
	for composite, entry := range s.flushing {
		ns, key := splitStoreKey(composite)
		if ns != namespace || !strings.HasPrefix(key, prefix) {
			continue
		}
		merge(key, entry)
	}
	for composite, entry := range s.dirty {
		ns, key := splitStoreKey(composite)
		if ns != namespace || !strings.HasPrefix(key, prefix) {
			continue
		}
		merge(key, entry)
	}
}

func (s *HybridStore) mergePendingPairs(namespace, prefix string, pairs []KV) []KV {
	seen := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		seen[pair.Key] = pair.Value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergePendingEntriesLocked(namespace, prefix, func(key string, entry dirtyEntry) {
		if entry.deleted {
			delete(seen, key)
			return
		}
		seen[key] = entry.value
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
