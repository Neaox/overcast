---
title: "Backup — AWS Backup"
description: "Metadata-only AWS Backup implementation."
section: "Service Reference"
tags:
  - aws
  - backup
  - docs
  - services
---

# Backup — AWS Backup

> AWS docs: https://docs.aws.amazon.com/aws-backup/latest/devguide/API_Reference.html

Metadata-only AWS Backup implementation.

## Summary

Supports backup-vault and backup-plan control-plane CRUD for local stack
compatibility. AWS Backup uses the REST JSON protocol, and Overcast serves it at
AWS's own bindings, so SDK clients, CDK constructs and `aws backup …` work
unmodified.

## Behavior Notes

- Routes are AWS's own bindings, in two subtrees rather than one prefix: vaults
  under `/backup-vaults` (e.g. `PUT /backup-vaults/{BackupVaultName}`) and plans
  under `/backup/plans`.
- Vaults and plans are per-region, as on AWS: the same vault name in two regions
  is two vaults.
- No recovery points are created or stored, so a vault's
  `NumberOfRecoveryPoints` is always zero.
- No backup jobs, restore jobs, or scheduling workers are executed — a plan's
  rules are stored and echoed back, never fired.
- Tagging is not implemented: `BackupVaultTags` and `BackupPlanTags` are
  accepted at creation and dropped, and there are no `TagResource`,
  `UntagResource` or `ListTags` operations
  ([#815](https://github.com/overcast-sh/overcast/issues/815)).
- Designed to unblock IaC/API flows expecting vault and plan resources.
- Operations that are not emulated return a JSON `501 Not Implemented` error
  response.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | ✅ Supported | ⚠️ Partial |
| ---------- | ------------ | ---------- |
| Operations | 6            | 6          |

---

## Endpoints

### Operations

| Operation             | Status       | Notes                                                                                                                                                                                  | AWS Docs                                                                                    |
| --------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `CreateBackupVault`   | ✅ Supported | PUT /backup-vaults/{BackupVaultName}; `BackupVaultTags` is applied at create time, in the same store write as the vault (#1195)                                                        | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_CreateBackupVault.html)   |
| `DeleteBackupVault`   | ✅ Supported | DELETE /backup-vaults/{BackupVaultName}; empty 200, the modeled Unit output                                                                                                            | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DeleteBackupVault.html)   |
| `DescribeBackupVault` | ⚠️ Partial   | GET /backup-vaults/{BackupVaultName}; honours `backupVaultAccountId`. Vault lock and MPA approval are not emulated, so `Locked` is always false and the retention members are absent   | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DescribeBackupVault.html) |
| `ListBackupVaults`    | ⚠️ Partial   | GET /backup-vaults; `vaultType`, `shared`, `maxResults` and `nextToken` query params. Every vault is a standard unshared vault, so `shared=true` lists none                            | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_ListBackupVaults.html)    |
| `CreateBackupPlan`    | ⚠️ Partial   | PUT /backup/plans; `BackupPlanTags` is applied at create time (#1195); `AdvancedBackupSettings` is not stored or returned                                                              | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_CreateBackupPlan.html)    |
| `DeleteBackupPlan`    | ✅ Supported | DELETE /backup/plans/{BackupPlanId}; returns the modeled id, ARN, VersionId and DeletionDate                                                                                           | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DeleteBackupPlan.html)    |
| `GetBackupPlan`       | ⚠️ Partial   | GET /backup/plans/{BackupPlanId}; only the current version is kept, so any other `versionId` is not found, and `MaxScheduledRunsPreview` yields no `ScheduledRunsPreview`              | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_GetBackupPlan.html)       |
| `ListBackupPlans`     | ⚠️ Partial   | GET /backup/plans; `maxResults` and `nextToken` query params. Plans are deleted outright rather than tombstoned, so `includeDeleted` adds nothing                                      | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_ListBackupPlans.html)     |
| `UpdateBackupPlan`    | ⚠️ Partial   | POST /backup/plans/{BackupPlanId}; mints a new VersionId per update. Prior versions are not retained and `AdvancedBackupSettings` is not stored                                        | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_UpdateBackupPlan.html)    |
| `TagResource`         | ✅ Supported | POST /tags/{ResourceArn}, shared with Pipes/EKS/Scheduler/AppConfig/API Gateway's ARN-dispatched path (#1195). Tags are stored inline on the vault or plan record, so they die with it | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_TagResource.html)         |
| `ListTags`            | ✅ Supported | GET /tags/{ResourceArn} (#1195)                                                                                                                                                        | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_ListTags.html)            |
| `UntagResource`       | ✅ Supported | POST /untag/{ResourceArn} — Backup's own path, not another member of the shared /tags dispatcher (#1195)                                                                               | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_UntagResource.html)       |

<!-- END overcast:capabilities -->
