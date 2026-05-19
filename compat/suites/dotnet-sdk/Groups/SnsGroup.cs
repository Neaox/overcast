using Amazon.SimpleNotificationService.Model;
using Amazon.SQS.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class SnsGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["CreateTopic"] = CreateTopicAsync,
        ["ListTopics"] = ListTopicsAsync,
        ["GetTopicAttributes"] = GetTopicAttributesAsync,
        ["SetTopicAttributes"] = SetTopicAttributesAsync,
        ["DeleteTopic"] = DeleteTopicAsync,
        ["Publish"] = PublishAsync,
        ["PublishWithAttributes"] = PublishWithAttributesAsync,
        ["PublishBatch"] = PublishBatchAsync,
        ["SubscribeSQS"] = SubscribeSQSAsync,
        ["ListSubscriptionsByTopic"] = ListSubscriptionsByTopicAsync,
        ["GetSubscriptionAttributes"] = GetSubscriptionAttributesAsync,
        ["PublishDeliveredToSQS"] = PublishDeliveredToSQSAsync,
        ["SetSubscriptionAttributes"] = SetSubscriptionAttributesAsync,
        ["Unsubscribe"] = UnsubscribeAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["sns-publish"] = SetupPublishAsync,
        ["sns-subscriptions"] = SetupSubscriptionsAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["sns-topics"] = TeardownTopicsAsync,
        ["sns-publish"] = TeardownPublishAsync,
        ["sns-subscriptions"] = TeardownSubscriptionsAsync,
    };

    // ── sns-topics ──

    private async Task CreateTopicAsync(TestContext context)
    {
        var name = $"{context.RunId}-sns-topics";
        var response = await clients.SNS().CreateTopicAsync(new CreateTopicRequest { Name = name });
        var arn = response.TopicArn;
        Assertions.NotBlank(arn, "CreateTopic: TopicArn");
        context.Set("snsTopicArn", arn);
        var topics = await clients.SNS().ListTopicsAsync(new ListTopicsRequest());
        Assertions.True(topics.Topics.Any(t => t.TopicArn == arn), $"CreateTopic: topic {name} not found in ListTopics (runId={context.RunId})");
    }

    private async Task ListTopicsAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsTopicArn");
        var response = await clients.SNS().ListTopicsAsync(new ListTopicsRequest());
        Assertions.True(response.Topics.Any(t => t.TopicArn == topicArn), $"ListTopics: topic {topicArn} not found (runId={context.RunId})");
    }

    private async Task GetTopicAttributesAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsTopicArn");
        var response = await clients.SNS().GetTopicAttributesAsync(new GetTopicAttributesRequest { TopicArn = topicArn });
        Assertions.True(response.Attributes.Count > 0, "GetTopicAttributes: expected non-empty attributes");
        Assertions.NotBlank(response.Attributes["TopicArn"], "GetTopicAttributes: TopicArn");
    }

    private async Task SetTopicAttributesAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsTopicArn");
        await clients.SNS().SetTopicAttributesAsync(new SetTopicAttributesRequest
        {
            TopicArn = topicArn,
            AttributeName = "DisplayName",
            AttributeValue = "compat-display",
        });
        var attrs = await clients.SNS().GetTopicAttributesAsync(new GetTopicAttributesRequest { TopicArn = topicArn });
        Assertions.Equal("compat-display", attrs.Attributes["DisplayName"], "SetTopicAttributes: DisplayName");
    }

    private async Task DeleteTopicAsync(TestContext context)
    {
        var name = $"{context.RunId}-sns-del";
        var create = await clients.SNS().CreateTopicAsync(new CreateTopicRequest { Name = name });
        var arn = create.TopicArn;
        await clients.SNS().DeleteTopicAsync(new DeleteTopicRequest { TopicArn = arn });
        var topics = await clients.SNS().ListTopicsAsync(new ListTopicsRequest());
        Assertions.False(topics.Topics.Any(t => t.TopicArn == arn), $"DeleteTopic: topic {name} still present (runId={context.RunId})");
    }

    private async Task TeardownTopicsAsync(TestContext context)
    {
        var prefix = $"{context.RunId}-sns";
        await DeleteTopicByPrefixAsync(prefix);
    }

    // ── sns-publish ──

    private async Task SetupPublishAsync(TestContext context)
    {
        var name = $"{context.RunId}-sns-pub";
        var response = await clients.SNS().CreateTopicAsync(new CreateTopicRequest { Name = name });
        context.Set("snsPubTopicArn", response.TopicArn);
    }

    private async Task PublishAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsPubTopicArn");
        var response = await clients.SNS().PublishAsync(new PublishRequest
        {
            TopicArn = topicArn,
            Message = "hello from dotnet-sdk",
            Subject = "dotnet-test",
        });
        Assertions.NotBlank(response.MessageId, "Publish: MessageId");
    }

    private async Task PublishWithAttributesAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsPubTopicArn");
        var response = await clients.SNS().PublishAsync(new PublishRequest
        {
            TopicArn = topicArn,
            Message = "message with attrs",
            MessageAttributes = new Dictionary<string, Amazon.SimpleNotificationService.Model.MessageAttributeValue>
            {
                ["color"] = new Amazon.SimpleNotificationService.Model.MessageAttributeValue { DataType = "String", StringValue = "red" },
                ["size"] = new Amazon.SimpleNotificationService.Model.MessageAttributeValue { DataType = "Number", StringValue = "42" },
            },
        });
        Assertions.NotBlank(response.MessageId, "PublishWithAttributes: MessageId");
    }

    private async Task PublishBatchAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsPubTopicArn");
        var response = await clients.SNS().PublishBatchAsync(new PublishBatchRequest
        {
            TopicArn = topicArn,
            PublishBatchRequestEntries =
            [
                new PublishBatchRequestEntry { Id = "1", Message = "batch-1" },
                new PublishBatchRequestEntry { Id = "2", Message = "batch-2" },
            ],
        });
        Assertions.GreaterThanOrEqual(2, response.Successful.Count, "PublishBatch: expected >= 2 successful");
        Assertions.True(response.Failed.Count == 0, "PublishBatch: expected 0 failed");
    }

    private async Task TeardownPublishAsync(TestContext context)
    {
        var prefix = $"{context.RunId}-sns-pub";
        await DeleteTopicByPrefixAsync(prefix);
    }

    // ── sns-subscriptions ──

    private async Task SetupSubscriptionsAsync(TestContext context)
    {
        var topicName = $"{context.RunId}-sns-sub";
        var queueName = $"{context.RunId}-sns-sub-q";

        var topicResponse = await clients.SNS().CreateTopicAsync(new CreateTopicRequest { Name = topicName });
        context.Set("snsSubTopicArn", topicResponse.TopicArn);

        var queueResponse = await clients.SQS().CreateQueueAsync(new CreateQueueRequest { QueueName = queueName });
        context.Set("snsSubQueueUrl", queueResponse.QueueUrl);
    }

    private async Task SubscribeSQSAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsSubTopicArn");
        var queueUrl = RequireQueueUrl(context, "snsSubQueueUrl");

        var qAttrs = await clients.SQS().GetQueueAttributesAsync(new GetQueueAttributesRequest
        {
            QueueUrl = queueUrl,
            AttributeNames = ["QueueArn"],
        });
        var queueArn = qAttrs.Attributes["QueueArn"];

        var response = await clients.SNS().SubscribeAsync(new SubscribeRequest
        {
            TopicArn = topicArn,
            Protocol = "sqs",
            Endpoint = queueArn,
        });
        var subArn = response.SubscriptionArn;
        Assertions.NotBlank(subArn, "SubscribeSQS: SubscriptionArn");
        context.Set("snsSubArn", subArn);
    }

    private async Task ListSubscriptionsByTopicAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsSubTopicArn");
        var subArn = RequireSubArn(context);

        var response = await clients.SNS().ListSubscriptionsByTopicAsync(new ListSubscriptionsByTopicRequest
        {
            TopicArn = topicArn,
        });
        Assertions.True(response.Subscriptions.Any(s => s.SubscriptionArn == subArn), $"ListSubscriptionsByTopic: subscription {subArn} not found (runId={context.RunId})");
    }

    private async Task GetSubscriptionAttributesAsync(TestContext context)
    {
        var subArn = RequireSubArn(context);
        var response = await clients.SNS().GetSubscriptionAttributesAsync(new GetSubscriptionAttributesRequest
        {
            SubscriptionArn = subArn,
        });
        Assertions.True(response.Attributes.Count > 0, "GetSubscriptionAttributes: expected non-empty attributes");
        Assertions.NotBlank(response.Attributes["Protocol"], "GetSubscriptionAttributes: Protocol");
    }

    private async Task PublishDeliveredToSQSAsync(TestContext context)
    {
        var topicArn = RequireTopicArn(context, "snsSubTopicArn");
        var queueUrl = RequireQueueUrl(context, "snsSubQueueUrl");

        var pub = await clients.SNS().PublishAsync(new PublishRequest
        {
            TopicArn = topicArn,
            Message = "delivery-test",
        });
        Assertions.NotBlank(pub.MessageId, "PublishDeliveredToSQS: MessageId");

        List<Message> messages = new();
        for (var i = 0; i < 10; i++)
        {
            var recv = await clients.SQS().ReceiveMessageAsync(new ReceiveMessageRequest
            {
                QueueUrl = queueUrl,
                MaxNumberOfMessages = 10,
                WaitTimeSeconds = 1,
            });
            messages = recv.Messages;
            if (messages.Count > 0)
            {
                break;
            }
        }
        Assertions.True(messages.Count > 0, "PublishDeliveredToSQS: expected at least 1 message delivered via SNS->SQS");
    }

    private async Task SetSubscriptionAttributesAsync(TestContext context)
    {
        var subArn = RequireSubArn(context);
        await clients.SNS().SetSubscriptionAttributesAsync(new SetSubscriptionAttributesRequest
        {
            SubscriptionArn = subArn,
            AttributeName = "RawMessageDelivery",
            AttributeValue = "true",
        });
        var attrs = await clients.SNS().GetSubscriptionAttributesAsync(new GetSubscriptionAttributesRequest
        {
            SubscriptionArn = subArn,
        });
        Assertions.Equal("true", attrs.Attributes["RawMessageDelivery"], "SetSubscriptionAttributes: RawMessageDelivery");
    }

    private async Task UnsubscribeAsync(TestContext context)
    {
        var subArn = RequireSubArn(context);
        await clients.SNS().UnsubscribeAsync(new UnsubscribeRequest { SubscriptionArn = subArn });
        // Clear the subArn so teardown does not try to unsubscribe again
        context.Set("snsSubArn", null);
    }

    private async Task TeardownSubscriptionsAsync(TestContext context)
    {
        // Unsubscribe if still active (not already unsubscribed by the Unsubscribe test)
        var subArn = context.GetString("snsSubArn");
        if (!string.IsNullOrWhiteSpace(subArn))
        {
            try { await clients.SNS().UnsubscribeAsync(new UnsubscribeRequest { SubscriptionArn = subArn }); } catch { }
        }

        // Delete the topic
        var topicPrefix = $"{context.RunId}-sns-sub";
        await DeleteTopicByPrefixAsync(topicPrefix);

        // Delete the SQS queue
        var queueUrl = context.GetString("snsSubQueueUrl");
        if (!string.IsNullOrWhiteSpace(queueUrl))
        {
            try { await clients.SQS().PurgeQueueAsync(new PurgeQueueRequest { QueueUrl = queueUrl }); } catch { }
            try { await clients.SQS().DeleteQueueAsync(new DeleteQueueRequest { QueueUrl = queueUrl }); } catch { }
        }
    }

    // ── helpers ──

    private async Task DeleteTopicByPrefixAsync(string prefix)
    {
        var response = await clients.SNS().ListTopicsAsync(new ListTopicsRequest());
        foreach (var topic in response.Topics)
        {
            if (topic.TopicArn != null && topic.TopicArn.Contains(prefix, StringComparison.OrdinalIgnoreCase))
            {
                try { await clients.SNS().DeleteTopicAsync(new DeleteTopicRequest { TopicArn = topic.TopicArn }); } catch { }
            }
        }
    }

    private static string RequireTopicArn(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }

    private static string RequireQueueUrl(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }

    private static string RequireSubArn(TestContext context)
    {
        return context.GetString("snsSubArn") ?? throw new InvalidOperationException("snsSubArn not set");
    }
}
