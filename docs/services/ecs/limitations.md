---
title: "ECS limitations"
description: "How Overcast's ECS scheduler decides a deployment is done, what a rollout does with ports and counts, and which volume and networking rules are enforced."
section: "Service Reference"
tags:
  - docs
  - ecs
  - limitations
  - services
---

# ECS limitations

Back to [ECS](../ecs.md).

## What "done" means for a deployment

CloudFormation waits for an `AWS::ECS::Service` to reach a **steady state**: one
deployment left, running its desired count, with `rolloutState: COMPLETED`. A
service that cannot place its tasks — or cannot keep them alive — fails the stack
rather than leaving it `CREATE_COMPLETE` around a service running nothing. A
stack update waits on the same definition, and a failure there is terminal for
the resource: CloudFormation does not answer it by replacing the service.

The rollout state goes further than the AWS CLI's `ecs wait services-stable`,
which the counts alone satisfy. That waiter polls every 15 seconds;
CloudFormation here polls every 100 milliseconds, so it sees moments real AWS
never shows a caller — including the instant a container that is about to exit is
briefly `RUNNING`. A service under a `CODE_DEPLOY` or `EXTERNAL` deployment
controller reports no `rolloutState`, so for those the counts are the only
evidence there is.

### The settle window

A deployment's tasks have to **stay** `RUNNING` before they count toward its
desired count — three seconds normally, ten once the deployment has already
failed a task and is replacing it on a backoff.

The window applies on the healthy path too, and that costs a couple of seconds on
every deploy. It is deliberate: a container that exits on startup is `RUNNING`
for an instant first, and anything sampling the service in that instant sees a
finished rollout around a service that is about to be at 0/1. AWS says the same
thing in substance — a deployment completes when the service is "healthy and at
the desired number of tasks" — but Overcast runs no health checks, so staying up
is the only evidence of health it can collect.

**Divergence:** the window buys evidence, not certainty. A container that
survives the window and then dies is still reported as a completed rollout first.

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
crash loop into something that slows down rather than a hot loop.

A task that dies on its own is a **failed task for the deployment that placed
it**, exactly as one that never started is:

- `failedTasks` counts **consecutive** failures and is cleared only when the
  deployment reaches a steady state — never merely because a replacement was
  placed.
- `(service X) is unable to consistently start tasks successfully.` is recorded
  once, as the count crosses three — once per episode, not once per failure.
- `(service X) has reached a steady state.` is recorded on the edge into a steady
  state, which a service replacing a dying task never crosses.

A task stopped deliberately — a scale-in, a `DeleteService` drain, `StopTask` —
is not a failure. It keeps its `ServiceSchedulerInitiated`/`UserInitiated` stop
code and is not replaced.

## How many tasks a rollout runs at once

`deploymentConfiguration.maximumPercent` and `minimumHealthyPercent` are honoured
and resolved against `desiredCount` the way AWS resolves them: `maximumPercent`
caps tasks that have not stopped, across both deployments, rounded down;
`minimumHealthyPercent` floors tasks reporting `RUNNING`, rounded up. They
default to AWS's `200` and `100`, which is what makes the default deploy a
start-then-stop.

Locally that ordering is not free. A task whose port mapping carries a `hostPort`
publishes it on the one Docker host, so while both deployments are alive they
contend for it and the replacement cannot start — where on AWS each `awsvpc` task
has its own ENI. Left on the defaults the service retries on a backoff, reports
`(service X) was unable to place a task. Reason: … port is already allocated`,
and with a `deploymentCircuitBreaker` eventually fails the deployment.

Inverting the order deploys cleanly, because the port is given up before it is
asked for again. It costs a moment of downtime, which is the same trade AWS
offers and usually the right one locally:

```ts
new ecs.FargateService(this, 'Service', {
  cluster,
  taskDefinition,
  maxHealthyPercent: 100,
  minHealthyPercent: 0,
});
```

**Divergence:** none in the arithmetic, but `100`/`100` cannot make progress at
any desired count — nothing may be retired and nothing more may be placed. AWS
stalls there silently; Overcast stalls too and says so once, with
`(service X) is holding at N task(s): its deployment configuration permits at
most M and requires K running.` That event is routine rather than failure-shaped,
so CloudFormation will not report it as the reason a stack failed.

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

**Divergence:** `deploymentCircuitBreaker.rollback` is accepted and echoed but
not acted on. Real ECS redeploys the last known-good deployment; Overcast fails
the deployment and stops there.

## Volumes

All four AWS shapes are honoured at placement, under AWS's own legality rules —
a task definition real ECS rejects is rejected here too.

| Volume shape | What a task gets |
| --- | --- |
| No configuration, or an empty `host` block | A scratch volume shared by every container that mounts it. Valid on Fargate. |
| `host.sourcePath` | A bind mount from that path on the container instance, which locally is the Docker daemon's host. **EC2 launch type only** — Fargate rejects it, as AWS does. |
| `dockerVolumeConfiguration` | A Docker volume. `scope: task` lives and dies with the task; `scope: shared` outlives it and is never removed, exactly as ECS never removes one from a container instance. `driver` and `driverOpts` reach Docker unchanged. **EC2 launch type only.** |
| `efsVolumeConfiguration` | The Docker volume backing the file system, in EFS live mode. |

A shared-scope volume is named as the task definition names it. With
`autoprovision: false` it must already exist and the task fails to start if it
does not — which makes it the supported way to hand a task a volume you prepared
yourself:

```bash
docker volume create --driver local \
  --opt type=none --opt o=bind --opt device=/srv/app-data \
  app-data
```

Task-lifetime volumes are named `overcast-ecs-task-<task-id-prefix>-<volume-name>`
(the first eight characters of the task ID) and are removed when the task stops;
anything left behind by a killed process is swept at the next startup.
`ECS_KEEP_CONTAINERS=true` keeps volumes as well as containers — a container kept
for post-mortem inspection with its volumes deleted cannot answer the question it
was kept for.

`efsVolumeConfiguration` mounts need EFS live mode, which is the default. Under
`OVERCAST_EFS_MODE=mock`, or with no reachable Docker daemon, the EFS control
plane is fully emulated with no data plane behind it: the task starts *without*
the mount and its writes to that path go to the container's writable layer. The
skipped-mount warning names which cause applied, once per mount point per task
start.

## `awsvpc` tasks share one network namespace

Every container in an `awsvpc` task — which is every Fargate task — runs in
**one** namespace, as on AWS: one ENI, one address, one set of listening ports,
and `127.0.0.1` reaching every other container in the task. That is the contract
the sidecar pattern is built on, so `fastcgi_pass 127.0.0.1:9000` from nginx to
php-fpm works here unchanged.

Overcast builds it the way the ECS agent does — an extra `pause` container per
task whose network stack the others share. So a task shows one container more
than its definition declares, named
`overcast-ecs-<cluster>-<task-id-prefix>-internal.ecs.pause`, and everything
belonging to the namespace hangs off it: the ENI address `DescribeTasks` reports
and an ip-type target group registers, the task's ports pooled from every
container definition, and the `/etc/hosts` entries and resolvers.

`DescribeTasks` reports the containers the task definition declared and nothing
else, as AWS does. Other network modes are unaffected: `bridge` gives each
container its own namespace on the docker bridge, and loopback there reaches only
the calling container.

## A task in a VPC is restricted to it

A task whose `networkConfiguration.awsvpcConfiguration` names subnets in a VPC
lands on that VPC's network **and nothing else** — it cannot reach a container
outside the VPC, exactly as on AWS. The way out is AWS's own field:
`assignPublicIp: ENABLED` also keeps the task on the default plane.

Overcast's own API endpoint stays reachable from every task either way, so
`AWS_ENDPOINT_URL` keeps working. See
[Networking § Lambda, ECS and VPCs](../../networking.md) for what is and is not
enforced, and for what a refused connection looks like.
