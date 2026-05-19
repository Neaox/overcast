package groups

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func DynamoDB(c *clients.Clients) ServiceGroup {
	g := &ddbGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateTable":                g.CreateTable,
			"DescribeTable":              g.DescribeTable,
			"ListTables":                 g.ListTables,
			"UpdateTable":                g.UpdateTable,
			"DeleteTable":                g.DeleteTable,
			"PutItem":                    g.PutItem,
			"PutItemConditionFail":       g.PutItemConditionFail,
			"GetItem":                    g.GetItem,
			"UpdateItem":                 g.UpdateItem,
			"DeleteItem":                 g.DeleteItem,
			"Scan":                       g.Scan,
			"Query":                      g.Query,
			"QueryWithFilterExpression":  g.QueryWithFilterExpression,
			"QueryWithLimit":             g.QueryWithLimit,
			"QueryPagination":            g.QueryPagination,
			"ScanWithFilter":             g.ScanWithFilter,
			"BatchWriteItem":             g.BatchWriteItem,
			"BatchGetItem":               g.BatchGetItem,
			"TransactWriteItems":         g.TransactWriteItems,
			"TransactGetItems":           g.TransactGetItems,
			"TransactWriteConditionFail": g.TransactWriteConditionFail,
			"UpdateTimeToLive":           g.UpdateTimeToLive,
			"DescribeTimeToLive":         g.DescribeTimeToLive,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"dynamodb-tables": g.setupTable,
			"dynamodb-items":  g.setupItems,
			"dynamodb-query":  g.setupQuery,
			"dynamodb-batch":  g.setupBatch,
			"dynamodb-txn":    g.setupTxn,
			"dynamodb-ttl":    g.setupTTL,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"dynamodb-tables": g.teardownTable("ddb_table"),
			"dynamodb-items":  g.teardownTable("ddb_items_table"),
			"dynamodb-query":  g.teardownTable("ddb_query_table"),
			"dynamodb-batch":  g.teardownTable("ddb_batch_table"),
			"dynamodb-txn":    g.teardownTable("ddb_txn_table"),
			"dynamodb-ttl":    g.teardownTable("ddb_ttl_table"),
		},
	}
}

type ddbGroup struct{ c *clients.Clients }

func (g *ddbGroup) client() *ddb.Client { return g.c.DynamoDB() }

func (g *ddbGroup) waitActive(ctx context.Context, name string) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := g.client().DescribeTable(ctx, &ddb.DescribeTableInput{TableName: aws.String(name)})
		if err == nil && resp.Table.TableStatus == ddbtypes.TableStatusActive {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("table %q did not become ACTIVE", name)
}

func (g *ddbGroup) createTable(ctx context.Context, name string) error {
	_, err := g.client().CreateTable(ctx, &ddb.CreateTableInput{
		TableName: aws.String(name),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		return err
	}
	return g.waitActive(ctx, name)
}

func (g *ddbGroup) teardownTable(key string) func(context.Context, *harness.TestContext) error {
	return func(ctx context.Context, t *harness.TestContext) error {
		name := t.GetString(key)
		if name == "" {
			return nil
		}
		g.client().DeleteTable(ctx, &ddb.DeleteTableInput{TableName: aws.String(name)}) //nolint:errcheck
		return nil
	}
}

func sv(s string) ddbtypes.AttributeValue { return &ddbtypes.AttributeValueMemberS{Value: s} }
func nv(s string) ddbtypes.AttributeValue { return &ddbtypes.AttributeValueMemberN{Value: s} }

// ── dynamodb-tables ──────────────────────────────────────────────────────────

func (g *ddbGroup) setupTable(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddb", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	t.Set("ddb_table", name)
	return nil
}

func (g *ddbGroup) CreateTable(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddbcreate", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	g.client().DeleteTable(ctx, &ddb.DeleteTableInput{TableName: aws.String(name)}) //nolint:errcheck
	return nil
}

func (g *ddbGroup) DescribeTable(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_table")
	resp, err := g.client().DescribeTable(ctx, &ddb.DescribeTableInput{TableName: aws.String(name)})
	if err != nil {
		return err
	}
	if aws.ToString(resp.Table.TableName) != name {
		return fmt.Errorf("DescribeTable: name mismatch")
	}
	return nil
}

func (g *ddbGroup) ListTables(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_table")
	resp, err := g.client().ListTables(ctx, &ddb.ListTablesInput{})
	if err != nil {
		return err
	}
	for _, n := range resp.TableNames {
		if n == name {
			return nil
		}
	}
	return fmt.Errorf("ListTables: %q not found", name)
}

func (g *ddbGroup) UpdateTable(ctx context.Context, t *harness.TestContext) error {
	// UpdateTable with billing changes returns immediately in our emulator
	name := t.GetString("ddb_table")
	_, err := g.client().UpdateTable(ctx, &ddb.UpdateTableInput{
		TableName:   aws.String(name),
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		return err
	}
	resp, err := g.client().DescribeTable(ctx, &ddb.DescribeTableInput{TableName: aws.String(name)})
	if err != nil {
		return fmt.Errorf("UpdateTable: DescribeTable verify failed: %w", err)
	}
	if resp.Table.BillingModeSummary == nil || resp.Table.BillingModeSummary.BillingMode != ddbtypes.BillingModePayPerRequest {
		return fmt.Errorf("UpdateTable: expected BillingMode PAY_PER_REQUEST")
	}
	return nil
}

func (g *ddbGroup) DeleteTable(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddbdel", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	_, err := g.client().DeleteTable(ctx, &ddb.DeleteTableInput{TableName: aws.String(name)})
	if err != nil {
		return err
	}
	resp, err := g.client().ListTables(ctx, &ddb.ListTablesInput{})
	if err != nil {
		return fmt.Errorf("DeleteTable: ListTables verify failed: %w", err)
	}
	for _, tn := range resp.TableNames {
		if tn == name {
			return fmt.Errorf("DeleteTable: table %q still present", name)
		}
	}
	return nil
}

// ── dynamodb-items ────────────────────────────────────────────────────────────

func (g *ddbGroup) setupItems(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddbitems", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	t.Set("ddb_items_table", name)
	return nil
}

func (g *ddbGroup) PutItem(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_items_table")
	_, err := g.client().PutItem(ctx, &ddb.PutItemInput{
		TableName: aws.String(name),
		Item: map[string]ddbtypes.AttributeValue{
			"pk": sv("user-1"), "sk": sv("profile"), "name": sv("Alice"),
		},
	})
	if err != nil {
		return err
	}
	resp, err := g.client().GetItem(ctx, &ddb.GetItemInput{
		TableName: aws.String(name),
		Key:       map[string]ddbtypes.AttributeValue{"pk": sv("user-1"), "sk": sv("profile")},
	})
	if err != nil {
		return fmt.Errorf("PutItem: GetItem verify failed: %w", err)
	}
	if v, ok := resp.Item["name"]; !ok {
		return fmt.Errorf("PutItem: name attribute missing")
	} else if sv, ok := v.(*ddbtypes.AttributeValueMemberS); !ok || sv.Value != "Alice" {
		return fmt.Errorf("PutItem: expected name=Alice")
	}
	return nil
}

func (g *ddbGroup) PutItemConditionFail(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_items_table")
	// Item with pk=user-1, sk=profile was put in PutItem — condition should fail
	_, err := g.client().PutItem(ctx, &ddb.PutItemInput{
		TableName: aws.String(name),
		Item: map[string]ddbtypes.AttributeValue{
			"pk": sv("user-1"), "sk": sv("profile"),
		},
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if err == nil {
		return fmt.Errorf("PutItemConditionFail: expected ConditionalCheckFailedException")
	}
	var ccfe *ddbtypes.ConditionalCheckFailedException
	if !errors.As(err, &ccfe) {
		return fmt.Errorf("PutItemConditionFail: expected ConditionalCheckFailedException, got %T: %v", err, err)
	}
	return nil
}

func (g *ddbGroup) GetItem(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_items_table")
	resp, err := g.client().GetItem(ctx, &ddb.GetItemInput{
		TableName: aws.String(name),
		Key:       map[string]ddbtypes.AttributeValue{"pk": sv("user-1"), "sk": sv("profile")},
	})
	if err != nil {
		return err
	}
	if v, ok := resp.Item["name"].(*ddbtypes.AttributeValueMemberS); !ok || v.Value != "Alice" {
		return fmt.Errorf("GetItem: expected name=Alice")
	}
	return nil
}

func (g *ddbGroup) UpdateItem(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_items_table")
	_, err := g.client().UpdateItem(ctx, &ddb.UpdateItemInput{
		TableName:                 aws.String(name),
		Key:                       map[string]ddbtypes.AttributeValue{"pk": sv("user-1"), "sk": sv("profile")},
		UpdateExpression:          aws.String("SET #n = :v"),
		ExpressionAttributeNames:  map[string]string{"#n": "name"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":v": sv("Bob")},
	})
	if err != nil {
		return err
	}
	// Verify the update persisted
	gi, err := g.client().GetItem(ctx, &ddb.GetItemInput{
		TableName: aws.String(name),
		Key:       map[string]ddbtypes.AttributeValue{"pk": sv("user-1"), "sk": sv("profile")},
	})
	if err != nil {
		return fmt.Errorf("UpdateItem: GetItem failed: %w", err)
	}
	if v, ok := gi.Item["name"].(*ddbtypes.AttributeValueMemberS); !ok || v.Value != "Bob" {
		return fmt.Errorf("UpdateItem: expected name=Bob, got %v", gi.Item["name"])
	}
	return nil
}

func (g *ddbGroup) DeleteItem(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_items_table")
	_, err := g.client().DeleteItem(ctx, &ddb.DeleteItemInput{
		TableName: aws.String(name),
		Key:       map[string]ddbtypes.AttributeValue{"pk": sv("user-1"), "sk": sv("profile")},
	})
	if err != nil {
		return err
	}
	// Verify the item is gone
	gi, err := g.client().GetItem(ctx, &ddb.GetItemInput{
		TableName: aws.String(name),
		Key:       map[string]ddbtypes.AttributeValue{"pk": sv("user-1"), "sk": sv("profile")},
	})
	if err != nil {
		return fmt.Errorf("DeleteItem: GetItem failed: %w", err)
	}
	if len(gi.Item) > 0 {
		return fmt.Errorf("DeleteItem: item still present after delete")
	}
	return nil
}

func (g *ddbGroup) Scan(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_query_table")
	// Repopulate for scan
	for i := 0; i < 3; i++ {
		g.client().PutItem(ctx, &ddb.PutItemInput{ //nolint:errcheck
			TableName: aws.String(name),
			Item: map[string]ddbtypes.AttributeValue{
				"pk": sv(fmt.Sprintf("u-%d", i)), "sk": sv("scan"),
			},
		})
	}
	resp, err := g.client().Scan(ctx, &ddb.ScanInput{TableName: aws.String(name)})
	if err != nil {
		return err
	}
	if resp.Count < 1 {
		return fmt.Errorf("Scan: expected ≥1 item")
	}
	return nil
}

// ── dynamodb-query ────────────────────────────────────────────────────────────

func (g *ddbGroup) setupQuery(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddbq", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	for i := 0; i < 5; i++ {
		g.client().PutItem(ctx, &ddb.PutItemInput{ //nolint:errcheck
			TableName: aws.String(name),
			Item: map[string]ddbtypes.AttributeValue{
				"pk": sv("partition1"), "sk": sv(fmt.Sprintf("item-%d", i)),
				"val": nv(fmt.Sprintf("%d", i)),
			},
		})
	}
	t.Set("ddb_query_table", name)
	t.Set("ddb_query_pk", "partition1")
	return nil
}

func (g *ddbGroup) query(ctx context.Context, t *harness.TestContext, opts ...func(*ddb.QueryInput)) (*ddb.QueryOutput, error) {
	name := t.GetString("ddb_query_table")
	pk := t.GetString("ddb_query_pk")
	input := &ddb.QueryInput{
		TableName:                 aws.String(name),
		KeyConditionExpression:    aws.String("pk = :pk"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":pk": sv(pk)},
	}
	for _, opt := range opts {
		opt(input)
	}
	return g.client().Query(ctx, input)
}

func (g *ddbGroup) Query(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.query(ctx, t)
	if err != nil {
		return err
	}
	if resp.Count < 5 {
		return fmt.Errorf("Query: expected ≥5 items, got %d", resp.Count)
	}
	return nil
}

func (g *ddbGroup) QueryWithFilterExpression(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_query_table")
	pk := t.GetString("ddb_query_pk")
	resp, err := g.client().Query(ctx, &ddb.QueryInput{
		TableName:                aws.String(name),
		KeyConditionExpression:   aws.String("pk = :pk"),
		FilterExpression:         aws.String("#v > :min"),
		ExpressionAttributeNames: map[string]string{"#v": "val"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": sv(pk), ":min": nv("2"),
		},
	})
	if err != nil {
		return err
	}
	if resp.Count < 1 {
		return fmt.Errorf("QueryWithFilterExpression: expected ≥1 item after filter")
	}
	return nil
}

func (g *ddbGroup) QueryPagination(ctx context.Context, t *harness.TestContext) error {
	resp1, err := g.query(ctx, t, func(q *ddb.QueryInput) { q.Limit = aws.Int32(1) })
	if err != nil {
		return err
	}
	if resp1.LastEvaluatedKey == nil {
		return fmt.Errorf("QueryPagination: expected LastEvaluatedKey with Limit=1")
	}
	resp2, err := g.query(ctx, t, func(q *ddb.QueryInput) {
		q.ExclusiveStartKey = resp1.LastEvaluatedKey
	})
	if err != nil {
		return err
	}
	if resp2.Count == 0 {
		return fmt.Errorf("QueryPagination: expected items on second page")
	}
	return nil
}

func (g *ddbGroup) ScanWithFilter(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_query_table")
	resp, err := g.client().Scan(ctx, &ddb.ScanInput{
		TableName:                 aws.String(name),
		FilterExpression:          aws.String("#v >= :min"),
		ExpressionAttributeNames:  map[string]string{"#v": "val"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":min": nv("3")},
	})
	if err != nil {
		return err
	}
	for _, item := range resp.Items {
		if v, ok := item["val"].(*ddbtypes.AttributeValueMemberN); ok {
			if v.Value < "3" {
				return fmt.Errorf("ScanWithFilter: item with val=%s violates filter", v.Value)
			}
		}
	}
	return nil
}

func (g *ddbGroup) QueryWithFilter(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_query_table")
	pk := t.GetString("ddb_query_pk")
	resp, err := g.client().Query(ctx, &ddb.QueryInput{
		TableName:                aws.String(name),
		KeyConditionExpression:   aws.String("pk = :pk"),
		FilterExpression:         aws.String("#v > :min"),
		ExpressionAttributeNames: map[string]string{"#v": "val"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": sv(pk), ":min": nv("2"),
		},
	})
	if err != nil {
		return err
	}
	if resp.Count < 1 {
		return fmt.Errorf("QueryWithFilter: expected ≥1 item after filter")
	}
	return nil
}

func (g *ddbGroup) QueryWithLimit(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.query(ctx, t, func(q *ddb.QueryInput) { q.Limit = aws.Int32(2) })
	if err != nil {
		return err
	}
	if resp.Count > 2 {
		return fmt.Errorf("QueryWithLimit: expected ≤2 items, got %d", resp.Count)
	}
	return nil
}

func (g *ddbGroup) QueryWithExclusiveStartKey(ctx context.Context, t *harness.TestContext) error {
	resp1, err := g.query(ctx, t, func(q *ddb.QueryInput) { q.Limit = aws.Int32(2) })
	if err != nil {
		return err
	}
	if resp1.LastEvaluatedKey == nil {
		return nil // fewer than 2 items, pagination not needed
	}
	_, err = g.query(ctx, t, func(q *ddb.QueryInput) {
		q.Limit = aws.Int32(2)
		q.ExclusiveStartKey = resp1.LastEvaluatedKey
	})
	return err
}

// ── dynamodb-batch ────────────────────────────────────────────────────────────

func (g *ddbGroup) setupBatch(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddbbatch", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	t.Set("ddb_batch_table", name)
	return nil
}

func (g *ddbGroup) BatchWriteItem(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_batch_table")
	var reqs []ddbtypes.WriteRequest
	for i := 0; i < 5; i++ {
		reqs = append(reqs, ddbtypes.WriteRequest{
			PutRequest: &ddbtypes.PutRequest{
				Item: map[string]ddbtypes.AttributeValue{
					"pk": sv(fmt.Sprintf("b-%d", i)), "sk": sv("batch"),
				},
			},
		})
	}
	resp, err := g.client().BatchWriteItem(ctx, &ddb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{name: reqs},
	})
	if err != nil {
		return err
	}
	if len(resp.UnprocessedItems) > 0 {
		return fmt.Errorf("BatchWriteItem: unprocessed items")
	}
	return nil
}

func (g *ddbGroup) BatchGetItem(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_batch_table")
	var keys []map[string]ddbtypes.AttributeValue
	for i := 0; i < 3; i++ {
		keys = append(keys, map[string]ddbtypes.AttributeValue{
			"pk": sv(fmt.Sprintf("b-%d", i)), "sk": sv("batch"),
		})
	}
	resp, err := g.client().BatchGetItem(ctx, &ddb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			name: {Keys: keys},
		},
	})
	if err != nil {
		return err
	}
	if len(resp.Responses[name]) < 3 {
		return fmt.Errorf("BatchGetItem: expected ≥3 items, got %d", len(resp.Responses[name]))
	}
	return nil
}

// ── dynamodb-txn ──────────────────────────────────────────────────────────────

func (g *ddbGroup) setupTxn(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddbtxn", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	t.Set("ddb_txn_table", name)
	return nil
}

func (g *ddbGroup) TransactWriteItems(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_txn_table")
	_, err := g.client().TransactWriteItems(ctx, &ddb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName: aws.String(name),
				Item: map[string]ddbtypes.AttributeValue{
					"pk": sv("txn-1"), "sk": sv("item"),
				},
			}},
			{Put: &ddbtypes.Put{
				TableName: aws.String(name),
				Item: map[string]ddbtypes.AttributeValue{
					"pk": sv("txn-2"), "sk": sv("item"),
				},
			}},
		},
	})
	if err != nil {
		return err
	}
	resp, err := g.client().GetItem(ctx, &ddb.GetItemInput{
		TableName: aws.String(name),
		Key:       map[string]ddbtypes.AttributeValue{"pk": sv("txn-1"), "sk": sv("item")},
	})
	if err != nil {
		return fmt.Errorf("TransactWriteItems: GetItem verify failed: %w", err)
	}
	if len(resp.Item) == 0 {
		return fmt.Errorf("TransactWriteItems: txn-1 item not found")
	}
	return nil
}

func (g *ddbGroup) TransactWriteConditionFail(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_txn_table")
	// txn-1 was already written by TransactWriteItems — condition should fail
	_, err := g.client().TransactWriteItems(ctx, &ddb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName:           aws.String(name),
				Item:                map[string]ddbtypes.AttributeValue{"pk": sv("txn-1"), "sk": sv("item")},
				ConditionExpression: aws.String("attribute_not_exists(pk)"),
			}},
		},
	})
	if err == nil {
		return fmt.Errorf("TransactWriteConditionFail: expected TransactionCanceledException")
	}
	var tce *ddbtypes.TransactionCanceledException
	if !errors.As(err, &tce) {
		return fmt.Errorf("TransactWriteConditionFail: expected TransactionCanceledException, got %T: %v", err, err)
	}
	return nil
}

func (g *ddbGroup) TransactGetItems(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_txn_table")
	resp, err := g.client().TransactGetItems(ctx, &ddb.TransactGetItemsInput{
		TransactItems: []ddbtypes.TransactGetItem{
			{Get: &ddbtypes.Get{
				TableName: aws.String(name),
				Key:       map[string]ddbtypes.AttributeValue{"pk": sv("txn-1"), "sk": sv("item")},
			}},
		},
	})
	if err != nil {
		return err
	}
	if len(resp.Responses) == 0 {
		return fmt.Errorf("TransactGetItems: no responses")
	}
	return nil
}

// ── dynamodb-ttl ──────────────────────────────────────────────────────────────

func (g *ddbGroup) setupTTL(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("%s-ddbttl", t.RunID)
	if err := g.createTable(ctx, name); err != nil {
		return err
	}
	t.Set("ddb_ttl_table", name)
	return nil
}

func (g *ddbGroup) UpdateTimeToLive(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_ttl_table")
	_, err := g.client().UpdateTimeToLive(ctx, &ddb.UpdateTimeToLiveInput{
		TableName: aws.String(name),
		TimeToLiveSpecification: &ddbtypes.TimeToLiveSpecification{
			AttributeName: aws.String("ttl"),
			Enabled:       aws.Bool(true),
		},
	})
	if err != nil {
		return err
	}
	resp, err := g.client().DescribeTimeToLive(ctx, &ddb.DescribeTimeToLiveInput{
		TableName: aws.String(name),
	})
	if err != nil {
		return fmt.Errorf("UpdateTimeToLive: DescribeTimeToLive verify failed: %w", err)
	}
	if resp.TimeToLiveDescription == nil || resp.TimeToLiveDescription.TimeToLiveStatus != ddbtypes.TimeToLiveStatusEnabled {
		return fmt.Errorf("UpdateTimeToLive: expected status ENABLED")
	}
	return nil
}

func (g *ddbGroup) DescribeTimeToLive(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ddb_ttl_table")
	resp, err := g.client().DescribeTimeToLive(ctx, &ddb.DescribeTimeToLiveInput{
		TableName: aws.String(name),
	})
	if err != nil {
		return err
	}
	if resp.TimeToLiveDescription == nil {
		return fmt.Errorf("DescribeTimeToLive: nil description")
	}
	if resp.TimeToLiveDescription.TimeToLiveStatus != ddbtypes.TimeToLiveStatusEnabled {
		return fmt.Errorf("DescribeTimeToLive: expected ENABLED, got %q", resp.TimeToLiveDescription.TimeToLiveStatus)
	}
	return nil
}
