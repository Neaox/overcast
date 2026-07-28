// Tests for TransactWriteItems' GSI index-row maintenance
// (docs/plans/dynamodb-gsi-design.md §3, phase 3 work item 1). PutItem,
// UpdateItem, DeleteItem, and BatchWriteItem all route through
// dynamoStore.putItemWithIndexMaintenance/deleteItemWithIndexMaintenance
// already; TransactWriteItems' Put/Delete/Update branches historically
// called the plain putItem/deleteItem instead, silently skipping index-row
// maintenance for every transactional write — a gap that would have let
// reads flip onto the new index structure (phase-3 work item 2) while
// TransactWriteItems writes quietly went stale in the index.
//
// oldItem is already fetched unconditionally in transactWriteItemsTyped's
// phase 1 (needed for ConditionExpression evaluation regardless of GSIs), so
// wiring diffIndexMutations through costs zero extra reads — these tests
// assert the effect (index rows appear/move/disappear), not the read count.
//
// Run against every backend this build provides (newTestDynamoStores) so a
// fix to one can't silently diverge from the other, matching
// index_store_test.go's convention.
package dynamodb

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/state"
)

// newTestDynamoStores returns one dynamoStore per backend pairing available
// in this build, wired the same way service.go wires production stores
// (newItemBackendFor/newStreamBackendFor keyed off the same underlying
// state.Store) — see item_store_test.go's newTestItemBackends for the
// itemBackend-only analogue this generalizes, including its nosqlite
// behavior: "sql" is only present when SQLite is compiled in, and the map
// degrades to memory-only otherwise.
func newTestDynamoStores(t *testing.T) map[string]*dynamoStore {
	t.Helper()

	stores := map[string]*dynamoStore{
		"memory": newDynamoStore(state.NewMemoryStore(), newMemItemBackend(), newMemStreamBackend(), "us-east-1"),
	}
	if !config.SQLiteSupported() {
		return stores
	}

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
	stores["sql"] = newDynamoStore(sqlStore, newItemBackendFor(sqlStore), newStreamBackendFor(sqlStore), "us-east-1")

	return stores
}

// ordersTableWithStatusGSI is a minimal single-GSI fixture for the tests
// below: hash-only base table (orderId), one GSI (gsi-status, hash=status,
// ALL projection) so assertions can check the full stored item without
// separately exercising projection narrowing (that's index_store_test.go's
// job).
func ordersTableWithStatusGSI() *Table {
	return &Table{
		TableName: "orders",
		KeySchema: []KeySchemaElement{{AttributeName: "orderId", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{
			{AttributeName: "orderId", AttributeType: "S"},
			{AttributeName: "status", AttributeType: "S"},
		},
		GlobalSecondaryIndexes: []SecondaryIndex{
			{
				IndexName:  "gsi-status",
				KeySchema:  []KeySchemaElement{{AttributeName: "status", KeyType: "HASH"}},
				Projection: Projection{ProjectionType: "ALL"},
			},
		},
	}
}

func newTestTransactHandler(store *dynamoStore) *Handler {
	return &Handler{store: store, bus: events.NewBus()}
}

// TestTransactWriteItems_Put_MaintainsGSIIndex is the "transact-put dense
// item appears in the index structure" case from the phase-3 task's item 1.
func TestTransactWriteItems_Put_MaintainsGSIIndex(t *testing.T) {
	for name, store := range newTestDynamoStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithStatusGSI()
			if aerr := store.putTable(ctx, table); aerr != nil {
				t.Fatalf("putTable: %v", aerr)
			}
			h := newTestTransactHandler(store)

			req := &transactWriteItemsRequest{
				TransactItems: []transactWriteItem{
					{Put: &transactPut{
						TableName: table.TableName,
						Item: Item{
							"orderId": attrValue{"S": "o1"},
							"status":  attrValue{"S": "shipped"},
							"amount":  attrValue{"S": "9.99"},
						},
					}},
				},
			}
			if _, aerr := h.transactWriteItemsTyped(ctx, req); aerr != nil {
				t.Fatalf("transactWriteItemsTyped: %v", aerr)
			}

			n, err := store.items.countIndexEntries(ctx, table.TableName, "gsi-status")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if n != 1 {
				t.Fatalf("countIndexEntries(gsi-status) = %d, want 1 — TransactWriteItems Put did not maintain the GSI index", n)
			}

			got, err := store.items.queryIndexByHash(ctx, table.TableName, "gsi-status", "shipped")
			if err != nil {
				t.Fatalf("queryIndexByHash: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("queryIndexByHash(gsi-status, shipped) = %d items, want 1", len(got))
			}
			if amt := got[0]["amount"]["S"]; amt != "9.99" {
				t.Fatalf("indexed row amount = %v, want 9.99 (ALL projection should store the full item)", amt)
			}
		})
	}
}

// TestTransactWriteItems_Delete_RemovesGSIIndexRow is the "transact-delete
// removes rows" case. The item is put via the already-tested normal write
// path (putItemWithIndexMaintenance) so this test isolates the delete-path
// wiring specifically.
func TestTransactWriteItems_Delete_RemovesGSIIndexRow(t *testing.T) {
	for name, store := range newTestDynamoStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithStatusGSI()
			if aerr := store.putTable(ctx, table); aerr != nil {
				t.Fatalf("putTable: %v", aerr)
			}
			item := Item{
				"orderId": attrValue{"S": "o1"},
				"status":  attrValue{"S": "shipped"},
			}
			if aerr := store.putItemWithIndexMaintenance(ctx, table, item, nil); aerr != nil {
				t.Fatalf("seed putItemWithIndexMaintenance: %v", aerr)
			}
			if n, err := store.items.countIndexEntries(ctx, table.TableName, "gsi-status"); err != nil || n != 1 {
				t.Fatalf("seed: countIndexEntries = %d, err %v, want 1 (test setup invalid)", n, err)
			}

			h := newTestTransactHandler(store)
			req := &transactWriteItemsRequest{
				TransactItems: []transactWriteItem{
					{Delete: &transactDelete{
						TableName: table.TableName,
						Key:       Item{"orderId": attrValue{"S": "o1"}},
					}},
				},
			}
			if _, aerr := h.transactWriteItemsTyped(ctx, req); aerr != nil {
				t.Fatalf("transactWriteItemsTyped: %v", aerr)
			}

			n, err := store.items.countIndexEntries(ctx, table.TableName, "gsi-status")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if n != 0 {
				t.Fatalf("countIndexEntries(gsi-status) = %d, want 0 — TransactWriteItems Delete left a stale index row", n)
			}
		})
	}
}

// TestTransactWriteItems_Update_MovesIndexKey is the "transact-update moving
// the index key swaps rows" case: an UpdateExpression that changes the GSI's
// key attribute (status: pending -> shipped) must delete the old-key index
// row and insert a new one — not leave a stale row at the old key or two
// rows for one item.
func TestTransactWriteItems_Update_MovesIndexKey(t *testing.T) {
	for name, store := range newTestDynamoStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			table := ordersTableWithStatusGSI()
			if aerr := store.putTable(ctx, table); aerr != nil {
				t.Fatalf("putTable: %v", aerr)
			}
			item := Item{
				"orderId": attrValue{"S": "o1"},
				"status":  attrValue{"S": "pending"},
			}
			if aerr := store.putItemWithIndexMaintenance(ctx, table, item, nil); aerr != nil {
				t.Fatalf("seed putItemWithIndexMaintenance: %v", aerr)
			}

			h := newTestTransactHandler(store)
			req := &transactWriteItemsRequest{
				TransactItems: []transactWriteItem{
					{Update: &transactUpdate{
						TableName:                table.TableName,
						Key:                      Item{"orderId": attrValue{"S": "o1"}},
						UpdateExpression:         "SET #s = :v",
						ExpressionAttributeNames: map[string]string{"#s": "status"},
						ExpressionAttributeValues: map[string]attrValue{
							":v": {"S": "shipped"},
						},
					}},
				},
			}
			if _, aerr := h.transactWriteItemsTyped(ctx, req); aerr != nil {
				t.Fatalf("transactWriteItemsTyped: %v", aerr)
			}

			oldRows, err := store.items.queryIndexByHash(ctx, table.TableName, "gsi-status", "pending")
			if err != nil {
				t.Fatalf("queryIndexByHash(pending): %v", err)
			}
			if len(oldRows) != 0 {
				t.Fatalf("queryIndexByHash(gsi-status, pending) = %d rows, want 0 — stale old-key row left behind", len(oldRows))
			}

			newRows, err := store.items.queryIndexByHash(ctx, table.TableName, "gsi-status", "shipped")
			if err != nil {
				t.Fatalf("queryIndexByHash(shipped): %v", err)
			}
			if len(newRows) != 1 {
				t.Fatalf("queryIndexByHash(gsi-status, shipped) = %d rows, want 1", len(newRows))
			}

			n, err := store.items.countIndexEntries(ctx, table.TableName, "gsi-status")
			if err != nil {
				t.Fatalf("countIndexEntries: %v", err)
			}
			if n != 1 {
				t.Fatalf("countIndexEntries(gsi-status) = %d, want 1 total row across both keys", n)
			}
		})
	}
}
