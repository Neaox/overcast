package dynamodb

// Perf guard for the base-table Number-key encoding retrofit (item 6 of the
// numeric-key-order fix): resolveStorageKeys (store.go) adds an encoding
// step ahead of every put/get/delete/scanPage-cursor call, but only for
// Number-typed key components — a String-keyed table's hash/sort key goes
// through keyAttrType's AttributeDefinitions lookup (a short slice scan) and
// then straight past encodeStorageKeyComponent's `typ != "N"` fast path
// unchanged. These benchmarks put that claim in allocs/op terms: a
// String-keyed PutItem/GetItem should show no meaningful allocation
// difference from what putItem/getItem already cost before this PR (there is
// no "before" binary left to compare against directly, so the comparison
// here is String-keyed vs Number-keyed at the same call sites — the
// Number-keyed path pays for encodeOrderableNumber's regexp match + string
// building; the String-keyed path should not).
//
// Convention follows item_store_bench_test.go: b.ReportAllocs() is the
// signal (not wall-clock ns/op, noisy on a shared dev machine), preload
// happens outside the timed loop.

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/state"
)

func benchmarkPutItem(b *testing.B, table *Table, item Item) {
	b.Helper()
	ctx := context.Background()
	dstore := newDynamoStore(state.NewMemoryStore(), newMemItemBackend(), newMemStreamBackend(), "us-east-1")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if aerr := dstore.putItem(ctx, table, item); aerr != nil {
			b.Fatalf("putItem: %v", aerr)
		}
	}
}

var stringKeyedTable = &Table{
	TableName: "bench-string",
	KeySchema: []KeySchemaElement{
		{AttributeName: "pk", KeyType: "HASH"},
		{AttributeName: "sk", KeyType: "RANGE"},
	},
	AttributeDefinitions: []AttributeDef{
		{AttributeName: "pk", AttributeType: "S"},
		{AttributeName: "sk", AttributeType: "S"},
	},
}

var numberKeyedTable = &Table{
	TableName: "bench-number",
	KeySchema: []KeySchemaElement{
		{AttributeName: "pk", KeyType: "HASH"},
		{AttributeName: "sk", KeyType: "RANGE"},
	},
	AttributeDefinitions: []AttributeDef{
		{AttributeName: "pk", AttributeType: "S"},
		{AttributeName: "sk", AttributeType: "N"},
	},
}

func BenchmarkDynamoDB_PutItem_StringKey(b *testing.B) {
	benchmarkPutItem(b, stringKeyedTable, Item{
		"pk":      attrValue{"S": "h1"},
		"sk":      attrValue{"S": "some-sort-value"},
		"payload": attrValue{"S": "some-attribute-value"},
	})
}

func BenchmarkDynamoDB_PutItem_NumberKey(b *testing.B) {
	benchmarkPutItem(b, numberKeyedTable, Item{
		"pk":      attrValue{"S": "h1"},
		"sk":      attrValue{"N": "12345.6789"},
		"payload": attrValue{"S": "some-attribute-value"},
	})
}
