package state_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

func newWALStore(t *testing.T, dir string) state.Store {
	t.Helper()
	s, err := state.NewWALStore(dir, state.WALOptions{SyncMode: state.WALSyncNever})
	if err != nil {
		t.Fatalf("NewWALStore: %v", err)
	}
	return s
}

func TestWALStore_ReplaysAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1 := newWALStore(t, dir)
	if err := s1.Set(ctx, "s3:buckets", "a", "1"); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := s1.Set(ctx, "s3:buckets", "b", "2"); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	if err := s1.Delete(ctx, "s3:buckets", "a"); err != nil {
		t.Fatalf("Delete a: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close s1: %v", err)
	}

	s2 := newWALStore(t, dir)
	defer s2.Close()

	if _, found, err := s2.Get(ctx, "s3:buckets", "a"); err != nil {
		t.Fatalf("Get a: %v", err)
	} else if found {
		t.Fatalf("expected a to be deleted after replay")
	}
	if got, found, err := s2.Get(ctx, "s3:buckets", "b"); err != nil {
		t.Fatalf("Get b: %v", err)
	} else if !found || got != "2" {
		t.Fatalf("expected b=2 after replay, got found=%v value=%q", found, got)
	}
}

func TestWALStore_ListAndScanPrefix(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s := newWALStore(t, dir)
	defer s.Close()

	_ = s.Set(ctx, "sns:topics", "prefix/a", "1")
	_ = s.Set(ctx, "sns:topics", "prefix/b", "2")
	_ = s.Set(ctx, "sns:topics", "other/c", "3")

	keys, err := s.List(ctx, "sns:topics", "prefix/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}

	pairs, err := s.Scan(ctx, "sns:topics", "prefix/")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs, got %d", len(pairs))
	}
}

func TestWALStore_CompactsLogAtThreshold(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := state.NewWALStore(dir, state.WALOptions{
		SyncMode:    state.WALSyncNever,
		MaxLogBytes: 256,
	})
	if err != nil {
		t.Fatalf("NewWALStore: %v", err)
	}

	for i := 0; i < 100; i++ {
		if err := s.Set(ctx, "dynamodb:items", "k", "value-that-changes"); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "overcast.wal"))
	if err != nil {
		t.Fatalf("Stat wal: %v", err)
	}
	if info.Size() > 4096 {
		t.Fatalf("expected compacted WAL to stay bounded, size=%d", info.Size())
	}

	s2 := newWALStore(t, dir)
	defer s2.Close()
	if got, found, err := s2.Get(ctx, "dynamodb:items", "k"); err != nil {
		t.Fatalf("Get after compact replay: %v", err)
	} else if !found || got != "value-that-changes" {
		t.Fatalf("unexpected value after compact replay: found=%v got=%q", found, got)
	}
}

func TestWALStore_IntervalSyncClosesCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := state.NewWALStore(dir, state.WALOptions{
		SyncMode:     state.WALSyncInterval,
		SyncInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWALStore: %v", err)
	}

	if err := s.Set(ctx, "sqs:queues", "q1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
