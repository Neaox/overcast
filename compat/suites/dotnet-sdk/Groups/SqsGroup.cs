using Amazon.SQS.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class SqsGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["CreateQueue"] = CreateQueueAsync,
        ["GetQueueUrl"] = GetQueueUrlAsync,
        ["ListQueues"] = ListQueuesAsync,
        ["SetQueueAttributes"] = SetQueueAttributesAsync,
        ["GetQueueAttributes"] = GetQueueAttributesAsync,
        ["TagQueue"] = TagQueueAsync,
        ["UntagQueue"] = UntagQueueAsync,
        ["DeleteQueue"] = DeleteQueueAsync,
        ["SendMessage"] = SendMessageAsync,
        ["SendMessageBatch"] = SendMessageBatchAsync,
        ["ReceiveMessage"] = ReceiveMessageAsync,
        ["DeleteMessage"] = DeleteMessageAsync,
        ["ChangeMessageVisibility"] = ChangeMessageVisibilityAsync,
        ["DeleteMessageBatch"] = DeleteMessageBatchAsync,
        ["PurgeQueue"] = PurgeQueueAsync,
        ["CreateDLQ"] = CreateDLQAsync,
        ["SetRedrivePolicy"] = SetRedrivePolicyAsync,
        ["GetRedrivePolicy"] = GetRedrivePolicyAsync,
        ["CreateFifoQueue"] = CreateFifoQueueAsync,
        ["SendFifoMessage"] = SendFifoMessageAsync,
        ["ReceiveFifoMessage"] = ReceiveFifoMessageAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["sqs-queues"] = SetupQueuesAsync,
        ["sqs-messages"] = SetupMessagesAsync,
        ["sqs-dlq"] = SetupDlqAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["sqs-queues"] = TeardownQueuesAsync,
        ["sqs-messages"] = TeardownMessagesAsync,
        ["sqs-dlq"] = TeardownDlqAsync,
        ["sqs-fifo"] = TeardownFifoAsync,
    };

    // ── sqs-queues ──

    private async Task SetupQueuesAsync(TestContext context)
    {
        var name = $"{context.RunId}-sqs-q";
        var response = await clients.SQS().CreateQueueAsync(new CreateQueueRequest { QueueName = name });
        context.Set("sqsQueueUrl", response.QueueUrl);
    }

    private async Task CreateQueueAsync(TestContext context)
    {
        var name = $"{context.RunId}-sqs-create-q";
        var response = await clients.SQS().CreateQueueAsync(new CreateQueueRequest { QueueName = name });
        var url = response.QueueUrl;
        Assertions.NotBlank(url, "CreateQueue: url");
        try
        {
            var list = await clients.SQS().ListQueuesAsync(new ListQueuesRequest { QueueNamePrefix = name });
            Assertions.True(list.QueueUrls.Any(u => u == url), $"CreateQueue: queue {name} not found in ListQueues (runId={context.RunId})");
        }
        finally
        {
            try { await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = url }); } catch { }
        }
    }

    private async Task GetQueueUrlAsync(TestContext context)
    {
        var name = $"{context.RunId}-sqs-q";
        var response = await clients.SQS().GetQueueUrlAsync(new GetQueueUrlRequest { QueueName = name });
        Assertions.NotBlank(response.QueueUrl, "GetQueueUrl: url");
    }

    private async Task ListQueuesAsync(TestContext context)
    {
        var storedUrl = RequireQueueUrl(context, "sqsQueueUrl");
        var response = await clients.SQS().ListQueuesAsync(new ListQueuesRequest { QueueNamePrefix = $"{context.RunId}-sqs-q" });
        Assertions.True(response.QueueUrls.Any(u => u == storedUrl), $"ListQueues: queue URL not found (runId={context.RunId})");
    }

    private async Task SetQueueAttributesAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsQueueUrl");
        await clients.SQS().SetQueueAttributesAsync(new SetQueueAttributesRequest
        {
            QueueUrl = url,
            Attributes = new Dictionary<string, string> { ["VisibilityTimeout"] = "120" },
        });
        var attrs = await clients.SQS().GetQueueAttributesAsync(new GetQueueAttributesRequest
        {
            QueueUrl = url,
            AttributeNames = ["VisibilityTimeout"],
        });
        Assertions.Equal("120", attrs.Attributes["VisibilityTimeout"], "SetQueueAttributes: VisibilityTimeout");
    }

    private async Task GetQueueAttributesAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsQueueUrl");
        var response = await clients.SQS().GetQueueAttributesAsync(new GetQueueAttributesRequest
        {
            QueueUrl = url,
            AttributeNames = ["All"],
        });
        Assertions.True(response.Attributes.Count > 0, "GetQueueAttributes: expected non-empty attributes");
        Assertions.Equal("120", response.Attributes["VisibilityTimeout"], "GetQueueAttributes: VisibilityTimeout");
    }

    private async Task TagQueueAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsQueueUrl");
        await clients.SQS().TagQueueAsync(new TagQueueRequest
        {
            QueueUrl = url,
            Tags = new Dictionary<string, string> { ["Environment"] = "compat", ["Team"] = "dotnet" },
        });
        var tags = await clients.SQS().ListQueueTagsAsync(new ListQueueTagsRequest { QueueUrl = url });
        Assertions.Equal("compat", tags.Tags["Environment"], "TagQueue: Environment tag");
    }

    private async Task UntagQueueAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsQueueUrl");
        await clients.SQS().UntagQueueAsync(new UntagQueueRequest
        {
            QueueUrl = url,
            TagKeys = ["Environment", "Team"],
        });
        var tags = await clients.SQS().ListQueueTagsAsync(new ListQueueTagsRequest { QueueUrl = url });
        Assertions.True(tags.Tags.Count == 0, "UntagQueue: expected empty tags after untag");
    }

    private async Task DeleteQueueAsync(TestContext context)
    {
        var name = $"{context.RunId}-sqs-del-q";
        var create = await clients.SQS().CreateQueueAsync(new CreateQueueRequest { QueueName = name });
        var url = create.QueueUrl;
        await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = url });
        var list = await clients.SQS().ListQueuesAsync(new ListQueuesRequest { QueueNamePrefix = name });
        Assertions.True(list.QueueUrls.Count == 0, $"DeleteQueue: queue {name} still present (runId={context.RunId})");
    }

    private async Task TeardownQueuesAsync(TestContext context)
    {
        var url = context.GetString("sqsQueueUrl");
        if (!string.IsNullOrWhiteSpace(url))
        {
            try { await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = url }); } catch { }
        }
    }

    // ── sqs-messages ──

    private async Task SetupMessagesAsync(TestContext context)
    {
        var name = $"{context.RunId}-sqs-msg";
        var response = await clients.SQS().CreateQueueAsync(new CreateQueueRequest { QueueName = name });
        context.Set("sqsMsgUrl", response.QueueUrl);
    }

    private async Task SendMessageAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsMsgUrl");
        var response = await clients.SQS().SendMessageAsync(new SendMessageRequest
        {
            QueueUrl = url,
            MessageBody = "hello from dotnet-sdk",
        });
        Assertions.NotBlank(response.MessageId, "SendMessage: MessageId");
    }

    private async Task SendMessageBatchAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsMsgUrl");
        var response = await clients.SQS().SendMessageBatchAsync(new SendMessageBatchRequest
        {
            QueueUrl = url,
            Entries =
            [
                new SendMessageBatchRequestEntry { Id = "a", MessageBody = "batch-a" },
                new SendMessageBatchRequestEntry { Id = "b", MessageBody = "batch-b" },
            ],
        });
        Assertions.GreaterThanOrEqual(2, response.Successful.Count, "SendMessageBatch: expected >= 2 successful");
        Assertions.NotBlank(response.Successful[0].MessageId, "SendMessageBatch: MessageId[0]");
    }

    private async Task ReceiveMessageAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsMsgUrl");
        await clients.SQS().SendMessageAsync(new SendMessageRequest { QueueUrl = url, MessageBody = "receive-test" });
        List<Message> messages = new();
        for (var i = 0; i < 5; i++)
        {
            var response = await clients.SQS().ReceiveMessageAsync(new ReceiveMessageRequest
            {
                QueueUrl = url,
                MaxNumberOfMessages = 10,
                WaitTimeSeconds = 1,
            });
            messages = response.Messages;
            if (messages.Count > 0)
            {
                break;
            }
        }
        Assertions.True(messages.Count > 0, "ReceiveMessage: expected at least 1 message");
    }

    private async Task DeleteMessageAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsMsgUrl");
        await clients.SQS().SendMessageAsync(new SendMessageRequest { QueueUrl = url, MessageBody = "delete-me" });
        var recv = await clients.SQS().ReceiveMessageAsync(new ReceiveMessageRequest
        {
            QueueUrl = url,
            MaxNumberOfMessages = 1,
            WaitTimeSeconds = 3,
        });
        Assertions.True(recv.Messages.Count > 0, "DeleteMessage: expected at least 1 message");
        var handle = recv.Messages[0].ReceiptHandle;
        Assertions.NotBlank(handle, "DeleteMessage: ReceiptHandle");
        await clients.SQS().DeleteMessageAsync(new DeleteMessageRequest { QueueUrl = url, ReceiptHandle = handle });
    }

    private async Task ChangeMessageVisibilityAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsMsgUrl");
        await clients.SQS().SendMessageAsync(new SendMessageRequest { QueueUrl = url, MessageBody = "visibility-test" });
        var recv = await clients.SQS().ReceiveMessageAsync(new ReceiveMessageRequest
        {
            QueueUrl = url,
            MaxNumberOfMessages = 1,
            WaitTimeSeconds = 3,
        });
        Assertions.True(recv.Messages.Count > 0, "ChangeMessageVisibility: expected at least 1 message");
        await clients.SQS().ChangeMessageVisibilityAsync(new ChangeMessageVisibilityRequest
        {
            QueueUrl = url,
            ReceiptHandle = recv.Messages[0].ReceiptHandle,
            VisibilityTimeout = 30,
        });
    }

    private async Task DeleteMessageBatchAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsMsgUrl");
        await clients.SQS().SendMessageAsync(new SendMessageRequest { QueueUrl = url, MessageBody = "batch-delete-a" });
        await clients.SQS().SendMessageAsync(new SendMessageRequest { QueueUrl = url, MessageBody = "batch-delete-b" });
        var recv = await clients.SQS().ReceiveMessageAsync(new ReceiveMessageRequest
        {
            QueueUrl = url,
            MaxNumberOfMessages = 10,
            WaitTimeSeconds = 3,
        });
        Assertions.GreaterThanOrEqual(2, recv.Messages.Count, "DeleteMessageBatch: expected >= 2 messages");
        var batchResp = await clients.SQS().DeleteMessageBatchAsync(new DeleteMessageBatchRequest
        {
            QueueUrl = url,
            Entries = recv.Messages.Select((msg, idx) => new DeleteMessageBatchRequestEntry
            {
                Id = (idx + 1).ToString(),
                ReceiptHandle = msg.ReceiptHandle,
            }).ToList(),
        });
        Assertions.GreaterThanOrEqual(2, batchResp.Successful.Count, "DeleteMessageBatch: expected >= 2 successful deletes");
    }

    private async Task PurgeQueueAsync(TestContext context)
    {
        var url = RequireQueueUrl(context, "sqsMsgUrl");
        await clients.SQS().SendMessageAsync(new SendMessageRequest { QueueUrl = url, MessageBody = "purge-test" });
        await clients.SQS().PurgeQueueAsync(new PurgeQueueRequest { QueueUrl = url });
        var recv = await clients.SQS().ReceiveMessageAsync(new ReceiveMessageRequest
        {
            QueueUrl = url,
            MaxNumberOfMessages = 10,
            WaitTimeSeconds = 0,
        });
        Assertions.Equal(0, recv.Messages.Count, "PurgeQueue: expected no messages after purge");
    }

    private async Task TeardownMessagesAsync(TestContext context)
    {
        var url = context.GetString("sqsMsgUrl");
        if (!string.IsNullOrWhiteSpace(url))
        {
            try { await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = url }); } catch { }
        }
    }

    // ── sqs-dlq ──

    private async Task SetupDlqAsync(TestContext context)
    {
        var srcName = $"{context.RunId}-sqs-src";
        var dlqName = $"{context.RunId}-sqs-dlq";
        var srcResponse = await clients.SQS().CreateQueueAsync(new CreateQueueRequest { QueueName = srcName });
        var dlqResponse = await clients.SQS().CreateQueueAsync(new CreateQueueRequest { QueueName = dlqName });
        context.Set("sqsSrcUrl", srcResponse.QueueUrl);
        context.Set("sqsDlqUrl", dlqResponse.QueueUrl);
    }

    private async Task CreateDLQAsync(TestContext context)
    {
        var srcUrl = context.GetString("sqsSrcUrl") ?? throw new InvalidOperationException("sqsSrcUrl not set");
        var dlqUrl = context.GetString("sqsDlqUrl") ?? throw new InvalidOperationException("sqsDlqUrl not set");
        var list = await clients.SQS().ListQueuesAsync(new ListQueuesRequest { QueueNamePrefix = $"{context.RunId}-sqs-src" });
        Assertions.True(list.QueueUrls.Any(u => u == srcUrl), $"CreateDLQ: src queue not found (runId={context.RunId})");
        var listDlq = await clients.SQS().ListQueuesAsync(new ListQueuesRequest { QueueNamePrefix = $"{context.RunId}-sqs-dlq" });
        Assertions.True(listDlq.QueueUrls.Any(u => u == dlqUrl), $"CreateDLQ: dlq queue not found (runId={context.RunId})");
    }

    private async Task SetRedrivePolicyAsync(TestContext context)
    {
        var srcUrl = context.GetString("sqsSrcUrl") ?? throw new InvalidOperationException("sqsSrcUrl not set");
        var dlqUrl = context.GetString("sqsDlqUrl") ?? throw new InvalidOperationException("sqsDlqUrl not set");
        var dlqAttrs = await clients.SQS().GetQueueAttributesAsync(new GetQueueAttributesRequest
        {
            QueueUrl = dlqUrl,
            AttributeNames = ["QueueArn"],
        });
        var dlqArn = dlqAttrs.Attributes["QueueArn"];
        var policy = $"{{\"maxReceiveCount\":\"3\",\"deadLetterTargetArn\":\"{dlqArn}\"}}";
        await clients.SQS().SetQueueAttributesAsync(new SetQueueAttributesRequest
        {
            QueueUrl = srcUrl,
            Attributes = new Dictionary<string, string> { ["RedrivePolicy"] = policy },
        });
        var srcAttrs = await clients.SQS().GetQueueAttributesAsync(new GetQueueAttributesRequest
        {
            QueueUrl = srcUrl,
            AttributeNames = ["RedrivePolicy"],
        });
        Assertions.True(srcAttrs.Attributes.ContainsKey("RedrivePolicy"), "SetRedrivePolicy: RedrivePolicy not found");
        Assertions.True(srcAttrs.Attributes["RedrivePolicy"].Contains("deadLetterTargetArn"), "SetRedrivePolicy: missing deadLetterTargetArn");
    }

    private async Task GetRedrivePolicyAsync(TestContext context)
    {
        var srcUrl = context.GetString("sqsSrcUrl") ?? throw new InvalidOperationException("sqsSrcUrl not set");
        var attrs = await clients.SQS().GetQueueAttributesAsync(new GetQueueAttributesRequest
        {
            QueueUrl = srcUrl,
            AttributeNames = ["RedrivePolicy"],
        });
        Assertions.True(attrs.Attributes.ContainsKey("RedrivePolicy"), "GetRedrivePolicy: RedrivePolicy not found");
        Assertions.True(attrs.Attributes["RedrivePolicy"].Contains("maxReceiveCount"), "GetRedrivePolicy: missing maxReceiveCount");
    }

    private async Task TeardownDlqAsync(TestContext context)
    {
        var srcUrl = context.GetString("sqsSrcUrl");
        if (!string.IsNullOrWhiteSpace(srcUrl))
        {
            try { await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = srcUrl }); } catch { }
        }
        var dlqUrl = context.GetString("sqsDlqUrl");
        if (!string.IsNullOrWhiteSpace(dlqUrl))
        {
            try { await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = dlqUrl }); } catch { }
        }
    }

    // ── sqs-fifo ──

    private async Task CreateFifoQueueAsync(TestContext context)
    {
        var name = $"{context.RunId}-sqs-fifo.fifo";
        var response = await clients.SQS().CreateQueueAsync(new CreateQueueRequest
        {
            QueueName = name,
            Attributes = new Dictionary<string, string> { ["FifoQueue"] = "true" },
        });
        var url = response.QueueUrl;
        Assertions.NotBlank(url, "CreateFifoQueue: url");
        context.Set("sqsFifoUrl", url);
    }

    private async Task SendFifoMessageAsync(TestContext context)
    {
        var url = context.GetString("sqsFifoUrl") ?? throw new InvalidOperationException("sqsFifoUrl not set");
        var response = await clients.SQS().SendMessageAsync(new SendMessageRequest
        {
            QueueUrl = url,
            MessageBody = "fifo-message",
            MessageGroupId = "test-group",
            MessageDeduplicationId = $"{context.RunId}-dedup-1",
        });
        Assertions.NotBlank(response.MessageId, "SendFifoMessage: MessageId");
    }

    private async Task ReceiveFifoMessageAsync(TestContext context)
    {
        var url = context.GetString("sqsFifoUrl") ?? throw new InvalidOperationException("sqsFifoUrl not set");
        List<Message> messages = new();
        for (var i = 0; i < 5; i++)
        {
            var response = await clients.SQS().ReceiveMessageAsync(new ReceiveMessageRequest
            {
                QueueUrl = url,
                MaxNumberOfMessages = 10,
                WaitTimeSeconds = 1,
            });
            messages = response.Messages;
            if (messages.Count > 0)
            {
                break;
            }
        }
        Assertions.True(messages.Count > 0, "ReceiveFifoMessage: expected at least 1 message");
    }

    private async Task TeardownFifoAsync(TestContext context)
    {
        var url = context.GetString("sqsFifoUrl");
        if (!string.IsNullOrWhiteSpace(url))
        {
            try { await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = url }); } catch { }
        }
    }

    private static string RequireQueueUrl(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }
}
