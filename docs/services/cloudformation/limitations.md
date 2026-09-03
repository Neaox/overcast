---
title: "CloudFormation limitations"
description: "The stack status machine and which operations each state allows, how rollbacks and teardown failures are reported, dynamic reference semantics, and update strategies."
section: "Service Reference"
tags:
  - cloudformation
  - docs
  - limitations
  - services
---

# CloudFormation limitations

The full divergence list behind [CloudFormation](../cloudformation.md): the stack
status machine, what a rollback puts back, and how dynamic references resolve.

## Stacks are addressable by name or ARN — a deleted stack only by ARN

Every stack-scoped operation's `StackName` accepts either the name or the unique
stack ID while the stack is live, exactly as AWS documents. A stack's name becomes
reusable the moment it reaches `DELETE_COMPLETE`, so from then on a name-based read
answers `ValidationError: Stack with id <name> does not exist` — the same answer a
name that was never created gets — while the ARN, which embeds the generation's
uuid, still resolves the deleted stack's final state and events.

CDK's deploy monitor relies on that: it polls by the ARN it holds from the
deploy, and the AWS SDK's `stack-delete-complete` waiter treats that
`ValidationError` as the terminal success case. A stack mid-`DELETE_IN_PROGRESS`
still resolves by name; only the completed record excludes it.

Mutating operations go further and treat a `DELETE_COMPLETE` stack as nonexistent
by either handle: `UpdateStack` and `CreateChangeSet` report it does not exist, and
a repeat `DeleteStack` is a no-op success. A stale ARN from a deleted-and-recreated
stack of the same name resolves to nothing, since stacks are keyed by name.

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

## Where the reason lives

In `StackStatusReason` and in each event's `ResourceStatusReason` — never in the
status itself. Overcast sets it on every rollback path (`resource X failed: …` for
an automatic rollback, `User Initiated` for `RollbackStack`) and clears it once the
rollback reaches its terminal `*_ROLLBACK_COMPLETE` state, so on a
`ROLLBACK_COMPLETE` stack the surviving explanation is the `ROLLBACK_IN_PROGRESS`
event and the `CREATE_FAILED` resource that caused it. `ListStacks` summaries carry
`StackStatusReason` alongside the status, as AWS's `StackSummary` does.

A create that fails with `DisableRollback` has no rollback to explain, so the stack
stops at `CREATE_FAILED` carrying AWS's summary of *which* resources failed —
`The following resource(s) failed to create: [MyBucket]` — rather than the
underlying service error. That error is not lost: it stays on the resource's
`ResourceStatusReason` and its `CREATE_FAILED` event, which is where AWS keeps it.

Every stack event also carries a `ClientRequestToken`: the caller's when one was
supplied, and otherwise the request ID of the API call that started the operation.
The request ID keys
`/_overcast/debug/trace/{requestId}`, so pasting an event's token into the trace
viewer opens the request behind it, with every internal service call the operation
made. The token belongs to the operation rather than the stack, so a create and a
later update are distinguishable, and a nested stack's events carry the parent
operation's token. `OVERCAST_DEBUG` must be on for the trace to be retained.

## What an update rollback puts back

An update writes the template, parameters and tags it was given onto the stack
record before it touches a resource, so from that moment `DescribeStacks` and
`GetTemplate` describe the attempt rather than what is deployed. A rollback
restores all three alongside the resource list, so a stack that reaches
`UPDATE_ROLLBACK_COMPLETE` describes the generation it actually returned to. A
nested stack restores its own, because a child whose update failed is never
recorded by the parent as an in-place success. Metadata the rollback could not
persist fails it: the stack reports `UPDATE_ROLLBACK_FAILED` rather than a
completion it did not achieve. An update that fails with `DisableRollback` keeps
the attempted template and parameters, because nothing was unwound.

## Teardown failure

A delete reports success only when the resource is gone, and there are two ways
for it to be gone: the delete removed it, or it was not there to begin with. An
absent resource is always a successful teardown — nothing may wedge a stack over a
resource that no longer exists — and every other outcome is reported. What the
stack does with it depends on which teardown is running:

- A **rollback** emits `DELETE_FAILED` carrying the service's own error, keeps the
  resource in the stack's resource list, and reaches `ROLLBACK_FAILED` or
  `UPDATE_ROLLBACK_FAILED`. Keeping the resource listed stops the next
  `UpdateStack` from treating it as new and creating a second copy alongside it,
  and is what lets `ContinueUpdateRollback` retry exactly those deletes.
- **`DeleteStack`** stops at the resource: `DELETE_FAILED`, the stack left
  `DELETE_FAILED` with the resource still listed so a retry knows what is standing,
  and no further progress. Reporting `DELETE_COMPLETE` over it would drop the
  stack's record of the resource, which is the only thing still naming what is out
  there.
- The **cleanup phase after a successful update**, which removes resources the new
  template dropped, emits `DELETE_FAILED` and still completes the stack. AWS runs
  that phase after the update has already succeeded and does not roll it back.

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
still standing, which is why both stop the teardown. `DeleteStack`'s
`RetainResources` option is not implemented, so a resource that keeps refusing
cannot yet be skipped past — clear the cause and call `DeleteStack` again.
Resources deleted before the failure stay deleted, so a retry resumes from what is
left.

## DeletionPolicy

Honoured. `Retain` (and `Snapshot`) skips deletion when a create rolls back, when
an update rolls back over a resource that update created, when the stack is
deleted, or when a resource is removed from the template on update — the resource
is orphaned and a `DELETE_SKIPPED` event is emitted. An update rollback drops the
retained resource from the stack's resource list along with the rest of what the
failed update created, so the stack that reaches `UPDATE_ROLLBACK_COMPLETE` is the
pre-update one. The *replacement* a failed update created is deleted regardless of
policy: the original is still standing and is what the stack rolls back to.

`RetainExceptOnCreate` deletes the resource during its initial create rollback and
retains it during ordinary deletion. The `RetainExceptOnCreate` **operation**
option does the same for a resource whose template policy is `Retain`: set it on
`CreateStack`, `UpdateStack`, `ExecuteChangeSet` or `RollbackStack` and the
rollback deletes what that operation created instead of orphaning it. It defaults
to `false`, as on AWS — so a first deploy that fails partway leaves a `Retain`
resource standing, and the next deploy of the same template collides with it.
`CreateChangeSet` does not take the option, as on AWS: the choice belongs to
`ExecuteChangeSet`. A `Snapshot` policy ignores the option, where `Retain` honours
it, and no snapshot is ever taken.

## Dynamic references

A dynamic reference is plain text inside a property value — not an intrinsic — that
CloudFormation substitutes at deploy time. `secretsmanager`, `ssm` and `ssm-secure`
resolve against the emulated services, so a reference reads exactly what the
equivalent `GetSecretValue` or `GetParameter` call would return. `{{resolve:s3:…}}`
is not supported and fails the resource rather than resolving to something wrong.

Resolution happens after the intrinsic functions, so a reference built by `Fn::Sub`
or `Fn::Join` resolves once the surrounding value is complete. A resolved value is
never rescanned — secret content containing `{{resolve:` is data, not a reference.

**A reference is compared as written, never as resolved.** Change detection and the
stored resource properties both keep the literal text, so:

- Rotating a secret behind an unchanged template does not make the resource look
  changed, matching AWS — to push a new value you must change the resource in the
  template. No `GetSecretValue` call is made for an unchanged containing resource,
  so a no-op stack update also succeeds if the secret is no longer available.
- A resolved secret is never written to Overcast's state. Only the service the
  property belongs to ever sees it, which is the one exposure AWS also allows.
- **Outputs leave references literal.** A `{{resolve:…}}` in an `Outputs` value
  comes back as the reference text, matching CloudFormation and avoiding publishing
  a secret through `DescribeStacks`.
- **A reference creates no dependency.** Only `Ref`, `Fn::GetAtt` and `Fn::Sub`
  order resources, so a resource reading a secret created by the same template
  needs an explicit `DependsOn`, as on AWS.

A reference that cannot be resolved **fails the resource** and the stack rolls
back, rather than creating it with the literal `{{resolve:…}}` text in place of a
value.

Divergences: an explicit SSM parameter version is accepted but resolves to the
current value, with a warning; `ssm-secure` is accepted in any resource property
except a custom resource's, where secure references are refused outright, whereas
AWS restricts it to an enumerated list of properties.

## Update strategies

`UpdateStack` and `ExecuteChangeSet` detect drift per resource by a sha256 hash of
the resolved property map stored alongside each `StackResource`. Resources whose
hash is unchanged are skipped. Where a resource changed, the provisioner picks one
of three strategies in order:

1. **In-place update** — the handler implements an `Update` method and applies the
   change through the service's own mutation API. Most provisioned types do; the
   per-resource detail is in each service's page.
2. **Replacement (delete + create)** — an immutable property changed, or no
   `Update` is registered. The identity properties that force it are the obvious
   ones: `BucketName`, `TableName`/`KeySchema`, `QueueName`/`FifoQueue`,
   `TopicName`, `Name`, `LogGroupName`, `RoleName`, `FunctionName`,
   `Name`/`GroupName`.
3. **Retain on replace** — `UpdateReplacePolicy: Retain` (or `Snapshot`) skips
   deleting the old resource, so the new one is created and the old one is orphaned
   and no longer tracked by the stack, as on AWS.

Stacks created before property-hash tracking have an empty hash; on the first
update, missing-hash resources are treated as unchanged, so re-running an unchanged
template (`cdk bootstrap`, say) is a no-op rather than a destructive replace.

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

## A limitation a resource carries is its `ResourceStatusReason`

A resource Overcast creates but will not act on in full is provisioned, and the
shortfall rides its status reason on the `CREATE_COMPLETE`/`UPDATE_COMPLETE` event,
so a deploy shows it as the resource goes by rather than only on a later describe.
Refusing such a resource instead would fail the stack over something the template
is right to contain. `AWS::CloudWatch::Alarm` is the current case: a metric-math,
extended-statistic or anomaly-detection alarm is created and says it will not be
evaluated.

The same channel carries the stub and inert-tier notice described on the landing
page. Nothing here enters `StackStatusReason` or changes the stack's own status —
the signal is per-resource, and `DescribeStackEvents` is what a `cdk deploy`
actually polls.

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
- [CloudFormation troubleshooting](./troubleshooting.md) — stuck stacks and failed deploys
- [CloudFormation operations](./operations.md) — per-operation status
- [Using AWS CDK](../../cdk.md) — the resource types a stack can provision
