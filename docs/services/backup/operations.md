---
title: "Backup operations"
description: "Every Backup operation Overcast declares — 12 of 12 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - backup
  - docs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# Backup operations

All 12 listed operations are implemented. Back to [Backup](../backup.md).

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

## Related

- [Backup](../backup.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
