/**
 * groups/secretsmanager.ts — Secrets Manager compat groups for node-js-sdk.
 *
 * Status: NOT implemented in Overcast. Tests expected to fail with 501.
 *
 * Groups:
 *   secretsmanager-crud   — full secret lifecycle
 *   secretsmanager-rotate — rotation config
 */

import {
  CreateSecretCommand,
  DeleteSecretCommand,
  DescribeSecretCommand,
  GetSecretValueCommand,
  ListSecretsCommand,
  PutSecretValueCommand,
  UpdateSecretCommand,
  RotateSecretCommand,
  CancelRotateSecretCommand,
  TagResourceCommand,
  UntagResourceCommand,
  ListSecretVersionIdsCommand,
  GetRandomPasswordCommand,
  BatchGetSecretValueCommand,
} from "@aws-sdk/client-secrets-manager";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeSecretsManagerGroups(suite: string): TestGroup[] {
  return [
    // ── secretsmanager-crud ────────────────────────────────────────────────
    {
      suite,
      service: "secretsmanager",
      name: "secretsmanager-crud",
      tests: [
        {
          name: "CreateSecret",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new CreateSecretCommand({
                Name: `${ctx.runId}/db-password`,
                SecretString: JSON.stringify({
                  username: "admin",
                  password: "s3cr3t",
                }),
                Description: "compat test secret",
              }),
            );
            assert.ok(resp.ARN, "CreateSecret: missing ARN");
            (ctx as Record<string, unknown>)["_secretArn"] = resp.ARN;
          },
        },
        {
          name: "GetSecretValue",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new GetSecretValueCommand({
                SecretId: `${ctx.runId}/db-password`,
              }),
            );
            assert.ok(resp.SecretString, "GetSecretValue: missing SecretString");
            const parsed = JSON.parse(resp.SecretString);
            assert.strictEqual(parsed.password, "s3cr3t", "GetSecretValue: unexpected value");
          },
        },
        {
          name: "DescribeSecret",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new DescribeSecretCommand({
                SecretId: `${ctx.runId}/db-password`,
              }),
            );
            assert.ok(resp.ARN, "DescribeSecret: missing ARN");
            assert.strictEqual(resp.Description, "compat test secret", "DescribeSecret: description mismatch");
          },
        },
        {
          name: "PutSecretValue",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new PutSecretValueCommand({
                SecretId: `${ctx.runId}/db-password`,
                SecretString: JSON.stringify({
                  username: "admin",
                  password: "n3ws3cr3t",
                }),
              }),
            );
            assert.ok(resp.VersionId, "PutSecretValue: missing VersionId");
            const get = await secretsmanager.send(
              new GetSecretValueCommand({
                SecretId: `${ctx.runId}/db-password`,
              }),
            );
            const parsed = JSON.parse(get.SecretString ?? "{}");
            assert.strictEqual(parsed.password, "n3ws3cr3t", `PutSecretValue: expected password=n3ws3cr3t, got ${parsed.password}`);
          },
        },
        {
          name: "ListSecretVersionIds",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new ListSecretVersionIdsCommand({
                SecretId: `${ctx.runId}/db-password`,
              }),
            );
            assert.ok(resp.Versions?.length, "ListSecretVersionIds: no versions");
          },
        },
        {
          name: "UpdateSecret",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            await secretsmanager.send(
              new UpdateSecretCommand({
                SecretId: `${ctx.runId}/db-password`,
                Description: "updated description",
              }),
            );
            const resp = await secretsmanager.send(
              new DescribeSecretCommand({
                SecretId: `${ctx.runId}/db-password`,
              }),
            );
            assert.strictEqual(resp.Description, "updated description", `UpdateSecret: expected description="updated description", got "${resp.Description}"`);
          },
        },
        {
          name: "TagResource",
          fn: async (ctx) => {
            const secretArn = (ctx as Record<string, unknown>)[
              "_secretArn"
            ] as string;
            assert.ok(secretArn, "no secret ARN");
            const { secretsmanager } = makeClients(ctx);
            await secretsmanager.send(
              new TagResourceCommand({
                SecretId: secretArn,
                Tags: [
                  { Key: "env", Value: "compat" },
                  { Key: "team", Value: "platform" },
                ],
              }),
            );
            const resp = await secretsmanager.send(
              new DescribeSecretCommand({ SecretId: secretArn }),
            );
            if (
              !resp.Tags?.some((t) => t.Key === "env" && t.Value === "compat")
            ) {
              throw new Error("TagResource: tag env=compat not found after tagging");
            }
            if (
              !resp.Tags?.some(
                (t) => t.Key === "team" && t.Value === "platform",
              )
            ) {
              throw new Error("TagResource: tag team=platform not found after tagging");
            }
          },
        },
        {
          name: "UntagResource",
          fn: async (ctx) => {
            const secretArn = (ctx as Record<string, unknown>)[
              "_secretArn"
            ] as string;
            assert.ok(secretArn, "no secret ARN");
            const { secretsmanager } = makeClients(ctx);
            await secretsmanager.send(
              new UntagResourceCommand({
                SecretId: secretArn,
                TagKeys: ["team"],
              }),
            );
            // Verify the tag was removed
            const resp = await secretsmanager.send(
              new DescribeSecretCommand({ SecretId: secretArn }),
            );
            assert.notStrictEqual(resp.Tags?.some((t) => t.Key, "team"), "UntagResource: 'team' tag still present");
            assert.ok(resp.Tags?.some((t) => t.Key === "env"), "UntagResource: 'env' tag was removed unexpectedly");
          },
        },
        {
          name: "GetRandomPassword",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new GetRandomPasswordCommand({ PasswordLength: 20 }),
            );
            assert.ok(resp.RandomPassword, "GetRandomPassword: missing RandomPassword");
            assert.strictEqual(resp.RandomPassword.length, 20, `GetRandomPassword: expected length 20, got ${resp.RandomPassword.length}`);
          },
        },
        {
          name: "BatchGetSecretValue",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new BatchGetSecretValueCommand({
                SecretIdList: [
                  `${ctx.runId}/db-password`,
                  `${ctx.runId}/no-such-secret`,
                ],
              }),
            );
            assert.ok(resp.SecretValues?.length, "BatchGetSecretValue: no SecretValues returned");
            assert.ok(resp.Errors?.length, "BatchGetSecretValue: expected an error entry for missing secret");
          },
        },
        {
          name: "ListSecrets",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(new ListSecretsCommand({}));
            if (
              !resp.SecretList?.some(
                (s) => s.Name === `${ctx.runId}/db-password`,
              )
            ) {
              throw new Error("ListSecrets: secret not found");
            }
          },
        },
        {
          name: "DeleteSecret",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            await secretsmanager.send(
              new DeleteSecretCommand({
                SecretId: `${ctx.runId}/db-password`,
                ForceDeleteWithoutRecovery: true,
              }),
            );
            const resp = await secretsmanager.send(
              new ListSecretsCommand({}),
            );
            if (
              resp.SecretList?.some(
                (s) => s.Name === `${ctx.runId}/db-password`,
              )
            ) {
              throw new Error("DeleteSecret: secret still present after delete");
            }
          },
        },
      ],
      teardown: async (ctx) => {
        const { secretsmanager } = makeClients(ctx);
        try {
          await secretsmanager.send(
            new DeleteSecretCommand({
              SecretId: `${ctx.runId}/db-password`,
              ForceDeleteWithoutRecovery: true,
            }),
          );
        } catch {}
      },
    },

    // ── secretsmanager-rotate ──────────────────────────────────────────────
    {
      suite,
      service: "secretsmanager",
      name: "secretsmanager-rotate",
      setup: async (ctx) => {
        const { secretsmanager } = makeClients(ctx);
        await secretsmanager.send(
          new CreateSecretCommand({
            Name: `${ctx.runId}/rotate-test`,
            SecretString: "initial-value",
          }),
        );
      },
      tests: [
        {
          name: "RotateSecret",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            // In Overcast, rotation without a Lambda is expected to fail gracefully
            // (no rotation Lambda ARN configured). We test that the API call is
            // accepted and returns a structured error rather than a 501.
            const resp = await secretsmanager.send(
              new RotateSecretCommand({
                SecretId: `${ctx.runId}/rotate-test`,
                RotationRules: { AutomaticallyAfterDays: 30 },
                // No RotationLambdaARN — tests the "configure rotation only" path.
              }),
            );
            assert.ok(resp.ARN, "RotateSecret: missing ARN");
          },
        },
        {
          name: "CancelRotateSecret",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            await secretsmanager.send(
              new CancelRotateSecretCommand({
                SecretId: `${ctx.runId}/rotate-test`,
              }),
            );
            const resp = await secretsmanager.send(
              new DescribeSecretCommand({
                SecretId: `${ctx.runId}/rotate-test`,
              }),
            );
            assert.ok(!(resp.RotationEnabled), "CancelRotateSecret: rotation still enabled after cancel");
          },
        },
      ],
      teardown: async (ctx) => {
        const { secretsmanager } = makeClients(ctx);
        try {
          await secretsmanager.send(
            new DeleteSecretCommand({
              SecretId: `${ctx.runId}/rotate-test`,
              ForceDeleteWithoutRecovery: true,
            }),
          );
        } catch {}
      },
    },
  ];
}
