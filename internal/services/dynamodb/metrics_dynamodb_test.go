package dynamodb

// metrics_dynamodb_test.go proves the phase-2 AWS/DynamoDB metric catalogue
// (metrics_dynamodb.go, docs/plans/service-metrics-platform.md) is recorded
// at each operation's "xxxTyped" wrapper, using a real
// internal/metrics.Recorder (not a stub) and reading it back the same way
// CloudWatch's read-through does — metrics.Service.QueryRange.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/state"
)

func newMetricsTestService(t *testing.T) (*Service, *metrics.Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), mock, events.NewBus())
	rec := metrics.NewRecorder(state.NewMemoryStore(), mock, zap.NewNop())
	svc.InitMetrics(rec)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rec.Stop(ctx)
	})
	return svc, rec, mock
}

func ddbSum(t *testing.T, rec *metrics.Service, name string, dims []metrics.Dimension, now time.Time) float64 {
	t.Helper()
	buckets, err := rec.QueryRange(context.Background(), "AWS/DynamoDB", name, dims, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange %s: %v", name, err)
	}
	var sum float64
	for _, b := range buckets {
		sum += b.Sum
	}
	return sum
}

func mustCreateTable(t *testing.T, svc *Service, name string) {
	t.Helper()
	_, aerr := svc.handler.createTableTyped(context.Background(), &createTableRequest{
		TableName:            name,
		KeySchema:            []KeySchemaElement{{AttributeName: "id", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{{AttributeName: "id", AttributeType: "S"}},
		BillingMode:          "PAY_PER_REQUEST",
	})
	if aerr != nil {
		t.Fatalf("CreateTable: %v", aerr.Message)
	}
}

func TestPutItem_RecordsLatencyAndConsumedWriteCapacity(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)
	mustCreateTable(t, svc, "orders")

	_, aerr := svc.handler.putItemTyped(context.Background(), &putItemRequest{
		TableName: "orders",
		Item:      Item{"id": attrValue{"S": "o1"}, "status": attrValue{"S": "placed"}},
	})
	if aerr != nil {
		t.Fatalf("PutItem: %v", aerr.Message)
	}

	now := mock.Now().UTC()
	dims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "Operation", Value: "PutItem"}}
	if got := ddbSum(t, rec, "SuccessfulRequestLatency", dims, now); got < 0 {
		t.Fatalf("SuccessfulRequestLatency Sum = %v, want >= 0", got)
	}
	wcuDims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "Source", Value: "Customer"}}
	if got := ddbSum(t, rec, "ConsumedWriteCapacityUnits", wcuDims, now); got < 1 {
		t.Fatalf("ConsumedWriteCapacityUnits Sum = %v, want >= 1", got)
	}
}

func TestGetItem_RecordsConsumedReadCapacity(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)
	mustCreateTable(t, svc, "orders")
	if _, aerr := svc.handler.putItemTyped(context.Background(), &putItemRequest{
		TableName: "orders", Item: Item{"id": attrValue{"S": "o1"}},
	}); aerr != nil {
		t.Fatalf("seed PutItem: %v", aerr.Message)
	}

	_, aerr := svc.handler.getItemTyped(context.Background(), &getItemRequest{
		TableName: "orders", Key: Item{"id": attrValue{"S": "o1"}},
	})
	if aerr != nil {
		t.Fatalf("GetItem: %v", aerr.Message)
	}

	now := mock.Now().UTC()
	rcuDims := []metrics.Dimension{{Name: "TableName", Value: "orders"}}
	if got := ddbSum(t, rec, "ConsumedReadCapacityUnits", rcuDims, now); got < 0.5 {
		t.Fatalf("ConsumedReadCapacityUnits Sum = %v, want >= 0.5", got)
	}
}

// TestPutItem_ConditionalCheckFailed_NotCountedAsUserError pins AWS's own
// carve-out: a ConditionalCheckFailedException is a UserErrors exclusion
// (it has its own ConditionalCheckFailedRequests metric instead — not
// implemented here, phase 2 scope is the metrics named in the issue).
func TestPutItem_ConditionalCheckFailed_NotCountedAsUserError(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)
	mustCreateTable(t, svc, "orders")
	if _, aerr := svc.handler.putItemTyped(context.Background(), &putItemRequest{
		TableName: "orders", Item: Item{"id": attrValue{"S": "o1"}},
	}); aerr != nil {
		t.Fatalf("seed PutItem: %v", aerr.Message)
	}

	_, aerr := svc.handler.putItemTyped(context.Background(), &putItemRequest{
		TableName:           "orders",
		Item:                Item{"id": attrValue{"S": "o1"}},
		ConditionExpression: "attribute_not_exists(id)",
	})
	if aerr == nil || aerr.Code != "ConditionalCheckFailedException" {
		t.Fatalf("expected ConditionalCheckFailedException, got %+v", aerr)
	}

	now := mock.Now().UTC()
	if got := ddbSum(t, rec, "UserErrors", nil, now); got != 0 {
		t.Fatalf("UserErrors Sum after a ConditionalCheckFailedException = %v, want 0 (AWS excludes it)", got)
	}
}

// TestGetItem_MissingTableName_RecordsUserError pins UserErrors' account-wide,
// dimensionless shape (AWS: "UserErrors represents the aggregate ... for the
// current AWS Region and the current AWS account").
func TestGetItem_MissingTableName_RecordsUserError(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)

	_, aerr := svc.handler.getItemTyped(context.Background(), &getItemRequest{Key: Item{"id": attrValue{"S": "o1"}}})
	if aerr == nil {
		t.Fatalf("expected an error for a missing TableName")
	}

	now := mock.Now().UTC()
	if got, want := ddbSum(t, rec, "UserErrors", nil, now), 1.0; got != want {
		t.Fatalf("UserErrors Sum = %v, want %v", got, want)
	}
}

// TestGetItem_SystemError_RecordsSystemErrors pins the HTTP-500 branch by
// forcing the underlying store into an internal-error state via an
// unreadable table record.
func TestGetItem_SystemError_RecordsSystemErrors(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)
	mustCreateTable(t, svc, "orders")

	// Corrupt the persisted table record so getTable returns ErrInternalError
	// rather than ResourceNotFound — a genuine HTTP 500 fact, not a client
	// mistake.
	region := "us-east-1"
	if err := svc.handler.store.tables.Set(context.Background(), nsTables, region+"/orders", "{not json"); err != nil {
		t.Fatalf("corrupt table record: %v", err)
	}

	_, aerr := svc.handler.getItemTyped(context.Background(), &getItemRequest{
		TableName: "orders", Key: Item{"id": attrValue{"S": "o1"}},
	})
	if aerr == nil || aerr.HTTPStatus < 500 {
		t.Fatalf("expected an HTTP 500 error from a corrupt table record, got %+v", aerr)
	}

	now := mock.Now().UTC()
	dims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "Operation", Value: "GetItem"}}
	if got, want := ddbSum(t, rec, "SystemErrors", dims, now), 1.0; got != want {
		t.Fatalf("SystemErrors Sum = %v, want %v", got, want)
	}
}

func TestQuery_RecordsConsumedReadCapacityWithGSIDimension(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)
	_, aerr := svc.handler.createTableTyped(context.Background(), &createTableRequest{
		TableName:            "orders",
		KeySchema:            []KeySchemaElement{{AttributeName: "id", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{{AttributeName: "id", AttributeType: "S"}, {AttributeName: "status", AttributeType: "S"}},
		BillingMode:          "PAY_PER_REQUEST",
		GlobalSecondaryIndexes: []SecondaryIndex{{
			IndexName:  "status-index",
			KeySchema:  []KeySchemaElement{{AttributeName: "status", KeyType: "HASH"}},
			Projection: Projection{ProjectionType: "ALL"},
		}},
	})
	if aerr != nil {
		t.Fatalf("CreateTable: %v", aerr.Message)
	}
	if _, aerr := svc.handler.putItemTyped(context.Background(), &putItemRequest{
		TableName: "orders", Item: Item{"id": attrValue{"S": "o1"}, "status": attrValue{"S": "placed"}},
	}); aerr != nil {
		t.Fatalf("seed PutItem: %v", aerr.Message)
	}

	_, aerr = svc.handler.queryTyped(context.Background(), &queryRequest{
		TableName:                 "orders",
		IndexName:                 "status-index",
		KeyConditionExpression:    "status = :s",
		ExpressionAttributeValues: map[string]attrValue{":s": {"S": "placed"}},
	})
	if aerr != nil {
		t.Fatalf("Query: %v", aerr.Message)
	}

	now := mock.Now().UTC()
	gsiDims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "GlobalSecondaryIndexName", Value: "status-index"}}
	if got := ddbSum(t, rec, "ConsumedReadCapacityUnits", gsiDims, now); got < 0.5 {
		t.Fatalf("ConsumedReadCapacityUnits{TableName,GlobalSecondaryIndexName} Sum = %v, want >= 0.5", got)
	}
	latDims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "Operation", Value: "Query"}}
	if got := ddbSum(t, rec, "SuccessfulRequestLatency", latDims, now); got < 0 {
		t.Fatalf("SuccessfulRequestLatency Sum = %v, want >= 0", got)
	}
}

func TestBatchWriteItem_RecordsPerTableConsumedWriteCapacity(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)
	mustCreateTable(t, svc, "orders")
	mustCreateTable(t, svc, "shipments")

	_, aerr := svc.handler.batchWriteItemTyped(context.Background(), &batchWriteItemRequest{
		RequestItems: map[string][]writeRequest{
			"orders":    {{PutRequest: &putRequest{Item: Item{"id": attrValue{"S": "o1"}}}}},
			"shipments": {{PutRequest: &putRequest{Item: Item{"id": attrValue{"S": "s1"}}}}},
		},
	})
	if aerr != nil {
		t.Fatalf("BatchWriteItem: %v", aerr.Message)
	}

	now := mock.Now().UTC()
	for _, table := range []string{"orders", "shipments"} {
		dims := []metrics.Dimension{{Name: "TableName", Value: table}, {Name: "Source", Value: "Customer"}}
		if got := ddbSum(t, rec, "ConsumedWriteCapacityUnits", dims, now); got < 1 {
			t.Fatalf("ConsumedWriteCapacityUnits Sum for %s = %v, want >= 1", table, got)
		}
	}
}

func TestTransactWriteItems_RecordsDoubleWeightedWriteCapacity(t *testing.T) {
	svc, rec, mock := newMetricsTestService(t)
	mustCreateTable(t, svc, "orders")

	_, aerr := svc.handler.transactWriteItemsTyped(context.Background(), &transactWriteItemsRequest{
		TransactItems: []transactWriteItem{
			{Put: &transactPut{TableName: "orders", Item: Item{"id": attrValue{"S": "o1"}}}},
		},
	})
	if aerr != nil {
		t.Fatalf("TransactWriteItems: %v", aerr.Message)
	}

	now := mock.Now().UTC()
	dims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "Source", Value: "Customer"}}
	transactUnits := ddbSum(t, rec, "ConsumedWriteCapacityUnits", dims, now)
	if transactUnits < 2 {
		t.Fatalf("ConsumedWriteCapacityUnits Sum for a 1-item TransactWriteItems = %v, want >= 2 (AWS doubles transactional writes)", transactUnits)
	}
	latDims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "Operation", Value: "TransactWriteItems"}}
	if got := ddbSum(t, rec, "SuccessfulRequestLatency", latDims, now); got < 0 {
		t.Fatalf("SuccessfulRequestLatency Sum = %v, want >= 0", got)
	}
}

func TestDynamoDBMetrics_NilRecorderIsNoOp(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New(), events.NewBus())
	mustCreateTable(t, svc, "orders")
	if _, aerr := svc.handler.putItemTyped(context.Background(), &putItemRequest{
		TableName: "orders", Item: Item{"id": attrValue{"S": "o1"}},
	}); aerr != nil {
		t.Fatalf("PutItem with nil metrics recorder must still succeed: %v", aerr.Message)
	}
}
