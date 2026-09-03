---
title: "CloudFormation stack updates"
description: "How an update decides what changed, when it updates in place and when it replaces, and what an update rollback puts back on the stack record."
section: "Service Reference"
tags:
  - cloudformation
  - docs
  - services
  - updates
---

# CloudFormation stack updates

What [CloudFormation](../cloudformation.md) does to each resource on an update,
and what a failed update leaves behind.

## Which strategy a changed resource gets

`UpdateStack` and `ExecuteChangeSet` detect drift per resource by a sha256 hash of
the resolved property map stored alongside each `StackResource`. Resources whose
hash is unchanged are skipped. Where a resource changed, the provisioner picks one
of three strategies in order:

| Strategy | Chosen when | Effect |
| --- | --- | --- |
| In-place update | The handler implements an `Update` method | The change goes through the service's own mutation API. Most provisioned types qualify; the per-resource detail is on each service's page |
| Replacement | An immutable property changed, or no `Update` is registered | Delete, then create |
| Retain on replace | `UpdateReplacePolicy: Retain` or `Snapshot` | The new resource is created and the old one is orphaned and no longer tracked by the stack, as on AWS |

The identity properties that force a replacement are the obvious ones:
`BucketName`, `TableName`/`KeySchema`, `QueueName`/`FifoQueue`, `TopicName`,
`Name`, `LogGroupName`, `RoleName`, `FunctionName`, `Name`/`GroupName`.

Stacks created before property-hash tracking have an empty hash. On the first
update, missing-hash resources are treated as unchanged, so re-running an
unchanged template (`cdk bootstrap`, say) is a no-op rather than a destructive
replace.

## What an update rollback puts back

An update writes the template, parameters and tags it was given onto the stack
record before it touches a resource, so from that moment `DescribeStacks` and
`GetTemplate` describe the attempt rather than what is deployed.

| Outcome | Stack record |
| --- | --- |
| `UPDATE_ROLLBACK_COMPLETE` | Template, parameters, tags and resource list all restored, so the stack describes the generation it returned to |
| A nested stack that rolled back | Restores its own three; a child whose update failed is never recorded by the parent as an in-place success |
| Metadata the rollback could not persist | `UPDATE_ROLLBACK_FAILED` rather than a completion it did not achieve |
| An update that failed with `DisableRollback` | Keeps the attempted template and parameters — nothing was unwound |

A rollback that fails leaves the stack in `UPDATE_ROLLBACK_FAILED`, which is
delete-or-continue only. See
[Getting out of `UPDATE_ROLLBACK_FAILED`](./troubleshooting.md#getting-out-of-update_rollback_failed).

## Related

- [CloudFormation](../cloudformation.md) — quick start and what works
- [CloudFormation limitations](./limitations.md) — the divergence table and the status machine
- [CloudFormation teardown](./teardown.md) — what a rollback deletes, and what it keeps
- [CloudFormation troubleshooting](./troubleshooting.md) — stuck stacks and failed deploys
- [CDK resource type coverage](../../cdk/resource-types.md) — which types a stack can provision
