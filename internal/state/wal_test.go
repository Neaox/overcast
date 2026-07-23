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

// walFilePath returns the on-disk WAL path used by WALStore inside dir,
// matching the unexported defaultWALFileName constant in wal.go.
func walFilePath(dir string) string {
	return filepath.Join(dir, "overcast.wal")
}

// writeRawWAL writes content verbatim to the WAL file inside dir, bypassing
// WALStore entirely so tests can construct torn/corrupt logs byte-for-byte.
func writeRawWAL(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(walFilePath(dir), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile wal: %v", err)
	}
}

func TestWALStore_TornFinalLineDoesNotBlockStartup(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1 := newWALStore(t, dir)
	if err := s1.Set(ctx, "s3:buckets", "a", "1"); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := s1.Set(ctx, "s3:buckets", "b", "2"); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close s1: %v", err)
	}

	// Simulate a process kill mid-append: a well-formed, newline-terminated
	// log followed by a truncated JSON line with no trailing newline.
	f, err := os.OpenFile(walFilePath(dir), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open wal for append: %v", err)
	}
	if _, err := f.WriteString(`{"op":"set","namespace":"s3:buckets","key":"c","valu`); err != nil {
		t.Fatalf("write torn line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}

	s2, err := state.NewWALStore(dir, state.WALOptions{SyncMode: state.WALSyncNever})
	if err != nil {
		t.Fatalf("NewWALStore should tolerate a torn final line, got: %v", err)
	}
	defer s2.Close()

	if got, found, err := s2.Get(ctx, "s3:buckets", "a"); err != nil || !found || got != "1" {
		t.Fatalf("expected a=1 after replay, got found=%v value=%q err=%v", found, got, err)
	}
	if got, found, err := s2.Get(ctx, "s3:buckets", "b"); err != nil || !found || got != "2" {
		t.Fatalf("expected b=2 after replay, got found=%v value=%q err=%v", found, got, err)
	}
	if _, found, err := s2.Get(ctx, "s3:buckets", "c"); err != nil {
		t.Fatalf("Get c: %v", err)
	} else if found {
		t.Fatalf("expected torn entry c to be absent after replay")
	}
}

func TestWALStore_CorruptMidFileLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	content := `{"op":"set","namespace":"sqs:queues","key":"k1","value":"v1"}` + "\n" +
		`not valid json at all` + "\n" +
		`{"op":"set","namespace":"sqs:queues","key":"k2","value":"v2"}` + "\n"
	writeRawWAL(t, dir, content)

	s, err := state.NewWALStore(dir, state.WALOptions{SyncMode: state.WALSyncNever})
	if err != nil {
		t.Fatalf("NewWALStore should skip a corrupt mid-file line, got: %v", err)
	}
	defer s.Close()

	if got, found, err := s.Get(ctx, "sqs:queues", "k1"); err != nil || !found || got != "v1" {
		t.Fatalf("expected k1=v1, got found=%v value=%q err=%v", found, got, err)
	}
	if got, found, err := s.Get(ctx, "sqs:queues", "k2"); err != nil || !found || got != "v2" {
		t.Fatalf("expected k2=v2, got found=%v value=%q err=%v", found, got, err)
	}
}

func TestWALStore_UnknownOpIsSkipped(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	content := `{"op":"set","namespace":"sns:topics","key":"k1","value":"v1"}` + "\n" +
		`{"op":"frobnicate","namespace":"sns:topics","key":"x","value":"y"}` + "\n" +
		`{"op":"set","namespace":"sns:topics","key":"k2","value":"v2"}` + "\n"
	writeRawWAL(t, dir, content)

	s, err := state.NewWALStore(dir, state.WALOptions{SyncMode: state.WALSyncNever})
	if err != nil {
		t.Fatalf("NewWALStore should skip an unknown op, got: %v", err)
	}
	defer s.Close()

	if got, found, err := s.Get(ctx, "sns:topics", "k1"); err != nil || !found || got != "v1" {
		t.Fatalf("expected k1=v1, got found=%v value=%q err=%v", found, got, err)
	}
	if got, found, err := s.Get(ctx, "sns:topics", "k2"); err != nil || !found || got != "v2" {
		t.Fatalf("expected k2=v2, got found=%v value=%q err=%v", found, got, err)
	}
	if _, found, err := s.Get(ctx, "sns:topics", "x"); err != nil {
		t.Fatalf("Get x: %v", err)
	} else if found {
		t.Fatalf("expected entry with unknown op to be skipped, not applied")
	}
}

func TestWALStore_EmptyLogReplaysCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// No WAL file exists at all yet — NewWALStore creates one from scratch.
	s := newWALStore(t, dir)
	defer s.Close()

	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	if len(namespaces) != 0 {
		t.Fatalf("expected empty store, got namespaces=%v", namespaces)
	}
}

func TestWALStore_WhitespaceOnlyLogReplaysCleanly(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	writeRawWAL(t, dir, "   \n\n  \t \n")

	s, err := state.NewWALStore(dir, state.WALOptions{SyncMode: state.WALSyncNever})
	if err != nil {
		t.Fatalf("NewWALStore should tolerate a whitespace-only log, got: %v", err)
	}
	defer s.Close()

	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	if len(namespaces) != 0 {
		t.Fatalf("expected empty store, got namespaces=%v", namespaces)
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
