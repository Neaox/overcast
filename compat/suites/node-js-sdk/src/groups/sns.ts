/**
 * groups/sns.ts — SNS compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   sns-topics        — topic lifecycle (implemented)
 *   sns-publish       — Publish, PublishBatch (implemented)
 *   sns-subscriptions — Subscribe, Unsubscribe, SQS delivery (implemented)
 *   sns-attributes    — topic/subscription attributes (partially implemented)
 */

import {
  CreateTopicCommand,
  DeleteTopicCommand,
  ListTopicsCommand,
  GetTopicAttributesCommand,
  SetTopicAttributesCommand,
  PublishCommand,
  PublishBatchCommand,
  SubscribeCommand,
  UnsubscribeCommand,
  ListSubscriptionsByTopicCommand,
  GetSubscriptionAttributesCommand,
  SetSubscriptionAttributesCommand,
} from "@aws-sdk/client-sns";
import {
  CreateQueueCommand,
  DeleteQueueCommand,
  GetQueueUrlCommand,
  GetQueueAttributesCommand,
  ReceiveMessageCommand,
  PurgeQueueCommand,
} from "@aws-sdk/client-sqs";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeSNSGroups(suite: string): TestGroup[] {
  return [
    // ── sns-topics ─────────────────────────────────────────────────────────
    {
      suite,
      service: "sns",
      name: "sns-topics",
      tests: [
        {
          name: "CreateTopic",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const resp = await sns.send(
              new CreateTopicCommand({ Name: `${ctx.runId}-sns-topic` }),
            );
            assert.ok(resp.TopicArn, "CreateTopic: missing TopicArn");
          },
        },
        {
          name: "ListTopics",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const resp = await sns.send(new ListTopicsCommand({}));
            const topicName = `${ctx.runId}-sns-topic`;
            assert.ok(resp.Topics?.some((t) => t.TopicArn?.includes(topicName)), `ListTopics: ${topicName} not found`);
          },
        },
        {
          name: "GetTopicAttributes",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-topic`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "GetTopicAttributes: topic not found");
            const resp = await sns.send(
              new GetTopicAttributesCommand({ TopicArn: arn }),
            );
            assert.ok(resp.Attributes?.TopicArn, "GetTopicAttributes: missing TopicArn attribute");
          },
        },
        {
          name: "SetTopicAttributes",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-topic`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "SetTopicAttributes: topic not found");
            await sns.send(
              new SetTopicAttributesCommand({
                TopicArn: arn,
                AttributeName: "DisplayName",
                AttributeValue: "Overcast Test Topic",
              }),
            );
            const resp = await sns.send(
              new GetTopicAttributesCommand({ TopicArn: arn }),
            );
            assert.strictEqual(resp.Attributes?.DisplayName, "Overcast Test Topic", `SetTopicAttributes: expected DisplayName="Overcast Test Topic", got "${resp.Attributes?.DisplayName}"`);
          },
        },
        {
          name: "DeleteTopic",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-topic`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "DeleteTopic: topic not found");
            await sns.send(new DeleteTopicCommand({ TopicArn: arn }));
            const after = await sns.send(new ListTopicsCommand({}));
            assert.notStrictEqual(after.Topics?.some((t) => t.TopicArn, arn), "DeleteTopic: topic still present after delete");
          },
        },
      ],
      teardown: async (ctx) => {
        const { sns } = makeClients(ctx);
        try {
          const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
          const arn = Topics.find((t) =>
            t.TopicArn?.includes(`${ctx.runId}-sns-topic`),
          )?.TopicArn;
          if (arn) await sns.send(new DeleteTopicCommand({ TopicArn: arn }));
        } catch {}
      },
    },

    // ── sns-publish ────────────────────────────────────────────────────────
    {
      suite,
      service: "sns",
      name: "sns-publish",
      tests: [
        {
          name: "Publish",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-pub`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "Publish: topic not found");
            const resp = await sns.send(
              new PublishCommand({
                TopicArn: arn,
                Message: "hello from overcast compat",
              }),
            );
            assert.ok(resp.MessageId, "Publish: missing MessageId");
          },
        },
        {
          name: "PublishWithAttributes",
          op: "Publish",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-pub`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "PublishWithAttributes: topic not found");
            const resp = await sns.send(
              new PublishCommand({
                TopicArn: arn,
                Message: "structured message",
                Subject: "test subject",
                MessageAttributes: {
                  color: { DataType: "String", StringValue: "red" },
                  count: { DataType: "Number", StringValue: "42" },
                },
              }),
            );
            assert.ok(resp.MessageId, "PublishWithAttributes: missing MessageId");
          },
        },
        {
          name: "PublishBatch",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-pub`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "PublishBatch: topic not found");
            const resp = await sns.send(
              new PublishBatchCommand({
                TopicArn: arn,
                PublishBatchRequestEntries: [
                  { Id: "1", Message: "batch-msg-1" },
                  { Id: "2", Message: "batch-msg-2" },
                ],
              }),
            );
            assert.ok(((resp.Successful?.length ?? 0)) >= (2), `PublishBatch: expected 2 successful, got ${resp.Successful?.length}`);
          },
        },
      ],
      setup: async (ctx) => {
        const { sns } = makeClients(ctx);
        await sns.send(
          new CreateTopicCommand({ Name: `${ctx.runId}-sns-pub` }),
        );
      },
      teardown: async (ctx) => {
        const { sns } = makeClients(ctx);
        try {
          const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
          const arn = Topics.find((t) =>
            t.TopicArn?.includes(`${ctx.runId}-sns-pub`),
          )?.TopicArn;
          if (arn) await sns.send(new DeleteTopicCommand({ TopicArn: arn }));
        } catch {}
      },
    },

    // ── sns-subscriptions ──────────────────────────────────────────────────
    {
      suite,
      service: "sns",
      name: "sns-subscriptions",
      tests: [
        {
          name: "SubscribeSQS",
          fn: async (ctx) => {
            const { sns, sqs } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-sub`;
            const queueName = `${ctx.runId}-sns-sub-q`;

            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "SubscribeSQS: topic not found");

            const { QueueUrl } = await sqs.send(
              new GetQueueUrlCommand({ QueueName: queueName }),
            );
            const qAttrs = await sqs.send(
              new GetQueueAttributesCommand({
                QueueUrl: QueueUrl!,
                AttributeNames: ["QueueArn"],
              }),
            );
            const queueArn = qAttrs.Attributes?.QueueArn;
            assert.ok(queueArn, "SubscribeSQS: queue ARN not found");

            const resp = await sns.send(
              new SubscribeCommand({
                TopicArn: arn,
                Protocol: "sqs",
                Endpoint: queueArn,
              }),
            );
            assert.ok(resp.SubscriptionArn, "SubscribeSQS: missing SubscriptionArn");
            ctx["_subArn"] = resp.SubscriptionArn;
          },
        },
        {
          name: "ListSubscriptionsByTopic",
          fn: async (ctx) => {
            const { sns } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-sub`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "ListSubscriptionsByTopic: topic not found");
            const resp = await sns.send(
              new ListSubscriptionsByTopicCommand({ TopicArn: arn }),
            );
            assert.notStrictEqual((resp.Subscriptions?.length ?? 0), 0, "ListSubscriptionsByTopic: expected at least one subscription");
          },
        },
        {
          name: "GetSubscriptionAttributes",
          fn: async (ctx) => {
            const subArn = ctx["_subArn"] as string;
            assert.ok(subArn, "no SubscriptionArn from previous step");
            const { sns } = makeClients(ctx);
            const resp = await sns.send(
              new GetSubscriptionAttributesCommand({ SubscriptionArn: subArn }),
            );
            assert.strictEqual(resp.Attributes?.Protocol, "sqs", `GetSubscriptionAttributes: expected Protocol=sqs, got ${resp.Attributes?.Protocol}`);
          },
        },
        {
          name: "PublishDeliveredToSQS",
          op: "Publish",
          fn: async (ctx) => {
            const { sns, sqs } = makeClients(ctx);
            const topicName = `${ctx.runId}-sns-sub`;
            const queueName = `${ctx.runId}-sns-sub-q`;

            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const arn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            assert.ok(arn, "PublishDeliveredToSQS: topic not found");

            await sns.send(
              new PublishCommand({ TopicArn: arn, Message: "delivery-test" }),
            );

            const { QueueUrl } = await sqs.send(
              new GetQueueUrlCommand({ QueueName: queueName }),
            );

            // Poll up to 5 seconds for delivery
            let received = false;
            for (let i = 0; i < 10; i++) {
              const resp = await sqs.send(
                new ReceiveMessageCommand({
                  QueueUrl: QueueUrl!,
                  MaxNumberOfMessages: 10,
                  WaitTimeSeconds: 1,
                }),
              );
              if ((resp.Messages?.length ?? 0) > 0) {
                received = true;
                break;
              }
            }
            assert.ok(received, "PublishDeliveredToSQS: message not delivered to SQS");
          },
        },
        {
          name: "SetSubscriptionAttributes",
          fn: async (ctx) => {
            const subArn = ctx["_subArn"] as string;
            assert.ok(subArn, "no SubscriptionArn");
            const { sns } = makeClients(ctx);
            await sns.send(
              new SetSubscriptionAttributesCommand({
                SubscriptionArn: subArn,
                AttributeName: "RawMessageDelivery",
                AttributeValue: "true",
              }),
            );
            const { Attributes } = await sns.send(
              new GetSubscriptionAttributesCommand({ SubscriptionArn: subArn }),
            );
            assert.strictEqual(Attributes?.["RawMessageDelivery"], "true", `SetSubscriptionAttributes: expected RawMessageDelivery=true, got ${Attributes?.["RawMessageDelivery"]}`);
          },
        },
        {
          name: "Unsubscribe",
          fn: async (ctx) => {
            const subArn = ctx["_subArn"] as string;
            assert.ok(subArn, "no SubscriptionArn");
            const { sns } = makeClients(ctx);
            await sns.send(new UnsubscribeCommand({ SubscriptionArn: subArn }));
            const topicName = `${ctx.runId}-sns-sub`;
            const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
            const topicArn = Topics.find((t) =>
              t.TopicArn?.includes(topicName),
            )?.TopicArn;
            if (topicArn) {
              const { Subscriptions = [] } = await sns.send(
                new ListSubscriptionsByTopicCommand({ TopicArn: topicArn }),
              );
              const found = Subscriptions.some(
                (s) => s.SubscriptionArn === subArn,
              );
              assert.ok(!(found), `Unsubscribe: subscription ${subArn} still present after unsubscribe`);
            }
          },
        },
      ],
      setup: async (ctx) => {
        const { sns, sqs } = makeClients(ctx);
        await Promise.all([
          sns.send(new CreateTopicCommand({ Name: `${ctx.runId}-sns-sub` })),
          sqs.send(
            new CreateQueueCommand({ QueueName: `${ctx.runId}-sns-sub-q` }),
          ),
        ]);
      },
      teardown: async (ctx) => {
        const { sns, sqs } = makeClients(ctx);
        try {
          const { Topics = [] } = await sns.send(new ListTopicsCommand({}));
          const arn = Topics.find((t) =>
            t.TopicArn?.includes(`${ctx.runId}-sns-sub`),
          )?.TopicArn;
          if (arn) await sns.send(new DeleteTopicCommand({ TopicArn: arn }));
        } catch {}
        try {
          const { QueueUrl } = await sqs.send(
            new GetQueueUrlCommand({ QueueName: `${ctx.runId}-sns-sub-q` }),
          );
          if (QueueUrl) await sqs.send(new PurgeQueueCommand({ QueueUrl }));
          if (QueueUrl) await sqs.send(new DeleteQueueCommand({ QueueUrl }));
        } catch {}
      },
    },
  ];
}
