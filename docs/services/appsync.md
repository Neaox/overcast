---
title: "AppSync — AWS AppSync"
description: "Quick start, the GraphQL and realtime endpoint URLs, the data sources and authorization modes that execute for real, and the ones that are only stored."
section: "Service Reference"
tags:
  - appsync
  - docs
  - graphql
  - services
---

# AppSync — AWS AppSync

GraphQL APIs execute: schemas are parsed and validated, resolvers run VTL or
APPSYNC_JS against real data sources, and mutations fan out to live
subscriptions.

**Status:** ✅ Supported

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
API=$(aws appsync create-graphql-api --name demo --authentication-type API_KEY \
  --query graphqlApi.apiId --output text)
KEY=$(aws appsync create-api-key --api-id "$API" --query apiKey.id --output text)

printf 'type Query { hello: String }' > schema.graphql
aws appsync start-schema-creation --api-id "$API" --definition fileb://schema.graphql
aws appsync create-data-source --api-id "$API" --name none --type NONE
aws appsync create-resolver --api-id "$API" --type-name Query --field-name hello \
  --data-source-name none \
  --request-mapping-template '{"version":"2018-05-29","payload":"world"}' \
  --response-mapping-template '$util.toJson($ctx.result)'

curl -s "http://localhost:4566/_overcast/appsync/apis/$API/graphql" \
  -H "x-api-key: $KEY" -d '{"query":"{ hello }"}'
```

## Endpoint URLs

A GraphQL API answers on two forms, with identical behaviour:

| Form | GraphQL | Subscriptions |
| --- | --- | --- |
| Path-style | `{base}/_overcast/appsync/apis/{apiId}/graphql` | `{base}/_overcast/appsync/apis/{apiId}/realtime` |
| Host-routed | `http://{apiId}.appsync-api.{region}.{base}/graphql` | `http://{apiId}.appsync-realtime-api.{region}.{base}` |

`uris` reports the host-routed form — what `Fn::GetAtt GraphQLUrl` passes on —
whenever the hostname you reached Overcast on can carry a subdomain. On a bare
`localhost` or an IP it reports the path-style form. Set
`OVERCAST_HOSTNAME=localhost.overcast.sh` to get the AWS shape everywhere — see
[Hostnames that resolve for every caller](../networking/hostnames.md).

The `dns` map always carries host-routed names — it holds a name, not a URL, so
it has nothing else to carry. `dns.REALTIME` repeats `dns.GRAPHQL`'s host:
Overcast serves both endpoints from one place.

## What works

| Area | Behaviour |
| --- | --- |
| Schema | SDL parsed and validated on upload; a schema without a `Query` type is rejected with `400` |
| Resolvers | UNIT and PIPELINE, VTL and APPSYNC_JS, with `$util`/`util` including `dynamodb`, `http`, `str`, `time`, `math`, `transform` |
| Data sources | `NONE`, `HTTP`, `AWS_LAMBDA` and `AMAZON_DYNAMODB` resolve at execution time, including DynamoDB batch and transact operations |
| Execution | Multi-operation documents via `operationName`, field arguments, nested field resolution, introspection, fragments |
| Subscriptions | AppSync's real-time WebSocket protocol, with mutation fan-out by convention: `createFoo` notifies `onCreateFoo` |
| Authorization | API keys with expiry, and Lambda authorizers for real — invoked per request, honouring `isAuthorized`, `identityValidationExpression`, `deniedFields` and the result TTL |
| Merged APIs | Source API association merges schemas; `StartSchemaMerge` re-merges on demand |
| Events API | Event APIs and channel namespaces under `/v2/apis`, sharing the path with API Gateway v2 |
| CloudFormation | GraphQL API, schema, key, data source, resolver, function, domain name, cache, source-API association, Event API and channel namespace resources |
| Evaluation | `EvaluateCode` and `EvaluateMappingTemplate` need no API to exist |

## Differences from AWS

| Area | Overcast |
| --- | --- |
| Cognito and OIDC authorization | A bearer token is required and its claims are decoded, but the JWT signature and expiry are **not** verified |
| IAM authorization | Accepted unconditionally; the SigV4 signature is not verified |
| Other data source types | OpenSearch, Elasticsearch, RDS, EventBridge and Bedrock Runtime are stored, but a resolver against one fails at execution |
| API cache | `CreateApiCache` stores configuration; nothing is cached |
| Domain names | Synthetic `appsyncDomainName` and `hostedZoneId`; no DNS or certificate validation |
| Complex configs | `logConfig`, `userPoolConfig`, `openIDConnectConfig` and similar are stored as passthrough JSON |
| `outErrors` | Never populated by either evaluator — `util.appendError` output is not collected |

The full list is in [AppSync limitations](./appsync/limitations.md).

## Gotchas

> [!CAUTION]
> Cognito, OIDC and IAM authorization are presence checks, not verification. A
> forged or expired token is accepted. Authorization logic that matters is only
> proven on AWS — but a Lambda authorizer *is* executed here, so that path can
> be tested locally.

Failures inside a resolver surface in the GraphQL response, not only in logs.

> [!TIP]
> `$util.error` errorType and data are propagated into
> `extensions.errorType`/`extensions.data`, and a field resolver error carries a
> `path` array. Read the `extensions` block before adding logging.

<!-- BEGIN overcast:capabilities -->

## Operations

All 82 listed operations are implemented.
Per-operation status, notes and AWS API links: [AppSync operations](appsync/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AppSync limitations](./appsync/limitations.md) — the full divergence list
- [All service pages](./README.md)
- [Networking and host-based addressing](../networking.md)
- [AWS API reference](https://docs.aws.amazon.com/appsync/latest/APIReference/Welcome.html)
