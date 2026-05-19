/**
 * groups/apigateway.ts — API Gateway (REST v1 + HTTP v2) compatibility test groups
 * for the Node.js suite.
 *
 * Status: Implemented in Overcast (REST v1 + HTTP v2 management, Lambda proxy execution).
 *
 * Groups:
 *   apigateway-rest  — REST API lifecycle (v1)
 *   apigateway-http  — HTTP API lifecycle (v2)
 */

import {
  CreateRestApiCommand,
  DeleteRestApiCommand,
  GetRestApisCommand,
} from "@aws-sdk/client-api-gateway";
import {
  CreateApiCommand,
  DeleteApiCommand,
  GetApisCommand,
  ProtocolType,
} from "@aws-sdk/client-apigatewayv2";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
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
  ];
}
