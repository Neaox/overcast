package dynamodb_test

// Key-schema validation on every operation that accepts an item or a key
// (issue #1637). DynamoDB is schemaless everywhere except the key schema: the
// attributes named in KeySchema must be present and must carry the type
// AttributeDefinitions declares for them, and nothing may be imposed on any
// other attribute.
//
// AWS evidence for each message asserted below:
//   - PutItem type mismatch — "One or more parameter values were invalid:
//     Type mismatch for key <name> expected: <S|N|B> actual: <type>".
//     https://dynobase.dev/dynamodb-errors/error-validationexception-one-or-more-parameter-values-were-invalid-type-mismatch-for-key-x-expected-s-actual-m/
//     and moto's InvalidAttributeTypeError (moto/dynamodb/exceptions.py),
//     raised from Table.put_item against hash_key_type/range_key_type.
//   - PutItem missing key — "One or more parameter values were invalid:
//     Missing the key <name> in the item", reported verbatim against real AWS
//     in aws/aws-sdk-js#4057 and raised by moto's Table.put_item.
//   - GetItem/DeleteItem/UpdateItem and the batch/transact key forms — "The
//     provided key element does not match the schema", AWS's single answer for
//     a Key map that disagrees with the schema by type, by a missing key
//     attribute, or by an extra one (AWS re:Post "Troubleshoot 'provided key
//     element' error in DynamoDB"; moto's ProvidedKeyDoesNotExist).
//   - Query key conditions — "One or more parameter values were invalid:
//     Condition parameter type does not match schema type".
//     https://www.kevinhooke.com/2022/11/15/aws-dynamodb-error-validationexception-one-or-more-parameter-values-were-invalid-condition-parameter-type-does-not-match-schema-type/

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const (
	// wantKeyElementMismatch is AWS's answer whenever a supplied Key map
	// disagrees with the key schema, whatever the disagreement is.
	wantKeyElementMismatch = "The provided key element does not match the schema"
	// wantConditionTypeMismatch is AWS's answer for a KeyConditionExpression
	// value whose type differs from the declared key type.
	wantConditionTypeMismatch = "One or more parameter values were invalid: Condition parameter type does not match schema type"
)

// createKeySchemaTable makes a table whose partition key is a String and whose
// sort key is a Number, so a request can get each one wrong independently.
func createKeySchemaTable(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	createTableWithSortKey(t, srv, name, "pk", "S", "sk", "N")
}

// ---- PutItem ---------------------------------------------------------------

func TestPutItem_sortKeyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-put-sort")

	// When: PutItem supplies the sort key as a String
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "ks-put-sort",
		"Item": map[string]any{
			"pk": map[string]any{"S": "b"},
			"sk": map[string]any{"S": "not-a-number"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS's type-mismatch ValidationException, naming both types
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"One or more parameter values were invalid: Type mismatch for key sk expected: N actual: S")
}

func TestPutItem_hashKeyTypeMismatch(t *testing.T) {
	// Given: a table whose partition key is declared String
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-put-hash")

	// When: PutItem supplies the partition key as a Number
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "ks-put-hash",
		"Item": map[string]any{
			"pk": map[string]any{"N": "7"},
			"sk": map[string]any{"N": "1"},
		},
	})
	defer resp.Body.Close()

	// Then: the mismatch is reported against the partition key
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"One or more parameter values were invalid: Type mismatch for key pk expected: S actual: N")
}

func TestPutItem_hashKeyTypeMismatchNonScalar(t *testing.T) {
	// Given: a hash-only table whose partition key is declared String
	srv := helpers.NewTestServer(t)
	createTable(t, srv, "ks-put-map")

	// When: PutItem supplies the partition key as a Map
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "ks-put-map",
		"Item": map[string]any{
			"id": map[string]any{"M": map[string]any{"a": map[string]any{"S": "b"}}},
		},
	})
	defer resp.Body.Close()

	// Then: the actual type is reported as the document type it is
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"One or more parameter values were invalid: Type mismatch for key id expected: S actual: M")
}

func TestPutItem_missingSortKey(t *testing.T) {
	// Given: a table with a partition key and a sort key
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-put-missing-sort")

	// When: PutItem omits the sort key
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "ks-put-missing-sort",
		"Item": map[string]any{
			"pk": map[string]any{"S": "b"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS names the missing key attribute
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"One or more parameter values were invalid: Missing the key sk in the item")
}

func TestPutItem_missingHashKey(t *testing.T) {
	// Given: a table with a partition key and a sort key
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-put-missing-hash")

	// When: PutItem omits the partition key
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "ks-put-missing-hash",
		"Item": map[string]any{
			"sk":   map[string]any{"N": "1"},
			"note": map[string]any{"S": "orphan"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS names the missing partition key
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"One or more parameter values were invalid: Missing the key pk in the item")
}

func TestPutItem_nonKeyAttributesAreSchemaless(t *testing.T) {
	// Given: a table whose key attributes are pk (S) and sk (N)
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-put-schemaless")

	// When: PutItem writes a correctly typed key alongside non-key attributes
	// of every DynamoDB type, including ones sharing a key attribute's name
	// prefix and ones that would be invalid as a key
	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "ks-put-schemaless",
		"Item": map[string]any{
			"pk":       map[string]any{"S": "b"},
			"sk":       map[string]any{"N": "1"},
			"aString":  map[string]any{"S": "x"},
			"aNumber":  map[string]any{"N": "2.5"},
			"aBinary":  map[string]any{"B": "ZGF0YQ=="},
			"aBool":    map[string]any{"BOOL": true},
			"aNull":    map[string]any{"NULL": true},
			"aList":    map[string]any{"L": []any{map[string]any{"S": "one"}}},
			"aMap":     map[string]any{"M": map[string]any{"in": map[string]any{"N": "3"}}},
			"aStrSet":  map[string]any{"SS": []any{"a", "b"}},
			"aNumSet":  map[string]any{"NS": []any{"1", "2"}},
			"aBinSet":  map[string]any{"BS": []any{"ZGF0YQ=="}},
			"pkPrefix": map[string]any{"N": "9"},
		},
	})
	defer resp.Body.Close()

	// Then: the write succeeds — nothing outside the key schema is typed
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestPutItem_numericSortKeyRoundTrips(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-roundtrip")

	// When: a correctly typed item is written and read back
	putItem(t, srv, "ks-roundtrip", map[string]any{
		"pk":   map[string]any{"S": "b"},
		"sk":   map[string]any{"N": "10"},
		"note": map[string]any{"S": "kept"},
	})
	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "ks-roundtrip",
		"Key": map[string]any{
			"pk": map[string]any{"S": "b"},
			"sk": map[string]any{"N": "10"},
		},
	})
	defer resp.Body.Close()

	// Then: the item comes back intact
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Item map[string]map[string]any `json:"Item"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if got := result.Item["note"]["S"]; got != "kept" {
		t.Errorf("note = %v, want %q", got, "kept")
	}
	if got := result.Item["sk"]["N"]; got != "10" {
		t.Errorf("sk = %v, want %q", got, "10")
	}
}

// ---- GetItem / DeleteItem / UpdateItem -------------------------------------

func TestGetItem_keyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-get-type")

	// When: GetItem supplies the sort key as a String
	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "ks-get-type",
		"Key": map[string]any{
			"pk": map[string]any{"S": "b"},
			"sk": map[string]any{"S": "10"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS's schema-mismatch ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
}

func TestGetItem_missingSortKey(t *testing.T) {
	// Given: a table with a composite key
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-get-missing")

	// When: GetItem supplies only the partition key
	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "ks-get-missing",
		"Key": map[string]any{
			"pk": map[string]any{"S": "b"},
		},
	})
	defer resp.Body.Close()

	// Then: the same schema-mismatch ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
}

func TestGetItem_extraKeyAttribute(t *testing.T) {
	// Given: a table with a composite key
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-get-extra")

	// When: GetItem's Key carries an attribute the key schema does not name
	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "ks-get-extra",
		"Key": map[string]any{
			"pk":    map[string]any{"S": "b"},
			"sk":    map[string]any{"N": "1"},
			"extra": map[string]any{"S": "nope"},
		},
	})
	defer resp.Body.Close()

	// Then: a Key is exactly the key schema — no more, no fewer
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
}

func TestDeleteItem_keyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-delete-type")

	// When: DeleteItem supplies the sort key as a String
	resp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName": "ks-delete-type",
		"Key": map[string]any{
			"pk": map[string]any{"S": "b"},
			"sk": map[string]any{"S": "10"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS's schema-mismatch ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
}

func TestUpdateItem_keyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-update-type")

	// When: UpdateItem supplies the sort key as a String
	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "ks-update-type",
		"Key": map[string]any{
			"pk": map[string]any{"S": "b"},
			"sk": map[string]any{"S": "10"},
		},
		"UpdateExpression":          "SET #n = :v",
		"ExpressionAttributeNames":  map[string]any{"#n": "note"},
		"ExpressionAttributeValues": map[string]any{":v": map[string]any{"S": "x"}},
	})
	defer resp.Body.Close()

	// Then: AWS's schema-mismatch ValidationException, and no upsert happened
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
	assertItemCount(t, srv, "ks-update-type", 0)
}

// ---- BatchWriteItem / BatchGetItem -----------------------------------------

func TestBatchWriteItem_putKeyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-batch-put")

	// When: one PutRequest in the batch carries a String sort key
	resp := ddbCall(t, srv, "BatchWriteItem", map[string]any{
		"RequestItems": map[string]any{
			"ks-batch-put": []map[string]any{
				{"PutRequest": map[string]any{"Item": map[string]any{
					"pk": map[string]any{"S": "good"},
					"sk": map[string]any{"N": "1"},
				}}},
				{"PutRequest": map[string]any{"Item": map[string]any{
					"pk": map[string]any{"S": "bad"},
					"sk": map[string]any{"S": "one"},
				}}},
			},
		},
	})
	defer resp.Body.Close()

	// Then: the whole call is rejected, and nothing in it was written
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"One or more parameter values were invalid: Type mismatch for key sk expected: N actual: S")
	assertItemCount(t, srv, "ks-batch-put", 0)
}

func TestBatchWriteItem_deleteKeyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-batch-delete")

	// When: a DeleteRequest carries a String sort key
	resp := ddbCall(t, srv, "BatchWriteItem", map[string]any{
		"RequestItems": map[string]any{
			"ks-batch-delete": []map[string]any{
				{"DeleteRequest": map[string]any{"Key": map[string]any{
					"pk": map[string]any{"S": "b"},
					"sk": map[string]any{"S": "one"},
				}}},
			},
		},
	})
	defer resp.Body.Close()

	// Then: AWS's schema-mismatch ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
}

func TestBatchGetItem_keyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-batch-get")

	// When: one requested key carries a String sort key
	resp := ddbCall(t, srv, "BatchGetItem", map[string]any{
		"RequestItems": map[string]any{
			"ks-batch-get": map[string]any{
				"Keys": []map[string]any{
					{"pk": map[string]any{"S": "b"}, "sk": map[string]any{"S": "one"}},
				},
			},
		},
	})
	defer resp.Body.Close()

	// Then: AWS's schema-mismatch ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
}

// ---- TransactWriteItems / TransactGetItems ---------------------------------

// A key-schema disagreement is a parameter-validation fault, which DynamoDB
// answers before it attempts the transaction at all — so it surfaces as a
// plain ValidationException, not as a TransactionCanceledException with a
// ValidationError cancellation reason. The cancellation reasons AWS documents
// under ValidationError are the ones that only become knowable while applying
// the transaction (item size, LSI size, update-expression faults), not a
// static check of the supplied key.
// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_TransactWriteItems.html

func TestTransactWriteItems_putKeyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-tx-put")

	// When: a Put action in the transaction carries a String sort key
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []map[string]any{
			{"Put": map[string]any{
				"TableName": "ks-tx-put",
				"Item": map[string]any{
					"pk": map[string]any{"S": "good"},
					"sk": map[string]any{"N": "1"},
				},
			}},
			{"Put": map[string]any{
				"TableName": "ks-tx-put",
				"Item": map[string]any{
					"pk": map[string]any{"S": "bad"},
					"sk": map[string]any{"S": "one"},
				},
			}},
		},
	})
	defer resp.Body.Close()

	// Then: a plain ValidationException, and the transaction wrote nothing
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp,
		"One or more parameter values were invalid: Type mismatch for key sk expected: N actual: S")
	assertItemCount(t, srv, "ks-tx-put", 0)
}

func TestTransactWriteItems_deleteKeyTypeMismatch(t *testing.T) {
	// Given: a table holding one item
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-tx-delete")
	putItem(t, srv, "ks-tx-delete", map[string]any{
		"pk": map[string]any{"S": "b"},
		"sk": map[string]any{"N": "1"},
	})

	// When: a Delete action supplies a String sort key
	resp := ddbCall(t, srv, "TransactWriteItems", map[string]any{
		"TransactItems": []map[string]any{
			{"Delete": map[string]any{
				"TableName": "ks-tx-delete",
				"Key": map[string]any{
					"pk": map[string]any{"S": "b"},
					"sk": map[string]any{"S": "1"},
				},
			}},
		},
	})
	defer resp.Body.Close()

	// Then: AWS's schema-mismatch ValidationException, and nothing was deleted
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
	assertItemCount(t, srv, "ks-tx-delete", 1)
}

func TestTransactGetItems_keyTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-tx-get")

	// When: a Get action supplies a String sort key
	resp := ddbCall(t, srv, "TransactGetItems", map[string]any{
		"TransactItems": []map[string]any{
			{"Get": map[string]any{
				"TableName": "ks-tx-get",
				"Key": map[string]any{
					"pk": map[string]any{"S": "b"},
					"sk": map[string]any{"S": "1"},
				},
			}},
		},
	})
	defer resp.Body.Close()

	// Then: AWS's schema-mismatch ValidationException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantKeyElementMismatch)
}

// ---- Query -----------------------------------------------------------------

func TestQuery_sortKeyConditionTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number, holding one item
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-query-sort")
	putItem(t, srv, "ks-query-sort", map[string]any{
		"pk": map[string]any{"S": "b"},
		"sk": map[string]any{"N": "10"},
	})

	// When: the sort key condition compares against a String
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "ks-query-sort",
		"KeyConditionExpression": "pk = :p AND sk = :s",
		"ExpressionAttributeValues": map[string]any{
			":p": map[string]any{"S": "b"},
			":s": map[string]any{"S": "10"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS rejects the condition rather than silently matching nothing
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantConditionTypeMismatch)
}

func TestQuery_hashKeyConditionTypeMismatch(t *testing.T) {
	// Given: a table whose partition key is declared String
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-query-hash")

	// When: the partition key condition compares against a Number
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "ks-query-hash",
		"KeyConditionExpression": "pk = :p",
		"ExpressionAttributeValues": map[string]any{
			":p": map[string]any{"N": "1"},
		},
	})
	defer resp.Body.Close()

	// Then: AWS rejects the condition
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantConditionTypeMismatch)
}

func TestQuery_betweenSortKeyConditionTypeMismatch(t *testing.T) {
	// Given: a table whose sort key is declared Number
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-query-between")

	// When: BETWEEN's upper bound is a String
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "ks-query-between",
		"KeyConditionExpression": "pk = :p AND sk BETWEEN :lo AND :hi",
		"ExpressionAttributeValues": map[string]any{
			":p":  map[string]any{"S": "b"},
			":lo": map[string]any{"N": "1"},
			":hi": map[string]any{"S": "9"},
		},
	})
	defer resp.Body.Close()

	// Then: every value in the condition is type-checked, not just the first
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	assertValidationMessage(t, resp, wantConditionTypeMismatch)
}

func TestQuery_correctlyTypedNumericSortKeyMatches(t *testing.T) {
	// Given: a table whose sort key is declared Number, holding two items
	srv := helpers.NewTestServer(t)
	createKeySchemaTable(t, srv, "ks-query-ok")
	putItem(t, srv, "ks-query-ok", map[string]any{
		"pk": map[string]any{"S": "b"},
		"sk": map[string]any{"N": "5"},
	})
	putItem(t, srv, "ks-query-ok", map[string]any{
		"pk": map[string]any{"S": "b"},
		"sk": map[string]any{"N": "10"},
	})

	// When: a correctly typed numeric range condition is used
	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "ks-query-ok",
		"KeyConditionExpression": "pk = :p AND sk > :s",
		"ExpressionAttributeValues": map[string]any{
			":p": map[string]any{"S": "b"},
			":s": map[string]any{"N": "6"},
		},
	})
	defer resp.Body.Close()

	// Then: it still matches numerically — validation did not change ordering
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Count int                         `json:"Count"`
		Items []map[string]map[string]any `json:"Items"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != 1 {
		t.Fatalf("Count = %d, want 1 (items: %v)", result.Count, result.Items)
	}
	if got := result.Items[0]["sk"]["N"]; got != "10" {
		t.Errorf("sk = %v, want %q", got, "10")
	}
}

// ---- Test helpers ----------------------------------------------------------

// assertItemCount fails unless a Scan of the table returns exactly want items.
func assertItemCount(t *testing.T, srv *helpers.TestServer, tableName string, want int) {
	t.Helper()
	resp := ddbCall(t, srv, "Scan", map[string]any{"TableName": tableName})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan %q: status %d: %s", tableName, resp.StatusCode, helpers.ReadBody(t, resp))
	}
	var result struct {
		Count int `json:"Count"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Count != want {
		t.Errorf("%s holds %d items, want %d", tableName, result.Count, want)
	}
}
