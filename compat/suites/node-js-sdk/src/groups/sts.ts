/**
 * groups/sts.ts — STS compatibility test groups for the node-js-sdk suite.
 *
 * Status: NOT implemented in Overcast. Tests expected to fail with 501.
 *
 * Groups:
 *   sts-identity   — GetCallerIdentity, GetFederationToken
 *   sts-assume     — AssumeRole, GetSessionToken
 */

import {
  GetCallerIdentityCommand,
  AssumeRoleCommand,
  AssumeRoleWithWebIdentityCommand,
  GetSessionTokenCommand,
  GetFederationTokenCommand,
} from "@aws-sdk/client-sts"
import { makeClients } from "../lib/clients.js"
import type { TestGroup } from "../lib/harness.js"
import * as assert from "node:assert/strict";

export function makeSTSGroups(suite: string): TestGroup[] {
  return [
    // ── sts-identity ────────────────────────────────────────────────────────
    {
      suite,
      service: "sts",
      name: "sts-identity",
      tests: [
        {
          name: "GetCallerIdentity",
          fn: async (ctx) => {
            const { sts } = makeClients(ctx)
            const resp = await sts.send(new GetCallerIdentityCommand({}))
            assert.ok(resp.Account, "GetCallerIdentity: missing Account");
            assert.ok(resp.UserId, "GetCallerIdentity: missing UserId");
            assert.ok(resp.Arn, "GetCallerIdentity: missing Arn");
          },
        },
        {
          name: "GetSessionToken",
          fn: async (ctx) => {
            const { sts } = makeClients(ctx)
            const resp = await sts.send(new GetSessionTokenCommand({ DurationSeconds: 900 }))
            assert.ok(resp.Credentials?.AccessKeyId, "GetSessionToken: missing AccessKeyId");
            assert.ok(resp.Credentials?.SecretAccessKey, "GetSessionToken: missing SecretAccessKey");
            assert.ok(resp.Credentials?.SessionToken, "GetSessionToken: missing SessionToken");
          },
        },
        {
          name: "GetFederationToken",
          fn: async (ctx) => {
            const { sts } = makeClients(ctx)
            const resp = await sts.send(
              new GetFederationTokenCommand({
                Name: "compat-test",
                DurationSeconds: 900,
                Policy: JSON.stringify({
                  Version: "2012-10-17",
                  Statement: [{ Effect: "Allow", Action: "s3:GetObject", Resource: "*" }],
                }),
              }),
            )
            assert.ok(resp.Credentials?.AccessKeyId, "GetFederationToken: missing AccessKeyId");
          },
        },
      ],
    },

    // ── sts-assume ──────────────────────────────────────────────────────────
    {
      suite,
      service: "sts",
      name: "sts-assume",
      tests: [
        {
          name: "AssumeRole",
          fn: async (ctx) => {
            const { sts } = makeClients(ctx)
            // In Overcast the IAM role doesn't need to exist; we just want the
            // STS assume-role API to return valid temporary credentials.
            const resp = await sts.send(
              new AssumeRoleCommand({
                RoleArn: `arn:aws:iam::000000000000:role/${ctx.runId}-role`,
                RoleSessionName: `compat-${ctx.runId}`,
                DurationSeconds: 900,
              }),
            )
            assert.ok(resp.Credentials?.AccessKeyId, "AssumeRole: missing AccessKeyId");
            assert.ok(resp.AssumedRoleUser?.AssumedRoleId, "AssumeRole: missing AssumedRoleId");
          },
        },
        {
          name: "AssumeRoleWithWebIdentity",
          fn: async (ctx) => {
            const { sts } = makeClients(ctx)
            // A syntactically valid but fake JWT (three base64 segments).
            const fakeToken = [
              Buffer.from(JSON.stringify({ alg: "RS256" })).toString("base64url"),
              Buffer.from(
                JSON.stringify({ sub: "test-user", iss: "https://example.com", exp: 9999999999 }),
              ).toString("base64url"),
              "fakesig",
            ].join(".")
            const resp = await sts.send(
              new AssumeRoleWithWebIdentityCommand({
                RoleArn: `arn:aws:iam::000000000000:role/${ctx.runId}-web-role`,
                RoleSessionName: `compat-web-${ctx.runId}`,
                WebIdentityToken: fakeToken,
              }),
            )
            assert.ok(resp.Credentials?.AccessKeyId, "AssumeRoleWithWebIdentity: missing AccessKeyId");
          },
        },
      ],
    },
  ]
}
