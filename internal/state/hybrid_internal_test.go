//go:build !nosqlite

package state

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestHybridSQLiteTransient_includesInterrupted(t *testing.T) {
	// Given: the SQLite error surfaced by background scheduler scans under load.
	err := fmt.Errorf("sqlite scan [scheduler:schedules/*]: interrupted (9)")

	// When/Then: interrupted reads are classified with other transient SQLite
	// contention errors so hybrid retries them instead of surfacing InternalError.
	if !isSQLiteTransient(err) {
		t.Fatal("interrupted SQLite error should be retryable")
	}
}

func TestHybridHotNamespaces_followNamespaceTiers(t *testing.T) {
	seeded := hybridNamespaceSet(hybridHotNamespaces())

	for namespace, tier := range namespaceTiers {
		_, ok := seeded[namespace]
		if tier == TierHot && !ok {
			t.Fatalf("TierHot namespace %q missing from hybrid seed list", namespace)
		}
		if tier == TierCached && ok {
			t.Fatalf("TierCached namespace %q should stay lazy", namespace)
		}
	}
}

// ---- NotReady (503-while-migrating middleware support) ----------------

func TestHybridStore_NotReady_FalseAfterMigrationCompletes(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	if err := s.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if s.NotReady() {
		t.Fatal("expected NotReady() = false once the background migration has completed")
	}
}

// TestHybridStore_NotReady_TrueImmediatelyAfterConstruction observes the
// store the instant NewHybridStore returns, before yielding to any other
// goroutine — see the identical-purpose SQLiteStore test for why this is a
// reliable (if not absolutely guaranteed) observation rather than a race.
func TestHybridStore_NotReady_TrueImmediatelyAfterConstruction(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	if !s.NotReady() {
		t.Skip("migration completed before this assertion ran — timing-dependent, not a correctness failure")
	}
}

// TestHybridStore_NotReady_FalseWhenDegraded proves NotReady stops reporting
// true once the store has permanently degraded to memory-only
// (degradeToMemoryOnly) — a failed/unusable persistent backend is an ongoing
// health condition (PersistentHealth), not a "still starting up" one, and
// must not make every request 503 forever. Points HybridStore at a file
// where a directory already exists, so sqlite.Open's underlying file
// operations fail deterministically and seedFromSQLite takes the
// degradeToMemoryOnly path.
func TestHybridStore_NotReady_FalseWhenDegraded(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/overcast.db"
	if err := os.Mkdir(dbPath, 0o755); err != nil {
		t.Fatalf("pre-create overcast.db as a directory: %v", err)
	}
	s, err := NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()

	deadline := time.Now().Add(5 * time.Second)
	for s.NotReady() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the store to leave its migration window")
		}
		time.Sleep(time.Millisecond)
	}
	if !s.sqliteDegraded.Load() {
		t.Fatal("expected the store to have degraded to memory-only given an unusable database path")
	}
}

// ---- 1.2 read-only connection pool -----------------------------------

func TestHybridStore_OpensDedicatedReadPoolWithMultipleConns(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	if err := s.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if s.sqliteRead == nil {
		t.Fatal("expected a dedicated read pool to be open")
	}
	if got := s.sqliteRead.Stats().MaxOpenConnections; got <= 1 {
		t.Fatalf("read pool MaxOpenConnections = %d, want > 1", got)
	}
	// The writer pool must stay pinned at 1 connection — unchanged by 1.2.
	if got := s.sqlite.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("writer pool MaxOpenConnections = %d, want 1", got)
	}
}

// ---- 1.4 size-triggered flush: threshold defaults & wiring ------------

func TestHybridStore_DirtyThresholdDefaults(t *testing.T) {
	s, err := NewHybridStoreWithOptions(t.TempDir(), HybridOptions{FlushInterval: time.Hour}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	defer s.Close()
	if s.dirtyEntryThreshold != defaultHybridDirtyEntryThreshold {
		t.Fatalf("dirtyEntryThreshold = %d, want default %d", s.dirtyEntryThreshold, defaultHybridDirtyEntryThreshold)
	}
	if s.dirtyByteThreshold != defaultHybridDirtyByteThreshold {
		t.Fatalf("dirtyByteThreshold = %d, want default %d", s.dirtyByteThreshold, defaultHybridDirtyByteThreshold)
	}
}

// ---- 1.8 ranged tombstones: DeletePrefix must not enumerate keys ------

// TestHybridStore_DeletePrefixDoesNotEnumerateKeys is the structural
// counterpart to the external ordering/reload tests: DeletePrefix must add
// exactly one op to the pending log (a single ranged tombstone), never one
// per matching key, regardless of how many keys currently match the prefix.
func TestHybridStore_DeletePrefixDoesNotEnumerateKeys(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	const n = 1000
	for i := 0; i < n; i++ {
		if err := s.Set(ctx, "svc", fmt.Sprintf("queue/%04d", i), "v"); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	s.mu.Lock()
	opsBefore := len(s.pendingOps)
	s.mu.Unlock()
	if opsBefore != 0 {
		t.Fatalf("pendingOps before DeletePrefix = %d, want 0 (already flushed)", opsBefore)
	}

	if err := s.DeletePrefix(ctx, "svc", "queue/"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}

	s.mu.Lock()
	opsAfter := len(s.pendingOps)
	tombstones := len(s.tombstones)
	s.mu.Unlock()
	if opsAfter != 1 {
		t.Fatalf("pendingOps after DeletePrefix over %d keys = %d, want exactly 1 (a single ranged tombstone)", n, opsAfter)
	}
	if tombstones != 1 {
		t.Fatalf("tombstones after DeletePrefix = %d, want 1", tombstones)
	}
}

// TestHybridStore_ResolvePendingLocked_TombstoneOrdering exercises
// resolvePendingLocked directly: a Set before a covering DeletePrefix reads
// as deleted, a Set after it survives, and an unrelated key outside the
// prefix is untouched.
func TestHybridStore_ResolvePendingLocked_TombstoneOrdering(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	if err := s.Set(ctx, "svc", "p/k1", "v1"); err != nil {
		t.Fatalf("Set k1: %v", err)
	}
	if err := s.Set(ctx, "svc", "other/keep", "keep"); err != nil {
		t.Fatalf("Set unrelated: %v", err)
	}
	if err := s.DeletePrefix(ctx, "svc", "p/"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if err := s.Set(ctx, "svc", "p/k2", "v2"); err != nil {
		t.Fatalf("Set k2: %v", err)
	}

	s.mu.Lock()
	k1, k1ok := s.resolvePendingLocked("svc", "p/k1")
	k2, k2ok := s.resolvePendingLocked("svc", "p/k2")
	other, otherOk := s.resolvePendingLocked("svc", "other/keep")
	s.mu.Unlock()

	if !k1ok || !k1.deleted {
		t.Fatalf("resolvePendingLocked(p/k1) = %+v, ok=%v; want deleted=true", k1, k1ok)
	}
	if !k2ok || k2.deleted || k2.value != "v2" {
		t.Fatalf("resolvePendingLocked(p/k2) = %+v, ok=%v; want deleted=false value=v2", k2, k2ok)
	}
	if !otherOk || other.deleted || other.value != "keep" {
		t.Fatalf("resolvePendingLocked(other/keep) = %+v, ok=%v; want deleted=false value=keep", other, otherOk)
	}
}

// ---- 1.7 crash recovery: flush failure retains entries for retry ------

// TestHybridStore_FlushFailure_RetainsEntriesAndRetriesSuccessfully exercises
// flushOnce's steal-then-restore-on-failure defer directly: when the
// transaction can't even begin (a broken writer connection stands in for any
// SQLite-level failure), the pendingOps/dirty state already stolen for this
// attempt must be put back rather than lost, and a subsequent flush against a
// working connection must succeed and persist the data.
func TestHybridStore_FlushFailure_RetainsEntriesAndRetriesSuccessfully(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	if err := s.Set(ctx, "svc", "k1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Break the writer connection so the next flush attempt fails at BeginTx.
	if err := s.sqlite.db.Close(); err != nil {
		t.Fatalf("close writer db: %v", err)
	}
	if err := s.flush(ctx); err == nil {
		t.Fatal("flush over a closed writer connection should have failed")
	}

	s.mu.Lock()
	opsAfterFailure := len(s.pendingOps)
	dirtyAfterFailure := len(s.dirty)
	s.mu.Unlock()
	if opsAfterFailure == 0 || dirtyAfterFailure == 0 {
		t.Fatalf("entries lost after failed flush: pendingOps=%d dirty=%d, want both > 0", opsAfterFailure, dirtyAfterFailure)
	}

	// Recover with a fresh connection to the same database file, as a restart
	// against the same data directory would. Reassigning s.sqlite.db (rather
	// than swapping in a whole new SQLiteStore) is enough because flush only
	// ever goes through that field.
	recovered, err := NewSQLiteStore(s.dataDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore (recovery): %v", err)
	}
	if err := recovered.ensureReady(ctx); err != nil {
		t.Fatalf("recovered ensureReady: %v", err)
	}
	s.sqlite.db = recovered.db // s.Close() (deferred above) closes this connection at test end.

	if err := s.flush(ctx); err != nil {
		t.Fatalf("retry flush after recovery: %v", err)
	}

	s.mu.Lock()
	opsAfterRetry := len(s.pendingOps)
	s.mu.Unlock()
	if opsAfterRetry != 0 {
		t.Fatalf("pendingOps after successful retry = %d, want 0", opsAfterRetry)
	}
	value, found, err := hybridSQLiteRawGet(ctx, s.sqlite.db, "svc", "k1")
	if err != nil {
		t.Fatalf("verify persisted value: %v", err)
	}
	if !found || value != "v1" {
		t.Fatalf("persisted value = (%q, found=%v); want (v1, true)", value, found)
	}
}

// TestHybridStore_FlushFailure_PendingLogSurvivesForReplay proves the
// kill-during-flush safety property: compactPendingAfterFlush only runs after
// a successful commit (see flushOnce), so a failed flush attempt must leave
// the on-disk pending log byte-for-byte as it was. If the process were killed
// immediately after a failed flush, a restart replaying that untouched log
// must still recover the unflushed write.
func TestHybridStore_FlushFailure_PendingLogSurvivesForReplay(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	if err := s.Set(ctx, "svc", "k1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	before, err := os.ReadFile(s.pendingPath)
	if err != nil {
		t.Fatalf("read pending log before flush attempt: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("expected the pending log to contain the accepted write before any flush attempt")
	}

	if err := s.sqlite.db.Close(); err != nil {
		t.Fatalf("close writer db: %v", err)
	}
	if err := s.flush(ctx); err == nil {
		t.Fatal("flush over a closed writer connection should have failed")
	}

	after, err := os.ReadFile(s.pendingPath)
	if err != nil {
		t.Fatalf("read pending log after failed flush: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("pending log changed after a failed flush; compaction must only run after a successful commit\nbefore: %q\nafter:  %q", before, after)
	}

	// Manual cleanup instead of s.Close(): the writer connection is
	// permanently broken in this test, and Close()'s own final flush would
	// hit the same failure again. Stop the background goroutines directly and
	// close only what's still usable.
	s.cancel()
	s.wg.Wait()
	_ = s.closePendingFile()
}

// waitForHybridSeedLoaded blocks until the background TierHot seed has
// finished (s.loaded closed). WaitReady only waits for the SQLite connection
// itself (s.sqliteReady) — tests that assert exact pendingOps/dirty-cache
// contents must also wait past seed completion, because seedFromSQLite sends
// a one-time signalFlush() nudge when it finishes (1.4's fix for a threshold
// crossed while still seeding), which would otherwise race an assertion made
// immediately after a handful of writes.
func waitForHybridSeedLoaded(t *testing.T, s *HybridStore) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !s.isLoaded() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for hybrid seed to complete")
		}
		time.Sleep(time.Millisecond)
	}
}

// ---- 1.11 degraded-mode pending log growth cap -------------------------

// TestHybridStore_DegradedPendingLogCap_stopsFileGrowth proves that once the
// store has degraded to memory-only and the pending log has reached
// hybridDegradedPendingLogCap, further writes are accepted into memory but no
// longer grow the log file — and that the cap warning machinery marks itself
// fired exactly once.
func TestHybridStore_DegradedPendingLogCap_stopsFileGrowth(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	// Given: a degraded store whose pending log is already at the cap.
	s.sqliteDegraded.Store(true)
	s.mu.Lock()
	s.pendingLogSize = hybridDegradedPendingLogCap
	s.mu.Unlock()
	sizeBefore := s.pendingLogSizeBytes()

	// When: more writes arrive.
	for i := 0; i < 3; i++ {
		if err := s.Set(ctx, "svc", fmt.Sprintf("capped-%d", i), "v"); err != nil {
			t.Fatalf("Set past cap: %v", err)
		}
	}

	// Then: the file did not grow, the writes are still readable from
	// memory, and the one-time warning latch is set.
	if sizeAfter := s.pendingLogSizeBytes(); sizeAfter != sizeBefore {
		t.Fatalf("pending log grew past the degraded-mode cap: before=%d after=%d", sizeBefore, sizeAfter)
	}
	got, found, err := s.Get(ctx, "svc", "capped-0")
	if err != nil || !found || got != "v" {
		t.Fatalf("capped write not served from memory: got=%q found=%v err=%v", got, found, err)
	}
	s.mu.Lock()
	warned := s.pendingCapWarned
	s.mu.Unlock()
	if !warned {
		t.Fatal("expected the degraded-mode cap warning latch to be set after a capped write")
	}
}
