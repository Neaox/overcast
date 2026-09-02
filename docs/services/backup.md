---
title: "Backup — AWS Backup"
description: "Quick start, the vault, plan and tag operations that work, and everything that never runs: no backup or restore jobs, no recovery points, no vault lock."
section: "Service Reference"
tags:
  - aws
  - backup
  - docs
  - services
---

# Backup — AWS Backup

Vaults and plans exist so IaC that declares them deploys; no backup job, restore
job or schedule ever runs.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws backup create-backup-vault --backup-vault-name nightly
aws backup create-backup-plan --backup-plan '{
  "BackupPlanName": "nightly",
  "Rules": [{
    "RuleName": "daily",
    "TargetBackupVaultName": "nightly",
    "ScheduleExpression": "cron(0 5 ? * * *)"
  }]
}'
aws backup list-backup-vaults
```

## What works

| Area | Behaviour |
| --- | --- |
| Vaults | Create, describe, list, delete, per region |
| Plans | Create, get, list, update, delete; an update mints a new `VersionId` |
| Rules | Stored and echoed back in full, including schedules and lifecycle blocks |
| Tags | `BackupVaultTags` and `BackupPlanTags` at creation, plus `TagResource`, `ListTags` and `UntagResource` |
| Bindings | AWS's own routes — vaults under `/backup-vaults`, plans under `/backup/plans` — so SDKs, CDK and `aws backup …` work unmodified |

## Differences from AWS

| Area                             | Overcast                                                                                                 |
| -------------------------------- | -------------------------------------------------------------------------------------------------------- |
| Nothing is backed up             | Rules are stored, never fired; there are no backup jobs, restore jobs or copy jobs                       |
| No recovery points               | A vault's `NumberOfRecoveryPoints` is always zero, and the recovery-point operations are not implemented |
| No vault lock                    | `Locked` is always false and the retention members are absent                                            |
| No sharing                       | Every vault is a standard unshared vault, so `ListBackupVaults --shared` lists none                      |
| One plan version                 | Only the current version is kept — `GetBackupPlan` with an older `VersionId` is a miss                   |
| Plans delete outright            | Nothing is tombstoned, so `ListBackupPlans --include-deleted` adds nothing                               |
| `AdvancedBackupSettings` dropped | Neither stored nor returned                                                                              |

## Gotchas

> [!NOTE]
> Tags are stored inline on the vault or plan record, so deleting the resource
> deletes its tags in the same write. Nothing outlives the record it describes.

<!-- BEGIN overcast:capabilities -->

## Operations

All 12 listed operations are implemented.
Per-operation status, notes and AWS API links: [Backup operations](backup/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_Reference.html)
