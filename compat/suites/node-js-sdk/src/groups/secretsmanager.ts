/**
 * groups/secretsmanager.ts — Secrets Manager compat groups for node-js-sdk.
 *
 * Groups:
 *   secretsmanager-crud   — full secret lifecycle
 *   secretsmanager-rotate — rotation config and the staging labels rotation moves
 *   secretsmanager-policy — resource-policy CRUD and validation
 *
 * The rotate group deliberately configures rotation with RotateImmediately
 * false: driving the four-step protocol end to end needs a real rotation
 * Lambda, which is Docker-dependent and belongs with the Lambda groups. What is
 * exercised here is everything around it — the validation contract, the
 * schedule, and the version staging labels a rotation function moves.
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
  UpdateSecretVersionStageCommand,
  PutResourcePolicyCommand,
  GetResourcePolicyCommand,
  DeleteResourcePolicyCommand,
  ValidateResourcePolicyCommand,
} from "@aws-sdk/client-secrets-manager";

/** Placeholder rotation function — never invoked, since rotation is scheduled only. */
const rotationLambdaArn = (ctx: { region: string }) =>
  `arn:aws:lambda:${ctx.region}:000000000000:function:oc-rotate-fn`;

/** AWS requires a ClientRequestToken of 32–64 characters; this is a UUID. */
const pendingToken = "0f9c1d2e-3a4b-4c5d-8e6f-7a8b9c0d1e2f";

const compatResourcePolicy = JSON.stringify({
  Version: "2012-10-17",
  Statement: [
    {
      Sid: "OvercastCompatRead",
      Effect: "Allow",
      Principal: { AWS: "arn:aws:iam::000000000000:root" },
      Action: "secretsmanager:GetSecretValue",
      Resource: "*",
    },
  ],
});

function errorCode(e: unknown): string {
  return (
    (e as { name?: string }).name ??
    (e as { __type?: string }).__type ??
    String(e)
  );
}
import { makeClients } from "../lib/clients.ts";
import type { TestGroup } from "../lib/harness.ts";
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
          name: "RotateSecretWithoutLambda",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            // AWS refuses to enable rotation on a secret with no rotation
            // function, neither on the call nor already configured.
            try {
              await secretsmanager.send(
                new RotateSecretCommand({
                  SecretId: `${ctx.runId}/rotate-test`,
                  RotationRules: { AutomaticallyAfterDays: 30 },
                }),
              );
            } catch (e: unknown) {
              assert.strictEqual(
                errorCode(e),
                "InvalidRequestException",
                `RotateSecretWithoutLambda: expected InvalidRequestException, got ${errorCode(e)}`,
              );
              return;
            }
            throw new Error(
              "RotateSecretWithoutLambda: expected InvalidRequestException, call succeeded",
            );
          },
        },
        {
          name: "RotateSecret",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const arn = rotationLambdaArn(ctx);
            const resp = await secretsmanager.send(
              new RotateSecretCommand({
                SecretId: `${ctx.runId}/rotate-test`,
                RotationLambdaARN: arn,
                RotationRules: { AutomaticallyAfterDays: 30 },
                RotateImmediately: false,
              }),
            );
            assert.ok(resp.ARN, "RotateSecret: missing ARN");

            const desc = await secretsmanager.send(
              new DescribeSecretCommand({ SecretId: `${ctx.runId}/rotate-test` }),
            );
            assert.strictEqual(desc.RotationEnabled, true, "RotateSecret: RotationEnabled not set");
            assert.strictEqual(desc.RotationLambdaARN, arn, "RotateSecret: RotationLambdaARN mismatch");
            assert.strictEqual(
              desc.RotationRules?.AutomaticallyAfterDays,
              30,
              "RotateSecret: AutomaticallyAfterDays mismatch",
            );
            assert.ok(desc.NextRotationDate, "RotateSecret: NextRotationDate not set");
          },
        },
        {
          name: "PutSecretValuePending",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new PutSecretValueCommand({
                SecretId: `${ctx.runId}/rotate-test`,
                SecretString: "pending-value",
                ClientRequestToken: pendingToken,
                VersionStages: ["AWSPENDING"],
              }),
            );
            assert.strictEqual(
              resp.VersionId,
              pendingToken,
              `PutSecretValuePending: VersionId should be the ClientRequestToken, got ${resp.VersionId}`,
            );
            assert.ok(
              resp.VersionStages?.includes("AWSPENDING"),
              `PutSecretValuePending: stages ${JSON.stringify(resp.VersionStages)} missing AWSPENDING`,
            );

            // AWSCURRENT must be untouched — staging a pending version is not
            // the same as changing the secret.
            const current = await secretsmanager.send(
              new GetSecretValueCommand({ SecretId: `${ctx.runId}/rotate-test` }),
            );
            assert.strictEqual(
              current.SecretString,
              "initial-value",
              `PutSecretValuePending: AWSCURRENT changed to ${current.SecretString}`,
            );
          },
        },
        {
          name: "GetSecretValueByStage",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new GetSecretValueCommand({
                SecretId: `${ctx.runId}/rotate-test`,
                VersionStage: "AWSPENDING",
              }),
            );
            assert.strictEqual(
              resp.SecretString,
              "pending-value",
              `GetSecretValueByStage: expected pending-value, got ${resp.SecretString}`,
            );
          },
        },
        {
          name: "UpdateSecretVersionStage",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const desc = await secretsmanager.send(
              new DescribeSecretCommand({ SecretId: `${ctx.runId}/rotate-test` }),
            );
            const currentVersion = Object.entries(desc.VersionIdsToStages ?? {}).find(
              ([, stages]) => stages.includes("AWSCURRENT"),
            )?.[0];
            assert.ok(currentVersion, "UpdateSecretVersionStage: no AWSCURRENT version");

            // This is what a rotation function's finishSecret step does.
            await secretsmanager.send(
              new UpdateSecretVersionStageCommand({
                SecretId: `${ctx.runId}/rotate-test`,
                VersionStage: "AWSCURRENT",
                MoveToVersionId: pendingToken,
                RemoveFromVersionId: currentVersion,
              }),
            );

            const after = await secretsmanager.send(
              new GetSecretValueCommand({ SecretId: `${ctx.runId}/rotate-test` }),
            );
            assert.strictEqual(
              after.SecretString,
              "pending-value",
              `UpdateSecretVersionStage: AWSCURRENT is ${after.SecretString}`,
            );
            assert.strictEqual(
              after.VersionId,
              pendingToken,
              `UpdateSecretVersionStage: AWSCURRENT version is ${after.VersionId}`,
            );

            const post = await secretsmanager.send(
              new DescribeSecretCommand({ SecretId: `${ctx.runId}/rotate-test` }),
            );
            assert.ok(
              post.VersionIdsToStages?.[currentVersion]?.includes("AWSPREVIOUS"),
              "UpdateSecretVersionStage: displaced version did not become AWSPREVIOUS",
            );
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
            // AWS keeps the function configured so rotation can be re-enabled.
            assert.strictEqual(
              resp.RotationLambdaARN,
              rotationLambdaArn(ctx),
              "CancelRotateSecret: rotation function should stay configured",
            );
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

    // ── secretsmanager-policy ──────────────────────────────────────────────
    // Overcast stores and validates a secret's resource policy but does not
    // evaluate it, so these assert the round-trip and the validation verdict,
    // never an access decision.
    {
      suite,
      service: "secretsmanager",
      name: "secretsmanager-policy",
      setup: async (ctx) => {
        const { secretsmanager } = makeClients(ctx);
        await secretsmanager.send(
          new CreateSecretCommand({
            Name: `${ctx.runId}/policy-test`,
            SecretString: "policy-value",
          }),
        );
      },
      tests: [
        {
          name: "ValidateResourcePolicy",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const ok = await secretsmanager.send(
              new ValidateResourcePolicyCommand({ ResourcePolicy: compatResourcePolicy }),
            );
            assert.strictEqual(
              ok.PolicyValidationPassed,
              true,
              `ValidateResourcePolicy: valid policy rejected: ${JSON.stringify(ok.ValidationErrors)}`,
            );

            const bad = await secretsmanager.send(
              new ValidateResourcePolicyCommand({
                ResourcePolicy: JSON.stringify({ Version: "2012-10-17" }),
              }),
            );
            assert.strictEqual(
              bad.PolicyValidationPassed,
              false,
              "ValidateResourcePolicy: policy with no Statement should not pass",
            );
            assert.ok(
              bad.ValidationErrors?.length,
              "ValidateResourcePolicy: no ValidationErrors for a failing policy",
            );
          },
        },
        {
          name: "PutResourcePolicy",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new PutResourcePolicyCommand({
                SecretId: `${ctx.runId}/policy-test`,
                ResourcePolicy: compatResourcePolicy,
              }),
            );
            assert.ok(resp.ARN, "PutResourcePolicy: missing ARN");
            assert.strictEqual(
              resp.Name,
              `${ctx.runId}/policy-test`,
              `PutResourcePolicy: Name mismatch, got ${resp.Name}`,
            );
          },
        },
        {
          name: "GetResourcePolicy",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            const resp = await secretsmanager.send(
              new GetResourcePolicyCommand({ SecretId: `${ctx.runId}/policy-test` }),
            );
            assert.ok(resp.ResourcePolicy, "GetResourcePolicy: no policy returned");
            assert.ok(
              resp.ResourcePolicy.includes("OvercastCompatRead"),
              `GetResourcePolicy: policy does not carry the Sid that was stored: ${resp.ResourcePolicy}`,
            );
          },
        },
        {
          name: "DeleteResourcePolicy",
          fn: async (ctx) => {
            const { secretsmanager } = makeClients(ctx);
            await secretsmanager.send(
              new DeleteResourcePolicyCommand({ SecretId: `${ctx.runId}/policy-test` }),
            );
            const resp = await secretsmanager.send(
              new GetResourcePolicyCommand({ SecretId: `${ctx.runId}/policy-test` }),
            );
            assert.ok(
              !resp.ResourcePolicy,
              `DeleteResourcePolicy: policy still present: ${resp.ResourcePolicy}`,
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { secretsmanager } = makeClients(ctx);
        try {
          await secretsmanager.send(
            new DeleteSecretCommand({
              SecretId: `${ctx.runId}/policy-test`,
              ForceDeleteWithoutRecovery: true,
            }),
          );
        } catch {}
      },
    },
  ];
}
