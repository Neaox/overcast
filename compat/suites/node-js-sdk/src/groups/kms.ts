/**
 * groups/kms.ts — KMS compatibility test groups for the node-js-sdk suite.
 *
 * Status: NOT implemented in Overcast. Tests expected to fail with 501.
 *
 * Groups:
 *   kms-keys    — key lifecycle (symmetric + asymmetric)
 *   kms-crypto  — Encrypt/Decrypt/GenerateDataKey/Sign/Verify
 */

import {
  CreateKeyCommand,
  DescribeKeyCommand,
  DisableKeyCommand,
  EnableKeyCommand,
  ListKeysCommand,
  ScheduleKeyDeletionCommand,
  CancelKeyDeletionCommand,
  CreateAliasCommand,
  DeleteAliasCommand,
  ListAliasesCommand,
  TagResourceCommand,
  UntagResourceCommand,
  ListResourceTagsCommand,
  EncryptCommand,
  DecryptCommand,
  GenerateDataKeyCommand,
  GenerateDataKeyWithoutPlaintextCommand,
  SignCommand,
  VerifyCommand,
  KeySpec,
  KeyUsageType,
  DataKeySpec,
  MessageType,
  SigningAlgorithmSpec,
} from "@aws-sdk/client-kms";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeKMSGroups(suite: string): TestGroup[] {
  return [
    // ── kms-keys ───────────────────────────────────────────────────────────
    {
      suite,
      service: "kms",
      name: "kms-keys",
      tests: [
        {
          name: "CreateKey",
          fn: async (ctx) => {
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new CreateKeyCommand({
                Description: `compat-${ctx.runId}`,
                KeySpec: KeySpec.SYMMETRIC_DEFAULT,
                KeyUsage: KeyUsageType.ENCRYPT_DECRYPT,
              }),
            );
            assert.ok(resp.KeyMetadata?.KeyId, "CreateKey: missing KeyId");
            (ctx as Record<string, unknown>)["_keyId"] = resp.KeyMetadata.KeyId;
          },
        },
        {
          name: "DescribeKey",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new DescribeKeyCommand({ KeyId: keyId }),
            );
            assert.ok(
              resp.KeyMetadata?.Enabled,
              "DescribeKey: key not enabled",
            );
          },
        },
        {
          name: "CreateKmsAlias",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            await kms.send(
              new CreateAliasCommand({
                AliasName: `alias/compat-${ctx.runId}`,
                TargetKeyId: keyId,
              }),
            );
          },
        },
        {
          name: "ListKmsAliases",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new ListAliasesCommand({ KeyId: keyId }),
            );
            if (
              !resp.Aliases?.some(
                (a) => a.AliasName === `alias/compat-${ctx.runId}`,
              )
            ) {
              throw new Error("ListAliases: alias not found");
            }
          },
        },
        {
          name: "ListKeys",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(new ListKeysCommand({}));
            assert.ok(
              resp.Keys?.some((k) => k.KeyId === keyId),
              "ListKeys: created key not found",
            );
          },
        },
        {
          name: "DisableKey",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            await kms.send(new DisableKeyCommand({ KeyId: keyId }));
            const { KeyMetadata } = await kms.send(
              new DescribeKeyCommand({ KeyId: keyId }),
            );
            assert.strictEqual(
              KeyMetadata?.KeyState,
              "Disabled",
              `DisableKey: expected KeyState=Disabled, got ${KeyMetadata?.KeyState}`,
            );
          },
        },
        {
          name: "EnableKey",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            await kms.send(new EnableKeyCommand({ KeyId: keyId }));
            const { KeyMetadata } = await kms.send(
              new DescribeKeyCommand({ KeyId: keyId }),
            );
            assert.strictEqual(
              KeyMetadata?.KeyState,
              "Enabled",
              `EnableKey: expected KeyState=Enabled, got ${KeyMetadata?.KeyState}`,
            );
          },
        },
        {
          name: "ScheduleKeyDeletion",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            await kms.send(
              new ScheduleKeyDeletionCommand({
                KeyId: keyId,
                PendingWindowInDays: 7,
              }),
            );
            const { KeyMetadata } = await kms.send(
              new DescribeKeyCommand({ KeyId: keyId }),
            );
            assert.strictEqual(
              KeyMetadata?.KeyState,
              "PendingDeletion",
              `ScheduleKeyDeletion: expected KeyState=PendingDeletion, got ${KeyMetadata?.KeyState}`,
            );
          },
        },
        {
          name: "CancelKeyDeletion",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            await kms.send(new CancelKeyDeletionCommand({ KeyId: keyId }));
            const { KeyMetadata } = await kms.send(
              new DescribeKeyCommand({ KeyId: keyId }),
            );
            assert.notStrictEqual(
              KeyMetadata?.KeyState,
              "PendingDeletion",
              `CancelKeyDeletion: key still in PendingDeletion after cancel`,
            );
          },
        },
        {
          name: "TagKMSResource",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            await kms.send(
              new TagResourceCommand({
                KeyId: keyId,
                Tags: [
                  { TagKey: "env", TagValue: "compat" },
                  { TagKey: "run", TagValue: ctx.runId },
                ],
              }),
            );
          },
        },
        {
          name: "ListKMSResourceTags",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new ListResourceTagsCommand({ KeyId: keyId }),
            );
            assert.ok(
              resp.Tags?.some(
                (t) => t.TagKey === "env" && t.TagValue === "compat",
              ),
              "ListKMSResourceTags: env tag not found",
            );
            assert.ok(
              resp.Tags?.some((t) => t.TagKey === "run"),
              "ListKMSResourceTags: run tag not found",
            );
          },
        },
        {
          name: "UntagKMSResource",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            await kms.send(
              new UntagResourceCommand({
                KeyId: keyId,
                TagKeys: ["run"],
              }),
            );
            // Verify the tag was actually removed
            const resp = await kms.send(
              new ListResourceTagsCommand({ KeyId: keyId }),
            );
            assert.ok(
              !resp.Tags?.some((t) => t.TagKey === "run"),
              "UntagKMSResource: run tag should have been removed",
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const keyId = (ctx as Record<string, unknown>)["_keyId"] as string;
        if (!keyId) return;
        const { kms } = makeClients(ctx);
        try {
          await kms.send(
            new DeleteAliasCommand({ AliasName: `alias/compat-${ctx.runId}` }),
          );
        } catch {}
        try {
          await kms.send(
            new ScheduleKeyDeletionCommand({
              KeyId: keyId,
              PendingWindowInDays: 7,
            }),
          );
        } catch {}
      },
    },

    // ── kms-crypto ─────────────────────────────────────────────────────────
    {
      suite,
      service: "kms",
      name: "kms-crypto",
      setup: async (ctx) => {
        const { kms } = makeClients(ctx);
        const resp = await kms.send(
          new CreateKeyCommand({
            Description: `compat-crypto-${ctx.runId}`,
            KeySpec: KeySpec.SYMMETRIC_DEFAULT,
            KeyUsage: KeyUsageType.ENCRYPT_DECRYPT,
          }),
        );
        (ctx as Record<string, unknown>)["_cryptoKeyId"] =
          resp.KeyMetadata!.KeyId;
      },
      tests: [
        {
          name: "Encrypt",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)[
              "_cryptoKeyId"
            ] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new EncryptCommand({
                KeyId: keyId,
                Plaintext: Buffer.from("hello overcast"),
              }),
            );
            assert.ok(resp.CiphertextBlob, "Encrypt: missing CiphertextBlob");
            (ctx as Record<string, unknown>)["_ciphertext"] =
              resp.CiphertextBlob;
          },
        },
        {
          name: "Decrypt",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)[
              "_cryptoKeyId"
            ] as string;
            const ciphertext = (ctx as Record<string, unknown>)[
              "_ciphertext"
            ] as Uint8Array;
            assert.ok(keyId || !ciphertext, "missing KeyId or ciphertext");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new DecryptCommand({ KeyId: keyId, CiphertextBlob: ciphertext }),
            );
            assert.ok(resp.Plaintext, "Decrypt: missing Plaintext");
            const text = Buffer.from(resp.Plaintext).toString();
            assert.strictEqual(
              text,
              "hello overcast",
              `Decrypt: unexpected plaintext: ${text}`,
            );
          },
        },
        {
          name: "GenerateDataKey",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)[
              "_cryptoKeyId"
            ] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new GenerateDataKeyCommand({
                KeyId: keyId,
                KeySpec: DataKeySpec.AES_256,
              }),
            );
            assert.ok(resp.Plaintext, "GenerateDataKey: missing Plaintext");
            assert.ok(
              resp.CiphertextBlob,
              "GenerateDataKey: missing CiphertextBlob",
            );
          },
        },
        {
          name: "GenerateDataKeyWithoutPlaintext",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)[
              "_cryptoKeyId"
            ] as string;
            assert.ok(keyId, "no KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new GenerateDataKeyWithoutPlaintextCommand({
                KeyId: keyId,
                KeySpec: DataKeySpec.AES_256,
              }),
            );
            assert.ok(
              resp.CiphertextBlob,
              "GenerateDataKeyWithoutPlaintext: missing CiphertextBlob",
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const keyId = (ctx as Record<string, unknown>)[
          "_cryptoKeyId"
        ] as string;
        if (!keyId) return;
        const { kms } = makeClients(ctx);
        try {
          await kms.send(
            new ScheduleKeyDeletionCommand({
              KeyId: keyId,
              PendingWindowInDays: 7,
            }),
          );
        } catch {}
      },
    },

    // ── kms-asymmetric ─────────────────────────────────────────────────────
    {
      suite,
      service: "kms",
      name: "kms-asymmetric",
      setup: async (ctx) => {
        const { kms } = makeClients(ctx);
        const resp = await kms.send(
          new CreateKeyCommand({
            Description: `compat-asym-${ctx.runId}`,
            KeySpec: KeySpec.RSA_2048,
            KeyUsage: KeyUsageType.SIGN_VERIFY,
          }),
        );
        (ctx as Record<string, unknown>)["_asymKeyId"] =
          resp.KeyMetadata!.KeyId;
      },
      tests: [
        {
          name: "Sign",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)[
              "_asymKeyId"
            ] as string;
            assert.ok(keyId, "no asymmetric KeyId");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new SignCommand({
                KeyId: keyId,
                Message: Buffer.from("message to sign"),
                MessageType: MessageType.RAW,
                SigningAlgorithm:
                  SigningAlgorithmSpec.RSASSA_PKCS1_V1_5_SHA_256,
              }),
            );
            assert.ok(resp.Signature, "Sign: missing Signature");
            (ctx as Record<string, unknown>)["_signature"] = resp.Signature;
          },
        },
        {
          name: "Verify",
          fn: async (ctx) => {
            const keyId = (ctx as Record<string, unknown>)[
              "_asymKeyId"
            ] as string;
            const sig = (ctx as Record<string, unknown>)[
              "_signature"
            ] as Uint8Array;
            assert.ok(keyId || !sig, "missing KeyId or Signature");
            const { kms } = makeClients(ctx);
            const resp = await kms.send(
              new VerifyCommand({
                KeyId: keyId,
                Message: Buffer.from("message to sign"),
                MessageType: MessageType.RAW,
                Signature: sig,
                SigningAlgorithm:
                  SigningAlgorithmSpec.RSASSA_PKCS1_V1_5_SHA_256,
              }),
            );
            assert.ok(resp.SignatureValid, "Verify: signature not valid");
          },
        },
      ],
      teardown: async (ctx) => {
        const keyId = (ctx as Record<string, unknown>)["_asymKeyId"] as string;
        if (!keyId) return;
        const { kms } = makeClients(ctx);
        try {
          await kms.send(
            new ScheduleKeyDeletionCommand({
              KeyId: keyId,
              PendingWindowInDays: 7,
            }),
          );
        } catch {}
      },
    },
  ];
}
