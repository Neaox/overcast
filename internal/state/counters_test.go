//go:build !nosqlite

package state_test

// Coverage for the storage-activity counters (health/metrics "storage
// activity" card — internal/state.StoreCounters) added to DebugMetrics: every
// Store implementation's total reads/writes, plus HybridStore's memory-vs-
// SQLite read split and its flushed-rows write counter. Exported-API only
// (package state_test) — SQLiteStore and HybridStore live behind the
// !nosqlite build tag, same as their other exported-API test files (see
// hybrid_flushchunk_test.go).

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// TestSQLiteStore_DebugMetrics_Counters proves SQLiteStore tallies total
// reads and writes — it has no second tier to split against, since every
// operation already goes straight to SQLite.
func TestSQLiteStore_DebugMetrics_Counters(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Set(ctx, "s3:buckets", "my-bucket", `{"name":"my-bucket"}`); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, _, err := store.Get(ctx, "s3:buckets", "my-bucket"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := store.List(ctx, "s3:buckets", ""); err != nil {
		t.Fatalf("List: %v", err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, store, state.DebugMetricsOptions{})
	if !ok || len(snapshots) != 1 {
		t.Fatalf("DebugMetricsSnapshot: ok=%v snapshots=%d, want one entry", ok, len(snapshots))
	}
	m := snapshots[0]
	if m.Mode != "persistent" {
		t.Errorf("expected mode %q, got %q", "persistent", m.Mode)
	}
	if m.Counters.Writes != 1 {
		t.Errorf("expected 1 write, got %d", m.Counters.Writes)
	}
	if m.Counters.Reads != 2 {
		t.Errorf("expected 2 reads (Get + List), got %d", m.Counters.Reads)
	}
	if m.Counters.ReadsMemory != 0 || m.Counters.ReadsSQLite != 0 {
		t.Errorf("expected no memory/sqlite tier split for SQLiteStore, got %+v", m.Counters)
	}
}

// TestHybridStore_DebugMetrics_Counters_TierHotServesFromMemory proves that
// once the store has finished seeding, Get/Set against a TierHot namespace
// (always resident in memory — see tier.go) are counted as memory-tier
// reads/writes, never SQLite reads, and that Reads == ReadsMemory +
// ReadsSQLite.
func TestHybridStore_DebugMetrics_Counters_TierHotServesFromMemory(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewHybridStore(dir, time.Hour) // long interval: no background flush during this test
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	const ns = "sqs:queues" // TierHot
	if err := store.Set(ctx, ns, "us-east-1/my-queue", `{"name":"my-queue"}`); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, _, err := store.Get(ctx, ns, "us-east-1/my-queue"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, store, state.DebugMetricsOptions{})
	if !ok || len(snapshots) != 1 {
		t.Fatalf("DebugMetricsSnapshot: ok=%v snapshots=%d, want one entry", ok, len(snapshots))
	}
	m := snapshots[0]
	if m.Mode != "hybrid" {
		t.Errorf("expected mode %q, got %q", "hybrid", m.Mode)
	}
	if m.Counters.Writes != 1 {
		t.Errorf("expected 1 write, got %d", m.Counters.Writes)
	}
	if m.Counters.ReadsMemory != 1 {
		t.Errorf("expected 1 memory-tier read, got %d (counters=%+v)", m.Counters.ReadsMemory, m.Counters)
	}
	if m.Counters.ReadsSQLite != 0 {
		t.Errorf("expected 0 sqlite-tier reads for a TierHot namespace, got %d", m.Counters.ReadsSQLite)
	}
	if m.Counters.Reads != m.Counters.ReadsMemory+m.Counters.ReadsSQLite {
		t.Errorf("expected Reads == ReadsMemory + ReadsSQLite, got Reads=%d ReadsMemory=%d ReadsSQLite=%d",
			m.Counters.Reads, m.Counters.ReadsMemory, m.Counters.ReadsSQLite)
	}
}

// TestHybridStore_DebugMetrics_Counters_TierCachedReadAfterFlushHitsSQLite
// proves that once a TierCached namespace's write has actually been flushed
// (so the pending-overlay hit in Get no longer applies), a subsequent Get is
// counted as a SQLite-tier read — the fall-through the user-facing "memory
// vs disk" split exists to surface — and that the flush itself is reflected
// in WritesFlushedRows.
func TestHybridStore_DebugMetrics_Counters_TierCachedReadAfterFlushHitsSQLite(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewHybridStore(dir, time.Hour) // long interval: only the explicit Flush below should fire
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	const ns = "s3:objects" // TierCached
	if err := store.Set(ctx, ns, "us-east-1/bucket/obj", `{"i":1}`); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := state.Flush(ctx, store); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// The write is now durable in SQLite and no longer in the pending
	// overlay, so this Get must fall through to sqliteGet.
	if _, found, err := store.Get(ctx, ns, "us-east-1/bucket/obj"); err != nil || !found {
		t.Fatalf("Get after flush: found=%v err=%v", found, err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, store, state.DebugMetricsOptions{})
	if !ok || len(snapshots) != 1 {
		t.Fatalf("DebugMetricsSnapshot: ok=%v snapshots=%d, want one entry", ok, len(snapshots))
	}
	m := snapshots[0]
	if m.Counters.ReadsSQLite < 1 {
		t.Errorf("expected at least 1 sqlite-tier read after flush, got %d (counters=%+v)", m.Counters.ReadsSQLite, m.Counters)
	}
	if m.Counters.WritesFlushedRows < 1 {
		t.Errorf("expected at least 1 flushed row after an explicit Flush, got %d", m.Counters.WritesFlushedRows)
	}
	if m.Counters.Writes != 1 {
		t.Errorf("expected 1 write accepted, got %d", m.Counters.Writes)
	}
}
