package dynamodb_test

// ConsistentRead validation on index reads
// (docs/plans/dynamodb-gsi-design.md §2's deferred follow-up). AWS's Query
// reference is categorical: strongly consistent reads are not supported on a
// global secondary index, and asking for one is a ValidationException — not a
// silently-ignored parameter, which is what the emulator did before (it never
// parsed ConsistentRead at all).
//
// LSIs are the deliberate contrast: they live in the base table's partition
// and DO support ConsistentRead=true, so the same request against an LSI must
// still succeed. Base-table reads likewise.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const consistentReadOnGSIMessage = "Consistent reads are not supported on global secondary indexes"

func assertConsistentReadRejected(t *testing.T, resp *http.Response) {
	t.Helper()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var body struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	helpers.DecodeJSON(t, resp, &body)
	if !strings.Contains(body.Type, "ValidationException") {
		t.Errorf("expected ValidationException, got __type=%q", body.Type)
	}
	if body.Message != consistentReadOnGSIMessage {
		t.Errorf("message = %q, want %q", body.Message, consistentReadOnGSIMessage)
	}
}

func TestQuery_GSI_ConsistentReadTrue_ValidationException(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "cr-products", "by-category", "category", "ALL", nil)
	putItem(t, srv, "cr-products", map[string]any{
		"id":       map[string]string{"S": "p1"},
		"category": map[string]string{"S": "electronics"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "cr-products",
		"IndexName":              "by-category",
		"ConsistentRead":         true,
		"KeyConditionExpression": "category = :c",
		"ExpressionAttributeValues": map[string]any{
			":c": map[string]string{"S": "electronics"},
		},
	})
	defer resp.Body.Close()
	assertConsistentReadRejected(t, resp)
}

func TestScan_GSI_ConsistentReadTrue_ValidationException(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "cr-products-scan", "by-category", "category", "ALL", nil)

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":      "cr-products-scan",
		"IndexName":      "by-category",
		"ConsistentRead": true,
	})
	defer resp.Body.Close()
	assertConsistentReadRejected(t, resp)
}

// ConsistentRead=false on a GSI is explicitly fine — it asks for the
// eventually consistent read a GSI already provides.
func TestQuery_GSI_ConsistentReadFalse_Accepted(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithGSI(t, srv, "cr-ok-products", "by-category", "category", "ALL", nil)
	putItem(t, srv, "cr-ok-products", map[string]any{
		"id":       map[string]string{"S": "p1"},
		"category": map[string]string{"S": "electronics"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "cr-ok-products",
		"IndexName":              "by-category",
		"ConsistentRead":         false,
		"KeyConditionExpression": "category = :c",
		"ExpressionAttributeValues": map[string]any{
			":c": map[string]string{"S": "electronics"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
}

// An LSI supports strongly consistent reads (it shares the base table's
// partition), so the same parameter that is rejected on a GSI must be
// accepted here.
func TestQuery_LSI_ConsistentReadTrue_Accepted(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithLSI(t, srv, "cr-lsi-posts")
	putItem(t, srv, "cr-lsi-posts", map[string]any{
		"forumId":   map[string]string{"S": "f1"},
		"postId":    map[string]string{"S": "p1"},
		"createdAt": map[string]string{"S": "2026-01-01"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "cr-lsi-posts",
		"IndexName":              "by-created",
		"ConsistentRead":         true,
		"KeyConditionExpression": "forumId = :f",
		"ExpressionAttributeValues": map[string]any{
			":f": map[string]string{"S": "f1"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
}

// A base-table Query with ConsistentRead=true is the ordinary strongly
// consistent read — always accepted.
func TestQuery_BaseTable_ConsistentReadTrue_Accepted(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "cr-base-table")
	putItem(t, srv, "cr-base-table", map[string]any{
		"id": map[string]string{"S": "a1"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "cr-base-table",
		"ConsistentRead":         true,
		"KeyConditionExpression": "id = :v",
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]string{"S": "a1"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
}
