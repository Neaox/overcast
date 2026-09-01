---
title: "AppConfigData operations"
description: "Every AppConfigData operation Overcast declares — 2 of 2 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - appconfigdata
  - docs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# AppConfigData operations

All 2 listed operations are implemented. Back to [AppConfigData](../appconfigdata.md).

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
