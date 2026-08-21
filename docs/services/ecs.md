---
title: "ECS — Elastic Container Service"
description: "ECS uses AWS JSON 1.1 over HTTPS. All operations share a single endpoint URL; the action is identified by the X-Amz-Target header with prefix AmazonEC2ContainerServiceV20141113..."
section: "Service Reference"
tags:
  - container
  - docs
  - ecs
  - elastic
  - service
  - services
---

# ECS — Elastic Container Service

> AWS docs: https://docs.aws.amazon.com/AmazonECS/latest/APIReference/Welcome.html

ECS uses AWS JSON 1.1 over HTTPS. All operations share a single endpoint URL;
the action is identified by the `X-Amz-Target` header with prefix
`AmazonEC2ContainerServiceV20141113.`. RPC v2 CBOR is also supported via
the Smithy RPC path (`POST /service/ecs/operation/{Operation}`).

**Accepted wire protocols:** `awsJson1_1`, `rpcv2Cbor`

---

## Services place real tasks

A service's scheduler places tasks the same way `RunTask` does: real containers
when Docker is available, carrying the service's `networkConfiguration`, its
synthetic ENI attachment, the Fargate platform version, and the deployment ID in
`startedBy`. There is no separate metadata-only path for services.

`networkConfiguration` is required whenever the **task definition's**
`networkMode` is `awsvpc` — which is every Fargate task definition — rather than
whenever `launchType` is `FARGATE`. That is the rule AWS applies, and it is the
one that matters for CDK: since v2 the `FargateService` construct carries the
launch type in a `capacityProviderStrategy` and omits `launchType` entirely, so
a check keyed on the launch type never fires and the service is created unable
to place a task.

CloudFormation applies its own documented default of `DesiredCount: 1` for a new
`AWS::ECS::Service`, and waits for the service to reach a **steady state** before
the resource completes: one deployment left, running its desired count, and that
deployment reporting `rolloutState: COMPLETED`. A service that cannot place its
tasks — or cannot keep them alive — fails the stack rather than leaving it
`CREATE_COMPLETE` around a service running nothing.

The rollout state is the part that goes further than the AWS CLI's `ecs wait
services-stable`, which is satisfied by the counts alone. That waiter polls every
15 seconds; CloudFormation here polls every 100 milliseconds, so it sees moments
real AWS never shows a caller — including the instant a container that is about
to exit is briefly `RUNNING`. Counting that instant is what reported
`CREATE_COMPLETE` around a crash-looping service. A service under a `CODE_DEPLOY`
or `EXTERNAL` deployment controller reports no `rolloutState`, so for those the
counts remain the only evidence there is.

A stack **update** waits the same way, on the same definition of done. An update
swapping in a task definition whose tasks cannot start fails the resource with
the reason the service's own deployment and events give, and the stack unwinds —
rather than reporting `UPDATE_COMPLETE` around a service still catching up, or
one sitting on a rollout that failed. The failure is terminal for the resource:
CloudFormation does not answer it by replacing the service.

## A task definition change is a rollout

Changing a service's task definition starts a new deployment, and the new
deployment places its own tasks. Each task belongs to the deployment that
started it (`startedBy`), and a deployment's counts and `rolloutState` come from
its own tasks alone — so a service that has not yet started the new revision is
`IN_PROGRESS`, not `COMPLETED` on the strength of the tasks it is replacing.

The tasks of the superseded deployment keep serving until the new ones are
running, then stop with `stopCode` `ServiceSchedulerInitiated`, and the drained
deployment is dropped once it holds no tasks. A new deployment that never starts
anything therefore leaves the old tasks running, which is what ECS does with a
rollout that fails.

## Images published to the emulated ECR

A container definition may name an image by its ECR address —
`{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}` — which is what CDK
synthesises for a container asset, from `AWS::AccountId` and `AWS::Region`
rather than from the repository it pushed to. That hostname belongs to real AWS
and resolves, so pulling it as written leaves the machine and is refused
anonymously.

When the address is this account's registry, Overcast pulls it from the registry
it actually serves — the same `repositoryUri` the push went to — with the
credentials `ecr:GetAuthorizationToken` issues. Any other image is pulled exactly
as written: a public image keeps its meaning, and another registry is never
offered these credentials. The task still reports the image its definition asked
for; the rewrite decides where the bytes come from, not what was deployed.

Nothing has to be pushed through ECR for this to apply, and nothing changes for a
task whose image is a public one. If the registry is not running — Docker
unavailable, so nothing could have been pushed to it either — the reference is
left alone rather than pointed at a registry that cannot answer.

## When a task cannot start

A task whose containers fail to start is `STOPPED` with `stopCode`
`TaskFailedToStart` and a `stoppedReason` naming the AWS stopped-task error code
for the failure (`CannotPullContainerError`, `CannotStartContainerError`,
`ResourceInitializationError`). It is never reported `RUNNING`. The
metadata-only path — where a task goes `RUNNING` with nothing behind it — is
reserved for Overcast running without a container runtime at all.

The same reason is recorded on the **one container it belongs to**, as AWS
records it. Every other container in the task is `STOPPED` with no `reason` of
its own: they stopped because the task did, not because they failed. So a task
definition whose application image cannot be pulled reports
`CannotPullContainerError` against that container, and leaves a sidecar running
an unrelated public image saying nothing about an image it does not run.

For a service, each failed placement is recorded the way AWS records it:

- a service event, `(service X) was unable to place a task. Reason: …`, readable
  through `DescribeServices` and in the web UI's service row;
- `failedTasks` on the primary deployment;
- an `ecs:TaskStartFailed` event on the emulator's event stream.

## When a task starts and then dies

A service keeps its desired count: a task whose containers exit is replaced, and
so is one whose containers never started. Replacements back off as tasks keep
failing (500 ms doubling to 30 s), so a container that cannot be pulled, or
exits immediately, produces a crash loop that slows down rather than a hot loop
— the same shape as AWS's service throttle logic. A service that stopped
retrying after the first failed launch would never recover from a transient one.

A task that dies on its own is a **failed task for the deployment that placed
it**, exactly as one that never started is. AWS counts both in `failedTasks`,
and the distinction matters: a crash-looping container is RUNNING for a moment
on every replacement, so "a task is running right now" is not evidence the
deployment is healthy.

- `failedTasks` counts **consecutive** failures and is cleared only when the
  deployment reaches a steady state — never merely because a replacement was
  placed.
- `(service X) is unable to consistently start tasks successfully. For more
  information, see the Troubleshooting section.` is recorded once, as the count
  crosses three — once per episode, not once per failure.
- `(service X) has reached a steady state.` is recorded on the edge into a
  steady state. A service replacing a dying task never crosses that edge, so it
  never records it.

A task the scheduler or the caller stopped deliberately — a scale-in, a
`DeleteService` drain, `StopTask` — is not a failure. It keeps its
`ServiceSchedulerInitiated`/`UserInitiated` stop code and is not replaced.

A deployment's tasks have to **stay** `RUNNING` before they count toward its
desired count — a **settle window** of three seconds normally, ten once the
deployment has already failed a task and is replacing it on a backoff. Only then
does the deployment report `COMPLETED`.

The window applies on the healthy path too, and that costs a couple of seconds on
every ECS deploy. It is deliberate: a container that exits on startup is
`RUNNING` for an instant first, and anything sampling the service in that instant
— CloudFormation's provisioner polls every 100 ms — sees a finished rollout
around a service that is about to be at 0/1. AWS says the same thing in
substance: a deployment reaches `COMPLETED` when the service reaches a steady
state, and a service reaches one when it is "healthy and at the desired number of
tasks". Overcast runs no health checks, so staying up is the only evidence of
health it can collect.

**Divergence:** the window buys evidence, not certainty. A container that
survives the window and then dies is still reported as a completed rollout first.

## Rollout state and the deployment circuit breaker

`rolloutState` moves `IN_PROGRESS` → `COMPLETED` when the service reaches a
steady state. It moves to `FAILED` only when the service enabled a
`deploymentCircuitBreaker`, which matches AWS: with the circuit breaker off a
stuck deployment stays `IN_PROGRESS` and the scheduler keeps retrying, and the
failure is reported through service events alone.

With `deploymentCircuitBreaker.enable` set, the breaker trips once `failedTasks`
reaches AWS's documented threshold — half the desired count, clamped to
`[3, 200]`. The deployment then reports:

- `rolloutState: FAILED`, with
  `rolloutStateReason: ECS deployment circuit breaker: task failed to start.`;
- the service event
  `(service X) (deployment Y) deployment failed: tasks failed to start.`;
- no further task placements. `FAILED` is terminal — the deployment is replaced
  by the next one, it does not recover.

**Divergence:** `deploymentCircuitBreaker.rollback` is accepted and echoed but
not acted on. Real ECS redeploys the last known-good deployment when the breaker
trips with rollback enabled; Overcast fails the deployment and stops there.

## Container secrets

`containerDefinitions[].secrets` are resolved at task start and injected as
environment variables, from either source AWS supports, told apart by the ARN:

- **Secrets Manager** — `arn:aws:secretsmanager:…:secret:name-AbCdEf`, with the
  optional `:json-key:version-stage:version-id` suffix. Naming a key reads that
  field out of a JSON secret, which is what
  `ecs.Secret.fromSecretsManager(secret, "password")` produces and therefore the
  form most task definitions use.
- **SSM Parameter Store** — `arn:aws:ssm:…:parameter/name`, or a bare parameter
  name.

A secret that cannot be resolved is named in a warning and left out, rather than
injected as an empty value — which would be indistinguishable from a secret
whose value really is empty.

The value is read once for each new container, immediately before it is
created. A running task keeps the value it started with. After changing or
rotating a secret, launch a new standalone task or call `UpdateService` with
`forceNewDeployment: true`; the replacement deployment resolves the current
value without requiring a new task-definition revision. CDK's
`forceNewDeployment`/nonce support reaches the same path through
`AWS::ECS::Service`.

## Load balancers

A service with `loadBalancers` registers each task it places into the named
target group, at the task's ENI address and container port, and deregisters it
when the task stops or the service scales in. The load balancer forwards to
those targets, so `ApplicationLoadBalancedFargateService` produces a URL that
serves the application.

The URL is reachable on Overcast's own port rather than on the listener's — the
DNS name resolves to Overcast, which serves every host-routed endpoint on one
listener. CDK's `ServiceURL` output carries that port already.

For a Docker-backed awsvpc task the ENI address is the address the container
really holds on its VPC's Docker network, not one allocated alongside it, and
Overcast joins that network itself — otherwise the address it registers is one
it cannot dial, and forwarding fails with a gateway error or a timeout. Both
only apply when Overcast is itself containerised; running it on the host leaves
reaching a VPC bridge to the host's own routing.

## Container logs

A container definition using the `awslogs` log driver has its output shipped to
CloudWatch Logs, into the group named by `awslogs-group` and a stream named
`<awslogs-stream-prefix>/<container>/<task-id>` — the naming ECS uses. This
applies to every task whichever way it was started (`RunTask` or a service) and
under either launch type, because the driver is a property of the container
definition rather than of Fargate or EC2.

Without it, Overcast captures the final 200 lines when a container exits.
`GET /_overcast/ecs/tasks/{taskArn}/logs/{container}` returns that captured
output for a stopped task, or a live tail for a running container. This is an
emulator-only diagnostic, not an ECS or CloudWatch Logs API, and remains
addressable only while ECS retains the stopped task record.

## Volumes

A task definition's volumes are honoured at placement in all four shapes AWS
models, and the rules that decide which shapes are legal are AWS's own — a task
definition real ECS rejects is rejected here too, so a template cannot pass
locally and fail its first real deploy.

| Volume shape | What a task gets |
| --- | --- |
| No configuration, or an empty `host` block | A scratch volume shared by every container that mounts it — the way an nginx sidecar and an application container share a document root. Valid on Fargate. |
| `host.sourcePath` | A bind mount from that path on the container instance, which locally is the Docker daemon's host. **EC2 launch type only** — Fargate rejects it, as AWS does, with `host.sourcePath should not be set for volumes in Fargate`. |
| `dockerVolumeConfiguration` | A Docker volume. `scope: task` (the default) lives and dies with the task; `scope: shared` outlives it and is never removed by Overcast, exactly as ECS never removes one from a container instance. `driver` and `driverOpts` reach Docker unchanged. **EC2 launch type only.** |
| `efsVolumeConfiguration` | The Docker volume backing the file system — see below. |

A shared-scope volume is named as the task definition names it, because that is
the name it has on a container instance. With `autoprovision: false` it must
already exist and the task fails to start if it does not, which is what AWS
does — and which makes it the supported way to hand a task a volume you
prepared yourself:

```bash
docker volume create --driver local \
  --opt type=none --opt o=bind --opt device=/srv/app-data \
  app-data
```

Task-lifetime volumes are named `overcast-ecs-task-<task-id>-<volume-name>` and
are removed when the task stops; a volume still held by a container that has not
finished dying is retried, and anything left behind by a killed process is swept
at the next startup. `ECS_KEEP_CONTAINERS=true` keeps volumes as well as
containers — a container kept for post-mortem inspection with its volumes
deleted cannot answer the question it was kept for.

## Hot reload — editing local source inside a task

A task definition can point one of its scratch volumes at a directory on your
machine, so a save is live in the container on the next request with no image
rebuild and no redeploy. This is the ECS half of the same mechanism Lambda uses
([lambda.md § Hot Reload](./lambda.md#hot-reload)) — same tag, same flag family.

Nothing about it changes what the task definition means to AWS. The volume is an
ordinary name-only scratch volume, legal on Fargate and deployable as-is; only a
tag, which real AWS stores and ignores, says to back it with a bind locally. A
tag that reaches a production deploy costs nothing there.

Enable it on the server, for every compute service or just ECS:

```bash
OVERCAST_HOT_RELOAD=true overcast serve
```

Then tag the task definition with the volume to redirect and the host path:

```
overcast:hot-reload-path/<volume-name> = /absolute/host/path
```

The volume name lives in the tag **key**, so a Windows path in the value stays
unambiguous. With exactly one redirectable volume you can drop the suffix and
use the bare `overcast:hot-reload-path`, the same key Lambda takes.

Only a volume declared with a name and **no configuration** can be redirected —
an EFS, Docker or `host.sourcePath` volume already names its own storage, and
overriding it would mean ignoring what the definition asked for. The container
path and `readOnly` come from the container's own `mountPoints`, exactly as in
production. Windows paths are normalized (`F:\dev\app` → `/f/dev/app`).

Anything that cannot be honoured — the flag off, an ambiguous bare tag, an
unknown or unredirectable volume, a relative path — leaves the task running on
the plain scratch volumes it declared, and says so in a warning naming what to
fix. A task never silently starts with a mount you asked for and did not get.

If Docker refuses the bind, the task stops with
`CannotStartContainerError` and a `stoppedReason` naming the host paths and
pointing at Docker Desktop's File Sharing setting, which is the usual cause.

## EFS volumes

`efsVolumeConfiguration` mounts need EFS live mode, which backs each file
system with a Docker volume tasks can really read and write. That is the
default, so a task with a Docker daemon behind it gets its mount without any
configuration; `OVERCAST_EFS_MODE=mock`, or no reachable daemon, leaves the EFS
control plane fully emulated — file systems, mount targets, access points,
tags — with no data plane behind it. A task then starts *without* the mount and
its writes to that path go to the container's own writable layer and are lost
when it stops. The skipped-mount warning says which of the three causes applied
(mock mode, no container runtime, or an unresolvable reference) rather than
listing all three; it is logged once per mount point each time the task is
started, so a service replacing a crash-looping task repeats it every cycle.

## Task container networking

When Docker is available, `RunTask` starts real containers on the
shared data plane (`OVERCAST_NETWORK`). Those containers are siblings of
Overcast, not children of it, so Overcast takes three steps to keep them able
to call back into the emulator:

- **`AWS_ENDPOINT_URL`** is set to an address the task can actually dial —
  Overcast's own IP on the ECS network when Overcast itself runs in a
  container, otherwise the host address on the interface carrying the default
  route. A development machine usually has several interfaces, and picking the
  wrong one is a silent failure, so link-local (`169.254.0.0/16`) addresses and
  interfaces with no route out are skipped rather than offered to the task.
  `host.docker.internal` is used only as a last resort; because Docker Desktop
  synthesises that name and native Linux does not, it is paired with a
  `host.docker.internal:host-gateway` entry in the container's `/etc/hosts` so
  the fallback resolves on both.

- **Loopback URLs in the task environment are rewritten.** AWS SDKs resolve the
  SQS endpoint from the `QueueUrl` rather than from `AWS_ENDPOINT_URL`, so a
  queue URL written into a task definition by a host-side `cdk deploy` would
  otherwise point the task's SQS client at its own loopback. Values in
  `containerDefinitions[].environment` and in `RunTask` container overrides have
  `http://localhost:<port>` and `http://127.0.0.1:<port>` origins on Overcast's
  port replaced with the endpoint above. URLs on any other host or port are left
  alone.

- **Split-horizon hostnames are mapped in the container's `/etc/hosts`.**
  `localhost.overcast.sh`, `localhost.localstack.cloud`, and
  `localhost.floci.io` resolve to `127.0.0.1` in public DNS, so a URL built on
  one of them works from the host; inside the task the same names are pointed at
  Overcast, so a single URL is dialable from both sides. `OVERCAST_HOSTNAME` and
  the comma-separated `OVERCAST_SPLIT_HORIZON_HOSTS` are added to that set.

### Containers in an awsvpc task share one network namespace

Every container in an `awsvpc` task — which is every Fargate task, since it is
the only network mode Fargate supports — runs in **one** network namespace, as
on AWS: one ENI, one address, one set of listening ports, and `127.0.0.1`
reaching every other container in the same task.

That is the contract the ECS sidecar pattern is built on. `fastcgi_pass
127.0.0.1:9000` from an nginx container to a php-fpm container in the same task,
or an application reaching its X-Ray daemon on `127.0.0.1:2000`, is the
AWS-correct configuration and works here unchanged.

Overcast builds the shared namespace the way the ECS agent does, and for the
same reason: the agent starts an extra `pause` container per task, configures
its network namespace, and then starts the task's own containers so they share
its network stack ([Allocate a network interface for an Amazon ECS
task](https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-networking-awsvpc.html)).
So a task shows one container more than its definition
declares, named `overcast-ecs-<cluster>-<task>-internal.ecs.pause`, and
everything belonging to the namespace rather than to a process hangs off it:

- the ENI, so the `privateIPv4Address` that `DescribeTasks` reports and that an
  ip-type target group is registered with is the address **every** container in
  the task answers on, not the first one's;
- the task's published ports, pooled from every container definition — an
  `awsvpc` mapping's `hostPort` must be blank or equal its `containerPort`
  ([PortMapping](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PortMapping.html)),
  so a task's ports are one flat set rather than a per-container mapping;
- the `/etc/hosts` entries and resolvers described above, which Docker shares
  with every container that joins the namespace.

`DescribeTasks` reports the containers the task definition declared and nothing
else, as AWS does — the namespace container is infrastructure, not a container
the user asked for. It is torn down with the task, including when the task ends
because its own containers exited.

Other network modes are unaffected: `bridge` gives each container its own
namespace on the docker bridge, exactly as on EC2, and loopback there reaches
only the calling container.

### A task in a VPC is restricted to it

A task whose `networkConfiguration.awsvpcConfiguration` names subnets in a VPC
lands on that VPC's network **and nothing else** — it cannot reach a container
outside the VPC, exactly as on AWS. The way out is AWS's own field:
`assignPublicIp: ENABLED` also keeps the task on the default plane.

Overcast's own API endpoint stays reachable from every task either way, so
`AWS_ENDPOINT_URL` keeps working; the control plane is not the thing being
restricted. See [Networking § Lambda, ECS and VPCs](../networking.md) for what
is and is not enforced, and for what a refused connection looks like.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported | ❌ Unsupported |
| -------- | ------------ | -------------- |
| General  | 40           | 8              |

---

## Endpoints

### General

| Operation                       | Status         | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                             | AWS Docs                                                                                                 |
| ------------------------------- | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `CreateCapacityProvider`        | ✅ Supported   | FARGATE and FARGATE_SPOT built-ins seeded automatically; rejects FARGATE\* prefix for custom providers                                                                                                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateCapacityProvider.html)        |
| `CreateCluster`                 | ✅ Supported   | Defaults name to "default" if empty                                                                                                                                                                                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateCluster.html)                 |
| `CreateService`                 | ✅ Supported   | Validates cluster + task def; creates PRIMARY deployment with rolloutState; scheduler starts Docker-backed tasks carrying the service networkConfiguration, ENI attachment and deployment ID in startedBy; networkConfiguration required when the task definition's networkMode is awsvpc; deploymentController defaults to ECS; failed placements are recorded as service events and a FAILED rollout when a deploymentCircuitBreaker is enabled | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateService.html)                 |
| `CreateTaskSet`                 | ✅ Supported   | Requires CODE_DEPLOY or EXTERNAL deployment controller; Scale defaults to 100%; ComputedDesiredCount calculated from scale                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateTaskSet.html)                 |
| `DeleteAccountSetting`          | ✅ Supported   | Removes override; subsequent reads return the hardcoded default                                                                                                                                                                                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteAccountSetting.html)          |
| `DeleteAttributes`              | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteAttributes.html)              |
| `DeleteCluster`                 | ✅ Supported   | Sets status INACTIVE                                                                                                                                                                                                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteCluster.html)                 |
| `DeleteService`                 | ✅ Supported   | Sets DRAINING, desired=0; reconciler stops excess tasks                                                                                                                                                                                                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteService.html)                 |
| `DeleteTaskSet`                 | ✅ Supported   | Returns DRAINING status; removes from service task set list                                                                                                                                                                                                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteTaskSet.html)                 |
| `DeregisterContainerInstance`   | ✅ Supported   | Removes from store; returns INACTIVE instance                                                                                                                                                                                                                                                                                                                                                                                                     | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeregisterContainerInstance.html)   |
| `DeregisterTaskDefinition`      | ✅ Supported   | Marks INACTIVE                                                                                                                                                                                                                                                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeregisterTaskDefinition.html)      |
| `DescribeCapacityProviders`     | ✅ Supported   | Filter by name or return all; failures array for missing providers                                                                                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeCapacityProviders.html)     |
| `DescribeClusters`              | ✅ Supported   | By name or ARN; returns failures for missing                                                                                                                                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeClusters.html)              |
| `DescribeContainerInstances`    | ✅ Supported   | By ARN; failures array for missing                                                                                                                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeContainerInstances.html)    |
| `DescribeServices`              | ✅ Supported   | By name or ARN; recounts task state; returns failures for missing                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeServices.html)              |
| `DescribeTaskDefinition`        | ✅ Supported   | By family, family:rev, or ARN                                                                                                                                                                                                                                                                                                                                                                                                                     | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTaskDefinition.html)        |
| `DescribeTaskSets`              | ✅ Supported   | Filter by task set ID or ARN; returns failures for missing                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTaskSets.html)              |
| `DescribeTasks`                 | ✅ Supported   | By task ID or ARN; returns failures for missing                                                                                                                                                                                                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTasks.html)                 |
| `DiscoverPollEndpoint`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DiscoverPollEndpoint.html)          |
| `ExecuteCommand`                | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ExecuteCommand.html)                |
| `ListAccountSettings`           | ✅ Supported   | Returns all known settings with effective values; filter by name; hardcoded defaults                                                                                                                                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListAccountSettings.html)           |
| `ListAttributes`                | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListAttributes.html)                |
| `ListClusters`                  | ✅ Supported   |                                                                                                                                                                                                                                                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListClusters.html)                  |
| `ListContainerInstances`        | ✅ Supported   | Filter by cluster and optional status                                                                                                                                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListContainerInstances.html)        |
| `ListServices`                  | ✅ Supported   | Filter by cluster; optional launchType filter                                                                                                                                                                                                                                                                                                                                                                                                     | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListServices.html)                  |
| `ListTagsForResource`           | ✅ Supported   | List tags for an ECS resource                                                                                                                                                                                                                                                                                                                                                                                                                     | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTagsForResource.html)           |
| `ListTaskDefinitionFamilies`    | ✅ Supported   | Optional familyPrefix filter                                                                                                                                                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTaskDefinitionFamilies.html)    |
| `ListTaskDefinitions`           | ✅ Supported   | Optional familyPrefix filter                                                                                                                                                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTaskDefinitions.html)           |
| `ListTasks`                     | ✅ Supported   | Filter by cluster, desiredStatus, family                                                                                                                                                                                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTasks.html)                     |
| `PutAccountSetting`             | ✅ Supported   | Stores override for the caller; no per-principal scoping in emulator                                                                                                                                                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutAccountSetting.html)             |
| `PutAccountSettingDefault`      | ✅ Supported   | Identical to PutAccountSetting in emulator (no principal distinction)                                                                                                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutAccountSettingDefault.html)      |
| `PutAttributes`                 | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutAttributes.html)                 |
| `PutClusterCapacityProviders`   | ✅ Supported   | Associates providers and default strategy with a cluster                                                                                                                                                                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutClusterCapacityProviders.html)   |
| `RegisterContainerInstance`     | ✅ Supported   | Metadata-only; auto-generates ARN; status ACTIVE; agentConnected true                                                                                                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RegisterContainerInstance.html)     |
| `RegisterTaskDefinition`        | ✅ Supported   | Family:revision versioning; Fargate: validates awsvpc networkMode, cpu/memory required with valid combos, rejects host.sourcePath and dockerVolumeConfiguration as AWS does; all four volume shapes honoured at placement (EFS, host.sourcePath binds, dockerVolumeConfiguration, name-only scratch); tags stored against the revision's ARN, including overcast:hot-reload-path                                                                  | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RegisterTaskDefinition.html)        |
| `RunTask`                       | ✅ Supported   | Docker-backed when available, metadata-only when no container runtime is wired; a task whose containers fail to start is STOPPED with stopCode TaskFailedToStart rather than reported RUNNING; async state transitions; networkConfiguration required when the task definition's networkMode is awsvpc, synthetic ENI attachment returned, platformVersion defaults to LATEST; awsvpc task containers share one network namespace and address     | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RunTask.html)                       |
| `StartTask`                     | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_StartTask.html)                     |
| `StopTask`                      | ✅ Supported   | Stops Docker containers; sets STOPPED with stopCode UserInitiated; cancels pending transitions                                                                                                                                                                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_StopTask.html)                      |
| `TagResource`                   | ✅ Supported   | Add tags to any ECS resource by ARN                                                                                                                                                                                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_TagResource.html)                   |
| `UntagResource`                 | ✅ Supported   | Remove tags by key                                                                                                                                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UntagResource.html)                 |
| `UpdateCapacityProvider`        | ✅ Supported   | Rejects updates to built-in FARGATE/FARGATE_SPOT providers                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateCapacityProvider.html)        |
| `UpdateCluster`                 | ✅ Supported   | Validates cluster exists; returns current state                                                                                                                                                                                                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateCluster.html)                 |
| `UpdateClusterSettings`         | ✅ Supported   | Accepts settings array (metadata only)                                                                                                                                                                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateClusterSettings.html)         |
| `UpdateContainerAgent`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateContainerAgent.html)          |
| `UpdateContainerInstancesState` | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateContainerInstancesState.html) |
| `UpdateService`                 | ✅ Supported   | Update desiredCount and/or taskDefinition; task definition changes and forceNewDeployment start a new deployment whose tasks refresh launch-time secrets and mutable image tags; propagates networkConfiguration/platformVersion                                                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateService.html)                 |
| `UpdateServicePrimaryTaskSet`   | ✅ Supported   | Promotes target to PRIMARY; demotes all other task sets to ACTIVE                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateServicePrimaryTaskSet.html)   |
| `UpdateTaskSet`                 | ✅ Supported   | Updates Scale and recalculates ComputedDesiredCount                                                                                                                                                                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateTaskSet.html)                 |

<!-- END overcast:capabilities -->
