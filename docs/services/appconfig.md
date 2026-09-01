---
title: "AppConfig — AWS AppConfig"
description: "AWS AppConfig uses the REST JSON protocol, served at the /applications and /tags paths AWS models."
section: "Service Reference"
tags:
  - appconfig
  - aws
  - docs
  - services
---

# AppConfig — AWS AppConfig

AWS AppConfig uses the REST JSON protocol, served at the paths AWS models:
`/applications` and `/tags/{ResourceArn}`. There is no `X-Amz-Target`
namespace — the AWS models give AppConfig none.

---

## Notes

- Routes are the modeled ones, e.g. `POST /applications`,
  `POST /applications/{ApplicationId}/environments`.
- `/applications` is shared with Service Catalog AppRegistry, which models the
  same tree. Overcast picks the service from the SigV4 credential scope, so an
  AppConfig SDK call must be signed as `appconfig`; unsigned callers reach
  AppRegistry, as they always have.
- `/tags/{ResourceArn}` is dispatched on the ARN, so an `arn:aws:appconfig:…`
  ARN reaches AppConfig's own tag store.
- `CreateHostedConfigurationVersion` and `GetHostedConfigurationVersion` carry
  the configuration as the HTTP payload and the metadata in `Application-Id`,
  `Configuration-Profile-Id`, `Version-Number`, `Description`, `Content-Type`
  and `VersionLabel` headers, as AWS binds them.
- List operations page with the `max_results` and `next_token` query
  parameters and answer `{ "Items": [...], "NextToken": "..." }`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Resources are stored in-memory with hierarchical relationships (Application → Environment, Application → ConfigurationProfile).
- Resources are not region-scoped: an application created in one region is
  visible in another.
- The data plane (`StartConfigurationSession`, `GetLatestConfiguration`) is a
  separate AWS service — see [appconfigdata.md](./appconfigdata.md).

<!-- BEGIN overcast:capabilities -->

## Operations

All 20 listed operations are implemented.
Per-operation status, notes and AWS API links: [AppConfig operations](appconfig/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
