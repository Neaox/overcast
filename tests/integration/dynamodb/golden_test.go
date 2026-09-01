// golden_test.go contains wire-byte golden tests for DynamoDB — see
// docs/plans/wire-byte-goldens.md for the harness design.
//
// DynamoDB is §5.1's P2 priority (highest traffic, all typed): unlike SQS,
// this service serves only the JSON protocol family here — SupportedProtocols
// in internal/services/dynamodb/typed_ops.go returns JSON10, JSON11, and
// RPCv2CBOR, with no Query/XML variant — so there is exactly one golden per
// operation, not a JSON/XML pair.
//
// Coverage — deterministic, high-traffic operations:
//
//	CreateTable, DescribeTable, ListTables, PutItem, GetItem, Query, Scan,
//	UpdateItem, DeleteItem, DeleteTable
//
// Plus one error-shape golden (GetItem against a nonexistent table, mirroring
// TestGetItem_tableNotFound / TestDescribeTable_notFound in dynamodb_test.go)
// asserting the exact ResourceNotFoundException wire bytes:
//
//	GetItem_notFound
//
// Table ARNs are built deterministically from region + account + table name
// (internal/protocol/arn.go), timestamps (CreationDateTime) are driven by the
// injected clock, and Query/Scan read from an ordered (hashKey, sortKey)
// tree/SQL index (see the itemBackend doc comment in item_store.go) — never a
// Go map — so item order is stable across runs without a normalize step for
// any of that. CreateTable and DescribeTable are the two exceptions: TableId
// (#1272) is a uuid.New()-minted UUID, so normalizeDynamoDBTableId redacts it
// to a fixed placeholder, following the same pattern as
// tests/integration/ec2/golden_test.go's normalizeEC2IDs and
// tests/integration/iam/golden_test.go's normalizeIAM. Every other GoldenTest
// call below still passes nil.
//
// Excluded (message-plane / non-deterministic / no golden value):
//
//   - BatchGetItem, BatchWriteItem, TransactGetItems, TransactWriteItems:
//     structurally identical to their single-item counterparts already
//     covered (GetItem/PutItem/DeleteItem) — a golden here would pin the same
//     item-envelope encoding a second time for no added coverage.
//   - Query/Scan against a GSI, ConsistentRead, parallel Scan (Segment/
//     TotalSegments): covered by dynamodb_test.go's behavioral tests; the
//     base-table single-golden here already exercises the same item/key
//     encoding these paths share.
//
// Record: go test ./tests/integration/dynamodb/ -run TestGolden -record
// Assert: go test ./tests/integration/dynamodb/ -run TestGolden
package dynamodb_test

import (
	"regexp"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const dynamodbGoldenDir = "goldens"

// tableIdRE matches DynamoDB's TableId — a UUID minted once by CreateTable
// (internal/services/dynamodb/handler.go's createTableTyped, via
// uuid.New()) and echoed unchanged by DescribeTable/UpdateTable/DeleteTable.
var tableIdRE = regexp.MustCompile(`"TableId":"[0-9a-fA-F-]+"`)

// normalizeDynamoDBTableId redacts TableId to a fixed placeholder so
// CreateTable/DescribeTable goldens compare byte-for-byte across the record
// run and every later assert run.
func normalizeDynamoDBTableId(body []byte) []byte {
	return tableIdRE.ReplaceAll(body, []byte(`"TableId":"00000000-0000-0000-0000-000000000000"`))
}

func TestGolden_CreateTable(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := ddbCall(t, srv, "CreateTable", map[string]any{
		"TableName": "golden-table",
		"AttributeDefinitions": []map[string]any{
			{"AttributeName": "id", "AttributeType": "S"},
		},
		"KeySchema": []map[string]any{
			{"AttributeName": "id", "KeyType": "HASH"},
		},
		"BillingMode": "PAY_PER_REQUEST",
	})
	helpers.GoldenTest(t, dynamodbGoldenDir, "CreateTable", resp, normalizeDynamoDBTableId)
}

func TestGolden_DescribeTable(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table")

	resp := ddbCall(t, srv, "DescribeTable", map[string]any{"TableName": "golden-table"})
	helpers.GoldenTest(t, dynamodbGoldenDir, "DescribeTable", resp, normalizeDynamoDBTableId)
}

func TestGolden_ListTables(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table-a")
	createTable(t, srv, "golden-table-b")

	resp := ddbCall(t, srv, "ListTables", map[string]any{})
	helpers.GoldenTest(t, dynamodbGoldenDir, "ListTables", resp, nil)
}

func TestGolden_PutItem(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table")

	resp := ddbCall(t, srv, "PutItem", map[string]any{
		"TableName": "golden-table",
		"Item": map[string]any{
			"id":   map[string]string{"S": "item-1"},
			"name": map[string]string{"S": "Alice"},
			"age":  map[string]string{"N": "30"},
		},
	})
	helpers.GoldenTest(t, dynamodbGoldenDir, "PutItem", resp, nil)
}

func TestGolden_GetItem(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table")
	putItem(t, srv, "golden-table", map[string]any{
		"id":   map[string]string{"S": "item-1"},
		"name": map[string]string{"S": "Alice"},
		"age":  map[string]string{"N": "30"},
	})

	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "golden-table",
		"Key": map[string]any{
			"id": map[string]string{"S": "item-1"},
		},
	})
	helpers.GoldenTest(t, dynamodbGoldenDir, "GetItem", resp, nil)
}

func TestGolden_GetItem_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())

	resp := ddbCall(t, srv, "GetItem", map[string]any{
		"TableName": "no-such-golden-table",
		"Key": map[string]any{
			"id": map[string]string{"S": "item-1"},
		},
	})
	helpers.GoldenTest(t, dynamodbGoldenDir, "GetItem_notFound", resp, nil)
}

func TestGolden_Query(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTableWithSortKey(t, srv, "golden-events", "userId", "S", "eventId", "S")
	putItem(t, srv, "golden-events", map[string]any{
		"userId":  map[string]string{"S": "user-1"},
		"eventId": map[string]string{"S": "event-1"},
		"kind":    map[string]string{"S": "login"},
	})
	putItem(t, srv, "golden-events", map[string]any{
		"userId":  map[string]string{"S": "user-1"},
		"eventId": map[string]string{"S": "event-2"},
		"kind":    map[string]string{"S": "logout"},
	})
	putItem(t, srv, "golden-events", map[string]any{
		"userId":  map[string]string{"S": "user-2"},
		"eventId": map[string]string{"S": "event-1"},
		"kind":    map[string]string{"S": "login"},
	})

	resp := ddbCall(t, srv, "Query", map[string]any{
		"TableName":              "golden-events",
		"KeyConditionExpression": "userId = :uid",
		"ExpressionAttributeValues": map[string]any{
			":uid": map[string]string{"S": "user-1"},
		},
	})
	helpers.GoldenTest(t, dynamodbGoldenDir, "Query", resp, nil)
}

func TestGolden_Scan(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table")
	putItem(t, srv, "golden-table", map[string]any{
		"id":   map[string]string{"S": "item-1"},
		"name": map[string]string{"S": "Alice"},
	})
	putItem(t, srv, "golden-table", map[string]any{
		"id":   map[string]string{"S": "item-2"},
		"name": map[string]string{"S": "Bob"},
	})

	resp := ddbCall(t, srv, "Scan", map[string]any{"TableName": "golden-table"})
	helpers.GoldenTest(t, dynamodbGoldenDir, "Scan", resp, nil)
}

func TestGolden_UpdateItem(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table")
	putItem(t, srv, "golden-table", map[string]any{
		"id":   map[string]string{"S": "item-1"},
		"name": map[string]string{"S": "Alice"},
	})

	resp := ddbCall(t, srv, "UpdateItem", map[string]any{
		"TableName": "golden-table",
		"Key": map[string]any{
			"id": map[string]string{"S": "item-1"},
		},
		"UpdateExpression":         "SET #a = :v",
		"ExpressionAttributeNames": map[string]string{"#a": "age"},
		"ExpressionAttributeValues": map[string]any{
			":v": map[string]string{"N": "31"},
		},
		"ReturnValues": "ALL_NEW",
	})
	helpers.GoldenTest(t, dynamodbGoldenDir, "UpdateItem", resp, nil)
}

func TestGolden_DeleteItem(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table")
	putItem(t, srv, "golden-table", map[string]any{
		"id":   map[string]string{"S": "item-1"},
		"name": map[string]string{"S": "Alice"},
	})

	resp := ddbCall(t, srv, "DeleteItem", map[string]any{
		"TableName": "golden-table",
		"Key": map[string]any{
			"id": map[string]string{"S": "item-1"},
		},
	})
	helpers.GoldenTest(t, dynamodbGoldenDir, "DeleteItem", resp, nil)
}

// TestGolden_DeleteTable pins DeleteTable's wire shape (issue #1288):
// TableDescription (not Table), with TableStatus DELETING, matching
// CreateTable/DescribeTable/UpdateTable's response envelope and AWS's
// documented DeleteTable ResponseSyntax.
func TestGolden_DeleteTable(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	createTable(t, srv, "golden-table")

	resp := ddbCall(t, srv, "DeleteTable", map[string]any{"TableName": "golden-table"})
	helpers.GoldenTest(t, dynamodbGoldenDir, "DeleteTable", resp, normalizeDynamoDBTableId)
}
