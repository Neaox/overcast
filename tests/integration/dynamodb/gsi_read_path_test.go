package dynamodb_test

// Tests for the GSI read-path flip (docs/plans/dynamodb-gsi-design.md §4,
// phase 3 work items 2-4): Query's IndexName branch and Scan's GSI branch
// (no parallel segments) now read from the GSI's own ordered index
// structure instead of scanning and filtering the whole base table.
//
// TestQuery_GSI_KeysOnlyProjection_ExcludesBaseAttributes and
// TestScan_GSI_KeysOnlyProjection_ExcludesBaseAttributes are the
// projection-fidelity regression tests design §2/§6 calls for: they were
// confirmed to FAIL against the pre-flip full-scan-and-filter fallback
// (which reads the full base item and has nothing else narrowing what it
// returns) and PASS once the flip lands (the index row physically never
// contains attributes outside its Projection — see
// index_maintenance.go's projectForIndex).
//
// The cursor tests generalize pagination-plan.md G2's "cursor survives
// deletion" property to the GSI's 4-tuple index cursor
// (indexHash, indexSort, baseHash, baseSort — design §3/§4): one deletes the
// exact last-returned item (the classic G2 case), the other deletes a
// different item that shares the cursor item's index key (a case unique to
// indexes, since index keys are not required to be unique across items).

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// createTableWithGSI creates a hash-only base table (attribute "id") plus a
// single GSI whose hash key is attrName, with the given projectionType
// ("ALL", "KEYS_ONLY", or "INCLUDE" — nonKeyAttrs only matters for INCLUDE).
func createTableWithGSI(t *testing.T, srv *helpers.TestServer, tableName, indexName, attrName, projectionType string, nonKeyAttrs []string) {
	t.Helper()
	projection := map[string]any{"ProjectionType": projectionType}
	if len(nonKeyAttrs) > 0 {
		projection["NonKeyAttributes"] = nonKeyAttrs
	}
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": tableName,
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
			{"AttributeName": attrName, "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"GlobalSecondaryIndexes": []map[string]any{
			{
				"IndexName":  indexName,
				"KeySchema":  []map[string]any{{"AttributeName": attrName, "KeyType": "HASH"}},
				"Projection": projection,
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := helpers.ReadBody(t, resp)
		t.Fatalf("createTableWithGSI %q: status %d: %s", tableName, resp.StatusCode, body)
	}
}

func deleteItemByID(t *testing.T, srv *helpers.TestServer, tableName, id string) {
	t.Helper()
	resp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName": tableName,
		"Key":       map[string]any{"id": map[string]string{"S": id}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := helpers.ReadBody(t, resp)
		t.Fatalf("deleteItemByID %q/%q: status %d: %s", tableName, id, resp.StatusCode, body)
	}
}

// ---- Projection fidelity (design §2/§6) ------------------------------------

func TestQuery_GSI_KeysOnlyProjection_ExcludesBaseAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "products", "by-category", "category", "KEYS_ONLY", nil)

	putItem(t, srv, "products", map[string]any{
		"id":       map[string]string{"S": "p1"},
		"category": map[string]string{"S": "electronics"},
		"secret":   map[string]string{"S": "not-projected"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "products",
		"IndexName":              "by-category",
		"KeyConditionExpression": "category = :c",
		"ExpressionAttributeValues": map[string]any{
			":c": map[string]string{"S": "electronics"},
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
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
	item := result.Items[0]
	if _, ok := item["secret"]; ok {
		t.Fatalf("KEYS_ONLY GSI Query returned a non-projected attribute %q: %v — real AWS cannot fetch attributes from the parent table on a GSI read", "secret", item)
	}
	if _, ok := item["id"]; !ok {
		t.Error("expected table primary key \"id\" to be present (KEYS_ONLY always includes table + index keys)")
	}
	if _, ok := item["category"]; !ok {
		t.Error("expected index hash key \"category\" to be present (KEYS_ONLY always includes table + index keys)")
	}
}

func TestScan_GSI_KeysOnlyProjection_ExcludesBaseAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "products-scan", "by-category", "category", "KEYS_ONLY", nil)

	putItem(t, srv, "products-scan", map[string]any{
		"id":       map[string]string{"S": "p1"},
		"category": map[string]string{"S": "electronics"},
		"secret":   map[string]string{"S": "not-projected"},
	})

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "products-scan",
		"IndexName": "by-category",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Count != 1 {
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
	if _, ok := result.Items[0]["secret"]; ok {
		t.Fatalf("KEYS_ONLY GSI Scan returned a non-projected attribute \"secret\": %v", result.Items[0])
	}
}

func TestQuery_GSI_IncludeProjection_ReturnsOnlyKeysPlusIncluded(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "orders-inc", "by-status", "status", "INCLUDE", []string{"amount"})

	putItem(t, srv, "orders-inc", map[string]any{
		"id":       map[string]string{"S": "o1"},
		"status":   map[string]string{"S": "shipped"},
		"amount":   map[string]string{"S": "9.99"},
		"internal": map[string]string{"S": "not-included"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "orders-inc",
		"IndexName":              "by-status",
		"KeyConditionExpression": "#s = :v",
		"ExpressionAttributeNames": map[string]string{
			"#s": "status",
		},
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]string{"S": "shipped"},
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
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
	item := result.Items[0]
	if _, ok := item["internal"]; ok {
		t.Fatalf("INCLUDE GSI Query returned a non-included attribute \"internal\": %v", item)
	}
	if _, ok := item["amount"]; !ok {
		t.Error("expected INCLUDE-projected attribute \"amount\" to be present")
	}
}

// ---- Partition-scoped correctness (design §4) ------------------------------

func TestQuery_GSI_PartitionScoped_ReturnsOnlyMatchingHash(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "users-by-team", "by-team", "team", "ALL", nil)

	putItem(t, srv, "users-by-team", map[string]any{"id": map[string]string{"S": "u1"}, "team": map[string]string{"S": "red"}})
	putItem(t, srv, "users-by-team", map[string]any{"id": map[string]string{"S": "u2"}, "team": map[string]string{"S": "blue"}})
	putItem(t, srv, "users-by-team", map[string]any{"id": map[string]string{"S": "u3"}, "team": map[string]string{"S": "red"}})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "users-by-team",
		"IndexName":              "by-team",
		"KeyConditionExpression": "team = :t",
		"ExpressionAttributeValues": map[string]any{
			":t": map[string]string{"S": "red"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Fatalf("expected 2 items for team=red, got %d: %v", result.Count, result.Items)
	}
}

// TestScan_GSI_SparseIndex_ExcludesItemsMissingIndexKey exercises the
// write-time sparse-index rule (design §2/§3) end-to-end through the flipped
// read path: an item that never defined the index's hash key must not
// appear in a GSI Scan at all, not merely have it filtered out client-side.
func TestScan_GSI_SparseIndex_ExcludesItemsMissingIndexKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "sparse-products", "by-category", "category", "ALL", nil)

	putItem(t, srv, "sparse-products", map[string]any{
		"id":       map[string]string{"S": "p1"},
		"category": map[string]string{"S": "electronics"},
	})
	putItem(t, srv, "sparse-products", map[string]any{
		"id": map[string]string{"S": "p2"}, // no "category" — sparse, excluded from the index
	})

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "sparse-products",
		"IndexName": "by-category",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Fatalf("expected 1 item (sparse item excluded), got %d: %v", result.Count, result.Items)
	}
}

// ---- Cursor: positional 4-tuple resolution (design §4, G2 generalized) ----

// TestScan_GSI_Pagination_CursorSurvivesDeletionOfCursorItem is G2's
// headline property carried over to the index cursor: deleting the exact
// item a page's LastEvaluatedKey names must not make the next page restart
// from the beginning or skip/duplicate entries.
func TestScan_GSI_Pagination_CursorSurvivesDeletionOfCursorItem(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "cursor-products", "by-category", "category", "ALL", nil)

	for i := 1; i <= 4; i++ {
		putItem(t, srv, "cursor-products", map[string]any{
			"id":       map[string]string{"S": fmt.Sprintf("p%d", i)},
			"category": map[string]string{"S": fmt.Sprintf("cat-%d", i)}, // each item its own distinct index key
		})
	}

	page1Resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "cursor-products",
		"IndexName": "by-category",
		"Limit":     2,
	})
	defer page1Resp.Body.Close()
	helpers.AssertStatus(t, page1Resp, http.StatusOK)

	var page1 struct {
		Items            []map[string]any `json:"Items"`
		Count            int              `json:"Count"`
		LastEvaluatedKey map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, page1Resp, &page1)
	if page1.Count != 2 {
		t.Fatalf("page1: expected 2 items, got %d", page1.Count)
	}
	if page1.LastEvaluatedKey == nil {
		t.Fatal("page1: expected LastEvaluatedKey for more pages")
	}

	// The cursor item is whichever item page1's LastEvaluatedKey names —
	// delete it by its table primary key before fetching page2.
	cursorID, _ := page1.LastEvaluatedKey["id"].(map[string]any)["S"].(string)
	deleteItemByID(t, srv, "cursor-products", cursorID)

	page2Resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":         "cursor-products",
		"IndexName":         "by-category",
		"Limit":             10,
		"ExclusiveStartKey": page1.LastEvaluatedKey,
	})
	defer page2Resp.Body.Close()
	helpers.AssertStatus(t, page2Resp, http.StatusOK)

	var page2 struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, page2Resp, &page2)

	// Then: no duplicates and no gap across the two pages — 4 items total,
	// deleting the cursor item after it was already handed out on page1
	// does not remove it from the already-delivered set, and page2 must
	// pick up exactly where page1 left off (the remaining 2 items).
	if page2.Count != 2 {
		t.Fatalf("page2: expected 2 items (deleted cursor item must not reappear or cause a restart), got %d: %v", page2.Count, page2.Items)
	}
	seen := map[string]bool{}
	for _, it := range page1.Items {
		seen[it["id"].(map[string]any)["S"].(string)] = true
	}
	for _, it := range page2.Items {
		id := it["id"].(map[string]any)["S"].(string)
		if seen[id] {
			t.Fatalf("item %q duplicated across page1 and page2", id)
		}
		seen[id] = true
	}
	if len(seen) != 4 {
		t.Fatalf("expected 4 distinct items across both pages, got %d: %v", len(seen), seen)
	}
}

// TestScan_GSI_Pagination_SurvivesDeletionOfItemSharingIndexKey is the
// index-specific generalization of G2: index keys are not unique (many base
// items can share one index key, unlike the base table's own primary key),
// so a page boundary can fall in the middle of a group of same-index-key
// items. Deleting a not-yet-returned item that shares the cursor's index
// key (but is a different base item) must not corrupt the next page.
func TestScan_GSI_Pagination_SurvivesDeletionOfItemSharingIndexKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "grouped-products", "by-category", "category", "ALL", nil)

	// p1, p2, p3 all share index key "cat-0" (tiebroken by base id order);
	// p4 is alone in "cat-1".
	for i, cat := range []string{"cat-0", "cat-0", "cat-0", "cat-1"} {
		putItem(t, srv, "grouped-products", map[string]any{
			"id":       map[string]string{"S": fmt.Sprintf("p%d", i+1)},
			"category": map[string]string{"S": cat},
		})
	}

	page1Resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "grouped-products",
		"IndexName": "by-category",
		"Limit":     2,
	})
	defer page1Resp.Body.Close()
	helpers.AssertStatus(t, page1Resp, http.StatusOK)

	var page1 struct {
		Items            []map[string]any `json:"Items"`
		Count            int              `json:"Count"`
		LastEvaluatedKey map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, page1Resp, &page1)
	if page1.Count != 2 {
		t.Fatalf("page1: expected 2 items (p1, p2), got %d: %v", page1.Count, page1.Items)
	}
	if page1.LastEvaluatedKey == nil {
		t.Fatal("page1: expected LastEvaluatedKey for more pages")
	}

	// p3 shares the cursor's index key ("cat-0") but has NOT been returned
	// yet (it sorts after p2, the cursor item, within the same index-key
	// group) — delete it before fetching page2.
	deleteItemByID(t, srv, "grouped-products", "p3")

	page2Resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":         "grouped-products",
		"IndexName":         "by-category",
		"Limit":             10,
		"ExclusiveStartKey": page1.LastEvaluatedKey,
	})
	defer page2Resp.Body.Close()
	helpers.AssertStatus(t, page2Resp, http.StatusOK)

	var page2 struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, page2Resp, &page2)

	// Then: p3 was deleted before ever being returned, so it correctly
	// never appears; only p4 remains — no duplicate of p1/p2, no crash from
	// the composite-key tiebreak logic when a same-key row vanishes.
	if page2.Count != 1 {
		t.Fatalf("page2: expected 1 item (p4; p3 deleted before delivery), got %d: %v", page2.Count, page2.Items)
	}
	if got := page2.Items[0]["id"].(map[string]any)["S"].(string); got != "p4" {
		t.Fatalf("page2: expected p4, got %q", got)
	}
}
