/**
 * groups/waf.ts — WAF v2 compatibility test groups for the Node.js suite.
 *
 * Status: NOT implemented in Overcast. All tests expected to fail with 501.
 * These tests define the coverage target for future WAF implementation.
 *
 * Groups:
 *   waf-webacls — Web ACL lifecycle
 */

import {
  CreateWebACLCommand,
  DeleteWebACLCommand,
  GetWebACLCommand,
  ListWebACLsCommand,
  Scope,
} from "@aws-sdk/client-wafv2";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeWAFGroups(suite: string): TestGroup[] {
  return [
    // ── waf-webacls ────────────────────────────────────────────────────────
    {
      suite,
      service: "waf",
      name: "waf-webacls",
      tests: [
        {
          name: "CreateWebACL",
          fn: async (ctx) => {
            const { wafv2 } = makeClients(ctx);
            const name = `compat-${ctx.runId}`;
            const resp = await wafv2.send(
              new CreateWebACLCommand({
                Name: name,
                Scope: Scope.REGIONAL,
                DefaultAction: { Allow: {} },
                VisibilityConfig: {
                  SampledRequestsEnabled: false,
                  CloudWatchMetricsEnabled: false,
                  MetricName: `compat-${ctx.runId}`,
                },
                Rules: [],
              }),
            );
            assert.ok(resp.Summary?.Id, "CreateWebACL: missing Id");
            (ctx as Record<string, unknown>)["_aclId"] = resp.Summary.Id;
            (ctx as Record<string, unknown>)["_aclName"] = name;
            (ctx as Record<string, unknown>)["_aclLockToken"] =
              resp.Summary.LockToken;
          },
        },
        {
          name: "GetWebACL",
          fn: async (ctx) => {
            const { wafv2 } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_aclId"] as string;
            const name = (ctx as Record<string, unknown>)["_aclName"] as string;
            assert.ok(id || !name, "GetWebACL: no ACL from CreateWebACL");
            const resp = await wafv2.send(
              new GetWebACLCommand({
                Id: id,
                Name: name,
                Scope: Scope.REGIONAL,
              }),
            );
            assert.ok(resp.WebACL?.Id, "GetWebACL: missing Id");
          },
        },
        {
          name: "ListWebACLs",
          fn: async (ctx) => {
            const { wafv2 } = makeClients(ctx);
            await wafv2.send(new ListWebACLsCommand({ Scope: Scope.REGIONAL }));
          },
        },
        {
          name: "DeleteWebACL",
          fn: async (ctx) => {
            const { wafv2 } = makeClients(ctx);
            const id = (ctx as Record<string, unknown>)["_aclId"] as string;
            const name = (ctx as Record<string, unknown>)["_aclName"] as string;
            const lockToken = (ctx as Record<string, unknown>)[
              "_aclLockToken"
            ] as string;
            if (!id || !name || !lockToken) return;
            await wafv2.send(
              new DeleteWebACLCommand({
                Id: id,
                Name: name,
                Scope: Scope.REGIONAL,
                LockToken: lockToken,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { wafv2 } = makeClients(ctx);
        const id = (ctx as Record<string, unknown>)["_aclId"] as string;
        const name = (ctx as Record<string, unknown>)["_aclName"] as string;
        if (!id || !name) return;
        try {
          // Refresh LockToken — it changes after each mutating call
          const fresh = await wafv2.send(
            new GetWebACLCommand({ Id: id, Name: name, Scope: Scope.REGIONAL }),
          );
          const lockToken = fresh.LockToken;
          if (!lockToken) return;
          await wafv2.send(
            new DeleteWebACLCommand({
              Id: id,
              Name: name,
              Scope: Scope.REGIONAL,
              LockToken: lockToken,
            }),
          );
        } catch {}
      },
    },
  ];
}
