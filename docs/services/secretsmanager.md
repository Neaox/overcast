---
title: "Secrets Manager — endpoint support"
description: "Generated for Overcast. See also: AWS Secrets Manager API Reference"
section: "Service Reference"
tags:
  - docs
  - endpoint
  - manager
  - secrets
  - secretsmanager
  - services
  - support
---

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
| Rotation    | 3            | 0              |
| Tags        | 2            | 0              |
| Password    | 1            | 0              |
| Policy/Misc | 4            | 3              |
| **Total**   | **19**       | **3**          |

## Endpoint details

| Operation                    | Status | Notes                              | AWS docs                                                                                                     |
| ---------------------------- | ------ | ---------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| CreateSecret                 | ✅     | String + binary, tags, description | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CreateSecret.html)                 |
| GetSecretValue               | ✅     | By name, ARN, version ID, or stage | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetSecretValue.html)               |
| DescribeSecret               | ✅     | Metadata, tags, versions, rotation | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DescribeSecret.html)               |
| PutSecretValue               | ✅     | Staging labels + ClientRequestToken | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutSecretValue.html)              |
| UpdateSecret                 | ✅     | Description + optional new value   | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UpdateSecret.html)                 |
| ListSecrets                  | ✅     | Sorted by name, optional filters   | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecrets.html)                  |
| ListSecretVersionIds         | ✅     | All versions with staging labels   | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecretVersionIds.html)         |
| DeleteSecret                 | ✅     | Immediate (ForceDelete) only       | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteSecret.html)                 |
| TagResource                  | ✅     | Merge/overwrite tags               | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_TagResource.html)                  |
| RotateSecret                 | ✅     | Invokes the rotation Lambda, 4 steps | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RotateSecret.html)               |
| CancelRotateSecret           | ✅     | Turns rotation off, keeps config   | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CancelRotateSecret.html)           |
| UpdateSecretVersionStage     | ✅     | Moves staging labels between versions | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UpdateSecretVersionStage.html) |
| UntagResource                | ✅     | Removes specified tag keys         | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UntagResource.html)                |
| RestoreSecret                | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RestoreSecret.html)                |
| GetResourcePolicy            | ✅     | Stored policy; not evaluated       | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetResourcePolicy.html)            |
| PutResourcePolicy            | ✅     | Validated + stored; not evaluated  | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutResourcePolicy.html)            |
| DeleteResourcePolicy         | ✅     | Removes the stored policy          | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteResourcePolicy.html)         |
| ReplicateSecretToRegions     | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ReplicateSecretToRegions.html)     |
| RemoveRegionsFromReplication | ❌     | Returns 501                        | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RemoveRegionsFromReplication.html) |
| ValidateResourcePolicy       | ✅     | Syntax + schema checks, no evaluation | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ValidateResourcePolicy.html)    |
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

- **Versioning**: `PutSecretValue` honours `ClientRequestToken` (which becomes the version ID) and `VersionStages` (default `AWSCURRENT`). Taking `AWSCURRENT` off a version moves `AWSPREVIOUS` onto it. Versions carrying a staging label are never pruned.
- **Deletion**: `ForceDeleteWithoutRecovery` is always treated as immediate deletion. Recovery window scheduling is not implemented, which is why `RestoreSecret` is still a 501.
- **Lookup**: Secrets resolve by name, by full ARN, or by the partial ARN without the six-character random suffix — all three, as on AWS.

### Rotation

`RotateSecret` runs AWS's four-step protocol against the local Lambda emulator,
invoking the configured rotation function once per step —
`createSecret`, `setSecret`, `testSecret`, `finishSecret` — with the payload
AWS sends (`{"Step", "SecretId", "ClientRequestToken"}`) and the same token
throughout. Each step must return before the next starts, so the synchronous
invoke path is used. Automatic rotation on a `RotationRules` schedule is driven
by one background loop over the injected clock; `NextRotationDate` and
`LastRotatedDate` reflect what actually happened.

Two deliberate divergences from AWS, both chosen so a local failure is visible
rather than silent:

- **Rotation is synchronous.** AWS's `RotateSecret` returns immediately and
  rotates in the background. Overcast returns once the sequence has finished,
  so the secret has rotated by the time the call comes back.
- **A failed step fails the call.** AWS answers `200` and reports the failure
  through CloudTrail and the console, which a local emulator has no equivalent
  of. Overcast returns `InvalidRequestException` naming the step that failed,
  leaves the staging labels exactly as the function left them, and records the
  attempt so the web console can show it. Reporting success for a rotation that
  did not happen would be worse than the old config-only stub.

`RotateSecret` with no rotation function configured — neither on the call nor
already on the secret — is `InvalidRequestException`, as on AWS. It used to be
accepted and silently do nothing.

### Resource policies

`Put`/`Get`/`Delete`/`ValidateResourcePolicy` store, return, remove and
syntactically validate a secret's resource policy. **Nothing evaluates it.** A
stored policy grants and denies nothing: request-time IAM enforcement
(`OVERCAST_ENFORCE_IAM`, off by default) consults identity policies only.
Handing each service's stored resource policy to that evaluator is tracked in
issue #496. `BlockPublicPolicy` is honoured — a policy allowing every principal
with no `Condition` is refused with `MalformedPolicyDocumentException`.

Validation is syntax and schema only: valid JSON, a `Version`, at least one
`Statement`, and an `Effect`/`Action`/`Principal` on each. AWS does not publish
the full set of `CheckName` values its own validator reports, so Overcast's are
in AWS's shape but are not a stable identifier to branch on — read the
`ErrorMessage`.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| Secret CRUD | 9            |                |
| Rotation    | 3            |                |
| Tags        | 2            |                |
| Password    | 1            |                |
| Policy/Misc | 4            | 3              |

---

## Endpoints

### Secret CRUD

| Operation              | Status       | Notes                                    | AWS Docs                                                                                             |
| ---------------------- | ------------ | ---------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `CreateSecret`         | ✅ Supported | String + binary, tags, description       | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CreateSecret.html)         |
| `GetSecretValue`       | ✅ Supported | By name, ARN, version ID, or stage       | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetSecretValue.html)       |
| `DescribeSecret`       | ✅ Supported | Metadata, tags, versions, rotation dates | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DescribeSecret.html)       |
| `PutSecretValue`       | ✅ Supported | Staging labels + ClientRequestToken      | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutSecretValue.html)       |
| `UpdateSecret`         | ✅ Supported | Description + optional new value         | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UpdateSecret.html)         |
| `ListSecrets`          | ✅ Supported | Sorted by name, optional filters         | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecrets.html)          |
| `ListSecretVersionIds` | ✅ Supported | All versions with staging labels         | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ListSecretVersionIds.html) |
| `DeleteSecret`         | ✅ Supported | Immediate (ForceDelete) only             | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteSecret.html)         |
| `BatchGetSecretValue`  | ✅ Supported | Partial results on missing secrets       | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_BatchGetSecretValue.html)  |

### Rotation

| Operation                  | Status       | Notes                                       | AWS Docs                                                                                                 |
| -------------------------- | ------------ | ------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `RotateSecret`             | ✅ Supported | Invokes the rotation Lambda, all four steps | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RotateSecret.html)             |
| `CancelRotateSecret`       | ✅ Supported | Turns rotation off, keeps the config        | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_CancelRotateSecret.html)       |
| `UpdateSecretVersionStage` | ✅ Supported | Moves staging labels between versions       | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_UpdateSecretVersionStage.html) |

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

| Operation                      | Status         | Notes                                    | AWS Docs                                                                                                     |
| ------------------------------ | -------------- | ---------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `GetResourcePolicy`            | ✅ Supported   | Stored policy; not evaluated (#496)      | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetResourcePolicy.html)            |
| `PutResourcePolicy`            | ✅ Supported   | Validated + stored; not evaluated (#496) | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_PutResourcePolicy.html)            |
| `DeleteResourcePolicy`         | ✅ Supported   | Removes the stored policy                | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_DeleteResourcePolicy.html)         |
| `ValidateResourcePolicy`       | ✅ Supported   | Syntax + schema checks, no evaluation    | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ValidateResourcePolicy.html)       |
| `RestoreSecret`                | ❌ Unsupported | stub; returns 501                        | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RestoreSecret.html)                |
| `ReplicateSecretToRegions`     | ❌ Unsupported | stub; returns 501                        | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_ReplicateSecretToRegions.html)     |
| `RemoveRegionsFromReplication` | ❌ Unsupported | stub; returns 501                        | [docs](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_RemoveRegionsFromReplication.html) |

<!-- END overcast:capabilities -->
