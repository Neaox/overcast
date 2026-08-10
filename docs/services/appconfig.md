---
title: "AppConfig — AWS AppConfig"
description: "AWS AppConfig uses the REST JSON protocol. Routes are served under the /_appconfig/ path prefix."
section: "Service Reference"
tags:
  - appconfig
  - aws
  - docs
  - services
---

# AppConfig — AWS AppConfig

> AWS docs: https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/

AWS AppConfig uses the REST JSON protocol. Routes are served under the
`/_appconfig/` path prefix.

---

## Notes

- REST routes are prefixed with `/_appconfig/` (e.g. `POST /_appconfig/applications`).
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Resources are stored in-memory with hierarchical relationships (Application → Environment, Application → ConfigurationProfile).

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                      | ✅ Supported | 🚧 WIP |
| ----------------------------- | ------------ | ------ |
| Applications                  | 4            |        |
| Environments                  |              | 4      |
| Configuration Profiles        |              | 4      |
| Hosted Configuration Versions |              | 4      |
| Tags                          | 3            |        |

---

## Endpoints

### Applications

| Operation           | Status       | Notes                       | AWS Docs                                                                                         |
| ------------------- | ------------ | --------------------------- | ------------------------------------------------------------------------------------------------ |
| `CreateApplication` | ✅ Supported | Creates an application      | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateApplication.html) |
| `GetApplication`    | ✅ Supported | Returns application details | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetApplication.html)    |
| `ListApplications`  | ✅ Supported | Lists all applications      | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListApplications.html)  |
| `DeleteApplication` | ✅ Supported | Deletes an application      | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteApplication.html) |

### Environments

| Operation           | Status | Notes                                                                                                                  | AWS Docs                                                                                         |
| ------------------- | ------ | ---------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `CreateEnvironment` | 🚧 WIP | Creates an environment for an application; Overcast does not serve the binding AWS models, so no SDK reaches it (#854) | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateEnvironment.html) |
| `GetEnvironment`    | 🚧 WIP | Returns environment details; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)               | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetEnvironment.html)    |
| `ListEnvironments`  | 🚧 WIP | Lists environments for an application; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)     | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListEnvironments.html)  |
| `DeleteEnvironment` | 🚧 WIP | Deletes an environment; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)                    | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteEnvironment.html) |

### Configuration Profiles

| Operation                    | Status | Notes                                                                                                              | AWS Docs                                                                                                  |
| ---------------------------- | ------ | ------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| `CreateConfigurationProfile` | 🚧 WIP | Creates a configuration profile; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)       | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateConfigurationProfile.html) |
| `GetConfigurationProfile`    | 🚧 WIP | Returns configuration profile details; Overcast does not serve the binding AWS models, so no SDK reaches it (#854) | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetConfigurationProfile.html)    |
| `ListConfigurationProfiles`  | 🚧 WIP | Lists configuration profiles; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)          | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListConfigurationProfiles.html)  |
| `DeleteConfigurationProfile` | 🚧 WIP | Deletes a configuration profile; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)       | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteConfigurationProfile.html) |

### Hosted Configuration Versions

| Operation                          | Status | Notes                                                                                                                                                                                                                                                                            | AWS Docs                                                                                                        |
| ---------------------------------- | ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `CreateHostedConfigurationVersion` | 🚧 WIP | Stores configuration content against a profile; `VersionNumber` auto-increments, `ContentType` defaults to `application/octet-stream`, and content over 1 MB is rejected with `BadRequestException`; Overcast does not serve the binding AWS models, so no SDK reaches it (#854) | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateHostedConfigurationVersion.html) |
| `GetHostedConfigurationVersion`    | 🚧 WIP | Returns the raw content as the response body with the `AppConfig-*` metadata headers; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)                                                                                                                | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetHostedConfigurationVersion.html)    |
| `ListHostedConfigurationVersions`  | 🚧 WIP | Returns version metadata without content; no pagination (`NextToken` never returned); Overcast does not serve the binding AWS models, so no SDK reaches it (#854)                                                                                                                | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListHostedConfigurationVersions.html)  |
| `DeleteHostedConfigurationVersion` | 🚧 WIP | Deletes a single version; other versions of the profile are untouched; Overcast does not serve the binding AWS models, so no SDK reaches it (#854)                                                                                                                               | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteHostedConfigurationVersion.html) |

### Tags

| Operation             | Status       | Notes                                                                             | AWS Docs                                                                                           |
| --------------------- | ------------ | --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Adds or overwrites tags on applications, environments, and configuration profiles | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tags by key from applications, environments, and configuration profiles   | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tags for applications, environments, and configuration profiles           | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
