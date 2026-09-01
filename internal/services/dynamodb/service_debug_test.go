package dynamodb

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/state"
)

// TestDebugScanRawDisplay_NumberKey_ShowsRawDecimalNotEncodedForm is the
// regression test for item 3's debugScan requirement (see task notes and
// docs/plans/dynamodb-gsi-design.md §2): once the base-table Number-key
// storage encoding lands, itemBackend.debugScan's HashKey/SortKey fields
// come straight from the storage key — which is now encodeOrderableNumber's
// fixed-width, mostly-non-decimal-looking form for a Number-typed key
// attribute, not the original "50". /_overcast/debug/state/dynamodb:items must keep
// showing the original decimal text; debugScanRawDisplay (service.go) is
// what re-derives it from the item's own attributes via the table schema.
func TestDebugScanRawDisplay_NumberKey_ShowsRawDecimalNotEncodedForm(t *testing.T) {
	ctx := context.Background()
	tables := state.NewMemoryStore()
	items := newMemItemBackend()
	streams := newMemStreamBackend()

	dstore := newDynamoStore(tables, items, streams, "us-east-1")
	svc := &Service{handler: &Handler{store: dstore}}

	table := &Table{
		TableName: "scores",
		KeySchema: []KeySchemaElement{
			{AttributeName: "game", KeyType: "HASH"},
			{AttributeName: "score", KeyType: "RANGE"},
		},
		AttributeDefinitions: []AttributeDef{
			{AttributeName: "game", AttributeType: "S"},
			{AttributeName: "score", AttributeType: "N"},
		},
		TableStatus: "ACTIVE",
	}
	if aerr := dstore.putTable(ctx, table); aerr != nil {
		t.Fatalf("putTable: %v", aerr)
	}

	item := Item{
		"game":  attrValue{"S": "g1"},
		"score": attrValue{"N": "50"},
	}
	if aerr := dstore.putItem(ctx, table, item); aerr != nil {
		t.Fatalf("putItem: %v", aerr)
	}

	// Sanity check: the raw storage key really is now the encoded form, not
	// "50" — otherwise this test wouldn't be exercising the bug it guards.
	rawRecords, err := items.debugScan(ctx)
	if err != nil {
		t.Fatalf("debugScan: %v", err)
	}
	if len(rawRecords) != 1 {
		t.Fatalf("expected 1 raw record, got %d", len(rawRecords))
	}
	if rawRecords[0].SortKey == "50" {
		t.Fatal("test setup invalid: raw storage SortKey is still \"50\" — storage encoding isn't active, this test proves nothing")
	}

	values, err := svc.DebugStateValues(ctx)
	if err != nil {
		t.Fatalf("DebugStateValues: %v", err)
	}
	keys, err := svc.DebugStateKeys(ctx)
	if err != nil {
		t.Fatalf("DebugStateKeys: %v", err)
	}

	// Item rows are keyed by the region-qualified table key
	// (dynamoStore.tableKey — issue #673), so the debug key names the region
	// too. That is deliberate rather than incidental: two same-named tables in
	// different regions now hold different items, and /_overcast/debug/state has to be
	// able to tell them apart.
	const wantKey = "us-east-1/scores/g1/50"
	if _, ok := values[wantKey]; !ok {
		t.Fatalf("expected debug key %q with raw decimal display, got value keys: %v", wantKey, mapKeys(values))
	}
	found := false
	for _, k := range keys {
		if k == wantKey {
			found = true
		}
		if len(k) > 40 {
			t.Errorf("debug key %q looks like it embeds the encoded storage key rather than the raw decimal value", k)
		}
	}
	if !found {
		t.Fatalf("expected %q in DebugStateKeys, got %v", wantKey, keys)
	}
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
