package state_test

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

// TestDebugMetricsSnapshot_memoryStoreReportsCounters proves MemoryStore
// implements state.DebugMetricsReporter to surface storage-activity counters
// (health/metrics "storage activity" card) even though it has no async
// write path or startup seed — every other DebugMetrics field stays at its
// zero value.
func TestDebugMetricsSnapshot_memoryStoreReportsCounters(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemoryStore()

	if err := store.Set(ctx, "s3", "bucket-1", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, _, err := store.Get(ctx, "s3", "bucket-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, store, state.DebugMetricsOptions{})
	if !ok {
		t.Fatal("expected ok=true for MemoryStore")
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot, got %+v", snapshots)
	}
	m := snapshots[0]
	if m.Mode != "memory" {
		t.Errorf("expected mode %q, got %q", "memory", m.Mode)
	}
	if m.Counters.Writes != 1 {
		t.Errorf("expected 1 write, got %d", m.Counters.Writes)
	}
	if m.Counters.Reads != 1 {
		t.Errorf("expected 1 read, got %d", m.Counters.Reads)
	}
}

// TestDebugMetricsSnapshot_walStoreReportsCounters mirrors the MemoryStore
// case for WALStore, which proxies its counters from its embedded
// MemoryStore (see WALStore.DebugMetrics) rather than keeping a second,
// redundant tally.
func TestDebugMetricsSnapshot_walStoreReportsCounters(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewWALStore(dir, state.WALOptions{})
	if err != nil {
		t.Fatalf("NewWALStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Set(ctx, "s3", "bucket-1", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, _, err := store.Get(ctx, "s3", "bucket-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, store, state.DebugMetricsOptions{})
	if !ok {
		t.Fatal("expected ok=true for WALStore")
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot, got %+v", snapshots)
	}
	m := snapshots[0]
	if m.Mode != "wal" {
		t.Errorf("expected mode %q, got %q", "wal", m.Mode)
	}
	if m.Counters.Writes != 1 {
		t.Errorf("expected 1 write, got %d", m.Counters.Writes)
	}
	if m.Counters.Reads != 1 {
		t.Errorf("expected 1 read, got %d", m.Counters.Reads)
	}
}
