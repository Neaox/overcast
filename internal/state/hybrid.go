//go:build !nosqlite

package state

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
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
// Durability trade-off: up to one flushInterval of writes may be lost if the
// process exits uncleanly (kill -9 / OOM). Close() always performs a final
// synchronous flush before returning, so clean shutdowns are fully durable.
type HybridStore struct {
	mem *MemoryStore

	// sqlite is nil until the background loader has opened the DB. Guarded
	// by loaded (closed once sqlite is non-nil or loadErr is set).
	sqlite  *SQLiteStore
	dataDir string

	mu            sync.Mutex
	dirty         map[string]dirtyEntry // "namespace\x00key" → entry (same format as storeKey)
	flushing      map[string]dirtyEntry // entries currently being written to SQLite
	flushInterval time.Duration
	log           *zap.Logger

	// sqliteReady is closed when SQLite is open and migrated. loaded is closed
	// when the background SQLite -> memory seed finishes.
	sqliteReady chan struct{}
	loaded      chan struct{}
	loadErr     error

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
	bgCtx, cancel := context.WithCancel(context.Background())
	hs := &HybridStore{
		mem:           NewMemoryStore(),
		dataDir:       dataDir,
		dirty:         make(map[string]dirtyEntry, 64),
		flushing:      make(map[string]dirtyEntry, 64),
		flushInterval: flushInterval,
		log:           logger,
		sqliteReady:   make(chan struct{}),
		loaded:        make(chan struct{}),
		cancel:        cancel,
	}

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
		s.loadErr = fmt.Errorf("hybrid store: open sqlite: %w", err)
		s.logWarn("hybrid seed failed", zap.Error(s.loadErr), zap.Duration("elapsed", time.Since(seedStart)))
		return
	}
	s.sqlite = sq
	// Hybrid streams a long-running seed query while foreground reads may fall
	// back to SQLite. Allow one seed reader plus one foreground connection.
	s.sqlite.db.SetMaxOpenConns(2)
	s.logDebug("hybrid sqlite opened", zap.Duration("elapsed", time.Since(openStart)))

	if err := s.sqlite.ensureReady(context.Background()); err != nil {
		s.loadErr = fmt.Errorf("hybrid store: sqlite ready: %w", err)
		s.logWarn("hybrid seed failed", zap.Error(s.loadErr), zap.Duration("elapsed", time.Since(seedStart)))
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
			s.loadErr = fmt.Errorf("hybrid store: query namespace %q: %w", namespace, err)
			s.logWarn("hybrid seed failed", zap.Error(s.loadErr), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
			return
		}
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				s.loadErr = fmt.Errorf("hybrid store: scan namespace %q: %w", namespace, err)
				s.logWarn("hybrid seed failed", zap.Error(s.loadErr), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
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
				s.loadErr = fmt.Errorf("hybrid store: seed memory: %w", err)
				s.logWarn("hybrid seed failed", zap.Error(s.loadErr), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
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
			s.loadErr = fmt.Errorf("hybrid store: scan namespace %q rows: %w", namespace, err)
			s.logWarn("hybrid seed failed", zap.Error(s.loadErr), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
			return
		}
		if err := rows.Close(); err != nil {
			s.loadErr = fmt.Errorf("hybrid store: close namespace %q rows: %w", namespace, err)
			s.logWarn("hybrid seed failed", zap.Error(s.loadErr), zap.Int("loaded", loaded), zap.Duration("elapsed", time.Since(seedStart)))
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
		return s.sqlite != nil && s.loadErr == nil
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
			return s.sqlite.Get(ctx, namespace, key)
		}
		return s.mem.Get(ctx, namespace, key)
	}
	if s.isLoaded() {
		if s.loadErr != nil {
			return "", false, s.loadErr
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
		return s.sqlite.Get(ctx, namespace, key)
	}
	return s.mem.Get(ctx, namespace, key)
}

// Set writes to memory immediately and marks the entry dirty for the next flush.
func (s *HybridStore) Set(ctx context.Context, namespace, key, value string) error {
	if err := s.mem.Set(ctx, namespace, key, value); err != nil {
		return err
	}
	s.mu.Lock()
	s.dirty[storeKey(namespace, key)] = dirtyEntry{value: value}
	s.mu.Unlock()
	return nil
}

// Delete removes from memory immediately and marks the entry as a tombstone.
func (s *HybridStore) Delete(ctx context.Context, namespace, key string) error {
	if err := s.mem.Delete(ctx, namespace, key); err != nil {
		return err
	}
	s.mu.Lock()
	s.dirty[storeKey(namespace, key)] = dirtyEntry{deleted: true}
	s.mu.Unlock()
	return nil
}

// DeletePrefix removes matching keys from memory immediately and records
// tombstones so the next flush removes them from SQLite.
func (s *HybridStore) DeletePrefix(ctx context.Context, namespace, prefix string) error {
	keys, err := s.List(ctx, namespace, prefix)
	if err != nil {
		return err
	}
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
	s.mu.Lock()
	for _, key := range keys {
		s.dirty[storeKey(namespace, key)] = dirtyEntry{deleted: true}
	}
	s.mu.Unlock()
	return nil
}

// List serves from memory after the initial seed completes. During seed it
// uses SQLite plus the dirty overlay to avoid first-request stalls.
func (s *HybridStore) List(ctx context.Context, namespace, prefix string) ([]string, error) {
	if shouldReadHybridNamespaceFromSQLite(namespace) {
		if s.sqliteReadyNow() {
			keys, err := s.sqlite.List(ctx, namespace, prefix)
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
		if s.loadErr != nil {
			return nil, s.loadErr
		}
		return s.mem.List(ctx, namespace, prefix)
	}
	if s.sqliteReadyNow() {
		keys, err := s.sqlite.List(ctx, namespace, prefix)
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

// Scan serves from memory after the initial seed completes. During seed it
// uses SQLite plus the dirty overlay to avoid first-request stalls.
func (s *HybridStore) Scan(ctx context.Context, namespace, prefix string) ([]KV, error) {
	if shouldReadHybridNamespaceFromSQLite(namespace) {
		if s.sqliteReadyNow() {
			pairs, err := s.sqlite.Scan(ctx, namespace, prefix)
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
		if s.loadErr != nil {
			return nil, s.loadErr
		}
		return s.mem.Scan(ctx, namespace, prefix)
	}
	if s.sqliteReadyNow() {
		pairs, err := s.sqlite.Scan(ctx, namespace, prefix)
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
		return s.loadErr
	case <-ctx.Done():
		return ctx.Err()
	}
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
		return s.loadErr
	}
	if err := s.flush(); err != nil {
		s.sqlite.Close()
		return fmt.Errorf("hybrid store: final flush: %w", err)
	}
	return s.sqlite.Close()
}

// run is the background goroutine that periodically flushes dirty entries.
func (s *HybridStore) run(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = s.flush() // background flush; errors are best-effort
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
// If the background SQLite open has not completed yet, flush is a no-op —
// the dirty map stays populated and will be flushed on the next tick (or
// by Close). This means a kill -9 during the first ~200 ms after startup
// can lose writes to SQLite, but those writes have always been best-effort
// under the hybrid backend's durability model.
func (s *HybridStore) flush() error {
	start := time.Now()
	select {
	case <-s.loaded:
		if s.sqlite == nil {
			return s.loadErr
		}
	default:
		return nil // SQLite not ready yet — defer flush
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

	ctx := context.Background()
	tx, err := s.sqlite.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("hybrid flush begin tx: %w", err)
	}
	for composite, entry := range toFlush {
		ns, key := splitStoreKey(composite)
		if entry.deleted {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM kv WHERE namespace = ? AND key = ?`, ns, key); err != nil {
				tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
				return fmt.Errorf("hybrid flush delete [%s/%s]: %w", ns, key, err)
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO kv (namespace, key, value) VALUES (?, ?, ?)`,
				ns, key, entry.value); err != nil {
				tx.Rollback() //nolint:errcheck // best-effort; already returning an error.
				return fmt.Errorf("hybrid flush set [%s/%s]: %w", ns, key, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("hybrid flush commit: %w", err)
	}
	committed = true
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
// multi-minute startup stall. Keep seeding opt-in and explicit; namespaces not
// listed here remain SQLite-backed and are read lazily with the dirty overlay.
var hybridSeedNamespaceList = []string{}

var hybridSeedNamespaces = map[string]struct{}{}

var hybridLazyNamespaceList = []string{"*"}

func shouldReadHybridNamespaceFromSQLite(namespace string) bool {
	_, seeded := hybridSeedNamespaces[namespace]
	return !seeded
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
