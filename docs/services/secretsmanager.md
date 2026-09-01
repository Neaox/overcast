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

## Protocol

Secrets Manager accepts AWS JSON 1.1 requests via `X-Amz-Target:
secretsmanager.<Operation>` and Smithy RPC v2 CBOR requests via
`/service/secretsmanager/operation/<Operation>` with `Smithy-Protocol:
rpc-v2-cbor`.

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
| GetRandomPassword            | ✅     | Length, exclusions, RequireEachIncludedType | [link](https://docs.aws.amazon.com/secretsmanager/latest/apireference/API_GetRandomPassword.html)   |
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

- **Versioning**: `PutSecretValue` honours `ClientRequestToken` (which becomes the version ID) and `VersionStages` (default `AWSCURRENT`). Taking `AWSCURRENT` off a version moves `AWSPREVIOUS` onto it. Versions carrying a staging label are never pruned. A version that exists but holds no value — the state a rotation leaves between staging `AWSPENDING` and the function writing to it — is listed by `DescribeSecret` but reported as `ResourceNotFoundException` by `GetSecretValue`, and `PutSecretValue` under that token fills it rather than answering `ResourceExistsException`.
- **Deletion**: `ForceDeleteWithoutRecovery` is always treated as immediate deletion. Recovery window scheduling is not implemented, which is why `RestoreSecret` is still a 501.
- **Lookup**: Secrets resolve by name, by full ARN, or by the partial ARN without the six-character random suffix — all three, as on AWS. With no version selector, every `GetSecretValue` and internal service read resolves the version currently labelled `AWSCURRENT`; Overcast adds no server-side value cache.
- **Password generation**: `GetRandomPassword` honours `PasswordLength` (default 32, modeled range 1–4096), the `Exclude*` settings, `IncludeSpace`, and `RequireEachIncludedType` — which, as on AWS, defaults to true, so a generated password holds at least one character of every type the exclusions left available. CloudFormation's `AWS::SecretsManager::Secret` `GenerateSecretString` generates through this same operation rather than carrying its own generator; see [CloudFormation § Notes](./cloudformation.md).
- **KMS metadata**: `CreateSecret` and `UpdateSecret` persist `KmsKeyId`; `DescribeSecret` and `ListSecrets` return it. Overcast records the selected key as AWS-visible metadata but does not perform KMS encryption.

### Rotation

`RotateSecret` runs AWS's four-step protocol against the local Lambda emulator,
invoking the configured rotation function once per step —
`createSecret`, `setSecret`, `testSecret`, `finishSecret` — with the payload
AWS sends (`{"Step", "SecretId", "ClientRequestToken"}`) and the same token
throughout. Each step must return before the next starts, so the synchronous
invoke path is used. Automatic rotation on a `RotationRules` schedule is driven
by one background loop over the injected clock; `NextRotationDate` and
`LastRotatedDate` reflect what actually happened.

Before the first invocation, the `ClientRequestToken` is made a version of the
secret staged `AWSPENDING`, with no value in it — as AWS does. Every rotation
blueprint AWS publishes opens by asserting exactly that, for every step
including `createSecret`, so a rotation function copied from one of them works
against Overcast unmodified.

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

## Operations

19 of 22 listed operations are implemented.
Per-operation status, notes and AWS API links: [Secrets Manager operations](secretsmanager/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
