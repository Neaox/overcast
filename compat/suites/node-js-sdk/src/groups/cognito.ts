/**
 * groups/cognito.ts — Cognito User Pools compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future Cognito implementation.
 *
 * Groups:
 *   cognito-userpools — user pool and user lifecycle
 */

import {
  CreateUserPoolCommand,
  DeleteUserPoolCommand,
  DescribeUserPoolCommand,
  ListUserPoolsCommand,
  AdminCreateUserCommand,
  AdminDeleteUserCommand,
  ListUsersCommand,
  CreateUserPoolClientCommand,
  ListUserPoolClientsCommand,
  DeleteUserPoolClientCommand,
  DescribeUserPoolClientCommand,
  UpdateUserPoolClientCommand,
} from "@aws-sdk/client-cognito-identity-provider";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeCognitoGroups(suite: string): TestGroup[] {
  return [
    // ── cognito-userpools ──────────────────────────────────────────────────
    {
      suite,
      service: "cognito",
      name: "cognito-userpools",
      tests: [
        {
          name: "CreateUserPool",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolName = `compat-${ctx.runId}`;
            const resp = await cognito.send(
              new CreateUserPoolCommand({ PoolName: poolName }),
            );
            assert.ok(resp.UserPool?.Id, "CreateUserPool: missing Id");
            (ctx as Record<string, unknown>)["_poolId"] = resp.UserPool.Id;
          },
        },
        {
          name: "DescribeUserPool",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_poolId"
            ] as string;
            assert.ok(poolId, "DescribeUserPool: no pool from CreateUserPool");
            const resp = await cognito.send(
              new DescribeUserPoolCommand({ UserPoolId: poolId }),
            );
            assert.ok(resp.UserPool?.Id, "DescribeUserPool: missing Id");
          },
        },
        {
          name: "ListUserPools",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            await cognito.send(new ListUserPoolsCommand({ MaxResults: 10 }));
          },
        },
        {
          name: "CreateUserPoolClient",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_poolId"
            ] as string;
            assert.ok(
              poolId,
              "CreateUserPoolClient: no pool from CreateUserPool",
            );
            const resp = await cognito.send(
              new CreateUserPoolClientCommand({
                UserPoolId: poolId,
                ClientName: `compat-client-${ctx.runId}`,
              }),
            );
            assert.ok(
              resp.UserPoolClient?.ClientId,
              "CreateUserPoolClient: missing ClientId",
            );
            (ctx as Record<string, unknown>)["_clientId"] =
              resp.UserPoolClient.ClientId;
          },
        },
        {
          name: "ListUserPoolClients",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_poolId"
            ] as string;
            assert.ok(
              poolId,
              "ListUserPoolClients: no pool from CreateUserPool",
            );
            const resp = await cognito.send(
              new ListUserPoolClientsCommand({
                UserPoolId: poolId,
                MaxResults: 10,
              }),
            );
            const clientId = (ctx as Record<string, unknown>)[
              "_clientId"
            ] as string;
            assert.ok(
              resp.UserPoolClients?.some((c) => c.ClientId === clientId),
              "ListUserPoolClients: created client not found",
            );
          },
        },
        {
          name: "AdminCreateUser",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_poolId"
            ] as string;
            assert.ok(poolId, "AdminCreateUser: no pool from CreateUserPool");
            await cognito.send(
              new AdminCreateUserCommand({
                UserPoolId: poolId,
                Username: `compat-user-${ctx.runId}`,
              }),
            );
            (ctx as Record<string, unknown>)["_username"] =
              `compat-user-${ctx.runId}`;
          },
        },
        {
          name: "ListUsers",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_poolId"
            ] as string;
            if (!poolId) return;
            await cognito.send(new ListUsersCommand({ UserPoolId: poolId }));
          },
        },
        {
          name: "AdminDeleteUser",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_poolId"
            ] as string;
            const username = (ctx as Record<string, unknown>)[
              "_username"
            ] as string;
            if (!poolId || !username) return;
            await cognito.send(
              new AdminDeleteUserCommand({
                UserPoolId: poolId,
                Username: username,
              }),
            );
          },
        },
        {
          name: "DeleteUserPool",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_poolId"
            ] as string;
            if (!poolId) return;
            await cognito.send(
              new DeleteUserPoolCommand({ UserPoolId: poolId }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { cognito } = makeClients(ctx);
        const poolId = (ctx as Record<string, unknown>)["_poolId"] as string;
        if (!poolId) return;
        const username = (ctx as Record<string, unknown>)[
          "_username"
        ] as string;
        if (username) {
          try {
            await cognito.send(
              new AdminDeleteUserCommand({
                UserPoolId: poolId,
                Username: username,
              }),
            );
          } catch {}
        }
        const clientId = (ctx as Record<string, unknown>)[
          "_clientId"
        ] as string;
        if (clientId) {
          try {
            await cognito.send(
              new DeleteUserPoolClientCommand({
                UserPoolId: poolId,
                ClientId: clientId,
              }),
            );
          } catch {}
        }
        try {
          await cognito.send(new DeleteUserPoolCommand({ UserPoolId: poolId }));
        } catch {}
      },
    },
    // ── cognito-token-validity ─────────────────────────────────────────────
    {
      suite,
      service: "cognito",
      name: "cognito-token-validity",
      tests: [
        {
          name: "CreateUserPoolClient with token validity",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolName = `compat-tv-${ctx.runId}`;
            const poolResp = await cognito.send(
              new CreateUserPoolCommand({ PoolName: poolName }),
            );
            const poolId = poolResp.UserPool?.Id;
            assert.ok(poolId, "CreateUserPool: missing Id");
            (ctx as Record<string, unknown>)["_tvPoolId"] = poolId;

            const resp = await cognito.send(
              new CreateUserPoolClientCommand({
                UserPoolId: poolId,
                ClientName: `compat-client-${ctx.runId}`,
                AccessTokenValidity: 2,
                IdTokenValidity: 3,
                RefreshTokenValidity: 7,
                TokenValidityUnits: {
                  AccessToken: "hours",
                  IdToken: "hours",
                  RefreshToken: "days",
                },
              }),
            );
            const client = resp.UserPoolClient;
            assert.ok(
              client?.ClientId,
              "CreateUserPoolClient: missing ClientId",
            );
            assert.equal(client.AccessTokenValidity, 2);
            assert.equal(client.IdTokenValidity, 3);
            assert.equal(client.RefreshTokenValidity, 7);
            assert.equal(client.TokenValidityUnits?.AccessToken, "hours");
            assert.equal(client.TokenValidityUnits?.IdToken, "hours");
            assert.equal(client.TokenValidityUnits?.RefreshToken, "days");
            (ctx as Record<string, unknown>)["_tvClientId"] = client.ClientId;
          },
        },
        {
          name: "DescribeUserPoolClient returns token validity",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_tvPoolId"
            ] as string;
            const clientId = (ctx as Record<string, unknown>)[
              "_tvClientId"
            ] as string;
            assert.ok(poolId && clientId, "no pool/client from create");

            const resp = await cognito.send(
              new DescribeUserPoolClientCommand({
                UserPoolId: poolId,
                ClientId: clientId,
              }),
            );
            const client = resp.UserPoolClient;
            assert.equal(client?.AccessTokenValidity, 2);
            assert.equal(client?.IdTokenValidity, 3);
            assert.equal(client?.RefreshTokenValidity, 7);
          },
        },
        {
          name: "UpdateUserPoolClient changes token validity",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_tvPoolId"
            ] as string;
            const clientId = (ctx as Record<string, unknown>)[
              "_tvClientId"
            ] as string;
            assert.ok(poolId && clientId, "no pool/client from create");

            const resp = await cognito.send(
              new UpdateUserPoolClientCommand({
                UserPoolId: poolId,
                ClientId: clientId,
                AccessTokenValidity: 30,
                TokenValidityUnits: {
                  AccessToken: "minutes",
                  IdToken: "hours",
                  RefreshToken: "days",
                },
              }),
            );
            const client = resp.UserPoolClient;
            assert.equal(client?.AccessTokenValidity, 30);
            assert.equal(client?.TokenValidityUnits?.AccessToken, "minutes");
          },
        },
        {
          name: "DeleteUserPoolClient",
          fn: async (ctx) => {
            const { cognito } = makeClients(ctx);
            const poolId = (ctx as Record<string, unknown>)[
              "_tvPoolId"
            ] as string;
            const clientId = (ctx as Record<string, unknown>)[
              "_tvClientId"
            ] as string;
            if (!poolId || !clientId) return;
            await cognito.send(
              new DeleteUserPoolClientCommand({
                UserPoolId: poolId,
                ClientId: clientId,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { cognito } = makeClients(ctx);
        const poolId = (ctx as Record<string, unknown>)["_tvPoolId"] as string;
        if (!poolId) return;
        const clientId = (ctx as Record<string, unknown>)[
          "_tvClientId"
        ] as string;
        if (clientId) {
          try {
            await cognito.send(
              new DeleteUserPoolClientCommand({
                UserPoolId: poolId,
                ClientId: clientId,
              }),
            );
          } catch {}
        }
        try {
          await cognito.send(new DeleteUserPoolCommand({ UserPoolId: poolId }));
        } catch {}
      },
    },
  ];
}
