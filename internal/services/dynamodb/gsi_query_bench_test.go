//go:build !nosqlite

// Benchmarks the phase-3 read-path flip's acceptance criterion
// (docs/plans/dynamodb-gsi-design.md §6): a GSI Query for one index-hash
// value stays flat (allocs/op) as *total table size* grows, because
// queryTyped's IndexName branch is now a partition-scoped read into the
// GSI's own ordered index structure (scanIndexByHash) instead of a full
// table scan + in-memory hash filter — the exact base-table Query vs. GSI
// Query cost-parity gap §1 of the design doc identifies.
//
// Convention matches item_store_bench_test.go: preload happens before
// b.ResetTimer via the backend's write path directly (putItemWithIndexMaintenance,
// not the HTTP handler), b.ReportAllocs() is the signal, and the benchmarked
// call (dynamoStore.scanIndexByHash) is exactly what queryTyped's GSI branch
// calls in handler.go.
//
// Preload spreads items across many *distinct* index-hash values so the
// queried hash's own group stays fixed at 25 items regardless of table
// size — the "flat curve" property being measured: a Limit=25-shaped GSI
// Query only ever touches its own partition's ~25 rows, never the other
// (preExisting-25) items sitting under different hash values elsewhere in
// the same index.
package dynamodb

import (
	"context"
	"fmt"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

const gsiQueryBenchTargetGroupSize = 25

func gsiQueryBenchTable() *Table {
	return &Table{
		TableName: "bench-gsi-query",
		KeySchema: []KeySchemaElement{{AttributeName: "orderId", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{
			{AttributeName: "orderId", AttributeType: "S"},
			{AttributeName: "customerId", AttributeType: "S"},
		},
		GlobalSecondaryIndexes: []SecondaryIndex{
			{
				IndexName:  "gsi-customer",
				KeySchema:  []KeySchemaElement{{AttributeName: "customerId", KeyType: "HASH"}},
				Projection: Projection{ProjectionType: "ALL"},
			},
		},
	}
}

// benchmarkGSIQueryByHash preloads preExisting items into store: the first
// gsiQueryBenchTargetGroupSize items share customerId "target-customer" (the
// group the benchmark repeatedly queries), and the rest each get their own
// distinct customerId — one item per hash value — so table size grows
// without growing the queried partition.
func benchmarkGSIQueryByHash(b *testing.B, store *dynamoStore, preExisting int) {
	b.Helper()
	ctx := context.Background()
	table := gsiQueryBenchTable()
	if aerr := store.putTable(ctx, table); aerr != nil {
		b.Fatalf("putTable: %v", aerr)
	}
	idx := table.findIndex("gsi-customer")

	targetGroupSize := preExisting
	if targetGroupSize > gsiQueryBenchTargetGroupSize {
		targetGroupSize = gsiQueryBenchTargetGroupSize
	}
	for i := 0; i < preExisting; i++ {
		customerID := "target-customer"
		if i >= targetGroupSize {
			customerID = fmt.Sprintf("other-customer-%08d", i)
		}
		item := Item{
			"orderId":    attrValue{"S": fmt.Sprintf("o%08d", i)},
			"customerId": attrValue{"S": customerID},
			"payload":    attrValue{"S": "some-attribute-value"},
		}
		if aerr := store.putItemWithIndexMaintenance(ctx, table, item, nil); aerr != nil {
			b.Fatalf("preload putItemWithIndexMaintenance: %v", aerr)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, aerr := store.scanIndexByHash(ctx, table, idx, "target-customer")
		if aerr != nil {
			b.Fatalf("scanIndexByHash: %v", aerr)
		}
		if len(items) != targetGroupSize {
			b.Fatalf("scanIndexByHash returned %d items, want %d", len(items), targetGroupSize)
		}
	}
}

func newMemDynamoStoreForBench() *dynamoStore {
	return newDynamoStore(state.NewMemoryStore(), newMemItemBackend(), newMemStreamBackend(), "us-east-1")
}

func newSQLDynamoStoreForBench(b *testing.B) *dynamoStore {
	b.Helper()
	dir := b.TempDir()
	sqlStore, err := state.NewSQLiteStore(dir)
	if err != nil {
		b.Fatalf("state.NewSQLiteStore: %v", err)
	}
	b.Cleanup(func() {
		if err := sqlStore.Close(); err != nil {
			b.Logf("sqlStore.Close: %v", err)
		}
	})
	return newDynamoStore(sqlStore, newItemBackendFor(sqlStore), newStreamBackendFor(sqlStore), "us-east-1")
}

func BenchmarkDynamoDB_GSIQueryPageLimit25_Memory_0Items(b *testing.B) {
	benchmarkGSIQueryByHash(b, newMemDynamoStoreForBench(), 0)
}

func BenchmarkDynamoDB_GSIQueryPageLimit25_Memory_2000Items(b *testing.B) {
	benchmarkGSIQueryByHash(b, newMemDynamoStoreForBench(), 2000)
}

func BenchmarkDynamoDB_GSIQueryPageLimit25_Memory_8000Items(b *testing.B) {
	benchmarkGSIQueryByHash(b, newMemDynamoStoreForBench(), 8000)
}

// SQL preload sizes are smaller than the memory backend's, matching
// item_store_bench_test.go's own rationale: putItemWithIndexMaintenance has
// no bulk-insert path (one base-item write + one index-row write per call,
// each its own statement, matching the production write path exactly), so
// preloading thousands of rows one at a time through modernc.org/sqlite is
// itself slow — that setup cost is deliberately not counted (b.ResetTimer
// runs after preload), but it still has to complete before the timed loop
// starts.
func BenchmarkDynamoDB_GSIQueryPageLimit25_SQL_0Items(b *testing.B) {
	benchmarkGSIQueryByHash(b, newSQLDynamoStoreForBench(b), 0)
}

func BenchmarkDynamoDB_GSIQueryPageLimit25_SQL_500Items(b *testing.B) {
	benchmarkGSIQueryByHash(b, newSQLDynamoStoreForBench(b), 500)
}

func BenchmarkDynamoDB_GSIQueryPageLimit25_SQL_1500Items(b *testing.B) {
	benchmarkGSIQueryByHash(b, newSQLDynamoStoreForBench(b), 1500)
}
