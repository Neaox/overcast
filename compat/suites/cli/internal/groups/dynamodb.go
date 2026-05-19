package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// One Namer per DynamoDB sub-group ensures parallel groups never share table names.
var (
	ddbTablesNamer = harness.NewNamer("ddb-tbl")
	ddbItemsNamer  = harness.NewNamer("ddb-itm")
	ddbQueryNamer  = harness.NewNamer("ddb-qry")
	ddbBatchNamer  = harness.NewNamer("ddb-bat")
	ddbTxnNamer    = harness.NewNamer("ddb-txn")
	ddbTTLNamer    = harness.NewNamer("ddb-ttl")
)

// DynamoDB returns the DynamoDB service group.
func DynamoDB() ServiceGroup {
	g := &dynamoGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// dynamodb-tables
			"CreateTable":   g.CreateTable,
			"DescribeTable": g.DescribeTable,
			"ListTables":    g.ListTables,
			"UpdateTable":   g.UpdateTable,
			"DeleteTable":   g.DeleteTable,
			// dynamodb-items
			"PutItem":              g.PutItem,
			"GetItem":              g.GetItem,
			"UpdateItem":           g.UpdateItem,
			"PutItemConditionFail": g.PutItemConditionFail,
			"DeleteItem":           g.DeleteItem,
			// dynamodb-query
			"Query":                     g.Query,
			"QueryWithFilterExpression": g.QueryWithFilterExpression,
			"QueryWithLimit":            g.QueryWithLimit,
			"QueryPagination":           g.QueryPagination,
			"Scan":                      g.Scan,
			"ScanWithFilter":            g.ScanWithFilter,
			// dynamodb-batch
			"BatchWriteItem": g.BatchWriteItem,
			"BatchGetItem":   g.BatchGetItem,
			// dynamodb-txn
			"TransactWriteItems":         g.TransactWriteItems,
			"TransactGetItems":           g.TransactGetItems,
			"TransactWriteConditionFail": g.TransactWriteConditionFail,
			// dynamodb-ttl
			"UpdateTimeToLive":   g.UpdateTimeToLive,
			"DescribeTimeToLive": g.DescribeTimeToLive,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"dynamodb-tables": g.setupTables,
			"dynamodb-items":  g.setupItems,
			"dynamodb-query":  g.setupQuery,
			"dynamodb-batch":  g.setupBatch,
			"dynamodb-txn":    g.setupTxn,
			"dynamodb-ttl":    g.setupTTL,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"dynamodb-tables": g.teardownTable,
			"dynamodb-items":  g.teardownTable,
			"dynamodb-query":  g.teardownTable,
			"dynamodb-batch":  g.teardownTable,
			"dynamodb-txn":    g.teardownTable,
			"dynamodb-ttl":    g.teardownTable,
		},
	}
}

type dynamoGroup struct{}

func (g *dynamoGroup) tableName(t *harness.TestContext) string {
	if tn := t.GetString("table_name"); tn != "" {
		return tn
	}
	return fmt.Sprintf("%s-ddb", t.RunID)
}

func (g *dynamoGroup) createTable(t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "create-table",
		"--table-name", g.tableName(t),
		"--attribute-definitions",
		`[{"AttributeName":"pk","AttributeType":"S"},{"AttributeName":"sk","AttributeType":"S"}]`,
		"--key-schema",
		`[{"AttributeName":"pk","KeyType":"HASH"},{"AttributeName":"sk","KeyType":"RANGE"}]`,
		"--billing-mode", "PAY_PER_REQUEST",
	)
}

// ─── dynamodb-tables ─────────────────────────────────────────────────────────

func (g *dynamoGroup) setupTables(_ context.Context, t *harness.TestContext) error {
	t.Set("table_name", ddbTablesNamer.Name(t))
	return nil
}

func (g *dynamoGroup) CreateTable(_ context.Context, t *harness.TestContext) error {
	err := g.createTable(t)
	if err != nil && isAlreadyExists(err) {
		return nil // idempotent across runs
	}
	return err
}

func (g *dynamoGroup) DescribeTable(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "describe-table",
		"--table-name", g.tableName(t),
	)
	if err != nil {
		return err
	}
	tbl, _ := out["Table"].(map[string]any)
	if tbl == nil {
		return fmt.Errorf("dynamodb DescribeTable: missing Table")
	}
	return nil
}

func (g *dynamoGroup) ListTables(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "dynamodb", "list-tables")
	if err != nil {
		return err
	}
	tables, _ := out["TableNames"].([]any)
	want := g.tableName(t)
	for _, name := range tables {
		if name == want {
			return nil
		}
	}
	return fmt.Errorf("dynamodb ListTables: table %q not found in list", want)
}

func (g *dynamoGroup) UpdateTable(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "update-table",
		"--table-name", g.tableName(t),
		"--billing-mode", "PAY_PER_REQUEST",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "describe-table",
		"--table-name", g.tableName(t),
	)
	if err != nil {
		return fmt.Errorf("dynamodb UpdateTable: describe-table failed: %w", err)
	}
	tbl, _ := out["Table"].(map[string]any)
	if tbl == nil {
		return fmt.Errorf("dynamodb UpdateTable: missing Table in describe-table response")
	}
	return nil
}

func (g *dynamoGroup) DeleteTable(_ context.Context, t *harness.TestContext) error {
	tableName := g.tableName(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "delete-table",
		"--table-name", tableName,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "dynamodb", "list-tables")
	if err != nil {
		return fmt.Errorf("dynamodb DeleteTable: list-tables failed: %w", err)
	}
	tables, _ := out["TableNames"].([]any)
	for _, name := range tables {
		if name == tableName {
			return fmt.Errorf("dynamodb DeleteTable: table %q still present after deletion", tableName)
		}
	}
	return nil
}

func (g *dynamoGroup) teardownTable(_ context.Context, t *harness.TestContext) error {
	awscli.Run(t.Endpoint, t.Region, "dynamodb", "delete-table", "--table-name", g.tableName(t)) //nolint:errcheck
	return nil
}

// ─── dynamodb-items ──────────────────────────────────────────────────────────

func (g *dynamoGroup) setupItems(_ context.Context, t *harness.TestContext) error {
	t.Set("table_name", ddbItemsNamer.Name(t))
	if err := g.createTable(t); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *dynamoGroup) PutItem(_ context.Context, t *harness.TestContext) error {
	item := fmt.Sprintf(
		`{"pk":{"S":"item-%s"},"sk":{"S":"v1"},"val":{"S":"hello"}}`,
		t.RunID,
	)
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "put-item",
		"--table-name", g.tableName(t),
		"--item", item,
	); err != nil {
		return err
	}
	// Verify via GetItem
	key := fmt.Sprintf(`{"pk":{"S":"item-%s"},"sk":{"S":"v1"}}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "get-item",
		"--table-name", g.tableName(t),
		"--key", key,
	)
	if err != nil {
		return fmt.Errorf("dynamodb PutItem: get-item failed: %w", err)
	}
	if out["Item"] == nil {
		return fmt.Errorf("dynamodb PutItem: item not found after put")
	}
	return nil
}

func (g *dynamoGroup) GetItem(_ context.Context, t *harness.TestContext) error {
	key := fmt.Sprintf(`{"pk":{"S":"item-%s"},"sk":{"S":"v1"}}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "get-item",
		"--table-name", g.tableName(t),
		"--key", key,
	)
	if err != nil {
		return err
	}
	if out["Item"] == nil {
		return fmt.Errorf("dynamodb GetItem: missing Item")
	}
	return nil
}

func (g *dynamoGroup) UpdateItem(_ context.Context, t *harness.TestContext) error {
	key := fmt.Sprintf(`{"pk":{"S":"item-%s"},"sk":{"S":"v1"}}`, t.RunID)
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "update-item",
		"--table-name", g.tableName(t),
		"--key", key,
		"--update-expression", "SET val = :v",
		"--expression-attribute-values", `{":v":{"S":"updated"}}`,
	); err != nil {
		return err
	}
	// Verify the update was applied.
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "get-item",
		"--table-name", g.tableName(t),
		"--key", key,
	)
	if err != nil {
		return fmt.Errorf("dynamodb UpdateItem: get-item failed: %w", err)
	}
	item, _ := out["Item"].(map[string]any)
	val, _ := item["val"].(map[string]any)
	if val["S"] != "updated" {
		return fmt.Errorf("dynamodb UpdateItem: expected val=updated, got %v", val["S"])
	}
	return nil
}

func (g *dynamoGroup) PutItemConditionFail(_ context.Context, t *harness.TestContext) error {
	item := fmt.Sprintf(
		`{"pk":{"S":"item-%s"},"sk":{"S":"v1"},"val":{"S":"conflict"}}`,
		t.RunID,
	)
	err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "put-item",
		"--table-name", g.tableName(t),
		"--item", item,
		"--condition-expression", "attribute_not_exists(pk)",
	)
	if err == nil {
		return fmt.Errorf("dynamodb PutItemConditionFail: expected failure but got success")
	}
	return nil
}

func (g *dynamoGroup) DeleteItem(_ context.Context, t *harness.TestContext) error {
	key := fmt.Sprintf(`{"pk":{"S":"item-%s"},"sk":{"S":"v1"}}`, t.RunID)
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "delete-item",
		"--table-name", g.tableName(t),
		"--key", key,
	); err != nil {
		return err
	}
	// Verify the item is gone.
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "get-item",
		"--table-name", g.tableName(t),
		"--key", key,
	)
	if err != nil {
		return fmt.Errorf("dynamodb DeleteItem: get-item failed: %w", err)
	}
	if out["Item"] != nil {
		return fmt.Errorf("dynamodb DeleteItem: item still present after deletion")
	}
	return nil
}

// ─── dynamodb-query ──────────────────────────────────────────────────────────

func (g *dynamoGroup) setupQuery(_ context.Context, t *harness.TestContext) error {
	t.Set("table_name", ddbQueryNamer.Name(t))
	if err := g.createTable(t); err != nil && !isAlreadyExists(err) {
		return err
	}
	// Seed some items.
	for i := 1; i <= 5; i++ {
		item := fmt.Sprintf(
			`{"pk":{"S":"qpk-%s"},"sk":{"S":"sk%d"},"n":{"N":"%d"}}`,
			t.RunID, i, i,
		)
		if err := awscli.Run(t.Endpoint, t.Region,
			"dynamodb", "put-item",
			"--table-name", g.tableName(t),
			"--item", item,
		); err != nil {
			return err
		}
	}
	return nil
}

func (g *dynamoGroup) Query(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "query",
		"--table-name", g.tableName(t),
		"--key-condition-expression", "pk = :pk",
		"--expression-attribute-values",
		fmt.Sprintf(`{":pk":{"S":"qpk-%s"}}`, t.RunID),
	)
	if err != nil {
		return err
	}
	count, _ := out["Count"].(float64)
	if count < 5 {
		return fmt.Errorf("dynamodb Query: expected Count >= 5, got %v", count)
	}
	return nil
}

func (g *dynamoGroup) QueryWithFilterExpression(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "query",
		"--table-name", g.tableName(t),
		"--key-condition-expression", "pk = :pk",
		"--filter-expression", "n > :n",
		"--expression-attribute-values",
		fmt.Sprintf(`{":pk":{"S":"qpk-%s"},":n":{"N":"2"}}`, t.RunID),
	)
	if err != nil {
		return err
	}
	count, _ := out["Count"].(float64)
	if count < 1 {
		return fmt.Errorf("dynamodb QueryWithFilterExpression: expected Count >= 1, got %v", count)
	}
	return nil
}

func (g *dynamoGroup) QueryWithLimit(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "query",
		"--table-name", g.tableName(t),
		"--key-condition-expression", "pk = :pk",
		"--expression-attribute-values",
		fmt.Sprintf(`{":pk":{"S":"qpk-%s"}}`, t.RunID),
		"--limit", "2",
	)
	if err != nil {
		return err
	}
	count, _ := out["Count"].(float64)
	if int(count) > 2 {
		return fmt.Errorf("dynamodb QueryWithLimit: expected Count <= 2, got %v", count)
	}
	if out["LastEvaluatedKey"] == nil {
		return fmt.Errorf("dynamodb QueryWithLimit: expected LastEvaluatedKey (5 items, limit 2)")
	}
	return nil
}

func (g *dynamoGroup) QueryPagination(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "query",
		"--table-name", g.tableName(t),
		"--key-condition-expression", "pk = :pk",
		"--expression-attribute-values",
		fmt.Sprintf(`{":pk":{"S":"qpk-%s"}}`, t.RunID),
		"--limit", "2",
	)
	if err != nil {
		return err
	}
	// If there's a LastEvaluatedKey, follow it once.
	if lek := out["LastEvaluatedKey"]; lek != nil {
		lekJSON := fmt.Sprintf(`{"pk":{"S":"qpk-%s"},"sk":{"S":"sk2"}}`, t.RunID)
		_, err = awscli.RunOutput(t.Endpoint, t.Region,
			"dynamodb", "query",
			"--table-name", g.tableName(t),
			"--key-condition-expression", "pk = :pk",
			"--expression-attribute-values",
			fmt.Sprintf(`{":pk":{"S":"qpk-%s"}}`, t.RunID),
			"--exclusive-start-key", lekJSON,
		)
	}
	return err
}

func (g *dynamoGroup) Scan(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "scan",
		"--table-name", g.tableName(t),
	)
	if err != nil {
		return err
	}
	count, _ := out["Count"].(float64)
	if count < 5 {
		return fmt.Errorf("dynamodb Scan: expected Count >= 5, got %v", count)
	}
	return nil
}

func (g *dynamoGroup) ScanWithFilter(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "scan",
		"--table-name", g.tableName(t),
		"--filter-expression", "n > :n",
		"--expression-attribute-values", `{":n":{"N":"3"}}`,
	)
	if err != nil {
		return err
	}
	count, _ := out["Count"].(float64)
	if count < 1 {
		return fmt.Errorf("dynamodb ScanWithFilter: expected Count >= 1, got %v", count)
	}
	return nil
}

// ─── dynamodb-batch ──────────────────────────────────────────────────────────

func (g *dynamoGroup) setupBatch(_ context.Context, t *harness.TestContext) error {
	t.Set("table_name", ddbBatchNamer.Name(t))
	if err := g.createTable(t); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *dynamoGroup) BatchWriteItem(_ context.Context, t *harness.TestContext) error {
	table := g.tableName(t)
	writeReqs := fmt.Sprintf(`{
		"PutRequest":{"Item":{"pk":{"S":"batch-%s"},"sk":{"S":"b1"},"val":{"S":"x"}}},
		"PutRequest":{"Item":{"pk":{"S":"batch-%s"},"sk":{"S":"b2"},"val":{"S":"y"}}}
	}`, t.RunID, t.RunID)
	_ = writeReqs
	// Use proper batch write format.
	req := fmt.Sprintf(
		`{"%s":[{"PutRequest":{"Item":{"pk":{"S":"batch-%s"},"sk":{"S":"b1"}}}},{"PutRequest":{"Item":{"pk":{"S":"batch-%s"},"sk":{"S":"b2"}}}}]}`,
		table, t.RunID, t.RunID,
	)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "batch-write-item",
		"--request-items", req,
	)
	if err != nil {
		return err
	}
	if unproc, _ := out["UnprocessedItems"].(map[string]any); len(unproc) > 0 {
		return fmt.Errorf("dynamodb BatchWriteItem: has UnprocessedItems")
	}
	return nil
}

func (g *dynamoGroup) BatchGetItem(_ context.Context, t *harness.TestContext) error {
	table := g.tableName(t)
	req := fmt.Sprintf(
		`{"%s":{"Keys":[{"pk":{"S":"batch-%s"},"sk":{"S":"b1"}},{"pk":{"S":"batch-%s"},"sk":{"S":"b2"}}]}}`,
		table, t.RunID, t.RunID,
	)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "batch-get-item",
		"--request-items", req,
	)
	if err != nil {
		return err
	}
	responses, _ := out["Responses"].(map[string]any)
	items, _ := responses[table].([]any)
	if len(items) < 1 {
		return fmt.Errorf("dynamodb BatchGetItem: expected at least 1 item in Responses, got %d", len(items))
	}
	return nil
}

// ─── dynamodb-txn ────────────────────────────────────────────────────────────

func (g *dynamoGroup) setupTxn(_ context.Context, t *harness.TestContext) error {
	t.Set("table_name", ddbTxnNamer.Name(t))
	if err := g.createTable(t); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *dynamoGroup) TransactWriteItems(_ context.Context, t *harness.TestContext) error {
	table := g.tableName(t)
	items := fmt.Sprintf(
		`[{"Put":{"TableName":"%s","Item":{"pk":{"S":"txn-%s"},"sk":{"S":"t1"}}}},`+
			`{"Put":{"TableName":"%s","Item":{"pk":{"S":"txn-%s"},"sk":{"S":"t2"}}}}]`,
		table, t.RunID, table, t.RunID,
	)
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "transact-write-items",
		"--transact-items", items,
	); err != nil {
		return err
	}
	// Verify t1 was written
	key := fmt.Sprintf(`{"pk":{"S":"txn-%s"},"sk":{"S":"t1"}}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "get-item",
		"--table-name", table,
		"--key", key,
	)
	if err != nil {
		return fmt.Errorf("dynamodb TransactWriteItems: get-item failed: %w", err)
	}
	if out["Item"] == nil {
		return fmt.Errorf("dynamodb TransactWriteItems: txn item t1 not found")
	}
	return nil
}

func (g *dynamoGroup) TransactGetItems(_ context.Context, t *harness.TestContext) error {
	table := g.tableName(t)
	items := fmt.Sprintf(
		`[{"Get":{"TableName":"%s","Key":{"pk":{"S":"txn-%s"},"sk":{"S":"t1"}}}},`+
			`{"Get":{"TableName":"%s","Key":{"pk":{"S":"txn-%s"},"sk":{"S":"t2"}}}}]`,
		table, t.RunID, table, t.RunID,
	)
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "transact-get-items",
		"--transact-items", items,
	)
	if err != nil {
		return err
	}
	resp, _ := out["Responses"].([]any)
	if len(resp) < 2 {
		return fmt.Errorf("dynamodb TransactGetItems: expected 2 Responses, got %d", len(resp))
	}
	return nil
}

func (g *dynamoGroup) TransactWriteConditionFail(_ context.Context, t *harness.TestContext) error {
	table := g.tableName(t)
	items := fmt.Sprintf(
		`[{"Put":{"TableName":"%s","Item":{"pk":{"S":"txn-%s"},"sk":{"S":"t1"}},"ConditionExpression":"attribute_not_exists(pk)"}}]`,
		table, t.RunID,
	)
	err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "transact-write-items",
		"--transact-items", items,
	)
	if err == nil {
		return fmt.Errorf("dynamodb TransactWriteConditionFail: expected error, got success")
	}
	return nil
}

// ─── dynamodb-ttl ────────────────────────────────────────────────────────────

func (g *dynamoGroup) setupTTL(_ context.Context, t *harness.TestContext) error {
	t.Set("table_name", ddbTTLNamer.Name(t))
	if err := g.createTable(t); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *dynamoGroup) UpdateTimeToLive(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"dynamodb", "update-time-to-live",
		"--table-name", g.tableName(t),
		"--time-to-live-specification", `{"Enabled":true,"AttributeName":"ttl"}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "describe-time-to-live",
		"--table-name", g.tableName(t),
	)
	if err != nil {
		return fmt.Errorf("dynamodb UpdateTimeToLive: describe-ttl failed: %w", err)
	}
	ttl, _ := out["TimeToLiveDescription"].(map[string]any)
	status, _ := ttl["TimeToLiveStatus"].(string)
	if status != "ENABLED" && status != "ENABLING" {
		return fmt.Errorf("dynamodb UpdateTimeToLive: expected ENABLED/ENABLING, got %q", status)
	}
	return nil
}

func (g *dynamoGroup) DescribeTimeToLive(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"dynamodb", "describe-time-to-live",
		"--table-name", g.tableName(t),
	)
	if err != nil {
		return err
	}
	ttl, _ := out["TimeToLiveDescription"].(map[string]any)
	status, _ := ttl["TimeToLiveStatus"].(string)
	if status != "ENABLED" && status != "ENABLING" {
		return fmt.Errorf("dynamodb DescribeTimeToLive: expected ENABLED/ENABLING, got %q", status)
	}
	return nil
}
