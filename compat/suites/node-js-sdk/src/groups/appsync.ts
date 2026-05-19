/**
 * groups/appsync.ts — AppSync compatibility test groups for the Node.js suite.
 *
 * Groups:
 *   appsync-apis         — GraphQL API lifecycle (create, get, list, update, delete)
 *   appsync-schemas      — Schema upload, status
 *   appsync-api-keys     — API key CRUD
 *   appsync-datasources  — Data source CRUD
 *   appsync-functions    — Function CRUD
 *   appsync-resolvers    — Resolver CRUD + ListResolversByFunction
 *   appsync-types        — Type CRUD
 *   appsync-tags         — Tag resource operations
 *   appsync-env-vars     — Environment variable management
 *   appsync-domains      — Domain name CRUD + API associations
 *   appsync-cache        — API cache CRUD
 */

import {
  CreateGraphqlApiCommand,
  DeleteGraphqlApiCommand,
  GetGraphqlApiCommand,
  ListGraphqlApisCommand,
  UpdateGraphqlApiCommand,
  AuthenticationType,
  StartSchemaCreationCommand,
  GetSchemaCreationStatusCommand,
  CreateApiKeyCommand,
  ListApiKeysCommand,
  UpdateApiKeyCommand,
  DeleteApiKeyCommand,
  CreateDataSourceCommand,
  GetDataSourceCommand,
  ListDataSourcesCommand,
  UpdateDataSourceCommand,
  DeleteDataSourceCommand,
  CreateFunctionCommand,
  GetFunctionCommand,
  ListFunctionsCommand,
  UpdateFunctionCommand,
  DeleteFunctionCommand,
  CreateResolverCommand,
  GetResolverCommand,
  ListResolversCommand,
  UpdateResolverCommand,
  DeleteResolverCommand,
  ListResolversByFunctionCommand,
  CreateTypeCommand,
  GetTypeCommand,
  ListTypesCommand,
  UpdateTypeCommand,
  DeleteTypeCommand,
  TagResourceCommand,
  UntagResourceCommand,
  ListTagsForResourceCommand,
  PutGraphqlApiEnvironmentVariablesCommand,
  GetGraphqlApiEnvironmentVariablesCommand,
  CreateDomainNameCommand,
  GetDomainNameCommand,
  ListDomainNamesCommand,
  UpdateDomainNameCommand,
  DeleteDomainNameCommand,
  AssociateApiCommand,
  GetApiAssociationCommand,
  DisassociateApiCommand,
  CreateApiCacheCommand,
  GetApiCacheCommand,
  UpdateApiCacheCommand,
  DeleteApiCacheCommand,
  FlushApiCacheCommand,
  TypeDefinitionFormat,
} from "@aws-sdk/client-appsync";
import { makeClients } from "../lib/clients.js";
import type { TestGroup } from "../lib/harness.js";
import * as assert from "node:assert/strict";

// Helper: cast-free accessor for shared test state.
function st(ctx: unknown): Record<string, unknown> {
  return ctx as Record<string, unknown>;
}

export function makeAppSyncGroups(suite: string): TestGroup[] {
  return [
    // ── appsync-apis ───────────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-apis",
      tests: [
        {
          name: "CreateGraphqlApi",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const resp = await appsync.send(
              new CreateGraphqlApiCommand({
                name: `compat-${ctx.runId}`,
                authenticationType: AuthenticationType.API_KEY,
              }),
            );
            assert.ok(resp.graphqlApi?.apiId, "missing apiId");
            st(ctx)["_apiId"] = resp.graphqlApi.apiId;
            st(ctx)["_apiArn"] = resp.graphqlApi.arn;
          },
        },
        {
          name: "GetGraphqlApi",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_apiId"] as string;
            assert.ok(apiId, "no api from CreateGraphqlApi");
            const resp = await appsync.send(
              new GetGraphqlApiCommand({ apiId }),
            );
            assert.ok(resp.graphqlApi?.apiId, "missing apiId");
          },
        },
        {
          name: "UpdateGraphqlApi",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_apiId"] as string;
            const resp = await appsync.send(
              new UpdateGraphqlApiCommand({
                apiId,
                name: `compat-updated-${ctx.runId}`,
                authenticationType: AuthenticationType.API_KEY,
              }),
            );
            assert.ok(resp.graphqlApi?.apiId, "missing apiId");
          },
        },
        {
          name: "ListGraphqlApis",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const resp = await appsync.send(new ListGraphqlApisCommand({}));
            assert.ok(Array.isArray(resp.graphqlApis), "expected array");
          },
        },
        {
          name: "DeleteGraphqlApi",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_apiId"] as string;
            if (!apiId) return;
            await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_apiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-schemas ────────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-schemas",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-schema-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        st(ctx)["_schemaApiId"] = resp.graphqlApi!.apiId;
      },
      tests: [
        {
          name: "StartSchemaCreation",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_schemaApiId"] as string;
            const sdl = `type Query { hello: String }`;
            const resp = await appsync.send(
              new StartSchemaCreationCommand({
                apiId,
                definition: new TextEncoder().encode(sdl),
              }),
            );
            assert.ok(resp.status, "missing status");
          },
        },
        {
          name: "GetSchemaCreationStatus",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_schemaApiId"] as string;
            const resp = await appsync.send(
              new GetSchemaCreationStatusCommand({ apiId }),
            );
            assert.ok(resp.status, "missing status");
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_schemaApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-api-keys ───────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-api-keys",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-keys-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        st(ctx)["_keysApiId"] = resp.graphqlApi!.apiId;
      },
      tests: [
        {
          name: "CreateApiKey",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_keysApiId"] as string;
            const resp = await appsync.send(
              new CreateApiKeyCommand({
                apiId,
                description: "compat test key",
              }),
            );
            assert.ok(resp.apiKey?.id, "missing id");
            st(ctx)["_keyId"] = resp.apiKey.id;
          },
        },
        {
          name: "ListApiKeys",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_keysApiId"] as string;
            const resp = await appsync.send(
              new ListApiKeysCommand({ apiId }),
            );
            assert.ok(Array.isArray(resp.apiKeys), "expected array");
            assert.ok(resp.apiKeys!.length > 0, "expected at least one key");
          },
        },
        {
          name: "UpdateApiKey",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_keysApiId"] as string;
            const keyId = st(ctx)["_keyId"] as string;
            const resp = await appsync.send(
              new UpdateApiKeyCommand({
                apiId,
                id: keyId,
                description: "updated compat key",
              }),
            );
            assert.ok(resp.apiKey?.id, "missing id");
          },
        },
        {
          name: "DeleteApiKey",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_keysApiId"] as string;
            const cr = await appsync.send(
              new CreateApiKeyCommand({ apiId }),
            );
            await appsync.send(
              new DeleteApiKeyCommand({ apiId, id: cr.apiKey!.id! }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_keysApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-datasources ────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-datasources",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-ds-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        st(ctx)["_dsApiId"] = resp.graphqlApi!.apiId;
      },
      tests: [
        {
          name: "CreateDataSource",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_dsApiId"] as string;
            const resp = await appsync.send(
              new CreateDataSourceCommand({
                apiId,
                name: "CompatNoneDS",
                type: "NONE",
              }),
            );
            assert.ok(resp.dataSource?.name, "missing name");
            assert.strictEqual(resp.dataSource!.name, "CompatNoneDS");
          },
        },
        {
          name: "GetDataSource",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_dsApiId"] as string;
            const resp = await appsync.send(
              new GetDataSourceCommand({ apiId, name: "CompatNoneDS" }),
            );
            assert.ok(resp.dataSource?.dataSourceArn, "missing ARN");
          },
        },
        {
          name: "UpdateDataSource",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_dsApiId"] as string;
            const resp = await appsync.send(
              new UpdateDataSourceCommand({
                apiId,
                name: "CompatNoneDS",
                type: "NONE",
                description: "updated",
              }),
            );
            assert.ok(resp.dataSource?.name, "missing name");
          },
        },
        {
          name: "ListDataSources",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_dsApiId"] as string;
            const resp = await appsync.send(
              new ListDataSourcesCommand({ apiId }),
            );
            assert.ok(Array.isArray(resp.dataSources), "expected array");
            assert.ok(resp.dataSources!.length > 0, "expected at least one");
          },
        },
        {
          name: "DeleteDataSource",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_dsApiId"] as string;
            await appsync.send(
              new CreateDataSourceCommand({
                apiId,
                name: "CompatDelDS",
                type: "NONE",
              }),
            );
            await appsync.send(
              new DeleteDataSourceCommand({ apiId, name: "CompatDelDS" }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_dsApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-functions ──────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-functions",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-fn-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        const apiId = resp.graphqlApi!.apiId!;
        st(ctx)["_fnApiId"] = apiId;
        await appsync.send(
          new CreateDataSourceCommand({
            apiId,
            name: "FnNoneDS",
            type: "NONE",
          }),
        );
      },
      tests: [
        {
          name: "CreateFunction",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_fnApiId"] as string;
            const resp = await appsync.send(
              new CreateFunctionCommand({
                apiId,
                name: "CompatFn",
                dataSourceName: "FnNoneDS",
                requestMappingTemplate:
                  '{"version":"2018-05-29","payload":{}}',
                responseMappingTemplate: "$util.toJson($context.result)",
              }),
            );
            assert.ok(
              resp.functionConfiguration?.functionId,
              "missing functionId",
            );
            st(ctx)["_fnId"] = resp.functionConfiguration!.functionId;
          },
        },
        {
          name: "GetFunction",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_fnApiId"] as string;
            const fnId = st(ctx)["_fnId"] as string;
            const resp = await appsync.send(
              new GetFunctionCommand({ apiId, functionId: fnId }),
            );
            assert.ok(resp.functionConfiguration?.name, "missing name");
          },
        },
        {
          name: "UpdateFunction",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_fnApiId"] as string;
            const fnId = st(ctx)["_fnId"] as string;
            const resp = await appsync.send(
              new UpdateFunctionCommand({
                apiId,
                functionId: fnId,
                name: "CompatFnUpdated",
                dataSourceName: "FnNoneDS",
                requestMappingTemplate:
                  '{"version":"2018-05-29","payload":{}}',
                responseMappingTemplate: "$util.toJson($context.result)",
              }),
            );
            assert.ok(
              resp.functionConfiguration?.functionId,
              "missing functionId",
            );
          },
        },
        {
          name: "ListFunctions",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_fnApiId"] as string;
            const resp = await appsync.send(
              new ListFunctionsCommand({ apiId }),
            );
            assert.ok(Array.isArray(resp.functions), "expected array");
          },
        },
        {
          name: "DeleteFunction",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_fnApiId"] as string;
            const cr = await appsync.send(
              new CreateFunctionCommand({
                apiId,
                name: "CompatFnDel",
                dataSourceName: "FnNoneDS",
                requestMappingTemplate: "{}",
                responseMappingTemplate: "{}",
              }),
            );
            await appsync.send(
              new DeleteFunctionCommand({
                apiId,
                functionId: cr.functionConfiguration!.functionId!,
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_fnApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-resolvers ──────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-resolvers",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-res-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        const apiId = resp.graphqlApi!.apiId!;
        st(ctx)["_resApiId"] = apiId;
        const sdl = `type Query { hello: String, goodbye: String, extra: String }`;
        await appsync.send(
          new StartSchemaCreationCommand({
            apiId,
            definition: new TextEncoder().encode(sdl),
          }),
        );
        await appsync.send(
          new CreateDataSourceCommand({
            apiId,
            name: "ResNoneDS",
            type: "NONE",
          }),
        );
        const fnResp = await appsync.send(
          new CreateFunctionCommand({
            apiId,
            name: "ResFn",
            dataSourceName: "ResNoneDS",
            requestMappingTemplate: "{}",
            responseMappingTemplate: "{}",
          }),
        );
        st(ctx)["_resFnId"] = fnResp.functionConfiguration!.functionId;
      },
      tests: [
        {
          name: "CreateResolver",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_resApiId"] as string;
            const resp = await appsync.send(
              new CreateResolverCommand({
                apiId,
                typeName: "Query",
                fieldName: "hello",
                dataSourceName: "ResNoneDS",
                kind: "UNIT",
                requestMappingTemplate:
                  '{"version":"2018-05-29","payload":"world"}',
                responseMappingTemplate: "$util.toJson($context.result)",
              }),
            );
            assert.ok(resp.resolver?.fieldName, "missing fieldName");
          },
        },
        {
          name: "GetResolver",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_resApiId"] as string;
            const resp = await appsync.send(
              new GetResolverCommand({
                apiId,
                typeName: "Query",
                fieldName: "hello",
              }),
            );
            assert.ok(resp.resolver?.resolverArn, "missing resolverArn");
          },
        },
        {
          name: "UpdateResolver",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_resApiId"] as string;
            const resp = await appsync.send(
              new UpdateResolverCommand({
                apiId,
                typeName: "Query",
                fieldName: "hello",
                dataSourceName: "ResNoneDS",
                kind: "UNIT",
                requestMappingTemplate:
                  '{"version":"2018-05-29","payload":"updated"}',
                responseMappingTemplate: "$util.toJson($context.result)",
              }),
            );
            assert.ok(resp.resolver?.fieldName, "missing fieldName");
          },
        },
        {
          name: "ListResolvers",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_resApiId"] as string;
            const resp = await appsync.send(
              new ListResolversCommand({ apiId, typeName: "Query" }),
            );
            assert.ok(Array.isArray(resp.resolvers), "expected array");
          },
        },
        {
          name: "ListResolversByFunction",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_resApiId"] as string;
            const fnId = st(ctx)["_resFnId"] as string;
            await appsync.send(
              new CreateResolverCommand({
                apiId,
                typeName: "Query",
                fieldName: "goodbye",
                kind: "PIPELINE",
                pipelineConfig: { functions: [fnId] },
                requestMappingTemplate: "{}",
                responseMappingTemplate: "$util.toJson($context.result)",
              }),
            );
            const resp = await appsync.send(
              new ListResolversByFunctionCommand({ apiId, functionId: fnId }),
            );
            assert.ok(Array.isArray(resp.resolvers), "expected array");
            assert.ok(resp.resolvers!.length > 0, "expected at least one");
          },
        },
        {
          name: "DeleteResolver",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_resApiId"] as string;
            await appsync.send(
              new CreateResolverCommand({
                apiId,
                typeName: "Query",
                fieldName: "extra",
                dataSourceName: "ResNoneDS",
                kind: "UNIT",
              }),
            );
            await appsync.send(
              new DeleteResolverCommand({
                apiId,
                typeName: "Query",
                fieldName: "extra",
              }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_resApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-types ──────────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-types",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-types-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        const apiId = resp.graphqlApi!.apiId!;
        st(ctx)["_typesApiId"] = apiId;
        const sdl = `type Query { hello: String }`;
        await appsync.send(
          new StartSchemaCreationCommand({
            apiId,
            definition: new TextEncoder().encode(sdl),
          }),
        );
      },
      tests: [
        {
          name: "CreateType",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_typesApiId"] as string;
            const resp = await appsync.send(
              new CreateTypeCommand({
                apiId,
                definition: "type CompatType { id: ID, name: String }",
                format: TypeDefinitionFormat.SDL,
              }),
            );
            assert.ok(resp.type?.name, "missing name");
          },
        },
        {
          name: "GetType",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_typesApiId"] as string;
            const resp = await appsync.send(
              new GetTypeCommand({
                apiId,
                typeName: "CompatType",
                format: TypeDefinitionFormat.SDL,
              }),
            );
            assert.ok(resp.type?.name, "missing name");
          },
        },
        {
          name: "UpdateType",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_typesApiId"] as string;
            const resp = await appsync.send(
              new UpdateTypeCommand({
                apiId,
                typeName: "CompatType",
                definition:
                  "type CompatType { id: ID, name: String, age: Int }",
                format: TypeDefinitionFormat.SDL,
              }),
            );
            assert.ok(resp.type?.name, "missing name");
          },
        },
        {
          name: "ListTypes",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_typesApiId"] as string;
            const resp = await appsync.send(
              new ListTypesCommand({
                apiId,
                format: TypeDefinitionFormat.SDL,
              }),
            );
            assert.ok(Array.isArray(resp.types), "expected array");
          },
        },
        {
          name: "DeleteType",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_typesApiId"] as string;
            await appsync.send(
              new CreateTypeCommand({
                apiId,
                definition: "type CompatDelType { x: String }",
                format: TypeDefinitionFormat.SDL,
              }),
            );
            await appsync.send(
              new DeleteTypeCommand({ apiId, typeName: "CompatDelType" }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_typesApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-tags ───────────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-tags",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-tags-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        st(ctx)["_tagsApiId"] = resp.graphqlApi!.apiId;
        st(ctx)["_tagsApiArn"] = resp.graphqlApi!.arn;
      },
      tests: [
        {
          name: "TagResource",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const arn = st(ctx)["_tagsApiArn"] as string;
            await appsync.send(
              new TagResourceCommand({
                resourceArn: arn,
                tags: { env: "test", suite: "compat" },
              }),
            );
          },
        },
        {
          name: "ListTagsForResource",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const arn = st(ctx)["_tagsApiArn"] as string;
            const resp = await appsync.send(
              new ListTagsForResourceCommand({ resourceArn: arn }),
            );
            assert.ok(resp.tags, "missing tags");
            assert.strictEqual(resp.tags!["env"], "test");
          },
        },
        {
          name: "UntagResource",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const arn = st(ctx)["_tagsApiArn"] as string;
            await appsync.send(
              new UntagResourceCommand({
                resourceArn: arn,
                tagKeys: ["suite"],
              }),
            );
            const resp = await appsync.send(
              new ListTagsForResourceCommand({ resourceArn: arn }),
            );
            assert.strictEqual(resp.tags?.["suite"], undefined);
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_tagsApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-env-vars ───────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-env-vars",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-env-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        st(ctx)["_envApiId"] = resp.graphqlApi!.apiId;
      },
      tests: [
        {
          name: "PutGraphqlApiEnvironmentVariables",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_envApiId"] as string;
            const resp = await appsync.send(
              new PutGraphqlApiEnvironmentVariablesCommand({
                apiId,
                environmentVariables: {
                  DB_HOST: "localhost",
                  DB_PORT: "5432",
                },
              }),
            );
            assert.ok(resp.environmentVariables, "missing result");
          },
        },
        {
          name: "GetGraphqlApiEnvironmentVariables",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_envApiId"] as string;
            const resp = await appsync.send(
              new GetGraphqlApiEnvironmentVariablesCommand({ apiId }),
            );
            assert.ok(resp.environmentVariables, "missing result");
            assert.strictEqual(resp.environmentVariables!["DB_HOST"], "localhost");
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_envApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },

    // ── appsync-domains ────────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-domains",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-dom-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        st(ctx)["_domApiId"] = resp.graphqlApi!.apiId;
      },
      tests: [
        {
          name: "CreateDomainName",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const domainName = `${ctx.runId}.example.com`;
            const resp = await appsync.send(
              new CreateDomainNameCommand({
                domainName,
                certificateArn:
                  "arn:aws:acm:us-east-1:000000000000:certificate/test",
              }),
            );
            assert.ok(
              resp.domainNameConfig?.domainName,
              "missing domainName",
            );
            st(ctx)["_domainName"] = domainName;
          },
        },
        {
          name: "GetDomainName",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const domainName = st(ctx)["_domainName"] as string;
            const resp = await appsync.send(
              new GetDomainNameCommand({ domainName }),
            );
            assert.ok(
              resp.domainNameConfig?.domainName,
              "missing domainName",
            );
          },
        },
        {
          name: "UpdateDomainName",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const domainName = st(ctx)["_domainName"] as string;
            const resp = await appsync.send(
              new UpdateDomainNameCommand({
                domainName,
                description: "updated-domain",
              }),
            );
            assert.ok(
              resp.domainNameConfig?.domainName,
              "missing domainName",
            );
          },
        },
        {
          name: "ListDomainNames",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const resp = await appsync.send(
              new ListDomainNamesCommand({}),
            );
            assert.ok(
              Array.isArray(resp.domainNameConfigs),
              "expected array",
            );
          },
        },
        {
          name: "AssociateApi",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const domainName = st(ctx)["_domainName"] as string;
            const apiId = st(ctx)["_domApiId"] as string;
            const resp = await appsync.send(
              new AssociateApiCommand({ domainName, apiId }),
            );
            assert.ok(resp.apiAssociation, "missing apiAssociation");
          },
        },
        {
          name: "GetApiAssociation",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const domainName = st(ctx)["_domainName"] as string;
            const resp = await appsync.send(
              new GetApiAssociationCommand({ domainName }),
            );
            assert.ok(resp.apiAssociation, "missing apiAssociation");
          },
        },
        {
          name: "DisassociateApi",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const domainName = st(ctx)["_domainName"] as string;
            await appsync.send(
              new DisassociateApiCommand({ domainName }),
            );
          },
        },
        {
          name: "DeleteDomainName",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const dn = `del-${ctx.runId}.example.com`;
            await appsync.send(
              new CreateDomainNameCommand({
                domainName: dn,
                certificateArn:
                  "arn:aws:acm:us-east-1:000000000000:certificate/test",
              }),
            );
            await appsync.send(
              new DeleteDomainNameCommand({ domainName: dn }),
            );
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const domainName = st(ctx)["_domainName"] as string;
        const apiId = st(ctx)["_domApiId"] as string;
        if (domainName) {
          try {
            await appsync.send(
              new DisassociateApiCommand({ domainName }),
            );
          } catch {}
          try {
            await appsync.send(
              new DeleteDomainNameCommand({ domainName }),
            );
          } catch {}
        }
        if (apiId) {
          try {
            await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
          } catch {}
        }
      },
    },

    // ── appsync-cache ──────────────────────────────────────────────────────
    {
      suite,
      service: "appsync",
      name: "appsync-cache",
      setup: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const resp = await appsync.send(
          new CreateGraphqlApiCommand({
            name: `compat-cache-${ctx.runId}`,
            authenticationType: AuthenticationType.API_KEY,
          }),
        );
        st(ctx)["_cacheApiId"] = resp.graphqlApi!.apiId;
      },
      tests: [
        {
          name: "CreateApiCache",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_cacheApiId"] as string;
            const resp = await appsync.send(
              new CreateApiCacheCommand({
                apiId,
                type: "T2_SMALL",
                apiCachingBehavior: "FULL_REQUEST_CACHING",
                ttl: 300,
              }),
            );
            assert.ok(resp.apiCache, "missing apiCache");
          },
        },
        {
          name: "GetApiCache",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_cacheApiId"] as string;
            const resp = await appsync.send(
              new GetApiCacheCommand({ apiId }),
            );
            assert.ok(resp.apiCache, "missing apiCache");
          },
        },
        {
          name: "UpdateApiCache",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_cacheApiId"] as string;
            const resp = await appsync.send(
              new UpdateApiCacheCommand({
                apiId,
                type: "T2_MEDIUM",
                apiCachingBehavior: "FULL_REQUEST_CACHING",
                ttl: 600,
              }),
            );
            assert.ok(resp.apiCache, "missing apiCache");
          },
        },
        {
          name: "FlushApiCache",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_cacheApiId"] as string;
            await appsync.send(new FlushApiCacheCommand({ apiId }));
          },
        },
        {
          name: "DeleteApiCache",
          fn: async (ctx) => {
            const { appsync } = makeClients(ctx);
            const apiId = st(ctx)["_cacheApiId"] as string;
            await appsync.send(new DeleteApiCacheCommand({ apiId }));
          },
        },
      ],
      teardown: async (ctx) => {
        const { appsync } = makeClients(ctx);
        const apiId = st(ctx)["_cacheApiId"] as string;
        if (!apiId) return;
        try {
          await appsync.send(new DeleteApiCacheCommand({ apiId }));
        } catch {}
        try {
          await appsync.send(new DeleteGraphqlApiCommand({ apiId }));
        } catch {}
      },
    },
  ];
}
