---
title: "AppConfigData — AWS AppConfig Data Plane"
description: "AWS AppConfigData is the **runtime data plane** for AppConfig. Applications use it to retrieve the latest deployed configuration content via a poll-based session model."
section: "Service Reference"
tags:
  - appconfig
  - appconfigdata
  - aws
  - data
  - docs
  - plane
  - services
---

# AppConfigData — AWS AppConfig Data Plane

> AWS docs: https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/

AWS AppConfigData is the **runtime data plane** for AppConfig. Applications use
it to retrieve the latest deployed configuration content via a poll-based
session model.

---

## Protocol

AppConfigData is a REST-JSON service, served at the two bindings AWS models:

```
POST /configurationsessions
GET  /configuration?configuration_token={token}
```

The configuration token is a **query parameter**, not a path segment — that is
how every AWS SDK sends it.

### Session lifecycle

1. Call `StartConfigurationSession` with your application, environment, and
   configuration profile identifiers (name or ID both work). Optionally set
   `RequiredMinimumPollIntervalInSeconds` (15–86400) to constrain how often the
   session may be polled.
2. Use the returned `InitialConfigurationToken` as `configuration_token` on
   `GetLatestConfiguration`.
3. Each `GetLatestConfiguration` response includes a `Next-Poll-Configuration-Token`
   header — **you must use this token for all subsequent polls**. Tokens are
   single-use, rotate on every call, and expire 24 hours after they are issued.
4. When no new version has been published since the last successful delivery,
   `GetLatestConfiguration` returns HTTP 200 with an **empty payload**. This
   matches the AWS behaviour that prevents well-behaved polling SDKs from
   re-applying unchanged config.

### Response headers from `GetLatestConfiguration`

| Header                          | Value                                                           |
| ------------------------------- | --------------------------------------------------------------- |
| `Next-Poll-Configuration-Token` | New opaque token (UUID)                                         |
| `Next-Poll-Interval-In-Seconds` | The session's `RequiredMinimumPollIntervalInSeconds`, else `60` |
| `Content-Type`                  | As stored in the hosted version (e.g. `application/json`)       |

The configuration itself is the response body. `Version-Label` is modeled here
too and is never sent: it carries the hosted configuration version's
user-defined label, which Overcast's AppConfig control plane does not store, and
AWS omits it when there is none.

---

## Relationship to AppConfig control plane

Configuration content is stored through the AppConfig control-plane API:

```
POST /_appconfig/applications/{app}/configurationprofiles/{profile}/hostedconfigurationversions
```

AppConfigData reads that stored content — both services must be enabled (they
are by default).

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Sessions | 2            |

---

## Endpoints

### Sessions

| Operation                   | Status       | Notes                                                                                                                                                                                               | AWS Docs                                                                                                               |
| --------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `StartConfigurationSession` | ✅ Supported | Starts a polling session; returns `InitialConfigurationToken`; honours `RequiredMinimumPollIntervalInSeconds`                                                                                       | [docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_StartConfigurationSession.html) |
| `GetLatestConfiguration`    | ✅ Supported | Takes the token as the `configuration_token` query parameter; returns the configuration as the response payload with the session state in headers; empty payload when unchanged since the last poll | [docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_GetLatestConfiguration.html)    |

<!-- END overcast:capabilities -->
