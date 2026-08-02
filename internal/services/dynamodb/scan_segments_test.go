// Storage-level properties of parallel-scan segmentation
// (docs/plans/dynamodb-gsi-design.md §5's follow-up, implemented in
// scan_segments.go). The wire-level behaviour lives in
// tests/integration/dynamodb/parallel_scan_test.go; what is checked here is
// the walk itself, against every backend this build provides, so a fix to
// one backend cannot silently diverge from the other — the convention
// index_store_test.go established.
package dynamodb

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func segmentScanTable() *Table {
	return &Table{
		TableName: "segmented",
		KeySchema: []KeySchemaElement{{AttributeName: "id", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{
			{AttributeName: "id", AttributeType: "S"},
			{AttributeName: "category", AttributeType: "S"},
		},
		GlobalSecondaryIndexes: []SecondaryIndex{
			{
				IndexName:  "gsi-category",
				KeySchema:  []KeySchemaElement{{AttributeName: "category", KeyType: "HASH"}},
				Projection: Projection{ProjectionType: "ALL"},
			},
		},
	}
}

// seedSegmentScanItems writes n items, each with its own id and a category
// spread over a handful of values (so the GSI has several partitions).
func seedSegmentScanItems(t *testing.T, store *dynamoStore, table *Table, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id%03d", i)
		item := Item{
			"id":       attrValue{"S": id},
			"category": attrValue{"S": fmt.Sprintf("cat-%d", i%5)},
		}
		if aerr := store.putItemWithIndexMaintenance(ctx, table, item, nil); aerr != nil {
			t.Fatalf("putItemWithIndexMaintenance: %v", aerr)
		}
		ids = append(ids, id)
	}
	return ids
}

// walkSegment pages one segment to exhaustion via the same cursor round trip
// the handler performs (LastEvaluatedKey shape in, storage cursor out).
func walkSegment(t *testing.T, store *dynamoStore, table *Table, idx *SecondaryIndex, segment, totalSegments, limit int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	var startKey Item
	for page := 0; ; page++ {
		if page > 500 {
			t.Fatalf("segment %d: pagination did not terminate", segment)
		}
		var items []Item
		var hasMore bool
		var aerr any
		if idx == nil {
			i, m, e := store.scanItemsSegmentPage(ctx, table, startKey, limit, segment, totalSegments)
			items, hasMore = i, m
			if e != nil {
				aerr = e
			}
		} else {
			i, m, e := store.scanIndexSegmentPage(ctx, table, idx, startKey, limit, segment, totalSegments)
			items, hasMore = i, m
			if e != nil {
				aerr = e
			}
		}
		if aerr != nil {
			t.Fatalf("segment page: %v", aerr)
		}
		for _, item := range items {
			ids = append(ids, extractKeyValue(item["id"]))
		}
		if !hasMore || len(items) == 0 {
			return ids
		}
		last := items[len(items)-1]
		if idx == nil {
			startKey = extractItemKeys(last, table)
		} else {
			startKey = extractItemKeysWithIndex(last, table, idx)
		}
	}
}

// TestScanSegmentPage_DisjointAndComplete is AWS's core segment contract:
// every item lands in exactly one segment, and the segments together cover
// the table — held across segment counts and page sizes, so a boundary
// landing mid-page cannot drop or repeat an item.
func TestScanSegmentPage_DisjointAndComplete(t *testing.T) {
	for name, store := range newTestDynamoStores(t) {
		t.Run(name, func(t *testing.T) {
			table := segmentScanTable()
			if aerr := store.putTable(context.Background(), table); aerr != nil {
				t.Fatalf("putTable: %v", aerr)
			}
			want := seedSegmentScanItems(t, store, table, 60)

			for _, totalSegments := range []int{1, 2, 3, 8} {
				for _, limit := range []int{1, 7, 1000} {
					seen := map[string]bool{}
					for seg := 0; seg < totalSegments; seg++ {
						for _, id := range walkSegment(t, store, table, nil, seg, totalSegments, limit) {
							if seen[id] {
								t.Fatalf("TotalSegments=%d limit=%d: %q returned twice", totalSegments, limit, id)
							}
							seen[id] = true
						}
					}
					if len(seen) != len(want) {
						t.Fatalf("TotalSegments=%d limit=%d: covered %d of %d items", totalSegments, limit, len(seen), len(want))
					}
					for _, id := range want {
						if !seen[id] {
							t.Fatalf("TotalSegments=%d limit=%d: %q missing from every segment", totalSegments, limit, id)
						}
					}
				}
			}
		})
	}
}

// TestScanIndexSegmentPage_DisjointAndComplete is the same contract over a
// GSI's ordered index structure, where the segment follows the index's
// partition key rather than the table's.
func TestScanIndexSegmentPage_DisjointAndComplete(t *testing.T) {
	for name, store := range newTestDynamoStores(t) {
		t.Run(name, func(t *testing.T) {
			table := segmentScanTable()
			if aerr := store.putTable(context.Background(), table); aerr != nil {
				t.Fatalf("putTable: %v", aerr)
			}
			want := seedSegmentScanItems(t, store, table, 40)
			idx := table.findIndex("gsi-category")

			for _, totalSegments := range []int{2, 3, 5} {
				for _, limit := range []int{2, 1000} {
					seen := map[string]bool{}
					for seg := 0; seg < totalSegments; seg++ {
						for _, id := range walkSegment(t, store, table, idx, seg, totalSegments, limit) {
							if seen[id] {
								t.Fatalf("TotalSegments=%d limit=%d: %q returned twice", totalSegments, limit, id)
							}
							seen[id] = true
						}
					}
					if len(seen) != len(want) {
						t.Fatalf("TotalSegments=%d limit=%d: covered %d of %d indexed items", totalSegments, limit, len(seen), len(want))
					}
				}
			}
		})
	}
}

// TestScanSegmentPage_SparseItemsAreNotInAnyIndexSegment: an item missing
// the index key was never written to the index, so no segment of an index
// scan can produce it.
func TestScanSegmentPage_SparseItemsAreNotInAnyIndexSegment(t *testing.T) {
	for name, store := range newTestDynamoStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := segmentScanTable()
			if aerr := store.putTable(ctx, table); aerr != nil {
				t.Fatalf("putTable: %v", aerr)
			}
			seedSegmentScanItems(t, store, table, 10)
			if aerr := store.putItemWithIndexMaintenance(ctx, table, Item{"id": attrValue{"S": "sparse"}}, nil); aerr != nil {
				t.Fatalf("putItemWithIndexMaintenance: %v", aerr)
			}
			idx := table.findIndex("gsi-category")

			for seg := 0; seg < 3; seg++ {
				for _, id := range walkSegment(t, store, table, idx, seg, 3, 4) {
					if id == "sparse" {
						t.Fatalf("segment %d returned the sparse item, which is not in the index", seg)
					}
				}
			}
		})
	}
}

// TestScanSegmentPage_TerminatesOnItemsWithoutResolvableKeys: the walk seeks
// past each row using that row's own storage key, so a row that cannot
// produce one gives it nothing to advance past. Without an explicit
// no-progress guard the same chunk is fetched forever and the request hangs
// — the worst possible failure mode for one corrupt record, and the
// opposite of what CLAUDE.md's isolation rule asks for. Rows are written
// through the backend directly because the item write path validates keys
// and would reject them, which is also why this state should never occur in
// practice.
func TestScanSegmentPage_TerminatesOnItemsWithoutResolvableKeys(t *testing.T) {
	for name, store := range newTestDynamoStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := segmentScanTable()
			if aerr := store.putTable(ctx, table); aerr != nil {
				t.Fatalf("putTable: %v", aerr)
			}
			// More than one chunk's worth of rows stored without the table's
			// key attribute (the walk below asks for limit*totalSegments =
			// 100 per fetch), so the first fetch comes back full and yields
			// no cursor at all — the shape that spins.
			for i := 0; i < 150; i++ {
				keyless := Item{"payload": attrValue{"S": fmt.Sprintf("v%03d", i)}}
				if err := store.items.put(ctx, table.TableName, fmt.Sprintf("k%03d", i), "", keyless); err != nil {
					t.Fatalf("put: %v", err)
				}
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				//nolint:errcheck // the assertion is that this returns at all
				_, _, _ = store.scanItemsSegmentPage(ctx, table, nil, 25, 0, 4)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("scanItemsSegmentPage did not terminate on rows whose keys cannot be resolved")
			}
		})
	}
}

// TestSegmentForKey_StableAndInRange pins the two properties the walk relies
// on: membership depends only on the key (so a growing table never
// reassigns an item), and the result is always a valid segment index.
func TestSegmentForKey_StableAndInRange(t *testing.T) {
	for _, totalSegments := range []int{1, 2, 3, 16, 1000} {
		for i := 0; i < 200; i++ {
			key := fmt.Sprintf("partition-%d", i)
			got := segmentForKey(key, totalSegments)
			if got < 0 || got >= totalSegments {
				t.Fatalf("segmentForKey(%q, %d) = %d, out of range", key, totalSegments, got)
			}
			if again := segmentForKey(key, totalSegments); again != got {
				t.Fatalf("segmentForKey(%q, %d) not deterministic: %d then %d", key, totalSegments, got, again)
			}
		}
	}
}

// TestSegmentForKey_SpreadsKeysAcrossSegments guards against a hash choice
// that degenerates for the sequential and prefixed key shapes emulator
// workloads use — segments need not be equal, but none of them may be
// starved, or a parallel scan buys the client nothing.
func TestSegmentForKey_SpreadsKeysAcrossSegments(t *testing.T) {
	const (
		keys          = 1000
		totalSegments = 4
	)
	counts := make([]int, totalSegments)
	for i := 0; i < keys; i++ {
		counts[segmentForKey(fmt.Sprintf("id%04d", i), totalSegments)]++
	}
	// A uniform split is 250 per segment; anything above half of that is a
	// working spread, and far below what a degenerate hash would produce.
	for seg, n := range counts {
		if n < keys/totalSegments/2 {
			t.Errorf("segment %d got %d of %d keys — hash does not spread sequential keys (all counts: %v)", seg, n, keys, counts)
		}
	}
}
