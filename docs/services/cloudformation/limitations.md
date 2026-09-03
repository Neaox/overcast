---
title: "CloudFormation limitations"
description: "Every CloudFormation divergence from AWS in one table, which state each operation may start from, and the resource types a stack waits on before completing."
section: "Service Reference"
tags:
  - cloudformation
  - docs
  - limitations
  - services
---

# CloudFormation limitations

Every divergence from AWS behind [CloudFormation](../cloudformation.md), which
state each operation may start from, and the resources a stack waits on.

## Divergences

| Area | Overcast | Detail |
| --- | --- | --- |
| `AWS::NoValue` | Substitutes the empty string rather than removing the property | — |
| `{{resolve:s3:…}}` | Not resolved; fails the resource | [Dynamic references](./dynamic-references.md) |
| SSM dynamic-reference versions | An explicit version resolves to the current value, with a warning | [Dynamic references](./dynamic-references.md) |
| `ssm-secure` | Accepted in any resource property except a custom resource's, where AWS restricts it to an enumerated list | [Dynamic references](./dynamic-references.md) |
| `DeleteStack`'s `RetainResources` | Not implemented — a resource that keeps refusing cannot be skipped past | [Teardown](./teardown.md) |
| `DeletionPolicy: Snapshot` | Treated as `Retain`; no snapshot is ever taken | [Teardown](./teardown.md) |
| Property-hash tracking | A stack created before it has no hashes, so its first update treats those resources as unchanged | [Stack updates](./updates.md) |
| Drift detection, StackSets, stack policies, resource imports | Not implemented | — |
| Unknown resource types | Accepted with a synthetic stub ID, a warning, and an `Overcast:`-prefixed `ResourceStatusReason`, rather than rejected | Below |
| Metric-math, extended-statistic and anomaly-detection `AWS::CloudWatch::Alarm` | Created, and says on its status reason that it will not be evaluated | Below |
| `ClientRequestToken` | Defaults to the request ID of the API call, which keys the debug trace | Below |
| A persistence flush that misses its deadline | Logged; the stack keeps the terminal status it reached | Below |

## Which state an operation may start from

The status is enforced, not merely reported.

| Operation | Allowed from | Refused with |
| --- | --- | --- |
| Create (`CreateStack`, or a `CREATE` change set at either step) | No stack of that name, or a `DELETE_COMPLETE` tombstone | `AlreadyExistsException` |
| Update (`UpdateStack`, or an `UPDATE` change set) | `CREATE_COMPLETE`, `UPDATE_COMPLETE`, `UPDATE_ROLLBACK_COMPLETE`, `IMPORT_COMPLETE`, `IMPORT_ROLLBACK_COMPLETE` | `ValidationError`: `Stack:<arn> is in <status> state and can not be updated.` |
| Update with `DisableRollback` | Also `CREATE_FAILED` and `UPDATE_FAILED` | as above, without the option |
| `ContinueUpdateRollback` | `UPDATE_ROLLBACK_FAILED` | as above |
| `RollbackStack` | `CREATE_FAILED`, `UPDATE_FAILED`, `UPDATE_ROLLBACK_FAILED` | as above |

A change set's own `REVIEW_IN_PROGRESS` placeholder is the one exception to the
create rule, and only for change sets: `CreateStack` against that placeholder still
fails, as on AWS. `ROLLBACK_COMPLETE` has no last known stable state, so it is
delete-only — which is why the CDK CLI deletes such a stack before deploying it
again. A change set may be created against `CREATE_FAILED`/`UPDATE_FAILED` without
`DisableRollback`, because that option belongs to the execution.

## Resources that wait

Some services answer a create long before the thing they created is usable, and
CloudFormation does not pass that on: the resource is not `CREATE_COMPLETE` until
it settles, and anything downstream waits behind it.

| Resource type | Complete when |
| --- | --- |
| `AWS::RDS::DBInstance` / `DBCluster` | it reports `available` |
| `AWS::ElastiCache::CacheCluster` / `ReplicationGroup` / `ServerlessCache` | it reports `available` |
| `AWS::EFS::FileSystem` / `MountTarget` / `AccessPoint` | it reports `available` |
| `AWS::MSK::Cluster` | the cluster reports `ACTIVE` |
| `AWS::EKS::Cluster` | the cluster reports `ACTIVE` |
| `AWS::Lambda::Function` | the function reports `Active` |
| `AWS::ECS::Service` | one deployment left, running its desired count, with `rolloutState: COMPLETED` where the controller reports one |

Status matching folds case, because AWS does not spell one service's vocabulary
consistently. An unrecognised status keeps the resource waiting rather than
completing it: AWS adds statuses, and completing on one nothing here understands is
the failure these waits exist to prevent. Which statuses end a wait comes from
AWS's own machine-readable answer wherever it has one — the waiters botocore ships.

Updates wait on the same condition, so an `UPDATE_COMPLETE` means the change was
applied *and* settled. A resource that cannot settle fails with the reason the
service itself gives — an RDS event, the newest actionable ECS service event, an
MSK `stateInfo`, an EKS health issue, a Lambda `StateReason` — and the stack rolls
back. A failed update is never answered by replacing the resource; the change has
already been applied to the one that exists.

A Lambda function is complete at `Active`, which is what real CloudFormation waits
for. `Active` means deployed, not working: a function with a broken handler is
`Active` on AWS too and fails at invoke, so the wait stops there.

Every wait runs whether or not the deployment has a container runtime behind the
service. A resource with no container coming reaches its ready status as soon as it
is recorded, and the wait gets its answer on the first poll.

## Stacks are addressable by name or ARN — a deleted stack only by ARN

Every stack-scoped operation's `StackName` accepts either the name or the unique
stack ID while the stack is live, exactly as AWS documents. A stack's name becomes
reusable the moment it reaches `DELETE_COMPLETE`, so from then on a name-based read
answers `ValidationError: Stack with id <name> does not exist` — the same answer a
name that was never created gets — while the ARN, which embeds the generation's
uuid, still resolves the deleted stack's final state and events. CDK's deploy
monitor relies on that, and the AWS SDK's `stack-delete-complete` waiter treats
that `ValidationError` as the terminal success case.

| Handle | On a `DELETE_COMPLETE` stack |
| --- | --- |
| Name, read operation | `ValidationError` — the name is reusable |
| ARN, read operation | Resolves the deleted generation's final state and events |
| Either handle, `UpdateStack` or `CreateChangeSet` | Reports the stack does not exist |
| Either handle, `DeleteStack` | A no-op success |
| A stale ARN from a deleted-and-recreated stack of the same name | Resolves to nothing; stacks are keyed by name |

A stack mid-`DELETE_IN_PROGRESS` still resolves by name; only the completed record
excludes it.

## A limitation a resource carries is its `ResourceStatusReason`

A resource Overcast creates but will not act on in full is provisioned, and the
shortfall rides its status reason on the `CREATE_COMPLETE`/`UPDATE_COMPLETE` event,
so a deploy shows it as the resource goes by rather than only on a later describe.
Refusing such a resource instead would fail the stack over something the template
is right to contain. `AWS::CloudWatch::Alarm` is the current case: a metric-math,
extended-statistic or anomaly-detection alarm is created and says it will not be
evaluated. The same channel carries the stub and inert-tier notice described on the
landing page.

Nothing here enters `StackStatusReason` or changes the stack's own status — the
signal is per-resource, and `DescribeStackEvents` is what a `cdk deploy` actually
polls.

## A persistence flush is not a stack failure

After a stack reaches a terminal status its state is flushed to the persistent
store, so a restart straight afterwards still finds it. A flush that does not
finish in time is logged and nothing more — the stack keeps the status it reached.
Every resource in it exists and answers requests, and the queued writes are neither
lost nor abandoned: they go back at the head of the pending queue, and the pending
log replays them after an unclean exit. Whether the store is keeping up is reported
by `/_overcast/health` and `/_overcast/debug/metrics`.

## Related

- [CloudFormation](../cloudformation.md) — quick start and what works
- [CloudFormation stack updates](./updates.md) — in-place, replace, and what a rollback puts back
- [CloudFormation teardown](./teardown.md) — failed deletes and `DeletionPolicy`
- [CloudFormation dynamic references](./dynamic-references.md) — `{{resolve:…}}` semantics
- [CloudFormation troubleshooting](./troubleshooting.md) — stuck stacks and failed deploys
- [CloudFormation operations](./operations.md) — per-operation status
- [Using AWS CDK](../../cdk.md) — the resource types a stack can provision
