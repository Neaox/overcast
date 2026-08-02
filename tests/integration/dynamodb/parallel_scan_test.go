package dynamodb_test

// Parallel-scan segmentation over the ordered storage structure
// (docs/plans/dynamodb-gsi-design.md §5's deferred follow-up).
//
// AWS's Scan reference defines a segment as a fixed portion of the key
// space: segments are disjoint, together cover the table, and an item's
// segment is a function of that item's own key — not of how many other
// items happen to exist. The pre-fix emulator materialized and sorted the
// whole table, then handed each segment a positional slice of that list,
// which satisfies "disjoint and complete" for a frozen table but breaks the
// moment the table changes: every insert re-slices the list and moves
// existing items between segments. A client running one worker per segment
// against a live table therefore saw items appear in two segments or in
// none.
//
// TestScan_ParallelScan_SegmentAssignmentIsStableAsTableGrows is the
// failing-first test for that property, and
// TestScan_ParallelScan_GSI_ProjectionFidelity is the failing-first test for
// the fidelity gap the old branch carried: a parallel GSI scan read full
// base-table items, so it returned attributes outside the index's own
// projection — the same divergence PR #300 closed for non-parallel GSI
// scans, still open here because segments took the fallback path.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// scanSegment returns the item IDs (attribute "id") a single segment
// returns, following LastEvaluatedKey until the segment is exhausted.
func scanSegment(t *testing.T, srv *helpers.TestServer, tableName, indexName string, segment, totalSegments, limit int) []string {
	t.Helper()
	var ids []string
	var startKey map[string]any
	for page := 0; ; page++ {
		if page > 100 {
			t.Fatalf("segment %d: pagination did not terminate", segment)
		}
		payload := map[string]any{
			"TableName":     tableName,
			"Segment":       segment,
			"TotalSegments": totalSegments,
		}
		if indexName != "" {
			payload["IndexName"] = indexName
		}
		if limit > 0 {
			payload["Limit"] = limit
		}
		if startKey != nil {
			payload["ExclusiveStartKey"] = startKey
		}
		resp := ddbCall(t, srv, "Scan", payload)
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Items            []map[string]any `json:"Items"`
			LastEvaluatedKey map[string]any   `json:"LastEvaluatedKey"`
		}
		helpers.DecodeJSON(t, resp, &result)
		resp.Body.Close()
		for _, it := range result.Items {
			ids = append(ids, it["id"].(map[string]any)["S"].(string))
		}
		if result.LastEvaluatedKey == nil {
			return ids
		}
		startKey = result.LastEvaluatedKey
	}
}

// segmentOf returns which segment contains id, or -1.
func segmentOf(t *testing.T, srv *helpers.TestServer, tableName, id string, totalSegments int) int {
	t.Helper()
	found := -1
	for seg := 0; seg < totalSegments; seg++ {
		for _, got := range scanSegment(t, srv, tableName, "", seg, totalSegments, 0) {
			if got == id {
				if found >= 0 {
					t.Fatalf("item %q appeared in segments %d and %d — segments must be disjoint", id, found, seg)
				}
				found = seg
			}
		}
	}
	return found
}

// TestScan_ParallelScan_SegmentAssignmentIsStableAsTableGrows: an item's
// segment is a function of its own key, so adding unrelated items must not
// move it to a different segment. The old positional-slice implementation
// recomputed segment boundaries from the item count, so growing a 6-item
// table to 8 items moved the 4th item from segment 1 to segment 0.
func TestScan_ParallelScan_SegmentAssignmentIsStableAsTableGrows(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "seg-stability")

	for i := 1; i <= 6; i++ {
		putItem(t, srv, "seg-stability", map[string]any{
			"id": map[string]string{"S": fmt.Sprintf("a%d", i)},
		})
	}

	before := map[string]int{}
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("a%d", i)
		seg := segmentOf(t, srv, "seg-stability", id, 2)
		if seg < 0 {
			t.Fatalf("item %q missing from every segment before growth", id)
		}
		before[id] = seg
	}

	for i := 7; i <= 8; i++ {
		putItem(t, srv, "seg-stability", map[string]any{
			"id": map[string]string{"S": fmt.Sprintf("a%d", i)},
		})
	}

	for id, wantSeg := range before {
		if got := segmentOf(t, srv, "seg-stability", id, 2); got != wantSeg {
			t.Errorf("item %q moved from segment %d to segment %d when unrelated items were added — an item's segment must depend only on its own key", id, wantSeg, got)
		}
	}
}

// TestScan_ParallelScan_GSI_ProjectionFidelity: a parallel GSI scan reads
// the index, so it can only return the index's projected attributes — the
// same rule non-parallel GSI reads already follow.
func TestScan_ParallelScan_GSI_ProjectionFidelity(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "seg-gsi-projection", "by-category", "category", "KEYS_ONLY", nil)

	for i := 1; i <= 6; i++ {
		putItem(t, srv, "seg-gsi-projection", map[string]any{
			"id":       map[string]string{"S": fmt.Sprintf("p%d", i)},
			"category": map[string]string{"S": fmt.Sprintf("cat-%d", i)},
			"secret":   map[string]string{"S": "not-projected"},
		})
	}

	for seg := 0; seg < 2; seg++ {
		resp := ddbCall(t, srv, "Scan", map[string]any{
			"TableName":     "seg-gsi-projection",
			"IndexName":     "by-category",
			"Segment":       seg,
			"TotalSegments": 2,
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Items []map[string]any `json:"Items"`
		}
		helpers.DecodeJSON(t, resp, &result)
		resp.Body.Close()
		for _, it := range result.Items {
			if _, ok := it["secret"]; ok {
				t.Fatalf("segment %d returned non-projected attribute \"secret\" on a KEYS_ONLY GSI: %v", seg, it)
			}
		}
	}
}

// TestScan_ParallelScan_DisjointAndComplete is the core AWS contract, held
// across segment counts and with per-page Limits forcing multi-page walks.
func TestScan_ParallelScan_DisjointAndComplete(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "seg-coverage")

	const itemCount = 25
	want := map[string]bool{}
	for i := 1; i <= itemCount; i++ {
		id := fmt.Sprintf("i%02d", i)
		putItem(t, srv, "seg-coverage", map[string]any{"id": map[string]string{"S": id}})
		want[id] = true
	}

	for _, totalSegments := range []int{1, 2, 3, 7} {
		for _, limit := range []int{0, 2} {
			seen := map[string]bool{}
			for seg := 0; seg < totalSegments; seg++ {
				for _, id := range scanSegment(t, srv, "seg-coverage", "", seg, totalSegments, limit) {
					if seen[id] {
						t.Fatalf("TotalSegments=%d Limit=%d: item %q returned by more than one segment", totalSegments, limit, id)
					}
					seen[id] = true
				}
			}
			if len(seen) != itemCount {
				t.Fatalf("TotalSegments=%d Limit=%d: segments covered %d of %d items", totalSegments, limit, len(seen), itemCount)
			}
			for id := range want {
				if !seen[id] {
					t.Fatalf("TotalSegments=%d Limit=%d: item %q missing from every segment", totalSegments, limit, id)
				}
			}
		}
	}
}

// TestScan_ParallelScan_GSI_DisjointAndComplete: the same contract for a
// parallel scan of a GSI, including the sparse rule (an item without the
// index key is in no segment because it is in no index).
func TestScan_ParallelScan_GSI_DisjointAndComplete(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "seg-gsi-coverage", "by-category", "category", "ALL", nil)

	want := map[string]bool{}
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("p%02d", i)
		putItem(t, srv, "seg-gsi-coverage", map[string]any{
			"id":       map[string]string{"S": id},
			"category": map[string]string{"S": fmt.Sprintf("cat-%d", i%4)},
		})
		want[id] = true
	}
	// Sparse: no "category", so it belongs to no segment of the index.
	putItem(t, srv, "seg-gsi-coverage", map[string]any{"id": map[string]string{"S": "sparse"}})

	for _, totalSegments := range []int{2, 3} {
		seen := map[string]bool{}
		for seg := 0; seg < totalSegments; seg++ {
			for _, id := range scanSegment(t, srv, "seg-gsi-coverage", "by-category", seg, totalSegments, 2) {
				if seen[id] {
					t.Fatalf("TotalSegments=%d: item %q returned by more than one segment", totalSegments, id)
				}
				seen[id] = true
			}
		}
		if seen["sparse"] {
			t.Fatalf("TotalSegments=%d: item without the index key must not appear in a GSI parallel scan", totalSegments)
		}
		if len(seen) != len(want) {
			t.Fatalf("TotalSegments=%d: segments covered %d of %d indexed items: %v", totalSegments, len(seen), len(want), seen)
		}
	}
}

// TestScan_ParallelScan_LSI_DisjointAndComplete: LSI parallel scans keep the
// full-scan fallback (there is no LSI index storage), but must use the same
// key-based segment assignment and the same sparse rule.
func TestScan_ParallelScan_LSI_DisjointAndComplete(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithLSI(t, srv, "seg-lsi-coverage")

	want := map[string]bool{}
	for i := 1; i <= 8; i++ {
		postID := fmt.Sprintf("p%02d", i)
		putItem(t, srv, "seg-lsi-coverage", map[string]any{
			"forumId":   map[string]string{"S": fmt.Sprintf("f%d", i%3)},
			"postId":    map[string]string{"S": postID},
			"createdAt": map[string]string{"S": fmt.Sprintf("2026-01-%02d", i)},
		})
		want[postID] = true
	}
	putItem(t, srv, "seg-lsi-coverage", map[string]any{
		"forumId": map[string]string{"S": "f0"},
		"postId":  map[string]string{"S": "sparse"}, // no createdAt
	})

	seen := map[string]bool{}
	for seg := 0; seg < 3; seg++ {
		resp := ddbCall(t, srv, "Scan", map[string]any{
			"TableName":     "seg-lsi-coverage",
			"IndexName":     "by-created",
			"Segment":       seg,
			"TotalSegments": 3,
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Items []map[string]any `json:"Items"`
		}
		helpers.DecodeJSON(t, resp, &result)
		resp.Body.Close()
		for _, it := range result.Items {
			id := it["postId"].(map[string]any)["S"].(string)
			if seen[id] {
				t.Fatalf("item %q returned by more than one LSI segment", id)
			}
			seen[id] = true
		}
	}
	if seen["sparse"] {
		t.Fatal("item without the LSI sort key must not appear in an LSI parallel scan")
	}
	if len(seen) != len(want) {
		t.Fatalf("LSI segments covered %d of %d indexed items: %v", len(seen), len(want), seen)
	}
}
