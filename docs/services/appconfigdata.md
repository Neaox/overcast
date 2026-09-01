---
title: "AppConfigData — AWS AppConfig Data Plane"
description: "The AppConfig runtime data plane: a poll-based session that hands back the latest configuration content, with single-use rotating tokens."
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
| Bindings | The two AWS models: `POST /configurationsessions` and `GET /configuration?configuration_token={token}`. The token is a **query parameter**, not a path segment — that is how every AWS SDK sends it. `StartConfigurationSession` answers `201`. |
| Token rotation | Every `GetLatestConfiguration` response carries a `Next-Poll-Configuration-Token` header, and **you must use it for the next poll**. Tokens are single-use, rotate on every call, and expire 24 hours after they are issued. |
| Unchanged configuration | HTTP `200` with an **empty payload** and no `Content-Type`, which is what stops a well-behaved polling SDK re-applying config it already has. |
| Poll interval | `RequiredMinimumPollIntervalInSeconds` (15–86400) is honoured and echoed as `Next-Poll-Interval-In-Seconds`, defaulting to `60`. A poll arriving inside the window is refused while the token stays valid. |

## Differences from AWS

| Area | Overcast | AWS |
| --- | --- | --- |
| `Version-Label` response header | Never sent, even for a hosted version that has one | Sent when the version is labelled |
| Deployments | Not modelled, so `GetLatestConfiguration` returns the newest hosted version rather than the deployed one | Returns what the deployment has rolled out |
| Encryption and KMS | Not modelled | Supported |

## Gotchas

> [!WARNING]
> A configuration token is spent by the call that uses it. Re-polling with the
> token you already used fails; keep the `Next-Poll-Configuration-Token` from each
> response. This is AWS's own contract, and the mistake is easy to make when a
> test retries a request.

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
- [AWS API reference](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
