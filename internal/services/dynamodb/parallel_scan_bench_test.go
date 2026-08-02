//go:build !nosqlite

// Benchmarks the parallel-scan segmentation follow-up
// (docs/plans/dynamodb-gsi-design.md §5): one segment's page of a
// TotalSegments>1 Scan should cost what a page costs, not what the table
// costs. The pre-change implementation read every item in the table, sorted
// the whole set in Go, then handed the segment a positional slice — so a
// Limit=25 page grew with table size, and an S-way parallel scan did S times
// that work. After the change each segment is a bounded keyset walk over the
// storage structure's existing key order.
//
// Deliberately benchmarked at the handler entry point (scanTyped) rather
// than the storage call: the claim being measured is what a client's Scan
// costs, and the sort and full materialization being removed lived in the
// handler. This also lets the same benchmark run unchanged against the
// pre-change code to produce the "before" column.
//
// Convention follows item_store_bench_test.go and gsi_query_bench_test.go:
// preload happens before b.ResetTimer through the production write path,
// b.ReportAllocs() is the signal rather than wall-clock, and SQL preload
// sizes are smaller because each preloaded item is its own statement.
package dynamodb

import (
	"context"
	"fmt"
	"testing"

	"github.com/Neaox/overcast/internal/events"
)

const parallelScanBenchSegments = 4

func parallelScanBenchTable() *Table {
	return &Table{
		TableName: "bench-parallel-scan",
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

// benchmarkParallelScanSegment times one Limit=25 page of segment 0 of a
// 4-way parallel Scan against a table preloaded with preExisting items. When
// indexName is set the scan is over the GSI instead of the base table.
func benchmarkParallelScanSegment(b *testing.B, store *dynamoStore, preExisting int, indexName string) {
	b.Helper()
	ctx := context.Background()
	table := parallelScanBenchTable()
	if aerr := store.putTable(ctx, table); aerr != nil {
		b.Fatalf("putTable: %v", aerr)
	}
	for i := 0; i < preExisting; i++ {
		item := Item{
			"id":       attrValue{"S": fmt.Sprintf("id%08d", i)},
			"category": attrValue{"S": fmt.Sprintf("cat-%04d", i%64)},
			"payload":  attrValue{"S": "some-attribute-value"},
		}
		if aerr := store.putItemWithIndexMaintenance(ctx, table, item, nil); aerr != nil {
			b.Fatalf("preload putItemWithIndexMaintenance: %v", aerr)
		}
	}

	h := &Handler{store: store, bus: events.NewBus()}
	req := &scanRequest{
		TableName:     table.TableName,
		IndexName:     indexName,
		Segment:       0,
		TotalSegments: parallelScanBenchSegments,
		Limit:         25,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, aerr := h.scanTyped(ctx, req); aerr != nil {
			b.Fatalf("scanTyped: %v", aerr)
		}
	}
}

func BenchmarkDynamoDB_ParallelScanSegmentLimit25_Memory_0Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newMemDynamoStoreForBench(), 0, "")
}

func BenchmarkDynamoDB_ParallelScanSegmentLimit25_Memory_2000Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newMemDynamoStoreForBench(), 2000, "")
}

func BenchmarkDynamoDB_ParallelScanSegmentLimit25_Memory_8000Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newMemDynamoStoreForBench(), 8000, "")
}

func BenchmarkDynamoDB_ParallelScanSegmentLimit25_SQL_0Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newSQLDynamoStoreForBench(b), 0, "")
}

func BenchmarkDynamoDB_ParallelScanSegmentLimit25_SQL_500Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newSQLDynamoStoreForBench(b), 500, "")
}

func BenchmarkDynamoDB_ParallelScanSegmentLimit25_SQL_1500Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newSQLDynamoStoreForBench(b), 1500, "")
}

func BenchmarkDynamoDB_ParallelScanGSISegmentLimit25_Memory_2000Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newMemDynamoStoreForBench(), 2000, "gsi-category")
}

func BenchmarkDynamoDB_ParallelScanGSISegmentLimit25_Memory_8000Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newMemDynamoStoreForBench(), 8000, "gsi-category")
}

func BenchmarkDynamoDB_ParallelScanGSISegmentLimit25_SQL_1500Items(b *testing.B) {
	benchmarkParallelScanSegment(b, newSQLDynamoStoreForBench(b), 1500, "gsi-category")
}
