package dynamodb_test

// Failing-first evidence for the base-table Number-key ordering bug fixed by
// docs/plans/dynamodb-gsi-design.md §2/§7 phase 1: extractKeyValue returned
// the raw decimal string for a Number-typed key attribute, and every
// ordering comparison built on top of it (storage composite key, cursor
// resolution, in-memory sorts) was a plain Go string compare — so a Number
// sort key holding 5, 10, 50 paginated/queried as 10, 5, 50 instead of AWS's
// documented numeric order ("If the data type of the sort key is Number, the
// results are returned in numeric order").
//
// These tests were confirmed red against pre-fix main. Actual observed
// output (go test -run ... -v, before any of this PR's storage/handler
// changes landed):
//
//	TestQuery_NumberSortKey_NumericOrder:
//	    Query returned Number sort key order [-3 10 2 5 50], want numeric order [-3 2 5 10 50]
//	TestScan_NumberSortKey_PaginationWalk_NumericOrder_NoDupsNoGaps:
//	    paginated walk order = [-1.5 -15 0 100 1000000000000000000000000000000000 20 3 7.5 9],
//	    want numeric order    [-15 -1.5 0 3 7.5 9 20 100 1000000000000000000000000000000000]
//	TestQuery_NumberSortKey_PaginationWalk_SurvivesDeletedCursorItem:
//	    page1: want numeric order [5 10], got [10 100]
//
// All three are plain string-order-on-raw-decimal-text artifacts — e.g.
// "10" < "2" < "5" lexicographically, and "10" < "100" < "5" < "50" puts
// "10" before "5" in the cursor test.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestQuery_NumberSortKey_NumericOrder is the headline reproduction: a
// Number sort key must come back from Query in numeric order, not
// lexicographic string order on the raw decimal text.
func TestQuery_NumberSortKey_NumericOrder(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "scores", "game", "S", "score", "N")

	for _, score := range []string{"50", "5", "10", "-3", "2"} {
		putItem(t, srv, "scores", map[string]any{
			"game":  map[string]string{"S": "g1"},
			"score": map[string]string{"N": score},
		})
	}

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "scores",
		"KeyConditionExpression": "game = :g",
		"ExpressionAttributeValues": map[string]any{
			":g": map[string]string{"S": "g1"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items []map[string]map[string]any `json:"Items"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if len(result.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(result.Items))
	}
	got := make([]string, len(result.Items))
	for i, item := range result.Items {
		got[i] = item["score"]["N"].(string)
	}
	want := []string{"-3", "2", "5", "10", "50"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Query returned Number sort key order %v, want numeric order %v", got, want)
		}
	}
}

// TestScan_NumberSortKey_PaginationWalk_NumericOrder_NoDupsNoGaps walks a
// table with a Number sort key one item at a time (Limit=1) and checks both
// that pagination has no duplicates/gaps (the pre-existing G2/A3 guarantee)
// AND that the walk order is numeric, not lexicographic — a regression this
// encoding must not introduce into the already-fixed pagination behavior.
func TestScan_NumberSortKey_PaginationWalk_NumericOrder_NoDupsNoGaps(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "walk", "pk", "S", "n", "N")

	values := []string{"100", "9", "20", "3", "-15", "0", "7.5", "-1.5", "1000000000000000000000000000000000"}
	for _, v := range values {
		putItem(t, srv, "walk", map[string]any{
			"pk": map[string]string{"S": "p"},
			"n":  map[string]string{"N": v},
		})
	}

	var got []string
	var exclusiveStartKey map[string]any
	for pages := 0; ; pages++ {
		if pages > len(values) {
			t.Fatalf("too many pages (%d) — probable infinite loop", pages)
		}
		req := map[string]any{
			"TableName": "walk",
			"Limit":     1,
		}
		if exclusiveStartKey != nil {
			req["ExclusiveStartKey"] = exclusiveStartKey
		}
		resp := ddbCall(t, srv, "Scan", req)
		helpers.AssertStatus(t, resp, http.StatusOK)
		var page struct {
			Items            []map[string]map[string]any `json:"Items"`
			LastEvaluatedKey map[string]any              `json:"LastEvaluatedKey"`
		}
		helpers.DecodeJSON(t, resp, &page)
		resp.Body.Close()

		for _, item := range page.Items {
			got = append(got, item["n"]["N"].(string))
		}
		if page.LastEvaluatedKey == nil {
			break
		}
		exclusiveStartKey = page.LastEvaluatedKey
	}

	if len(got) != len(values) {
		t.Fatalf("paginated walk returned %d items, want %d distinct values (dup or gap): %v", len(got), len(values), got)
	}
	seen := map[string]bool{}
	for _, v := range got {
		if seen[v] {
			t.Fatalf("duplicate value %s delivered across pages: %v", v, got)
		}
		seen[v] = true
	}
	want := []string{"-15", "-1.5", "0", "3", "7.5", "9", "20", "100", "1000000000000000000000000000000000"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("paginated walk order = %v, want numeric order %v", got, want)
	}
}

// TestQuery_NumberSortKey_PaginationWalk_SurvivesDeletedCursorItem is the
// pagination-plan.md G2 cursor-survives-deletion guarantee, generalized to a
// Number sort key: deleting the last-returned item between pages must not
// restart pagination from page 1, and the resume point must still be
// correct in *numeric* order.
func TestQuery_NumberSortKey_PaginationWalk_SurvivesDeletedCursorItem(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-del-cursor-n", "pk", "S", "n", "N")
	for _, v := range []string{"5", "10", "50", "100"} {
		putItem(t, srv, "q-del-cursor-n", map[string]any{
			"pk": map[string]any{"S": "user#1"},
			"n":  map[string]any{"N": v},
		})
	}

	resp1 := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "q-del-cursor-n",
		"KeyConditionExpression":    "pk = :pk",
		"ExpressionAttributeValues": map[string]any{":pk": map[string]any{"S": "user#1"}},
		"Limit":                     2,
	})
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)

	var page1 struct {
		Items            []map[string]map[string]any `json:"Items"`
		LastEvaluatedKey map[string]any               `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp1, &page1)
	if len(page1.Items) != 2 {
		t.Fatalf("page1: want 2 items, got %d", len(page1.Items))
	}
	if got := []string{page1.Items[0]["n"]["N"].(string), page1.Items[1]["n"]["N"].(string)}; got[0] != "5" || got[1] != "10" {
		t.Fatalf("page1: want numeric order [5 10], got %v", got)
	}
	if page1.LastEvaluatedKey == nil {
		t.Fatal("page1: expected LastEvaluatedKey")
	}

	// Delete the last-returned item (n=10) before fetching page 2.
	cursorN := page1.Items[len(page1.Items)-1]["n"]["N"].(string)
	delResp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName": "q-del-cursor-n",
		"Key": map[string]any{
			"pk": map[string]any{"S": "user#1"},
			"n":  map[string]any{"N": cursorN},
		},
	})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)

	resp2 := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "q-del-cursor-n",
		"KeyConditionExpression":    "pk = :pk",
		"ExpressionAttributeValues": map[string]any{":pk": map[string]any{"S": "user#1"}},
		"Limit":                     2,
		"ExclusiveStartKey":         page1.LastEvaluatedKey,
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var page2 struct {
		Items            []map[string]map[string]any `json:"Items"`
		LastEvaluatedKey map[string]any               `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp2, &page2)

	got := map[string]bool{}
	for _, item := range page2.Items {
		got[item["n"]["N"].(string)] = true
	}
	if got["5"] {
		t.Error("page2 re-delivered n=5 — pagination silently restarted from page 1")
	}
	if !got["50"] || !got["100"] {
		t.Errorf("page2 should contain n=50 and n=100, got %v", got)
	}
	if len(page2.Items) != 2 {
		t.Errorf("page2: want 2 items (50, 100), got %d: %v", len(page2.Items), got)
	}
}
