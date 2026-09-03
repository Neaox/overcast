---
title: "ECS limitations"
description: "Every ECS divergence from AWS in one table, the four volume shapes a task may declare and what each gets, and what an awsvpc namespace and a VPC placement mean here."
section: "Service Reference"
tags:
  - docs
  - ecs
  - limitations
  - services
---

# ECS limitations

Every divergence from AWS behind [ECS](../ecs.md), the volume shapes a task may
declare, and what its networking gives it.

## Divergences

| Area | On AWS | Overcast |
| --- | --- | --- |
| Health checks | Container and target-group health checks | None. Staying `RUNNING` for a [settle window](./scheduler.md#the-settle-window) is the only evidence of health |
| The settle window | — | Buys evidence, not certainty: a container that survives it and then dies is still reported as a completed rollout first |
| `deploymentCircuitBreaker.rollback` | Redeploys the last known-good deployment | Accepted and echoed, never acted on — the deployment fails and stops there |
| `maximumPercent`/`minimumHealthyPercent` of `100`/`100` | Stalls silently | Stalls too, and says so once with `(service X) is holding at N task(s)` |
| Port collisions | Each `awsvpc` task has its own ENI | A `hostPort` is published on the one Docker host, so two live deployments contend for it |
| Deployment polling | `ecs wait services-stable` polls every 15 seconds | CloudFormation polls every 100 ms, so it sees moments AWS never shows a caller |
| IAM | Task and execution roles enforced | Stored, never enforced |
| Capacity providers, placement constraints, service discovery | Enforced | Accepted and ignored |
| `efsVolumeConfiguration` under `OVERCAST_EFS_MODE=mock`, or with no Docker daemon | Mounted | The task starts *without* the mount; writes to that path go to the container's writable layer |
| A `shared`-scope Docker volume | Never removed from a container instance | Never removed, the same |

The rollout arithmetic, the settle window and the circuit breaker are on
[Scheduler](./scheduler.md).

## Volumes

All four AWS shapes are honoured at placement, under AWS's own legality rules —
a task definition real ECS rejects is rejected here too.

| Volume shape | What a task gets |
| --- | --- |
| No configuration, or an empty `host` block | A scratch volume shared by every container that mounts it. Valid on Fargate. |
| `host.sourcePath` | A bind mount from that path on the container instance, which locally is the Docker daemon's host. **EC2 launch type only** — Fargate rejects it, as AWS does. |
| `dockerVolumeConfiguration` | A Docker volume. `scope: task` lives and dies with the task; `scope: shared` outlives it. `driver` and `driverOpts` reach Docker unchanged. **EC2 launch type only.** |
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
`ECS_KEEP_CONTAINERS=true` keeps volumes as well as containers, so a container
kept for post-mortem inspection can still answer the question it was kept for.

`efsVolumeConfiguration` mounts need EFS live mode, which is the default. Where
they are skipped, the warning names which cause applied, once per mount point per
task start.

## `awsvpc` tasks share one network namespace

Every container in an `awsvpc` task — which is every Fargate task — runs in
**one** namespace, as on AWS: one ENI, one address, one set of listening ports,
and `127.0.0.1` reaching every other container in the task. That is the contract
the sidecar pattern is built on, so `fastcgi_pass 127.0.0.1:9000` from nginx to
php-fpm works here unchanged.

Overcast builds it the way the ECS agent does — an extra `pause` container per
task whose network stack the others share.

| Consequence | Detail |
| --- | --- |
| A task shows one container more than its definition declares | Named `overcast-ecs-<cluster>-<task-id-prefix>-internal.ecs.pause` |
| Everything belonging to the namespace hangs off that container | The ENI address `DescribeTasks` reports and an ip-type target group registers, the task's ports pooled from every container definition, and the `/etc/hosts` entries and resolvers |
| `DescribeTasks` output | Reports the containers the task definition declared and nothing else, as AWS does |
| `bridge` network mode | Unaffected — each container gets its own namespace on the docker bridge, and loopback there reaches only the calling container |

## A task in a VPC is restricted to it

A task whose `networkConfiguration.awsvpcConfiguration` names subnets in a VPC
lands on that VPC's network **and nothing else** — it cannot reach a container
outside the VPC, exactly as on AWS. The way out is AWS's own field:
`assignPublicIp: ENABLED` also keeps the task on the default plane.

Overcast's own API endpoint stays reachable from every task either way, so
`AWS_ENDPOINT_URL` keeps working. See
[Lambda, ECS and VPCs](../../networking/vpcs.md) for what a refused connection
looks like.

## Related

- [ECS](../ecs.md) — quick start and what works
- [ECS scheduler](./scheduler.md) — rollouts, the settle window, the circuit breaker
- [ECS troubleshooting](./troubleshooting.md) — tasks that will not start or stay up
- [ECS operations](./operations.md) — per-operation status
- [Lambda, ECS and VPCs](../../networking/vpcs.md) — what VPC membership restricts
