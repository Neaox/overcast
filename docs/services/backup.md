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

Metadata-only AWS Backup implementation.

## What works
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

## Operations

All 12 listed operations are implemented.
Per-operation status, notes and AWS API links: [Backup operations](backup/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/aws-backup/latest/devguide/API_Reference.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
