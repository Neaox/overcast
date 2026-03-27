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
	"net/http"
	"testing"

	"github.com/your-org/overcast/tests/helpers"
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

// ---- Unimplemented operations return 501 -----------------------------------

func TestTransactWriteItems_returns501(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []any{},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusNotImplemented)
	if got := resp.Header.Get("x-emulator-unsupported"); got != "true" {
		t.Error("expected x-emulator-unsupported: true on 501 responses")
	}
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
