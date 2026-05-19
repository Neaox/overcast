/**
 * groups/shield.ts — Shield (DDoS protection) compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future Shield implementation.
 *
 * Groups:
 *   shield-protections — protection lifecycle
 */

import {
  CreateProtectionCommand,
  DeleteProtectionCommand,
  DescribeProtectionCommand,
  ListProtectionsCommand,
  DescribeSubscriptionCommand,
} from "@aws-sdk/client-shield";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeShieldGroups(suite: string): TestGroup[] {
  return [
    // ── shield-protections ─────────────────────────────────────────────────
    {
      suite,
      service: "shield",
      name: "shield-protections",
      tests: [
        {
          name: "DescribeSubscription",
          fn: async (ctx) => {
            const { shield } = makeClients(ctx);
            // Shield Advanced requires a subscription; expect 501 in the stub.
            await shield.send(new DescribeSubscriptionCommand({}));
          },
        },
        {
          name: "CreateProtection",
          fn: async (ctx) => {
            const { shield } = makeClients(ctx);
            const resp = await shield.send(
              new CreateProtectionCommand({
                Name: `compat-${ctx.runId}`,
                ResourceArn: `arn:aws:ec2:us-east-1:000000000000:eip/eipalloc-${ctx.runId}`,
              }),
            );
            assert.ok(
              resp.ProtectionId,
              "CreateProtection: missing ProtectionId",
            );
            (ctx as Record<string, unknown>)["_protectionId"] =
              resp.ProtectionId;
          },
        },
        {
          name: "ListProtections",
          fn: async (ctx) => {
            const { shield } = makeClients(ctx);
            await shield.send(new ListProtectionsCommand({}));
          },
        },
        {
          name: "DeleteProtection",
          fn: async (ctx) => {
            const { shield } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)[
              "_protectionId"
            ] as string;
            if (!id) return;
            await shield.send(
              new DeleteProtectionCommand({ ProtectionId: id }),
            );
          },
        },
        {
          name: "DescribeProtection",
          fn: async (ctx) => {
            const { shield } = makeClients(ctx);
            // Create a fresh protection to describe (since DeleteProtection may have already deleted it).
            const createResp = await shield.send(
              new CreateProtectionCommand({
                Name: `compat-desc-${ctx.runId}`,
                ResourceArn: `arn:aws:ec2:us-east-1:000000000000:eip/eipalloc-desc-${ctx.runId}`,
              }),
            );
            const id = createResp.ProtectionId;
            assert.ok(id, "DescribeProtection: could not create protection");
            (ctx as Record<string, unknown>)["_descProtectionId"] = id;
            const resp = await shield.send(
              new DescribeProtectionCommand({ ProtectionId: id }),
            );
            assert.ok(
              resp.Protection,
              "DescribeProtection: missing Protection",
            );
            assert.strictEqual(
              resp.Protection.Id,
              id,
              `DescribeProtection: expected Id ${id}`,
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { shield } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_protectionId"] as string;
        if (id) {
          try {
            await shield.send(
              new DeleteProtectionCommand({ ProtectionId: id }),
            );
          } catch {}
        }
        const descId = (ctx as Record<string, unknown>)[
          "_descProtectionId"
        ] as string;
        if (descId) {
          try {
            await shield.send(
              new DeleteProtectionCommand({ ProtectionId: descId }),
            );
          } catch {}
        }
      },
    },
  ];
}
