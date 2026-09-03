---
title: "ECS scheduler"
description: "How Overcast's ECS scheduler decides a deployment is done: the settle window, what counts as a failed task, the rollout arithmetic, and when the circuit breaker trips."
section: "Service Reference"
tags:
  - docs
  - ecs
  - scheduler
  - services
---

# ECS scheduler

How the scheduler behind [ECS](../ecs.md) decides a deployment is done, and what
it does with a task that will not stay up.

## What "done" means for a deployment

A **steady state** is one deployment left, running its desired count, with
`rolloutState: COMPLETED`. That is what CloudFormation waits for on an
`AWS::ECS::Service`, so a service that cannot place its tasks — or cannot keep
them alive — fails the stack rather than leaving it `CREATE_COMPLETE` around a
service running nothing.

The rollout state goes further than the AWS CLI's `ecs wait services-stable`,
which the counts alone satisfy. A service under a `CODE_DEPLOY` or `EXTERNAL`
deployment controller reports no `rolloutState`, so for those the counts are the
only evidence there is.

### The settle window

A deployment's tasks have to **stay** `RUNNING` before they count toward its
desired count — three seconds normally, ten once the deployment has already
failed a task and is replacing it on a backoff.

A container that exits on startup is `RUNNING` for an instant first, and anything
sampling the service in that instant sees a finished rollout around a service
about to be at 0/1. AWS says the same thing in substance — a deployment completes
when the service is "healthy and at the desired number of tasks" — but Overcast
runs no health checks, so staying up is the only evidence of health it can
collect. The window applies on the healthy path too, and costs a couple of
seconds on every deploy.

## A task definition change is a rollout

Each task belongs to the deployment that started it (`startedBy`), and a
deployment's counts and `rolloutState` come from its own tasks alone — so a
service that has not yet started the new revision is `IN_PROGRESS`, not
`COMPLETED` on the strength of the tasks it is replacing.

The superseded deployment's tasks keep serving until the new ones are running,
then stop with `stopCode` `ServiceSchedulerInitiated`, and the drained deployment
is dropped once it holds no tasks. A new deployment that never starts anything
leaves the old tasks running, which is what ECS does with a rollout that fails.

## When a task cannot start, and when it dies later

A task whose containers fail to start is `STOPPED` with `stopCode`
`TaskFailedToStart` and a `stoppedReason` naming AWS's own error code —
`CannotPullContainerError`, `CannotStartContainerError` or
`ResourceInitializationError`. It is never reported `RUNNING`. The metadata-only
path, where a task goes `RUNNING` with nothing behind it, is reserved for
Overcast running without a container runtime at all.

The reason is recorded on the **one container it belongs to**, as AWS records it.
Every other container in the task is `STOPPED` with no `reason` of its own: they
stopped because the task did.

A service keeps its desired count, so a task that exits is replaced and so is one
that never started. Replacements back off (500 ms doubling to 30 s), turning a
crash loop into something that slows down rather than a hot loop. A task that
dies on its own is a **failed task for the deployment that placed it**, exactly
as one that never started is:

| Signal | Rule |
| --- | --- |
| `failedTasks` | Counts **consecutive** failures. Cleared only when the deployment reaches a steady state, never merely because a replacement was placed |
| `(service X) is unable to consistently start tasks successfully.` | Recorded once, as the count crosses three — once per episode, not once per failure |
| `(service X) has reached a steady state.` | Recorded on the edge into a steady state, which a service replacing a dying task never crosses |
| A task stopped deliberately — a scale-in, a `DeleteService` drain, `StopTask` | Not a failure. Keeps its `ServiceSchedulerInitiated`/`UserInitiated` stop code, and is not replaced |

## How many tasks a rollout runs at once

`deploymentConfiguration.maximumPercent` and `minimumHealthyPercent` are honoured
and resolved against `desiredCount` the way AWS resolves them: `maximumPercent`
caps tasks that have not stopped, across both deployments, rounded down;
`minimumHealthyPercent` floors tasks reporting `RUNNING`, rounded up. They
default to AWS's `200` and `100`, which is what makes the default deploy a
start-then-stop.

Locally that ordering is not free: while both deployments are alive they contend
for any `hostPort` the task maps, and the replacement cannot start. Left on the
defaults the service retries on a backoff, reports `(service X) was unable to
place a task. Reason: … port is already allocated`, and with a
`deploymentCircuitBreaker` eventually fails the deployment.

Inverting the order deploys cleanly: the port is given up before it is asked for
again, at the cost of a moment of downtime.

```ts
new ecs.FargateService(this, 'Service', {
  cluster,
  taskDefinition,
  maxHealthyPercent: 100,
  minHealthyPercent: 0,
});
```

`100`/`100` cannot make progress at any desired count — nothing may be retired
and nothing more may be placed. Overcast says so once, with `(service X) is
holding at N task(s): its deployment configuration permits at most M and requires
K running.` That event is routine rather than failure-shaped, so CloudFormation
will not report it as the reason a stack failed.

## Rollout state and the circuit breaker

`rolloutState` moves `IN_PROGRESS` → `COMPLETED` at a steady state. It moves to
`FAILED` **only** when the service enabled a `deploymentCircuitBreaker`, matching
AWS: with the breaker off a stuck deployment stays `IN_PROGRESS`, the scheduler
keeps retrying, and the failure is reported through service events alone.

With the breaker on, it trips once `failedTasks` reaches AWS's documented
threshold — half the desired count, clamped to `[3, 200]` — and the deployment
reports `rolloutState: FAILED` with
`rolloutStateReason: ECS deployment circuit breaker: task failed to start.`, the
service event `(service X) (deployment Y) deployment failed: tasks failed to
start.`, and no further placements. `FAILED` is terminal.

## Related

- [ECS](../ecs.md) — quick start and what works
- [ECS limitations](./limitations.md) — the divergence table, volumes and networking
- [ECS troubleshooting](./troubleshooting.md) — tasks that will not start or stay up
- [ECS operations](./operations.md) — per-operation status
- [CloudFormation limitations](../cloudformation/limitations.md) — what a stack waits on
