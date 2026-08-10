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

Supports backup-vault and backup-plan control-plane CRUD for local stack compatibility.

## Behavior Notes

- No recovery points are created or stored.
- No backup jobs, restore jobs, or scheduling workers are executed.
- Designed to unblock IaC/API flows expecting vault and plan resources.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | 🚧 WIP |
| ---------- | ------ |
| Operations | 9      |

---

## Endpoints

### Operations

| Operation             | Status | Notes                                                                       | AWS Docs                                                                                    |
| --------------------- | ------ | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `CreateBackupVault`   | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_CreateBackupVault.html)   |
| `DeleteBackupVault`   | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DeleteBackupVault.html)   |
| `DescribeBackupVault` | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DescribeBackupVault.html) |
| `ListBackupVaults`    | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_ListBackupVaults.html)    |
| `CreateBackupPlan`    | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_CreateBackupPlan.html)    |
| `DeleteBackupPlan`    | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DeleteBackupPlan.html)    |
| `GetBackupPlan`       | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_GetBackupPlan.html)       |
| `ListBackupPlans`     | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_ListBackupPlans.html)     |
| `UpdateBackupPlan`    | 🚧 WIP | Overcast does not serve the binding AWS models, so no SDK reaches it (#815) | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_UpdateBackupPlan.html)    |

<!-- END overcast:capabilities -->
