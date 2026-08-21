//go:build !nosqlite

package state

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestHybridStore_Set_TierCached_DoesNotGrowMemoryTier is the failing-test-
// first regression check for #785: HybridStore.Set used to write every value
// into s.mem unconditionally, regardless of the namespace's tier, so a
// TierCached namespace's writes (e.g. "sqs:messages", "lambda:function-code",
// "s3:objects") accumulated in memory for the rest of the process's life even
// though nothing ever reads them back from mem — every read path checks the
// pending overlay first, then SQLite once ready, and only falls back to mem
// pre-ready/degraded. This proves that after Set, the memory tier
// (s.mem, introspected via its existing Len() — "Used by
// /_overcast/debug/state and tests", per memory.go) stays flat for a
// TierCached namespace regardless of how much is written, while reads
// through the public Store contract keep returning the correct values (via
// the pending overlay, then via SQLite after a Flush).
func TestHybridStore_Set_TierCached_DoesNotGrowMemoryTier(t *testing.T) {
	s := newBenchStyleHybridStore(t)
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	const ns = "sqs:messages" // real, registered TierCached namespace
	const n = 500
	baseline := s.mem.Len()

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("my-queue/msg-%d", i)
		value := fmt.Sprintf(`{"body":"payload-%d","attributes":{}}`, i)
		if err := s.Set(ctx, ns, key, value); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}

	if got := s.mem.Len(); got != baseline {
		t.Fatalf("memory tier grew from %d to %d after %d TierCached writes; Set must not pin cold-tier values into mem (#785)", baseline, got, n)
	}

	// Reads must still be correct — served from the pending overlay before
	// any flush.
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("my-queue/msg-%d", i)
		want := fmt.Sprintf(`{"body":"payload-%d","attributes":{}}`, i)
		got, found, err := s.Get(ctx, ns, key)
		if err != nil || !found || got != want {
			t.Fatalf("Get %s (pre-flush) = (%q, %v, %v), want (%q, true, nil)", key, got, found, err, want)
		}
	}

	// Flush drains the overlay into SQLite. The memory tier must still not
	// have grown, and reads must now be correct via the SQLite path.
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := s.mem.Len(); got != baseline {
		t.Fatalf("memory tier grew from %d to %d after Flush; a flushed TierCached write must not leave a copy in mem", baseline, got)
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("my-queue/msg-%d", i)
		want := fmt.Sprintf(`{"body":"payload-%d","attributes":{}}`, i)
		got, found, err := s.Get(ctx, ns, key)
		if err != nil || !found || got != want {
			t.Fatalf("Get %s (post-flush) = (%q, %v, %v), want (%q, true, nil)", key, got, found, err, want)
		}
	}
}

// TestHybridStore_Set_UnregisteredNamespace_DoesNotGrowMemoryTier covers
// TierFor's documented fallback: a namespace absent from namespaceTiers reads
// as TierCached (tier.go's TierFor doc comment), and shouldReadHybridNamespaceFromSQLite
// agrees since the hybrid seed list is built from the same table. Set must
// honour that fallback the same way it honours an explicit TierCached entry.
func TestHybridStore_Set_UnregisteredNamespace_DoesNotGrowMemoryTier(t *testing.T) {
	s := newBenchStyleHybridStore(t)
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	const ns = "svc:unregistered"
	if _, ok := namespaceTiers[ns]; ok {
		t.Fatalf("test setup: %q must not be a registered namespace", ns)
	}
	baseline := s.mem.Len()

	for i := 0; i < 200; i++ {
		if err := s.Set(ctx, ns, fmt.Sprintf("k%d", i), "v"); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	if got := s.mem.Len(); got != baseline {
		t.Fatalf("memory tier grew from %d to %d for an unregistered (TierCached-fallback) namespace", baseline, got)
	}
}

// TestHybridStore_Set_TierHot_StillPopulatesMemoryTier is the regression
// guard on the other side of the fix: TierHot namespaces must keep being
// written straight into mem on every Set, exactly as before — TierHot is
// "always held in memory and never evicted" (tier.go) and every TierHot read
// once seeded/loaded serves from mem alone with no overlay/SQLite fallback in
// the steady state.
func TestHybridStore_Set_TierHot_StillPopulatesMemoryTier(t *testing.T) {
	s := newBenchStyleHybridStore(t)
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	const ns = "sqs:queues" // real, registered TierHot namespace
	baseline := s.mem.Len()

	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("queue-%d", i)
		if err := s.Set(ctx, ns, key, fmt.Sprintf(`{"name":%q}`, key)); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	if got := s.mem.Len(); got != baseline+50 {
		t.Fatalf("memory tier = %d after 50 TierHot writes (baseline %d), want %d — TierHot must stay resident in mem", got, baseline, baseline+50)
	}
}

// TestHybridStore_Set_TierCached_Degraded_NoStaleMemoryCopy proves the
// invalidation-correctness half of #785, not just the memory-usage half: even
// once the persistent backend has degraded to memory-only (storage-plan.md
// 1.11 — "reads/writes work" on the pending overlay, flushes are permanently
// skipped so the overlay is never cleared for the rest of this run), a
// TierCached Set still must not write a second, independently-updatable copy
// into mem. Two copies of the same key (one in the overlay, one pinned in
// mem) is exactly the shape of bug that could later read stale: whichever
// copy Get happens to consult first would win, and nothing here keeps them in
// sync. With the fix, mem never held a copy in the first place, so there is
// nothing to go stale — the overlay is the sole and therefore consistent
// source of truth for this key regardless of degraded state.
func TestHybridStore_Set_TierCached_Degraded_NoStaleMemoryCopy(t *testing.T) {
	s := newBenchStyleHybridStore(t)
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)

	const ns = "sqs:messages"
	baseline := s.mem.Len()

	s.sqliteDegraded.Store(true)

	if err := s.Set(ctx, ns, "k1", "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set(ctx, ns, "k1", "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	if got := s.mem.Len(); got != baseline {
		t.Fatalf("memory tier grew from %d to %d for a degraded-mode TierCached write; mem must never hold a copy for this namespace", baseline, got)
	}
	got, found, err := s.Get(ctx, ns, "k1")
	if err != nil || !found || got != "v2" {
		t.Fatalf("Get k1 (degraded) = (%q, %v, %v), want (\"v2\", true, nil) — must observe the latest write via the overlay, not a stale mem copy", got, found, err)
	}
	// mem itself must never have been asked to store this key — confirms
	// there is no second, independently-stale copy sitting in mem.
	if _, found, _ := s.mem.Get(ctx, ns, "k1"); found {
		t.Fatal("mem holds a copy of a degraded-mode TierCached key; a second copy is exactly the staleness hazard #785 warns about")
	}
}

// TestHybridStore_Restart_ReplaysPendingTierCachedWrite_WithoutGrowingMemory
// covers crash-recovery replay (replayPendingLog -> applyPendingEntry), the
// third writer of mem besides Set/Delete/DeletePrefix. A process that Set a
// TierCached key and crashed before the next flush leaves that write only in
// the on-disk pending log; the next process replays it on startup. That
// replay must not reopen #785 on every restart by pinning the replayed value
// into mem — the replayed overlay entry (which applyPendingEntry populates
// unconditionally, same as a live Set) is what read paths already consult
// first, exactly as for a live write.
func TestHybridStore_Restart_ReplaysPendingTierCachedWrite_WithoutGrowingMemory(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1, err := NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	if err := s1.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	// Stop the background flusher entirely so the write below is never
	// flushed — it must still be sitting in the on-disk pending log, exactly
	// as if the process crashed right after accepting it.
	waitForHybridSeedThenStopBackground(t, s1)

	const ns = "sqs:messages"
	if err := s1.Set(ctx, ns, "crash-key", "crash-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Manual cleanup instead of s1.Close(): Close performs a final flush,
	// which would defeat the point of this test (the write must reach the
	// second process only via pending-log replay, never via SQLite). Mirrors
	// the cleanup in TestHybridStore_FlushFailure_PendingLogSurvivesForReplay.
	if s1.sqliteRead != nil {
		_ = s1.sqliteRead.Close()
	}
	if err := s1.sqlite.db.Close(); err != nil {
		t.Fatalf("close writer db: %v", err)
	}
	if err := s1.closePendingFile(); err != nil {
		t.Fatalf("close pending file: %v", err)
	}

	s2, err := NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore (restart): %v", err)
	}
	defer s2.Close()
	if err := s2.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady (restart): %v", err)
	}

	if got := s2.mem.Len(); got != 0 {
		t.Fatalf("restarted store's memory tier has %d entries after replaying one TierCached pending write, want 0", got)
	}
	got, found, err := s2.Get(ctx, ns, "crash-key")
	if err != nil || !found || got != "crash-value" {
		t.Fatalf("Get crash-key after restart = (%q, %v, %v), want (\"crash-value\", true, nil) — replay must still make the pending write visible", got, found, err)
	}
}

// newBenchStyleHybridStore mirrors newBenchHybridStore (hybrid_bench_test.go)
// for use from *testing.T: a fresh temp-dir HybridStore with a long flush
// interval so a timer-driven background flush cannot race the assertions
// below (tests that need flushing call s.Flush explicitly).
func newBenchStyleHybridStore(t *testing.T) *HybridStore {
	t.Helper()
	s, err := NewHybridStoreWithOptions(t.TempDir(), HybridOptions{FlushInterval: time.Hour}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
