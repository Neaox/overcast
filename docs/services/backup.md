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

| Category   | 🧊 Inert |
| ---------- | -------- |
| Operations | 9        |

---

## Endpoints

### Operations

| Operation             | Status   | Notes | AWS Docs                                                                                    |
| --------------------- | -------- | ----- | ------------------------------------------------------------------------------------------- |
| `CreateBackupVault`   | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_CreateBackupVault.html)   |
| `DeleteBackupVault`   | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DeleteBackupVault.html)   |
| `DescribeBackupVault` | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DescribeBackupVault.html) |
| `ListBackupVaults`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_ListBackupVaults.html)    |
| `CreateBackupPlan`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_CreateBackupPlan.html)    |
| `DeleteBackupPlan`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_DeleteBackupPlan.html)    |
| `GetBackupPlan`       | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_GetBackupPlan.html)       |
| `ListBackupPlans`     | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_ListBackupPlans.html)     |
| `UpdateBackupPlan`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_UpdateBackupPlan.html)    |

<!-- END overcast:capabilities -->
