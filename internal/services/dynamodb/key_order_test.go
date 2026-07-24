package dynamodb

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

func TestKeyAttrType(t *testing.T) {
	table := &Table{
		AttributeDefinitions: []AttributeDef{
			{AttributeName: "pk", AttributeType: "S"},
			{AttributeName: "score", AttributeType: "N"},
			{AttributeName: "blob", AttributeType: "B"},
		},
	}
	cases := []struct {
		name string
		want string
	}{
		{"pk", "S"},
		{"score", "N"},
		{"blob", "B"},
		{"undeclared", "S"}, // defensive default
		{"", "S"},
	}
	for _, c := range cases {
		if got := keyAttrType(table, c.name); got != c.want {
			t.Errorf("keyAttrType(table, %q) = %q, want %q", c.name, got, c.want)
		}
	}
	if got := keyAttrType(nil, "pk"); got != "S" {
		t.Errorf("keyAttrType(nil, \"pk\") = %q, want \"S\"", got)
	}
}

func TestCompareKeyAttr_NumberType_NumericOrder(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign
	}{
		{"5", "10", -1},
		{"10", "5", 1},
		{"5", "5", 0},
		{"5", "5.0", 0}, // equal representations must compare equal, not just order
		{"-10", "-5", -1},
		{"0.5", "0.05", 1},
	}
	for _, c := range cases {
		got := compareKeyAttr("N", c.a, c.b)
		if sign3(got) != c.want {
			t.Errorf("compareKeyAttr(N, %q, %q) sign = %d, want %d", c.a, c.b, sign3(got), c.want)
		}
	}
}

func TestCompareKeyAttr_StringType_PlainStringOrder(t *testing.T) {
	// String-typed keys must NOT go through numeric encoding, even when the
	// raw text happens to look numeric — "10" < "5" lexicographically is
	// correct S-type behavior, unlike the N-type case above.
	if got := compareKeyAttr("S", "10", "5"); got >= 0 {
		t.Errorf("compareKeyAttr(S, \"10\", \"5\") = %d, want < 0 (lexicographic)", got)
	}
}

func TestCompareKeyAttr_MalformedNumber_FallsBackToStringCompare(t *testing.T) {
	// A malformed persisted N value must not panic or error the comparator —
	// CLAUDE.md's isolation rule: degrade to string compare instead.
	got := compareKeyAttr("N", "not-a-number", "also-not-a-number")
	want := compareKeyAttr("S", "not-a-number", "also-not-a-number")
	if got != want {
		t.Errorf("compareKeyAttr(N, malformed...) = %d, want fallback string-compare result %d", got, want)
	}
}

// TestResolveStorageKeys_EqualNumberRepresentations_CollapseToSameStorageRow
// is the storage-layer half of the "equal representations" requirement:
// writing sort key "5" then "5.0" for the same hash key must overwrite the
// same item (as real DynamoDB does — Number keys compare by numeric value
// for equality too), not create two separate rows.
func TestResolveStorageKeys_EqualNumberRepresentations_CollapseToSameStorageRow(t *testing.T) {
	ctx := context.Background()
	dstore := newDynamoStore(state.NewMemoryStore(), newMemItemBackend(), newMemStreamBackend(), "us-east-1")

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
	}

	if aerr := dstore.putItem(ctx, table, Item{"game": attrValue{"S": "g1"}, "score": attrValue{"N": "5"}}); aerr != nil {
		t.Fatalf("putItem(score=5): %v", aerr)
	}
	if aerr := dstore.putItem(ctx, table, Item{"game": attrValue{"S": "g1"}, "score": attrValue{"N": "5.0"}, "note": attrValue{"S": "overwritten"}}); aerr != nil {
		t.Fatalf("putItem(score=5.0): %v", aerr)
	}

	items, aerr := dstore.scanItems(ctx, "scores")
	if aerr != nil {
		t.Fatalf("scanItems: %v", aerr)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item (5 and 5.0 are the same key), got %d: %#v", len(items), items)
	}
	if note, ok := items[0]["note"]; !ok || note["S"] != "overwritten" {
		t.Errorf("expected the second write (score=5.0) to have overwritten the first, got %#v", items[0])
	}
}

// TestScanItemsByHashKey_NumberHashKey_EncodesLookup is the base-table Query
// hash+sort path's own encoding requirement: scanItemsByHashKey's raw hashVal
// argument must be encoded before it's used as the storage lookup key when
// the table's hash key is Number-typed, or a Query on such a table would
// silently return zero results after putItem started storing encoded keys.
func TestScanItemsByHashKey_NumberHashKey_EncodesLookup(t *testing.T) {
	ctx := context.Background()
	dstore := newDynamoStore(state.NewMemoryStore(), newMemItemBackend(), newMemStreamBackend(), "us-east-1")

	table := &Table{
		TableName: "leaderboard",
		KeySchema: []KeySchemaElement{
			{AttributeName: "rank", KeyType: "HASH"},
			{AttributeName: "player", KeyType: "RANGE"},
		},
		AttributeDefinitions: []AttributeDef{
			{AttributeName: "rank", AttributeType: "N"},
			{AttributeName: "player", AttributeType: "S"},
		},
	}
	if aerr := dstore.putItem(ctx, table, Item{"rank": attrValue{"N": "1"}, "player": attrValue{"S": "alice"}}); aerr != nil {
		t.Fatalf("putItem: %v", aerr)
	}

	items, aerr := dstore.scanItemsByHashKey(ctx, table, "1")
	if aerr != nil {
		t.Fatalf("scanItemsByHashKey: %v", aerr)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item for hash key \"1\", got %d", len(items))
	}
}
