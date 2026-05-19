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

| Category               | ✅ Supported |
| ---------------------- | ------------ |
| Applications           | 4            |
| Environments           | 4            |
| Configuration Profiles | 4            |

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

| Operation           | Status       | Notes                                     | AWS Docs                                                                                         |
| ------------------- | ------------ | ----------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `CreateEnvironment` | ✅ Supported | Creates an environment for an application | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateEnvironment.html) |
| `GetEnvironment`    | ✅ Supported | Returns environment details               | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetEnvironment.html)    |
| `ListEnvironments`  | ✅ Supported | Lists environments for an application     | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListEnvironments.html)  |
| `DeleteEnvironment` | ✅ Supported | Deletes an environment                    | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteEnvironment.html) |

### Configuration Profiles

| Operation                    | Status       | Notes                                 | AWS Docs                                                                                                  |
| ---------------------------- | ------------ | ------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `CreateConfigurationProfile` | ✅ Supported | Creates a configuration profile       | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_CreateConfigurationProfile.html) |
| `GetConfigurationProfile`    | ✅ Supported | Returns configuration profile details | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_GetConfigurationProfile.html)    |
| `ListConfigurationProfiles`  | ✅ Supported | Lists configuration profiles          | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_ListConfigurationProfiles.html)  |
| `DeleteConfigurationProfile` | ✅ Supported | Deletes a configuration profile       | [docs](https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_DeleteConfigurationProfile.html) |

<!-- END overcast:capabilities -->
