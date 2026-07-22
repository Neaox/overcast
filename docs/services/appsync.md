---
title: "AppSync — endpoint support"
description: "AppSync uses REST-JSON under the /v1/apis and /v2/apis path prefixes. Overcast implements GraphQL API management, schema upload, API key CRUD, data source CRUD, function CRUD,..."
section: "Service Reference"
tags:
  - appsync
  - docs
  - endpoint
  - services
  - support
---

# AppSync — endpoint support

> AWS docs: [AppSync API Reference](https://docs.aws.amazon.com/appsync/latest/APIReference/Welcome.html)

AppSync uses REST-JSON under the `/v1/apis` and `/v2/apis` path prefixes. Overcast implements
GraphQL API management, schema upload, API key CRUD, data source CRUD, function
CRUD, resolver CRUD, types CRUD, tagging, and GraphQL query execution with full
authentication (API_KEY, Cognito, OIDC, Lambda, IAM, multi-auth), NONE, HTTP,
AWS_LAMBDA, and AMAZON_DYNAMODB data source resolution, pipeline resolvers,
argument passing, nested field resolution, operationName selection, mutation
support, VTL mapping template evaluation (including `$util.transform`,
`$util.http`, `$util.str`, `$ctx.info.selectionSetGraphQL`), APPSYNC_JS resolver
runtime (including `util.transform`, `util.http`, extended `util.str`,
`ctx.info.selectionSetGraphQL`), GraphQL introspection (`__schema`, `__type`,
`__typename`, fragment expansion, `IntrospectionConfig` enforcement),
DynamoDB batch and transact operations (BatchGetItem, BatchWriteItem,
TransactGetItems, TransactWriteItems), real-time WebSocket subscriptions
with mutation fan-out, merged API management (source API associations with
automatic schema merging), CloudFormation/CDK provisioning for common GraphQL
API stacks, and Events API (v2) with channel namespace CRUD.

> **Emulation tier: Config + Execution** — GraphQL APIs and all sub-resources
> are stored and managed via the AWS SDK. Schema uploads are validated (SDL parsed,
> Query type required). GraphQL queries and mutations can be executed against NONE,
> HTTP, AWS_LAMBDA, and AMAZON_DYNAMODB data sources with full authentication
> (API_KEY, Cognito, OIDC, Lambda, IAM) and multi-auth support.
> Multi-operation documents are supported via operationName. Field arguments are
> extracted and passed to resolvers. Nested field resolution allows child types to
> have their own resolvers. DynamoDB data sources support GetItem, PutItem,
> DeleteItem, Query, Scan, UpdateItem, BatchGetItem, BatchWriteItem,
> TransactGetItems, and TransactWriteItems operations. APPSYNC_JS resolver runtime
> is supported using a pure-Go JS engine (goja) with expanded `@aws-appsync/utils`
> module. VTL mapping template evaluation uses a full Go interpreter. Real-time
> subscriptions are supported via WebSocket at `/_appsync/{apiId}/realtime` with
> mutation-to-subscription fan-out. Error enrichment: `$util.error` errorType
> and data are propagated into `extensions.errorType`/`extensions.data`
> (both VTL and APPSYNC_JS runtimes), and field resolver errors include a `path`
> array for accurate client debugging. Merged API management includes source API
> association, automatic schema merging via gqlparser, and schema re-merge on
> demand. Events API (v2) supports full CRUD for Event APIs and channel namespaces
> with SigV4 service-name dispatch to coexist with API Gateway v2 on `/v2/apis`.

---

## Notes

- **Schema validation.** Uploaded SDL is parsed and validated using gqlparser. Invalid
  SDL or schemas without a Query type are rejected with 400 BadRequestException.
  Parsed schemas are cached in-memory for query validation and execution.
- **GraphQL execution.** The `POST /_appsync/{apiId}/graphql` endpoint executes
  queries and mutations against NONE, HTTP, AWS_LAMBDA, and AMAZON_DYNAMODB data
  sources. Both UNIT and PIPELINE resolvers are supported. PIPELINE resolvers
  execute functions in order, with the last function's result returned. For NONE
  resolvers, the `payload` field from the `requestMappingTemplate` JSON becomes
  the field result. For HTTP resolvers, the request template's `resourcePath`,
  `method`, `headers`, and `body` fields are used to proxy to the data source's
  configured endpoint. For AWS_LAMBDA resolvers, the configured Lambda function is
  invoked synchronously with an AppSync resolver event containing `arguments`,
  `source`, `info` (fieldName, parentTypeName, selectionSetList), and `request`
  headers. For AMAZON_DYNAMODB resolvers, the request mapping template specifies
  the operation (GetItem, PutItem, DeleteItem, Query, Scan, UpdateItem) and is
  forwarded to the local DynamoDB emulator via the DynamoDB invoker. Multi-operation
  documents require an `operationName` parameter. Field arguments are extracted
  from the query AST and resolved against variables. Nested field resolution allows
  child types to have their own resolvers (e.g. `Author.posts` can resolve
  independently from `Query.author`). Sub-field selection is applied to resolved
  objects.
- **Types API.** Types can be created explicitly via CreateType or derived
  automatically from the uploaded schema. ListTypes merges both sources.
  GetType checks the store first, then falls back to the parsed schema.
- **ListResolversByFunction.** Scans all resolvers for an API and filters by
  those whose pipeline config references the given function ID.
- **API_KEY authentication.** When an API's `authenticationType` is `API_KEY`,
  requests must include a valid `x-api-key` header. Expired keys are rejected.
- **Full authentication.** All five auth types are supported: API_KEY (key
  validation with expiry), AMAZON_COGNITO_USER_POOLS (Bearer token + JWT claims
  parsing), OPENID_CONNECT (Bearer token + issuer), AWS_LAMBDA (accept-all
  stub), and AWS_IAM (SigV4 stub accepts all). Multi-auth is supported via
  `additionalAuthenticationProviders` with fallback chain. Identity claims are
  propagated through `$context.identity` in both VTL and JS resolvers.
- **VTL mapping templates.** Full Go VTL interpreter supporting $context/$ctx
  references, #set/#if/#elseif/#else/#foreach/#return directives, $util (toJson,
  parseJson, autoId, isNull, matches, error, validate), $util.time.*,
  $util.dynamodb.\*, string/map/list methods, quiet references ($!), and
  nested property assignment. Used for requestMappingTemplate and
  responseMappingTemplate on resolvers and functions.
- **APPSYNC_JS runtime.** Expanded `@aws-appsync/utils` module: util.dynamodb
  (full: toDynamoDB, toMapValues, toBoolean, toNull, toList, toMap, toStringSet,
  toNumberSet, etc.), util.str (toLower, toUpper, toReplace, normalize),
  util.math (roundNum, minVal, maxVal, randomDouble, randomWithinRange), type
  checking (isNull, isString, isList, isMap, isNumber, isBoolean), null
  coalescing (defaultIfNull, defaultIfNullOrEmpty), util.matches, util.validate.
  ctx.env injected from EnvironmentVariables store.
- **Real-time subscriptions.** WebSocket endpoint at `/_appsync/{apiId}/realtime`
  using the AppSync real-time protocol: connection_init→connection_ack,
  start→start_ack, stop→complete, ka (30s keepalive). Mutations automatically
  fan out to matching subscriptions (convention: mutation `createFoo` → subscription
  `onCreateFoo`). Connection lifecycle managed by in-memory subscription manager.
- **Config-level emulation.** All resources are stored for CDK/IaC compatibility.
- **CloudFormation/CDK provisioning.** CloudFormation provisions real AppSync state for `AWS::AppSync::GraphQLApi`, `GraphQLSchema`, `ApiKey`, `DataSource`, `Resolver`, and `FunctionConfiguration`. CDK-style references such as `Fn::GetAtt GraphqlApi.ApiId`, `Fn::GetAtt ApiKey.ApiKey`, and `Fn::GetAtt Function.FunctionId` are supported, and stack-created APIs execute through `POST /_appsync/{apiId}/graphql`.
- **Cascade delete.** Deleting a GraphQL API removes all child resources (schema, keys,
  data sources, functions, resolvers). Deleting an Event API removes all its channel
  namespaces. Deleting a domain name removes its API association.
- **REST-JSON protocol.** Operations are path-routed: `POST /v1/apis`, `GET /v1/apis/{apiId}`, etc.
- **GraphQL API creation.** `CreateGraphqlApi` returns HTTP 200 and validates required top-level fields/enums per AWS docs; nested auth/log/metrics configs are stored as passthrough JSON.
- **Complex config passthrough.** Nested configs (logConfig, userPoolConfig, openIDConnectConfig,
  etc.) are stored and returned as-is without validation.
- **Environment variables.** Validates max 50 entries, key length 2–64 chars, value ≤512 chars.
- **Domain names.** Generates synthetic `appsyncDomainName` and `hostedZoneId` values.
  No real DNS or certificate validation.
- **API cache.** Cosmetic — stores config for CDK/CloudFormation compatibility.
  No actual resolver-level caching.
- **Merged APIs.** Source APIs can be associated with a MERGED-type GraphQL API.
  On association, source schemas are merged via gqlparser and stored as the merged
  API's schema. Disassociating a source re-merges the remaining sources.
  `StartSchemaMerge` triggers an on-demand re-merge.
- **Events API.** Event APIs are managed under `/v2/apis` (shared path with API
  Gateway v2, disambiguated via SigV4 service-name in the credential scope).
  Full CRUD with auto-generated apiId, ARN, and DNS endpoints.
- **Channel namespaces.** Each Event API can have multiple channel namespaces
  with publish/subscribe auth modes and optional code handlers. Namespaces are
  cascade-deleted when their parent Event API is removed.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                     | ✅ Supported |
| ---------------------------- | ------------ |
| GraphQL APIs                 | 5            |
| Schemas                      | 3            |
| API Keys                     | 4            |
| Data Sources                 | 5            |
| Functions                    | 5            |
| Resolvers                    | 6            |
| Tags                         | 3            |
| Environment Variables        | 2            |
| Domain Names                 | 5            |
| API Associations             | 3            |
| API Cache                    | 5            |
| Types                        | 5            |
| Merged APIs                  | 7            |
| Events API                   | 5            |
| Channel Namespaces           | 5            |
| Execution & Evaluation       | 3            |
| DynamoDB Resolver Operations | 11           |

---

## Endpoints

### GraphQL APIs

| Operation          | Status       | Notes | AWS Docs                                                                                  |
| ------------------ | ------------ | ----- | ----------------------------------------------------------------------------------------- |
| `CreateGraphqlApi` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateGraphqlApi.html) |
| `GetGraphqlApi`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetGraphqlApi.html)    |
| `ListGraphqlApis`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListGraphqlApis.html)  |
| `UpdateGraphqlApi` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateGraphqlApi.html) |
| `DeleteGraphqlApi` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteGraphqlApi.html) |

### Schemas

| Operation                 | Status       | Notes | AWS Docs                                                                                         |
| ------------------------- | ------------ | ----- | ------------------------------------------------------------------------------------------------ |
| `StartSchemaCreation`     | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_StartSchemaCreation.html)     |
| `GetSchemaCreationStatus` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetSchemaCreationStatus.html) |
| `GetIntrospectionSchema`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetIntrospectionSchema.html)  |

### API Keys

| Operation      | Status       | Notes | AWS Docs                                                                              |
| -------------- | ------------ | ----- | ------------------------------------------------------------------------------------- |
| `CreateApiKey` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateApiKey.html) |
| `ListApiKeys`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListApiKeys.html)  |
| `UpdateApiKey` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateApiKey.html) |
| `DeleteApiKey` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteApiKey.html) |

### Data Sources

| Operation          | Status       | Notes                                                                                                             | AWS Docs                                                                                  |
| ------------------ | ------------ | ----------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `CreateDataSource` | ✅ Supported | AMAZON_DYNAMODB, AWS_LAMBDA, HTTP, AMAZON_OPENSEARCH_SERVICE, RELATIONAL_DATABASE, NONE, AMAZON_EVENTBRIDGE types | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateDataSource.html) |
| `GetDataSource`    | ✅ Supported |                                                                                                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetDataSource.html)    |
| `ListDataSources`  | ✅ Supported |                                                                                                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListDataSources.html)  |
| `UpdateDataSource` | ✅ Supported |                                                                                                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateDataSource.html) |
| `DeleteDataSource` | ✅ Supported |                                                                                                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteDataSource.html) |

### Functions

| Operation        | Status       | Notes | AWS Docs                                                                                |
| ---------------- | ------------ | ----- | --------------------------------------------------------------------------------------- |
| `CreateFunction` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateFunction.html) |
| `GetFunction`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetFunction.html)    |
| `ListFunctions`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListFunctions.html)  |
| `UpdateFunction` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateFunction.html) |
| `DeleteFunction` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteFunction.html) |

### Resolvers

| Operation                 | Status       | Notes                                                                        | AWS Docs                                                                                         |
| ------------------------- | ------------ | ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `CreateResolver`          | ✅ Supported | UNIT and PIPELINE resolvers; requestMappingTemplate, responseMappingTemplate | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateResolver.html)          |
| `GetResolver`             | ✅ Supported |                                                                              | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetResolver.html)             |
| `ListResolvers`           | ✅ Supported |                                                                              | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListResolvers.html)           |
| `UpdateResolver`          | ✅ Supported |                                                                              | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateResolver.html)          |
| `DeleteResolver`          | ✅ Supported |                                                                              | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteResolver.html)          |
| `ListResolversByFunction` | ✅ Supported |                                                                              | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListResolversByFunction.html) |

### Tags

| Operation             | Status       | Notes | AWS Docs                                                                                     |
| --------------------- | ------------ | ----- | -------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListTagsForResource.html) |

### Environment Variables

| Operation                           | Status       | Notes | AWS Docs                                                                                                   |
| ----------------------------------- | ------------ | ----- | ---------------------------------------------------------------------------------------------------------- |
| `PutGraphqlApiEnvironmentVariables` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_PutGraphqlApiEnvironmentVariables.html) |
| `GetGraphqlApiEnvironmentVariables` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetGraphqlApiEnvironmentVariables.html) |

### Domain Names

| Operation          | Status       | Notes                             | AWS Docs                                                                                  |
| ------------------ | ------------ | --------------------------------- | ----------------------------------------------------------------------------------------- |
| `CreateDomainName` | ✅ Supported | Inert metadata; no routing effect | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateDomainName.html) |
| `GetDomainName`    | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetDomainName.html)    |
| `ListDomainNames`  | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListDomainNames.html)  |
| `UpdateDomainName` | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateDomainName.html) |
| `DeleteDomainName` | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteDomainName.html) |

### API Associations

| Operation           | Status       | Notes | AWS Docs                                                                                   |
| ------------------- | ------------ | ----- | ------------------------------------------------------------------------------------------ |
| `AssociateApi`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_AssociateApi.html)      |
| `GetApiAssociation` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetApiAssociation.html) |
| `DisassociateApi`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DisassociateApi.html)   |

### API Cache

| Operation        | Status       | Notes                                     | AWS Docs                                                                                |
| ---------------- | ------------ | ----------------------------------------- | --------------------------------------------------------------------------------------- |
| `CreateApiCache` | ✅ Supported | Config stored; no actual caching enforced | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateApiCache.html) |
| `GetApiCache`    | ✅ Supported |                                           | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetApiCache.html)    |
| `UpdateApiCache` | ✅ Supported |                                           | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateApiCache.html) |
| `DeleteApiCache` | ✅ Supported |                                           | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteApiCache.html) |
| `FlushApiCache`  | ✅ Supported |                                           | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_FlushApiCache.html)  |

### Types

| Operation    | Status       | Notes | AWS Docs                                                                            |
| ------------ | ------------ | ----- | ----------------------------------------------------------------------------------- |
| `CreateType` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateType.html) |
| `GetType`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetType.html)    |
| `ListTypes`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListTypes.html)  |
| `UpdateType` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateType.html) |
| `DeleteType` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteType.html) |

### Merged APIs

| Operation                      | Status       | Notes | AWS Docs                                                                                              |
| ------------------------------ | ------------ | ----- | ----------------------------------------------------------------------------------------------------- |
| `AssociateSourceGraphqlApi`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_AssociateSourceGraphqlApi.html)    |
| `AssociateMergedGraphqlApi`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_AssociateMergedGraphqlApi.html)    |
| `GetSourceApiAssociation`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetSourceApiAssociation.html)      |
| `ListSourceApiAssociations`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListSourceApiAssociations.html)    |
| `DisassociateSourceGraphqlApi` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DisassociateSourceGraphqlApi.html) |
| `DisassociateMergedGraphqlApi` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DisassociateMergedGraphqlApi.html) |
| `StartSchemaMerge`             | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_StartSchemaMerge.html)             |

### Events API

| Operation   | Status       | Notes                              | AWS Docs                                                                           |
| ----------- | ------------ | ---------------------------------- | ---------------------------------------------------------------------------------- |
| `CreateApi` | ✅ Supported | GRAPHQL and MERGED event API types | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateApi.html) |
| `GetApi`    | ✅ Supported |                                    | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetApi.html)    |
| `ListApis`  | ✅ Supported |                                    | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListApis.html)  |
| `UpdateApi` | ✅ Supported |                                    | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateApi.html) |
| `DeleteApi` | ✅ Supported |                                    | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteApi.html) |

### Channel Namespaces

| Operation                | Status       | Notes | AWS Docs                                                                                        |
| ------------------------ | ------------ | ----- | ----------------------------------------------------------------------------------------------- |
| `CreateChannelNamespace` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_CreateChannelNamespace.html) |
| `GetChannelNamespace`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetChannelNamespace.html)    |
| `ListChannelNamespaces`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ListChannelNamespaces.html)  |
| `UpdateChannelNamespace` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateChannelNamespace.html) |
| `DeleteChannelNamespace` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteChannelNamespace.html) |

### Execution & Evaluation

| Operation                 | Status       | Notes                                        | AWS Docs                                                                                         |
| ------------------------- | ------------ | -------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `ExecuteGraphQL`          | ✅ Supported | Executes a GraphQL operation against the API | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ExecuteGraphQL.html)          |
| `EvaluateMappingTemplate` | ✅ Supported | Evaluates VTL mapping templates              | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_EvaluateMappingTemplate.html) |
| `EvaluateCode`            | ✅ Supported | Evaluates JavaScript resolver code           | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_EvaluateCode.html)            |

### DynamoDB Resolver Operations

| Operation            | Status       | Notes                                   | AWS Docs                                                                                    |
| -------------------- | ------------ | --------------------------------------- | ------------------------------------------------------------------------------------------- |
| `GetItem`            | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_GetItem.html)            |
| `PutItem`            | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_PutItem.html)            |
| `DeleteItem`         | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_DeleteItem.html)         |
| `UpdateItem`         | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_UpdateItem.html)         |
| `Query`              | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_Query.html)              |
| `Scan`               | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_Scan.html)               |
| `BatchGetItem`       | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_BatchGetItem.html)       |
| `BatchWriteItem`     | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_BatchWriteItem.html)     |
| `TransactGetItems`   | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_TransactGetItems.html)   |
| `TransactWriteItems` | ✅ Supported | DynamoDB data source resolver operation | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_TransactWriteItems.html) |
| `ConditionCheck`     | ✅ Supported | DynamoDB transact-write condition check | [docs](https://docs.aws.amazon.com/appsync/latest/APIReference/API_ConditionCheck.html)     |

<!-- END overcast:capabilities -->
