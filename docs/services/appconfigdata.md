---
title: "AppConfigData — AWS AppConfig Data Plane"
description: "AWS AppConfigData is the **runtime data plane** for AppConfig. Applications use it to retrieve the latest deployed configuration content via a poll-based session model. Routes are..."
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
session model. Routes are served under the `/_appconfigdata/` path prefix.

---

## Protocol

The AppConfigData data plane is a REST-JSON service served at `/_appconfigdata/*`.

### Session lifecycle

1. Call `StartConfigurationSession` with your application, environment, and
   configuration profile identifiers (name or ID both work).
2. Use the returned `InitialConfigurationToken` to call `GetLatestConfiguration`.
3. Each `GetLatestConfiguration` response includes a `Next-Poll-Configuration-Token`
   header — **you must use this token for all subsequent polls**. Tokens are
   single-use and rotate on every call.
4. When no new version has been published since the last successful delivery,
   `GetLatestConfiguration` returns HTTP 200 with an **empty body**. This matches
   the AWS behaviour that prevents well-behaved polling SDKs from re-applying
   unchanged config.

### Response headers from `GetLatestConfiguration`

| Header                            | Value                                                     |
| --------------------------------- | --------------------------------------------------------- |
| `Next-Poll-Configuration-Token`   | New opaque token (UUID)                                   |
| `Next-Poll-Interval-In-Seconds`   | `60`                                                      |
| `Content-Type`                    | As stored in the hosted version (e.g. `application/json`) |
| `AppConfig-Configuration-Version` | Integer version number (only when content is returned)    |

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

| Category         | ✅ Supported |
| ---------------- | ------------ |
| Sessions         | 2            |
| Response headers | 1            |

---

## Endpoints

### Sessions

| Operation                   | Status       | Notes                                                                     | AWS Docs                                                                                                               |
| --------------------------- | ------------ | ------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `StartConfigurationSession` | ✅ Supported | Starts a polling session; returns `InitialConfigurationToken`             | [docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_StartConfigurationSession.html) |
| `GetLatestConfiguration`    | ✅ Supported | Returns current config content; empty body when unchanged since last poll | [docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/API_appconfigdata_GetLatestConfiguration.html)    |

### Response headers

| Operation                         | Status       | Notes                                                  | AWS Docs                                                                           |
| --------------------------------- | ------------ | ------------------------------------------------------ | ---------------------------------------------------------------------------------- |
| `AppConfig-Configuration-Version` | ✅ Supported | Integer version number (only when content is returned) | [docs](https://docs.aws.amazon.com/appconfig/2021-11-11/APIReference/Welcome.html) |

<!-- END overcast:capabilities -->
