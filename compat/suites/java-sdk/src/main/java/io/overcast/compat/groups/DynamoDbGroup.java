package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.dynamodb.DynamoDbClient;
import software.amazon.awssdk.services.dynamodb.model.*;

import java.util.List;
import java.util.Map;

/**
 * DynamoDB compatibility test group.
 *
 * <p>Groups: dynamodb-tables, dynamodb-items, dynamodb-query, dynamodb-batch,
 * dynamodb-txn, dynamodb-ttl.
 */
public final class DynamoDbGroup implements ServiceGroup {

    private final AwsClients clients;

    public DynamoDbGroup(AwsClients clients) {
        this.clients = clients;
    }

    private DynamoDbClient ddb() { return clients.dynamoDb(); }

    // ── AttributeValue helpers ────────────────────────────────────────────────

    private static AttributeValue s(String v) { return AttributeValue.fromS(v); }
    private static AttributeValue n(String v) { return AttributeValue.fromN(v); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateTable",               this::createTable),
                Map.entry("DescribeTable",             this::describeTable),
                Map.entry("ListTables",                this::listTables),
                Map.entry("UpdateTable",               this::updateTable),
                Map.entry("DeleteTable",               this::deleteTable),
                Map.entry("PutItem",                   this::putItem),
                Map.entry("GetItem",                   this::getItem),
                Map.entry("UpdateItem",                this::updateItem),
                Map.entry("PutItemConditionFail",      this::putItemConditionFail),
                Map.entry("DeleteItem",                this::deleteItem),
                Map.entry("Query",                     this::query),
                Map.entry("QueryWithFilterExpression", this::queryWithFilterExpression),
                Map.entry("QueryWithLimit",            this::queryWithLimit),
                Map.entry("QueryPagination",           this::queryPagination),
                Map.entry("Scan",                      this::scan),
                Map.entry("ScanWithFilter",            this::scanWithFilter),
                Map.entry("BatchWriteItem",            this::batchWriteItem),
                Map.entry("BatchGetItem",              this::batchGetItem),
                Map.entry("TransactWriteItems",        this::transactWriteItems),
                Map.entry("TransactGetItems",          this::transactGetItems),
                Map.entry("TransactWriteConditionFail",this::transactWriteConditionFail),
                Map.entry("UpdateTimeToLive",          this::updateTimeToLive),
                Map.entry("DescribeTimeToLive",        this::describeTimeToLive)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("dynamodb-tables", this::setupTables),
                Map.entry("dynamodb-items",  this::setupItems),
                Map.entry("dynamodb-query",  this::setupQuery),
                Map.entry("dynamodb-batch",  this::setupBatch),
                Map.entry("dynamodb-txn",    this::setupTxn),
                Map.entry("dynamodb-ttl",    this::setupTtl)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("dynamodb-tables", ctx -> deleteTableSilently(ctx.getString("ddbTable"))),
                Map.entry("dynamodb-items",  ctx -> deleteTableSilently(ctx.getString("ddbItemTable"))),
                Map.entry("dynamodb-query",  ctx -> deleteTableSilently(ctx.getString("ddbQueryTable"))),
                Map.entry("dynamodb-batch",  ctx -> deleteTableSilently(ctx.getString("ddbBatchTable"))),
                Map.entry("dynamodb-txn",    ctx -> deleteTableSilently(ctx.getString("ddbTxnTable"))),
                Map.entry("dynamodb-ttl",    ctx -> deleteTableSilently(ctx.getString("ddbTtlTable")))
        );
    }

    // ── dynamodb-tables ───────────────────────────────────────────────────────

    private void setupTables(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-ddbtbl";
        createTestTable(name);
        ctx.set("ddbTable", name);
    }

    private void createTable(TestContext ctx) {
        Assertions.assertNotBlank(ctx.getString("ddbTable"), "ddbTable");
    }

    private void describeTable(TestContext ctx) throws Exception {
        String name = ctx.getString("ddbTable");
        var resp = ddb().describeTable(r -> r.tableName(name));
        Assertions.assertEquals(name, resp.table().tableName(), "DescribeTable: tableName mismatch");
        Assertions.assertEquals(TableStatus.ACTIVE, resp.table().tableStatus(),
                "DescribeTable: status should be ACTIVE");
    }

    private void listTables(TestContext ctx) throws Exception {
        String name = ctx.getString("ddbTable");
        var resp = ddb().listTables();
        Assertions.assertTrue(resp.tableNames().contains(name),
                "ListTables: table " + name + " not found");
    }

    private void updateTable(TestContext ctx) throws Exception {
        // UpdateTable: switch billing mode to PROVISIONED then back to PAY_PER_REQUEST.
        String name = ctx.getString("ddbTable");
        ddb().updateTable(r -> r.tableName(name)
                .billingMode(BillingMode.PAY_PER_REQUEST));
    }

    private void deleteTable(TestContext ctx) throws Exception {
        // Create an ephemeral table and delete it; setup table is cleaned by teardown.
        String name = ctx.runId() + "-ddbdel";
        createTestTable(name);
        ddb().deleteTable(r -> r.tableName(name));
        var resp = ddb().listTables();
        Assertions.assertFalse(resp.tableNames().contains(name),
                "DeleteTable: table still present after deletion");
    }

    // ── dynamodb-items ────────────────────────────────────────────────────────

    private void setupItems(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-ddbitm";
        createTestTable(name);
        ctx.set("ddbItemTable", name);
    }

    private void putItem(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbItemTable");
        ddb().putItem(r -> r.tableName(table).item(Map.of(
                "pk",   s("user#1"),
                "sk",   s("profile"),
                "name", s("Alice"),
                "age",  n("30"))));
    }

    private void getItem(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbItemTable");
        var resp = ddb().getItem(r -> r.tableName(table)
                .key(Map.of("pk", s("user#1"), "sk", s("profile"))));
        Assertions.assertTrue(resp.hasItem(), "GetItem: item not found");
        Assertions.assertEquals("Alice", resp.item().get("name").s(), "GetItem: name mismatch");
    }

    private void updateItem(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbItemTable");
        ddb().updateItem(r -> r.tableName(table)
                .key(Map.of("pk", s("user#1"), "sk", s("profile")))
                .updateExpression("SET #n = :v")
                .expressionAttributeNames(Map.of("#n", "name"))
                .expressionAttributeValues(Map.of(":v", s("Bob"))));
        var resp = ddb().getItem(r -> r.tableName(table)
                .key(Map.of("pk", s("user#1"), "sk", s("profile"))));
        Assertions.assertEquals("Bob", resp.item().get("name").s(), "UpdateItem: name not updated");
    }

    private void putItemConditionFail(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbItemTable");
        try {
            ddb().putItem(r -> r.tableName(table)
                    .item(Map.of("pk", s("user#1"), "sk", s("profile"), "name", s("Charlie")))
                    .conditionExpression("attribute_not_exists(pk)"));
            throw new AssertionError("PutItemConditionFail: expected ConditionalCheckFailedException");
        } catch (ConditionalCheckFailedException e) {
            // expected
        }
    }

    private void deleteItem(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbItemTable");
        ddb().deleteItem(r -> r.tableName(table)
                .key(Map.of("pk", s("user#1"), "sk", s("profile"))));
        var resp = ddb().getItem(r -> r.tableName(table)
                .key(Map.of("pk", s("user#1"), "sk", s("profile"))));
        Assertions.assertFalse(resp.hasItem(), "DeleteItem: item still present after deletion");
    }

    // ── dynamodb-query ────────────────────────────────────────────────────────

    private void setupQuery(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-ddbqry";
        createTestTable(name);
        // Seed data: 5 items for user#1
        for (int i = 1; i <= 5; i++) {
            int idx = i;
            ddb().putItem(r -> r.tableName(name).item(Map.of(
                    "pk",    s("user#1"),
                    "sk",    s("item#" + String.format("%03d", idx)),
                    "score", n(String.valueOf(idx * 10)),
                    "tag",   s(idx % 2 == 0 ? "even" : "odd"))));
        }
        ctx.set("ddbQueryTable", name);
    }

    private void query(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbQueryTable");
        var resp = ddb().query(r -> r.tableName(table)
                .keyConditionExpression("pk = :pk")
                .expressionAttributeValues(Map.of(":pk", s("user#1"))));
        Assertions.assertEquals(5, resp.count(), "Query: expected 5 items");
    }

    private void queryWithFilterExpression(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbQueryTable");
        var resp = ddb().query(r -> r.tableName(table)
                .keyConditionExpression("pk = :pk")
                .filterExpression("tag = :tag")
                .expressionAttributeValues(Map.of(":pk", s("user#1"), ":tag", s("even"))));
        Assertions.assertEquals(2, resp.count(), "QueryWithFilterExpression: expected 2 even items");
    }

    private void queryWithLimit(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbQueryTable");
        var resp = ddb().query(r -> r.tableName(table)
                .keyConditionExpression("pk = :pk")
                .expressionAttributeValues(Map.of(":pk", s("user#1")))
                .limit(2));
        Assertions.assertGreaterThanOrEqual(1, resp.items().size(), "QueryWithLimit: expected at least 1 item");
    }

    private void queryPagination(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbQueryTable");
        var page1 = ddb().query(r -> r.tableName(table)
                .keyConditionExpression("pk = :pk")
                .expressionAttributeValues(Map.of(":pk", s("user#1")))
                .limit(2));
        Assertions.assertNotNull(page1.lastEvaluatedKey(), "QueryPagination: expected lastEvaluatedKey");
        var page2 = ddb().query(r -> r.tableName(table)
                .keyConditionExpression("pk = :pk")
                .expressionAttributeValues(Map.of(":pk", s("user#1")))
                .exclusiveStartKey(page1.lastEvaluatedKey()));
        Assertions.assertGreaterThanOrEqual(1, page2.items().size(), "QueryPagination: page 2 should have items");
    }

    private void scan(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbQueryTable");
        var resp = ddb().scan(r -> r.tableName(table));
        Assertions.assertGreaterThanOrEqual(5, resp.count(), "Scan: expected >= 5 items");
    }

    private void scanWithFilter(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbQueryTable");
        var resp = ddb().scan(r -> r.tableName(table)
                .filterExpression("tag = :tag")
                .expressionAttributeValues(Map.of(":tag", s("even"))));
        Assertions.assertEquals(2, resp.count(), "ScanWithFilter: expected 2 even items");
    }

    // ── dynamodb-batch ────────────────────────────────────────────────────────

    private void setupBatch(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-ddbbtch";
        createTestTable(name);
        ctx.set("ddbBatchTable", name);
    }

    private void batchWriteItem(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbBatchTable");
        ddb().batchWriteItem(r -> r.requestItems(Map.of(table, List.of(
                WriteRequest.builder().putRequest(p -> p.item(Map.of(
                        "pk", s("batch#1"), "sk", s("a"), "v", s("alpha")))).build(),
                WriteRequest.builder().putRequest(p -> p.item(Map.of(
                        "pk", s("batch#1"), "sk", s("b"), "v", s("beta")))).build()
        ))));
    }

    private void batchGetItem(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbBatchTable");
        var resp = ddb().batchGetItem(r -> r.requestItems(Map.of(
                table, KeysAndAttributes.builder().keys(List.of(
                        Map.of("pk", s("batch#1"), "sk", s("a")),
                        Map.of("pk", s("batch#1"), "sk", s("b"))
                )).build()
        )));
        var items = resp.responses().get(table);
        Assertions.assertNotNull(items, "BatchGetItem: no items returned");
        Assertions.assertEquals(2, items.size(), "BatchGetItem: expected 2 items");
    }

    // ── dynamodb-txn ──────────────────────────────────────────────────────────

    private void setupTxn(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-ddbtxn";
        createTestTable(name);
        ctx.set("ddbTxnTable", name);
    }

    private void transactWriteItems(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbTxnTable");
        ddb().transactWriteItems(r -> r.transactItems(
                TransactWriteItem.builder().put(p -> p.tableName(table).item(Map.of(
                        "pk", s("txn#1"), "sk", s("a"), "val", s("x")))).build(),
                TransactWriteItem.builder().put(p -> p.tableName(table).item(Map.of(
                        "pk", s("txn#1"), "sk", s("b"), "val", s("y")))).build()
        ));
    }

    private void transactGetItems(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbTxnTable");
        var resp = ddb().transactGetItems(r -> r.transactItems(
                TransactGetItem.builder().get(g -> g.tableName(table)
                        .key(Map.of("pk", s("txn#1"), "sk", s("a")))).build(),
                TransactGetItem.builder().get(g -> g.tableName(table)
                        .key(Map.of("pk", s("txn#1"), "sk", s("b")))).build()
        ));
        Assertions.assertEquals(2, resp.responses().size(), "TransactGetItems: expected 2 responses");
    }

    private void transactWriteConditionFail(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbTxnTable");
        try {
            ddb().transactWriteItems(r -> r.transactItems(
                    TransactWriteItem.builder().put(p -> p.tableName(table)
                            .item(Map.of("pk", s("txn#1"), "sk", s("a"), "val", s("z")))
                            .conditionExpression("attribute_not_exists(pk)")).build()
            ));
            throw new AssertionError("TransactWriteConditionFail: expected TransactionCanceledException");
        } catch (TransactionCanceledException e) {
            // expected
        }
    }

    // ── dynamodb-ttl ──────────────────────────────────────────────────────────

    private void setupTtl(TestContext ctx) throws Exception {
        String name = ctx.runId() + "-ddbttl";
        createTestTable(name);
        ctx.set("ddbTtlTable", name);
    }

    private void updateTimeToLive(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbTtlTable");
        ddb().updateTimeToLive(r -> r.tableName(table)
                .timeToLiveSpecification(t -> t.attributeName("ttl").enabled(true)));
    }

    private void describeTimeToLive(TestContext ctx) throws Exception {
        String table = ctx.getString("ddbTtlTable");
        var resp = ddb().describeTimeToLive(r -> r.tableName(table));
        Assertions.assertNotNull(resp.timeToLiveDescription(), "DescribeTimeToLive: description is null");
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    /** Creates a simple table with pk (S, HASH) + sk (S, RANGE), PAY_PER_REQUEST. */
    private void createTestTable(String name) {
        ddb().createTable(r -> r
                .tableName(name)
                .attributeDefinitions(
                        AttributeDefinition.builder().attributeName("pk").attributeType(ScalarAttributeType.S).build(),
                        AttributeDefinition.builder().attributeName("sk").attributeType(ScalarAttributeType.S).build())
                .keySchema(
                        KeySchemaElement.builder().attributeName("pk").keyType(KeyType.HASH).build(),
                        KeySchemaElement.builder().attributeName("sk").keyType(KeyType.RANGE).build())
                .billingMode(BillingMode.PAY_PER_REQUEST));
    }

    private void deleteTableSilently(String name) {
        if (name == null) return;
        try { ddb().deleteTable(r -> r.tableName(name)); } catch (Exception ignored) {}
    }
}
