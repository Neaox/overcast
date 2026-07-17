// Package dynamodb_test contains integration tests for the DynamoDB emulator.
//
// TDD status: tests are written and FAILING — this is correct.
// Implement handlers in internal/services/dynamodb/ to make them pass.
// Implementation order matches the test order in this file.
//
// Run: go test ./tests/integration/dynamodb/...
package dynamodb_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// ---- CreateTable -----------------------------------------------------------

func TestCreateTable_success(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "users",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TableDescription struct {
			TableName   string `json:"TableName"`
			TableStatus string `json:"TableStatus"`
		} `json:"TableDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.TableDescription.TableName != "users" {
		t.Errorf("expected TableName 'users', got %q", result.TableDescription.TableName)
	}
	if result.TableDescription.TableStatus != "ACTIVE" {
		t.Errorf("expected TableStatus ACTIVE, got %q", result.TableDescription.TableStatus)
	}
}

func TestCreateTable_duplicate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "users")

	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName":            "users",
		"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
		"BillingMode":          "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceInUseException")
}

func TestCreateTable_invalidName(t *testing.T) {
	// Given: invalid DynamoDB table names from the documented name constraints.
	cases := []struct {
		name      string
		tableName string
	}{
		{name: "too short", tableName: "ab"},
		{name: "too long", tableName: strings.Repeat("a", 256)},
		{name: "invalid character", tableName: "bad!table"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			// When: CreateTable is called with an invalid TableName.
			resp := ddbCall(t, srv, "CreateTable", map[string]any{
				"TableName":            tc.tableName,
				"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
				"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
				"BillingMode":          "PAY_PER_REQUEST",
			})
			defer resp.Body.Close()

			// Then: DynamoDB rejects the request with an AWS-modeled validation error.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "ValidationException")
		})
	}
}

// ---- DescribeTable ---------------------------------------------------------

func TestDescribeTable_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "orders")

	resp := ddbCall(t, srv, "DescribeTable", map[string]any{
		"TableName": "orders",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestDescribeTable_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "DescribeTable", map[string]any{
		"TableName": "no-such-table",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ---- PutItem ---------------------------------------------------------------

func TestPutItem_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "users")

	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "users",
		"Item": map[string]any{
			"id":   map[string]string{"S": "user-1"},
			"name": map[string]string{"S": "Alice"},
			"age":  map[string]string{"N": "30"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestPutItem_tableNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "no-table",
		"Item":      map[string]any{"id": map[string]string{"S": "1"}},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestPutItem_conditionCheckFailed(t *testing.T) {
	// Given: a table with an existing item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "users")
	putItem(t, srv, "users", map[string]any{
		"id":   map[string]string{"S": "user-1"},
		"name": map[string]string{"S": "Alice"},
	})

	// When: PutItem with attribute_not_exists(id) — item exists, so condition fails
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "users",
		"Item": map[string]any{
			"id":   map[string]string{"S": "user-1"},
			"name": map[string]string{"S": "Bob"},
		},
		"ConditionExpression": "attribute_not_exists(id)",
	})
	defer resp.Body.Close()

	// Then: ConditionalCheckFailedException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ConditionalCheckFailedException")
}

func TestPutItem_conditionCheckPasses(t *testing.T) {
	// Given: a table with no matching item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "users")

	// When: PutItem with attribute_not_exists(id) on non-existent item
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "users",
		"Item": map[string]any{
			"id":   map[string]string{"S": "user-1"},
			"name": map[string]string{"S": "Alice"},
		},
		"ConditionExpression": "attribute_not_exists(id)",
	})
	defer resp.Body.Close()

	// Then: success — item is created
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ---- GetItem ---------------------------------------------------------------

func TestGetItem_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "users")
	putItem(t, srv, "users", map[string]any{
		"id":   map[string]string{"S": "user-1"},
		"name": map[string]string{"S": "Alice"},
	})

	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "users",
		"Key": map[string]any{
			"id": map[string]string{"S": "user-1"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Item map[string]map[string]string `json:"Item"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Item["name"]["S"] != "Alice" {
		t.Errorf("expected name Alice, got %q", result.Item["name"]["S"])
	}
}

func TestGetItem_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "users")

	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "users",
		"Key":       map[string]any{"id": map[string]string{"S": "no-such-id"}},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	// DynamoDB returns 200 with empty Item when key doesn't exist.
	var result struct {
		Item map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Item) != 0 {
		t.Errorf("expected empty Item for missing key, got %v", result.Item)
	}
}

// ---- DeleteItem ------------------------------------------------------------

func TestDeleteItem_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "users")
	putItem(t, srv, "users", map[string]any{
		"id": map[string]string{"S": "user-1"},
	})

	resp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName": "users",
		"Key":       map[string]any{"id": map[string]string{"S": "user-1"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify it's gone.
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "users",
		"Key":       map[string]any{"id": map[string]string{"S": "user-1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if len(result.Item) != 0 {
		t.Error("expected item to be deleted")
	}
}

// ---- Scan ------------------------------------------------------------------

func TestScan_returnsAllItems(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "products")

	for _, id := range []string{"p1", "p2", "p3"} {
		putItem(t, srv, "products", map[string]any{
			"id": map[string]string{"S": id},
		})
	}

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "products",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Count != 3 {
		t.Errorf("expected Count 3, got %d", result.Count)
	}
}

func TestScan_emptyTable(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "empty")

	resp := ddbCall(t, srv, "Scan", map[string]any{"TableName": "empty"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items []any `json:"Items"`
		Count int   `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 0 {
		t.Errorf("expected Count 0, got %d", result.Count)
	}
}

func TestScan_filterExpression_equality(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "products")

	for _, item := range []map[string]any{
		{"id": map[string]string{"S": "1"}, "status": map[string]string{"S": "active"}},
		{"id": map[string]string{"S": "2"}, "status": map[string]string{"S": "inactive"}},
		{"id": map[string]string{"S": "3"}, "status": map[string]string{"S": "active"}},
	} {
		putItem(t, srv, "products", item)
	}

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "products",
		"FilterExpression": "status = :s",
		"ExpressionAttributeValues": map[string]any{
			":s": map[string]string{"S": "active"},
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
		t.Errorf("expected 2 active items, got %d", result.Count)
	}
}

func TestScan_filterExpression_attributeExists(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "orders")

	putItem(t, srv, "orders", map[string]any{
		"id": map[string]string{"S": "1"}, "note": map[string]string{"S": "rush"},
	})
	putItem(t, srv, "orders", map[string]any{
		"id": map[string]string{"S": "2"},
	})

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "orders",
		"FilterExpression": "attribute_exists(note)",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Errorf("expected 1 item with note, got %d", result.Count)
	}
}

func TestScan_filterExpression_compoundAnd(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "widgets")

	for _, item := range []map[string]any{
		{"id": map[string]string{"S": "1"}, "color": map[string]string{"S": "red"}, "size": map[string]string{"S": "large"}},
		{"id": map[string]string{"S": "2"}, "color": map[string]string{"S": "red"}, "size": map[string]string{"S": "small"}},
		{"id": map[string]string{"S": "3"}, "color": map[string]string{"S": "blue"}, "size": map[string]string{"S": "large"}},
	} {
		putItem(t, srv, "widgets", item)
	}

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "widgets",
		"FilterExpression": "color = :c AND size = :s",
		"ExpressionAttributeValues": map[string]any{
			":c": map[string]string{"S": "red"},
			":s": map[string]string{"S": "large"},
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
		t.Errorf("expected 1 red+large widget, got %d", result.Count)
	}
}

// ---- Query -----------------------------------------------------------------

func TestQuery_byHashKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "events", "userId", "S", "timestamp", "S")

	putItem(t, srv, "events", map[string]any{
		"userId":    map[string]string{"S": "user-1"},
		"timestamp": map[string]string{"S": "2024-01-01"},
		"event":     map[string]string{"S": "login"},
	})
	putItem(t, srv, "events", map[string]any{
		"userId":    map[string]string{"S": "user-1"},
		"timestamp": map[string]string{"S": "2024-01-02"},
		"event":     map[string]string{"S": "logout"},
	})
	putItem(t, srv, "events", map[string]any{
		"userId":    map[string]string{"S": "user-2"},
		"timestamp": map[string]string{"S": "2024-01-01"},
		"event":     map[string]string{"S": "login"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "events",
		"KeyConditionExpression": "userId = :uid",
		"ExpressionAttributeValues": map[string]any{
			":uid": map[string]string{"S": "user-1"},
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
		t.Errorf("expected 2 items for user-1, got %d", result.Count)
	}
}

func TestQuery_byHashAndSortKey(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "events", "userId", "S", "timestamp", "S")

	for _, ts := range []string{"2024-01-01", "2024-01-02", "2024-01-03"} {
		putItem(t, srv, "events", map[string]any{
			"userId":    map[string]string{"S": "user-1"},
			"timestamp": map[string]string{"S": ts},
		})
	}

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "events",
		"KeyConditionExpression": "userId = :uid AND #ts = :ts",
		"ExpressionAttributeNames": map[string]string{
			"#ts": "timestamp",
		},
		"ExpressionAttributeValues": map[string]any{
			":uid": map[string]string{"S": "user-1"},
			":ts":  map[string]string{"S": "2024-01-02"},
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
		t.Errorf("expected 1 item, got %d", result.Count)
	}
}

func TestQuery_withFilterExpression(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "log", "userId", "S", "timestamp", "S")

	for _, item := range []map[string]any{
		{"userId": map[string]string{"S": "u1"}, "timestamp": map[string]string{"S": "t1"}, "level": map[string]string{"S": "error"}},
		{"userId": map[string]string{"S": "u1"}, "timestamp": map[string]string{"S": "t2"}, "level": map[string]string{"S": "info"}},
	} {
		putItem(t, srv, "log", item)
	}

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "log",
		"KeyConditionExpression": "userId = :uid",
		"FilterExpression":       "level = :lvl",
		"ExpressionAttributeValues": map[string]any{
			":uid": map[string]string{"S": "u1"},
			":lvl": map[string]string{"S": "error"},
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
		t.Errorf("expected 1 error-level item, got %d", result.Count)
	}
}

// ---- Unimplemented operations return 501 -----------------------------------

func TestTransactWriteItems_putMultipleItems(t *testing.T) {
	// Given: a table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-table")

	// When: TransactWriteItems with multiple Put operations
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Put": map[string]any{
					"TableName": "txn-table",
					"Item": map[string]any{
						"id":   map[string]any{"S": "item-1"},
						"name": map[string]any{"S": "Alice"},
					},
				},
			},
			map[string]any{
				"Put": map[string]any{
					"TableName": "txn-table",
					"Item": map[string]any{
						"id":   map[string]any{"S": "item-2"},
						"name": map[string]any{"S": "Bob"},
					},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: success
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: both items exist
	get1 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-table",
		"Key":       map[string]any{"id": map[string]any{"S": "item-1"}},
	})
	defer get1.Body.Close()
	var r1 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get1, &r1)
	if r1.Item == nil {
		t.Fatal("expected item-1 to exist")
	}

	get2 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-table",
		"Key":       map[string]any{"id": map[string]any{"S": "item-2"}},
	})
	defer get2.Body.Close()
	var r2 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get2, &r2)
	if r2.Item == nil {
		t.Fatal("expected item-2 to exist")
	}
}

func TestTransactWriteItems_deleteItems(t *testing.T) {
	// Given: a table with items
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-del")
	putItem(t, srv, "txn-del", map[string]any{
		"id":   map[string]any{"S": "del-1"},
		"name": map[string]any{"S": "Alice"},
	})
	putItem(t, srv, "txn-del", map[string]any{
		"id":   map[string]any{"S": "del-2"},
		"name": map[string]any{"S": "Bob"},
	})

	// When: TransactWriteItems with Delete operations
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Delete": map[string]any{
					"TableName": "txn-del",
					"Key":       map[string]any{"id": map[string]any{"S": "del-1"}},
				},
			},
			map[string]any{
				"Delete": map[string]any{
					"TableName": "txn-del",
					"Key":       map[string]any{"id": map[string]any{"S": "del-2"}},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: success
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: items are gone
	get1 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-del",
		"Key":       map[string]any{"id": map[string]any{"S": "del-1"}},
	})
	defer get1.Body.Close()
	var r1 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get1, &r1)
	if r1.Item != nil {
		t.Error("expected del-1 to be deleted")
	}
}

func TestTransactWriteItems_updateItems(t *testing.T) {
	// Given: a table with an item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-upd")
	putItem(t, srv, "txn-upd", map[string]any{
		"id":   map[string]any{"S": "u-1"},
		"name": map[string]any{"S": "Alice"},
	})

	// When: TransactWriteItems with an Update operation
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Update": map[string]any{
					"TableName":                "txn-upd",
					"Key":                      map[string]any{"id": map[string]any{"S": "u-1"}},
					"UpdateExpression":         "SET #n = :v",
					"ExpressionAttributeNames": map[string]any{"#n": "name"},
					"ExpressionAttributeValues": map[string]any{
						":v": map[string]any{"S": "Bob"},
					},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: success
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: item is updated
	get := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-upd",
		"Key":       map[string]any{"id": map[string]any{"S": "u-1"}},
	})
	defer get.Body.Close()
	var r struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get, &r)
	if r.Item["name"]["S"] != "Bob" {
		t.Errorf("expected name=Bob, got %v", r.Item["name"])
	}
}

func TestTransactWriteItems_conditionCheckFailure(t *testing.T) {
	// Given: a table with an item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-cond")
	putItem(t, srv, "txn-cond", map[string]any{
		"id":     map[string]any{"S": "c-1"},
		"status": map[string]any{"S": "active"},
	})

	// When: TransactWriteItems with a ConditionCheck that fails
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"ConditionCheck": map[string]any{
					"TableName":           "txn-cond",
					"Key":                 map[string]any{"id": map[string]any{"S": "c-1"}},
					"ConditionExpression": "#s = :v",
					"ExpressionAttributeNames": map[string]any{
						"#s": "status",
					},
					"ExpressionAttributeValues": map[string]any{
						":v": map[string]any{"S": "inactive"},
					},
				},
			},
			map[string]any{
				"Put": map[string]any{
					"TableName": "txn-cond",
					"Item": map[string]any{
						"id":   map[string]any{"S": "c-2"},
						"name": map[string]any{"S": "New"},
					},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: transaction cancelled
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var errResp struct {
		Code    string `json:"__type"`
		Message string `json:"Message"`
	}
	helpers.DecodeJSON(t, resp, &errResp)
	if errResp.Code != "TransactionCanceledException" {
		t.Errorf("expected TransactionCanceledException, got %q", errResp.Code)
	}

	// And: the Put was NOT applied (atomicity)
	get := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-cond",
		"Key":       map[string]any{"id": map[string]any{"S": "c-2"}},
	})
	defer get.Body.Close()
	var r struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get, &r)
	if r.Item != nil {
		t.Error("expected c-2 to NOT exist after cancelled transaction")
	}
}

func TestTransactWriteItems_conditionCheckSuccess(t *testing.T) {
	// Given: a table with an item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-condok")
	putItem(t, srv, "txn-condok", map[string]any{
		"id":     map[string]any{"S": "c-1"},
		"status": map[string]any{"S": "active"},
	})

	// When: TransactWriteItems with a ConditionCheck that passes
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"ConditionCheck": map[string]any{
					"TableName":           "txn-condok",
					"Key":                 map[string]any{"id": map[string]any{"S": "c-1"}},
					"ConditionExpression": "#s = :v",
					"ExpressionAttributeNames": map[string]any{
						"#s": "status",
					},
					"ExpressionAttributeValues": map[string]any{
						":v": map[string]any{"S": "active"},
					},
				},
			},
			map[string]any{
				"Put": map[string]any{
					"TableName": "txn-condok",
					"Item": map[string]any{
						"id":   map[string]any{"S": "c-2"},
						"name": map[string]any{"S": "New"},
					},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: success
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the Put was applied
	get := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-condok",
		"Key":       map[string]any{"id": map[string]any{"S": "c-2"}},
	})
	defer get.Body.Close()
	var r struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get, &r)
	if r.Item == nil {
		t.Fatal("expected c-2 to exist after successful transaction")
	}
}

func TestTransactWriteItems_putWithConditionExpression(t *testing.T) {
	// Given: a table — item does NOT exist
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-putcond")

	// When: Put with attribute_not_exists condition (insert-only)
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Put": map[string]any{
					"TableName":           "txn-putcond",
					"ConditionExpression": "attribute_not_exists(id)",
					"Item": map[string]any{
						"id":   map[string]any{"S": "new-1"},
						"name": map[string]any{"S": "Alice"},
					},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: same Put again — item now exists, condition fails
	resp2 := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Put": map[string]any{
					"TableName":           "txn-putcond",
					"ConditionExpression": "attribute_not_exists(id)",
					"Item": map[string]any{
						"id":   map[string]any{"S": "new-1"},
						"name": map[string]any{"S": "Alice2"},
					},
				},
			},
		},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusBadRequest)
}

func TestTransactWriteItems_mixedOperations(t *testing.T) {
	// Given: a table with items
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-mix")
	putItem(t, srv, "txn-mix", map[string]any{
		"id":   map[string]any{"S": "exist-1"},
		"name": map[string]any{"S": "Keep"},
	})
	putItem(t, srv, "txn-mix", map[string]any{
		"id":   map[string]any{"S": "exist-2"},
		"name": map[string]any{"S": "DeleteMe"},
	})

	// When: mixed Put + Delete + Update in one transaction
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Put": map[string]any{
					"TableName": "txn-mix",
					"Item": map[string]any{
						"id":   map[string]any{"S": "new-3"},
						"name": map[string]any{"S": "Charlie"},
					},
				},
			},
			map[string]any{
				"Delete": map[string]any{
					"TableName": "txn-mix",
					"Key":       map[string]any{"id": map[string]any{"S": "exist-2"}},
				},
			},
			map[string]any{
				"Update": map[string]any{
					"TableName":                 "txn-mix",
					"Key":                       map[string]any{"id": map[string]any{"S": "exist-1"}},
					"UpdateExpression":          "SET #n = :v",
					"ExpressionAttributeNames":  map[string]any{"#n": "name"},
					"ExpressionAttributeValues": map[string]any{":v": map[string]any{"S": "Updated"}},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: new item exists
	get1 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-mix",
		"Key":       map[string]any{"id": map[string]any{"S": "new-3"}},
	})
	defer get1.Body.Close()
	var r1 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get1, &r1)
	if r1.Item == nil {
		t.Fatal("expected new-3 to exist")
	}
	if r1.Item["name"]["S"] != "Charlie" {
		t.Errorf("expected name=Charlie, got %v", r1.Item["name"])
	}

	// And: deleted item is gone
	get2 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-mix",
		"Key":       map[string]any{"id": map[string]any{"S": "exist-2"}},
	})
	defer get2.Body.Close()
	var r2 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get2, &r2)
	if r2.Item != nil {
		t.Error("expected exist-2 to be deleted")
	}

	// And: updated item has new value
	get3 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-mix",
		"Key":       map[string]any{"id": map[string]any{"S": "exist-1"}},
	})
	defer get3.Body.Close()
	var r3 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get3, &r3)
	if r3.Item["name"]["S"] != "Updated" {
		t.Errorf("expected name=Updated, got %v", r3.Item["name"])
	}
}

func TestTransactWriteItems_tooManyItems(t *testing.T) {
	// Given: a table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-limit")

	// When: more than 100 items (AWS limit is 100 for transact, unlike 25 for batch)
	items := make([]any, 101)
	for i := range items {
		items[i] = map[string]any{
			"Put": map[string]any{
				"TableName": "txn-limit",
				"Item": map[string]any{
					"id": map[string]any{"S": "item-" + strconv.Itoa(i)},
				},
			},
		}
	}
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": items,
	})
	defer resp.Body.Close()

	// Then: validation error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestTransactWriteItems_tableNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Put": map[string]any{
					"TableName": "nonexistent",
					"Item":      map[string]any{"id": map[string]any{"S": "x"}},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestTransactWriteItems_multipleTablesSuccess(t *testing.T) {
	// Given: two tables
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-t1")
	createTable(t, srv, "txn-t2")

	// When: transaction spans both tables
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Put": map[string]any{
					"TableName": "txn-t1",
					"Item":      map[string]any{"id": map[string]any{"S": "a1"}},
				},
			},
			map[string]any{
				"Put": map[string]any{
					"TableName": "txn-t2",
					"Item":      map[string]any{"id": map[string]any{"S": "b1"}},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: items exist in both tables
	get1 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-t1",
		"Key":       map[string]any{"id": map[string]any{"S": "a1"}},
	})
	defer get1.Body.Close()
	var r1 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get1, &r1)
	if r1.Item == nil {
		t.Fatal("expected item in txn-t1")
	}

	get2 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "txn-t2",
		"Key":       map[string]any{"id": map[string]any{"S": "b1"}},
	})
	defer get2.Body.Close()
	var r2 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, get2, &r2)
	if r2.Item == nil {
		t.Fatal("expected item in txn-t2")
	}
}

// ---- TransactGetItems ------------------------------------------------------

func TestTransactGetItems_success(t *testing.T) {
	// Given: a table with items
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-get")
	putItem(t, srv, "txn-get", map[string]any{
		"id":   map[string]any{"S": "g-1"},
		"name": map[string]any{"S": "Alice"},
	})
	putItem(t, srv, "txn-get", map[string]any{
		"id":   map[string]any{"S": "g-2"},
		"name": map[string]any{"S": "Bob"},
	})

	// When: TransactGetItems
	resp := ddbCall(t, srv, "TransactGetItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Get": map[string]any{
					"TableName": "txn-get",
					"Key":       map[string]any{"id": map[string]any{"S": "g-1"}},
				},
			},
			map[string]any{
				"Get": map[string]any{
					"TableName": "txn-get",
					"Key":       map[string]any{"id": map[string]any{"S": "g-2"}},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: both items returned in order
	var result struct {
		Responses []struct {
			Item map[string]map[string]any `json:"Item"`
		} `json:"Responses"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(result.Responses))
	}
	if result.Responses[0].Item["name"]["S"] != "Alice" {
		t.Errorf("expected Alice, got %v", result.Responses[0].Item)
	}
	if result.Responses[1].Item["name"]["S"] != "Bob" {
		t.Errorf("expected Bob, got %v", result.Responses[1].Item)
	}
}

func TestTransactGetItems_missingItem(t *testing.T) {
	// Given: a table with one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-getmiss")
	putItem(t, srv, "txn-getmiss", map[string]any{
		"id":   map[string]any{"S": "exists"},
		"name": map[string]any{"S": "Found"},
	})

	// When: TransactGetItems for existing + nonexistent item
	resp := ddbCall(t, srv, "TransactGetItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Get": map[string]any{
					"TableName": "txn-getmiss",
					"Key":       map[string]any{"id": map[string]any{"S": "exists"}},
				},
			},
			map[string]any{
				"Get": map[string]any{
					"TableName": "txn-getmiss",
					"Key":       map[string]any{"id": map[string]any{"S": "nope"}},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: success (missing items return empty response slot)
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Responses []struct {
			Item map[string]map[string]any `json:"Item"`
		} `json:"Responses"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(result.Responses))
	}
	if result.Responses[0].Item == nil {
		t.Error("expected first item to be found")
	}
	if result.Responses[1].Item != nil {
		t.Error("expected second item to be nil (not found)")
	}
}

func TestTransactGetItems_tableNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "TransactGetItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Get": map[string]any{
					"TableName": "nonexistent",
					"Key":       map[string]any{"id": map[string]any{"S": "x"}},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestTransactGetItems_multipleTablesSuccess(t *testing.T) {
	// Given: two tables with items
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-ga")
	createTable(t, srv, "txn-gb")
	putItem(t, srv, "txn-ga", map[string]any{
		"id":  map[string]any{"S": "a1"},
		"val": map[string]any{"S": "from-a"},
	})
	putItem(t, srv, "txn-gb", map[string]any{
		"id":  map[string]any{"S": "b1"},
		"val": map[string]any{"S": "from-b"},
	})

	// When: TransactGetItems across both tables
	resp := ddbCall(t, srv, "TransactGetItems", map[string]any{
		"TransactItems": []any{
			map[string]any{
				"Get": map[string]any{
					"TableName": "txn-ga",
					"Key":       map[string]any{"id": map[string]any{"S": "a1"}},
				},
			},
			map[string]any{
				"Get": map[string]any{
					"TableName": "txn-gb",
					"Key":       map[string]any{"id": map[string]any{"S": "b1"}},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Responses []struct {
			Item map[string]map[string]any `json:"Item"`
		} `json:"Responses"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(result.Responses))
	}
	if result.Responses[0].Item["val"]["S"] != "from-a" {
		t.Errorf("expected from-a, got %v", result.Responses[0].Item)
	}
	if result.Responses[1].Item["val"]["S"] != "from-b" {
		t.Errorf("expected from-b, got %v", result.Responses[1].Item)
	}
}

func TestTransactGetItems_tooManyItems(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "txn-glimit")

	items := make([]any, 101)
	for i := range items {
		items[i] = map[string]any{
			"Get": map[string]any{
				"TableName": "txn-glimit",
				"Key":       map[string]any{"id": map[string]any{"S": "x"}},
			},
		}
	}
	resp := ddbCall(t, srv, "TransactGetItems", map[string]any{
		"TransactItems": items,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- ListTables ------------------------------------------------------------

func TestListTables_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "ListTables", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		TableNames []string `json:"TableNames"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.TableNames) != 0 {
		t.Errorf("expected 0 tables, got %d", len(result.TableNames))
	}
}

func TestListTables_returnsAll(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "table-a")
	createTable(t, srv, "table-b")
	createTable(t, srv, "table-c")

	resp := ddbCall(t, srv, "ListTables", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		TableNames []string `json:"TableNames"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.TableNames) != 3 {
		t.Errorf("expected 3 tables, got %d", len(result.TableNames))
	}
}

// ---- UpdateTable: BillingMode -----------------------------------------------

func TestUpdateTable_BillingMode(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create table with PROVISIONED billing mode.
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName":            "billing-test",
		"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
		"BillingMode":          "PROVISIONED",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Update billing mode to PAY_PER_REQUEST.
	resp = ddbCall(t, srv, "UpdateTable", map[string]any{
		"TableName":   "billing-test",
		"BillingMode": "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TableDescription struct {
			BillingModeSummary struct {
				BillingMode string `json:"BillingMode"`
			} `json:"BillingModeSummary"`
		} `json:"TableDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.TableDescription.BillingModeSummary.BillingMode != "PAY_PER_REQUEST" {
		t.Errorf("expected BillingMode PAY_PER_REQUEST, got %q",
			result.TableDescription.BillingModeSummary.BillingMode)
	}

	// DescribeTable should also reflect the new billing mode.
	resp2 := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "billing-test"})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var desc struct {
		Table struct {
			BillingModeSummary struct {
				BillingMode string `json:"BillingMode"`
			} `json:"BillingModeSummary"`
		} `json:"Table"`
	}
	helpers.DecodeJSON(t, resp2, &desc)
	if desc.Table.BillingModeSummary.BillingMode != "PAY_PER_REQUEST" {
		t.Errorf("DescribeTable: expected BillingMode PAY_PER_REQUEST, got %q",
			desc.Table.BillingModeSummary.BillingMode)
	}
}

// ---- UpdateTable: GSI -------------------------------------------------------

func TestUpdateTable_AddGSI(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create table with only a hash key.
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "gsi-test",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "pk", "AttributeType": "S"},
		},
		"KeySchema":   []map[string]any{{"AttributeName": "pk", "KeyType": "HASH"}},
		"BillingMode": "PAY_PER_REQUEST",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Add a GSI via UpdateTable.
	resp = ddbCall(t, srv, "UpdateTable", map[string]any{
		"TableName": "gsi-test",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "pk", "AttributeType": "S"},
			{"AttributeName": "gsi_pk", "AttributeType": "S"},
		},
		"GlobalSecondaryIndexUpdates": []map[string]any{
			{
				"Create": map[string]any{
					"IndexName": "gsi-by-gpk",
					"KeySchema": []map[string]any{
						{"AttributeName": "gsi_pk", "KeyType": "HASH"},
					},
					"Projection": map[string]any{"ProjectionType": "ALL"},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TableDescription struct {
			GlobalSecondaryIndexes []struct {
				IndexName string `json:"IndexName"`
			} `json:"GlobalSecondaryIndexes"`
		} `json:"TableDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)
	found := false
	for _, gsi := range result.TableDescription.GlobalSecondaryIndexes {
		if gsi.IndexName == "gsi-by-gpk" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected GSI gsi-by-gpk, got %v", result.TableDescription.GlobalSecondaryIndexes)
	}
}

// ---- ReturnValues ----------------------------------------------------------

func TestPutItem_ReturnValues_ALL_OLD(t *testing.T) {
	// Given: an existing item in the table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rv-table")
	putItem(t, srv, "rv-table", map[string]any{
		"id":  map[string]string{"S": "item1"},
		"val": map[string]string{"S": "original"},
	})

	// When: PutItem with ReturnValues=ALL_OLD
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "rv-table",
		"Item": map[string]any{
			"id":  map[string]string{"S": "item1"},
			"val": map[string]string{"S": "updated"},
		},
		"ReturnValues": "ALL_OLD",
	})
	defer resp.Body.Close()

	// Then: 200 with the old item
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Attributes["val"]["S"] != "original" {
		t.Errorf("ReturnValues=ALL_OLD: expected val=original, got %v", result.Attributes["val"])
	}
}

func TestPutItem_ReturnValues_NONE(t *testing.T) {
	// Given: an existing item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rv-none-table")
	putItem(t, srv, "rv-none-table", map[string]any{
		"id": map[string]string{"S": "x"},
	})

	// When: PutItem with ReturnValues=NONE (default)
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "rv-none-table",
		"Item":      map[string]any{"id": map[string]string{"S": "x"}, "v": map[string]string{"S": "1"}},
	})
	defer resp.Body.Close()

	// Then: 200 with empty response (no Attributes key)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	helpers.DecodeJSON(t, resp, &result)
	if _, ok := result["Attributes"]; ok {
		t.Error("expected no Attributes in NONE response")
	}
}

func TestDeleteItem_ReturnValues_ALL_OLD(t *testing.T) {
	// Given: an item exists
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "del-rv-table")
	putItem(t, srv, "del-rv-table", map[string]any{
		"id":   map[string]string{"S": "d1"},
		"data": map[string]string{"S": "hello"},
	})

	// When: DeleteItem with ReturnValues=ALL_OLD
	resp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName":    "del-rv-table",
		"Key":          map[string]any{"id": map[string]string{"S": "d1"}},
		"ReturnValues": "ALL_OLD",
	})
	defer resp.Body.Close()

	// Then: 200 with the deleted item's attributes
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Attributes["data"]["S"] != "hello" {
		t.Errorf("DeleteItem ReturnValues=ALL_OLD: expected data=hello, got %v", result.Attributes["data"])
	}
}

func TestDeleteItem_ConditionExpression(t *testing.T) {
	// Given: an item with a status attribute
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "del-cond-table")
	putItem(t, srv, "del-cond-table", map[string]any{
		"id":     map[string]string{"S": "c1"},
		"status": map[string]string{"S": "inactive"},
	})

	// When: DeleteItem with a failing ConditionExpression
	resp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName":                 "del-cond-table",
		"Key":                       map[string]any{"id": map[string]string{"S": "c1"}},
		"ConditionExpression":       "#s = :active",
		"ExpressionAttributeNames":  map[string]string{"#s": "status"},
		"ExpressionAttributeValues": map[string]any{":active": map[string]string{"S": "active"}},
	})
	defer resp.Body.Close()

	// Then: 400 ConditionalCheckFailedException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	var result map[string]any
	helpers.DecodeJSON(t, resp, &result)
	if result["__type"] != "ConditionalCheckFailedException" {
		t.Errorf("expected ConditionalCheckFailedException, got %v", result["__type"])
	}

	// And: item still exists
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "del-cond-table",
		"Key":       map[string]any{"id": map[string]string{"S": "c1"}},
	})
	defer getResp.Body.Close()
	var getResult struct {
		Item map[string]map[string]string `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &getResult)
	if getResult.Item["id"]["S"] != "c1" {
		t.Error("item should still exist after failed conditional delete")
	}
}

func TestUpdateItem_ReturnValues_ALL_OLD(t *testing.T) {
	// Given: an item with field "score"
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-rv-table")
	putItem(t, srv, "upd-rv-table", map[string]any{
		"id":    map[string]string{"S": "u1"},
		"score": map[string]string{"N": "10"},
	})

	// When: UpdateItem with ReturnValues=ALL_OLD
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":                 "upd-rv-table",
		"Key":                       map[string]any{"id": map[string]string{"S": "u1"}},
		"UpdateExpression":          "SET score = :ns",
		"ExpressionAttributeValues": map[string]any{":ns": map[string]string{"N": "99"}},
		"ReturnValues":              "ALL_OLD",
	})
	defer resp.Body.Close()

	// Then: 200 with old item values
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Attributes["score"]["N"] != "10" {
		t.Errorf("ALL_OLD: expected score=10, got %v", result.Attributes["score"])
	}
}

func TestUpdateItem_ReturnValues_ALL_NEW(t *testing.T) {
	// Given: an item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-allnew-table")
	putItem(t, srv, "upd-allnew-table", map[string]any{
		"id":    map[string]string{"S": "u2"},
		"score": map[string]string{"N": "10"},
		"extra": map[string]string{"S": "keep"},
	})

	// When: UpdateItem with ReturnValues=ALL_NEW
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":                 "upd-allnew-table",
		"Key":                       map[string]any{"id": map[string]string{"S": "u2"}},
		"UpdateExpression":          "SET score = :ns",
		"ExpressionAttributeValues": map[string]any{":ns": map[string]string{"N": "99"}},
		"ReturnValues":              "ALL_NEW",
	})
	defer resp.Body.Close()

	// Then: 200 with full new item
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Attributes["score"]["N"] != "99" {
		t.Errorf("ALL_NEW: expected score=99, got %v", result.Attributes["score"])
	}
	if result.Attributes["extra"]["S"] != "keep" {
		t.Errorf("ALL_NEW: expected extra=keep, got %v", result.Attributes["extra"])
	}
}

func TestUpdateItem_ReturnValues_UPDATED_NEW(t *testing.T) {
	// Given: an item with multiple fields
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-updnew-table")
	putItem(t, srv, "upd-updnew-table", map[string]any{
		"id":        map[string]string{"S": "u3"},
		"score":     map[string]string{"N": "5"},
		"untouched": map[string]string{"S": "ignore"},
	})

	// When: UpdateItem with ReturnValues=UPDATED_NEW (only updated attrs)
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":                 "upd-updnew-table",
		"Key":                       map[string]any{"id": map[string]string{"S": "u3"}},
		"UpdateExpression":          "SET score = :ns",
		"ExpressionAttributeValues": map[string]any{":ns": map[string]string{"N": "50"}},
		"ReturnValues":              "UPDATED_NEW",
	})
	defer resp.Body.Close()

	// Then: Attributes contains only the updated field
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Attributes["score"]["N"] != "50" {
		t.Errorf("UPDATED_NEW: expected score=50, got %v", result.Attributes["score"])
	}
	if _, ok := result.Attributes["untouched"]; ok {
		t.Error("UPDATED_NEW: should not include untouched attribute")
	}
}

func TestUpdateItem_ReturnValues_UPDATED_OLD(t *testing.T) {
	// Given: an item with multiple fields
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-updold-table")
	putItem(t, srv, "upd-updold-table", map[string]any{
		"id":        map[string]string{"S": "u4"},
		"score":     map[string]string{"N": "7"},
		"untouched": map[string]string{"S": "ignore"},
	})

	// When: UpdateItem with ReturnValues=UPDATED_OLD
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":                 "upd-updold-table",
		"Key":                       map[string]any{"id": map[string]string{"S": "u4"}},
		"UpdateExpression":          "SET score = :ns",
		"ExpressionAttributeValues": map[string]any{":ns": map[string]string{"N": "70"}},
		"ReturnValues":              "UPDATED_OLD",
	})
	defer resp.Body.Close()

	// Then: Attributes contains only the old value of the updated field
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Attributes["score"]["N"] != "7" {
		t.Errorf("UPDATED_OLD: expected score=7, got %v", result.Attributes["score"])
	}
	if _, ok := result.Attributes["untouched"]; ok {
		t.Error("UPDATED_OLD: should not include untouched attribute")
	}
}

// ---- Scan Limit + pagination + Parallel Scan --------------------------------

func TestScan_Limit_Pagination(t *testing.T) {
	// Given: 5 items in a table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "scan-page-table")
	for i := 1; i <= 5; i++ {
		putItem(t, srv, "scan-page-table", map[string]any{
			"id": map[string]string{"S": fmt.Sprintf("item%d", i)},
		})
	}

	// When: Scan with Limit=2
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "scan-page-table",
		"Limit":     2,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var page1 struct {
		Items            []map[string]any `json:"Items"`
		Count            int              `json:"Count"`
		ScannedCount     int              `json:"ScannedCount"`
		LastEvaluatedKey map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp, &page1)
	if page1.Count != 2 {
		t.Fatalf("expected 2 items in page1, got %d", page1.Count)
	}
	if page1.LastEvaluatedKey == nil {
		t.Fatal("expected LastEvaluatedKey for more pages")
	}

	// When: Scan page 2
	resp2 := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":         "scan-page-table",
		"Limit":             2,
		"ExclusiveStartKey": page1.LastEvaluatedKey,
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var page2 struct {
		Items            []map[string]any `json:"Items"`
		Count            int              `json:"Count"`
		LastEvaluatedKey map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp2, &page2)
	if page2.Count != 2 {
		t.Fatalf("expected 2 items in page2, got %d", page2.Count)
	}
	// Then: ScannedCount reflects items evaluated (after Limit, 5 items available, but Limit=2 so only 2 evaluated)
	if page1.ScannedCount != 2 {
		t.Errorf("expected ScannedCount=2 (Limit=2 items evaluated), got %d", page1.ScannedCount)
	}
}

func TestScan_scannedCountWithoutFilter(t *testing.T) {
	// Given: 5 items in a table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "scan-sc-table")
	for i := 1; i <= 5; i++ {
		putItem(t, srv, "scan-sc-table", map[string]any{
			"id": map[string]string{"S": fmt.Sprintf("item%d", i)},
		})
	}

	// When: Scan with no Limit — all 5 items are evaluated
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "scan-sc-table",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Count        int `json:"Count"`
		ScannedCount int `json:"ScannedCount"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: Count and ScannedCount both return 5 (no filtering, no limit)
	if result.Count != 5 {
		t.Errorf("expected Count=5, got %d", result.Count)
	}
	if result.ScannedCount != 5 {
		t.Errorf("expected ScannedCount=5, got %d", result.ScannedCount)
	}
}

func TestScan_scannedCountWithFilter(t *testing.T) {
	// Given: 5 items; first 2 have flag="skip"
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "scan-filter-sc")
	for i := 1; i <= 5; i++ {
		flag := "keep"
		if i <= 2 {
			flag = "skip"
		}
		putItem(t, srv, "scan-filter-sc", map[string]any{
			"id":   map[string]any{"S": fmt.Sprintf("item%d", i)},
			"flag": map[string]any{"S": flag},
		})
	}

	// When: Scan with a filter expression
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "scan-filter-sc",
		"FilterExpression": "flag = :v",
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]any{"S": "keep"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Count        int `json:"Count"`
		ScannedCount int `json:"ScannedCount"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: Count reflects items AFTER filtering (3 keep items)
	if result.Count != 3 {
		t.Errorf("expected Count=3, got %d", result.Count)
	}
	// Then: ScannedCount reflects items EVALUATED before filter (all 5)
	if result.ScannedCount != 5 {
		t.Errorf("expected ScannedCount=5, got %d", result.ScannedCount)
	}
}

func TestScan_scannedCountWithLimitAndFilter(t *testing.T) {
	// Given: 5 items; only items 3-5 match the filter
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "scan-limit-filter-sc")
	for i := 1; i <= 5; i++ {
		flag := "keep"
		if i <= 2 {
			flag = "skip"
		}
		putItem(t, srv, "scan-limit-filter-sc", map[string]any{
			"id":   map[string]any{"S": fmt.Sprintf("item%d", i)},
			"flag": map[string]any{"S": flag},
		})
	}

	// When: Scan evaluates only the first 3 items and then applies the filter
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "scan-limit-filter-sc",
		"Limit":            3,
		"FilterExpression": "flag = :v",
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]any{"S": "keep"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Count            int            `json:"Count"`
		ScannedCount     int            `json:"ScannedCount"`
		LastEvaluatedKey map[string]any `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: ScannedCount reflects the 3 evaluated items, while Count reflects the 1 matching item
	if result.Count != 1 {
		t.Errorf("expected Count=1, got %d", result.Count)
	}
	if result.ScannedCount != 3 {
		t.Errorf("expected ScannedCount=3, got %d", result.ScannedCount)
	}
	if result.LastEvaluatedKey == nil {
		t.Error("expected LastEvaluatedKey when Limit stops scan before the table is exhausted")
	}
}

func TestScan_ParallelScan(t *testing.T) {
	// Given: 6 items in a table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "parallel-scan-table")
	for i := 1; i <= 6; i++ {
		putItem(t, srv, "parallel-scan-table", map[string]any{
			"id": map[string]string{"S": fmt.Sprintf("item%d", i)},
		})
	}

	// When: two parallel workers each scan half
	resp0 := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":     "parallel-scan-table",
		"TotalSegments": 2,
		"Segment":       0,
	})
	defer resp0.Body.Close()
	helpers.AssertStatus(t, resp0, http.StatusOK)

	resp1 := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":     "parallel-scan-table",
		"TotalSegments": 2,
		"Segment":       1,
	})
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)

	var r0, r1 struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, resp0, &r0)
	helpers.DecodeJSON(t, resp1, &r1)

	// Then: combined, all 6 items are returned with no duplicates
	total := r0.Count + r1.Count
	if total != 6 {
		t.Errorf("parallel scan should cover all 6 items total, got seg0=%d seg1=%d", r0.Count, r1.Count)
	}
	// Verify no overlap
	seen := map[string]bool{}
	for _, item := range append(r0.Items, r1.Items...) {
		id := item["id"].(map[string]any)["S"].(string)
		if seen[id] {
			t.Errorf("duplicate item %q in parallel scan", id)
		}
		seen[id] = true
	}
}

func TestUpdateTable_UpdateGSI_ProvisionedThroughput(t *testing.T) {
	// Given: a PROVISIONED table with a GSI
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "gsi-throughput-test",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "pk", "AttributeType": "S"},
			{"AttributeName": "gsi_pk", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "pk", "KeyType": "HASH"},
		},
		"GlobalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "gsi-by-gpk",
				"KeySchema": []map[string]any{
					{"AttributeName": "gsi_pk", "KeyType": "HASH"},
				},
				"Projection":            map[string]any{"ProjectionType": "ALL"},
				"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": 5, "WriteCapacityUnits": 5},
			},
		},
		"BillingMode": "PROVISIONED",
		"ProvisionedThroughput": map[string]any{
			"ReadCapacityUnits":  5,
			"WriteCapacityUnits": 5,
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: Update the GSI's provisioned throughput
	resp = ddbCall(t, srv, "UpdateTable", map[string]any{
		"TableName": "gsi-throughput-test",
		"GlobalSecondaryIndexUpdates": []map[string]any{
			{
				"Update": map[string]any{
					"IndexName": "gsi-by-gpk",
					"ProvisionedThroughput": map[string]any{
						"ReadCapacityUnits":  10,
						"WriteCapacityUnits": 8,
					},
				},
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeTable reflects the new throughput on the GSI
	descResp := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "gsi-throughput-test"})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)

	var result struct {
		Table struct {
			GlobalSecondaryIndexes []struct {
				IndexName             string `json:"IndexName"`
				ProvisionedThroughput struct {
					ReadCapacityUnits  int `json:"ReadCapacityUnits"`
					WriteCapacityUnits int `json:"WriteCapacityUnits"`
				} `json:"ProvisionedThroughput"`
			} `json:"GlobalSecondaryIndexes"`
		} `json:"Table"`
	}
	helpers.DecodeJSON(t, descResp, &result)

	if len(result.Table.GlobalSecondaryIndexes) != 1 {
		t.Fatalf("expected 1 GSI, got %d", len(result.Table.GlobalSecondaryIndexes))
	}
	gsi := result.Table.GlobalSecondaryIndexes[0]
	if gsi.ProvisionedThroughput.ReadCapacityUnits != 10 {
		t.Errorf("GSI ReadCapacityUnits = %d, want 10", gsi.ProvisionedThroughput.ReadCapacityUnits)
	}
	if gsi.ProvisionedThroughput.WriteCapacityUnits != 8 {
		t.Errorf("GSI WriteCapacityUnits = %d, want 8", gsi.ProvisionedThroughput.WriteCapacityUnits)
	}
}

func TestUpdateTable_ProvisionedThroughput(t *testing.T) {
	// Given: a PROVISIONED table
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "throughput-test",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "pk", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "pk", "KeyType": "HASH"},
		},
		"BillingMode": "PROVISIONED",
		"ProvisionedThroughput": map[string]any{
			"ReadCapacityUnits":  5,
			"WriteCapacityUnits": 5,
		},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: UpdateTable with new provisioned throughput
	resp = ddbCall(t, srv, "UpdateTable", map[string]any{
		"TableName": "throughput-test",
		"ProvisionedThroughput": map[string]any{
			"ReadCapacityUnits":  20,
			"WriteCapacityUnits": 10,
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeTable reflects the new throughput
	descResp := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "throughput-test"})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)

	var result struct {
		Table struct {
			ProvisionedThroughput struct {
				ReadCapacityUnits  int `json:"ReadCapacityUnits"`
				WriteCapacityUnits int `json:"WriteCapacityUnits"`
			} `json:"ProvisionedThroughput"`
		} `json:"Table"`
	}
	helpers.DecodeJSON(t, descResp, &result)

	if result.Table.ProvisionedThroughput.ReadCapacityUnits != 20 {
		t.Errorf("ReadCapacityUnits = %d, want 20", result.Table.ProvisionedThroughput.ReadCapacityUnits)
	}
	if result.Table.ProvisionedThroughput.WriteCapacityUnits != 10 {
		t.Errorf("WriteCapacityUnits = %d, want 10", result.Table.ProvisionedThroughput.WriteCapacityUnits)
	}
}

// ---- Test helpers ----------------------------------------------------------

func ddbCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", operation, err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810."+operation)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ddbCall %s: %v", operation, err)
	}
	return resp
}

func createTable(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName":            name,
		"AttributeDefinitions": []map[string]any{{"AttributeName": "id", "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": "id", "KeyType": "HASH"}},
		"BillingMode":          "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := helpers.ReadBody(t, resp)
		t.Fatalf("createTable %q: status %d: %s", name, resp.StatusCode, body)
	}
}

func createTableWithSortKey(t *testing.T, srv *helpers.TestServer, name, hashKey, hashType, sortKey, sortType string) {
	t.Helper()
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": name,
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": hashKey, "AttributeType": hashType},
			{"AttributeName": sortKey, "AttributeType": sortType},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": hashKey, "KeyType": "HASH"},
			{"AttributeName": sortKey, "KeyType": "RANGE"},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createTableWithSortKey %q: status %d", name, resp.StatusCode)
	}
}

func putItem(t *testing.T, srv *helpers.TestServer, tableName string, item map[string]any) {
	t.Helper()
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": tableName,
		"Item":      item,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("putItem into %q: status %d", tableName, resp.StatusCode)
	}
}

// ---- UpdateItem ------------------------------------------------------------

func TestUpdateItem_setNewAttribute(t *testing.T) {
	// Given: a table with one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-table")
	putItem(t, srv, "upd-table", map[string]any{
		"id":   map[string]any{"S": "item-1"},
		"name": map[string]any{"S": "Alice"},
	})

	// When: UpdateItem sets a new attribute
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-table",
		"Key": map[string]any{
			"id": map[string]any{"S": "item-1"},
		},
		"UpdateExpression":          "SET #a = :v",
		"ExpressionAttributeNames":  map[string]any{"#a": "age"},
		"ExpressionAttributeValues": map[string]any{":v": map[string]any{"N": "30"}},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the attribute is visible in GetItem
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-table",
		"Key":       map[string]any{"id": map[string]any{"S": "item-1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["age"]["N"] != "30" {
		t.Errorf("expected age=30 after update, got %v", result.Item["age"])
	}
	if result.Item["name"]["S"] != "Alice" {
		t.Errorf("expected name=Alice to be preserved, got %v", result.Item["name"])
	}
}

func TestUpdateItem_overwriteExistingAttribute(t *testing.T) {
	// Given: a table with one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-table2")
	putItem(t, srv, "upd-table2", map[string]any{
		"id":    map[string]any{"S": "x"},
		"score": map[string]any{"N": "10"},
	})

	// When: UpdateItem overwrites an existing attribute
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":        "upd-table2",
		"Key":              map[string]any{"id": map[string]any{"S": "x"}},
		"UpdateExpression": "SET score = :s",
		"ExpressionAttributeValues": map[string]any{
			":s": map[string]any{"N": "99"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: score is updated
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-table2",
		"Key":       map[string]any{"id": map[string]any{"S": "x"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["score"]["N"] != "99" {
		t.Errorf("expected score=99, got %v", result.Item["score"])
	}
}

func TestUpdateItem_removeAttribute(t *testing.T) {
	// Given: a table with one item with an extra attribute
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-table3")
	putItem(t, srv, "upd-table3", map[string]any{
		"id":    map[string]any{"S": "y"},
		"extra": map[string]any{"S": "delete-me"},
	})

	// When: UpdateItem removes the attribute
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":        "upd-table3",
		"Key":              map[string]any{"id": map[string]any{"S": "y"}},
		"UpdateExpression": "REMOVE extra",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: extra is gone
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-table3",
		"Key":       map[string]any{"id": map[string]any{"S": "y"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if _, exists := result.Item["extra"]; exists {
		t.Errorf("expected extra attribute to be removed, but it still exists: %v", result.Item["extra"])
	}
}

func TestUpdateItem_createItemIfNotExists(t *testing.T) {
	// Given: an empty table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-table4")

	// When: UpdateItem targets a key that does not exist
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":        "upd-table4",
		"Key":              map[string]any{"id": map[string]any{"S": "new-key"}},
		"UpdateExpression": "SET val = :v",
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]any{"S": "created"},
		},
	})
	defer resp.Body.Close()

	// Then: 200 — item is created (upsert semantics)
	helpers.AssertStatus(t, resp, http.StatusOK)

	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-table4",
		"Key":       map[string]any{"id": map[string]any{"S": "new-key"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["val"]["S"] != "created" {
		t.Errorf("expected val=created after upsert, got %v", result.Item["val"])
	}
}

// ---- TTL -------------------------------------------------------------------

func TestUpdateTimeToLive_enable(t *testing.T) {
	// Given: a table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "ttl-table")

	// When: enable TTL
	resp := ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-table",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "expiresAt",
		},
	})
	defer resp.Body.Close()

	// Then: 200 OK with the specification echoed back
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TimeToLiveSpecification struct {
			Enabled       bool   `json:"Enabled"`
			AttributeName string `json:"AttributeName"`
		} `json:"TimeToLiveSpecification"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if !result.TimeToLiveSpecification.Enabled {
		t.Error("expected TTL to be enabled")
	}
	if result.TimeToLiveSpecification.AttributeName != "expiresAt" {
		t.Errorf("expected AttributeName 'expiresAt', got %q", result.TimeToLiveSpecification.AttributeName)
	}
}

func TestDescribeTimeToLive_disabled(t *testing.T) {
	// Given: a table with no TTL configured
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "no-ttl")

	// When: describe TTL
	resp := ddbCall(t, srv, "DescribeTimeToLive", map[string]any{
		"TableName": "no-ttl",
	})
	defer resp.Body.Close()

	// Then: returns DISABLED status
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TimeToLiveDescription struct {
			TimeToLiveStatus string `json:"TimeToLiveStatus"`
		} `json:"TimeToLiveDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.TimeToLiveDescription.TimeToLiveStatus != "DISABLED" {
		t.Errorf("expected DISABLED, got %q", result.TimeToLiveDescription.TimeToLiveStatus)
	}
}

func TestDescribeTimeToLive_enabled(t *testing.T) {
	// Given: a table with TTL enabled
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "ttl-desc")

	resp := ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-desc",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "ttl",
		},
	})
	resp.Body.Close()

	// When: describe TTL
	resp = ddbCall(t, srv, "DescribeTimeToLive", map[string]any{
		"TableName": "ttl-desc",
	})
	defer resp.Body.Close()

	// Then: returns ENABLED status with attribute name
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TimeToLiveDescription struct {
			TimeToLiveStatus string `json:"TimeToLiveStatus"`
			AttributeName    string `json:"AttributeName"`
		} `json:"TimeToLiveDescription"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.TimeToLiveDescription.TimeToLiveStatus != "ENABLED" {
		t.Errorf("expected ENABLED, got %q", result.TimeToLiveDescription.TimeToLiveStatus)
	}
	if result.TimeToLiveDescription.AttributeName != "ttl" {
		t.Errorf("expected AttributeName 'ttl', got %q", result.TimeToLiveDescription.AttributeName)
	}
}

func TestUpdateTimeToLive_disable(t *testing.T) {
	// Given: a table with TTL enabled
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "ttl-disable")

	resp := ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-disable",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "expires",
		},
	})
	resp.Body.Close()

	// When: disable TTL
	resp = ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-disable",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       false,
			"AttributeName": "expires",
		},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: DescribeTimeToLive shows DISABLED
	descResp := ddbCall(t, srv, "DescribeTimeToLive", map[string]any{
		"TableName": "ttl-disable",
	})
	defer descResp.Body.Close()

	var result struct {
		TimeToLiveDescription struct {
			TimeToLiveStatus string `json:"TimeToLiveStatus"`
		} `json:"TimeToLiveDescription"`
	}
	helpers.DecodeJSON(t, descResp, &result)

	if result.TimeToLiveDescription.TimeToLiveStatus != "DISABLED" {
		t.Errorf("expected DISABLED after disabling, got %q", result.TimeToLiveDescription.TimeToLiveStatus)
	}
}

func TestUpdateTimeToLive_tableNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "nonexistent",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "ttl",
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestUpdateTimeToLive_conflictingAttribute(t *testing.T) {
	// Given: TTL is enabled with attribute "ttl"
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "ttl-conflict")

	resp := ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-conflict",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "ttl",
		},
	})
	resp.Body.Close()

	// When: try to enable TTL with a different attribute
	resp = ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-conflict",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "expires",
		},
	})
	defer resp.Body.Close()

	// Then: validation error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- BatchGetItem ----------------------------------------------------------

func TestBatchGetItem_success(t *testing.T) {
	// Given: a table with three items
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "batch-get")
	putItem(t, srv, "batch-get", map[string]any{
		"id":   map[string]any{"S": "a"},
		"name": map[string]any{"S": "Alice"},
	})
	putItem(t, srv, "batch-get", map[string]any{
		"id":   map[string]any{"S": "b"},
		"name": map[string]any{"S": "Bob"},
	})
	putItem(t, srv, "batch-get", map[string]any{
		"id":   map[string]any{"S": "c"},
		"name": map[string]any{"S": "Charlie"},
	})

	// When: batch-get items a and c
	resp := ddbCall(t, srv, "BatchGetItem", map[string]any{
		"RequestItems": map[string]any{
			"batch-get": map[string]any{
				"Keys": []map[string]any{
					{"id": map[string]any{"S": "a"}},
					{"id": map[string]any{"S": "c"}},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: both items returned
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Responses map[string][]map[string]map[string]any `json:"Responses"`
	}
	helpers.DecodeJSON(t, resp, &result)
	items := result.Responses["batch-get"]
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestBatchGetItem_multipleTablesAndMissingKeys(t *testing.T) {
	// Given: two tables with different items
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "tbl-1")
	createTable(t, srv, "tbl-2")
	putItem(t, srv, "tbl-1", map[string]any{
		"id": map[string]any{"S": "x"},
	})
	putItem(t, srv, "tbl-2", map[string]any{
		"id":  map[string]any{"S": "y"},
		"val": map[string]any{"N": "42"},
	})

	// When: batch-get across both tables, including a missing key
	resp := ddbCall(t, srv, "BatchGetItem", map[string]any{
		"RequestItems": map[string]any{
			"tbl-1": map[string]any{
				"Keys": []map[string]any{
					{"id": map[string]any{"S": "x"}},
					{"id": map[string]any{"S": "missing"}},
				},
			},
			"tbl-2": map[string]any{
				"Keys": []map[string]any{
					{"id": map[string]any{"S": "y"}},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: found items returned, missing key silently skipped
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Responses map[string][]map[string]map[string]any `json:"Responses"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Responses["tbl-1"]) != 1 {
		t.Errorf("expected 1 item from tbl-1, got %d", len(result.Responses["tbl-1"]))
	}
	if len(result.Responses["tbl-2"]) != 1 {
		t.Errorf("expected 1 item from tbl-2, got %d", len(result.Responses["tbl-2"]))
	}
}

func TestBatchGetItem_tableNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "BatchGetItem", map[string]any{
		"RequestItems": map[string]any{
			"nonexistent": map[string]any{
				"Keys": []map[string]any{
					{"id": map[string]any{"S": "a"}},
				},
			},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- BatchWriteItem --------------------------------------------------------

func TestBatchWriteItem_putAndDelete(t *testing.T) {
	// Given: a table with one existing item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "batch-write")
	putItem(t, srv, "batch-write", map[string]any{
		"id":   map[string]any{"S": "delete-me"},
		"data": map[string]any{"S": "old"},
	})

	// When: batch-write puts two new items and deletes the existing one
	resp := ddbCall(t, srv, "BatchWriteItem", map[string]any{
		"RequestItems": map[string]any{
			"batch-write": []map[string]any{
				{"PutRequest": map[string]any{
					"Item": map[string]any{
						"id":   map[string]any{"S": "new-1"},
						"data": map[string]any{"S": "first"},
					},
				}},
				{"PutRequest": map[string]any{
					"Item": map[string]any{
						"id":   map[string]any{"S": "new-2"},
						"data": map[string]any{"S": "second"},
					},
				}},
				{"DeleteRequest": map[string]any{
					"Key": map[string]any{
						"id": map[string]any{"S": "delete-me"},
					},
				}},
			},
		},
	})
	defer resp.Body.Close()

	// Then: operation succeeds
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify: new items exist
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "batch-write",
		"Key":       map[string]any{"id": map[string]any{"S": "new-1"}},
	})
	defer getResp.Body.Close()
	var r1 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, getResp, &r1)
	if r1.Item == nil {
		t.Error("expected new-1 to exist after batch write")
	}

	// Verify: deleted item is gone
	getResp2 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "batch-write",
		"Key":       map[string]any{"id": map[string]any{"S": "delete-me"}},
	})
	defer getResp2.Body.Close()
	var r2 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, getResp2, &r2)
	if r2.Item != nil {
		t.Error("expected delete-me to be gone after batch write")
	}
}

func TestBatchWriteItem_multipleTablesSuccess(t *testing.T) {
	// Given: two tables
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "bw-t1")
	createTable(t, srv, "bw-t2")

	// When: batch write across both tables
	resp := ddbCall(t, srv, "BatchWriteItem", map[string]any{
		"RequestItems": map[string]any{
			"bw-t1": []map[string]any{
				{"PutRequest": map[string]any{
					"Item": map[string]any{"id": map[string]any{"S": "a"}},
				}},
			},
			"bw-t2": []map[string]any{
				{"PutRequest": map[string]any{
					"Item": map[string]any{"id": map[string]any{"S": "b"}},
				}},
			},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify items landed in respective tables
	g1 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "bw-t1",
		"Key":       map[string]any{"id": map[string]any{"S": "a"}},
	})
	defer g1.Body.Close()
	var res1 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, g1, &res1)
	if res1.Item == nil {
		t.Error("expected item 'a' in bw-t1")
	}

	g2 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "bw-t2",
		"Key":       map[string]any{"id": map[string]any{"S": "b"}},
	})
	defer g2.Body.Close()
	var res2 struct{ Item map[string]map[string]any }
	helpers.DecodeJSON(t, g2, &res2)
	if res2.Item == nil {
		t.Error("expected item 'b' in bw-t2")
	}
}

func TestBatchWriteItem_tableNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "BatchWriteItem", map[string]any{
		"RequestItems": map[string]any{
			"nonexistent": []map[string]any{
				{"PutRequest": map[string]any{
					"Item": map[string]any{"id": map[string]any{"S": "a"}},
				}},
			},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestBatchWriteItem_tooManyItems(t *testing.T) {
	// Given: a table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "bw-limit")

	// When: submit 26 requests (limit is 25)
	items := make([]map[string]any, 26)
	for i := range items {
		items[i] = map[string]any{
			"PutRequest": map[string]any{
				"Item": map[string]any{
					"id": map[string]any{"S": "item-" + strconv.Itoa(i)},
				},
			},
		}
	}

	resp := ddbCall(t, srv, "BatchWriteItem", map[string]any{
		"RequestItems": map[string]any{
			"bw-limit": items,
		},
	})
	defer resp.Body.Close()

	// Then: validation error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- GSI / LSI -------------------------------------------------------------

func TestCreateTable_withGSI(t *testing.T) {
	// Given: nothing
	srv := helpers.NewTestServer(t)

	// When: create table with a GSI
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "gsi-table",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
			{"AttributeName": "email", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"GlobalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "email-index",
				"KeySchema": []map[string]any{
					{"AttributeName": "email", "KeyType": "HASH"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()

	// Then: table is created and DescribeTable includes GSI info
	helpers.AssertStatus(t, resp, http.StatusOK)

	descResp := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "gsi-table"})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)

	var result struct {
		Table struct {
			GlobalSecondaryIndexes []struct {
				IndexName  string `json:"IndexName"`
				IndexArn   string `json:"IndexArn"`
				KeySchema  []struct{ AttributeName, KeyType string }
				Projection struct{ ProjectionType string }
			} `json:"GlobalSecondaryIndexes"`
		} `json:"Table"`
	}
	helpers.DecodeJSON(t, descResp, &result)

	if len(result.Table.GlobalSecondaryIndexes) != 1 {
		t.Fatalf("expected 1 GSI, got %d", len(result.Table.GlobalSecondaryIndexes))
	}
	gsi := result.Table.GlobalSecondaryIndexes[0]
	if gsi.IndexName != "email-index" {
		t.Errorf("GSI name = %q, want %q", gsi.IndexName, "email-index")
	}
	if gsi.IndexArn == "" {
		t.Error("GSI IndexArn should not be empty")
	}
	if gsi.Projection.ProjectionType != "ALL" {
		t.Errorf("projection = %q, want %q", gsi.Projection.ProjectionType, "ALL")
	}
}

func TestCreateTable_withLSI(t *testing.T) {
	// Given: nothing
	srv := helpers.NewTestServer(t)

	// When: create table with composite key and an LSI
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "lsi-table",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "pk", "AttributeType": "S"},
			{"AttributeName": "sk", "AttributeType": "S"},
			{"AttributeName": "createdAt", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "pk", "KeyType": "HASH"},
			{"AttributeName": "sk", "KeyType": "RANGE"},
		},
		"LocalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "created-index",
				"KeySchema": []map[string]any{
					{"AttributeName": "pk", "KeyType": "HASH"},
					{"AttributeName": "createdAt", "KeyType": "RANGE"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	descResp := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "lsi-table"})
	defer descResp.Body.Close()
	helpers.AssertStatus(t, descResp, http.StatusOK)

	var result struct {
		Table struct {
			LocalSecondaryIndexes []struct {
				IndexName  string `json:"IndexName"`
				IndexArn   string `json:"IndexArn"`
				KeySchema  []struct{ AttributeName, KeyType string }
				Projection struct{ ProjectionType string }
			} `json:"LocalSecondaryIndexes"`
		} `json:"Table"`
	}
	helpers.DecodeJSON(t, descResp, &result)

	if len(result.Table.LocalSecondaryIndexes) != 1 {
		t.Fatalf("expected 1 LSI, got %d", len(result.Table.LocalSecondaryIndexes))
	}
	lsi := result.Table.LocalSecondaryIndexes[0]
	if lsi.IndexName != "created-index" {
		t.Errorf("LSI name = %q, want %q", lsi.IndexName, "created-index")
	}
}

func TestQuery_GSI(t *testing.T) {
	// Given: a table with a GSI on "email" and some items
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "users-gsi",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
			{"AttributeName": "email", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"GlobalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "email-index",
				"KeySchema": []map[string]any{
					{"AttributeName": "email", "KeyType": "HASH"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	resp.Body.Close()

	putItem(t, srv, "users-gsi", map[string]any{
		"id":    map[string]string{"S": "u1"},
		"email": map[string]string{"S": "alice@example.com"},
		"name":  map[string]string{"S": "Alice"},
	})
	putItem(t, srv, "users-gsi", map[string]any{
		"id":    map[string]string{"S": "u2"},
		"email": map[string]string{"S": "bob@example.com"},
		"name":  map[string]string{"S": "Bob"},
	})
	putItem(t, srv, "users-gsi", map[string]any{
		"id":    map[string]string{"S": "u3"},
		"email": map[string]string{"S": "alice@example.com"},
		"name":  map[string]string{"S": "Alice2"},
	})

	// When: query the GSI by email
	qResp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "users-gsi",
		"IndexName":              "email-index",
		"KeyConditionExpression": "email = :e",
		"ExpressionAttributeValues": map[string]any{
			":e": map[string]string{"S": "alice@example.com"},
		},
	})
	defer qResp.Body.Close()

	// Then: returns the 2 items with that email
	helpers.AssertStatus(t, qResp, http.StatusOK)
	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, qResp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 items via GSI, got %d", result.Count)
	}
}

func TestQuery_GSI_withSortKey(t *testing.T) {
	// Given: a table with a GSI having hash+sort key
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "orders-gsi",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "orderId", "AttributeType": "S"},
			{"AttributeName": "customerId", "AttributeType": "S"},
			{"AttributeName": "orderDate", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "orderId", "KeyType": "HASH"},
		},
		"GlobalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "customer-date-index",
				"KeySchema": []map[string]any{
					{"AttributeName": "customerId", "KeyType": "HASH"},
					{"AttributeName": "orderDate", "KeyType": "RANGE"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	resp.Body.Close()

	putItem(t, srv, "orders-gsi", map[string]any{
		"orderId":    map[string]string{"S": "o1"},
		"customerId": map[string]string{"S": "c1"},
		"orderDate":  map[string]string{"S": "2024-01-01"},
	})
	putItem(t, srv, "orders-gsi", map[string]any{
		"orderId":    map[string]string{"S": "o2"},
		"customerId": map[string]string{"S": "c1"},
		"orderDate":  map[string]string{"S": "2024-02-01"},
	})
	putItem(t, srv, "orders-gsi", map[string]any{
		"orderId":    map[string]string{"S": "o3"},
		"customerId": map[string]string{"S": "c2"},
		"orderDate":  map[string]string{"S": "2024-01-15"},
	})

	// When: query the GSI by customerId + orderDate
	qResp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "orders-gsi",
		"IndexName":              "customer-date-index",
		"KeyConditionExpression": "customerId = :c AND orderDate = :d",
		"ExpressionAttributeValues": map[string]any{
			":c": map[string]string{"S": "c1"},
			":d": map[string]string{"S": "2024-01-01"},
		},
	})
	defer qResp.Body.Close()

	// Then: returns only the one matching item
	helpers.AssertStatus(t, qResp, http.StatusOK)
	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, qResp, &result)
	if result.Count != 1 {
		t.Errorf("expected 1 item via GSI hash+sort, got %d", result.Count)
	}
}

func TestQuery_LSI(t *testing.T) {
	// Given: a table with composite key (pk, sk) and LSI on (pk, createdAt)
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "posts-lsi",
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
				"IndexName": "created-index",
				"KeySchema": []map[string]any{
					{"AttributeName": "forumId", "KeyType": "HASH"},
					{"AttributeName": "createdAt", "KeyType": "RANGE"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	resp.Body.Close()

	putItem(t, srv, "posts-lsi", map[string]any{
		"forumId":   map[string]string{"S": "f1"},
		"postId":    map[string]string{"S": "p1"},
		"createdAt": map[string]string{"S": "2024-03-01"},
		"title":     map[string]string{"S": "First"},
	})
	putItem(t, srv, "posts-lsi", map[string]any{
		"forumId":   map[string]string{"S": "f1"},
		"postId":    map[string]string{"S": "p2"},
		"createdAt": map[string]string{"S": "2024-03-02"},
		"title":     map[string]string{"S": "Second"},
	})
	putItem(t, srv, "posts-lsi", map[string]any{
		"forumId":   map[string]string{"S": "f2"},
		"postId":    map[string]string{"S": "p3"},
		"createdAt": map[string]string{"S": "2024-03-01"},
		"title":     map[string]string{"S": "Other"},
	})

	// When: query the LSI to find f1 posts by createdAt
	qResp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "posts-lsi",
		"IndexName":              "created-index",
		"KeyConditionExpression": "forumId = :f AND createdAt = :c",
		"ExpressionAttributeValues": map[string]any{
			":f": map[string]string{"S": "f1"},
			":c": map[string]string{"S": "2024-03-01"},
		},
	})
	defer qResp.Body.Close()

	// Then: returns 1 item (forumId=f1, createdAt=2024-03-01)
	helpers.AssertStatus(t, qResp, http.StatusOK)
	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, qResp, &result)
	if result.Count != 1 {
		t.Errorf("expected 1 item via LSI, got %d", result.Count)
	}
}

func TestQuery_indexNotFound(t *testing.T) {
	// Given: a table with no indexes
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "no-idx")

	// When: query with a nonexistent IndexName
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "no-idx",
		"IndexName":              "nonexistent-index",
		"KeyConditionExpression": "id = :v",
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]string{"S": "x"},
		},
	})
	defer resp.Body.Close()

	// Then: 400 ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestScan_GSI(t *testing.T) {
	// Given: a table with a GSI on "status" — some items have the attribute, some don't
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "scan-gsi",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
			{"AttributeName": "status", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"GlobalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "status-index",
				"KeySchema": []map[string]any{
					{"AttributeName": "status", "KeyType": "HASH"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	resp.Body.Close()

	putItem(t, srv, "scan-gsi", map[string]any{
		"id":     map[string]string{"S": "1"},
		"status": map[string]string{"S": "active"},
	})
	putItem(t, srv, "scan-gsi", map[string]any{
		"id": map[string]string{"S": "2"},
		// no "status" attribute — should be excluded from GSI scan
	})
	putItem(t, srv, "scan-gsi", map[string]any{
		"id":     map[string]string{"S": "3"},
		"status": map[string]string{"S": "inactive"},
	})

	// When: scan the GSI
	sResp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "scan-gsi",
		"IndexName": "status-index",
	})
	defer sResp.Body.Close()

	// Then: only items with "status" attribute are returned
	helpers.AssertStatus(t, sResp, http.StatusOK)
	var result struct {
		Items []map[string]any `json:"Items"`
		Count int              `json:"Count"`
	}
	helpers.DecodeJSON(t, sResp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 items in GSI scan (excluding item without GSI key), got %d", result.Count)
	}
}

func TestTTL_expiredItemsAreDeleted(t *testing.T) {
	// Given: a table with TTL enabled and an item with TTL in the past
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "ttl-expire")

	resp := ddbCall(t, srv, "UpdateTimeToLive", map[string]any{
		"TableName": "ttl-expire",
		"TimeToLiveSpecification": map[string]any{
			"Enabled":       true,
			"AttributeName": "ttl",
		},
	})
	resp.Body.Close()

	// Put an item that will expire 1 second from "now"
	nowUnix := srv.Clock.Now().Unix()
	putItem(t, srv, "ttl-expire", map[string]any{
		"id":  map[string]any{"S": "expire-me"},
		"ttl": map[string]any{"N": strconv.FormatInt(nowUnix+1, 10)},
	})

	// Put an item with no TTL attribute (should survive)
	putItem(t, srv, "ttl-expire", map[string]any{
		"id":   map[string]any{"S": "keep-me"},
		"data": map[string]any{"S": "persistent"},
	})

	// Put an item with TTL far in the future (should survive)
	putItem(t, srv, "ttl-expire", map[string]any{
		"id":  map[string]any{"S": "future"},
		"ttl": map[string]any{"N": strconv.FormatInt(nowUnix+86400, 10)},
	})

	// When: advance clock past TTL expiry and trigger the sweeper tick.
	// The mock clock controls both Now() (for expiry checks) and the ticker
	// (for sweep scheduling), so no real sleep is needed.
	srv.Clock.Add(2 * time.Hour) // past the 1h sweep interval + item TTL
	// Give the sweeper goroutine a moment to process the tick.
	time.Sleep(200 * time.Millisecond)

	// Then: expired item is gone
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "ttl-expire",
		"Key":       map[string]any{"id": map[string]any{"S": "expire-me"}},
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)

	var expiredResult struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &expiredResult)
	if expiredResult.Item != nil {
		t.Errorf("expected expired item to be deleted, but got %v", expiredResult.Item)
	}

	// And: non-TTL item survives
	getResp2 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "ttl-expire",
		"Key":       map[string]any{"id": map[string]any{"S": "keep-me"}},
	})
	defer getResp2.Body.Close()

	var keepResult struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp2, &keepResult)
	if keepResult.Item == nil {
		t.Error("expected non-TTL item to survive, but it was deleted")
	}

	// And: future-TTL item survives
	getResp3 := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "ttl-expire",
		"Key":       map[string]any{"id": map[string]any{"S": "future"}},
	})
	defer getResp3.Body.Close()

	var futureResult struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp3, &futureResult)
	if futureResult.Item == nil {
		t.Error("expected future-TTL item to survive, but it was deleted")
	}
}

// ---- Query with Limit and Pagination ---------------------------------------

func TestQuery_withLimit(t *testing.T) {
	// Given: a table with 4 items sharing the same hash key
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-limit", "pk", "S", "sk", "S")
	for _, sk := range []string{"item#1", "item#2", "item#3", "item#4"} {
		putItem(t, srv, "q-limit", map[string]any{
			"pk": map[string]any{"S": "user#1"},
			"sk": map[string]any{"S": sk},
		})
	}

	// When: query with Limit=2
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "q-limit",
		"KeyConditionExpression":    "pk = :pk",
		"ExpressionAttributeValues": map[string]any{":pk": map[string]any{"S": "user#1"}},
		"Limit":                     2,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items            []map[string]map[string]any `json:"Items"`
		Count            int                         `json:"Count"`
		ScannedCount     int                         `json:"ScannedCount"`
		LastEvaluatedKey map[string]map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: at most 2 items returned
	if result.Count > 2 {
		t.Errorf("expected <=2 items, got %d", result.Count)
	}
	// And: LastEvaluatedKey present for pagination
	if result.LastEvaluatedKey == nil {
		t.Error("expected LastEvaluatedKey for pagination")
	}
}

func TestQuery_pagination(t *testing.T) {
	// Given: a table with 4 items sharing the same hash key
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-page", "pk", "S", "sk", "S")
	for _, sk := range []string{"item#1", "item#2", "item#3", "item#4"} {
		putItem(t, srv, "q-page", map[string]any{
			"pk": map[string]any{"S": "user#1"},
			"sk": map[string]any{"S": sk},
		})
	}

	// When: first page with Limit=2
	resp1 := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "q-page",
		"KeyConditionExpression":    "pk = :pk",
		"ExpressionAttributeValues": map[string]any{":pk": map[string]any{"S": "user#1"}},
		"Limit":                     2,
	})
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)

	var page1 struct {
		Items            []map[string]map[string]any `json:"Items"`
		Count            int                         `json:"Count"`
		LastEvaluatedKey map[string]map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp1, &page1)

	if page1.LastEvaluatedKey == nil {
		t.Fatal("expected LastEvaluatedKey on first page")
	}

	// When: second page using ExclusiveStartKey
	resp2 := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "q-page",
		"KeyConditionExpression":    "pk = :pk",
		"ExpressionAttributeValues": map[string]any{":pk": map[string]any{"S": "user#1"}},
		"ExclusiveStartKey":         page1.LastEvaluatedKey,
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var page2 struct {
		Items            []map[string]map[string]any `json:"Items"`
		Count            int                         `json:"Count"`
		LastEvaluatedKey map[string]map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp2, &page2)

	// Then: second page has remaining items
	if page2.Count == 0 {
		t.Error("expected items on second page")
	}

	// And: total across both pages = 4
	total := page1.Count + page2.Count
	if total != 4 {
		t.Errorf("expected 4 total items across pages, got %d", total)
	}

	// And: no overlap between pages
	page1Keys := map[string]bool{}
	for _, item := range page1.Items {
		page1Keys[item["sk"]["S"].(string)] = true
	}
	for _, item := range page2.Items {
		sk := item["sk"]["S"].(string)
		if page1Keys[sk] {
			t.Errorf("item %q appeared on both pages", sk)
		}
	}
}

func TestQuery_paginationExhausted(t *testing.T) {
	// Given: a table with 2 items
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-exhaust", "pk", "S", "sk", "S")
	putItem(t, srv, "q-exhaust", map[string]any{
		"pk": map[string]any{"S": "user#1"},
		"sk": map[string]any{"S": "a"},
	})
	putItem(t, srv, "q-exhaust", map[string]any{
		"pk": map[string]any{"S": "user#1"},
		"sk": map[string]any{"S": "b"},
	})

	// When: query with Limit >= item count
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "q-exhaust",
		"KeyConditionExpression":    "pk = :pk",
		"ExpressionAttributeValues": map[string]any{":pk": map[string]any{"S": "user#1"}},
		"Limit":                     10,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Items            []map[string]map[string]any `json:"Items"`
		Count            int                         `json:"Count"`
		LastEvaluatedKey map[string]map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: all items returned, no pagination token
	if result.Count != 2 {
		t.Errorf("expected 2 items, got %d", result.Count)
	}
	if result.LastEvaluatedKey != nil {
		t.Error("expected no LastEvaluatedKey when all items fit")
	}
}

// ---------------------------------------------------------------------------
// Expression engine integration tests
// ---------------------------------------------------------------------------

// ---- FilterExpression: NOT ------------------------------------------------

func TestScan_filterExpression_not(t *testing.T) {
	// Given: a table with items of different statuses
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-not")
	for _, s := range []string{"active", "inactive", "pending"} {
		putItem(t, srv, "expr-not", map[string]any{
			"id":     map[string]any{"S": s},
			"status": map[string]any{"S": s},
		})
	}

	// When: scan with NOT filter
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-not",
		"FilterExpression": "NOT status = :s",
		"ExpressionAttributeValues": map[string]any{
			":s": map[string]any{"S": "active"},
		},
	})
	defer resp.Body.Close()

	// Then: 2 non-active items returned
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 non-active items, got %d", result.Count)
	}
}

// ---- FilterExpression: OR -------------------------------------------------

func TestScan_filterExpression_or(t *testing.T) {
	// Given: items with different colours
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-or")
	for _, c := range []string{"red", "green", "blue"} {
		putItem(t, srv, "expr-or", map[string]any{
			"id":    map[string]any{"S": c},
			"color": map[string]any{"S": c},
		})
	}

	// When: scan for red OR blue
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-or",
		"FilterExpression": "color = :a OR color = :b",
		"ExpressionAttributeValues": map[string]any{
			":a": map[string]any{"S": "red"},
			":b": map[string]any{"S": "blue"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 items (red+blue), got %d", result.Count)
	}
}

// ---- FilterExpression: BETWEEN --------------------------------------------

func TestScan_filterExpression_between(t *testing.T) {
	// Given: items with numeric prices
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-between")
	for i := 1; i <= 5; i++ {
		putItem(t, srv, "expr-between", map[string]any{
			"id":    map[string]any{"S": strconv.Itoa(i)},
			"price": map[string]any{"N": strconv.Itoa(i * 10)},
		})
	}

	// When: scan for price BETWEEN 20 AND 40
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-between",
		"FilterExpression": "price BETWEEN :lo AND :hi",
		"ExpressionAttributeValues": map[string]any{
			":lo": map[string]any{"N": "20"},
			":hi": map[string]any{"N": "40"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 3 {
		t.Errorf("expected 3 items (price 20,30,40), got %d", result.Count)
	}
}

// ---- FilterExpression: IN -------------------------------------------------

func TestScan_filterExpression_in(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-in")
	for _, c := range []string{"red", "green", "blue", "yellow"} {
		putItem(t, srv, "expr-in", map[string]any{
			"id":    map[string]any{"S": c},
			"color": map[string]any{"S": c},
		})
	}

	// When: scan for color IN (red, yellow)
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-in",
		"FilterExpression": "color IN (:a, :b)",
		"ExpressionAttributeValues": map[string]any{
			":a": map[string]any{"S": "red"},
			":b": map[string]any{"S": "yellow"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 items, got %d", result.Count)
	}
}

// ---- FilterExpression: begins_with ----------------------------------------

func TestScan_filterExpression_beginsWith(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-bw")
	for _, name := range []string{"alice", "albert", "bob"} {
		putItem(t, srv, "expr-bw", map[string]any{
			"id":   map[string]any{"S": name},
			"name": map[string]any{"S": name},
		})
	}

	// When: scan for names beginning with "al"
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-bw",
		"FilterExpression": "begins_with(name, :p)",
		"ExpressionAttributeValues": map[string]any{
			":p": map[string]any{"S": "al"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 items (alice, albert), got %d", result.Count)
	}
}

// ---- FilterExpression: contains -------------------------------------------

func TestScan_filterExpression_contains(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-cont")
	putItem(t, srv, "expr-cont", map[string]any{
		"id":   map[string]any{"S": "1"},
		"desc": map[string]any{"S": "hello world"},
	})
	putItem(t, srv, "expr-cont", map[string]any{
		"id":   map[string]any{"S": "2"},
		"desc": map[string]any{"S": "goodbye"},
	})

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-cont",
		"FilterExpression": "contains(desc, :s)",
		"ExpressionAttributeValues": map[string]any{
			":s": map[string]any{"S": "world"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Errorf("expected 1 item containing 'world', got %d", result.Count)
	}
}

// ---- FilterExpression: size() ---------------------------------------------

func TestScan_filterExpression_size(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-size")
	putItem(t, srv, "expr-size", map[string]any{
		"id":   map[string]any{"S": "1"},
		"name": map[string]any{"S": "hi"},
	})
	putItem(t, srv, "expr-size", map[string]any{
		"id":   map[string]any{"S": "2"},
		"name": map[string]any{"S": "hello world"},
	})

	// When: scan for names with size > 5
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-size",
		"FilterExpression": "size(name) > :s",
		"ExpressionAttributeValues": map[string]any{
			":s": map[string]any{"N": "5"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Errorf("expected 1 item with name size > 5, got %d", result.Count)
	}
}

// ---- FilterExpression: parentheses ----------------------------------------

func TestScan_filterExpression_parentheses(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-paren")
	putItem(t, srv, "expr-paren", map[string]any{
		"id":     map[string]any{"S": "1"},
		"color":  map[string]any{"S": "red"},
		"status": map[string]any{"S": "active"},
	})
	putItem(t, srv, "expr-paren", map[string]any{
		"id":     map[string]any{"S": "2"},
		"color":  map[string]any{"S": "blue"},
		"status": map[string]any{"S": "active"},
	})
	putItem(t, srv, "expr-paren", map[string]any{
		"id":     map[string]any{"S": "3"},
		"color":  map[string]any{"S": "red"},
		"status": map[string]any{"S": "inactive"},
	})

	// (color = red OR color = blue) AND status = active
	// Should match items 1 and 2
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "expr-paren",
		"FilterExpression": "(color = :red OR color = :blue) AND status = :active",
		"ExpressionAttributeValues": map[string]any{
			":red":    map[string]any{"S": "red"},
			":blue":   map[string]any{"S": "blue"},
			":active": map[string]any{"S": "active"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 active items (red+blue), got %d", result.Count)
	}
}

// ---- GetItem: ProjectionExpression ----------------------------------------

func TestGetItem_projectionExpression(t *testing.T) {
	// Given: an item with multiple attributes
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "proj-test")
	putItem(t, srv, "proj-test", map[string]any{
		"id":     map[string]any{"S": "1"},
		"name":   map[string]any{"S": "Alice"},
		"age":    map[string]any{"N": "30"},
		"secret": map[string]any{"S": "hidden"},
	})

	// When: GetItem with ProjectionExpression
	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "proj-test",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"ProjectionExpression": "name, age",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: item should have id (key always included), name, age but NOT secret
	if result.Item["id"] == nil {
		t.Error("expected key 'id' to be included")
	}
	if result.Item["name"] == nil {
		t.Error("expected 'name' to be included")
	}
	if result.Item["age"] == nil {
		t.Error("expected 'age' to be included")
	}
	if result.Item["secret"] != nil {
		t.Error("expected 'secret' to be excluded by projection")
	}
}

func TestGetItem_projectionWithAlias(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "proj-alias")
	putItem(t, srv, "proj-alias", map[string]any{
		"id":     map[string]any{"S": "1"},
		"status": map[string]any{"S": "active"},
		"data":   map[string]any{"S": "value"},
	})

	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "proj-alias",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"ProjectionExpression":     "#s",
		"ExpressionAttributeNames": map[string]any{"#s": "status"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Item["status"] == nil {
		t.Error("expected 'status' to be included via alias")
	}
	if result.Item["data"] != nil {
		t.Error("expected 'data' to be excluded by projection")
	}
}

// ---- Scan: ProjectionExpression -------------------------------------------

func TestScan_projectionExpression(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "scan-proj")
	putItem(t, srv, "scan-proj", map[string]any{
		"id":     map[string]any{"S": "1"},
		"name":   map[string]any{"S": "Alice"},
		"secret": map[string]any{"S": "hidden"},
	})

	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":            "scan-proj",
		"ProjectionExpression": "name",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Items []map[string]map[string]any `json:"Items"`
		Count int                         `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Count != 1 {
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
	item := result.Items[0]
	if item["name"] == nil {
		t.Error("expected 'name' to be included")
	}
	if item["secret"] != nil {
		t.Error("expected 'secret' to be excluded by projection")
	}
}

// ---- Query: sort key conditions -------------------------------------------

func TestQuery_sortKeyLessThan(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-lt", "pk", "S", "sk", "N")
	for i := 1; i <= 5; i++ {
		putItem(t, srv, "q-lt", map[string]any{
			"pk": map[string]any{"S": "user#1"},
			"sk": map[string]any{"N": strconv.Itoa(i)},
		})
	}

	// When: query with sk < 3
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "q-lt",
		"KeyConditionExpression": "pk = :pk AND sk < :sk",
		"ExpressionAttributeValues": map[string]any{
			":pk": map[string]any{"S": "user#1"},
			":sk": map[string]any{"N": "3"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 items (sk 1,2), got %d", result.Count)
	}
}

func TestQuery_sortKeyBetween(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-btw", "pk", "S", "sk", "N")
	for i := 1; i <= 5; i++ {
		putItem(t, srv, "q-btw", map[string]any{
			"pk": map[string]any{"S": "user#1"},
			"sk": map[string]any{"N": strconv.Itoa(i * 10)},
		})
	}

	// When: query with sk BETWEEN 20 AND 40
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "q-btw",
		"KeyConditionExpression": "pk = :pk AND sk BETWEEN :lo AND :hi",
		"ExpressionAttributeValues": map[string]any{
			":pk": map[string]any{"S": "user#1"},
			":lo": map[string]any{"N": "20"},
			":hi": map[string]any{"N": "40"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 3 {
		t.Errorf("expected 3 items (sk 20,30,40), got %d", result.Count)
	}
}

func TestQuery_sortKeyBeginsWith(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-bw", "pk", "S", "sk", "S")
	putItem(t, srv, "q-bw", map[string]any{
		"pk": map[string]any{"S": "user#1"},
		"sk": map[string]any{"S": "order#100"},
	})
	putItem(t, srv, "q-bw", map[string]any{
		"pk": map[string]any{"S": "user#1"},
		"sk": map[string]any{"S": "order#200"},
	})
	putItem(t, srv, "q-bw", map[string]any{
		"pk": map[string]any{"S": "user#1"},
		"sk": map[string]any{"S": "profile#main"},
	})

	// When: query with begins_with(sk, 'order#')
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "q-bw",
		"KeyConditionExpression": "pk = :pk AND begins_with(sk, :prefix)",
		"ExpressionAttributeValues": map[string]any{
			":pk":     map[string]any{"S": "user#1"},
			":prefix": map[string]any{"S": "order#"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 2 {
		t.Errorf("expected 2 order items, got %d", result.Count)
	}
}

func TestQuery_sortKeyGreaterThanOrEqual(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-ge", "pk", "S", "sk", "N")
	for i := 1; i <= 5; i++ {
		putItem(t, srv, "q-ge", map[string]any{
			"pk": map[string]any{"S": "user#1"},
			"sk": map[string]any{"N": strconv.Itoa(i)},
		})
	}

	// When: query with sk >= 3
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "q-ge",
		"KeyConditionExpression": "pk = :pk AND sk >= :sk",
		"ExpressionAttributeValues": map[string]any{
			":pk": map[string]any{"S": "user#1"},
			":sk": map[string]any{"N": "3"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 3 {
		t.Errorf("expected 3 items (sk 3,4,5), got %d", result.Count)
	}
}

// ---- Query: ProjectionExpression ------------------------------------------

func TestQuery_projectionExpression(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "q-proj", "pk", "S", "sk", "S")
	putItem(t, srv, "q-proj", map[string]any{
		"pk":     map[string]any{"S": "user#1"},
		"sk":     map[string]any{"S": "profile"},
		"name":   map[string]any{"S": "Alice"},
		"secret": map[string]any{"S": "hidden"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "q-proj",
		"KeyConditionExpression": "pk = :pk",
		"ExpressionAttributeValues": map[string]any{
			":pk": map[string]any{"S": "user#1"},
		},
		"ProjectionExpression": "name",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Items []map[string]map[string]any `json:"Items"`
		Count int                         `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Count != 1 {
		t.Fatalf("expected 1 item, got %d", result.Count)
	}
	item := result.Items[0]
	if item["name"] == nil {
		t.Error("expected 'name' to be included")
	}
	if item["secret"] != nil {
		t.Error("expected 'secret' to be excluded by projection")
	}
	// Keys should always be included
	if item["pk"] == nil {
		t.Error("expected key 'pk' to be included")
	}
}

// ---- UpdateItem: ADD and DELETE -------------------------------------------

func TestUpdateItem_addNumber(t *testing.T) {
	// Given: an item with a counter
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-add")
	putItem(t, srv, "upd-add", map[string]any{
		"id":    map[string]any{"S": "1"},
		"count": map[string]any{"N": "10"},
	})

	// When: ADD 5 to the counter
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-add",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression": "ADD #c :inc",
		"ExpressionAttributeNames": map[string]any{
			"#c": "count",
		},
		"ExpressionAttributeValues": map[string]any{
			":inc": map[string]any{"N": "5"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: verify counter is now 15
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-add",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["count"]["N"] != "15" {
		t.Errorf("expected count 15, got %v", result.Item["count"]["N"])
	}
}

func TestUpdateItem_addNewNumber(t *testing.T) {
	// Given: an item without a counter
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-add-new")
	putItem(t, srv, "upd-add-new", map[string]any{
		"id": map[string]any{"S": "1"},
	})

	// When: ADD to a non-existent attribute
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-add-new",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression": "ADD hits :v",
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]any{"N": "7"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: attribute should be created with value 7
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-add-new",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["hits"]["N"] != "7" {
		t.Errorf("expected hits 7, got %v", result.Item["hits"]["N"])
	}
}

func TestUpdateItem_addStringSet(t *testing.T) {
	// Given: an item with a string set
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-add-ss")
	putItem(t, srv, "upd-add-ss", map[string]any{
		"id":   map[string]any{"S": "1"},
		"tags": map[string]any{"SS": []any{"a", "b"}},
	})

	// When: ADD new elements to the set
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-add-ss",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression": "ADD tags :new",
		"ExpressionAttributeValues": map[string]any{
			":new": map[string]any{"SS": []any{"b", "c"}},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: set should be the union {a, b, c}
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-add-ss",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	tags, ok := result.Item["tags"]["SS"].([]any)
	if !ok {
		t.Fatalf("expected SS array, got %T", result.Item["tags"]["SS"])
	}
	if len(tags) != 3 {
		t.Errorf("expected 3 tags after union, got %d: %v", len(tags), tags)
	}
}

func TestUpdateItem_deleteStringSetElements(t *testing.T) {
	// Given: an item with a string set
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-del-ss")
	putItem(t, srv, "upd-del-ss", map[string]any{
		"id":   map[string]any{"S": "1"},
		"tags": map[string]any{"SS": []any{"a", "b", "c"}},
	})

	// When: DELETE element "b" from the set
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-del-ss",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression": "DELETE tags :rem",
		"ExpressionAttributeValues": map[string]any{
			":rem": map[string]any{"SS": []any{"b"}},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: set should be {a, c}
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-del-ss",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	tags, ok := result.Item["tags"]["SS"].([]any)
	if !ok {
		t.Fatalf("expected SS array, got %T", result.Item["tags"]["SS"])
	}
	if len(tags) != 2 {
		t.Errorf("expected 2 tags after delete, got %d: %v", len(tags), tags)
	}
}

// ---- UpdateItem: if_not_exists and list_append ----------------------------

func TestUpdateItem_ifNotExists(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-ine")
	putItem(t, srv, "upd-ine", map[string]any{
		"id": map[string]any{"S": "1"},
	})

	// When: SET with if_not_exists on a missing attribute
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-ine",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression": "SET hits = if_not_exists(hits, :zero)",
		"ExpressionAttributeValues": map[string]any{
			":zero": map[string]any{"N": "0"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Get and verify
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-ine",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["hits"]["N"] != "0" {
		t.Errorf("expected hits 0, got %v", result.Item["hits"]["N"])
	}
}

func TestUpdateItem_listAppend(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-la")
	putItem(t, srv, "upd-la", map[string]any{
		"id": map[string]any{"S": "1"},
		"tags": map[string]any{"L": []any{
			map[string]any{"S": "a"},
		}},
	})

	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-la",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression": "SET tags = list_append(tags, :new)",
		"ExpressionAttributeValues": map[string]any{
			":new": map[string]any{"L": []any{
				map[string]any{"S": "b"},
				map[string]any{"S": "c"},
			}},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-la",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	tags, ok := result.Item["tags"]["L"].([]any)
	if !ok {
		t.Fatalf("expected L array, got %T", result.Item["tags"]["L"])
	}
	if len(tags) != 3 {
		t.Errorf("expected 3 tags after list_append, got %d", len(tags))
	}
}

// ---- UpdateItem: ConditionExpression --------------------------------------

func TestUpdateItem_conditionCheckFails(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-cond")
	putItem(t, srv, "upd-cond", map[string]any{
		"id":     map[string]any{"S": "1"},
		"status": map[string]any{"S": "inactive"},
	})

	// When: update with a failing condition
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-cond",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression":    "SET #n = :v",
		"ConditionExpression": "status = :active",
		"ExpressionAttributeNames": map[string]any{
			"#n": "name",
		},
		"ExpressionAttributeValues": map[string]any{
			":v":      map[string]any{"S": "Alice"},
			":active": map[string]any{"S": "active"},
		},
	})
	defer resp.Body.Close()

	// Then: should fail with ConditionalCheckFailedException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ConditionalCheckFailedException")
}

func TestUpdateItem_conditionCheckPasses(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-cond-ok")
	putItem(t, srv, "upd-cond-ok", map[string]any{
		"id":     map[string]any{"S": "1"},
		"status": map[string]any{"S": "active"},
	})

	// When: update with a passing condition
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-cond-ok",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression":    "SET #n = :v",
		"ConditionExpression": "status = :active",
		"ExpressionAttributeNames": map[string]any{
			"#n": "name",
		},
		"ExpressionAttributeValues": map[string]any{
			":v":      map[string]any{"S": "Alice"},
			":active": map[string]any{"S": "active"},
		},
	})
	defer resp.Body.Close()

	// Then: update succeeds
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify the update was applied
	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-cond-ok",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["name"]["S"] != "Alice" {
		t.Errorf("expected name 'Alice', got %v", result.Item["name"]["S"])
	}
}

// ---- UpdateItem: arithmetic -----------------------------------------------

func TestUpdateItem_arithmeticIncrement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "upd-arith")
	putItem(t, srv, "upd-arith", map[string]any{
		"id":    map[string]any{"S": "1"},
		"count": map[string]any{"N": "10"},
	})

	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "upd-arith",
		"Key": map[string]any{
			"id": map[string]any{"S": "1"},
		},
		"UpdateExpression": "SET #c = #c + :inc",
		"ExpressionAttributeNames": map[string]any{
			"#c": "count",
		},
		"ExpressionAttributeValues": map[string]any{
			":inc": map[string]any{"N": "5"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	getResp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "upd-arith",
		"Key":       map[string]any{"id": map[string]any{"S": "1"}},
	})
	defer getResp.Body.Close()
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, getResp, &result)
	if result.Item["count"]["N"] != "15" {
		t.Errorf("expected count 15, got %v", result.Item["count"]["N"])
	}
}

// ---- FilterExpression: ExpressionAttributeNames ----------------------------

func TestScan_filterExpression_withAttributeNames(t *testing.T) {
	// Given: items using a reserved word as attribute name
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "expr-names")
	putItem(t, srv, "expr-names", map[string]any{
		"id":     map[string]any{"S": "1"},
		"status": map[string]any{"S": "active"},
	})
	putItem(t, srv, "expr-names", map[string]any{
		"id":     map[string]any{"S": "2"},
		"status": map[string]any{"S": "inactive"},
	})

	// When: scan using ExpressionAttributeNames
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":                "expr-names",
		"FilterExpression":         "#s = :v",
		"ExpressionAttributeNames": map[string]any{"#s": "status"},
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]any{"S": "active"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Errorf("expected 1 active item, got %d", result.Count)
	}
}

// ---- Limit semantics -------------------------------------------------------

// TestQuery_Limit_AppliedBeforeFilter verifies that Limit caps items READ before
// FilterExpression is applied, matching AWS DynamoDB semantics. With Limit=3 and
// a FilterExpression that matches none of the first 3 items, the result should be
// Count=0, ScannedCount=3, and a non-nil LastEvaluatedKey pointing into the table.
func TestQuery_Limit_AppliedBeforeFilter(t *testing.T) {
	// Given: a hash+sort table with 5 items, sort keys 1..5
	// items with sk 1,2,3 have flag="skip"; items 4,5 have flag="keep"
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "query-limit-filter", "pk", "S", "sk", "N")
	for i := 1; i <= 5; i++ {
		flag := "skip"
		if i > 3 {
			flag = "keep"
		}
		putItem(t, srv, "query-limit-filter", map[string]any{
			"pk":   map[string]any{"S": "A"},
			"sk":   map[string]any{"N": strconv.Itoa(i)},
			"flag": map[string]any{"S": flag},
		})
	}

	// When: Query with Limit=3 and FilterExpression that only matches "keep"
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "query-limit-filter",
		"KeyConditionExpression": "pk = :pk",
		"FilterExpression":       "flag = :keep",
		"ExpressionAttributeValues": map[string]any{
			":pk":   map[string]any{"S": "A"},
			":keep": map[string]any{"S": "keep"},
		},
		"Limit": 3,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DynamoDB reads 3 items (sk=1,2,3), none pass the filter
	var out struct {
		Items            []map[string]any `json:"Items"`
		Count            int              `json:"Count"`
		ScannedCount     int              `json:"ScannedCount"`
		LastEvaluatedKey map[string]any   `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp, &out)

	if out.ScannedCount != 3 {
		t.Errorf("ScannedCount: want 3 (Limit=3 items read before filter), got %d", out.ScannedCount)
	}
	if out.Count != 0 {
		t.Errorf("Count: want 0 (no items pass filter in first 3), got %d", out.Count)
	}
	if out.LastEvaluatedKey == nil {
		t.Error("LastEvaluatedKey should be non-nil (more items exist beyond the read window)")
	}
}

// TestScan_Limit_AppliedBeforeFilter verifies the same Limit-before-filter semantics for Scan.
func TestScan_Limit_AppliedBeforeFilter(t *testing.T) {
	// Given: 5 items; item1 and item2 have flag="skip", items 3..5 have flag="keep"
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "scan-limit-filter")
	for i := 1; i <= 5; i++ {
		flag := "keep"
		if i <= 2 {
			flag = "skip"
		}
		putItem(t, srv, "scan-limit-filter", map[string]any{
			"id":   map[string]any{"S": fmt.Sprintf("item%d", i)},
			"flag": map[string]any{"S": flag},
		})
	}

	// When: Scan with Limit=2 and a filter that would only match "keep" items
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":        "scan-limit-filter",
		"Limit":            2,
		"FilterExpression": "flag = :v",
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]any{"S": "keep"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DynamoDB reads exactly 2 items (Limit=2), both have flag="skip" so Count=0
	var out struct {
		Count            int            `json:"Count"`
		ScannedCount     int            `json:"ScannedCount"`
		LastEvaluatedKey map[string]any `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp, &out)

	if out.ScannedCount != 2 {
		t.Errorf("ScannedCount: want 2 (Limit=2 items read before filter), got %d", out.ScannedCount)
	}
	if out.Count != 0 {
		t.Errorf("Count: want 0 (first 2 items are 'skip'), got %d", out.Count)
	}
	if out.LastEvaluatedKey == nil {
		t.Error("LastEvaluatedKey should be set (Limit=2 but 5 items exist)")
	}
}

// ---- Select=COUNT ----------------------------------------------------------

// TestQuery_Select_COUNT verifies that Select="COUNT" returns Count and ScannedCount
// without including an Items array in the response.
func TestQuery_Select_COUNT(t *testing.T) {
	// Given: a hash+sort table with 3 items in one partition
	srv := helpers.NewTestServer(t)
	createTableWithSortKey(t, srv, "query-count-table", "pk", "S", "sk", "N")
	for i := 1; i <= 3; i++ {
		putItem(t, srv, "query-count-table", map[string]any{
			"pk": map[string]any{"S": "P"},
			"sk": map[string]any{"N": strconv.Itoa(i)},
		})
	}

	// When: Query with Select="COUNT"
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "query-count-table",
		"KeyConditionExpression": "pk = :pk",
		"ExpressionAttributeValues": map[string]any{
			":pk": map[string]any{"S": "P"},
		},
		"Select": "COUNT",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: Count=3, ScannedCount=3, no Items field
	var raw map[string]any
	helpers.DecodeJSON(t, resp, &raw)

	if count, _ := raw["Count"].(float64); count != 3 {
		t.Errorf("Count: want 3, got %v", raw["Count"])
	}
	if sc, _ := raw["ScannedCount"].(float64); sc != 3 {
		t.Errorf("ScannedCount: want 3, got %v", raw["ScannedCount"])
	}
	if _, hasItems := raw["Items"]; hasItems {
		t.Error("Items must not be present when Select=COUNT")
	}
}

// TestScan_Select_COUNT verifies that Select="COUNT" with Scan returns only counts.
func TestScan_Select_COUNT(t *testing.T) {
	// Given: a table with 4 items
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "scan-count-table")
	for i := 1; i <= 4; i++ {
		putItem(t, srv, "scan-count-table", map[string]any{
			"id": map[string]any{"S": fmt.Sprintf("item%d", i)},
		})
	}

	// When: Scan with Select="COUNT"
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName": "scan-count-table",
		"Select":    "COUNT",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: Count=4, no Items field
	var raw map[string]any
	helpers.DecodeJSON(t, resp, &raw)

	if count, _ := raw["Count"].(float64); count != 4 {
		t.Errorf("Count: want 4, got %v", raw["Count"])
	}
	if _, hasItems := raw["Items"]; hasItems {
		t.Error("Items must not be present when Select=COUNT")
	}
}

// TestQuery_GSI_LastEvaluatedKey_IncludesIndexKeys verifies that LastEvaluatedKey for a
// GSI query includes both the table primary-key attributes AND the GSI key attributes,
// matching AWS DynamoDB behaviour required for correct paginated GSI iteration.
func TestQuery_GSI_LastEvaluatedKey_IncludesIndexKeys(t *testing.T) {
	// Given: a table with a GSI on "category" (hash) + "ts" (sort)
	srv := helpers.NewTestServer(t)
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "gsi-lastkey-table",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
			{"AttributeName": "category", "AttributeType": "S"},
			{"AttributeName": "ts", "AttributeType": "N"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"GlobalSecondaryIndexes": []map[string]any{
			{
				"IndexName": "category-ts-index",
				"KeySchema": []map[string]any{
					{"AttributeName": "category", "KeyType": "HASH"},
					{"AttributeName": "ts", "KeyType": "RANGE"},
				},
				"Projection": map[string]any{"ProjectionType": "ALL"},
			},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	resp.Body.Close()

	for i := 1; i <= 4; i++ {
		putItem(t, srv, "gsi-lastkey-table", map[string]any{
			"id":       map[string]any{"S": fmt.Sprintf("item%d", i)},
			"category": map[string]any{"S": "books"},
			"ts":       map[string]any{"N": strconv.Itoa(i * 100)},
		})
	}

	// When: Query the GSI with Limit=2
	resp = ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "gsi-lastkey-table",
		"IndexName":              "category-ts-index",
		"KeyConditionExpression": "category = :cat",
		"ExpressionAttributeValues": map[string]any{
			":cat": map[string]any{"S": "books"},
		},
		"Limit": 2,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var out struct {
		Count            int            `json:"Count"`
		LastEvaluatedKey map[string]any `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp, &out)

	if out.Count != 2 {
		t.Errorf("Count: want 2, got %d", out.Count)
	}
	if out.LastEvaluatedKey == nil {
		t.Fatal("LastEvaluatedKey should be set (there are more items)")
	}
	// Then: LastEvaluatedKey must include the table PK ("id") and both GSI keys ("category", "ts")
	if _, ok := out.LastEvaluatedKey["id"]; !ok {
		t.Error("LastEvaluatedKey must include table PK attribute 'id'")
	}
	if _, ok := out.LastEvaluatedKey["category"]; !ok {
		t.Error("LastEvaluatedKey must include GSI hash key attribute 'category'")
	}
	if _, ok := out.LastEvaluatedKey["ts"]; !ok {
		t.Error("LastEvaluatedKey must include GSI sort key attribute 'ts'")
	}

	// And: using it as ExclusiveStartKey returns the next page
	resp2 := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "gsi-lastkey-table",
		"IndexName":              "category-ts-index",
		"KeyConditionExpression": "category = :cat",
		"ExpressionAttributeValues": map[string]any{
			":cat": map[string]any{"S": "books"},
		},
		"Limit":             2,
		"ExclusiveStartKey": out.LastEvaluatedKey,
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var out2 struct {
		Count            int            `json:"Count"`
		LastEvaluatedKey map[string]any `json:"LastEvaluatedKey"`
	}
	helpers.DecodeJSON(t, resp2, &out2)

	if out2.Count != 2 {
		t.Errorf("page 2 Count: want 2, got %d", out2.Count)
	}
	if out2.LastEvaluatedKey != nil {
		t.Error("page 2 LastEvaluatedKey should be nil (no more items)")
	}
}
