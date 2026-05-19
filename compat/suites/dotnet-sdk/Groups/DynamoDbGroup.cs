using Amazon.DynamoDBv2;
using Amazon.DynamoDBv2.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class DynamoDbGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["CreateTable"] = CreateTableAsync,
        ["DescribeTable"] = DescribeTableAsync,
        ["ListTables"] = ListTablesAsync,
        ["UpdateTable"] = UpdateTableAsync,
        ["DeleteTable"] = DeleteTableAsync,
        ["PutItem"] = PutItemAsync,
        ["GetItem"] = GetItemAsync,
        ["UpdateItem"] = UpdateItemAsync,
        ["PutItemConditionFail"] = PutItemConditionFailAsync,
        ["DeleteItem"] = DeleteItemAsync,
        ["Query"] = QueryAsync,
        ["QueryWithFilterExpression"] = QueryWithFilterExpressionAsync,
        ["QueryWithLimit"] = QueryWithLimitAsync,
        ["QueryPagination"] = QueryPaginationAsync,
        ["Scan"] = ScanAsync,
        ["ScanWithFilter"] = ScanWithFilterAsync,
        ["BatchWriteItem"] = BatchWriteItemAsync,
        ["BatchGetItem"] = BatchGetItemAsync,
        ["TransactWriteItems"] = TransactWriteItemsAsync,
        ["TransactGetItems"] = TransactGetItemsAsync,
        ["TransactWriteConditionFail"] = TransactWriteConditionFailAsync,
        ["UpdateTimeToLive"] = UpdateTimeToLiveAsync,
        ["DescribeTimeToLive"] = DescribeTimeToLiveAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["dynamodb-tables"] = SetupTablesAsync,
        ["dynamodb-items"] = SetupItemsAsync,
        ["dynamodb-query"] = SetupQueryAsync,
        ["dynamodb-batch"] = SetupBatchAsync,
        ["dynamodb-txn"] = SetupTxnAsync,
        ["dynamodb-ttl"] = SetupTtlAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["dynamodb-tables"] = context => DeleteTableIfExistsAsync(context.GetString("dynamodbTablesTable")),
        ["dynamodb-items"] = context => DeleteTableIfExistsAsync(context.GetString("dynamodbItemsTable")),
        ["dynamodb-query"] = context => DeleteTableIfExistsAsync(context.GetString("dynamodbQueryTable")),
        ["dynamodb-batch"] = context => DeleteTableIfExistsAsync(context.GetString("dynamodbBatchTable")),
        ["dynamodb-txn"] = context => DeleteTableIfExistsAsync(context.GetString("dynamodbTxnTable")),
        ["dynamodb-ttl"] = context => DeleteTableIfExistsAsync(context.GetString("dynamodbTtlTable")),
    };

    // ---- helpers ----

    private async Task CreateTableWithKeysAsync(string tableName, bool withRangeKey)
    {
        var keySchema = new List<KeySchemaElement>
        {
            new("pk", KeyType.HASH),
        };
        var attrDefs = new List<AttributeDefinition>
        {
            new("pk", ScalarAttributeType.S),
        };
        if (withRangeKey)
        {
            keySchema.Add(new KeySchemaElement("sk", KeyType.RANGE));
            attrDefs.Add(new AttributeDefinition("sk", ScalarAttributeType.S));
        }

        await clients.DynamoDB().CreateTableAsync(new CreateTableRequest
        {
            TableName = tableName,
            KeySchema = keySchema,
            AttributeDefinitions = attrDefs,
            BillingMode = BillingMode.PAY_PER_REQUEST,
        });
    }

    private async Task DeleteTableIfExistsAsync(string? tableName)
    {
        if (string.IsNullOrWhiteSpace(tableName))
        {
            return;
        }

        try
        {
            await clients.DynamoDB().DeleteTableAsync(new DeleteTableRequest { TableName = tableName });
        }
        catch
        {
        }
    }

    private static string RequireTable(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }

    private static Dictionary<string, AttributeValue> Key(string pk, string sk)
    {
        return new Dictionary<string, AttributeValue>
        {
            ["pk"] = new AttributeValue { S = pk },
            ["sk"] = new AttributeValue { S = sk },
        };
    }

    private static Dictionary<string, AttributeValue> Item(string pk, string sk, string value)
    {
        return new Dictionary<string, AttributeValue>
        {
            ["pk"] = new AttributeValue { S = pk },
            ["sk"] = new AttributeValue { S = sk },
            ["value"] = new AttributeValue { S = value },
        };
    }

    // ---- setups ----

    private async Task SetupTablesAsync(TestContext context)
    {
        var tableName = $"{context.RunId}-ddbtbl";
        await CreateTableWithKeysAsync(tableName, withRangeKey: true);
        context.Set("dynamodbTablesTable", tableName);
    }

    private async Task SetupItemsAsync(TestContext context)
    {
        var tableName = $"{context.RunId}-ddbitem";
        await CreateTableWithKeysAsync(tableName, withRangeKey: true);
        context.Set("dynamodbItemsTable", tableName);
    }

    private async Task SetupQueryAsync(TestContext context)
    {
        var tableName = $"{context.RunId}-ddbqry";
        await CreateTableWithKeysAsync(tableName, withRangeKey: true);

        var ddb = clients.DynamoDB();
        await ddb.PutItemAsync(new PutItemRequest
        {
            TableName = tableName,
            Item = Item("user-1", "2024-01-01", "a"),
        });
        await ddb.PutItemAsync(new PutItemRequest
        {
            TableName = tableName,
            Item = Item("user-1", "2024-02-01", "b"),
        });
        await ddb.PutItemAsync(new PutItemRequest
        {
            TableName = tableName,
            Item = Item("user-1", "2024-03-01", "c"),
        });
        await ddb.PutItemAsync(new PutItemRequest
        {
            TableName = tableName,
            Item = Item("user-2", "2024-01-01", "d"),
        });

        context.Set("dynamodbQueryTable", tableName);
    }

    private async Task SetupBatchAsync(TestContext context)
    {
        var tableName = $"{context.RunId}-ddbbat";
        await CreateTableWithKeysAsync(tableName, withRangeKey: true);
        context.Set("dynamodbBatchTable", tableName);
    }

    private async Task SetupTxnAsync(TestContext context)
    {
        var tableName = $"{context.RunId}-ddbtxn";
        await CreateTableWithKeysAsync(tableName, withRangeKey: true);
        context.Set("dynamodbTxnTable", tableName);
    }

    private async Task SetupTtlAsync(TestContext context)
    {
        var tableName = $"{context.RunId}-ddbttl";
        await CreateTableWithKeysAsync(tableName, withRangeKey: false);
        context.Set("dynamodbTtlTable", tableName);
    }

    // ---- dynamodb-tables ----

    private async Task CreateTableAsync(TestContext context)
    {
        var tableName = $"{context.RunId}-ddbcreate";
        await clients.DynamoDB().CreateTableAsync(new CreateTableRequest
        {
            TableName = tableName,
            KeySchema =
            [
                new KeySchemaElement("pk", KeyType.HASH),
                new KeySchemaElement("sk", KeyType.RANGE),
            ],
            AttributeDefinitions =
            [
                new AttributeDefinition("pk", ScalarAttributeType.S),
                new AttributeDefinition("sk", ScalarAttributeType.S),
            ],
            BillingMode = BillingMode.PAY_PER_REQUEST,
        });

        try
        {
            var listResponse = await clients.DynamoDB().ListTablesAsync();
            Assertions.True(listResponse.TableNames.Contains(tableName),
                $"CreateTable: table {tableName} not found in ListTables (runId={context.RunId})");
        }
        finally
        {
            try
            {
                await clients.DynamoDB().DeleteTableAsync(new DeleteTableRequest { TableName = tableName });
            }
            catch
            {
            }
        }
    }

    private async Task DescribeTableAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTablesTable");
        var response = await clients.DynamoDB().DescribeTableAsync(new DescribeTableRequest { TableName = tableName });
        Assertions.NotNull(response.Table, "DescribeTable: table");
        Assertions.NotBlank(response.Table.TableName, "DescribeTable: tableName");
        Assertions.Equal(TableStatus.ACTIVE, response.Table.TableStatus,
            $"DescribeTable: expected ACTIVE but was {response.Table.TableStatus} (runId={context.RunId})");
    }

    private async Task ListTablesAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTablesTable");
        var response = await clients.DynamoDB().ListTablesAsync();
        Assertions.True(response.TableNames.Contains(tableName),
            $"ListTables: table {tableName} not found (runId={context.RunId})");
    }

    private async Task UpdateTableAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTablesTable");
        await clients.DynamoDB().UpdateTableAsync(new UpdateTableRequest
        {
            TableName = tableName,
            AttributeDefinitions =
            [
                new AttributeDefinition("gsi_pk", ScalarAttributeType.S),
            ],
            GlobalSecondaryIndexUpdates =
            [
                new GlobalSecondaryIndexUpdate
                {
                    Create = new CreateGlobalSecondaryIndexAction
                    {
                        IndexName = "gsi1",
                        KeySchema =
                        [
                            new KeySchemaElement("gsi_pk", KeyType.HASH),
                        ],
                        Projection = new Projection { ProjectionType = ProjectionType.ALL },
                    },
                },
            ],
        });

        var desc = await clients.DynamoDB().DescribeTableAsync(new DescribeTableRequest { TableName = tableName });
        Assertions.True(
            desc.Table.GlobalSecondaryIndexes.Any(idx => idx.IndexName == "gsi1"),
            $"UpdateTable: GSI gsi1 not found (runId={context.RunId})");
    }

    private async Task DeleteTableAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTablesTable");
        await clients.DynamoDB().DeleteTableAsync(new DeleteTableRequest { TableName = tableName });
        var listResponse = await clients.DynamoDB().ListTablesAsync();
        Assertions.False(listResponse.TableNames.Contains(tableName),
            $"DeleteTable: table {tableName} still present after deletion (runId={context.RunId})");
    }

    // ---- dynamodb-items ----

    private async Task PutItemAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbItemsTable");
        var item = new Dictionary<string, AttributeValue>
        {
            ["pk"] = new AttributeValue { S = "user-1" },
            ["sk"] = new AttributeValue { S = "profile" },
            ["name"] = new AttributeValue { S = "Alice" },
            ["age"] = new AttributeValue { N = "30" },
        };
        await clients.DynamoDB().PutItemAsync(new PutItemRequest
        {
            TableName = tableName,
            Item = item,
        });

        var getResponse = await clients.DynamoDB().GetItemAsync(new GetItemRequest
        {
            TableName = tableName,
            Key = Key("user-1", "profile"),
        });
        Assertions.NotNull(getResponse.Item, "PutItem: item not found after put");
        Assertions.Equal("Alice", getResponse.Item["name"].S,
            $"PutItem: name mismatch (runId={context.RunId})");
    }

    private async Task GetItemAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbItemsTable");
        var response = await clients.DynamoDB().GetItemAsync(new GetItemRequest
        {
            TableName = tableName,
            Key = Key("user-1", "profile"),
        });
        Assertions.NotNull(response.Item, "GetItem: item not found");
        Assertions.Equal("Alice", response.Item["name"].S,
            $"GetItem: name mismatch (runId={context.RunId})");
    }

    private async Task UpdateItemAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbItemsTable");
        await clients.DynamoDB().UpdateItemAsync(new UpdateItemRequest
        {
            TableName = tableName,
            Key = Key("user-1", "profile"),
            UpdateExpression = "SET age = :age",
            ExpressionAttributeValues = new Dictionary<string, AttributeValue>
            {
                [":age"] = new AttributeValue { N = "31" },
            },
        });

        var getResponse = await clients.DynamoDB().GetItemAsync(new GetItemRequest
        {
            TableName = tableName,
            Key = Key("user-1", "profile"),
        });
        Assertions.NotNull(getResponse.Item, "UpdateItem: item not found");
        Assertions.Equal("31", getResponse.Item["age"].N,
            $"UpdateItem: age not updated (runId={context.RunId})");
    }

    private async Task PutItemConditionFailAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbItemsTable");
        try
        {
            await clients.DynamoDB().PutItemAsync(new PutItemRequest
            {
                TableName = tableName,
                Item = new Dictionary<string, AttributeValue>
                {
                    ["pk"] = new AttributeValue { S = "user-1" },
                    ["sk"] = new AttributeValue { S = "profile" },
                    ["name"] = new AttributeValue { S = "Bob" },
                },
                ConditionExpression = "attribute_not_exists(pk)",
            });
            throw new InvalidOperationException(
                $"PutItemConditionFail: expected ConditionalCheckFailedException but call succeeded (runId={context.RunId})");
        }
        catch (ConditionalCheckFailedException)
        {
        }
    }

    private async Task DeleteItemAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbItemsTable");
        await clients.DynamoDB().DeleteItemAsync(new DeleteItemRequest
        {
            TableName = tableName,
            Key = Key("user-1", "profile"),
        });

        var getResponse = await clients.DynamoDB().GetItemAsync(new GetItemRequest
        {
            TableName = tableName,
            Key = Key("user-1", "profile"),
        });
        Assertions.True(getResponse.Item.Count == 0,
            $"DeleteItem: item still present after deletion (runId={context.RunId})");
    }

    // ---- dynamodb-query ----

    private async Task QueryAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbQueryTable");
        var response = await clients.DynamoDB().QueryAsync(new QueryRequest
        {
            TableName = tableName,
            KeyConditionExpression = "pk = :pk",
            ExpressionAttributeValues = new Dictionary<string, AttributeValue>
            {
                [":pk"] = new AttributeValue { S = "user-1" },
            },
        });
        Assertions.GreaterThanOrEqual(3, response.Count ?? 0,
            $"Query: expected >= 3 items for user-1 (runId={context.RunId})");
    }

    private async Task QueryWithFilterExpressionAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbQueryTable");
        var response = await clients.DynamoDB().QueryAsync(new QueryRequest
        {
            TableName = tableName,
            KeyConditionExpression = "pk = :pk",
            FilterExpression = "#v = :val",
            ExpressionAttributeNames = new Dictionary<string, string> { ["#v"] = "value" },
            ExpressionAttributeValues = new Dictionary<string, AttributeValue>
            {
                [":pk"] = new AttributeValue { S = "user-1" },
                [":val"] = new AttributeValue { S = "a" },
            },
        });
        Assertions.GreaterThanOrEqual(1, response.Count ?? 0,
            $"QueryWithFilterExpression: expected >= 1 item (runId={context.RunId})");
    }

    private async Task QueryWithLimitAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbQueryTable");
        var response = await clients.DynamoDB().QueryAsync(new QueryRequest
        {
            TableName = tableName,
            KeyConditionExpression = "pk = :pk",
            ExpressionAttributeValues = new Dictionary<string, AttributeValue>
            {
                [":pk"] = new AttributeValue { S = "user-1" },
            },
            Limit = 2,
        });
        Assertions.Equal(2, response.Count ?? 0,
            $"QueryWithLimit: expected exactly 2 items (runId={context.RunId})");
    }

    private async Task QueryPaginationAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbQueryTable");
        var page1 = await clients.DynamoDB().QueryAsync(new QueryRequest
        {
            TableName = tableName,
            KeyConditionExpression = "pk = :pk",
            ExpressionAttributeValues = new Dictionary<string, AttributeValue>
            {
                [":pk"] = new AttributeValue { S = "user-1" },
            },
            Limit = 2,
        });
        Assertions.Equal(2, page1.Count ?? 0,
            $"QueryPagination page1: expected 2 items (runId={context.RunId})");
        Assertions.True(page1.LastEvaluatedKey.Count > 0,
            $"QueryPagination page1: expected LastEvaluatedKey (runId={context.RunId})");

        var page2 = await clients.DynamoDB().QueryAsync(new QueryRequest
        {
            TableName = tableName,
            KeyConditionExpression = "pk = :pk",
            ExpressionAttributeValues = new Dictionary<string, AttributeValue>
            {
                [":pk"] = new AttributeValue { S = "user-1" },
            },
            Limit = 2,
            ExclusiveStartKey = page1.LastEvaluatedKey,
        });
        Assertions.GreaterThanOrEqual(1, page2.Count ?? 0,
            $"QueryPagination page2: expected >= 1 item (runId={context.RunId})");
    }

    private async Task ScanAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbQueryTable");
        var response = await clients.DynamoDB().ScanAsync(new ScanRequest { TableName = tableName });
        Assertions.GreaterThanOrEqual(4, response.Count ?? 0,
            $"Scan: expected >= 4 items (runId={context.RunId})");
    }

    private async Task ScanWithFilterAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbQueryTable");
        var response = await clients.DynamoDB().ScanAsync(new ScanRequest
        {
            TableName = tableName,
            FilterExpression = "#v = :val",
            ExpressionAttributeNames = new Dictionary<string, string> { ["#v"] = "value" },
            ExpressionAttributeValues = new Dictionary<string, AttributeValue>
            {
                [":val"] = new AttributeValue { S = "a" },
            },
        });
        Assertions.GreaterThanOrEqual(1, response.Count ?? 0,
            $"ScanWithFilter: expected >= 1 item matching filter (runId={context.RunId})");
    }

    // ---- dynamodb-batch ----

    private async Task BatchWriteItemAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbBatchTable");
        await clients.DynamoDB().BatchWriteItemAsync(new BatchWriteItemRequest
        {
            RequestItems = new Dictionary<string, List<WriteRequest>>
            {
                [tableName] =
                [
                    new WriteRequest { PutRequest = new PutRequest { Item = Item("batch-1", "a", "one") } },
                    new WriteRequest { PutRequest = new PutRequest { Item = Item("batch-2", "b", "two") } },
                ],
            },
        });

        var get1 = await clients.DynamoDB().GetItemAsync(new GetItemRequest
        {
            TableName = tableName,
            Key = Key("batch-1", "a"),
        });
        Assertions.NotNull(get1.Item, "BatchWriteItem: batch-1 not found");
        Assertions.Equal("one", get1.Item["value"].S,
            $"BatchWriteItem: batch-1 value mismatch (runId={context.RunId})");
    }

    private async Task BatchGetItemAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbBatchTable");
        var response = await clients.DynamoDB().BatchGetItemAsync(new BatchGetItemRequest
        {
            RequestItems = new Dictionary<string, KeysAndAttributes>
            {
                [tableName] = new KeysAndAttributes
                {
                    Keys = [Key("batch-1", "a"), Key("batch-2", "b")],
                },
            },
        });
        Assertions.True(response.Responses.ContainsKey(tableName),
            $"BatchGetItem: no responses for table {tableName} (runId={context.RunId})");
        Assertions.GreaterThanOrEqual(2, response.Responses[tableName].Count,
            $"BatchGetItem: expected >= 2 items (runId={context.RunId})");
    }

    // ---- dynamodb-txn ----

    private async Task TransactWriteItemsAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTxnTable");
        await clients.DynamoDB().TransactWriteItemsAsync(new TransactWriteItemsRequest
        {
            TransactItems =
            [
                new TransactWriteItem
                {
                    Put = new Put
                    {
                        TableName = tableName,
                        Item = Item("txn-1", "meta", "first"),
                    },
                },
                new TransactWriteItem
                {
                    Put = new Put
                    {
                        TableName = tableName,
                        Item = Item("txn-2", "meta", "second"),
                    },
                },
            ],
        });

        var get1 = await clients.DynamoDB().GetItemAsync(new GetItemRequest
        {
            TableName = tableName,
            Key = Key("txn-1", "meta"),
        });
        Assertions.NotNull(get1.Item, "TransactWriteItems: txn-1 not found");
        Assertions.Equal("first", get1.Item["value"].S,
            $"TransactWriteItems: txn-1 value mismatch (runId={context.RunId})");
    }

    private async Task TransactGetItemsAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTxnTable");
        var response = await clients.DynamoDB().TransactGetItemsAsync(new TransactGetItemsRequest
        {
            TransactItems =
            [
                new TransactGetItem
                {
                    Get = new Get
                    {
                        TableName = tableName,
                        Key = Key("txn-1", "meta"),
                    },
                },
                new TransactGetItem
                {
                    Get = new Get
                    {
                        TableName = tableName,
                        Key = Key("txn-2", "meta"),
                    },
                },
            ],
        });
        Assertions.GreaterThanOrEqual(2, response.Responses.Count,
            $"TransactGetItems: expected >= 2 responses (runId={context.RunId})");
    }

    private async Task TransactWriteConditionFailAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTxnTable");
        try
        {
            await clients.DynamoDB().TransactWriteItemsAsync(new TransactWriteItemsRequest
            {
                TransactItems =
                [
                    new TransactWriteItem
                    {
                        Put = new Put
                        {
                            TableName = tableName,
                            Item = Item("txn-1", "meta", "overwrite"),
                            ConditionExpression = "attribute_not_exists(pk)",
                        },
                    },
                ],
            });
            throw new InvalidOperationException(
                $"TransactWriteConditionFail: expected TransactionCanceledException but call succeeded (runId={context.RunId})");
        }
        catch (TransactionCanceledException)
        {
        }
    }

    // ---- dynamodb-ttl ----

    private async Task UpdateTimeToLiveAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTtlTable");
        await clients.DynamoDB().UpdateTimeToLiveAsync(new UpdateTimeToLiveRequest
        {
            TableName = tableName,
            TimeToLiveSpecification = new TimeToLiveSpecification
            {
                AttributeName = "ttl",
                Enabled = true,
            },
        });

        var desc = await clients.DynamoDB().DescribeTimeToLiveAsync(new DescribeTimeToLiveRequest
        {
            TableName = tableName,
        });
        Assertions.NotNull(desc.TimeToLiveDescription, "UpdateTimeToLive: TimeToLiveDescription");
        Assertions.Equal(TimeToLiveStatus.ENABLED, desc.TimeToLiveDescription.TimeToLiveStatus,
            $"UpdateTimeToLive: expected ENABLED but was {desc.TimeToLiveDescription.TimeToLiveStatus} (runId={context.RunId})");
        Assertions.Equal("ttl", desc.TimeToLiveDescription.AttributeName,
            $"UpdateTimeToLive: expected ttl attribute but was {desc.TimeToLiveDescription.AttributeName} (runId={context.RunId})");
    }

    private async Task DescribeTimeToLiveAsync(TestContext context)
    {
        var tableName = RequireTable(context, "dynamodbTtlTable");
        var response = await clients.DynamoDB().DescribeTimeToLiveAsync(new DescribeTimeToLiveRequest
        {
            TableName = tableName,
        });
        Assertions.NotNull(response.TimeToLiveDescription, "DescribeTimeToLive: TimeToLiveDescription");
        Assertions.Equal("ttl", response.TimeToLiveDescription.AttributeName,
            $"DescribeTimeToLive: expected ttl attribute but was {response.TimeToLiveDescription.AttributeName} (runId={context.RunId})");
    }
}
