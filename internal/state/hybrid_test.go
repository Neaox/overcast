//go:build !nosqlite

package state_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	_ "modernc.org/sqlite"
)

// ---- HybridStore contract tests --------------------------------------------
//
// These mirror the MemoryStore and SQLiteStore tests to verify the hybrid
// implementation satisfies the same Store contract.

func newHybridStore(t *testing.T) *state.HybridStore {
	t.Helper()
	s, err := state.NewHybridStore(t.TempDir(), 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestHybridStore_GetSetDelete(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	// Get missing key returns not-found.
	_, found, err := s.Get(ctx, "ns", "key")
	if err != nil || found {
		t.Fatalf("Get missing: err=%v found=%v", err, found)
	}

	// Set and retrieve.
	if err := s.Set(ctx, "ns", "key", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := s.Get(ctx, "ns", "key")
	if err != nil || !found || got != "value" {
		t.Fatalf("Get after Set: err=%v found=%v got=%q", err, found, got)
	}

	// Delete.
	if err := s.Delete(ctx, "ns", "key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, found, _ = s.Get(ctx, "ns", "key")
	if found {
		t.Fatal("key should not exist after delete")
	}
}

func TestHybridStore_Delete_nonExistent(t *testing.T) {
	s := newHybridStore(t)
	if err := s.Delete(context.Background(), "ns", "does-not-exist"); err != nil {
		t.Fatalf("Delete non-existent key returned error: %v", err)
	}
}

func TestHybridStore_Set_overwrite(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ns", "key", "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set(ctx, "ns", "key", "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, _, _ := s.Get(ctx, "ns", "key")
	if got != "v2" {
		t.Errorf("expected overwritten value v2, got %q", got)
	}
}

func TestHybridStore_List_prefix(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ns", "a/1", "v")
	_ = s.Set(ctx, "ns", "a/2", "v")
	_ = s.Set(ctx, "ns", "b/1", "v")

	keys, err := s.List(ctx, "ns", "a/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys with prefix 'a/', got %d: %v", len(keys), keys)
	}
}

func TestHybridStore_Scan(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ns", "x/1", "hello")
	_ = s.Set(ctx, "ns", "x/2", "world")
	_ = s.Set(ctx, "ns", "y/1", "other")

	pairs, err := s.Scan(ctx, "ns", "x/")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(pairs) != 2 {
		t.Errorf("expected 2 pairs, got %d", len(pairs))
	}
}

func TestHybridStore_LazyReadCanceledContextRetriesWithBackgroundContext(t *testing.T) {
	// Given: a hybrid store with both persisted-only rows and pending in-memory writes.
	dir := t.TempDir()
	seedHybridSQLiteNamespace(t, dir, "svc", 1, "persisted")
	s, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.Set(ctx, "svc", "queue/pending", "pending"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// When: lazy reads are called with a canceled request context.
	value, found, getErr := s.Get(canceled, "svc", "queue/00000000")
	keys, listErr := s.List(canceled, "svc", "queue/")
	pairs, scanErr := s.Scan(canceled, "svc", "queue/")

	// Then: transient request cancellation does not hide persisted-only rows or
	// lose pending writes from the overlay.
	if getErr != nil || !found || value != "persisted" {
		t.Fatalf("Get persisted with canceled context: err=%v found=%v value=%q", getErr, found, value)
	}
	if listErr != nil {
		t.Fatalf("List with canceled context: %v", listErr)
	}
	if len(keys) != 2 || keys[0] != "queue/00000000" || keys[1] != "queue/pending" {
		t.Fatalf("List keys = %#v, want persisted and pending rows", keys)
	}
	if scanErr != nil {
		t.Fatalf("Scan with canceled context: %v", scanErr)
	}
	if len(pairs) != 2 || pairs[0].Key != "queue/00000000" || pairs[0].Value != "persisted" || pairs[1].Key != "queue/pending" || pairs[1].Value != "pending" {
		t.Fatalf("Scan pairs = %#v, want persisted and pending rows", pairs)
	}
}

func TestHybridStore_ReplaysPendingWritesAfterUncleanExit(t *testing.T) {
	// Given: a hybrid store that has accepted writes but has not reached its
	// periodic SQLite flush interval.
	dir := t.TempDir()
	ctx := context.Background()
	s1, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore (1): %v", err)
	}
	t.Cleanup(func() { s1.Close() })
	if err := s1.Set(ctx, "svc", "key", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s1.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady (1): %v", err)
	}

	// When: a new store starts against the same directory before the first store
	// has a chance to flush to SQLite, simulating process death before flush.
	s2, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore (2): %v", err)
	}
	defer s2.Close()

	// Then: the pending log replays the acknowledged write.
	got, found, err := s2.Get(ctx, "svc", "key")
	if err != nil || !found || got != "value" {
		t.Fatalf("Get replayed pending write: err=%v found=%v got=%q", err, found, got)
	}
}

func TestHybridStore_PersistentHealthTracksPendingAndFlush(t *testing.T) {
	// Given: a hybrid store with an accepted but unflushed write.
	s := newHybridStore(t)
	ctx := context.Background()
	if err := s.Set(ctx, "svc", "key", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// When: health is read before and after an explicit flush.
	before := s.PersistentHealth()
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	after := s.PersistentHealth()

	// Then: the backend exposes pending write pressure and successful persistence.
	if !before.Healthy || before.PendingWrites != 1 {
		t.Fatalf("health before flush = %#v, want healthy with one pending write", before)
	}
	if !after.Healthy || after.PendingWrites != 0 || after.LastSuccessAt.IsZero() {
		t.Fatalf("health after flush = %#v, want healthy with no pending writes and success timestamp", after)
	}
}

func TestHybridStore_ConcurrentFlushesSerializeSQLiteWrites(t *testing.T) {
	// Given: a hybrid store with SQLite ready and many concurrent callers forcing
	// synchronous persistence, as CloudFormation terminal-state updates do.
	s := newHybridStore(t)
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	const workers = 32
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%02d", i)
			if err := s.Set(ctx, "svc", key, "value"); err != nil {
				errCh <- fmt.Errorf("set %s: %w", key, err)
				return
			}
			if err := s.Flush(ctx); err != nil {
				errCh <- fmt.Errorf("flush %s: %w", key, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)

	// Then: overlapping flush attempts are serialized and every acknowledged
	// write remains readable and persistable.
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("final Flush: %v", err)
	}
	for i := 0; i < workers; i++ {
		key := fmt.Sprintf("key-%02d", i)
		got, found, err := s.Get(ctx, "svc", key)
		if err != nil || !found || got != "value" {
			t.Fatalf("Get %s: err=%v found=%v got=%q", key, err, found, got)
		}
	}
}

// TestHybridStore_Persistence verifies that data written before Close is
// available in a new HybridStore opened against the same directory.
func TestHybridStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Write some data and close (triggers final flush).
	s1, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (1): %v", err)
	}
	if err := s1.Set(ctx, "svc", "key", "persisted"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close (1): %v", err)
	}

	// Re-open and verify the value was loaded from SQLite.
	s2, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (2): %v", err)
	}
	defer s2.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, found, err := s2.Get(ctx, "svc", "key")
		if err != nil {
			t.Fatalf("Get after reopen: %v", err)
		}
		if found && got == "persisted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Get after reopen: found=%v got=%q", found, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHybridStore_DeletePersistence verifies that a delete is flushed and the
// key is absent in a new store opened against the same directory.
func TestHybridStore_DeletePersistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (1): %v", err)
	}
	_ = s1.Set(ctx, "svc", "key", "to-delete")
	if err := s1.Close(); err != nil {
		t.Fatalf("Close (1): %v", err)
	}

	s2, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (2): %v", err)
	}
	_ = s2.Delete(ctx, "svc", "key")
	if err := s2.Close(); err != nil {
		t.Fatalf("Close (2): %v", err)
	}

	s3, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (3): %v", err)
	}
	defer s3.Close()

	_, found, err := s3.Get(ctx, "svc", "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Error("key should be absent after delete + reopen")
	}
}

func TestHybridStore_RestoreLargeState(t *testing.T) {
	if testing.Short() {
		t.Skip("large restore regression test")
	}

	// Given: a value-heavy persisted hybrid store. Prefix reads only need keys,
	// so they should not wait for the full value payload to hydrate memory.
	dir := t.TempDir()
	ctx := context.Background()
	const rows = 10000
	value := strings.Repeat("x", 4096)
	seedHybridSQLiteNamespace(t, dir, "svc:metadata", rows, value)

	// When: the store is reopened and the first state-backed read runs while
	// the cache seed is still in progress.
	core, logs := observer.New(zap.DebugLevel)
	s2, err := state.NewHybridStoreWithLogger(dir, time.Hour, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStore (2): %v", err)
	}
	defer s2.Close()

	_, err = s2.List(ctx, "svc:metadata", "queue/")
	if err != nil {
		t.Fatalf("List after restore: %v", err)
	}

	// Then: the read must not block on cache hydration. A no-op seed may complete
	// first, but it must not scan or block on the persisted payload rows. Avoid a
	// hard wall-clock threshold here: -race/-cover overhead in CI can make an
	// indexed SQLite key query over 10k rows exceed an arbitrary sub-second bound.
	if logs.FilterMessage("hybrid wait blocked").Len() != 0 {
		t.Fatalf("first read blocked on seed, got logs: %#v", logs.All())
	}
	if logs.FilterMessage("hybrid seed complete").Len() > 0 {
		loaded := observedIntField(t, logs, "hybrid seed complete", "loaded")
		if loaded != 0 {
			t.Fatalf("first read waited for seeded rows = %d, got logs: %#v", loaded, logs.All())
		}
	}
	waitForObservedLog(t, logs, "hybrid seed complete")
	keys, err := s2.List(ctx, "svc:metadata", "queue/")
	if err != nil {
		t.Fatalf("List after sqlite ready: %v", err)
	}
	if len(keys) != rows {
		t.Fatalf("restored rows after sqlite ready = %d, want %d", len(keys), rows)
	}
}

func TestHybridStore_RestoreSeedsHotNamespacesOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("large restore regression test")
	}

	// Given: mostly bulk SQS messages plus small hot control-plane namespaces
	// used by background engines.
	dir := t.TempDir()
	ctx := context.Background()
	seedHybridSQLiteNamespace(t, dir, "sqs:queues", 2, "queue-metadata")
	seedHybridSQLiteNamespace(t, dir, "scheduler:schedules", 1, "schedule-metadata")
	seedHybridSQLiteNamespace(t, dir, "eb:rules", 1, "rule-metadata")
	seedHybridSQLiteNamespace(t, dir, "sqs:messages", 5000, strings.Repeat("x", 4096))

	// When: hybrid restore completes.
	core, logs := observer.New(zap.DebugLevel)
	s, err := state.NewHybridStoreWithLogger(dir, time.Hour, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	waitForObservedLog(t, logs, "hybrid seed complete")

	// Then: startup hydrates only hot metadata rows. Bulk messages remain readable
	// through the SQLite-backed lazy path without forcing full data-plane restore.
	loaded := observedIntField(t, logs, "hybrid seed complete", "loaded")
	if loaded != 4 {
		t.Fatalf("seeded rows = %d, want hot metadata only", loaded)
	}
	metadataKeys, err := s.List(ctx, "sqs:queues", "queue/")
	if err != nil {
		t.Fatalf("List metadata after seed: %v", err)
	}
	if len(metadataKeys) != 2 {
		t.Fatalf("metadata rows = %d, want 2", len(metadataKeys))
	}
	schedulerKeys, err := s.List(ctx, "scheduler:schedules", "queue/")
	if err != nil {
		t.Fatalf("List scheduler metadata after seed: %v", err)
	}
	if len(schedulerKeys) != 1 {
		t.Fatalf("scheduler rows = %d, want 1", len(schedulerKeys))
	}
	ruleKeys, err := s.List(ctx, "eb:rules", "queue/")
	if err != nil {
		t.Fatalf("List eventbridge metadata after seed: %v", err)
	}
	if len(ruleKeys) != 1 {
		t.Fatalf("eventbridge rule rows = %d, want 1", len(ruleKeys))
	}
	keys, err := s.List(ctx, "sqs:messages", "queue/")
	if err != nil {
		t.Fatalf("List bulk messages after seed: %v", err)
	}
	if len(keys) != 5000 {
		t.Fatalf("bulk message rows = %d, want 5000", len(keys))
	}
}

func seedHybridSQLiteNamespace(t *testing.T, dir string, namespace string, rows int, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "overcast.db")+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open seed sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (
		namespace TEXT NOT NULL,
		key       TEXT NOT NULL,
		value     TEXT NOT NULL,
		PRIMARY KEY (namespace, key)
	)`); err != nil {
		t.Fatalf("create kv: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO kv (namespace, key, value) VALUES (?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare seed: %v", err)
	}
	defer stmt.Close()
	for i := 0; i < rows; i++ {
		if _, err := stmt.Exec(namespace, fmt.Sprintf("queue/%08d", i), value); err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}
}

// waitForObservedLog polls for message for up to 30s. The bound is generous
// (rather than sub-second) because two callers seed tens of megabytes into a
// legacy (pre-migration-runner) overcast.db before opening the store: the
// first HybridStore open against that fixture now also runs the
// PRAGMA-user_version migration runner's one-time pre-migration backup
// (file copy) and the auto_vacuum migration's VACUUM (internal/state/migrate.go),
// both of which are proportional to database size and can legitimately take
// several seconds on a slow disk/bind mount — see storage-plan.md Phase 2
// item 2.4 ("acceptable as a one-time migration"). A real hang is still
// caught well within this bound in practice, and well within `go test`'s own
// default per-test timeout.
func waitForObservedLog(t *testing.T, logs *observer.ObservedLogs, message string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if logs.FilterMessage(message).Len() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log %q, got logs: %#v", message, logs.All())
}

func observedIntField(t *testing.T, logs *observer.ObservedLogs, message, field string) int {
	t.Helper()
	entries := logs.FilterMessage(message).All()
	if len(entries) == 0 {
		t.Fatalf("missing log %q", message)
	}
	for _, f := range entries[0].Context {
		if f.Key == field {
			return int(f.Integer)
		}
	}
	t.Fatalf("missing field %q in log %q: %#v", field, message, entries[0].Context)
	return 0
}

// ---- 1.2 read-only connection pool ----------------------------------------

// TestHybridStore_ReadsDontBlockOnConcurrentFlush is a -race-safe concurrency
// test that hammers a TierCached namespace with concurrent writes and reads
// while the background flush loop runs on a short interval. Before 1.2, all
// SQLite access (including reads) shared one connection with the flush
// writer, so this scenario would serialize reads behind flush transactions;
// it must complete promptly, without deadlock, and without any error.
func TestHybridStore_ReadsDontBlockOnConcurrentFlush(t *testing.T) {
	s, err := state.NewHybridStore(t.TempDir(), 5*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	const ns = "sqs:messages" // TierCached — routed through the SQLite read path.
	const writers = 8
	const readers = 8

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, writers+readers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("queue/%d-%d", i, j)
				if err := s.Set(ctx, ns, key, "v"); err != nil {
					select {
					case errCh <- fmt.Errorf("set: %w", err):
					default:
					}
					return
				}
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := s.List(ctx, ns, ""); err != nil {
					select {
					case errCh <- fmt.Errorf("list: %w", err):
					default:
					}
					return
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		time.Sleep(300 * time.Millisecond)
		close(stop)
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out — reads likely blocked on an in-flight flush")
	}
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

// ---- 1.3 pending-log fsync modes --------------------------------------

func TestNewHybridStoreWithOptions_InvalidSyncMode(t *testing.T) {
	_, err := state.NewHybridStoreWithOptions(t.TempDir(), state.HybridOptions{SyncMode: "bogus"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid sync mode")
	}
}

func TestHybridStore_SyncModesCloseCleanlyAndPersist(t *testing.T) {
	for _, mode := range []state.WALSyncMode{state.WALSyncAlways, state.WALSyncInterval, state.WALSyncNever} {
		t.Run(string(mode), func(t *testing.T) {
			dir := t.TempDir()
			s, err := state.NewHybridStoreWithOptions(dir, state.HybridOptions{
				FlushInterval: time.Hour,
				SyncMode:      mode,
				SyncInterval:  10 * time.Millisecond,
			}, nil)
			if err != nil {
				t.Fatalf("NewHybridStoreWithOptions: %v", err)
			}
			ctx := context.Background()
			if err := s.Set(ctx, "svc", "key", "value"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			s2, err := state.NewHybridStore(dir, time.Hour)
			if err != nil {
				t.Fatalf("NewHybridStore (reopen): %v", err)
			}
			defer s2.Close()
			if err := s2.WaitReady(ctx); err != nil {
				t.Fatalf("WaitReady (reopen): %v", err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				got, found, err := s2.Get(ctx, "svc", "key")
				if err != nil {
					t.Fatalf("Get after reopen: %v", err)
				}
				if found && got == "value" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("Get after reopen: found=%v got=%q", found, got)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// ---- 1.4 size-triggered flush ------------------------------------------

// TestHybridStore_DirtyEntryThresholdTriggersEarlyFlush writes past the
// configured entry threshold with a long timer-driven flush interval and
// asserts an out-of-band flush happens promptly anyway.
func TestHybridStore_DirtyEntryThresholdTriggersEarlyFlush(t *testing.T) {
	dir := t.TempDir()
	s, err := state.NewHybridStoreWithOptions(dir, state.HybridOptions{
		FlushInterval:       time.Hour, // never fires during the test
		DirtyEntryThreshold: 5,
		DirtyByteThreshold:  1 << 30, // effectively disabled
	}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	for i := 0; i < 6; i++ {
		if err := s.Set(ctx, "svc", fmt.Sprintf("key-%d", i), "v"); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		h := s.PersistentHealth()
		if h.PendingWrites == 0 {
			break
		}
		if time.Now().After(deadline) {
			// Dump the full health snapshot: it distinguishes a flush that
			// never started (healthy, no error) from one stuck or failing
			// (LastError set / Healthy false) when this timing-sensitive
			// test trips under load.
			t.Fatalf("dirty entries not flushed promptly after crossing threshold: pending=%d healthy=%v mode=%q lastErr=%q",
				h.PendingWrites, h.Healthy, h.Mode, h.LastError)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ---- 1.6 corrupt pending-log lines: skip-and-warn -----------------------

// TestHybridStore_CorruptMidFilePendingLine_SkipsAndContinues writes a
// pending log with a corrupt line in the middle (not the final line) and
// asserts the store still starts and the surrounding valid entries survive.
func TestHybridStore_CorruptMidFilePendingLine_SkipsAndContinues(t *testing.T) {
	dir := t.TempDir()
	pendingPath := filepath.Join(dir, "overcast.hybrid.pending.wal")
	lines := []string{
		`{"op":"set","namespace":"svc","key":"before","value":"1"}`,
		`{not valid json at all`,
		`{"op":"set","namespace":"svc","key":"after","value":"2"}`,
	}
	if err := os.WriteFile(pendingPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write pending log fixture: %v", err)
	}

	core, logs := observer.New(zap.WarnLevel)
	s, err := state.NewHybridStoreWithLogger(dir, time.Hour, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStoreWithLogger: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	got, found, err := s.Get(ctx, "svc", "before")
	if err != nil || !found || got != "1" {
		t.Fatalf("Get before: err=%v found=%v got=%q", err, found, got)
	}
	got, found, err = s.Get(ctx, "svc", "after")
	if err != nil || !found || got != "2" {
		t.Fatalf("Get after: err=%v found=%v got=%q", err, found, got)
	}
	if logs.FilterMessage("hybrid pending log entry undecodable; skipping").Len() == 0 {
		t.Fatalf("expected a warning for the corrupt mid-file line, got logs: %#v", logs.All())
	}
	if logs.FilterMessage("hybrid pending log replay skipped corrupt entries").Len() == 0 {
		t.Fatalf("expected a replay summary warning, got logs: %#v", logs.All())
	}
}

// ---- 1.8 ranged tombstones for DeletePrefix -----------------------------

// TestHybridStore_DeletePrefix_LaterSetSurvives is the ordering test called
// out by the storage plan: Set(k1) under prefix p, DeletePrefix(p), then
// Set(k2) under the same prefix. k1 must read as deleted and k2 must survive
// both immediately (overlay) and after a flush + store reload (SQLite).
func TestHybridStore_DeletePrefix_LaterSetSurvives(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	if err := s.Set(ctx, "svc", "p/k1", "v1"); err != nil {
		t.Fatalf("Set k1: %v", err)
	}
	if err := s.DeletePrefix(ctx, "svc", "p/"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if err := s.Set(ctx, "svc", "p/k2", "v2"); err != nil {
		t.Fatalf("Set k2: %v", err)
	}

	// Overlay (pre-flush) view.
	assertDeletePrefixOrdering(t, s)

	// Flush to SQLite and confirm the ranged tombstone landed correctly.
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	assertDeletePrefixOrdering(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reload from SQLite and confirm the flushed state survived.
	s2, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore (reopen): %v", err)
	}
	defer s2.Close()
	if err := s2.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady (reopen): %v", err)
	}
	assertDeletePrefixOrdering(t, s2)
}

func assertDeletePrefixOrdering(t *testing.T, s *state.HybridStore) {
	t.Helper()
	ctx := context.Background()
	_, found, err := s.Get(ctx, "svc", "p/k1")
	if err != nil {
		t.Fatalf("Get k1: %v", err)
	}
	if found {
		t.Error("k1 should be deleted by DeletePrefix")
	}
	got, found, err := s.Get(ctx, "svc", "p/k2")
	if err != nil || !found || got != "v2" {
		t.Fatalf("Get k2: err=%v found=%v got=%q, want found=true got=\"v2\"", err, found, got)
	}
	keys, err := s.List(ctx, "svc", "p/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "p/k2" {
		t.Fatalf("List p/ = %#v, want only [p/k2]", keys)
	}
}

// TestHybridStore_DeletePrefix_PurgesManyKeysInOneFlushRoundTrip exercises a
// PurgeQueue-shaped workload: many keys under one prefix, deleted with a
// single DeletePrefix call. This must not enumerate keys on the write path
// (see TestHybridStore_DeletePrefixDoesNotEnumerateKeys in the internal test
// file for the structural assertion) and must correctly purge everything
// after a flush + reload.
func TestHybridStore_DeletePrefix_PurgesManyKeysInOneFlushRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	const n = 500
	for i := 0; i < n; i++ {
		if err := s.Set(ctx, "svc", fmt.Sprintf("queue/%04d", i), "v"); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	if err := s.Set(ctx, "svc", "other/keep", "keep-me"); err != nil {
		t.Fatalf("Set unrelated key: %v", err)
	}
	if err := s.DeletePrefix(ctx, "svc", "queue/"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore (reopen): %v", err)
	}
	defer s2.Close()
	if err := s2.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady (reopen): %v", err)
	}
	keys, err := s2.List(ctx, "svc", "queue/")
	if err != nil {
		t.Fatalf("List queue/: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("List queue/ after purge+reload = %d keys, want 0", len(keys))
	}
	_, found, err := s2.Get(ctx, "svc", "other/keep")
	if err != nil || !found {
		t.Fatalf("Get other/keep: err=%v found=%v, want found=true", err, found)
	}
}

// ---- 1.11 corrupt-database startup policy: degrade, don't poison -------

// TestHybridStore_DegradesToMemoryOnlyWhenDatabaseFileIsCorrupt points a
// store at a directory whose overcast.db file is not a valid SQLite
// database. Reads and writes must keep working via the memory fallback and
// PersistentHealth must report unhealthy, instead of every subsequent
// request failing (the pre-1.11 behavior).
func TestHybridStore_DegradesToMemoryOnlyWhenDatabaseFileIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overcast.db"), []byte("this is not a sqlite database file"), 0o644); err != nil {
		t.Fatalf("write corrupt db fixture: %v", err)
	}

	core, logs := observer.New(zap.WarnLevel)
	s, err := state.NewHybridStoreWithLogger(dir, time.Hour, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStoreWithLogger: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.WaitReady(ctx); err == nil {
		t.Fatal("expected WaitReady to surface the corrupt-database error")
	}

	// Reads and writes must still work via the memory fallback.
	if err := s.Set(ctx, "svc", "key", "value"); err != nil {
		t.Fatalf("Set on degraded store: %v", err)
	}
	got, found, err := s.Get(ctx, "svc", "key")
	if err != nil || !found || got != "value" {
		t.Fatalf("Get on degraded store: err=%v found=%v got=%q", err, found, got)
	}
	if _, err := s.List(ctx, "svc", ""); err != nil {
		t.Fatalf("List on degraded store: %v", err)
	}

	health := s.PersistentHealth()
	if health.Healthy {
		t.Fatal("expected PersistentHealth.Healthy = false for a corrupt database")
	}
	if health.LastError == "" {
		t.Fatal("expected PersistentHealth.LastError to be populated")
	}

	// An explicit Flush must be a no-op, not an error, while degraded.
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush on degraded store should no-op, got: %v", err)
	}

	waitForObservedLog(t, logs, "hybrid store degraded to memory-only for this run; persisted state is unavailable until restart")
}

// ---- 3.2 ScanPage --------------------------------------------------------

// TestHybridStore_ScanPage_TierHotAfterSeed exercises ScanPage's simplest
// branch: a TierHot namespace (sqs:queues is TierHot per tier.go) once the
// background seed has finished, which delegates straight to
// MemoryStore.ScanPage with no overlay merge — see HybridStore.ScanPage's
// doc comment.
func TestHybridStore_ScanPage_TierHotAfterSeed(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	s, err := state.NewHybridStoreWithLogger(t.TempDir(), 10*time.Second, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStoreWithLogger: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForObservedLog(t, logs, "hybrid seed complete")
	assertScanPagePaginatesFullRange(t, s, "sqs:queues", "queue/", 29, 5)
}

// TestHybridStore_ScanPage_LazyNamespaceMergesOverlayAndBase exercises
// ScanPage against a namespace HybridStore treats as lazy/SQLite-backed
// (anything absent from the static TierHot seed list, e.g. "svc" — see
// shouldReadHybridNamespaceFromSQLite), where every write is accepted into
// the pending overlay and ScanPage must merge it with the (in this test,
// not-yet-flushed) base via hybridScanPageMerged.
func TestHybridStore_ScanPage_LazyNamespaceMergesOverlayAndBase(t *testing.T) {
	s, err := state.NewHybridStoreWithOptions(t.TempDir(), state.HybridOptions{FlushInterval: time.Hour}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	assertScanPagePaginatesFullRange(t, s, "svc", "queue/", 31, 4)
}

// TestHybridStore_ScanPage_OverlayBaseBoundary_NoDuplicatesOrGaps is the
// pagination-correctness test the storage plan calls out explicitly for
// HybridStore: entries in both the base store (SQLite, seeded before the
// store even opens, so definitely not in the overlay) and the pending
// overlay (new keys, an override of an existing base key, an outright
// delete of a base key, and a DeletePrefix tombstone with a later Set under
// the same prefix — the exact ordering case HybridStore.DeletePrefix's own
// doc comment calls out), paginated with a small page size that forces the
// walk across several base/overlay boundaries.
//
// s.Scan (already covered by its own tests) is used as the correctness
// oracle: it performs the identical merge, just unpaginated. If ScanPage's
// paginated walk agrees with it exactly, the pagination itself introduced no
// duplicates or gaps.
func TestHybridStore_ScanPage_OverlayBaseBoundary_NoDuplicatesOrGaps(t *testing.T) {
	dir := t.TempDir()
	const baseRows = 20
	seedHybridSQLiteNamespace(t, dir, "sqs:messages", baseRows, "base") // queue/00000000 .. queue/00000019

	s, err := state.NewHybridStoreWithOptions(dir, state.HybridOptions{FlushInterval: time.Hour}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	// Overlay entries layered on top of the persisted base, all still
	// pending (FlushInterval is an hour, and nothing calls Flush).
	mustSet := func(key, value string) {
		t.Helper()
		if err := s.Set(ctx, "sqs:messages", key, value); err != nil {
			t.Fatalf("Set %s: %v", key, err)
		}
	}
	// Brand-new keys interleaved between existing base keys.
	mustSet("queue/00000003a", "new")
	mustSet("queue/00000010a", "new")
	// Override an existing base key's value.
	mustSet("queue/00000005", "overridden")
	// Delete an existing base key outright.
	if err := s.Delete(ctx, "sqs:messages", "queue/00000007"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// DeletePrefix a sub-range covering several base keys (00000012..00000019
	// plus anything else under that prefix), then Set one key back into the
	// tombstoned range — it must survive per HybridStore.DeletePrefix's
	// ordering guarantee, while the rest of the range stays deleted.
	if err := s.DeletePrefix(ctx, "sqs:messages", "queue/0000001"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	mustSet("queue/00000015", "resurrected")

	want, err := s.Scan(ctx, "sqs:messages", "queue/")
	if err != nil {
		t.Fatalf("Scan (oracle): %v", err)
	}
	if len(want) == 0 {
		t.Fatal("test setup produced an empty oracle result — the scenario isn't exercising anything")
	}

	for _, pageSize := range []int{1, 3, 7, 1000} {
		t.Run(fmt.Sprintf("pageSize=%d", pageSize), func(t *testing.T) {
			var got []state.KV
			startAfter := ""
			pages := 0
			for {
				pages++
				if pages > len(want)+2 {
					t.Fatalf("ScanPage did not terminate after %d pages", pages)
				}
				page, next, err := s.ScanPage(ctx, "sqs:messages", "queue/", startAfter, pageSize)
				if err != nil {
					t.Fatalf("ScanPage(startAfter=%q): %v", startAfter, err)
				}
				got = append(got, page...)
				if next == "" {
					break
				}
				startAfter = next
			}
			if len(got) != len(want) {
				t.Fatalf("paginated result has %d entries, oracle Scan has %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("entry %d = %+v, want %+v (full got=%v want=%v)", i, got[i], want[i], got, want)
				}
			}
		})
	}
}

// ---- 3.5 vacuum/checkpoint maintenance loop ------------------------------

// TestHybridStore_Maintenance_RunsOnConfiguredInterval proves the background
// maintenance loop actually runs on its configured interval and executes the
// full pragma sequence end to end: enough dead rows are created to push the
// freelist ratio over the vacuum threshold, and the test waits for the
// resulting "incremental_vacuum complete" log — which runSQLitePragmaMaintenance
// only reaches after PRAGMA wal_checkpoint(PASSIVE) has already succeeded,
// so this also proves the checkpoint step works.
func TestHybridStore_Maintenance_RunsOnConfiguredInterval(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	s, err := state.NewHybridStoreWithOptions(t.TempDir(), state.HybridOptions{
		FlushInterval:       50 * time.Millisecond,
		MaintenanceInterval: 50 * time.Millisecond,
	}, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	// Create and then delete a large number of rows so the freelist ratio
	// clears maintenanceVacuumFreelistRatio, and flush so the deletes are
	// actually committed to SQLite (freelist accounting is a SQLite-file
	// property, not something the pending overlay can influence).
	const rows = 2000
	for i := 0; i < rows; i++ {
		if err := s.Set(ctx, "svc", fmt.Sprintf("bulk/%05d", i), strings.Repeat("x", 512)); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush after inserts: %v", err)
	}
	for i := 0; i < rows-5; i++ {
		if err := s.Delete(ctx, "svc", fmt.Sprintf("bulk/%05d", i)); err != nil {
			t.Fatalf("Delete %d: %v", i, err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush after deletes: %v", err)
	}

	waitForObservedLog(t, logs, "hybrid maintenance: incremental_vacuum complete")
}

// TestHybridStore_Maintenance_SkipsWhileDegraded proves the maintenance loop
// never attempts a pragma against a store that degraded to memory-only (an
// unopenable/corrupt database file — see degradeToMemoryOnly) and,
// critically, never panics or logs an error trying: the store must keep
// serving reads/writes normally across several maintenance-interval ticks.
func TestHybridStore_Maintenance_SkipsWhileDegraded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overcast.db"), []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write corrupt db fixture: %v", err)
	}

	core, logs := observer.New(zap.WarnLevel)
	s, err := state.NewHybridStoreWithOptions(dir, state.HybridOptions{
		FlushInterval:       time.Hour,
		MaintenanceInterval: 20 * time.Millisecond,
	}, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.WaitReady(ctx); err == nil {
		t.Fatal("expected WaitReady to surface the corrupt-database error")
	}

	// Let several maintenance ticks pass while degraded.
	time.Sleep(150 * time.Millisecond)

	if err := s.Set(ctx, "svc", "key", "value"); err != nil {
		t.Fatalf("Set on degraded store after maintenance ticks: %v", err)
	}
	got, found, err := s.Get(ctx, "svc", "key")
	if err != nil || !found || got != "value" {
		t.Fatalf("Get on degraded store after maintenance ticks: err=%v found=%v got=%q", err, found, got)
	}
	if n := logs.FilterMessage("hybrid maintenance: wal_checkpoint(PASSIVE) failed").Len(); n != 0 {
		t.Fatalf("maintenance loop attempted a pragma against a degraded store %d times, want 0", n)
	}
}

// TestHybridStore_Close_StopsMaintenanceGoroutineCleanly proves Close waits
// for the maintenance goroutine (started alongside run/runPendingSync in
// NewHybridStoreWithOptions) to actually exit — no goroutine leak across
// repeated open/close cycles. If runMaintenance failed to respect ctx.Done()
// or wg.Done(), Close (which calls s.wg.Wait()) would hang and this test
// would time out.
func TestHybridStore_Close_StopsMaintenanceGoroutineCleanly(t *testing.T) {
	for i := 0; i < 3; i++ {
		s, err := state.NewHybridStoreWithOptions(t.TempDir(), state.HybridOptions{
			FlushInterval:       time.Hour,
			MaintenanceInterval: time.Millisecond,
		}, nil)
		if err != nil {
			t.Fatalf("NewHybridStoreWithOptions: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}
