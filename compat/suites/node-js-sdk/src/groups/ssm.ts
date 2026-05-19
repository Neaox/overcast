/**
 * groups/ssm.ts — SSM Parameter Store compat groups for the node-js-sdk suite.
 *
 * Status: NOT implemented in Overcast. Tests expected to fail with 501.
 *
 * Groups:
 *   ssm-parameters — String parameter lifecycle
 *   ssm-secure     — SecureString parameters (KMS-encrypted)
 *   ssm-path       — GetParametersByPath + hierarchical namespaces
 */

import {
  PutParameterCommand,
  GetParameterCommand,
  GetParametersCommand,
  GetParametersByPathCommand,
  DeleteParameterCommand,
  DeleteParametersCommand,
  DescribeParametersCommand,
  GetParameterHistoryCommand,
  AddTagsToResourceCommand,
  ListTagsForResourceCommand,
  ParameterType,
  ResourceTypeForTagging,
} from "@aws-sdk/client-ssm";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

export function makeSSMGroups(suite: string): TestGroup[] {
  return [
    // ── ssm-parameters ──────────────────────────────────────────────────────
    {
      suite,
      service: "ssm",
      name: "ssm-parameters",
      tests: [
        {
          name: "PutParameter",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new PutParameterCommand({
                Name: `/${ctx.runId}/db/host`,
                Value: "db.example.com",
                Type: ParameterType.STRING,
                Description: "compat test parameter",
                Overwrite: false,
              }),
            );
            assert.ok(resp.Version, "PutParameter: missing Version");
          },
        },
        {
          name: "GetParameter",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new GetParameterCommand({ Name: `/${ctx.runId}/db/host` }),
            );
            assert.strictEqual(resp.Parameter?.Value, "db.example.com", `GetParameter: value mismatch: ${resp.Parameter?.Value}`);
          },
        },
        {
          name: "PutParameterOverwrite",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            await ssm.send(
              new PutParameterCommand({
                Name: `/${ctx.runId}/db/host`,
                Value: "db2.example.com",
                Type: ParameterType.STRING,
                Overwrite: true,
              }),
            );
            const resp = await ssm.send(
              new GetParameterCommand({ Name: `/${ctx.runId}/db/host` }),
            );
            assert.strictEqual(resp.Parameter?.Value, "db2.example.com", `PutParameterOverwrite: expected db2.example.com, got ${resp.Parameter?.Value}`);
          },
        },
        {
          name: "GetParameterHistory",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new GetParameterHistoryCommand({ Name: `/${ctx.runId}/db/host` }),
            );
            assert.ok(((resp.Parameters?.length ?? 0)) >= (2), "GetParameterHistory: expected at least 2 versions");
          },
        },
        {
          name: "PutMultipleParameters",
          op: "PutParameter",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            await ssm.send(
              new PutParameterCommand({
                Name: `/${ctx.runId}/db/port`,
                Value: "5432",
                Type: ParameterType.STRING,
                Overwrite: false,
              }),
            );
            await ssm.send(
              new PutParameterCommand({
                Name: `/${ctx.runId}/db/name`,
                Value: "mydb",
                Type: ParameterType.STRING,
                Overwrite: false,
              }),
            );
            const resp = await ssm.send(
              new GetParametersCommand({
                Names: [`/${ctx.runId}/db/port`, `/${ctx.runId}/db/name`],
              }),
            );
            assert.strictEqual(resp.Parameters?.length, 2, `PutMultipleParameters: expected 2 params, got ${resp.Parameters?.length}`);
          },
        },
        {
          name: "GetParameters",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new GetParametersCommand({
                Names: [`/${ctx.runId}/db/host`, `/${ctx.runId}/db/port`],
              }),
            );
            assert.strictEqual(resp.Parameters?.length, 2, `GetParameters: expected 2, got ${resp.Parameters?.length}`);
          },
        },
        {
          name: "DescribeParameters",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new DescribeParametersCommand({
                ParameterFilters: [
                  {
                    Key: "Name",
                    Option: "BeginsWith",
                    Values: [`/${ctx.runId}/db`],
                  },
                ],
              }),
            );
            assert.ok(((resp.Parameters?.length ?? 0)) >= (3), `DescribeParameters: expected ≥3 params, got ${resp.Parameters?.length}`);
          },
        },
        {
          name: "TagParameter",
          op: "AddTagsToResource",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            await ssm.send(
              new AddTagsToResourceCommand({
                ResourceType: ResourceTypeForTagging.PARAMETER,
                ResourceId: `/${ctx.runId}/db/host`,
                Tags: [{ Key: "env", Value: "compat" }],
              }),
            );
            const resp = await ssm.send(
              new ListTagsForResourceCommand({
                ResourceType: ResourceTypeForTagging.PARAMETER,
                ResourceId: `/${ctx.runId}/db/host`,
              }),
            );
            assert.ok(resp.TagList?.some((t) => t.Key === "env" && t.Value === "compat"), "TagParameter: tag env=compat not found after add");
          },
        },
        {
          name: "ListSSMTagsForResource",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new ListTagsForResourceCommand({
                ResourceType: ResourceTypeForTagging.PARAMETER,
                ResourceId: `/${ctx.runId}/db/host`,
              }),
            );
            assert.ok(resp.TagList?.some((t) => t.Key === "env"), "ListTagsForResource: tag not found");
          },
        },
        {
          name: "DeleteParameters",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new DeleteParametersCommand({
                Names: [
                  `/${ctx.runId}/db/host`,
                  `/${ctx.runId}/db/port`,
                  `/${ctx.runId}/db/name`,
                ],
              }),
            );
            assert.strictEqual(resp.DeletedParameters?.length, 3, `DeleteParameters: expected 3 deleted, got ${resp.DeletedParameters?.length}`);
            const get = await ssm.send(
              new GetParametersCommand({
                Names: [
                  `/${ctx.runId}/db/host`,
                  `/${ctx.runId}/db/port`,
                  `/${ctx.runId}/db/name`,
                ],
              }),
            );
            assert.ok(((get.Parameters?.length ?? 0)) <= (0), "DeleteParameters: parameters still present after delete");
          },
        },
      ],
      teardown: async (ctx) => {
        const { ssm } = makeClients(ctx);
        try {
          await ssm.send(
            new DeleteParametersCommand({
              Names: [
                `/${ctx.runId}/db/host`,
                `/${ctx.runId}/db/port`,
                `/${ctx.runId}/db/name`,
              ],
            }),
          );
        } catch {}
      },
    },

    // ── ssm-secure ───────────────────────────────────────────────────────────
    {
      suite,
      service: "ssm",
      name: "ssm-secure",
      tests: [
        {
          name: "PutSecureStringParameter",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new PutParameterCommand({
                Name: `/${ctx.runId}/secrets/api-key`,
                Value: "super-secret-value",
                Type: ParameterType.SECURE_STRING,
                Description: "compat secure parameter",
                Overwrite: false,
              }),
            );
            assert.ok(resp.Version, "PutSecureStringParameter: missing Version");
          },
        },
        {
          name: "GetSecureStringParameter",
          op: "GetParameter",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new GetParameterCommand({
                Name: `/${ctx.runId}/secrets/api-key`,
                WithDecryption: true,
              }),
            );
            assert.strictEqual(resp.Parameter?.Type, ParameterType.SECURE_STRING, `GetSecureStringParameter: type mismatch: ${resp.Parameter?.Type}`);
            assert.strictEqual(resp.Parameter?.Value, "super-secret-value", "GetSecureStringParameter: decrypted value mismatch");
          },
        },
        {
          name: "GetSecureStringWithoutDecryption",
          op: "GetParameter",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new GetParameterCommand({
                Name: `/${ctx.runId}/secrets/api-key`,
                WithDecryption: false,
              }),
            );
            // When WithDecryption=false the value should be masked/encrypted.
            assert.notStrictEqual(resp.Parameter?.Value, "super-secret-value", "GetSecureStringWithoutDecryption: plaintext returned for encrypted param");
          },
        },
      ],
      teardown: async (ctx) => {
        const { ssm } = makeClients(ctx);
        try {
          await ssm.send(
            new DeleteParameterCommand({
              Name: `/${ctx.runId}/secrets/api-key`,
            }),
          );
        } catch {}
      },
    },

    // ── ssm-path ─────────────────────────────────────────────────────────────
    {
      suite,
      service: "ssm",
      name: "ssm-path",
      setup: async (ctx) => {
        const { ssm } = makeClients(ctx);
        for (const [name, value] of [
          ["host", "db.example.com"],
          ["port", "5432"],
          ["user", "admin"],
        ]) {
          await ssm.send(
            new PutParameterCommand({
              Name: `/${ctx.runId}/app/db/${name}`,
              Value: value,
              Type: ParameterType.STRING,
              Overwrite: false,
            }),
          );
        }
      },
      tests: [
        {
          name: "GetParametersByPath",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new GetParametersByPathCommand({
                Path: `/${ctx.runId}/app/db`,
                Recursive: false,
              }),
            );
            assert.strictEqual(resp.Parameters?.length, 3, `GetParametersByPath: expected 3, got ${resp.Parameters?.length}`);
          },
        },
        {
          name: "GetParametersByPathRecursive",
          op: "GetParametersByPath",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            const resp = await ssm.send(
              new GetParametersByPathCommand({
                Path: `/${ctx.runId}/app`,
                Recursive: true,
              }),
            );
            assert.ok(((resp.Parameters?.length ?? 0)) >= (3), `GetParametersByPathRecursive: expected ≥3, got ${resp.Parameters?.length}`);
          },
        },
        {
          name: "GetParametersByPathPaginated",
          op: "GetParametersByPath",
          fn: async (ctx) => {
            const { ssm } = makeClients(ctx);
            // MaxResults=2 → should return 2 with a token, then 1 on the next call.
            const page1 = await ssm.send(
              new GetParametersByPathCommand({
                Path: `/${ctx.runId}/app/db`,
                Recursive: false,
                MaxResults: 2,
              }),
            );
            assert.strictEqual(page1.Parameters?.length, 2, `GetParametersByPathPaginated: page1 expected 2, got ${page1.Parameters?.length}`);
            assert.ok(page1.NextToken, "GetParametersByPathPaginated: missing NextToken");
            const page2 = await ssm.send(
              new GetParametersByPathCommand({
                Path: `/${ctx.runId}/app/db`,
                Recursive: false,
                MaxResults: 2,
                NextToken: page1.NextToken,
              }),
            );
            assert.strictEqual(page2.Parameters?.length, 1, `GetParametersByPathPaginated: page2 expected 1, got ${page2.Parameters?.length}`);
          },
        },
      ],
      teardown: async (ctx) => {
        const { ssm } = makeClients(ctx);
        try {
          await ssm.send(
            new DeleteParametersCommand({
              Names: [
                `/${ctx.runId}/app/db/host`,
                `/${ctx.runId}/app/db/port`,
                `/${ctx.runId}/app/db/user`,
              ],
            }),
          );
        } catch {}
      },
    },
  ];
}
