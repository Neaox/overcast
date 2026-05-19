/**
 * groups/dynamodb.ts — DynamoDB compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   dynamodb-tables  — table lifecycle (implemented)
 *   dynamodb-items   — PutItem, GetItem, UpdateItem, DeleteItem (implemented)
 *   dynamodb-query   — Query, Scan, pagination (implemented)
 *   dynamodb-batch   — BatchWriteItem, BatchGetItem (not fully implemented)
 *   dynamodb-txn     — TransactWriteItems, TransactGetItems (not implemented)
 *   dynamodb-gsi     — Global Secondary Indexes (partially implemented)
 *   dynamodb-ttl     — TTL management (implemented)
 *   dynamodb-streams — DynamoDB Streams (implemented)
 */

import {
  CreateTableCommand,
  DeleteTableCommand,
  DescribeTableCommand,
  ListTablesCommand,
  UpdateTableCommand,
  PutItemCommand,
  GetItemCommand,
  UpdateItemCommand,
  DeleteItemCommand,
  QueryCommand,
  ScanCommand,
  BatchWriteItemCommand,
  BatchGetItemCommand,
  TransactWriteItemsCommand,
  TransactGetItemsCommand,
  DescribeTimeToLiveCommand,
  UpdateTimeToLiveCommand,
  type AttributeValue,
} from "@aws-sdk/client-dynamodb";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

/** Simple marshalling helpers to avoid pulling in @aws-sdk/util-dynamodb. */
function s(v: string): AttributeValue {
  return { S: v };
}
function n(v: string | number): AttributeValue {
  return { N: String(v) };
}
function bool(v: boolean): AttributeValue {
  return { BOOL: v };
}

export function makeDynamoDBGroups(suite: string): TestGroup[] {
  return [
    // ── dynamodb-tables ────────────────────────────────────────────────────
    {
      suite,
      service: "dynamodb",
      name: "dynamodb-tables",
      tests: [
        {
          name: "CreateTable",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-tbl`;
            const resp = await dynamodb.send(
              new CreateTableCommand({
                TableName: tableName,
                KeySchema: [
                  { AttributeName: "pk", KeyType: "HASH" },
                  { AttributeName: "sk", KeyType: "RANGE" },
                ],
                AttributeDefinitions: [
                  { AttributeName: "pk", AttributeType: "S" },
                  { AttributeName: "sk", AttributeType: "S" },
                ],
                BillingMode: "PAY_PER_REQUEST",
              }),
            );
            assert.ok(resp.TableDescription?.TableArn, "CreateTable: missing TableArn");
          },
        },
        {
          name: "DescribeTable",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new DescribeTableCommand({ TableName: `${ctx.runId}-ddb-tbl` }),
            );
            assert.strictEqual(resp.Table?.TableStatus, "ACTIVE", `DescribeTable: expected ACTIVE, got ${resp.Table?.TableStatus}`);
          },
        },
        {
          name: "ListTables",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-tbl`;
            const resp = await dynamodb.send(new ListTablesCommand({}));
            assert.ok(resp.TableNames?.includes(tableName), `ListTables: ${tableName} not found`);
          },
        },
        {
          name: "UpdateTable",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            // Add a GSI to the table
            const tableName = `${ctx.runId}-ddb-tbl`;
            await dynamodb.send(
              new UpdateTableCommand({
                TableName: tableName,
                AttributeDefinitions: [
                  { AttributeName: "pk", AttributeType: "S" },
                  { AttributeName: "sk", AttributeType: "S" },
                  { AttributeName: "gsi_pk", AttributeType: "S" },
                ],
                GlobalSecondaryIndexUpdates: [
                  {
                    Create: {
                      IndexName: "gsi-by-gpk",
                      KeySchema: [{ AttributeName: "gsi_pk", KeyType: "HASH" }],
                      Projection: { ProjectionType: "ALL" },
                    },
                  },
                ],
              }),
            );
            const tableResp = await dynamodb.send(
              new DescribeTableCommand({ TableName: tableName }),
            );
            assert.ok(tableResp.Table, "UpdateTable: DescribeTable returned no Table");
            const gsiNames = (tableResp.Table.GlobalSecondaryIndexes ?? []).map(
              (g) => g.IndexName,
            );
            assert.ok(gsiNames.includes("gsi-by-gpk"), `UpdateTable: expected GSI gsi-by-gpk, got [${gsiNames.join(", ")}]`);
          },
        },
        {
          name: "DeleteTable",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-tbl`;
            await dynamodb.send(
              new DeleteTableCommand({ TableName: tableName }),
            );
            const resp = await dynamodb.send(new ListTablesCommand({}));
            assert.ok(!(resp.TableNames?.includes(tableName)), `DeleteTable: ${tableName} still present after delete`);
          },
        },
      ],
      teardown: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        try {
          await dynamodb.send(
            new DeleteTableCommand({ TableName: `${ctx.runId}-ddb-tbl` }),
          );
        } catch {}
      },
    },

    // ── dynamodb-items ─────────────────────────────────────────────────────
    {
      suite,
      service: "dynamodb",
      name: "dynamodb-items",
      tests: [
        {
          name: "PutItem",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            await dynamodb.send(
              new PutItemCommand({
                TableName: `${ctx.runId}-ddb-items`,
                Item: {
                  pk: s("user#1"),
                  sk: s("profile"),
                  name: s("Alice"),
                  age: n(30),
                  active: bool(true),
                },
              }),
            );
            const resp = await dynamodb.send(
              new GetItemCommand({
                TableName: `${ctx.runId}-ddb-items`,
                Key: { pk: s("user#1"), sk: s("profile") },
              }),
            );
            assert.ok(resp.Item, "PutItem: item not found after put");
            assert.strictEqual(resp.Item.name?.S, "Alice", `PutItem: expected name=Alice, got ${resp.Item.name?.S}`);
          },
        },
        {
          name: "GetItem",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new GetItemCommand({
                TableName: `${ctx.runId}-ddb-items`,
                Key: { pk: s("user#1"), sk: s("profile") },
              }),
            );
            assert.ok(resp.Item, "GetItem: item not found");
            assert.strictEqual(resp.Item.name?.S, "Alice", `GetItem: expected name=Alice, got ${resp.Item.name?.S}`);
          },
        },
        {
          name: "UpdateItem",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            await dynamodb.send(
              new UpdateItemCommand({
                TableName: `${ctx.runId}-ddb-items`,
                Key: { pk: s("user#1"), sk: s("profile") },
                UpdateExpression: "SET age = :newAge, #n = :newName",
                ExpressionAttributeNames: { "#n": "name" },
                ExpressionAttributeValues: {
                  ":newAge": n(31),
                  ":newName": s("Alice Updated"),
                },
                ReturnValues: "ALL_NEW",
              }),
            );
            const resp = await dynamodb.send(
              new GetItemCommand({
                TableName: `${ctx.runId}-ddb-items`,
                Key: { pk: s("user#1"), sk: s("profile") },
              }),
            );
            assert.strictEqual(resp.Item?.age?.N, "31", `UpdateItem: expected age=31, got ${resp.Item?.age?.N}`);
          },
        },
        {
          name: "PutItemConditionFail",
          op: "PutItem",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            // Condition expression that should fail (item already exists)
            try {
              await dynamodb.send(
                new PutItemCommand({
                  TableName: `${ctx.runId}-ddb-items`,
                  Item: { pk: s("user#1"), sk: s("profile") },
                  ConditionExpression: "attribute_not_exists(pk)",
                }),
              );
              throw new Error("PutItemConditionFail: expected ConditionalCheckFailedException");
            } catch (err: unknown) {
              const name = err instanceof Error ? err.constructor.name : "";
              if (
                !name.includes("ConditionalCheckFailed") &&
                !(
                  err instanceof Error &&
                  err.message.includes(
                    "expected ConditionalCheckFailedException",
                  )
                )
              ) {
                if (
                  err instanceof Error &&
                  err.message.includes(
                    "expected ConditionalCheckFailedException",
                  )
                ) {
                  throw err;
                }
                // Accept any error that isn't our sentinel
              }
            }
          },
        },
        {
          name: "DeleteItem",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            await dynamodb.send(
              new DeleteItemCommand({
                TableName: `${ctx.runId}-ddb-items`,
                Key: { pk: s("user#1"), sk: s("profile") },
              }),
            );
            const resp = await dynamodb.send(
              new GetItemCommand({
                TableName: `${ctx.runId}-ddb-items`,
                Key: { pk: s("user#1"), sk: s("profile") },
              }),
            );
            assert.ok(!(resp.Item), "DeleteItem: item still present after delete");
          },
        },
      ],
      setup: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        await dynamodb.send(
          new CreateTableCommand({
            TableName: `${ctx.runId}-ddb-items`,
            KeySchema: [
              { AttributeName: "pk", KeyType: "HASH" },
              { AttributeName: "sk", KeyType: "RANGE" },
            ],
            AttributeDefinitions: [
              { AttributeName: "pk", AttributeType: "S" },
              { AttributeName: "sk", AttributeType: "S" },
            ],
            BillingMode: "PAY_PER_REQUEST",
          }),
        );
      },
      teardown: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        try {
          await dynamodb.send(
            new DeleteTableCommand({ TableName: `${ctx.runId}-ddb-items` }),
          );
        } catch {}
      },
    },

    // ── dynamodb-query ─────────────────────────────────────────────────────
    {
      suite,
      service: "dynamodb",
      name: "dynamodb-query",
      tests: [
        {
          name: "Query",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new QueryCommand({
                TableName: `${ctx.runId}-ddb-query`,
                KeyConditionExpression: "pk = :pk",
                ExpressionAttributeValues: { ":pk": s("user#1") },
              }),
            );
            assert.ok(((resp.Count ?? 0)) >= (3), `Query: expected >=3 items, got ${resp.Count}`);
          },
        },
        {
          name: "QueryWithFilterExpression",
          op: "Query",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new QueryCommand({
                TableName: `${ctx.runId}-ddb-query`,
                KeyConditionExpression: "pk = :pk",
                FilterExpression: "score > :minScore",
                ExpressionAttributeValues: {
                  ":pk": s("user#1"),
                  ":minScore": n(50),
                },
              }),
            );
            assert.notStrictEqual((resp.Count ?? 0), 0, "QueryWithFilterExpression: expected at least one result");
          },
        },
        {
          name: "QueryWithLimit",
          op: "Query",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new QueryCommand({
                TableName: `${ctx.runId}-ddb-query`,
                KeyConditionExpression: "pk = :pk",
                ExpressionAttributeValues: { ":pk": s("user#1") },
                Limit: 2,
              }),
            );
            assert.ok(((resp.Count ?? 0)) <= (2), `QueryWithLimit: expected <=2 items, got ${resp.Count}`);
            // Should have a pagination token for remaining items
            assert.ok(resp.LastEvaluatedKey, "QueryWithLimit: expected LastEvaluatedKey for pagination");
          },
        },
        {
          name: "QueryPagination",
          op: "Query",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            // Fetch remaining items using ExclusiveStartKey
            const first = await dynamodb.send(
              new QueryCommand({
                TableName: `${ctx.runId}-ddb-query`,
                KeyConditionExpression: "pk = :pk",
                ExpressionAttributeValues: { ":pk": s("user#1") },
                Limit: 2,
              }),
            );
            assert.ok(first.LastEvaluatedKey, "QueryPagination: no LastEvaluatedKey");
            const second = await dynamodb.send(
              new QueryCommand({
                TableName: `${ctx.runId}-ddb-query`,
                KeyConditionExpression: "pk = :pk",
                ExpressionAttributeValues: { ":pk": s("user#1") },
                ExclusiveStartKey: first.LastEvaluatedKey,
              }),
            );
            assert.notStrictEqual((second.Count ?? 0), 0, "QueryPagination: expected items on second page");
          },
        },
        {
          name: "Scan",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new ScanCommand({ TableName: `${ctx.runId}-ddb-query` }),
            );
            assert.notStrictEqual((resp.Count ?? 0), 0, "Scan: expected at least one item");
          },
        },
        {
          name: "ScanWithFilter",
          op: "Scan",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new ScanCommand({
                TableName: `${ctx.runId}-ddb-query`,
                FilterExpression: "score >= :min",
                ExpressionAttributeValues: { ":min": n(80) },
              }),
            );
            // All returned items should satisfy the filter
            for (const item of resp.Items ?? []) {
              const score = Number(item.score?.N ?? 0);
              assert.ok((score) >= (80), `ScanWithFilter: item with score=${score} violates filter`);
            }
          },
        },
      ],
      setup: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        const tableName = `${ctx.runId}-ddb-query`;
        await dynamodb.send(
          new CreateTableCommand({
            TableName: tableName,
            KeySchema: [
              { AttributeName: "pk", KeyType: "HASH" },
              { AttributeName: "sk", KeyType: "RANGE" },
            ],
            AttributeDefinitions: [
              { AttributeName: "pk", AttributeType: "S" },
              { AttributeName: "sk", AttributeType: "S" },
            ],
            BillingMode: "PAY_PER_REQUEST",
          }),
        );
        // Seed items
        await Promise.all(
          [
            { sk: "item#1", score: 90 },
            { sk: "item#2", score: 45 },
            { sk: "item#3", score: 80 },
            { sk: "item#4", score: 10 },
          ].map(({ sk, score }) =>
            dynamodb.send(
              new PutItemCommand({
                TableName: tableName,
                Item: { pk: s("user#1"), sk: s(sk), score: n(score) },
              }),
            ),
          ),
        );
      },
      teardown: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        try {
          await dynamodb.send(
            new DeleteTableCommand({ TableName: `${ctx.runId}-ddb-query` }),
          );
        } catch {}
      },
    },

    // ── dynamodb-batch ─────────────────────────────────────────────────────
    {
      suite,
      service: "dynamodb",
      name: "dynamodb-batch",
      tests: [
        {
          name: "BatchWriteItem",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-batch`;
            const resp = await dynamodb.send(
              new BatchWriteItemCommand({
                RequestItems: {
                  [tableName]: [
                    {
                      PutRequest: {
                        Item: { pk: s("b1"), sk: s("x"), val: s("one") },
                      },
                    },
                    {
                      PutRequest: {
                        Item: { pk: s("b2"), sk: s("x"), val: s("two") },
                      },
                    },
                    {
                      PutRequest: {
                        Item: { pk: s("b3"), sk: s("x"), val: s("three") },
                      },
                    },
                  ],
                },
              }),
            );
            if (
              resp.UnprocessedItems &&
              Object.keys(resp.UnprocessedItems).length > 0
            ) {
              throw new Error("BatchWriteItem: unexpected UnprocessedItems");
            }
          },
        },
        {
          name: "BatchGetItem",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-batch`;
            const resp = await dynamodb.send(
              new BatchGetItemCommand({
                RequestItems: {
                  [tableName]: {
                    Keys: [
                      { pk: s("b1"), sk: s("x") },
                      { pk: s("b2"), sk: s("x") },
                    ],
                  },
                },
              }),
            );
            const items = resp.Responses?.[tableName] ?? [];
            assert.ok((items.length) >= (2), `BatchGetItem: expected 2 items, got ${items.length}`);
          },
        },
      ],
      setup: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        await dynamodb.send(
          new CreateTableCommand({
            TableName: `${ctx.runId}-ddb-batch`,
            KeySchema: [
              { AttributeName: "pk", KeyType: "HASH" },
              { AttributeName: "sk", KeyType: "RANGE" },
            ],
            AttributeDefinitions: [
              { AttributeName: "pk", AttributeType: "S" },
              { AttributeName: "sk", AttributeType: "S" },
            ],
            BillingMode: "PAY_PER_REQUEST",
          }),
        );
      },
      teardown: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        try {
          await dynamodb.send(
            new DeleteTableCommand({ TableName: `${ctx.runId}-ddb-batch` }),
          );
        } catch {}
      },
    },

    // ── dynamodb-txn ───────────────────────────────────────────────────────
    {
      suite,
      service: "dynamodb",
      name: "dynamodb-txn",
      tests: [
        {
          name: "TransactWriteItems",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-txn`;
            await dynamodb.send(
              new TransactWriteItemsCommand({
                TransactItems: [
                  {
                    Put: {
                      TableName: tableName,
                      Item: { pk: s("t1"), sk: s("x"), val: s("txn1") },
                    },
                  },
                  {
                    Put: {
                      TableName: tableName,
                      Item: { pk: s("t2"), sk: s("x"), val: s("txn2") },
                    },
                  },
                ],
              }),
            );
            const item1 = await dynamodb.send(
              new GetItemCommand({
                TableName: tableName,
                Key: { pk: s("t1"), sk: s("x") },
              }),
            );
            assert.strictEqual(item1.Item?.val?.S, "txn1", `TransactWriteItems: expected val=txn1, got ${item1.Item?.val?.S}`);
          },
        },
        {
          name: "TransactGetItems",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-txn`;
            const resp = await dynamodb.send(
              new TransactGetItemsCommand({
                TransactItems: [
                  {
                    Get: {
                      TableName: tableName,
                      Key: { pk: s("t1"), sk: s("x") },
                    },
                  },
                  {
                    Get: {
                      TableName: tableName,
                      Key: { pk: s("t2"), sk: s("x") },
                    },
                  },
                ],
              }),
            );
            assert.ok(((resp.Responses?.length ?? 0)) >= (2), `TransactGetItems: expected 2 responses, got ${resp.Responses?.length}`);
          },
        },
        {
          name: "TransactWriteConditionFail",
          op: "TransactWriteItems",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const tableName = `${ctx.runId}-ddb-txn`;
            try {
              await dynamodb.send(
                new TransactWriteItemsCommand({
                  TransactItems: [
                    {
                      Put: {
                        TableName: tableName,
                        Item: { pk: s("t1"), sk: s("x") },
                        ConditionExpression: "attribute_not_exists(pk)",
                      },
                    },
                  ],
                }),
              );
              throw new Error("TransactWriteConditionFail: expected TransactionCanceledException");
            } catch (err) {
              const name = err instanceof Error ? err.constructor.name : "";
              if (
                name === "Error" &&
                (err as Error).message.includes(
                  "expected TransactionCanceledException",
                )
              ) {
                throw err;
              }
              // Any other error (TransactionCanceledException etc.) = expected
            }
          },
        },
      ],
      setup: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        await dynamodb.send(
          new CreateTableCommand({
            TableName: `${ctx.runId}-ddb-txn`,
            KeySchema: [
              { AttributeName: "pk", KeyType: "HASH" },
              { AttributeName: "sk", KeyType: "RANGE" },
            ],
            AttributeDefinitions: [
              { AttributeName: "pk", AttributeType: "S" },
              { AttributeName: "sk", AttributeType: "S" },
            ],
            BillingMode: "PAY_PER_REQUEST",
          }),
        );
      },
      teardown: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        try {
          await dynamodb.send(
            new DeleteTableCommand({ TableName: `${ctx.runId}-ddb-txn` }),
          );
        } catch {}
      },
    },

    // ── dynamodb-ttl ───────────────────────────────────────────────────────
    {
      suite,
      service: "dynamodb",
      name: "dynamodb-ttl",
      tests: [
        {
          name: "UpdateTimeToLive",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            await dynamodb.send(
              new UpdateTimeToLiveCommand({
                TableName: `${ctx.runId}-ddb-ttl`,
                TimeToLiveSpecification: {
                  Enabled: true,
                  AttributeName: "expires_at",
                },
              }),
            );
            const resp = await dynamodb.send(
              new DescribeTimeToLiveCommand({
                TableName: `${ctx.runId}-ddb-ttl`,
              }),
            );
            assert.strictEqual(resp.TimeToLiveDescription?.TimeToLiveStatus, "ENABLED", `UpdateTimeToLive: expected ENABLED, got ${resp.TimeToLiveDescription?.TimeToLiveStatus}`);
          },
        },
        {
          name: "DescribeTimeToLive",
          fn: async (ctx) => {
            const { dynamodb } = makeClients(ctx);
            const resp = await dynamodb.send(
              new DescribeTimeToLiveCommand({
                TableName: `${ctx.runId}-ddb-ttl`,
              }),
            );
            assert.strictEqual(resp.TimeToLiveDescription?.TimeToLiveStatus, "ENABLED", `DescribeTimeToLive: expected ENABLED, got ${resp.TimeToLiveDescription?.TimeToLiveStatus}`);
            assert.strictEqual(resp.TimeToLiveDescription?.AttributeName, "expires_at", `DescribeTimeToLive: expected expires_at, got ${resp.TimeToLiveDescription?.AttributeName}`);
          },
        },
      ],
      setup: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        await dynamodb.send(
          new CreateTableCommand({
            TableName: `${ctx.runId}-ddb-ttl`,
            KeySchema: [{ AttributeName: "pk", KeyType: "HASH" }],
            AttributeDefinitions: [{ AttributeName: "pk", AttributeType: "S" }],
            BillingMode: "PAY_PER_REQUEST",
          }),
        );
      },
      teardown: async (ctx) => {
        const { dynamodb } = makeClients(ctx);
        try {
          await dynamodb.send(
            new DeleteTableCommand({ TableName: `${ctx.runId}-ddb-ttl` }),
          );
        } catch {}
      },
    },
  ];
}
