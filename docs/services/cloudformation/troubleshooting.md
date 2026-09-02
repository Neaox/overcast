---
title: "CloudFormation troubleshooting"
description: "Stacks stuck in a failed rollback, deploys whose evidence the rollback destroyed, deletes that will not finish, and resources that deploy green but do nothing."
section: "Service Reference"
tags:
  - cloudformation
  - docs
  - services
  - troubleshooting
---

# CloudFormation troubleshooting

Back to [CloudFormation](../cloudformation.md).

| Symptom | Cause | Fix |
| --- | --- | --- |
| `AlreadyExistsException` on a stack you thought was gone | The stack is in `ROLLBACK_COMPLETE`, which is delete-only | `DeleteStack`, then create again — this is what the CDK CLI does for you |
| `Stack:<arn> is in <status> state and can not be updated.` | The stack is not in a last-known-stable state | See [which state an operation may start from](./limitations.md#which-state-an-operation-may-start-from) |
| Every update refused, stack in `UPDATE_ROLLBACK_FAILED` | The update failed and its rollback failed too | Clear the blocker, then `ContinueUpdateRollback` — below |
| `DescribeStacks` says a stack does not exist, but you just deleted it | A `DELETE_COMPLETE` stack resolves by ARN only, as on AWS | Poll by the ARN `CreateStack` returned |
| Stack `DELETE_FAILED` with a resource still listed | A teardown refusal — a non-empty bucket, an IAM `DeleteConflict`, an EC2 `DependencyViolation`, RDS deletion protection, or a failed child stack | Clear the cause and call `DeleteStack` again; it resumes from what is left |
| A resource is `CREATE_COMPLETE` but does nothing | No handler, a deliberate no-op handler, or an inert/stub-tier owning service | Read its `ResourceStatusReason` — an `Overcast:`-prefixed sentence says which |
| A property you set has no effect | The resource type is provisioned but the property is not threaded through | The `ResourceStatusReason` says so where Overcast knows; otherwise check the owning service's page |
| A `{{resolve:…}}` reference fails the resource | `{{resolve:s3:…}}` is unsupported, or the secret or parameter does not exist | Create it, or use a supported scheme. Failing is deliberate — the alternative is a resource created with the literal reference text as its value |
| A secret rotation does not reach the deployed resource | References are compared as written, not as resolved — matching AWS | Change the resource in the template |
| A large `--template-body` is refused | AWS's 51,200-byte inline quota | Use `TemplateURL`, which lifts it to 1,000,000 bytes. `aws cloudformation deploy` and `cdk deploy` switch for you |
| SDK waiters see `CREATE_IN_PROGRESS` forever on a fast stack | `OVERCAST_CFN_SYNC_WAIT_MS` was set to `0` | Leave it at its `1000` default so a fast stack is terminal on the first `DescribeStacks` |

## Getting out of `UPDATE_ROLLBACK_FAILED`

A stack reaches it when an update fails and the automatic rollback fails too —
usually because both attempts were blocked by the same thing outside the stack, a
host port already bound or a resource something else is still holding. That state
has no last known stable state to update from, so every later `UpdateStack`,
`ExecuteChangeSet` and change set is refused until the rollback is finished.

`ContinueUpdateRollback` is the way out: clear the blocker by hand, call it, and
the rollback resumes — retrying the deletes that failed, retiring what the failed
attempt half-created, and driving the stack to `UPDATE_ROLLBACK_COMPLETE`, which is
updatable again. A retry that meets the same blocker lands back in
`UPDATE_ROLLBACK_FAILED` and can be tried again; the failure is reported on the
stack and its events, not to the caller, which sees `200` either way. Nested stacks
are continued before their parent.

`ResourcesToSkip` is honoured for a resource that cannot be cleaned up at all,
including AWS's `NestedStackName.ResourceLogicalID` form and deeper paths: the
named resource is left physically untouched, reported `UPDATE_COMPLETE`, and the
rest of the stack rolls back around it — AWS's own trade, a resource whose recorded
state is now a fiction in exchange for a stack that works. Every member is
validated before the operation is accepted, so a typo, or a resource the rollback
did not actually fail on, is a `ValidationError` rather than a half-continued
rollback.

`UPDATE_FAILED` is not this state: no rollback is under way to continue there, and
`RollbackStack` is the call that starts one.

## Why a deploy failed, after the rollback deleted the evidence

A rollback is faithful, which means it destroys the thing you needed to read. When
an ECS service cannot keep its tasks alive, the best sentence CloudFormation can
carry is the one ECS gives it — `(service Foo) is unable to consistently start
tasks successfully` — and the actual answer, that container `app` exited 1 having
printed `DATABASE_URL is not set`, is not expressible in CloudFormation's
vocabulary at all.

So Overcast reads the evidence at the moment a deploy is declared failed, *before*
teardown, and keeps it here:

```bash
curl "$AWS_ENDPOINT_URL/_overcast/cloudformation/stacks/demo/diagnostics"
```

What comes back is the CloudFormation reason, one sentence of Overcast's own
reading of the evidence, and a set of panes each tagged with where it came from:
`aws-api` for what `aws ecs describe-services` would also have returned,
`overcast-capture` for what Overcast preserved and AWS discards too,
`overcast-inference` for Overcast's interpretation. A counterfactual names what
real AWS would have left you instead, because the question worth answering is
whether the signal you are about to write a fix against is one you will also have
in production.

`AWS::ECS::Service` is the resource type covered today. Capture is best-effort and
time-boxed, so a collector that cannot answer costs a missing pane and never a
failed rollback; nothing gathered ever reaches an AWS-shaped field, and
`DescribeStacks`, `DescribeStackEvents` and `ListStackResources` are byte-identical
whether or not a diagnosis exists. A diagnosis is kept only for a stack whose *most
recent* deploy failed — a successful deploy clears it, so the endpoint answers `404`
for a healthy stack. Pass `?stackId=<arn>` for a stack no longer resolvable by
name.

Environment variable values, secrets and resolved parameter values are never
included, names only; a container's own output is reproduced verbatim because that
is the diagnosis, but Overcast never adds a value the container did not print
itself.
