//go:build !nosqlite

// Benchmarks the write-amplification risk dynamodb-gsi-design.md section 7
// flags explicitly: "A table with N GSIs now pays up to 2N extra index
// writes per base-table write... worth a benchmark specifically for it
// (PutItem/UpdateItem ns/op and allocs/op vs. GSI count, at fixed item
// size)." This is a guard, not a flat-curve proof like
// item_store_bench_test.go's Scan benchmarks (that property belongs to
// phase 3's read-path benchmarks, out of scope here) — it exists so a
// future regression that makes index maintenance quadratic-ish in GSI count
// shows up as a step change in allocs/op across these three data points.
//
// Convention matches item_store_bench_test.go: preload/setup happens before
// b.ResetTimer, b.ReportAllocs() is the signal (not wall-clock), and the
// benchmark drives the backend directly — no HTTP handler — but the timed
// loop reproduces exactly the write-path shape
// dynamoStore.putItemWithIndexMaintenance uses (fetch old item, diff,
// write), not just a raw backend call, so the benchmark reflects what
// PutItem actually pays per call.
package dynamodb

import (
	"context"
	"fmt"
	"testing"
)

func benchmarkPutItemPathByGSICount(b *testing.B, gsiCount int) {
	b.Helper()
	ctx := context.Background()
	backend := newMemItemBackend()

	table := &Table{
		TableName:            "bench-put-gsi",
		KeySchema:            []KeySchemaElement{{AttributeName: "pk", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{{AttributeName: "pk", AttributeType: "S"}},
	}
	item := Item{"pk": attrValue{"S": "k1"}, "payload": attrValue{"S": "some-attribute-value"}}
	for i := 0; i < gsiCount; i++ {
		attrName := fmt.Sprintf("gsi%d_pk", i)
		table.AttributeDefinitions = append(table.AttributeDefinitions, AttributeDef{AttributeName: attrName, AttributeType: "S"})
		table.GlobalSecondaryIndexes = append(table.GlobalSecondaryIndexes, SecondaryIndex{
			IndexName:  fmt.Sprintf("gsi%d", i),
			KeySchema:  []KeySchemaElement{{AttributeName: attrName, KeyType: "HASH"}},
			Projection: Projection{ProjectionType: "ALL"},
		})
		item[attrName] = attrValue{"S": fmt.Sprintf("v%d", i)}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Same shape as dynamoStore.putItemWithIndexMaintenance: fetch old
		// item (present from the previous iteration onward), diff against
		// the new value, write base item + index rows atomically.
		oldItem, _, err := backend.get(ctx, table.TableName, "k1", "")
		if err != nil {
			b.Fatalf("get: %v", err)
		}
		hashKey, sortKey, aerr := resolveStorageKeys(table, item)
		if aerr != nil {
			b.Fatalf("resolveStorageKeys: %v", aerr)
		}
		mutations := diffIndexMutations(table, oldItem, item)
		if err := backend.putWithIndexMutations(ctx, table.TableName, hashKey, sortKey, item, mutations); err != nil {
			b.Fatalf("putWithIndexMutations: %v", err)
		}
	}
}

func BenchmarkDynamoDB_PutItemPath_GSICount_Memory_0(b *testing.B) {
	benchmarkPutItemPathByGSICount(b, 0)
}

func BenchmarkDynamoDB_PutItemPath_GSICount_Memory_1(b *testing.B) {
	benchmarkPutItemPathByGSICount(b, 1)
}

func BenchmarkDynamoDB_PutItemPath_GSICount_Memory_3(b *testing.B) {
	benchmarkPutItemPathByGSICount(b, 3)
}
