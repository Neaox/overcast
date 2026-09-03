---
title: "ECS — Elastic Container Service"
description: "Quick start, what the task and service scheduler runs for real, awsvpc networking, volumes, secrets and logs, and the health checks and IAM that are never enforced."
section: "Service Reference"
tags:
  - container
  - docs
  - ecs
  - services
---

# ECS — Elastic Container Service

Real Docker containers for standalone tasks and for services, with AWS's
deployment and `awsvpc` networking semantics on top.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws ecs create-cluster --cluster-name dev

aws ecs register-task-definition --family web --network-mode bridge \
  --container-definitions \
  '[{"name":"web","image":"nginx:alpine","essential":true,"memory":128}]'

aws ecs run-task --cluster dev --task-definition web
aws ecs list-tasks --cluster dev
```

## What works

| Area | Behaviour |
| --- | --- |
| Tasks | `RunTask` starts real containers when Docker is available. Without a container runtime, tasks are metadata only. |
| Services | The service scheduler places tasks the same way `RunTask` does — no separate metadata-only path — keeps the desired count, and replaces failures on a backoff. |
| Deployments | A task-definition change starts a new deployment with its own tasks, counts and `rolloutState`; the superseded deployment drains and is dropped. |
| `awsvpc` networking | Every container in a task shares one network namespace, one ENI address and one flat set of ports — so `fastcgi_pass 127.0.0.1:9000` between sidecars works as on AWS. |
| Volumes | All four AWS shapes: scratch, `host.sourcePath`, `dockerVolumeConfiguration` and `efsVolumeConfiguration`, under AWS's own legality rules. |
| Secrets | `containerDefinitions[].secrets` resolve from Secrets Manager (including a JSON key) and SSM Parameter Store at container start. |
| Logs | The `awslogs` driver ships container output to CloudWatch Logs under ECS's own stream naming. |
| Load balancers | A service with `loadBalancers` registers and deregisters its tasks in the target group, so `ApplicationLoadBalancedFargateService` serves. |
| ECR images | `{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}` is pulled from the registry Overcast serves, with the credentials `GetAuthorizationToken` issues. |
| Hot reload | A scratch volume can be backed by a directory on your machine, so a save is live on the next request. |

## Differences from AWS

| Area                                                         | On AWS                                   | Overcast                                                                            |
| ------------------------------------------------------------ | ---------------------------------------- | ----------------------------------------------------------------------------------- |
| Health checks                                                | Container and target-group health checks | None. Staying `RUNNING` for a settle window is the only evidence of health.         |
| Circuit breaker rollback                                     | Redeploys the last known-good deployment | `rollback` is accepted and echoed, never acted on                                   |
| IAM                                                          | Enforced                                 | Task and execution roles are stored, never enforced                                 |
| Port collisions                                              | Each `awsvpc` task has its own ENI       | A `hostPort` is published on the one Docker host, so two deployments contend for it |
| Capacity providers, placement constraints, service discovery | Enforced                                 | Accepted and ignored                                                                |

Every divergence, the volume rules and what a VPC placement restricts are in
[Limitations](./ecs/limitations.md); the rollout arithmetic, the settle window
and the circuit breaker are in [Scheduler](./ecs/scheduler.md).

## Gotchas

> [!WARNING]
> A service whose task definition maps a `hostPort` cannot roll out on the
> defaults (`maximumPercent: 200`), because the replacement needs the port the
> old task still holds. Set `maximumPercent: 100` with `minimumHealthyPercent: 0`
> to deploy stop-then-start instead — see
> [Scheduler](./ecs/scheduler.md#how-many-tasks-a-rollout-runs-at-once).

The other common stall is a service that places no task at all.

> [!IMPORTANT]
> `networkConfiguration` is required whenever the **task definition's**
> `networkMode` is `awsvpc`, not whenever `launchType` is `FARGATE`. CDK v2's
> `FargateService` carries the launch type in a `capacityProviderStrategy` and
> omits `launchType` entirely, so a check keyed on the launch type never fires.

<!-- BEGIN overcast:capabilities -->

## Operations

40 of 48 listed operations are implemented.
Per-operation status, notes and AWS API links: [ECS operations](ecs/operations.md).

<!-- END overcast:capabilities -->

## Related

- [ECS limitations](./ecs/limitations.md) — every divergence, volumes, networking
- [ECS troubleshooting](./ecs/troubleshooting.md) — tasks that will not start or stay up
- [ECS scheduler](./ecs/scheduler.md) — rollouts, the settle window, the circuit breaker
- [ECS examples](./ecs/examples.md) — hot reload, ECR images, secrets, logs
- [ECR](./ecr.md) — where task images come from
- [ELBv2](./elb.md) — where a service registers its tasks as targets
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/Welcome.html)
