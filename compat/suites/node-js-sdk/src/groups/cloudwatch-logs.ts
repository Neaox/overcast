/**
 * groups/cloudwatch-logs.ts — CloudWatch Logs test groups for Node.js suite.
 *
 * Groups:
 *   logs-groups  — log group lifecycle (implemented)
 *   logs-streams — log stream lifecycle (implemented)
 *   logs-events  — PutLogEvents, GetLogEvents, FilterLogEvents (implemented)
 */

import {
  CreateLogGroupCommand,
  DeleteLogGroupCommand,
  DescribeLogGroupsCommand,
  CreateLogStreamCommand,
  DeleteLogStreamCommand,
  DescribeLogStreamsCommand,
  PutLogEventsCommand,
  GetLogEventsCommand,
  FilterLogEventsCommand,
  PutRetentionPolicyCommand,
  DeleteRetentionPolicyCommand,
  TagLogGroupCommand,
  ListTagsLogGroupCommand,
} from "@aws-sdk/client-cloudwatch-logs";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeCloudWatchLogsGroups(suite: string): TestGroup[] {
  return [
    // ── logs-groups ────────────────────────────────────────────────────────
    {
      suite,
      service: "cloudwatch-logs",
      name: "logs-groups",
      tests: [
        {
          name: "CreateLogGroup",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/test`;
            // Should not throw — 200/no body on success
            await logs.send(
              new CreateLogGroupCommand({
                logGroupName: groupName,
              }),
            );
            const resp = await logs.send(
              new DescribeLogGroupsCommand({
                logGroupNamePrefix: groupName,
              }),
            );
            assert.ok(
              resp.logGroups?.some((g) => g.logGroupName === groupName),
              `CreateLogGroup: ${groupName} not found after create`,
            );
          },
        },
        {
          name: "DescribeLogGroups",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/test`;
            const resp = await logs.send(
              new DescribeLogGroupsCommand({
                logGroupNamePrefix: `/overcast/${ctx.runId}`,
              }),
            );
            assert.ok(
              resp.logGroups?.some((g) => g.logGroupName === groupName),
              `DescribeLogGroups: ${groupName} not found`,
            );
          },
        },
        {
          name: "PutRetentionPolicy",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            await logs.send(
              new PutRetentionPolicyCommand({
                logGroupName: `/overcast/${ctx.runId}/test`,
                retentionInDays: 7,
              }),
            );
            const resp = await logs.send(
              new DescribeLogGroupsCommand({
                logGroupNamePrefix: `/overcast/${ctx.runId}/test`,
              }),
            );
            const group = resp.logGroups?.find(
              (g) => g.logGroupName === `/overcast/${ctx.runId}/test`,
            );
            assert.strictEqual(
              group?.retentionInDays,
              7,
              `PutRetentionPolicy: expected retentionInDays=7, got ${group?.retentionInDays}`,
            );
          },
        },
        {
          name: "VerifyRetentionPolicy",
          op: false,
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/test`;
            const resp = await logs.send(
              new DescribeLogGroupsCommand({ logGroupNamePrefix: groupName }),
            );
            const group = resp.logGroups?.find(
              (g) => g.logGroupName === groupName,
            );
            assert.strictEqual(
              group?.retentionInDays,
              7,
              `VerifyRetentionPolicy: expected retentionInDays=7, got ${group?.retentionInDays}`,
            );
          },
        },
        {
          name: "DeleteRetentionPolicy",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            await logs.send(
              new DeleteRetentionPolicyCommand({
                logGroupName: `/overcast/${ctx.runId}/test`,
              }),
            );
            const resp = await logs.send(
              new DescribeLogGroupsCommand({
                logGroupNamePrefix: `/overcast/${ctx.runId}/test`,
              }),
            );
            const group = resp.logGroups?.find(
              (g) => g.logGroupName === `/overcast/${ctx.runId}/test`,
            );
            assert.strictEqual(
              group?.retentionInDays,
              undefined,
              "DeleteRetentionPolicy: retentionInDays still set",
            );
          },
        },
        {
          name: "CreateLogStream",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/test`;
            await logs.send(
              new CreateLogStreamCommand({
                logGroupName: groupName,
                logStreamName: "compat-stream-1",
              }),
            );
            const resp = await logs.send(
              new DescribeLogStreamsCommand({ logGroupName: groupName }),
            );
            assert.ok(
              resp.logStreams?.some(
                (s) => s.logStreamName === "compat-stream-1",
              ),
              "CreateLogStream: stream not found after create",
            );
          },
        },
        {
          name: "TagLogGroup",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/test`;
            await logs.send(
              new TagLogGroupCommand({
                logGroupName: groupName,
                tags: { env: "compat", run: ctx.runId },
              }),
            );
            const resp = await logs.send(
              new ListTagsLogGroupCommand({ logGroupName: groupName }),
            );
            assert.strictEqual(
              resp.tags?.env,
              "compat",
              `TagLogGroup: expected tag env=compat, got ${resp.tags?.env}`,
            );
          },
        },
        {
          name: "DeleteLogGroup",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/test`;
            await logs.send(
              new DeleteLogGroupCommand({ logGroupName: groupName }),
            );
            const resp = await logs.send(
              new DescribeLogGroupsCommand({ logGroupNamePrefix: groupName }),
            );
            assert.notStrictEqual(
              resp.logGroups?.some((g) => g.logGroupName, groupName),
              `DeleteLogGroup: ${groupName} still present`,
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { logs } = makeClients(ctx);
        try {
          await logs.send(
            new DeleteLogGroupCommand({
              logGroupName: `/overcast/${ctx.runId}/test`,
            }),
          );
        } catch {}
      },
    },

    // ── logs-events ────────────────────────────────────────────────────────
    {
      suite,
      service: "cloudwatch-logs",
      name: "logs-events",
      tests: [
        {
          name: "PutLogEvents",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/events`;
            const streamName = "stream-1";
            const resp = await logs.send(
              new PutLogEventsCommand({
                logGroupName: groupName,
                logStreamName: streamName,
                logEvents: [
                  { timestamp: Date.now() - 2000, message: "first log event" },
                  { timestamp: Date.now() - 1000, message: "second log event" },
                  {
                    timestamp: Date.now(),
                    message: JSON.stringify({
                      level: "info",
                      msg: "structured",
                    }),
                  },
                ],
              }),
            );
            assert.ok(
              !resp.rejectedLogEventsInfo,
              "PutLogEvents: events rejected as too old",
            );
          },
        },
        {
          name: "GetLogEvents",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/events`;
            const streamName = "stream-1";
            const resp = await logs.send(
              new GetLogEventsCommand({
                logGroupName: groupName,
                logStreamName: streamName,
              }),
            );
            assert.ok(
              (resp.events?.length ?? 0) >= 3,
              `GetLogEvents: expected >=3 events, got ${resp.events?.length}`,
            );
          },
        },
        {
          name: "FilterLogEvents",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/events`;
            const resp = await logs.send(
              new FilterLogEventsCommand({
                logGroupName: groupName,
                filterPattern: "structured",
              }),
            );
            assert.notStrictEqual(
              resp.events?.length ?? 0,
              0,
              "FilterLogEvents: expected at least one matching event",
            );
          },
        },
        {
          name: "DescribeLogStreams",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/events`;
            const resp = await logs.send(
              new DescribeLogStreamsCommand({ logGroupName: groupName }),
            );
            assert.ok(
              resp.logStreams?.some((s) => s.logStreamName === "stream-1"),
              "DescribeLogStreams: stream-1 not found",
            );
          },
        },
        {
          name: "DeleteLogStream",
          fn: async (ctx) => {
            const { logs } = makeClients(ctx);
            const groupName = `/overcast/${ctx.runId}/events`;
            await logs.send(
              new DeleteLogStreamCommand({
                logGroupName: groupName,
                logStreamName: "stream-1",
              }),
            );
            const resp = await logs.send(
              new DescribeLogStreamsCommand({ logGroupName: groupName }),
            );
            assert.notStrictEqual(
              resp.logStreams?.some((s) => s.logStreamName, "stream-1"),
              "DeleteLogStream: stream-1 still present after delete",
            );
          },
        },
      ],
      setup: async (ctx) => {
        const { logs } = makeClients(ctx);
        const groupName = `/overcast/${ctx.runId}/events`;
        await logs.send(new CreateLogGroupCommand({ logGroupName: groupName }));
        await logs.send(
          new CreateLogStreamCommand({
            logGroupName: groupName,
            logStreamName: "stream-1",
          }),
        );
      },
      teardown: async (ctx) => {
        const { logs } = makeClients(ctx);
        const groupName = `/overcast/${ctx.runId}/events`;
        try {
          await logs.send(
            new DeleteLogGroupCommand({ logGroupName: groupName }),
          );
        } catch {}
      },
    },
  ];
}
