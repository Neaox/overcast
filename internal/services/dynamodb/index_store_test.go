//go:build !nosqlite

// Tests for the GSI index-structure write path (docs/plans/dynamodb-gsi-
// design.md section 3, phase 2): the memory-tree/SQL-table storage added to
// itemBackend, the diffIndexMutations/indexKeyComponents/projectForIndex
// decision logic in index_maintenance.go, and the SQL backend's new
// *sql.Tx-wrapped atomicity. Run against BOTH backends (newTestItemBackends,
// shared with item_store_test.go) so a fix to one can't silently diverge
// from the other.
//
// No read-path handler (scanTyped/queryTyped) consults this structure yet —
// these tests exercise the storage layer and the pure diff/projection
// helpers directly, proving the structure correct in isolation ahead of the
// phase-3 read-path switch (dynamodb-gsi-design.md section 7 phasing).
package dynamodb

import (
	"context"
	"fmt"
	"sort"
	"testing"
)

// ordersTableWithGSIs returns a fixture table used by several tests below:
// hash-only base table (orderId, String), with three GSIs exercising each
// Projection type:
//
//	gsi-all     hash=customerId (S)            Projection: ALL
//	gsi-keys    hash=customerId (S)            Projection: KEYS_ONLY
//	gsi-include hash=customerId (S)            Projection: INCLUDE [status]
//	gsi-score   hash=league (S), sort=score (N) Projection: ALL
func ordersTableWithGSIs() *Table {
	return &Table{
		TableName: "Orders",
		KeySchema: []KeySchemaElement{{AttributeName: "orderId", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{
			{AttributeName: "orderId", AttributeType: "S"},
			{AttributeName: "customerId", AttributeType: "S"},
			{AttributeName: "league", AttributeType: "S"},
			{AttributeName: "score", AttributeType: "N"},
		},
		GlobalSecondaryIndexes: []SecondaryIndex{
			{
				IndexName:  "gsi-all",
				KeySchema:  []KeySchemaElement{{AttributeName: "customerId", KeyType: "HASH"}},
				Projection: Projection{ProjectionType: "ALL"},
			},
			{
				IndexName:  "gsi-keys",
				KeySchema:  []KeySchemaElement{{AttributeName: "customerId", KeyType: "HASH"}},
				Projection: Projection{ProjectionType: "KEYS_ONLY"},
			},
			{
				IndexName:  "gsi-include",
				KeySchema:  []KeySchemaElement{{AttributeName: "customerId", KeyType: "HASH"}},
				Projection: Projection{ProjectionType: "INCLUDE", NonKeyAttributes: []string{"status"}},
			},
			{
				IndexName: "gsi-score",
				KeySchema: []KeySchemaElement{
					{AttributeName: "league", KeyType: "HASH"},
					{AttributeName: "score", KeyType: "RANGE"},
				},
				Projection: Projection{ProjectionType: "ALL"},
			},
		},
	}
}

// putViaWritePath mirrors what dynamoStore.putItemWithIndexMaintenance does,
// against the backend directly (no dynamoStore/Handler involved) — the same
// "call the backend method, not the HTTP handler" convention
// item_store_test.go / item_store_bench_test.go already use.
func putViaWritePath(t *testing.T, ctx context.Context, backend itemBackend, table *Table, oldItem, item Item) {
	t.Helper()
	hashKey, sortKey, aerr := resolveStorageKeys(table, item)
	if aerr != nil {
		t.Fatalf("resolveStorageKeys: %v", aerr)
	}
	mutations := diffIndexMutations(table, oldItem, item)
	if err := backend.putWithIndexMutations(ctx, table.TableName, hashKey, sortKey, item, mutations); err != nil {
		t.Fatalf("putWithIndexMutations: %v", err)
	}
}

// deleteViaWritePath mirrors dynamoStore.deleteItemWithIndexMaintenance.
func deleteViaWritePath(t *testing.T, ctx context.Context, backend itemBackend, table *Table, oldItem, key Item) {
	t.Helper()
	hashKey, sortKey, aerr := resolveStorageKeys(table, key)
	if aerr != nil {
		t.Fatalf("resolveStorageKeys: %v", aerr)
	}
	mutations := diffIndexMutations(table, oldItem, nil)
	if err := backend.removeWithIndexMutations(ctx, table.TableName, hashKey, sortKey, mutations); err != nil {
		t.Fatalf("removeWithIndexMutations: %v", err)
	}
}

// fallbackFilterByIndexHashPresence mirrors today's GSI Scan fallback
// (handler.go scanTyped's else branch): every base-table item that defines
// the index's hash key attribute — the read side of the sparse-index rule
// this design moves to write time (dynamodb-gsi-design.md section 2/3).
func fallbackFilterByIndexHashPresence(items []Item, hashAttrName string) []Item {
	var out []Item
	for _, item := range items {
		if _, ok := item[hashAttrName]; ok {
			out = append(out, item)
		}
	}
	return out
}

// orderIDs extracts and sorts the "orderId" attribute from a set of items,
// for order-independent set comparison in the parity tests below.
func orderIDs(items []Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item["orderId"]["S"].(string))
	}
	sort.Strings(ids)
	return ids
}

func assertStringSlicesEqual(t *testing.T, got, want []string, msg string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", msg, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", msg, got, want)
		}
	}
}

// TestIndexStructure_ParityWithFullScanFallback is dynamodb-gsi-design.md
// section 6's headline correctness check for phase 2: walking the new index
// structure (scanIndexAll — the whole-index GSI Scan shape) returns exactly
// the item set today's full-scan-and-filter-by-key-presence fallback
// returns, for a fixture mixing dense and sparse items, on both backends.
func TestIndexStructure_ParityWithFullScanFallback(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithGSIs()

			items := []Item{
				{"orderId": attrValue{"S": "o1"}, "customerId": attrValue{"S": "c1"}, "status": attrValue{"S": "shipped"}, "amount": attrValue{"S": "9.99"}},
				{"orderId": attrValue{"S": "o2"}, "customerId": attrValue{"S": "c2"}, "status": attrValue{"S": "pending"}, "amount": attrValue{"S": "4.50"}},
				{"orderId": attrValue{"S": "o3"}}, // sparse — no customerId
				{"orderId": attrValue{"S": "o4"}, "customerId": attrValue{"S": "c1"}, "status": attrValue{"S": "returned"}, "amount": attrValue{"S": "1.00"}},
				{"orderId": attrValue{"S": "o5"}}, // sparse — no customerId
			}
			for _, item := range items {
				putViaWritePath(t, ctx, backend, table, nil, item)
			}

			// Fallback: today's Scan-on-a-GSI behavior (handler.go scanTyped)
			// re-derived independently here, against the same source items.
			wantAll := fallbackFilterByIndexHashPresence(items, "customerId")

			gotAll, err := backend.scanIndexAll(ctx, table.TableName, "gsi-all")
			if err != nil {
				t.Fatalf("scanIndexAll: %v", err)
			}
			assertStringSlicesEqual(t, orderIDs(gotAll), orderIDs(wantAll), "gsi-all scanIndexAll vs fallback")

			// Per-hash Query parity: customerId=c1 fallback vs queryIndexByHash.
			var wantC1 []Item
			for _, item := range wantAll {
				if item["customerId"]["S"].(string) == "c1" {
					wantC1 = append(wantC1, item)
				}
			}
			gotC1, err := backend.queryIndexByHash(ctx, table.TableName, "gsi-all", "c1")
			if err != nil {
				t.Fatalf("queryIndexByHash: %v", err)
			}
			assertStringSlicesEqual(t, orderIDs(gotC1), orderIDs(wantC1), "gsi-all queryIndexByHash(c1) vs fallback")

			// No duplicates, no gaps: count matches exactly.
			n, err := backend.countIndexEntries(ctx, table.TableName, "gsi-all")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if int(n) != len(wantAll) {
				t.Fatalf("countIndexEntries(gsi-all) = %d, want %d", n, len(wantAll))
			}
		})
	}
}

// TestIndexStructure_SparseWriteRules is dynamodb-gsi-design.md section 3's
// sparse-index write rule, and section 6's three required cases: absent key
// -> no row; update-removes-key -> row deleted; A->B key change -> exactly
// one old gone, one new present.
func TestIndexStructure_SparseWriteRules(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithGSIs()

			// Case 1: put an item missing the index key -> zero index rows.
			sparse := Item{"orderId": attrValue{"S": "o1"}}
			putViaWritePath(t, ctx, backend, table, nil, sparse)
			n, err := backend.countIndexEntries(ctx, table.TableName, "gsi-all")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if n != 0 {
				t.Fatalf("case 1: expected 0 index rows for a sparse item, got %d", n)
			}

			// Case 1b: a second item that DOES have the key produces exactly
			// one row (not two, not zero) — confirms case 1 wasn't a fluke
			// of an always-empty index.
			dense := Item{"orderId": attrValue{"S": "o2"}, "customerId": attrValue{"S": "c1"}}
			putViaWritePath(t, ctx, backend, table, nil, dense)
			n, err = backend.countIndexEntries(ctx, table.TableName, "gsi-all")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if n != 1 {
				t.Fatalf("case 1b: expected exactly 1 index row after one dense item, got %d", n)
			}

			// Case 2: update removes the previously-present index key ->
			// the stale row is deleted.
			denseNoKey := Item{"orderId": attrValue{"S": "o2"}}
			putViaWritePath(t, ctx, backend, table, dense, denseNoKey)
			n, err = backend.countIndexEntries(ctx, table.TableName, "gsi-all")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if n != 0 {
				t.Fatalf("case 2: expected 0 index rows after removing the indexed attribute, got %d", n)
			}

			// Case 3: A -> B key value change -> exactly one old-key row
			// gone, one new-key row present (no duplicate, no gap).
			putViaWritePath(t, ctx, backend, table, nil, dense) // back to customerId=c1
			changed := Item{"orderId": attrValue{"S": "o2"}, "customerId": attrValue{"S": "c9"}}
			putViaWritePath(t, ctx, backend, table, dense, changed)

			oldRows, err := backend.queryIndexByHash(ctx, table.TableName, "gsi-all", "c1")
			if err != nil {
				t.Fatalf("queryIndexByHash(c1): %v", err)
			}
			if len(oldRows) != 0 {
				t.Fatalf("case 3: expected the old key (c1) to have no rows after the key changed, got %d", len(oldRows))
			}
			newRows, err := backend.queryIndexByHash(ctx, table.TableName, "gsi-all", "c9")
			if err != nil {
				t.Fatalf("queryIndexByHash(c9): %v", err)
			}
			if len(newRows) != 1 {
				t.Fatalf("case 3: expected exactly 1 row at the new key (c9), got %d", len(newRows))
			}
			totalN, err := backend.countIndexEntries(ctx, table.TableName, "gsi-all")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if totalN != 1 {
				t.Fatalf("case 3: expected exactly 1 total index row after the key change (no duplicate left behind), got %d", totalN)
			}
		})
	}
}

// TestIndexStructure_ProjectionStorage is dynamodb-gsi-design.md section 2's
// projection-fidelity fix: KEYS_ONLY/INCLUDE index rows store only the
// projected attribute set, never a base-table attribute outside the
// projection. This is a regression test for a real bug — today's fallback
// (handler.go) reads the full base item and would happily return "amount"
// through a KEYS_ONLY GSI, which real AWS refuses to do.
func TestIndexStructure_ProjectionStorage(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithGSIs()

			item := Item{
				"orderId":    attrValue{"S": "o1"},
				"customerId": attrValue{"S": "c1"},
				"status":     attrValue{"S": "shipped"},
				"amount":     attrValue{"S": "9.99"},
			}
			putViaWritePath(t, ctx, backend, table, nil, item)

			// KEYS_ONLY: table PK (orderId) + index key (customerId) only.
			keysOnly, err := backend.queryIndexByHash(ctx, table.TableName, "gsi-keys", "c1")
			if err != nil {
				t.Fatalf("queryIndexByHash(gsi-keys): %v", err)
			}
			if len(keysOnly) != 1 {
				t.Fatalf("expected 1 gsi-keys row, got %d", len(keysOnly))
			}
			stored := keysOnly[0]
			if len(stored) != 2 {
				t.Fatalf("KEYS_ONLY row has %d attributes, want exactly 2 (orderId, customerId): %#v", len(stored), stored)
			}
			if _, ok := stored["amount"]; ok {
				t.Fatal("KEYS_ONLY row must not store non-key attribute \"amount\"")
			}
			if _, ok := stored["status"]; ok {
				t.Fatal("KEYS_ONLY row must not store non-key attribute \"status\"")
			}

			// INCLUDE [status]: KEYS_ONLY set + status, but not amount.
			included, err := backend.queryIndexByHash(ctx, table.TableName, "gsi-include", "c1")
			if err != nil {
				t.Fatalf("queryIndexByHash(gsi-include): %v", err)
			}
			if len(included) != 1 {
				t.Fatalf("expected 1 gsi-include row, got %d", len(included))
			}
			storedInc := included[0]
			if len(storedInc) != 3 {
				t.Fatalf("INCLUDE row has %d attributes, want exactly 3 (orderId, customerId, status): %#v", len(storedInc), storedInc)
			}
			if _, ok := storedInc["status"]; !ok {
				t.Fatal("INCLUDE[status] row must store the named non-key attribute \"status\"")
			}
			if _, ok := storedInc["amount"]; ok {
				t.Fatal("INCLUDE[status] row must not store un-named non-key attribute \"amount\"")
			}

			// ALL: every base-table attribute duplicated into the index.
			all, err := backend.queryIndexByHash(ctx, table.TableName, "gsi-all", "c1")
			if err != nil {
				t.Fatalf("queryIndexByHash(gsi-all): %v", err)
			}
			if len(all) != 1 {
				t.Fatalf("expected 1 gsi-all row, got %d", len(all))
			}
			if len(all[0]) != len(item) {
				t.Fatalf("ALL row has %d attributes, want %d (every base attribute): %#v", len(all[0]), len(item), all[0])
			}
			if _, ok := all[0]["amount"]; !ok {
				t.Fatal("ALL row must include \"amount\"")
			}
		})
	}
}

// TestIndexStructure_NumericSortKeyOrdersNumerically reuses the numeric-
// encoding's ordering guarantee (numeric_encoding.go/key_order.go) for a
// GSI's own sort key: a Number-typed index sort key must order entries
// numerically (5, 10, 50), not lexicographically ("10" < "5" < "50") — the
// same bug class dynamodb-gsi-design.md section 2 identifies for the base
// table, which a naively-keyed index structure would otherwise reintroduce.
func TestIndexStructure_NumericSortKeyOrdersNumerically(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithGSIs()

			scores := []string{"50", "5", "10", "100", "9"}
			for i, score := range scores {
				item := Item{
					"orderId": attrValue{"S": fmt.Sprintf("o%d", i)},
					"league":  attrValue{"S": "L1"},
					"score":   attrValue{"N": score},
				}
				putViaWritePath(t, ctx, backend, table, nil, item)
			}

			got, err := backend.queryIndexByHash(ctx, table.TableName, "gsi-score", "L1")
			if err != nil {
				t.Fatalf("queryIndexByHash(gsi-score): %v", err)
			}
			if len(got) != len(scores) {
				t.Fatalf("got %d rows, want %d", len(got), len(scores))
			}
			var gotOrder []string
			for _, item := range got {
				gotOrder = append(gotOrder, item["score"]["N"].(string))
			}
			want := []string{"5", "9", "10", "50", "100"}
			for i := range want {
				if gotOrder[i] != want[i] {
					t.Fatalf("index sort-key order = %v, want numeric order %v (lexicographic would give a different order)", gotOrder, want)
				}
			}
		})
	}
}

// TestSQLItemBackend_PutWithIndexMutations_TxRollsBackOnFailure is
// dynamodb-gsi-design.md section 7's risk-flagged focused test: a failure
// partway through applying a write's index mutations must leave neither the
// base item nor any of that write's index rows committed — the *sql.Tx
// this phase adds to sqlItemBackend (previously bare, untransacted
// ExecContext calls) is what makes this true instead of leaving a
// partially-applied write behind.
//
// The failure is induced deterministically: the second index mutation's
// projected item contains a value json.Marshal cannot encode (a channel),
// so applyIndexMutationsTx fails after the first mutation has already been
// executed (but not committed) inside the same transaction as the base item
// write.
func TestSQLItemBackend_PutWithIndexMutations_TxRollsBackOnFailure(t *testing.T) {
	backend, ok := newTestItemBackends(t)["sql"].(*sqlItemBackend)
	if !ok {
		t.Fatal("expected the \"sql\" backend from newTestItemBackends to be a *sqlItemBackend")
	}
	ctx := context.Background()

	mutations := []indexMutation{
		{
			indexName: "gsi-ok", op: indexMutationUpsert,
			indexHash: "h1", baseHash: "b1",
			item: Item{"a": attrValue{"S": "ok"}},
		},
		{
			indexName: "gsi-bad", op: indexMutationUpsert,
			indexHash: "h1", baseHash: "b1",
			item: Item{"bad": attrValue{"X": make(chan int)}}, // json.Marshal fails on this
		},
	}

	err := backend.putWithIndexMutations(ctx, "T", "b1", "", Item{"pk": attrValue{"S": "b1"}}, mutations)
	if err == nil {
		t.Fatal("expected an error from the unmarshalable second index mutation")
	}

	// Then: the base item write in the same transaction must not have
	// committed either — this is the atomicity guarantee, not just "the
	// bad mutation didn't apply."
	if _, found, getErr := backend.get(ctx, "T", "b1", ""); getErr != nil {
		t.Fatalf("get after rollback: %v", getErr)
	} else if found {
		t.Fatal("base item must not be committed when the transaction rolled back")
	}

	// And: the FIRST (individually valid) index mutation must not have
	// committed either — proving this is a real transaction, not
	// statement-by-statement auto-commit.
	n, err := backend.countIndexEntries(ctx, "T", "gsi-ok")
	if err != nil {
		t.Fatalf("countIndexEntries after rollback: %v", err)
	}
	if n != 0 {
		t.Fatalf("gsi-ok index entry must not be committed when the transaction rolled back, got %d rows", n)
	}
}

// TestIndexStructure_DeleteRemovesIndexRows covers the full-item-deletion
// half of the write-amplification table (design section 3: "every GSI whose
// key the old item satisfied gets one index-row delete") at the backend
// level — the sparse-rules test covers key *removal via update*; this covers
// DeleteItem's shape, where newItem is nil and every index row for the item
// must go.
func TestIndexStructure_DeleteRemovesIndexRows(t *testing.T) {
	for name, backend := range newTestItemBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithGSIs()

			dense := Item{"orderId": attrValue{"S": "o1"}, "customerId": attrValue{"S": "c1"}, "status": attrValue{"S": "shipped"}, "amount": attrValue{"S": "9.99"}}
			putViaWritePath(t, ctx, backend, table, nil, dense)

			var before int64
			for _, idx := range table.GlobalSecondaryIndexes {
				n, err := backend.countIndexEntries(ctx, table.TableName, idx.IndexName)
				if err != nil {
					t.Fatalf("countIndexEntries(%s) before: %v", idx.IndexName, err)
				}
				before += n
			}
			if before == 0 {
				t.Fatalf("expected index rows after dense put, got 0")
			}

			key := Item{"orderId": attrValue{"S": "o1"}}
			deleteViaWritePath(t, ctx, backend, table, dense, key)

			for _, idx := range table.GlobalSecondaryIndexes {
				n, err := backend.countIndexEntries(ctx, table.TableName, idx.IndexName)
				if err != nil {
					t.Fatalf("countIndexEntries(%s) after: %v", idx.IndexName, err)
				}
				if n != 0 {
					t.Fatalf("expected 0 index rows in %s after delete, got %d", idx.IndexName, n)
				}
			}
		})
	}
}
