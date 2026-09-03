---
title: "CloudFormation teardown"
description: "What a delete does when a resource refuses to go: which teardown stops the stack, which refusals need clearing by hand, and how DeletionPolicy and RetainExceptOnCreate behave."
section: "Service Reference"
tags:
  - cloudformation
  - docs
  - services
  - teardown
---

# CloudFormation teardown

What [CloudFormation](../cloudformation.md) does when a resource will not go, and
which resources it leaves standing.

A delete reports success only when the resource is gone: either the delete
removed it, or it was not there to begin with. An absent resource is always a
successful teardown; every other outcome is reported.

## What the stack does with a failed delete

| Teardown | On `DELETE_FAILED` | Why |
| --- | --- | --- |
| A rollback | Emits the service's own error, keeps the resource listed, reaches `ROLLBACK_FAILED` or `UPDATE_ROLLBACK_FAILED` | Keeping it listed stops the next `UpdateStack` creating a second copy alongside it, and is what lets `ContinueUpdateRollback` retry exactly those deletes |
| `DeleteStack` | Stops at the resource, leaves the stack `DELETE_FAILED` with the resource still listed, makes no further progress | The stack record is the only thing still naming what is out there |
| The cleanup phase after a successful update | Emits `DELETE_FAILED` and still completes the stack | AWS runs that phase after the update has already succeeded and does not roll it back |

Some failures are *refusals* — a lasting condition to clear before a retry gets
past it:

| Refusal | Raised by |
| --- | --- |
| `DeleteConflict` (HTTP 409) | IAM's `DeleteRole`, `DeleteUser`, `DeletePolicy` while a dependency remains |
| `DependencyViolation` | Every EC2 teardown — internet gateway, main route table, VPN gateway, security group, subnet, VPC |
| Non-empty bucket | `AWS::S3::Bucket` |
| `DeletionProtection` enabled | `AWS::RDS::DBCluster` |
| Child teardown failed | A nested `AWS::CloudFormation::Stack` |

The rest are failures a retry may clear on its own. Either way the resource is
still standing, so both stop the teardown. `DeleteStack`'s
`RetainResources` option is not implemented, so a resource that keeps refusing
cannot yet be skipped past — clear the cause and call `DeleteStack` again.
Resources deleted before the failure stay deleted, so a retry resumes from what is
left.

## `DeletionPolicy`

Honoured. `Retain` (and `Snapshot`) skips deletion and emits a `DELETE_SKIPPED`
event in four cases: a create that rolls back, an update rollback over a resource
that update created, a stack delete, and a resource removed from the template on
update.

An update rollback drops the retained resource from the stack's resource list
along with the rest of what the failed update created, so the stack that reaches
`UPDATE_ROLLBACK_COMPLETE` is the pre-update one. The *replacement* a failed
update created is deleted whatever the policy says: the original is still standing
and is what the stack rolls back to.

| Setting | Effect |
| --- | --- |
| `DeletionPolicy: RetainExceptOnCreate` | Deletes the resource during its initial create rollback; retains it during ordinary deletion |
| The `RetainExceptOnCreate` **operation** option | The same, for a resource whose template policy is `Retain`. Set it on `CreateStack`, `UpdateStack`, `ExecuteChangeSet` or `RollbackStack` |
| That option's default | `false`, as on AWS — so a first deploy that fails partway leaves a `Retain` resource standing, and the next deploy of the same template collides with it |
| `CreateChangeSet` | Does not take the option, as on AWS: the choice belongs to `ExecuteChangeSet` |
| `DeletionPolicy: Snapshot` | Ignores the option, where `Retain` honours it. No snapshot is ever taken |

## Related

- [CloudFormation](../cloudformation.md) — quick start and what works
- [CloudFormation limitations](./limitations.md) — the divergence table and the status machine
- [CloudFormation stack updates](./updates.md) — what a rollback puts back on the stack record
- [CloudFormation troubleshooting](./troubleshooting.md) — stuck stacks and failed deploys
- [S3](../s3.md) — emptying a bucket before it will delete
