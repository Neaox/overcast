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

> AWS docs: https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/

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

## Summary

| Category                      | ✅ Supported |
| ----------------------------- | ------------ |
| Applications                  | 5            |
| Environments                  | 4            |
| Configuration Profiles        | 4            |
| Hosted Configuration Versions | 4            |
| Tags                          | 3            |

---

## Endpoints

### Applications

| Operation           | Status       | Notes                                                                                       | AWS Docs                                                                                         |
| ------------------- | ------------ | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `CreateApplication` | ✅ Supported | Creates an application; an inline `Tags` map is applied                                     | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateApplication.html) |
| `GetApplication`    | ✅ Supported | Returns application details                                                                 | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetApplication.html)    |
| `ListApplications`  | ✅ Supported | Lists all applications, paginated by `max_results`/`next_token`                             | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListApplications.html)  |
| `UpdateApplication` | ✅ Supported | Updates `Name` and `Description`; an omitted member leaves the stored value alone           | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_UpdateApplication.html) |
| `DeleteApplication` | ✅ Supported | Deletes an application and its tags; environments and profiles beneath it are left in place | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteApplication.html) |

### Environments

| Operation           | Status       | Notes                                                                                                                    | AWS Docs                                                                                         |
| ------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------ |
| `CreateEnvironment` | ✅ Supported | Creates an environment for an application; `State` is always `READY_FOR_DEPLOYMENT` because deployments are not emulated | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateEnvironment.html) |
| `GetEnvironment`    | ✅ Supported | Returns environment details                                                                                              | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetEnvironment.html)    |
| `ListEnvironments`  | ✅ Supported | Lists environments for an application, paginated by `max_results`/`next_token`                                           | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListEnvironments.html)  |
| `DeleteEnvironment` | ✅ Supported | Deletes an environment and its tags; `x-amzn-deletion-protection-check` is ignored                                       | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteEnvironment.html) |

### Configuration Profiles

| Operation                    | Status       | Notes                                                                                                            | AWS Docs                                                                                                  |
| ---------------------------- | ------------ | ---------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `CreateConfigurationProfile` | ✅ Supported | Creates a configuration profile; `Name` and `LocationUri` are required and `Validators` are stored but never run | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateConfigurationProfile.html) |
| `GetConfigurationProfile`    | ✅ Supported | Returns configuration profile details                                                                            | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetConfigurationProfile.html)    |
| `ListConfigurationProfiles`  | ✅ Supported | Lists profile summaries, filtered by the `type` query parameter and paginated by `max_results`/`next_token`      | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListConfigurationProfiles.html)  |
| `DeleteConfigurationProfile` | ✅ Supported | Deletes a configuration profile and its tags; hosted versions beneath it are left in place                       | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteConfigurationProfile.html) |

### Hosted Configuration Versions

| Operation                          | Status       | Notes                                                                                                                                                                                                             | AWS Docs                                                                                                        |
| ---------------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `CreateHostedConfigurationVersion` | ✅ Supported | Stores the request payload against a profile; `VersionNumber` auto-increments, a stale `Latest-Version-Number` header is a `ConflictException`, and content over 1 MB is rejected with `PayloadTooLargeException` | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateHostedConfigurationVersion.html) |
| `GetHostedConfigurationVersion`    | ✅ Supported | Returns the raw content as the response payload with the metadata in headers                                                                                                                                      | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetHostedConfigurationVersion.html)    |
| `ListHostedConfigurationVersions`  | ✅ Supported | Returns version summaries newest first, filtered by the `version_label` query parameter and paginated by `max_results`/`next_token`                                                                               | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListHostedConfigurationVersions.html)  |
| `DeleteHostedConfigurationVersion` | ✅ Supported | Deletes a single version; other versions of the profile are untouched and version numbers are not reused                                                                                                          | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteHostedConfigurationVersion.html) |

### Tags

| Operation             | Status       | Notes                                                                             | AWS Docs                                                                                           |
| --------------------- | ------------ | --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Adds or overwrites tags on applications, environments, and configuration profiles | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tags by the `tagKeys` query parameter                                     | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tags for applications, environments, and configuration profiles           | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
