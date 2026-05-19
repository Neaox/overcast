# Secrets Manager — endpoint support

> Generated for Overcast. See also: [AWS Secrets Manager API Reference](https://docs.aws.amazon.com/secretsmanager/latest/apireference/Welcome.html)

## Summary

## Protocol

Secrets Manager accepts AWS JSON 1.1 requests via `X-Amz-Target:
secretsmanager.<Operation>` and Smithy RPC v2 CBOR requests via
`/service/secretsmanager/operation/<Operation>` with `Smithy-Protocol:
rpc-v2-cbor`.

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| Secret CRUD | 9            | 0              |
| Rotation    | 2            | 0              |
| Tags        | 2            | 0              |
| Password    | 1            | 0              |
| Policy/Misc | 0            | 7              |
| **Total**   | **14**       | **7**          |

## Endpoint details

| Operation                    | Status | Notes                              | AWS docs                                                                                                     |
| ---------------------------- | ------ | ---------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| CreateSecret                 | ✅     | String + binary, tags, description | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CreateSecret.html)                 |
| GetSecretValue               | ✅     | By name, ARN, or version ID        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetSecretValue.html)               |
| DescribeSecret               | ✅     | Metadata, tags, version map        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DescribeSecret.html)               |
| PutSecretValue               | ✅     | Creates new AWSCURRENT version     | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutSecretValue.html)               |
| UpdateSecret                 | ✅     | Description + optional new value   | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UpdateSecret.html)                 |
| ListSecrets                  | ✅     | All secrets, sorted by name        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecrets.html)                  |
| ListSecretVersionIds         | ✅     | All versions with staging labels   | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecretVersionIds.html)         |
| DeleteSecret                 | ✅     | Immediate (ForceDelete) only       | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteSecret.html)                 |
| TagResource                  | ✅     | Merge/overwrite tags               | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_TagResource.html)                  |
| RotateSecret                 | ✅     | Config only (no Lambda invocation) | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RotateSecret.html)                 |
| CancelRotateSecret           | ✅     | Clears rotation config             | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CancelRotateSecret.html)           |
| UntagResource                | ✅     | Removes specified tag keys         | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UntagResource.html)                |
| RestoreSecret                | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RestoreSecret.html)                |
| GetResourcePolicy            | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetResourcePolicy.html)            |
| PutResourcePolicy            | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutResourcePolicy.html)            |
| DeleteResourcePolicy         | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteResourcePolicy.html)         |
| ReplicateSecretToRegions     | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ReplicateSecretToRegions.html)     |
| RemoveRegionsFromReplication | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RemoveRegionsFromReplication.html) |
| ValidateResourcePolicy       | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ValidateResourcePolicy.html)       |
| GetRandomPassword            | ✅     | Configurable length + exclusions   | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetRandomPassword.html)            |
| BatchGetSecretValue          | ✅     | Partial results on missing secrets | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_BatchGetSecretValue.html)          |

## SDK compatibility

| SDK                       | Tested |
| ------------------------- | ------ |
| AWS SDK for Go v2         | ❌     |
| AWS SDK for JavaScript v3 | ✅     |
| boto3 (Python)            | ❌     |
| AWS SDK for Java          | ❌     |
| AWS SDK for .NET          | ❌     |

## Notes

- **Versioning**: Each `PutSecretValue` or `UpdateSecret` (with value) creates a new version. The previous AWSCURRENT version is demoted to AWSPREVIOUS.
- **Deletion**: `ForceDeleteWithoutRecovery` is always treated as immediate deletion. Recovery window scheduling is not implemented.
- **Rotation**: `RotateSecret` saves rotation configuration but does not invoke a Lambda function. No actual secret rotation occurs.
- **Lookup**: Secrets can be looked up by name or by ARN.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| Secret CRUD | 9            |                |
| Rotation    | 2            |                |
| Tags        | 2            |                |
| Password    | 1            |                |
| Policy/Misc |              | 7              |

---

## Endpoints

### Secret CRUD

| Operation              | Status       | Notes                              | AWS Docs                                                                                             |
| ---------------------- | ------------ | ---------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `CreateSecret`         | ✅ Supported | String + binary, tags, description | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CreateSecret.html)         |
| `GetSecretValue`       | ✅ Supported | By name, ARN, or version ID        | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetSecretValue.html)       |
| `DescribeSecret`       | ✅ Supported | Metadata, tags, version map        | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DescribeSecret.html)       |
| `PutSecretValue`       | ✅ Supported | Creates new AWSCURRENT version     | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutSecretValue.html)       |
| `UpdateSecret`         | ✅ Supported | Description + optional new value   | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UpdateSecret.html)         |
| `ListSecrets`          | ✅ Supported | All secrets, sorted by name        | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecrets.html)          |
| `ListSecretVersionIds` | ✅ Supported | All versions with staging labels   | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecretVersionIds.html) |
| `DeleteSecret`         | ✅ Supported | Immediate (ForceDelete) only       | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteSecret.html)         |
| `BatchGetSecretValue`  | ✅ Supported | Partial results on missing secrets | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_BatchGetSecretValue.html)  |

### Rotation

| Operation            | Status       | Notes                              | AWS Docs                                                                                           |
| -------------------- | ------------ | ---------------------------------- | -------------------------------------------------------------------------------------------------- |
| `RotateSecret`       | ✅ Supported | Config only (no Lambda invocation) | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RotateSecret.html)       |
| `CancelRotateSecret` | ✅ Supported | Clears rotation config             | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CancelRotateSecret.html) |

### Tags

| Operation       | Status       | Notes                      | AWS Docs                                                                                      |
| --------------- | ------------ | -------------------------- | --------------------------------------------------------------------------------------------- |
| `TagResource`   | ✅ Supported | Merge/overwrite tags       | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_TagResource.html)   |
| `UntagResource` | ✅ Supported | Removes specified tag keys | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UntagResource.html) |

### Password

| Operation           | Status       | Notes                            | AWS Docs                                                                                          |
| ------------------- | ------------ | -------------------------------- | ------------------------------------------------------------------------------------------------- |
| `GetRandomPassword` | ✅ Supported | Configurable length + exclusions | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetRandomPassword.html) |

### Policy/Misc

| Operation                      | Status         | Notes             | AWS Docs                                                                                                     |
| ------------------------------ | -------------- | ----------------- | ------------------------------------------------------------------------------------------------------------ |
| `RestoreSecret`                | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RestoreSecret.html)                |
| `GetResourcePolicy`            | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetResourcePolicy.html)            |
| `PutResourcePolicy`            | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutResourcePolicy.html)            |
| `DeleteResourcePolicy`         | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteResourcePolicy.html)         |
| `ReplicateSecretToRegions`     | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ReplicateSecretToRegions.html)     |
| `RemoveRegionsFromReplication` | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RemoveRegionsFromReplication.html) |
| `ValidateResourcePolicy`       | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ValidateResourcePolicy.html)       |

<!-- END overcast:capabilities -->
