//go:build !nosqlite

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// ---- SQLiteStore contract tests --------------------------------------------
//
// These mirror the MemoryStore tests to verify both implementations are
// interchangeable. Every Store contract test should appear in both suites.

func newSQLiteStore(t *testing.T) *state.SQLiteStore {
	t.Helper()
	s, err := state.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSQLiteStore_GetSetDelete(t *testing.T) {
	s := newSQLiteStore(t)
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

func TestSQLiteStore_Delete_nonExistent(t *testing.T) {
	s := newSQLiteStore(t)
	if err := s.Delete(context.Background(), "ns", "missing"); err != nil {
		t.Errorf("Delete non-existent key returned error: %v", err)
	}
}

func TestSQLiteStore_Set_overwrite(t *testing.T) {
	s := newSQLiteStore(t)
	ctx := context.Background()

	s.Set(ctx, "ns", "key", "first")
	s.Set(ctx, "ns", "key", "second")

	got, _, _ := s.Get(ctx, "ns", "key")
	if got != "second" {
		t.Errorf("expected second value to win, got %q", got)
	}
}

func TestSQLiteStore_List_prefix(t *testing.T) {
	s := newSQLiteStore(t)
	ctx := context.Background()

	s.Set(ctx, "ns", "prefix/a", "1")
	s.Set(ctx, "ns", "prefix/b", "2")
	s.Set(ctx, "ns", "other/c", "3")

	keys, err := s.List(ctx, "ns", "prefix/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys with prefix 'prefix/', got %d: %v", len(keys), keys)
	}
}

func TestSQLiteStore_List_emptyPrefix(t *testing.T) {
	s := newSQLiteStore(t)
	ctx := context.Background()

	s.Set(ctx, "ns", "a", "1")
	s.Set(ctx, "ns", "b", "2")

	keys, err := s.List(ctx, "ns", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestSQLiteStore_List_returnsEmptySlice(t *testing.T) {
	s := newSQLiteStore(t)

	keys, err := s.List(context.Background(), "empty-ns", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if keys == nil {
		t.Error("List should return empty slice, not nil")
	}
}

func TestSQLiteStore_namespaceIsolation(t *testing.T) {
	s := newSQLiteStore(t)
	ctx := context.Background()

	s.Set(ctx, "ns1", "key", "ns1-value")
	s.Set(ctx, "ns2", "key", "ns2-value")

	v1, _, _ := s.Get(ctx, "ns1", "key")
	v2, _, _ := s.Get(ctx, "ns2", "key")

	if v1 != "ns1-value" || v2 != "ns2-value" {
		t.Errorf("namespace isolation broken: ns1=%q ns2=%q", v1, v2)
	}
}

func TestSQLiteStore_Close(t *testing.T) {
	s, err := state.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// Close must wait for the background migration goroutine to finish before
// it closes the underlying *sql.DB. If it didn't, the migrate goroutine
// would race with handle teardown — a regression worth pinning down with
// the race detector enabled (`go test -race`).
func TestSQLiteStore_CloseImmediatelyAfterOpen_isRaceFree(t *testing.T) {
	for i := 0; i < 3; i++ {
		s, err := state.NewSQLiteStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewSQLiteStore #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Errorf("Close #%d returned error: %v", i, err)
		}
	}
}

// Operations issued with an already-cancelled context must surface ctx.Err()
// instead of blocking on the migration ready channel. The cancelled-ctx case
// before migration completes is the only way a fast caller can bail out of
// the cold-start ~200–300 ms migrate cost.
func TestSQLiteStore_CancelledContext_doesNotBlockOnMigrate(t *testing.T) {
	s := newSQLiteStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before migration may have completed

	// We don't assert which error we get — Go's select is free to pick the
	// already-ready channel. We assert only that the call returns promptly
	// (the test would deadlock or be killed by `go test -timeout` otherwise).
	done := make(chan struct{})
	go func() {
		_, _, _ = s.Get(ctx, "ns", "k")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Get with cancelled context did not return within 2s")
	}
}

func TestSQLiteStore_List_escapeLikeSpecials(t *testing.T) {
	s := newSQLiteStore(t)
	ctx := context.Background()

	// Keys that contain LIKE special chars should be matched literally.
	s.Set(ctx, "ns", "pre%fix/a", "1")
	s.Set(ctx, "ns", "pre_fix/b", "2")
	s.Set(ctx, "ns", "other/c", "3")

	// Should find the percent key only.
	keys, err := s.List(ctx, "ns", "pre%fix/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key for percent prefix, got %d: %v", len(keys), keys)
	}

	// Should find the underscore key only.
	keys, err = s.List(ctx, "ns", "pre_fix/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("expected 1 key for underscore prefix, got %d: %v", len(keys), keys)
	}
}

func TestNewSQLiteStore_persistsAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Write data to the first instance.
	s1, err := state.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	if err := s1.Set(ctx, "ns", "key", "persisted"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	s1.Close()

	// Re-open and verify data survived.
	s2, err := state.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore second open: %v", err)
	}
	defer s2.Close()

	got, found, err := s2.Get(ctx, "ns", "key")
	if err != nil || !found || got != "persisted" {
		t.Errorf("data did not survive reopen: err=%v found=%v got=%q", err, found, got)
	}
}
