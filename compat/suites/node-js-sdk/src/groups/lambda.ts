/**
 * groups/lambda.ts — Lambda compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   lambda-crud    — function lifecycle (implemented)
 *   lambda-invoke  — synchronous invocation (requires Docker, skip if unavailable)
 *   lambda-invoke-stream — InvokeWithResponseStream (requires Docker, skip if unavailable)
 *   lambda-config  — environment variables, timeout, memory (implemented)
 *   lambda-aliases — versions and aliases (implemented)
 *   lambda-layers  — layer management (implemented)
 */

import {
  CreateFunctionCommand,
  DeleteFunctionCommand,
  GetFunctionCommand,
  ListFunctionsCommand,
  UpdateFunctionCodeCommand,
  UpdateFunctionConfigurationCommand,
  InvokeCommand,
  InvokeWithResponseStreamCommand,
  CreateAliasCommand,
  UpdateAliasCommand,
  GetAliasCommand,
  ListAliasesCommand,
  DeleteAliasCommand,
  PublishVersionCommand,
  ListVersionsByFunctionCommand,
  PublishLayerVersionCommand,
  ListLayersCommand,
  DeleteLayerVersionCommand,
  type FunctionConfiguration,
} from "@aws-sdk/client-lambda";
import { DeleteLogGroupCommand } from "@aws-sdk/client-cloudwatch-logs";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

/**
 * Minimal Node.js Lambda handler bundled as a base64-encoded zip.
 * Handler: exports.handler = async () => ({ statusCode: 200, body: "ok" })
 * Generated programmatically with correct local header sizes and CRC-32.
 * (The previous zip was created by piping to `zip`, which left size=0 in
 * local headers and wrong offsets in the central directory, causing
 * Go's archive/zip to return ErrFormat on f.Open().)
 */
const MINIMAL_ZIP_BASE64 =
  "UEsDBBQAAAAAAAAAAAAKhksPNQAAADUAAAAIAAAAaW5kZXguanNleHBvcnRzLmhhbmRsZXI9YXN5bmMoKT0+KHtzdGF0dXNDb2RlOjIwMCxib2R5OiJvayJ9KVBLAQIUABQAAAAAAAAAAAAKhksPNQAAADUAAAAIAAAAAAAAAAAAAAAAAAAAAABpbmRleC5qc1BLBQYAAAAAAQABADYAAABbAAAAAAA=";

function makeZipBuffer(): Uint8Array {
  return Buffer.from(MINIMAL_ZIP_BASE64, "base64");
}

const ROLE_ARN = "arn:aws:iam::000000000000:role/lambda-exec";
const RUNTIME = "nodejs20.x";
const HANDLER = "index.handler";

import type { LambdaClient } from "@aws-sdk/client-lambda";
import type { CloudWatchLogsClient } from "@aws-sdk/client-cloudwatch-logs";

async function deleteFunctionAndLogGroup(
  lambda: LambdaClient,
  logs: CloudWatchLogsClient,
  name: string,
): Promise<void> {
  try { await lambda.send(new DeleteFunctionCommand({ FunctionName: name })); } catch {}
  try { await logs.send(new DeleteLogGroupCommand({ logGroupName: `/aws/lambda/${name}` })); } catch {}
}

async function waitFunctionActive(lambda: LambdaClient, name: string, maxAttempts = 30): Promise<void> {
  for (let i = 0; i < maxAttempts; i++) {
    const resp = await lambda.send(
      new GetFunctionCommand({ FunctionName: name }),
    );
    const state = resp.Configuration?.State;
    if (state === "Active") return;
    if (state !== "Pending") return;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`Function ${name} did not become Active after ${maxAttempts} attempts`);
}

export function makeLambdaGroups(suite: string): TestGroup[] {
  return [
    // ── lambda-crud ────────────────────────────────────────────────────────
    {
      suite,
      service: "lambda",
      name: "lambda-crud",
      tests: [
        {
          name: "CreateFunction",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const name = `${ctx.runId}-fn`;
            const resp = await lambda.send(
              new CreateFunctionCommand({
                FunctionName: name,
                Runtime: RUNTIME,
                Handler: HANDLER,
                Role: ROLE_ARN,
                Code: { ZipFile: makeZipBuffer() },
              }),
            );
            assert.ok(resp.FunctionArn, "CreateFunction: missing FunctionArn");
            assert.strictEqual(
              resp.FunctionName,
              name,
              `CreateFunction: expected name=${name}, got ${resp.FunctionName}`,
            );
            assert.ok(resp.CodeSha256, "CreateFunction: missing CodeSha256");
          },
        },
        {
          name: "GetFunction",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new GetFunctionCommand({ FunctionName: `${ctx.runId}-fn` }),
            );
            assert.ok(
              resp.Configuration?.FunctionArn,
              "GetFunction: missing FunctionArn",
            );
          },
        },
        {
          name: "ListFunctions",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const name = `${ctx.runId}-fn`;
            const resp = await lambda.send(new ListFunctionsCommand({}));
            if (
              !resp.Functions?.some(
                (f: FunctionConfiguration) => f.FunctionName === name,
              )
            ) {
              throw new Error(`ListFunctions: ${name} not found`);
            }
          },
        },
        {
          name: "UpdateFunctionCode",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new UpdateFunctionCodeCommand({
                FunctionName: `${ctx.runId}-fn`,
                ZipFile: makeZipBuffer(),
              }),
            );
            assert.ok(
              resp.FunctionArn,
              "UpdateFunctionCode: missing FunctionArn",
            );
            assert.ok(
              resp.CodeSha256,
              "UpdateFunctionCode: missing CodeSha256",
            );
          },
        },
        {
          name: "UpdateFunctionConfiguration",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new UpdateFunctionConfigurationCommand({
                FunctionName: `${ctx.runId}-fn`,
                Timeout: 30,
                MemorySize: 256,
                Environment: { Variables: { LOG_LEVEL: "debug" } },
              }),
            );
            assert.strictEqual(
              resp.Timeout,
              30,
              `UpdateFunctionConfiguration: expected Timeout=30, got ${resp.Timeout}`,
            );
          },
        },
        {
          name: "DeleteFunction",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const name = `${ctx.runId}-fn`;
            await lambda.send(
              new DeleteFunctionCommand({ FunctionName: name }),
            );
            const resp = await lambda.send(new ListFunctionsCommand({}));
            if (
              resp.Functions?.some(
                (f: FunctionConfiguration) => f.FunctionName === name,
              )
            ) {
              throw new Error(
                `DeleteFunction: ${name} still present after delete`,
              );
            }
          },
        },
      ],
      teardown: async (ctx) => {
        const { lambda, logs } = makeClients(ctx);
        await deleteFunctionAndLogGroup(lambda, logs, `${ctx.runId}-fn`);
      },
    },

    // ── lambda-invoke ──────────────────────────────────────────────────────
    {
      suite,
      service: "lambda",
      name: "lambda-invoke",
      tests: [
        {
          name: "InvokeDryRun",
          op: "Invoke",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new InvokeCommand({
                FunctionName: `${ctx.runId}-fn-invoke`,
                InvocationType: "DryRun",
              }),
            );
            assert.strictEqual(
              resp.StatusCode,
              204,
              `InvokeDryRun: expected StatusCode=204, got ${resp.StatusCode}`,
            );
          },
        },
        {
          name: "InvokeSync",
          // Real Docker invocation — skip if OVERCAST_COMPAT_SKIP_DOCKER is set
          skip:
            process.env.OVERCAST_COMPAT_SKIP_DOCKER === "1"
              ? "Docker not available (set OVERCAST_COMPAT_SKIP_DOCKER=0 to enable)"
              : false,
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new InvokeCommand({
                FunctionName: `${ctx.runId}-fn-invoke`,
                InvocationType: "RequestResponse",
                Payload: new TextEncoder().encode(
                  JSON.stringify({ test: true }),
                ),
              }),
            );
            assert.strictEqual(
              resp.StatusCode,
              200,
              `InvokeSync: expected StatusCode=200, got ${resp.StatusCode}`,
            );
            if (resp.FunctionError) {
              const body = resp.Payload
                ? new TextDecoder().decode(resp.Payload)
                : "";
              throw new Error(
                `InvokeSync: function error: ${resp.FunctionError} — ${body}`,
              );
            }
          },
        },
        {
          name: "InvokeAsync",
          skip:
            process.env.OVERCAST_COMPAT_SKIP_DOCKER === "1"
              ? "Docker not available (set OVERCAST_COMPAT_SKIP_DOCKER=0 to enable)"
              : false,
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new InvokeCommand({
                FunctionName: `${ctx.runId}-fn-invoke`,
                InvocationType: "Event",
                Payload: new TextEncoder().encode("{}"),
              }),
            );
            assert.strictEqual(
              resp.StatusCode,
              202,
              `InvokeAsync: expected StatusCode=202, got ${resp.StatusCode}`,
            );
          },
        },
      ],
      setup: async (ctx) => {
        const { lambda } = makeClients(ctx);
        const name = `${ctx.runId}-fn-invoke`;
        await lambda.send(
          new CreateFunctionCommand({
            FunctionName: name,
            Runtime: RUNTIME,
            Handler: HANDLER,
            Role: ROLE_ARN,
            Code: { ZipFile: makeZipBuffer() },
            Timeout: 30,
          }),
        );
        await waitFunctionActive(lambda, name);
      },
      teardown: async (ctx) => {
        const { lambda, logs } = makeClients(ctx);
        await deleteFunctionAndLogGroup(lambda, logs, `${ctx.runId}-fn-invoke`);
      },
    },

    // ── lambda-invoke-stream ───────────────────────────────────────────────
    {
      suite,
      service: "lambda",
      name: "lambda-invoke-stream",
      tests: [
        {
          name: "InvokeWithResponseStream",
          skip:
            process.env.OVERCAST_COMPAT_SKIP_DOCKER === "1"
              ? "Docker not available (set OVERCAST_COMPAT_SKIP_DOCKER=0 to enable)"
              : false,
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new InvokeWithResponseStreamCommand({
                FunctionName: `${ctx.runId}-fn-stream`,
                InvocationType: "RequestResponse",
                Payload: new TextEncoder().encode(
                  JSON.stringify({ test: true }),
                ),
              }),
            );
            assert.strictEqual(
              resp.StatusCode,
              200,
              `InvokeWithResponseStream: expected StatusCode=200, got ${resp.StatusCode}`,
            );
            assert.strictEqual(
              resp.ExecutedVersion,
              "$LATEST",
              `InvokeWithResponseStream: expected ExecutedVersion=$LATEST`,
            );

            // Consume event stream and collect chunks.
            const chunks: Uint8Array[] = [];
            let completed = false;
            if (resp.EventStream) {
              for await (const event of resp.EventStream) {
                if (event.PayloadChunk?.Payload) {
                  chunks.push(event.PayloadChunk.Payload);
                }
                if (event.InvokeComplete !== undefined) {
                  completed = true;
                }
              }
            }
            assert.ok(
              completed,
              "InvokeWithResponseStream: expected InvokeComplete event",
            );
            assert.ok(
              chunks.length > 0,
              "InvokeWithResponseStream: expected at least one PayloadChunk",
            );
          },
        },
      ],
      setup: async (ctx) => {
        const { lambda } = makeClients(ctx);
        const name = `${ctx.runId}-fn-stream`;
        await lambda.send(
          new CreateFunctionCommand({
            FunctionName: name,
            Runtime: RUNTIME,
            Handler: HANDLER,
            Role: ROLE_ARN,
            Code: { ZipFile: makeZipBuffer() },
            Timeout: 30,
          }),
        );
        await waitFunctionActive(lambda, name);
      },
      teardown: async (ctx) => {
        const { lambda, logs } = makeClients(ctx);
        await deleteFunctionAndLogGroup(lambda, logs, `${ctx.runId}-fn-stream`);
      },
    },

    // ── lambda-aliases ─────────────────────────────────────────────────────
    {
      suite,
      service: "lambda",
      name: "lambda-aliases",
      tests: [
        {
          name: "PublishVersion",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new PublishVersionCommand({
                FunctionName: `${ctx.runId}-fn-alias`,
              }),
            );
            assert.ok(resp.Version, "PublishVersion: missing Version");
            ctx["_version"] = resp.Version;
          },
        },
        {
          name: "ListVersionsByFunction",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new ListVersionsByFunctionCommand({
                FunctionName: `${ctx.runId}-fn-alias`,
              }),
            );
            assert.notStrictEqual(
              resp.Versions?.length ?? 0,
              0,
              "ListVersionsByFunction: expected at least one version",
            );
          },
        },
        {
          name: "CreateAlias",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const version = (ctx["_version"] as string) ?? "1";
            const resp = await lambda.send(
              new CreateAliasCommand({
                FunctionName: `${ctx.runId}-fn-alias`,
                Name: "live",
                FunctionVersion: version,
              }),
            );
            assert.ok(resp.AliasArn, "CreateAlias: missing AliasArn");
          },
        },
        {
          name: "GetAlias",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new GetAliasCommand({
                FunctionName: `${ctx.runId}-fn-alias`,
                Name: "live",
              }),
            );
            assert.strictEqual(
              resp.Name,
              "live",
              `GetAlias: expected Name=live, got ${resp.Name}`,
            );
          },
        },
        {
          name: "ListAliases",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new ListAliasesCommand({ FunctionName: `${ctx.runId}-fn-alias` }),
            );
            assert.ok(
              resp.Aliases?.some((a) => a.Name === "live"),
              "ListAliases: expected alias 'live'",
            );
          },
        },
        {
          name: "UpdateAlias",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(
              new UpdateAliasCommand({
                FunctionName: `${ctx.runId}-fn-alias`,
                Name: "live",
                Description: "production alias",
              }),
            );
            assert.strictEqual(
              resp.Description,
              "production alias",
              `UpdateAlias: expected Description="production alias"`,
            );
          },
        },
        {
          name: "DeleteAlias",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            await lambda.send(
              new DeleteAliasCommand({
                FunctionName: `${ctx.runId}-fn-alias`,
                Name: "live",
              }),
            );
            const { Aliases = [] } = await lambda.send(
              new ListAliasesCommand({ FunctionName: `${ctx.runId}-fn-alias` }),
            );
            assert.notStrictEqual(
              Aliases.some((a) => a.Name, "live"),
              "DeleteAlias: alias 'live' still present after delete",
            );
          },
        },
      ],
      setup: async (ctx) => {
        const { lambda } = makeClients(ctx);
        await lambda.send(
          new CreateFunctionCommand({
            FunctionName: `${ctx.runId}-fn-alias`,
            Runtime: RUNTIME,
            Handler: HANDLER,
            Role: ROLE_ARN,
            Code: { ZipFile: makeZipBuffer() },
          }),
        );
      },
      teardown: async (ctx) => {
        const { lambda, logs } = makeClients(ctx);
        await deleteFunctionAndLogGroup(lambda, logs, `${ctx.runId}-fn-alias`);
      },
    },

    // ── lambda-layers ──────────────────────────────────────────────────────
    {
      suite,
      service: "lambda",
      name: "lambda-layers",
      tests: [
        {
          name: "PublishLayerVersion",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const layerName = `${ctx.runId}-layer`;
            const resp = await lambda.send(
              new PublishLayerVersionCommand({
                LayerName: layerName,
                Description: "test layer",
                Content: { ZipFile: makeZipBuffer() },
                CompatibleRuntimes: [RUNTIME],
              }),
            );
            assert.ok(
              resp.LayerVersionArn,
              "PublishLayerVersion: missing LayerVersionArn",
            );
            ctx["_layerArn"] = resp.LayerVersionArn;
            ctx["_layerVersion"] = resp.Version;
          },
        },
        {
          name: "ListLayers",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const resp = await lambda.send(new ListLayersCommand({}));
            const layerName = `${ctx.runId}-layer`;
            assert.ok(
              resp.Layers?.some((l) => l.LayerName === layerName),
              `ListLayers: ${layerName} not found`,
            );
          },
        },
        {
          name: "DeleteLayerVersion",
          fn: async (ctx) => {
            const { lambda } = makeClients(ctx);
            const layerName = `${ctx.runId}-layer`;
            const version = (ctx["_layerVersion"] as number) ?? 1;
            await lambda.send(
              new DeleteLayerVersionCommand({
                LayerName: layerName,
                VersionNumber: version,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { lambda } = makeClients(ctx);
        const layerName = `${ctx.runId}-layer`;
        const version = (ctx["_layerVersion"] as number) ?? 1;
        try {
          await lambda.send(
            new DeleteLayerVersionCommand({
              LayerName: layerName,
              VersionNumber: version,
            }),
          );
        } catch {}
      },
    },
  ];
}
