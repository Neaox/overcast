package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestWALStore_DirtyTracking pins the skip-clean-fsync behavior added after
// fsync-degraded hosts stalled for minutes in Close on stores that never
// wrote (see docs/dev/performance.md). The dirty flag must be false on open
// (even over a pre-existing log), true after an append, false again after a
// WALSyncAlways append's fsync, and Close on a clean store must not error.
func TestWALStore_DirtyTracking(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Pre-existing (whitespace) log — mirrors the replay-tolerance test's
	// shape, the exact scenario whose Close paid a pointless fsync.
	if err := os.WriteFile(filepath.Join(dir, defaultWALFileName), []byte("  \n\n"), 0o644); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	s, err := NewWALStore(dir, WALOptions{SyncMode: WALSyncNever})
	if err != nil {
		t.Fatalf("NewWALStore: %v", err)
	}
	if s.dirty {
		t.Fatal("store dirty immediately after open — nothing has been appended")
	}
	if err := s.Set(ctx, "ns", "k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !s.dirty {
		t.Fatal("store not dirty after append")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close (dirty): %v", err)
	}

	// WALSyncAlways: every append syncs, so the store is never left dirty.
	s2, err := NewWALStore(dir, WALOptions{SyncMode: WALSyncAlways})
	if err != nil {
		t.Fatalf("NewWALStore(always): %v", err)
	}
	if err := s2.Set(ctx, "ns", "k2", "v2"); err != nil {
		t.Fatalf("Set(always): %v", err)
	}
	if s2.dirty {
		t.Fatal("WALSyncAlways left store dirty after synced append")
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close (clean): %v", err)
	}
}
