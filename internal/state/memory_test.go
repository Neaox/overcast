package state_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Neaox/overcast/internal/state"
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

func TestMemoryStore_ListNamespaces(t *testing.T) {
	// Given: keys exist across multiple namespaces.
	s := state.NewMemoryStore()
	ctx := context.Background()
	if err := s.Set(ctx, "sqs:queues", "orders", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "appsync", "us-east-1/api:abc", "{}"); err != nil {
		t.Fatal(err)
	}

	// When: namespaces are listed.
	namespaces, err := s.ListNamespaces(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Then: only populated namespaces are returned in deterministic order.
	want := []string{"appsync", "sqs:queues"}
	if !reflect.DeepEqual(namespaces, want) {
		t.Fatalf("expected namespaces %#v, got %#v", want, namespaces)
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

// ---- 3.2 ScanPage ------------------------------------------------------

func TestMemoryStore_ScanPage_PaginatesFullRangeNoDuplicatesOrGaps(t *testing.T) {
	s := state.NewMemoryStore()
	assertScanPagePaginatesFullRange(t, s, "ns", "queue/", 37, 5)
}

func TestMemoryStore_ScanPage_StartAfterBoundary(t *testing.T) {
	s := state.NewMemoryStore()
	ctx := context.Background()
	seedScanPageKeys(t, s, "ns", "queue/", 5)

	// startAfter == the last key of a previous page must exclude that key.
	page, next, err := s.ScanPage(ctx, "ns", "queue/", "queue/0002", 0)
	if err != nil {
		t.Fatalf("ScanPage: %v", err)
	}
	if next != "" {
		t.Fatalf("nextKey = %q, want empty (limit 0 = unlimited)", next)
	}
	wantKeys := []string{"queue/0003", "queue/0004"}
	if got := scanPageKeys(page); !equalStringSlices(got, wantKeys) {
		t.Fatalf("ScanPage after queue/0002 = %v, want %v", got, wantKeys)
	}

	// startAfter beyond every key returns an empty page.
	page, next, err = s.ScanPage(ctx, "ns", "queue/", "queue/9999", 0)
	if err != nil {
		t.Fatalf("ScanPage: %v", err)
	}
	if len(page) != 0 || next != "" {
		t.Fatalf("ScanPage past the end = %v (next=%q), want empty", page, next)
	}
}

func TestMemoryStore_ScanPage_EmptyNamespace(t *testing.T) {
	s := state.NewMemoryStore()
	page, next, err := s.ScanPage(context.Background(), "does-not-exist", "", "", 10)
	if err != nil {
		t.Fatalf("ScanPage: %v", err)
	}
	if page == nil {
		t.Error("ScanPage should return an empty slice, not nil, for a missing namespace")
	}
	if len(page) != 0 || next != "" {
		t.Fatalf("ScanPage on empty namespace = %v (next=%q), want empty", page, next)
	}
}

func TestMemoryStore_ScanPage_LimitEdgeCases(t *testing.T) {
	s := state.NewMemoryStore()
	ctx := context.Background()
	seedScanPageKeys(t, s, "ns", "queue/", 3)

	t.Run("limit zero means unlimited", func(t *testing.T) {
		page, next, err := s.ScanPage(ctx, "ns", "queue/", "", 0)
		if err != nil {
			t.Fatalf("ScanPage: %v", err)
		}
		if len(page) != 3 || next != "" {
			t.Fatalf("ScanPage limit=0 = %d items (next=%q), want all 3 items and no nextKey", len(page), next)
		}
	})

	t.Run("negative limit means unlimited", func(t *testing.T) {
		page, next, err := s.ScanPage(ctx, "ns", "queue/", "", -1)
		if err != nil {
			t.Fatalf("ScanPage: %v", err)
		}
		if len(page) != 3 || next != "" {
			t.Fatalf("ScanPage limit=-1 = %d items (next=%q), want all 3 items and no nextKey", len(page), next)
		}
	})

	t.Run("limit larger than dataset", func(t *testing.T) {
		page, next, err := s.ScanPage(ctx, "ns", "queue/", "", 1000)
		if err != nil {
			t.Fatalf("ScanPage: %v", err)
		}
		if len(page) != 3 || next != "" {
			t.Fatalf("ScanPage limit=1000 over 3 items = %d items (next=%q), want all 3 and no nextKey", len(page), next)
		}
	})
}

// ---- shared ScanPage test helpers, used across MemoryStore, SQLiteStore,
// WALStore, and NamespacedStore's contract tests --------------------------

// seedScanPageKeys writes n sequential, zero-padded keys under prefix so
// their lexical and insertion order match.
func seedScanPageKeys(t *testing.T, s state.Store, namespace, prefix string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		key := prefix + zeroPad4(i)
		if err := s.Set(ctx, namespace, key, "v"); err != nil {
			t.Fatalf("seed Set %s: %v", key, err)
		}
	}
}

func zeroPad4(i int) string {
	digits := "0123456789"
	b := make([]byte, 4)
	for pos := 3; pos >= 0; pos-- {
		b[pos] = digits[i%10]
		i /= 10
	}
	return string(b)
}

func scanPageKeys(page []state.KV) []string {
	keys := make([]string, len(page))
	for i, kv := range page {
		keys[i] = kv.Key
	}
	return keys
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertScanPagePaginatesFullRange seeds n sequential keys under prefix,
// pages through the whole range in pageSize-sized calls, and asserts every
// key is returned exactly once, in order, with nextKey empty only on the
// final page. This is the core pagination contract every Store
// implementation's ScanPage must satisfy.
func assertScanPagePaginatesFullRange(t *testing.T, s state.Store, namespace, prefix string, n, pageSize int) {
	t.Helper()
	ctx := context.Background()
	seedScanPageKeys(t, s, namespace, prefix, n)

	var got []string
	startAfter := ""
	pages := 0
	for {
		pages++
		if pages > n+1 {
			t.Fatalf("ScanPage did not terminate after %d pages (n=%d)", pages, n)
		}
		page, next, err := s.ScanPage(ctx, namespace, prefix, startAfter, pageSize)
		if err != nil {
			t.Fatalf("ScanPage(startAfter=%q): %v", startAfter, err)
		}
		if pageSize > 0 && len(page) > pageSize {
			t.Fatalf("ScanPage returned %d items, want at most limit=%d", len(page), pageSize)
		}
		for _, kv := range page {
			got = append(got, kv.Key)
		}
		if next == "" {
			break
		}
		startAfter = next
	}

	want := make([]string, n)
	for i := 0; i < n; i++ {
		want[i] = prefix + zeroPad4(i)
	}
	if !equalStringSlices(got, want) {
		t.Fatalf("paginated ScanPage over %d keys (page size %d) = %v\nwant %v", n, pageSize, got, want)
	}
}
