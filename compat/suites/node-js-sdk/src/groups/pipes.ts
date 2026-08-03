/**
 * groups/pipes.ts — EventBridge Pipes compat groups for the node-js-sdk suite.
 *
 * Groups:
 *   pipes-wiring — pipe CRUD, the loud rejection of a source/target
 *                  combination the emulator cannot run, and an end-to-end
 *                  delivery from a DynamoDB stream source to an SQS target.
 *
 * The group provisions its own table and queue (issue #388), and the delivery
 * assertion matches on this group's own record rather than "the first message
 * received", so a shared resource can never make it pass by accident.
 */

import {
  CreatePipeCommand,
  DeletePipeCommand,
  DescribePipeCommand,
  ListPipesCommand,
  UpdatePipeCommand,
  RequestedPipeState,
} from "@aws-sdk/client-pipes";
import {
  CreateTableCommand,
  DeleteTableCommand,
  DescribeTableCommand,
  PutItemCommand,
} from "@aws-sdk/client-dynamodb";
import {
  CreateQueueCommand,
  DeleteMessageCommand,
  DeleteQueueCommand,
  GetQueueAttributesCommand,
  ReceiveMessageCommand,
} from "@aws-sdk/client-sqs";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

/**
 * RoleArn is a required CreatePipe member. Overcast does not evaluate it — it
 * is not a security boundary — but AWS rejects the call without it.
 */
const ROLE_ARN = "arn:aws:iam::000000000000:role/oc-compat-pipe";

type PipeCtx = Parameters<NonNullable<TestGroup["setup"]>>[0];

const pipeName = (ctx: PipeCtx) => `oc-${ctx.runId}-pipe`;
const tableName = (ctx: PipeCtx) => `oc-${ctx.runId}-pipe-src`;
const queueName = (ctx: PipeCtx) => `oc-${ctx.runId}-pipe-dst`;

const store = (ctx: PipeCtx) => ctx as Record<string, unknown>;

export function makePipesGroups(suite: string): TestGroup[] {
  return [
    {
      suite,
      service: "pipes",
      name: "pipes-wiring",
      setup: async (ctx) => {
        const { dynamodb, sqs } = makeClients(ctx);
        await dynamodb.send(
          new CreateTableCommand({
            TableName: tableName(ctx),
            AttributeDefinitions: [{ AttributeName: "id", AttributeType: "S" }],
            KeySchema: [{ AttributeName: "id", KeyType: "HASH" }],
            BillingMode: "PAY_PER_REQUEST",
            StreamSpecification: { StreamEnabled: true, StreamViewType: "NEW_AND_OLD_IMAGES" },
          }),
        );
        const described = await dynamodb.send(
          new DescribeTableCommand({ TableName: tableName(ctx) }),
        );
        const streamArn = described.Table?.LatestStreamArn;
        assert.ok(streamArn, "pipes setup: table has no LatestStreamArn");
        store(ctx)["_pipeSourceArn"] = streamArn;

        const created = await sqs.send(new CreateQueueCommand({ QueueName: queueName(ctx) }));
        const attrs = await sqs.send(
          new GetQueueAttributesCommand({
            QueueUrl: created.QueueUrl,
            AttributeNames: ["QueueArn"],
          }),
        );
        store(ctx)["_pipeQueueUrl"] = created.QueueUrl;
        store(ctx)["_pipeTargetArn"] = attrs.Attributes?.QueueArn;
      },
      teardown: async (ctx) => {
        const { dynamodb, sqs, pipes } = makeClients(ctx);
        try {
          await pipes.send(new DeletePipeCommand({ Name: pipeName(ctx) }));
        } catch {}
        const url = store(ctx)["_pipeQueueUrl"] as string | undefined;
        if (url) {
          try {
            await sqs.send(new DeleteQueueCommand({ QueueUrl: url }));
          } catch {}
        }
        try {
          await dynamodb.send(new DeleteTableCommand({ TableName: tableName(ctx) }));
        } catch {}
      },
      tests: [
        {
          name: "CreatePipe",
          fn: async (ctx) => {
            const { pipes } = makeClients(ctx);
            const resp = await pipes.send(
              new CreatePipeCommand({
                Name: pipeName(ctx),
                Source: store(ctx)["_pipeSourceArn"] as string,
                Target: store(ctx)["_pipeTargetArn"] as string,
                RoleArn: ROLE_ARN,
              }),
            );
            assert.ok(resp.Arn, "CreatePipe: missing Arn");
            assert.strictEqual(resp.Name, pipeName(ctx), "CreatePipe: name mismatch");
            assert.strictEqual(
              resp.DesiredState,
              RequestedPipeState.RUNNING,
              "CreatePipe: DesiredState",
            );
          },
        },
        {
          name: "DescribePipe",
          fn: async (ctx) => {
            const { pipes } = makeClients(ctx);
            const resp = await pipes.send(new DescribePipeCommand({ Name: pipeName(ctx) }));
            assert.strictEqual(
              resp.Source,
              store(ctx)["_pipeSourceArn"],
              "DescribePipe: Source did not round-trip",
            );
            assert.strictEqual(
              resp.Target,
              store(ctx)["_pipeTargetArn"],
              "DescribePipe: Target did not round-trip",
            );
            assert.strictEqual(resp.RoleArn, ROLE_ARN, "DescribePipe: RoleArn did not round-trip");
          },
        },
        {
          name: "ListPipes",
          fn: async (ctx) => {
            const { pipes } = makeClients(ctx);
            const resp = await pipes.send(new ListPipesCommand({ NamePrefix: `oc-${ctx.runId}` }));
            assert.ok(
              resp.Pipes?.some((p) => p.Name === pipeName(ctx)),
              "ListPipes: pipe not found",
            );
          },
        },
        {
          // A well-formed ARN naming a service the emulator cannot deliver to
          // must fail the call rather than being stored and silently ignored.
          name: "CreatePipeRejectsUnsupportedTarget",
          fn: async (ctx) => {
            const { pipes } = makeClients(ctx);
            let rejected: unknown;
            try {
              await pipes.send(
                new CreatePipeCommand({
                  Name: `${pipeName(ctx)}-bad`,
                  Source: store(ctx)["_pipeSourceArn"] as string,
                  Target: "arn:aws:logs:us-east-1:000000000000:log-group:/aws/pipes/compat",
                  RoleArn: ROLE_ARN,
                }),
              );
            } catch (err) {
              rejected = err;
            }
            assert.ok(rejected, "CreatePipe accepted a target the emulator cannot deliver to");
            assert.strictEqual(
              (rejected as { name?: string }).name,
              "ValidationException",
              `CreatePipe: error ${String(rejected)}, want ValidationException`,
            );
          },
        },
        {
          name: "PipeDeliversToTarget",
          fn: async (ctx) => {
            const { dynamodb, pipes, sqs } = makeClients(ctx);
            const marker = `oc-${ctx.runId}-delivery`;

            // The pipe reaches RUNNING asynchronously.
            for (let attempt = 0; attempt < 20; attempt++) {
              const described = await pipes.send(new DescribePipeCommand({ Name: pipeName(ctx) }));
              if (described.CurrentState === "RUNNING") break;
              await new Promise((resolve) => setTimeout(resolve, 100));
            }

            await dynamodb.send(
              new PutItemCommand({
                TableName: tableName(ctx),
                Item: { id: { S: marker } },
              }),
            );

            const url = store(ctx)["_pipeQueueUrl"] as string;
            for (let attempt = 0; attempt < 20; attempt++) {
              const resp = await sqs.send(
                new ReceiveMessageCommand({
                  QueueUrl: url,
                  MaxNumberOfMessages: 10,
                  WaitTimeSeconds: 1,
                }),
              );
              for (const message of resp.Messages ?? []) {
                try {
                  await sqs.send(
                    new DeleteMessageCommand({
                      QueueUrl: url,
                      ReceiptHandle: message.ReceiptHandle,
                    }),
                  );
                } catch {}
                if (!message.Body?.includes(marker)) continue;
                const record = JSON.parse(message.Body) as Record<string, unknown>;
                assert.strictEqual(
                  record["eventSource"],
                  "aws:dynamodb",
                  `PipeDeliversToTarget: delivered record is not a DynamoDB record: ${message.Body}`,
                );
                return;
              }
              await new Promise((resolve) => setTimeout(resolve, 100));
            }
            throw new Error("PipeDeliversToTarget: no delivery reached the target queue");
          },
        },
        {
          name: "UpdatePipe",
          fn: async (ctx) => {
            const { pipes } = makeClients(ctx);
            const resp = await pipes.send(
              new UpdatePipeCommand({
                Name: pipeName(ctx),
                RoleArn: ROLE_ARN,
                DesiredState: RequestedPipeState.STOPPED,
                Description: "compat",
              }),
            );
            assert.strictEqual(
              resp.DesiredState,
              RequestedPipeState.STOPPED,
              "UpdatePipe: DesiredState",
            );
          },
        },
        {
          name: "DeletePipe",
          fn: async (ctx) => {
            const { pipes } = makeClients(ctx);
            const resp = await pipes.send(new DeletePipeCommand({ Name: pipeName(ctx) }));
            assert.strictEqual(resp.CurrentState, "DELETING", "DeletePipe: CurrentState");
          },
        },
      ],
    },
  ];
}
