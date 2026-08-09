/**
 * groups/apigateway.ts — API Gateway (REST v1 + HTTP v2) compatibility test groups
 * for the Node.js suite.
 *
 * Status: Implemented in Overcast (REST v1 + HTTP v2 management, Lambda proxy execution).
 *
 * Groups:
 *   apigateway-rest         — REST API lifecycle (v1)
 *   apigateway-http         — HTTP API lifecycle (v2)
 *   apigateway-usage-plans  — usage plan throttle/quota config + GetUsage
 */

import {
  CreateRestApiCommand,
  DeleteRestApiCommand,
  GetRestApisCommand,
  CreateUsagePlanCommand,
  GetUsagePlanCommand,
  DeleteUsagePlanCommand,
  CreateApiKeyCommand,
  DeleteApiKeyCommand,
  CreateUsagePlanKeyCommand,
  DeleteUsagePlanKeyCommand,
  GetUsagePlanKeysCommand,
  GetUsageCommand,
} from "@aws-sdk/client-api-gateway";
import {
  CreateApiCommand,
  DeleteApiCommand,
  GetApisCommand,
  ProtocolType,
} from "@aws-sdk/client-apigatewayv2";
import { makeClients } from "../lib/clients.ts";
import type { TestGroup } from "../lib/harness.ts";
import * as assert from "node:assert/strict";

export function makeAPIGatewayGroups(suite: string): TestGroup[] {
  return [
    // ── apigateway-rest ────────────────────────────────────────────────────
    {
      suite,
      service: "apigateway",
      name: "apigateway-rest",
      tests: [
        {
          name: "CreateRestApi",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const apiName = `compat-${ctx.runId}`;
            const resp = await apigateway.send(
              new CreateRestApiCommand({ name: apiName }),
            );
            assert.ok(resp.id, "CreateRestApi: missing id");
            (ctx as Record<string, unknown>)["_restApiId"] = resp.id;
          },
        },
        {
          name: "GetRestApis",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const resp = await apigateway.send(new GetRestApisCommand({}));
            assert.notStrictEqual(resp.items, undefined, "GetRestApis: missing items");
          },
        },
        {
          name: "DeleteRestApi",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const apiId = (ctx as Record<string, unknown>)[
              "_restApiId"
            ] as string;
            if (!apiId) return;
            await apigateway.send(
              new DeleteRestApiCommand({ restApiId: apiId }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { apigateway } = makeClients(ctx);
        const apiId = (ctx as Record<string, unknown>)["_restApiId"] as string;
        if (!apiId) return;
        try {
          await apigateway.send(new DeleteRestApiCommand({ restApiId: apiId }));
        } catch {}
      },
    },
    // ── apigateway-http ────────────────────────────────────────────────────
    {
      suite,
      service: "apigateway",
      name: "apigateway-http",
      tests: [
        {
          name: "CreateApi",
          fn: async (ctx) => {
            const { apigatewayv2 } = makeClients(ctx);
            const name = `compat-${ctx.runId}`;
            const resp = await apigatewayv2.send(
              new CreateApiCommand({
                Name: name,
                ProtocolType: ProtocolType.HTTP,
              }),
            );
            assert.ok(resp.ApiId, "CreateApi: missing ApiId");
            (ctx as Record<string, unknown>)["_httpApiId"] = resp.ApiId;
          },
        },
        {
          name: "GetApis",
          fn: async (ctx) => {
            const { apigatewayv2 } = makeClients(ctx);
            const resp = await apigatewayv2.send(new GetApisCommand({}));
            assert.notStrictEqual(resp.Items, undefined, "GetApis: missing Items");
          },
        },
        {
          name: "DeleteApi",
          fn: async (ctx) => {
            const { apigatewayv2 } = makeClients(ctx);
            const apiId = (ctx as Record<string, unknown>)[
              "_httpApiId"
            ] as string;
            if (!apiId) return;
            await apigatewayv2.send(new DeleteApiCommand({ ApiId: apiId }));
          },
        },
      ],
      teardown: async (ctx) => {
        const { apigatewayv2 } = makeClients(ctx);
        const apiId = (ctx as Record<string, unknown>)["_httpApiId"] as string;
        if (!apiId) return;
        try {
          await apigatewayv2.send(new DeleteApiCommand({ ApiId: apiId }));
        } catch {}
      },
    },
    // ── apigateway-usage-plans ─────────────────────────────────────────────
    {
      suite,
      service: "apigateway",
      name: "apigateway-usage-plans",
      tests: [
        {
          name: "CreateUsagePlanWithLimits",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const resp = await apigateway.send(
              new CreateUsagePlanCommand({
                name: `oc-${ctx.runId}-usage-plan`,
                throttle: { rateLimit: 10, burstLimit: 20 },
                quota: { limit: 1000, period: "DAY" },
              }),
            );
            assert.ok(resp.id, "CreateUsagePlan: missing id");
            assert.strictEqual(resp.name, `oc-${ctx.runId}-usage-plan`);
            (ctx as Record<string, unknown>)["_usagePlanId"] = resp.id;
          },
        },
        {
          name: "GetUsagePlan",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const planId = (ctx as Record<string, unknown>)["_usagePlanId"] as string;
            const resp = await apigateway.send(
              new GetUsagePlanCommand({ usagePlanId: planId }),
            );
            assert.strictEqual(resp.id, planId, "GetUsagePlan: wrong id");
            assert.strictEqual(resp.throttle?.rateLimit, 10, "GetUsagePlan: rateLimit lost");
            assert.strictEqual(resp.throttle?.burstLimit, 20, "GetUsagePlan: burstLimit lost");
            assert.strictEqual(resp.quota?.limit, 1000, "GetUsagePlan: quota limit lost");
            assert.strictEqual(resp.quota?.period, "DAY", "GetUsagePlan: quota period lost");
          },
        },
        {
          name: "CreateApiKey",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const resp = await apigateway.send(
              new CreateApiKeyCommand({
                name: `oc-${ctx.runId}-usage-key`,
                enabled: true,
              }),
            );
            assert.ok(resp.id, "CreateApiKey: missing id");
            assert.strictEqual(resp.name, `oc-${ctx.runId}-usage-key`);
            (ctx as Record<string, unknown>)["_usageKeyId"] = resp.id;
          },
        },
        {
          name: "CreateUsagePlanKey",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const planId = (ctx as Record<string, unknown>)["_usagePlanId"] as string;
            const keyId = (ctx as Record<string, unknown>)["_usageKeyId"] as string;
            await apigateway.send(
              new CreateUsagePlanKeyCommand({
                usagePlanId: planId,
                keyId,
                keyType: "API_KEY",
              }),
            );
            const listed = await apigateway.send(
              new GetUsagePlanKeysCommand({ usagePlanId: planId }),
            );
            assert.ok(
              (listed.items ?? []).some((k) => k.id === keyId),
              "GetUsagePlanKeys: attached key not listed",
            );
          },
        },
        {
          name: "GetUsage",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const planId = (ctx as Record<string, unknown>)["_usagePlanId"] as string;
            const keyId = (ctx as Record<string, unknown>)["_usageKeyId"] as string;
            const today = new Date().toISOString().slice(0, 10);
            const resp = await apigateway.send(
              new GetUsageCommand({
                usagePlanId: planId,
                startDate: today,
                endDate: today,
              }),
            );
            assert.strictEqual(resp.usagePlanId, planId, "GetUsage: wrong usagePlanId");
            assert.strictEqual(resp.startDate, today, "GetUsage: startDate not echoed");
            const days = (resp.items ?? {})[keyId];
            assert.ok(days, "GetUsage: no daily log for the attached key");
            assert.strictEqual(days.length, 1, "GetUsage: expected one day of data");
            assert.strictEqual(days[0].length, 2, "GetUsage: expected [used, remaining]");
          },
        },
        {
          name: "DeleteUsagePlan",
          fn: async (ctx) => {
            const { apigateway } = makeClients(ctx);
            const planId = (ctx as Record<string, unknown>)["_usagePlanId"] as string;
            const keyId = (ctx as Record<string, unknown>)["_usageKeyId"] as string;
            // AWS refuses to delete a plan that still has keys attached.
            await apigateway.send(
              new DeleteUsagePlanKeyCommand({ usagePlanId: planId, keyId }),
            );
            await apigateway.send(new DeleteUsagePlanCommand({ usagePlanId: planId }));
            await assert.rejects(
              apigateway.send(new GetUsagePlanCommand({ usagePlanId: planId })),
              "GetUsagePlan: deleted plan still readable",
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { apigateway } = makeClients(ctx);
        const planId = (ctx as Record<string, unknown>)["_usagePlanId"] as string;
        const keyId = (ctx as Record<string, unknown>)["_usageKeyId"] as string;
        if (planId) {
          try {
            await apigateway.send(new DeleteUsagePlanCommand({ usagePlanId: planId }));
          } catch {}
        }
        if (keyId) {
          try {
            await apigateway.send(new DeleteApiKeyCommand({ apiKey: keyId }));
          } catch {}
        }
      },
    },
  ];
}
