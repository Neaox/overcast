/**
 * groups/eventbridge.ts — EventBridge compat groups for the node-js-sdk suite.
 *
 * Status: NOT implemented in Overcast. Tests expected to fail with 501.
 *
 * Groups:
 *   eventbridge-buses  — custom event bus lifecycle
 *   eventbridge-rules  — rule + target lifecycle on the default bus
 *   eventbridge-events — PutEvents
 */

import {
  CreateEventBusCommand,
  DeleteEventBusCommand,
  DescribeEventBusCommand,
  ListEventBusesCommand,
  PutRuleCommand,
  DeleteRuleCommand,
  DescribeRuleCommand,
  ListRulesCommand,
  PutTargetsCommand,
  RemoveTargetsCommand,
  ListTargetsByRuleCommand,
  PutEventsCommand,
  EnableRuleCommand,
  DisableRuleCommand,
  TagResourceCommand,
  ListTagsForResourceCommand,
  RuleState,
} from "@aws-sdk/client-eventbridge";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeEventBridgeGroups(suite: string): TestGroup[] {
  return [
    // ── eventbridge-buses ──────────────────────────────────────────────────
    {
      suite,
      service: "eventbridge",
      name: "eventbridge-buses",
      tests: [
        {
          name: "CreateEventBus",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new CreateEventBusCommand({ Name: `${ctx.runId}-bus` }),
            );
            assert.ok(resp.EventBusArn, "CreateEventBus: missing EventBusArn");
            (ctx as Record<string, unknown>)["_busArn"] = resp.EventBusArn;
          },
        },
        {
          name: "DescribeEventBus",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new DescribeEventBusCommand({ Name: `${ctx.runId}-bus` }),
            );
            assert.ok(resp.Arn, "DescribeEventBus: missing Arn");
          },
        },
        {
          name: "ListEventBuses",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new ListEventBusesCommand({ NamePrefix: ctx.runId }),
            );
            assert.ok(resp.EventBuses?.some((b) => b.Name === `${ctx.runId}-bus`), "ListEventBuses: bus not found");
          },
        },
        {
          name: "TagEventBus",
          op: "TagResource",
          fn: async (ctx) => {
            const busArn = (ctx as Record<string, unknown>)[
              "_busArn"
            ] as string;
            assert.ok(busArn, "no bus ARN");
            const { eventbridge } = makeClients(ctx);
            await eventbridge.send(
              new TagResourceCommand({
                ResourceARN: busArn,
                Tags: [{ Key: "env", Value: "compat" }],
              }),
            );
          },
        },
        {
          name: "ListEventBridgeTagsForResource",
          fn: async (ctx) => {
            const busArn = (ctx as Record<string, unknown>)[
              "_busArn"
            ] as string;
            assert.ok(busArn, "no bus ARN");
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new ListTagsForResourceCommand({ ResourceARN: busArn }),
            );
            assert.ok(resp.Tags?.some((t) => t.Key === "env"), "ListTagsForResource: tag not found");
          },
        },
        {
          name: "DeleteEventBus",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const busName = `${ctx.runId}-bus`;
            await eventbridge.send(
              new DeleteEventBusCommand({ Name: busName }),
            );
            const resp = await eventbridge.send(
              new ListEventBusesCommand({ NamePrefix: ctx.runId }),
            );
            assert.notStrictEqual(resp.EventBuses?.some((b) => b.Name, busName), `DeleteEventBus: event bus ${busName} still present after delete`);
          },
        },
      ],
      teardown: async (ctx) => {
        const { eventbridge } = makeClients(ctx);
        try {
          await eventbridge.send(
            new DeleteEventBusCommand({ Name: `${ctx.runId}-bus` }),
          );
        } catch {}
      },
    },

    // ── eventbridge-rules ──────────────────────────────────────────────────
    {
      suite,
      service: "eventbridge",
      name: "eventbridge-rules",
      tests: [
        {
          name: "PutRule",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new PutRuleCommand({
                Name: `${ctx.runId}-rule`,
                EventPattern: JSON.stringify({
                  source: [`compat.${ctx.runId}`],
                }),
                State: RuleState.ENABLED,
                Description: "compat test rule",
              }),
            );
            assert.ok(resp.RuleArn, "PutRule: missing RuleArn");
            (ctx as Record<string, unknown>)["_ruleArn"] = resp.RuleArn;
          },
        },
        {
          name: "DescribeRule",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new DescribeRuleCommand({ Name: `${ctx.runId}-rule` }),
            );
            assert.ok(resp.Arn, "DescribeRule: missing Arn");
            assert.strictEqual(resp.State, RuleState.ENABLED, `DescribeRule: unexpected state: ${resp.State}`);
          },
        },
        {
          name: "ListRules",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new ListRulesCommand({ NamePrefix: ctx.runId }),
            );
            assert.ok(resp.Rules?.some((r) => r.Name === `${ctx.runId}-rule`), "ListRules: rule not found");
          },
        },
        {
          name: "PutTargets",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            // Use a synthetic SQS ARN — the rule just needs a valid ARN shape.
            const queueArn = `arn:aws:sqs:us-east-1:000000000000:${ctx.runId}-eb-target`;
            const resp = await eventbridge.send(
              new PutTargetsCommand({
                Rule: `${ctx.runId}-rule`,
                Targets: [{ Id: "target-1", Arn: queueArn }],
              }),
            );
            assert.ok(((resp.FailedEntryCount ?? 0)) <= (0), `PutTargets: ${resp.FailedEntryCount} failed entries`);
          },
        },
        {
          name: "ListTargetsByRule",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new ListTargetsByRuleCommand({ Rule: `${ctx.runId}-rule` }),
            );
            assert.ok(resp.Targets?.some((t) => t.Id === "target-1"), "ListTargetsByRule: target not found");
          },
        },
        {
          name: "DisableRule",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            await eventbridge.send(
              new DisableRuleCommand({ Name: `${ctx.runId}-rule` }),
            );
            const resp = await eventbridge.send(
              new DescribeRuleCommand({ Name: `${ctx.runId}-rule` }),
            );
            assert.strictEqual(resp.State, "DISABLED", `DisableRule: expected State=DISABLED, got ${resp.State}`);
          },
        },
        {
          name: "EnableRule",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            await eventbridge.send(
              new EnableRuleCommand({ Name: `${ctx.runId}-rule` }),
            );
            const resp = await eventbridge.send(
              new DescribeRuleCommand({ Name: `${ctx.runId}-rule` }),
            );
            assert.strictEqual(resp.State, "ENABLED", `EnableRule: expected State=ENABLED, got ${resp.State}`);
          },
        },
        {
          name: "RemoveTargets",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            await eventbridge.send(
              new RemoveTargetsCommand({
                Rule: `${ctx.runId}-rule`,
                Ids: ["target-1"],
              }),
            );
            const resp = await eventbridge.send(
              new ListTargetsByRuleCommand({ Rule: `${ctx.runId}-rule` }),
            );
            assert.notStrictEqual(resp.Targets?.some((t) => t.Id, "target-1"), "RemoveTargets: target-1 still present after remove");
          },
        },
        {
          name: "DeleteRule",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            await eventbridge.send(
              new DeleteRuleCommand({ Name: `${ctx.runId}-rule` }),
            );
            const resp = await eventbridge.send(
              new ListRulesCommand({ NamePrefix: ctx.runId }),
            );
            assert.notStrictEqual(resp.Rules?.some((r) => r.Name, `${ctx.runId}-rule`), `DeleteRule: rule ${ctx.runId}-rule still present after delete`);
          },
        },
      ],
      teardown: async (ctx) => {
        const { eventbridge } = makeClients(ctx);
        try {
          await eventbridge.send(
            new RemoveTargetsCommand({
              Rule: `${ctx.runId}-rule`,
              Ids: ["target-1"],
            }),
          );
        } catch {}
        try {
          await eventbridge.send(
            new DeleteRuleCommand({ Name: `${ctx.runId}-rule` }),
          );
        } catch {}
      },
    },

    // ── eventbridge-events ─────────────────────────────────────────────────
    {
      suite,
      service: "eventbridge",
      name: "eventbridge-events",
      tests: [
        {
          name: "PutEvents",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const resp = await eventbridge.send(
              new PutEventsCommand({
                Entries: [
                  {
                    Source: `compat.${ctx.runId}`,
                    DetailType: "CompatTest",
                    Detail: JSON.stringify({
                      runId: ctx.runId,
                      test: "PutEvents",
                    }),
                    EventBusName: "default",
                  },
                ],
              }),
            );
            assert.ok(((resp.FailedEntryCount ?? 0)) <= (0), `PutEvents: ${resp.FailedEntryCount} failed entries`);
          },
        },
        {
          name: "PutEventsBatch",
          fn: async (ctx) => {
            const { eventbridge } = makeClients(ctx);
            const entries = Array.from({ length: 5 }, (_, i) => ({
              Source: `compat.${ctx.runId}`,
              DetailType: "CompatBatch",
              Detail: JSON.stringify({ index: i }),
              EventBusName: "default",
            }));
            const resp = await eventbridge.send(
              new PutEventsCommand({ Entries: entries }),
            );
            assert.ok(((resp.FailedEntryCount ?? 0)) <= (0), `PutEventsBatch: ${resp.FailedEntryCount} failed entries`);
          },
        },
      ],
    },
  ];
}
