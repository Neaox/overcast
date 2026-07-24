package state_test

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

// TestDebugMetricsSnapshot_falseForMemoryStore proves MemoryStore (which
// doesn't implement state.DebugMetricsReporter) produces the documented
// "nothing to report" zero-value result instead of an error.
func TestDebugMetricsSnapshot_falseForMemoryStore(t *testing.T) {
	store := state.NewMemoryStore()
	snapshots, ok := state.DebugMetricsSnapshot(context.Background(), store, state.DebugMetricsOptions{})
	if ok {
		t.Fatalf("expected ok=false for MemoryStore, got ok=true snapshots=%+v", snapshots)
	}
	if len(snapshots) != 0 {
		t.Fatalf("expected no snapshots for MemoryStore, got %+v", snapshots)
	}
}

// TestDebugMetricsSnapshot_falseForWALStore mirrors the MemoryStore case for
// WALStore — neither backend has an async batched-flush/seed lifecycle, so
// neither implements DebugMetricsReporter.
func TestDebugMetricsSnapshot_falseForWALStore(t *testing.T) {
	dir := t.TempDir()
	store, err := state.NewWALStore(dir, state.WALOptions{})
	if err != nil {
		t.Fatalf("NewWALStore: %v", err)
	}
	defer store.Close()

	snapshots, ok := state.DebugMetricsSnapshot(context.Background(), store, state.DebugMetricsOptions{})
	if ok {
		t.Fatalf("expected ok=false for WALStore, got ok=true snapshots=%+v", snapshots)
	}
	if len(snapshots) != 0 {
		t.Fatalf("expected no snapshots for WALStore, got %+v", snapshots)
	}
}
