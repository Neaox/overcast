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
`AWS::ECS::Service`, and waits for the service to reach that count before the
resource completes. A service that cannot place its tasks fails the stack rather
than leaving it `CREATE_COMPLETE` around a service running nothing.

A stack **update** waits the same way, on the same definition of done that the
AWS CLI's `ecs wait services-stable` uses: one deployment left, running its
desired count. An update swapping in a task definition whose tasks cannot start
fails the resource with the reason the service's own events give, and the stack
unwinds — rather than reporting `UPDATE_COMPLETE` around a service still
catching up, or one sitting on a rollout that failed. The failure is terminal
for the resource: CloudFormation does not answer it by replacing the service.

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

## When a task cannot start

A task whose containers fail to start is `STOPPED` with `stopCode`
`TaskFailedToStart` and a `stoppedReason` naming the AWS stopped-task error code
for the failure (`CannotPullContainerError`, `CannotStartContainerError`,
`ResourceInitializationError`). It is never reported `RUNNING`. The
metadata-only path — where a task goes `RUNNING` with nothing behind it — is
reserved for Overcast running without a container runtime at all.

For a service, each failed placement is recorded the way AWS records it:

- a service event, `(service X) was unable to place a task. Reason: …`, readable
  through `DescribeServices` and in the web UI's service row;
- `failedTasks` on the primary deployment, reset once a task runs successfully;
- `(service X) is unable to consistently start tasks successfully.` once
  failures accumulate;
- an `ecs:TaskStartFailed` event on the emulator's event stream.

`rolloutState` moves `IN_PROGRESS` → `COMPLETED` when the service reaches its
desired count. It moves to `FAILED` only when the service enabled a
`deploymentCircuitBreaker`, which matches AWS: with the circuit breaker off a
stuck deployment stays `IN_PROGRESS` and the scheduler keeps retrying, and the
failure is reported through service events alone.

A service also keeps its desired count: a task whose containers exit is
replaced, and so is one whose containers never started. Replacements back off as
tasks keep failing (500 ms doubling to 30 s), so a container that cannot be
pulled, or exits immediately, produces a crash loop that slows down rather than
a hot loop — the same shape as AWS's service throttle logic. A service that
stopped retrying after the first failed launch would never recover from a
transient one.

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

Without it a crash-looping task explains itself nowhere: the container is gone
before `docker logs` can reach it. `GET /_ecs/tasks/{taskArn}/logs/{container}`
remains available as an emulator-only tail of a *running* container.

## EFS volumes

`efsVolumeConfiguration` mounts need EFS live mode (`OVERCAST_EFS_MODE=live`),
which backs each file system with a Docker volume tasks can really read and
write. In the default mock mode the EFS control plane is still fully emulated —
file systems, mount targets, access points, tags — but there is no data plane, so
a task starts *without* the mount and its writes to that path go to the
container's own writable layer and are lost when it stops. The skipped-mount
warning says which of the three causes applied (mock mode, no container runtime,
or an unresolvable reference) rather than listing all three.

## Task container networking

When Docker is available, `RunTask` starts real containers on the
`overcast_ecs` network (`ECS_NETWORK`). Those containers are siblings of
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
| `RegisterTaskDefinition`        | ✅ Supported   | Family:revision versioning; Fargate: validates awsvpc networkMode, cpu/memory required with valid combos; volumes with efsVolumeConfiguration and container mountPoints supported (EFS volumes mounted in live mode)                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RegisterTaskDefinition.html)        |
| `RunTask`                       | ✅ Supported   | Docker-backed when available, metadata-only when no container runtime is wired; a task whose containers fail to start is STOPPED with stopCode TaskFailedToStart rather than reported RUNNING; async state transitions; networkConfiguration required when the task definition's networkMode is awsvpc, synthetic ENI attachment returned, platformVersion defaults to LATEST                                                                     | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RunTask.html)                       |
| `StartTask`                     | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_StartTask.html)                     |
| `StopTask`                      | ✅ Supported   | Stops Docker containers; sets STOPPED with stopCode UserInitiated; cancels pending transitions                                                                                                                                                                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_StopTask.html)                      |
| `TagResource`                   | ✅ Supported   | Add tags to any ECS resource by ARN                                                                                                                                                                                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_TagResource.html)                   |
| `UntagResource`                 | ✅ Supported   | Remove tags by key                                                                                                                                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UntagResource.html)                 |
| `UpdateCapacityProvider`        | ✅ Supported   | Rejects updates to built-in FARGATE/FARGATE_SPOT providers                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateCapacityProvider.html)        |
| `UpdateCluster`                 | ✅ Supported   | Validates cluster exists; returns current state                                                                                                                                                                                                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateCluster.html)                 |
| `UpdateClusterSettings`         | ✅ Supported   | Accepts settings array (metadata only)                                                                                                                                                                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateClusterSettings.html)         |
| `UpdateContainerAgent`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateContainerAgent.html)          |
| `UpdateContainerInstancesState` | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateContainerInstancesState.html) |
| `UpdateService`                 | ✅ Supported   | Update desiredCount and/or taskDefinition; a task def change starts a new deployment that places its own tasks and retires the superseded one's as they come up; propagates networkConfiguration/platformVersion                                                                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateService.html)                 |
| `UpdateServicePrimaryTaskSet`   | ✅ Supported   | Promotes target to PRIMARY; demotes all other task sets to ACTIVE                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateServicePrimaryTaskSet.html)   |
| `UpdateTaskSet`                 | ✅ Supported   | Updates Scale and recalculates ComputedDesiredCount                                                                                                                                                                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateTaskSet.html)                 |

<!-- END overcast:capabilities -->
