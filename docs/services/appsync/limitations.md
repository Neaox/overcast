---
title: "AppSync limitations"
description: "Where Overcast's AppSync diverges from AWS: which authorization modes are verified, which data source types execute, and what is stored without behaviour."
section: "Service Reference"
tags:
  - appsync
  - docs
  - limitations
  - services
---

# AppSync limitations

Every divergence from real AppSync. The working set is on
[AppSync](../appsync.md).

## Authorization

| Mode | Overcast |
| --- | --- |
| `API_KEY` | Verified. The key must exist and must not have expired |
| `AWS_LAMBDA` | Verified. The authorizer function is invoked, `isAuthorized` decides, `identityValidationExpression` is applied as a regex, `resolverContext` reaches `$context.identity`, `deniedFields` are enforced per field, and results are cached for `authorizerResultTtlInSeconds`. Accept-all only when no `authorizerUri` is configured |
| `AMAZON_COGNITO_USER_POOLS` | A `Bearer` token must be present. The payload is decoded so claims reach `$context.identity`, but the **signature and `exp` are not checked** — unless `OVERCAST_ENFORCE_APPSYNC_COGNITO_AUTH=true`, which verifies the token against the local Cognito pool the API names (signature, issuer, `token_use`, `exp`, `appIdClientRegex`) |
| `OPENID_CONNECT` | A `Bearer` token must be present and its payload is decoded, plus an issuer override from configuration. The signature and `exp` are never checked; there is no enforcement switch for this mode |
| `AWS_IAM` | Accepted unconditionally. The access key is read out of the header; no SigV4 signature is verified |

Multi-auth through `additionalAuthenticationProviders` works as a fallback
chain over the modes above, under the same rules — a Cognito entry there is
verified exactly when the primary mode would be.

Under `OVERCAST_ENFORCE_APPSYNC_COGNITO_AUTH` the token must come from
Overcast's own Cognito: verification reads the pool's key from local state, so
a token minted by real AWS Cognito is refused. Rejections carry AppSync's
`UnauthorizedException` and HTTP `401`.

> [!CAUTION]
> By default, three of the five modes above accept a token nobody signed. Do
> not use Overcast to prove that an unauthorized caller is refused, even with
> Cognito enforcement on: `OPENID_CONNECT` and `AWS_IAM` are unaffected by it.

## Data sources

`NONE`, `HTTP`, `AWS_LAMBDA` and `AMAZON_DYNAMODB` resolve at execution time.
`AMAZON_OPENSEARCH_SERVICE`, `AMAZON_ELASTICSEARCH`, `RELATIONAL_DATABASE`,
`AMAZON_EVENTBRIDGE` and `AMAZON_BEDROCK_RUNTIME` are accepted by
`CreateDataSource` and stored, but a resolver bound to one fails when the field
is executed.

`AMAZON_DYNAMODB` resolvers forward to the local DynamoDB emulator and support
`GetItem`, `PutItem`, `DeleteItem`, `UpdateItem`, `Query`, `Scan`,
`BatchGetItem`, `BatchWriteItem`, `TransactGetItems` and `TransactWriteItems`.

## Stored without behaviour

| Feature | What is missing |
| --- | --- |
| API cache | Configuration is stored for CDK and CloudFormation; no resolver result is ever cached |
| Domain names | `appsyncDomainName` and `hostedZoneId` are synthetic. No DNS record is created and no certificate is validated |
| `logConfig`, `userPoolConfig`, `openIDConnectConfig` and other nested configs | Stored and returned as passthrough JSON, without validation |

## Evaluation endpoints

`EvaluateCode` and `EvaluateMappingTemplate` take no `apiId` and need no API to
exist. `context` is a JSON **string**, as AWS models it, not an object. A fault
in the evaluated code or template returns HTTP 200 with an `error` member,
matching the modelled response.

`evaluationResult`, `error`, `stash` and — for `EvaluateCode` only — `logs` are
populated. `outErrors` never is: neither evaluator collects
`util.appendError` output.

## Subscriptions

Fan-out is by naming convention, not by schema analysis: a mutation on field
`createFoo` notifies subscribers of `onCreateFoo`. A subscription whose name
does not follow that pattern receives nothing, and `@aws_subscribe` directives
are not read.

The subscription manager is in-process, so connections and their filters do not
survive a restart.

## Related

- [AppSync](../appsync.md) — quick start and what works
- [AppSync operations](./operations.md) — per-operation status
- [Networking and host-based addressing](../../networking.md)
