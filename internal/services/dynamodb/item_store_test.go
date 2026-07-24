//go:build !nosqlite

// Tests for itemBackend.scanPage (storage-access-plan.md A3 / pagination-plan.md
// G2). Run against BOTH backends — memItemBackend and a real SQLite-backed
// sqlItemBackend — so a fix to one implementation can't silently diverge from
// the other (item_store.go's interface contract is meant to be identical).
package dynamodb

import (
	"context"
	"fmt"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

// newTestItemBackends returns one instance per itemBackend implementation,
// keyed by a short label used in t.Run subtests.
func newTestItemBackends(t *testing.T) map[string]itemBackend {
	t.Helper()

	dir := t.TempDir()
	sqlStore, err := state.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("state.NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlStore.Close(); err != nil {
			t.Logf("sqlStore.Close: %v", err)
		}
	})

	sqlBackend := newItemBackendFor(sqlStore)
	if _, ok := sqlBackend.(*sqlItemBackend); !ok {
		t.Fatalf("expected newItemBackendFor(sqlStore) to select sqlItemBackend, got %T", sqlBackend)
	}

	return map[string]itemBackend{
		"memory": newMemItemBackend(),
		"sql":    sqlBackend,
	}
}

// TestItemBackend_ScanPage_ParityWithScanAll is storage-access-plan.md A3's
// headline correctness check: walking scanPage to exhaustion — on both
// backends — must return exactly the same item set scanAll returns, with no
// duplicates and no gaps, regardless of page size.
func TestItemBackend_ScanPage_ParityWithScanAll(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const table = "parity-table"
			const n = 7

			// Given: n items spread across a few hash keys, each item
			// carrying its own (hk, sk) attributes so the walk below can
			// derive the next cursor without external bookkeeping — exactly
			// how the real handler derives LastEvaluatedKey from an item.
			want := map[string]bool{}
			for i := 0; i < n; i++ {
				hk := fmt.Sprintf("h%d", i%3)
				sk := fmt.Sprintf("s%02d", i)
				id := fmt.Sprintf("item-%d", i)
				item := Item{
					"hk": attrValue{"S": hk},
					"sk": attrValue{"S": sk},
					"id": attrValue{"S": id},
				}
				if err := backend.put(ctx, table, hk, sk, item); err != nil {
					t.Fatalf("put: %v", err)
				}
				want[id] = true
			}

			// When: walking scanPage with a page size smaller than the table
			for _, pageSize := range []int{1, 2, 3, 100} {
				t.Run(fmt.Sprintf("pageSize=%d", pageSize), func(t *testing.T) {
					got := map[string]bool{}
					hasAfter := false
					var afterHash, afterSort string
					for page := 0; page < n+2; page++ {
						items, err := backend.scanPage(ctx, table, hasAfter, afterHash, afterSort, pageSize)
						if err != nil {
							t.Fatalf("scanPage: %v", err)
						}
						if len(items) == 0 {
							break
						}
						if len(items) > pageSize {
							t.Fatalf("scanPage returned %d items, more than the requested page size %d", len(items), pageSize)
						}
						for _, item := range items {
							id := item["id"]["S"].(string)
							if got[id] {
								t.Fatalf("duplicate item %s delivered across pages", id)
							}
							got[id] = true
						}
						last := items[len(items)-1]
						afterHash = last["hk"]["S"].(string)
						afterSort = last["sk"]["S"].(string)
						hasAfter = true
					}

					// Then: the walk covers exactly the same items as scanAll — no dup, no gap.
					if len(got) != len(want) {
						t.Fatalf("scanPage walk returned %d distinct items, want %d", len(got), len(want))
					}
					for id := range want {
						if !got[id] {
							t.Errorf("scanPage walk missed item %s", id)
						}
					}
				})
			}

			all, err := backend.scanAll(ctx, table)
			if err != nil {
				t.Fatalf("scanAll: %v", err)
			}
			if len(all) != n {
				t.Fatalf("scanAll returned %d items, want %d", len(all), n)
			}
		})
	}
}

// TestItemBackend_ScanPage_CursorSurvivesDeletedItem is pagination-plan.md
// G2's headline test at the storage layer: a cursor naming an item that has
// since been deleted must still resolve to the correct resume position — not
// restart from the beginning (the old itemKeysEqual-based behavior) and not
// skip the items that would have sorted immediately after it.
func TestItemBackend_ScanPage_CursorSurvivesDeletedItem(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const table = "delete-survives"

			// Given: 5 items, hash-only table, keys h0..h4 in order.
			for i := 0; i < 5; i++ {
				hk := fmt.Sprintf("h%d", i)
				item := Item{"hk": attrValue{"S": hk}, "id": attrValue{"S": fmt.Sprintf("item-%d", i)}}
				if err := backend.put(ctx, table, hk, "", item); err != nil {
					t.Fatalf("put: %v", err)
				}
			}

			// When: page 1 returns h0, h1.
			page1, err := backend.scanPage(ctx, table, false, "", "", 2)
			if err != nil {
				t.Fatalf("page1: %v", err)
			}
			if len(page1) != 2 {
				t.Fatalf("page1: want 2 items, got %d", len(page1))
			}
			cursorHash := page1[1]["hk"]["S"].(string) // "h1"

			// And: the cursor item (h1) is deleted before page 2 is fetched.
			if err := backend.remove(ctx, table, cursorHash, ""); err != nil {
				t.Fatalf("remove cursor item: %v", err)
			}

			// And: page 2 resumes from the now-deleted cursor.
			page2, err := backend.scanPage(ctx, table, true, cursorHash, "", 10)
			if err != nil {
				t.Fatalf("page2: %v", err)
			}

			// Then: no duplicates and no gap across the two pages. item-1 was
			// already delivered on page1 (before it was deleted) — deleting
			// the "last returned item" after the client has it does not make
			// it vanish from the result set already handed out; it just
			// means the *next* page must not use identity to find where to
			// resume. All 5 items are seen exactly once in total.
			seen := map[string]bool{}
			for _, item := range page1 {
				seen[item["id"]["S"].(string)] = true
			}
			for _, item := range page2 {
				id := item["id"]["S"].(string)
				if seen[id] {
					t.Errorf("duplicate item %s delivered on both pages", id)
				}
				seen[id] = true
			}
			if len(seen) != 5 {
				t.Fatalf("expected all 5 items delivered exactly once across both pages, got %d: %v", len(seen), seen)
			}
			if len(page2) != 3 {
				t.Errorf("expected page2 to contain h2,h3,h4 (3 items) — a silent restart would instead re-deliver h0 — got %d", len(page2))
			}
		})
	}
}

// TestItemBackend_ScanPage_EmptyTable checks the boundary: an empty table
// (or a nonexistent one) returns an empty page, not an error.
func TestItemBackend_ScanPage_EmptyTable(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			items, err := backend.scanPage(ctx, "no-such-table", false, "", "", 10)
			if err != nil {
				t.Fatalf("scanPage on empty/nonexistent table: %v", err)
			}
			if len(items) != 0 {
				t.Errorf("expected 0 items, got %d", len(items))
			}
		})
	}
}
