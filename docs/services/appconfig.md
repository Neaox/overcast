---
title: "AppConfig — AWS AppConfig"
description: "Quick start, the application/environment/profile/hosted-version hierarchy, the REST-JSON bindings and pagination, and why an AppConfig call must be signed as appconfig."
section: "Service Reference"
tags:
  - appconfig
  - configuration
  - docs
  - services
---

# AppConfig — AWS AppConfig

The AppConfig control plane — applications, environments, profiles and hosted
configuration versions — with the runtime data plane in
[AppConfigData](./appconfigdata.md).

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

APP=$(aws appconfig create-application --name my-app --query Id --output text)
aws appconfig create-environment --application-id "$APP" --name dev
PROF=$(aws appconfig create-configuration-profile --application-id "$APP" \
  --name cfg --location-uri hosted --query Id --output text)

echo '{"featureX":true}' > cfg.json
aws appconfig create-hosted-configuration-version --application-id "$APP" \
  --configuration-profile-id "$PROF" --content-type application/json \
  --content fileb://cfg.json version.json
```

## What works

| Area | Behaviour |
| --- | --- |
| Resources | Applications, environments, configuration profiles and hosted configuration versions, in the hierarchy AWS models |
| Protocol | REST-JSON at AWS's own paths — `POST /applications`, `POST /applications/{ApplicationId}/environments`, and so on. There is no `X-Amz-Target` namespace |
| Hosted versions | The configuration travels as the HTTP payload, with the metadata on request and response headers — see below |
| Tagging | `/tags/{ResourceArn}`, dispatched on the ARN, so an `arn:aws:appconfig:…` ARN reaches AppConfig's own tag store |
| Pagination | `max_results` and `next_token` query parameters, answering `{ "Items": [...], "NextToken": "..." }` |

### How a hosted version is carried

`CreateHostedConfigurationVersion` requires a `Content-Type`, and takes
`Description` and `VersionLabel` as request headers. The response echoes
`Application-Id`, `Configuration-Profile-Id`, `Version-Number`, `Description`,
`Content-Type` and `VersionLabel`, as AWS binds them. `Latest-Version-Number`
gives optimistic concurrency, answering `409` on a mismatch.

## Differences from AWS

| Area                                  | On AWS                             | Overcast                                                                                                                                           |
| ------------------------------------- | ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Region scoping                        | Region-scoped                      | Resources are not region-scoped — an application created in one region is visible in another                                                       |
| Deployments and deployment strategies | Percentage-based rollout over time | Not modelled. A hosted version is readable through [AppConfigData](./appconfigdata.md) as soon as it is created — nothing has to be deployed first |
| Updating an environment or a profile  | Supported                          | Only `UpdateApplication` exists; the other update operations answer `501`                                                                          |
| Hosted version size                   | Same cap                           | Capped at 1 MB, answering `PayloadTooLargeException`                                                                                               |
| Other operations AWS models           | Implemented                        | A JSON `501 Not Implemented`                                                                                                                       |

## Gotchas

> [!IMPORTANT]
> `/applications` is shared with [Service Catalog AppRegistry](./appregistry.md),
> which models the same tree. Overcast picks the service from the SigV4 credential
> scope, so an AppConfig call must be signed as `appconfig`. An unsigned caller, or
> one whose scope cannot be parsed, reaches AppRegistry; a scope that mismatches
> gets `403 InvalidSignatureException`. Every AWS SDK and the CLI sign correctly.

<!-- BEGIN overcast:capabilities -->

## Operations

All 20 listed operations are implemented.
Per-operation status, notes and AWS API links: [AppConfig operations](appconfig/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AppConfigData](./appconfigdata.md) — the runtime data plane that reads these configurations
- [AppRegistry](./appregistry.md) — the other service on `/applications`
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/)
