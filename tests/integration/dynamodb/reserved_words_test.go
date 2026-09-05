package dynamodb_test

// Reserved words may not be used as bare attribute names in DynamoDB
// expressions — see "Reserved words in DynamoDB" in the Developer Guide
// (https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/ReservedWords.html).
// One test per expression parameter, each paired with the documented escape
// hatch: the same attribute reached through an ExpressionAttributeNames alias
// (https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Expressions.ExpressionAttributeNames.html).

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ---- UpdateExpression ------------------------------------------------------

func TestUpdateItem_reservedWordAsBareAttributeName(t *testing.T) {
	// Given: a table holding one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-update")
	putItem(t, srv, "rw-update", map[string]any{"id": map[string]any{"S": "item-1"}})

	// When: an UpdateExpression names the reserved word "size" directly
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":                 "rw-update",
		"Key":                       map[string]any{"id": map[string]any{"S": "item-1"}},
		"UpdateExpression":          "SET size = :v",
		"ExpressionAttributeValues": map[string]any{":v": map[string]any{"N": "1"}},
	})
	defer resp.Body.Close()

	// Then: the request is rejected, naming the keyword
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertRequestID(t, resp)
	assertValidationMessage(t, resp,
		"Invalid UpdateExpression: Attribute name is a reserved keyword; reserved keyword: size")
}

func TestUpdateItem_reservedWordViaExpressionAttributeName(t *testing.T) {
	// Given: a table holding one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-update-alias")
	putItem(t, srv, "rw-update-alias", map[string]any{"id": map[string]any{"S": "item-1"}})

	// When: the same attribute is reached through an alias
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":                 "rw-update-alias",
		"Key":                       map[string]any{"id": map[string]any{"S": "item-1"}},
		"UpdateExpression":          "SET #s = :v",
		"ExpressionAttributeNames":  map[string]any{"#s": "size"},
		"ExpressionAttributeValues": map[string]any{":v": map[string]any{"N": "1"}},
		"ReturnValues":              "ALL_NEW",
	})
	defer resp.Body.Close()

	// Then: the update succeeds and writes the attribute
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]map[string]any `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if got := result.Attributes["size"]["N"]; got != "1" {
		t.Errorf("Attributes[size].N = %v, want \"1\"", got)
	}
}

func TestUpdateItem_reservedWordCaseVariants(t *testing.T) {
	// Given: a table holding one item — the published list is upper-case, but
	// AWS states it "isn't case-sensitive"
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-case")
	putItem(t, srv, "rw-case", map[string]any{"id": map[string]any{"S": "item-1"}})

	for _, word := range []string{"size", "SIZE", "Size"} {
		t.Run(word, func(t *testing.T) {
			// When: the reserved word is spelled with that casing
			resp := ddbCall(t, srv, "UpdateItem", map[string]any{
				"TableName":                 "rw-case",
				"Key":                       map[string]any{"id": map[string]any{"S": "item-1"}},
				"UpdateExpression":          "SET " + word + " = :v",
				"ExpressionAttributeValues": map[string]any{":v": map[string]any{"N": "1"}},
			})
			defer resp.Body.Close()

			// Then: it is rejected, echoing the spelling the caller used
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			assertValidationMessage(t, resp,
				"Invalid UpdateExpression: Attribute name is a reserved keyword; reserved keyword: "+word)
		})
	}
}

func TestUpdateItem_reservedWordAsNestedPathSegment(t *testing.T) {
	// Given: a table holding one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-nested")
	putItem(t, srv, "rw-nested", map[string]any{"id": map[string]any{"S": "item-1"}})

	// When: the reserved word is a nested segment rather than the top level
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName":                 "rw-nested",
		"Key":                       map[string]any{"id": map[string]any{"S": "item-1"}},
		"UpdateExpression":          "SET info.size = :v",
		"ExpressionAttributeValues": map[string]any{":v": map[string]any{"N": "1"}},
	})
	defer resp.Body.Close()

	// Then: it is still rejected — every attribute name in a path is checked
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"Invalid UpdateExpression: Attribute name is a reserved keyword; reserved keyword: size")
}

// ---- ConditionExpression ---------------------------------------------------

func TestPutItem_reservedWordInConditionExpression(t *testing.T) {
	// Given: an empty table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-condition")

	// When: a ConditionExpression names the reserved word "comment" as a
	// function argument
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName":           "rw-condition",
		"Item":                map[string]any{"id": map[string]any{"S": "item-1"}},
		"ConditionExpression": "attribute_not_exists(comment)",
	})
	defer resp.Body.Close()

	// Then: the request is rejected, naming the keyword
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"Invalid ConditionExpression: Attribute name is a reserved keyword; reserved keyword: comment")
}

func TestPutItem_conditionExpressionReservedWordViaExpressionAttributeName(t *testing.T) {
	// Given: an empty table
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-condition-alias")

	// When: the same attribute is reached through an alias
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName":                "rw-condition-alias",
		"Item":                     map[string]any{"id": map[string]any{"S": "item-1"}},
		"ConditionExpression":      "attribute_not_exists(#c)",
		"ExpressionAttributeNames": map[string]any{"#c": "comment"},
	})
	defer resp.Body.Close()

	// Then: the write succeeds
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ---- FilterExpression ------------------------------------------------------

func TestScan_reservedWordInFilterExpression(t *testing.T) {
	// Given: a table holding one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-filter")
	putItem(t, srv, "rw-filter", map[string]any{
		"id":     map[string]any{"S": "item-1"},
		"status": map[string]any{"S": "active"},
	})

	// When: a FilterExpression names the reserved word "status" directly
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":                 "rw-filter",
		"FilterExpression":          "status = :s",
		"ExpressionAttributeValues": map[string]any{":s": map[string]any{"S": "active"}},
	})
	defer resp.Body.Close()

	// Then: the scan is rejected, naming the keyword
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"Invalid FilterExpression: Attribute name is a reserved keyword; reserved keyword: status")
}

func TestScan_filterExpressionReservedWordViaExpressionAttributeName(t *testing.T) {
	// Given: a table holding one item
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-filter-alias")
	putItem(t, srv, "rw-filter-alias", map[string]any{
		"id":     map[string]any{"S": "item-1"},
		"status": map[string]any{"S": "active"},
	})

	// When: the same attribute is reached through an alias
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":                 "rw-filter-alias",
		"FilterExpression":          "#st = :s",
		"ExpressionAttributeNames":  map[string]any{"#st": "status"},
		"ExpressionAttributeValues": map[string]any{":s": map[string]any{"S": "active"}},
	})
	defer resp.Body.Close()

	// Then: the scan succeeds and matches the item
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Errorf("Count = %d, want 1", result.Count)
	}
}

func TestScan_sizeFunctionIsNotAReservedWordUse(t *testing.T) {
	// Given: a table holding one item with a three-element list
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-size-fn")
	putItem(t, srv, "rw-size-fn", map[string]any{
		"id": map[string]any{"S": "item-1"},
		"tags": map[string]any{"L": []any{
			map[string]any{"S": "a"},
			map[string]any{"S": "b"},
			map[string]any{"S": "c"},
		}},
	})

	// When: size() is called as a function — SIZE is on the reserved list, but
	// a function name is not an attribute name
	resp := ddbCall(t, srv, "Scan", map[string]any{
		"TableName":                 "rw-size-fn",
		"FilterExpression":          "size(tags) > :n",
		"ExpressionAttributeValues": map[string]any{":n": map[string]any{"N": "2"}},
	})
	defer resp.Body.Close()

	// Then: the scan succeeds and matches the item
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Errorf("Count = %d, want 1", result.Count)
	}
}

// ---- KeyConditionExpression ------------------------------------------------

func TestQuery_reservedWordInKeyConditionExpression(t *testing.T) {
	// Given: a table whose partition key is called "name"
	srv := helpers.NewTestServer(t)
	createTableWithHashKey(t, srv, "rw-key", "name")
	putItem(t, srv, "rw-key", map[string]any{"name": map[string]any{"S": "alice"}})

	// When: a KeyConditionExpression names the key directly
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "rw-key",
		"KeyConditionExpression":    "name = :n",
		"ExpressionAttributeValues": map[string]any{":n": map[string]any{"S": "alice"}},
	})
	defer resp.Body.Close()

	// Then: the query is rejected, naming the keyword
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"Invalid KeyConditionExpression: Attribute name is a reserved keyword; reserved keyword: name")
}

func TestQuery_keyConditionExpressionReservedWordViaExpressionAttributeName(t *testing.T) {
	// Given: a table whose partition key is called "name"
	srv := helpers.NewTestServer(t)
	createTableWithHashKey(t, srv, "rw-key-alias", "name")
	putItem(t, srv, "rw-key-alias", map[string]any{"name": map[string]any{"S": "alice"}})

	// When: the key is reached through an alias
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":                 "rw-key-alias",
		"KeyConditionExpression":    "#n = :n",
		"ExpressionAttributeNames":  map[string]any{"#n": "name"},
		"ExpressionAttributeValues": map[string]any{":n": map[string]any{"S": "alice"}},
	})
	defer resp.Body.Close()

	// Then: the query succeeds and returns the item
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Errorf("Count = %d, want 1", result.Count)
	}
}

// ---- ProjectionExpression --------------------------------------------------

func TestGetItem_reservedWordInProjectionExpression(t *testing.T) {
	// Given: a table holding one item with a "comment" attribute
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-projection")
	putItem(t, srv, "rw-projection", map[string]any{
		"id":      map[string]any{"S": "item-1"},
		"comment": map[string]any{"S": "hello"},
	})

	// When: a ProjectionExpression names the reserved word directly
	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName":            "rw-projection",
		"Key":                  map[string]any{"id": map[string]any{"S": "item-1"}},
		"ProjectionExpression": "comment",
	})
	defer resp.Body.Close()

	// Then: the read is rejected, naming the keyword
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"Invalid ProjectionExpression: Attribute name is a reserved keyword; reserved keyword: comment")
}

func TestGetItem_projectionExpressionReservedWordViaExpressionAttributeName(t *testing.T) {
	// Given: a table holding one item with a "comment" attribute
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "rw-projection-alias")
	putItem(t, srv, "rw-projection-alias", map[string]any{
		"id":      map[string]any{"S": "item-1"},
		"comment": map[string]any{"S": "hello"},
	})

	// When: the attribute is reached through an alias
	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName":                "rw-projection-alias",
		"Key":                      map[string]any{"id": map[string]any{"S": "item-1"}},
		"ProjectionExpression":     "#c",
		"ExpressionAttributeNames": map[string]any{"#c": "comment"},
	})
	defer resp.Body.Close()

	// Then: the read succeeds and projects the attribute
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if got := result.Item["comment"]["S"]; got != "hello" {
		t.Errorf("Item[comment].S = %v, want \"hello\"", got)
	}
}

// ---- Test helpers ----------------------------------------------------------

// createTableWithHashKey creates a table whose only key attribute is named
// hashKey, so a test can put a reserved word in the key schema.
func createTableWithHashKey(t *testing.T, srv *helpers.TestServer, name, hashKey string) {
	t.Helper()
	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName":            name,
		"AttributeDefinitions": []map[string]any{{"AttributeName": hashKey, "AttributeType": "S"}},
		"KeySchema":            []map[string]any{{"AttributeName": hashKey, "KeyType": "HASH"}},
		"BillingMode":          "PAY_PER_REQUEST",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createTableWithHashKey %q: status %d: %s", name, resp.StatusCode, helpers.ReadBody(t, resp))
	}
}
