package state_test

import (
	"context"
	"testing"

	"github.com/your-org/overcast/internal/state"
)

// These tests define the contract for ALL Store implementations.
// When SQLiteStore is added, run the same suite against it via a table test.

func TestMemoryStore_GetSetDelete(t *testing.T) {
	s := state.NewMemoryStore()
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

func TestMemoryStore_Delete_nonExistent(t *testing.T) {
	s := state.NewMemoryStore()
	// Deleting a non-existent key must not return an error.
	if err := s.Delete(context.Background(), "ns", "missing"); err != nil {
		t.Errorf("Delete non-existent key returned error: %v", err)
	}
}

func TestMemoryStore_Set_overwrite(t *testing.T) {
	s := state.NewMemoryStore()
	ctx := context.Background()

	s.Set(ctx, "ns", "key", "first")
	s.Set(ctx, "ns", "key", "second")

	got, _, _ := s.Get(ctx, "ns", "key")
	if got != "second" {
		t.Errorf("expected second value to win, got %q", got)
	}
}

func TestMemoryStore_List_prefix(t *testing.T) {
	s := state.NewMemoryStore()
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

func TestMemoryStore_List_emptyPrefix(t *testing.T) {
	s := state.NewMemoryStore()
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

func TestMemoryStore_List_returnsEmptySlice(t *testing.T) {
	s := state.NewMemoryStore()

	keys, err := s.List(context.Background(), "empty-ns", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if keys == nil {
		t.Error("List should return empty slice, not nil")
	}
}

func TestMemoryStore_namespaceIsolation(t *testing.T) {
	s := state.NewMemoryStore()
	ctx := context.Background()

	s.Set(ctx, "ns1", "key", "ns1-value")
	s.Set(ctx, "ns2", "key", "ns2-value")

	v1, _, _ := s.Get(ctx, "ns1", "key")
	v2, _, _ := s.Get(ctx, "ns2", "key")

	if v1 != "ns1-value" || v2 != "ns2-value" {
		t.Errorf("namespace isolation broken: ns1=%q ns2=%q", v1, v2)
	}
}

func TestMemoryStore_concurrentAccess(t *testing.T) {
	// Run with -race to detect data races.
	s := state.NewMemoryStore()
	ctx := context.Background()

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(i int) {
			key := "key"
			s.Set(ctx, "ns", key, "value")
			s.Get(ctx, "ns", key)
			s.List(ctx, "ns", "")
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}

// ---- MemoryStore additional method coverage --------------------------------

func TestMemoryStore_Close(t *testing.T) {
	s := state.NewMemoryStore()
	if err := s.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestMemoryStore_Reset_wipesData(t *testing.T) {
	s := state.NewMemoryStore()
	ctx := context.Background()

	s.Set(ctx, "ns", "key1", "val1")
	s.Set(ctx, "ns", "key2", "val2")

	s.Reset()

	_, found, _ := s.Get(ctx, "ns", "key1")
	if found {
		t.Error("expected key1 to be gone after Reset")
	}
	if n := s.Len(); n != 0 {
		t.Errorf("expected Len()=0 after Reset, got %d", n)
	}
}

func TestMemoryStore_Len(t *testing.T) {
	s := state.NewMemoryStore()
	ctx := context.Background()

	if n := s.Len(); n != 0 {
		t.Errorf("expected Len()=0 for empty store, got %d", n)
	}

	s.Set(ctx, "ns1", "a", "1")
	s.Set(ctx, "ns2", "b", "2")

	if n := s.Len(); n != 2 {
		t.Errorf("expected Len()=2, got %d", n)
	}
}
