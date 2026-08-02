package dynamodb_test

// Tests for the LSI Query routing fix (docs/plans/dynamodb-gsi-design.md §5
// follow-up): an LSI shares the base table's hash key by definition, so an
// LSI Query is a partition-scoped read (the same O(k) primitive base-table
// Query uses) filtered by the LSI's sort key — not a full-table scan.
//
// The sparse-index tests are the behavioral anchor: real AWS only tracks
// items in an LSI when the LSI's sort key attribute exists on the item, so a
// hash-only LSI Query (no sort-key condition) must exclude items missing
// that attribute. The pre-fix full-scan fallback only filtered on hash-key
// presence (always true for an LSI — it IS the table's hash key), so these
// tests FAIL against the old routing and pass with the fix.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// createTableWithLSI creates a composite-key table (forumId, postId) with a
// single LSI on (forumId, createdAt), projection ALL.
func createTableWithLSI(t *testing.T, srv *helpers.TestServer, tableName string) {
	t.Helper()
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": tableName,
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "forumId", "AttributeType": "S"},
			{"AttributeName": "postId", "AttributeType": "S"},
			{"AttributeName": "createdAt", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "forumId", "KeyType": "HASH"},
			{"AttributeName": "postId", "KeyType": "RANGE"},
		},
		"LocalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "by-created",
				"KeySchema": []map[string]any{
					{"AttributeName": "forumId", "KeyType": "HASH"},
					{"AttributeName": "createdAt", "KeyType": "RANGE"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := helpers.ReadBody(t, resp)
		t.Fatalf("createTableWithLSI %q: status %d: %s", tableName, resp.StatusCode, body)
	}
}

// TestQuery_LSI_SparseIndex_ExcludesItemsMissingSortKey: an item without the
// LSI's sort key attribute is not in the LSI at all, so a hash-only Query on
// the LSI must not return it ("A local secondary index only tracks data
// items where its key attributes actually exist" — same sparse rule GSIs
// have, and the LSI sort key is the only key attribute an item can lack).
func TestQuery_LSI_SparseIndex_ExcludesItemsMissingSortKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithLSI(t, srv, "lsi-sparse-query")

	putItem(t, srv, "lsi-sparse-query", map[string]any{
		"forumId":   map[string]string{"S": "f1"},
		"postId":    map[string]string{"S": "p1"},
		"createdAt": map[string]string{"S": "2026-01-01"},
	})
	putItem(t, srv, "lsi-sparse-query", map[string]any{
		"forumId": map[string]string{"S": "f1"},
		"postId":  map[string]string{"S": "p2"}, // no createdAt — absent from the LSI
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "lsi-sparse-query",
		"IndexName":              "by-created",
		"KeyConditionExpression": "forumId = :f",
		"ExpressionAttributeValues": map[string]any{
			":f": map[string]string{"S": "f1"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Fatalf("expected 1 item (p2 lacks the LSI sort key, so it is not in the index), got %d: %v", result.Count, result.Items)
	}
	if got := result.Items[0]["postId"].(map[string]any)["S"].(string); got != "p1" {
		t.Fatalf("expected p1, got %q", got)
	}
}

// TestScan_LSI_SparseIndex_ExcludesItemsMissingSortKey: same sparse rule for
// Scan with the LSI's IndexName — the index simply does not contain items
// missing its sort key.
func TestScan_LSI_SparseIndex_ExcludesItemsMissingSortKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithLSI(t, srv, "lsi-sparse-scan")

	putItem(t, srv, "lsi-sparse-scan", map[string]any{
		"forumId":   map[string]string{"S": "f1"},
		"postId":    map[string]string{"S": "p1"},
		"createdAt": map[string]string{"S": "2026-01-01"},
	})
	putItem(t, srv, "lsi-sparse-scan", map[string]any{
		"forumId": map[string]string{"S": "f1"},
		"postId":  map[string]string{"S": "p2"}, // no createdAt — absent from the LSI
	})

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "lsi-sparse-scan",
		"IndexName": "by-created",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Fatalf("expected 1 item (p2 lacks the LSI sort key, so it is not in the index), got %d: %v", result.Count, result.Items)
	}
}

// TestQuery_LSI_PartitionScopedAndOrderedBySortKey pins the semantics the
// routing fix must preserve: only the queried partition's items come back,
// ordered by the LSI's sort key (not the table's), including with
// ScanIndexForward=false.
func TestQuery_LSI_PartitionScopedAndOrderedBySortKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithLSI(t, srv, "lsi-ordering")

	// Insert with postId order deliberately opposite createdAt order, so a
	// result ordered by the table's sort key would be detected.
	for i, created := range []string{"2026-03-03", "2026-01-01", "2026-02-02"} {
		putItem(t, srv, "lsi-ordering", map[string]any{
			"forumId":   map[string]string{"S": "f1"},
			"postId":    map[string]string{"S": fmt.Sprintf("p%d", i+1)},
			"createdAt": map[string]string{"S": created},
		})
	}
	putItem(t, srv, "lsi-ordering", map[string]any{
		"forumId":   map[string]string{"S": "f2"},
		"postId":    map[string]string{"S": "px"},
		"createdAt": map[string]string{"S": "2026-01-15"},
	})

	query := func(forward *bool) []string {
		t.Helper()
		payload := map[string]any{
			"TableName":              "lsi-ordering",
			"IndexName":              "by-created",
			"KeyConditionExpression": "forumId = :f",
			"ExpressionAttributeValues": map[string]any{
				":f": map[string]string{"S": "f1"},
			},
		}
		if forward != nil {
			payload["ScanIndexForward"] = *forward
		}
		resp := ddbCall(t, srv, "Query", payload)
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Items []map[string]any `json:"Items"`
		}
		helpers.DecodeJSON(t, resp, &result)
		ids := make([]string, 0, len(result.Items))
		for _, it := range result.Items {
			ids = append(ids, it["postId"].(map[string]any)["S"].(string))
		}
		return ids
	}

	got := query(nil)
	want := []string{"p2", "p3", "p1"} // createdAt ascending
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("LSI query order: got %v, want %v (createdAt ascending, f2 excluded)", got, want)
	}

	reverse := false
	got = query(&reverse)
	want = []string{"p1", "p3", "p2"} // createdAt descending
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("LSI query order (ScanIndexForward=false): got %v, want %v", got, want)
	}
}
