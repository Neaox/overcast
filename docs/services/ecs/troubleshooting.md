---
title: "ECS troubleshooting"
description: "Symptom, cause and fix for ECS tasks that will not start, services that will not roll out, missing mounts, and hot-reload tags that are ignored."
section: "Service Reference"
tags:
  - docs
  - ecs
  - services
  - troubleshooting
---

# ECS troubleshooting

Symptom, cause and fix for tasks that will not start or stay up behind
[ECS](../ecs.md).

| Symptom | Cause | Fix |
| --- | --- | --- |
| `CreateService` succeeds but no task is ever placed | The task definition is `awsvpc` and the service has no `networkConfiguration` | Supply it. The requirement follows the task definition's `networkMode`, not `launchType` |
| `was unable to place a task. Reason: … port is already allocated` | A `hostPort` mapping, and both deployments are alive at once | Set `maximumPercent: 100` / `minimumHealthyPercent: 0` — see [Scheduler](./scheduler.md#how-many-tasks-a-rollout-runs-at-once) |
| Task `STOPPED` with `CannotPullContainerError` | The image is not in the registry, or the address is not this account's ECR | Push it, or check the `repositoryUri` in [ECR](../ecr.md) |
| Task `STOPPED` with `ResourceInitializationError` | The task's network namespace or ENI could not be set up | Check the VPC's Docker network exists and that Overcast can reach it |
| Task `STOPPED` with `CannotStartContainerError` naming host paths | Docker refused a bind mount | Allow the directory in Docker Desktop's File Sharing settings |
| Service stuck `IN_PROGRESS`, tasks cycling | The container exits shortly after start | Read the retained tail: `GET /_overcast/ecs/tasks/{taskArn}/logs/{container}` |
| `is unable to consistently start tasks successfully` | Three consecutive failed placements | The event fires once per episode; the cause is on the individual task's `stoppedReason` |
| `is holding at N task(s)` | `maximumPercent` and `minimumHealthyPercent` leave no room to move | Widen one of them. AWS stalls here silently; this event is informational, not a failure |
| A secret is missing from the container's environment | It could not be resolved, and is left out rather than injected empty | Check the warning naming the ARN; verify the secret or parameter exists |
| Writes to an EFS mount path vanish when the task stops | The mount was skipped, so writes went to the container's writable layer | The warning names the cause: `OVERCAST_EFS_MODE=mock`, no container runtime, or an unresolvable reference |
| A hot-reload tag has no effect | The flag is off, the tag is ambiguous, or the volume is not redirectable | The warning names which. Only a name-only scratch volume can be redirected, and the path must be absolute |
| Only the first invocation sees edited source | Overcast cannot read the host path itself, so it cannot fingerprint the tree | When Overcast runs in a container, mount the source at the same path into it too |
| A task shows one more container than the definition declares | The `awsvpc` network namespace container | Expected. `DescribeTasks` reports only the declared containers |

## A stack completes around a service that is not working

CloudFormation waits for one deployment at its desired count with
`rolloutState: COMPLETED`, and a [settle window](./scheduler.md#the-settle-window)
keeps a container that exits on startup from counting. A container that survives
the window and then dies is still reported as a completed rollout first.

When a deploy *does* fail, the rollback destroys the evidence. Overcast reads it
first and keeps it at
`GET /_overcast/cloudformation/stacks/{stackName}/diagnostics` — see
[CloudFormation](../cloudformation.md).

## Related

- [ECS](../ecs.md) — quick start and what works
- [ECS limitations](./limitations.md) — every divergence, volumes, networking
- [ECS scheduler](./scheduler.md) — rollouts, the settle window, the circuit breaker
- [ECS examples](./examples.md) — ECR images, secrets, logs, load balancers, hot reload
- [ECR](../ecr.md) — where task images come from
