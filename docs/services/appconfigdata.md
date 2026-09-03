---
title: "AppConfigData — AWS AppConfig Data Plane"
description: "Quick start, the two AWS bindings, single-use token rotation and the poll interval, and the response headers and deployment behaviour that are not modelled."
section: "Service Reference"
tags:
  - appconfig
  - appconfigdata
  - docs
  - services
---

# AppConfigData — AWS AppConfig Data Plane

The runtime half of [AppConfig](./appconfig.md): a poll-based session that hands
back the latest configuration content, on single-use rotating tokens.

**Status:** ⚠️ Partial

## Quick start

Given an application, environment and configuration profile with a hosted version
(see [AppConfig](./appconfig.md)):

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

TOKEN=$(aws appconfigdata start-configuration-session \
  --application-identifier my-app \
  --environment-identifier dev \
  --configuration-profile-identifier cfg \
  --query InitialConfigurationToken --output text)

aws appconfigdata get-latest-configuration \
  --configuration-token "$TOKEN" config.json && cat config.json
```

Identifiers may be names or IDs. Nothing has to be deployed first — a hosted
version is readable as soon as it is created.

## What works

| Area | Behaviour |
| --- | --- |
| Bindings | `POST /configurationsessions` and `GET /configuration?configuration_token={token}`, with the token as a query parameter. `StartConfigurationSession` answers `201` |
| Token rotation | Every `GetLatestConfiguration` response carries a `Next-Poll-Configuration-Token` header. Tokens are single-use, rotate on every call, and expire 24 hours after they are issued |
| Unchanged configuration | HTTP `200` with an empty payload and no `Content-Type`, so a polling SDK does not re-apply configuration it already has |
| Poll interval | `RequiredMinimumPollIntervalInSeconds` (15–86400) is honoured and echoed as `Next-Poll-Interval-In-Seconds`, defaulting to `60` |

A poll arriving inside the interval window is refused, and the token it used
stays valid for the retry.

## Differences from AWS

| Area                            | On AWS                                     | Overcast                                                                                                 |
| ------------------------------- | ------------------------------------------ | -------------------------------------------------------------------------------------------------------- |
| `Version-Label` response header | Sent when the version is labelled          | Never sent, even for a hosted version that has one                                                       |
| Deployments                     | Returns what the deployment has rolled out | Not modelled, so `GetLatestConfiguration` returns the newest hosted version rather than the deployed one |
| Encryption and KMS              | Supported                                  | Not modelled                                                                                             |

## Gotchas

> [!WARNING]
> A configuration token is spent by the call that uses it. Re-polling with the
> token you already used fails; keep the `Next-Poll-Configuration-Token` from
> each response, as you would against AWS.

AppConfigData reads only; the configuration itself is created on the control
plane.

> [!IMPORTANT]
> Configuration content is written through the AppConfig **control plane**
> (`POST /applications/{app}/configurationprofiles/{profile}/hostedconfigurationversions`),
> and that request must carry an `appconfig` SigV4 credential scope — `/applications`
> is shared with AppRegistry. Both services must be enabled; they are by default.

<!-- BEGIN overcast:capabilities -->

## Operations

All 2 listed operations are implemented.
Per-operation status, notes and AWS API links: [AppConfigData operations](appconfigdata/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AppConfig](./appconfig.md) — the control plane that stores the configuration
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/)
