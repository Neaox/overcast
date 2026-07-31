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

| Operation                       | Status         | Notes                                                                                                                                                                                                                | AWS Docs                                                                                                 |
| ------------------------------- | -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `CreateCapacityProvider`        | ✅ Supported   | FARGATE and FARGATE_SPOT built-ins seeded automatically; rejects FARGATE\* prefix for custom providers                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateCapacityProvider.html)        |
| `CreateCluster`                 | ✅ Supported   | Defaults name to "default" if empty                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateCluster.html)                 |
| `CreateService`                 | ✅ Supported   | Validates cluster + task def; creates PRIMARY deployment; reconciler starts tasks; Fargate: networkConfiguration required, deploymentController defaults to ECS                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateService.html)                 |
| `CreateTaskSet`                 | ✅ Supported   | Requires CODE_DEPLOY or EXTERNAL deployment controller; Scale defaults to 100%; ComputedDesiredCount calculated from scale                                                                                           | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_CreateTaskSet.html)                 |
| `DeleteAccountSetting`          | ✅ Supported   | Removes override; subsequent reads return the hardcoded default                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteAccountSetting.html)          |
| `DeleteAttributes`              | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteAttributes.html)              |
| `DeleteCluster`                 | ✅ Supported   | Sets status INACTIVE                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteCluster.html)                 |
| `DeleteService`                 | ✅ Supported   | Sets DRAINING, desired=0; reconciler stops excess tasks                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteService.html)                 |
| `DeleteTaskSet`                 | ✅ Supported   | Returns DRAINING status; removes from service task set list                                                                                                                                                          | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeleteTaskSet.html)                 |
| `DeregisterContainerInstance`   | ✅ Supported   | Removes from store; returns INACTIVE instance                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeregisterContainerInstance.html)   |
| `DeregisterTaskDefinition`      | ✅ Supported   | Marks INACTIVE                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DeregisterTaskDefinition.html)      |
| `DescribeCapacityProviders`     | ✅ Supported   | Filter by name or return all; failures array for missing providers                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeCapacityProviders.html)     |
| `DescribeClusters`              | ✅ Supported   | By name or ARN; returns failures for missing                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeClusters.html)              |
| `DescribeContainerInstances`    | ✅ Supported   | By ARN; failures array for missing                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeContainerInstances.html)    |
| `DescribeServices`              | ✅ Supported   | By name or ARN; recounts task state; returns failures for missing                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeServices.html)              |
| `DescribeTaskDefinition`        | ✅ Supported   | By family, family:rev, or ARN                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTaskDefinition.html)        |
| `DescribeTaskSets`              | ✅ Supported   | Filter by task set ID or ARN; returns failures for missing                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTaskSets.html)              |
| `DescribeTasks`                 | ✅ Supported   | By task ID or ARN; returns failures for missing                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTasks.html)                 |
| `DiscoverPollEndpoint`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DiscoverPollEndpoint.html)          |
| `ExecuteCommand`                | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ExecuteCommand.html)                |
| `ListAccountSettings`           | ✅ Supported   | Returns all known settings with effective values; filter by name; hardcoded defaults                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListAccountSettings.html)           |
| `ListAttributes`                | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListAttributes.html)                |
| `ListClusters`                  | ✅ Supported   |                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListClusters.html)                  |
| `ListContainerInstances`        | ✅ Supported   | Filter by cluster and optional status                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListContainerInstances.html)        |
| `ListServices`                  | ✅ Supported   | Filter by cluster; optional launchType filter                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListServices.html)                  |
| `ListTagsForResource`           | ✅ Supported   | List tags for an ECS resource                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTagsForResource.html)           |
| `ListTaskDefinitionFamilies`    | ✅ Supported   | Optional familyPrefix filter                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTaskDefinitionFamilies.html)    |
| `ListTaskDefinitions`           | ✅ Supported   | Optional familyPrefix filter                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTaskDefinitions.html)           |
| `ListTasks`                     | ✅ Supported   | Filter by cluster, desiredStatus, family                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ListTasks.html)                     |
| `PutAccountSetting`             | ✅ Supported   | Stores override for the caller; no per-principal scoping in emulator                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutAccountSetting.html)             |
| `PutAccountSettingDefault`      | ✅ Supported   | Identical to PutAccountSetting in emulator (no principal distinction)                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutAccountSettingDefault.html)      |
| `PutAttributes`                 | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutAttributes.html)                 |
| `PutClusterCapacityProviders`   | ✅ Supported   | Associates providers and default strategy with a cluster                                                                                                                                                             | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PutClusterCapacityProviders.html)   |
| `RegisterContainerInstance`     | ✅ Supported   | Metadata-only; auto-generates ARN; status ACTIVE; agentConnected true                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RegisterContainerInstance.html)     |
| `RegisterTaskDefinition`        | ✅ Supported   | Family:revision versioning; Fargate: validates awsvpc networkMode, cpu/memory required with valid combos; volumes with efsVolumeConfiguration and container mountPoints supported (EFS volumes mounted in live mode) | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RegisterTaskDefinition.html)        |
| `RunTask`                       | ✅ Supported   | Docker-backed when available; async state transitions; falls back to metadata-only; Fargate: networkConfiguration required, synthetic ENI attachment returned, platformVersion defaults to LATEST                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RunTask.html)                       |
| `StartTask`                     | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_StartTask.html)                     |
| `StopTask`                      | ✅ Supported   | Stops Docker containers; sets STOPPED; cancels pending transitions                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_StopTask.html)                      |
| `TagResource`                   | ✅ Supported   | Add tags to any ECS resource by ARN                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_TagResource.html)                   |
| `UntagResource`                 | ✅ Supported   | Remove tags by key                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UntagResource.html)                 |
| `UpdateCapacityProvider`        | ✅ Supported   | Rejects updates to built-in FARGATE/FARGATE_SPOT providers                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateCapacityProvider.html)        |
| `UpdateCluster`                 | ✅ Supported   | Validates cluster exists; returns current state                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateCluster.html)                 |
| `UpdateClusterSettings`         | ✅ Supported   | Accepts settings array (metadata only)                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateClusterSettings.html)         |
| `UpdateContainerAgent`          | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateContainerAgent.html)          |
| `UpdateContainerInstancesState` | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateContainerInstancesState.html) |
| `UpdateService`                 | ✅ Supported   | Update desiredCount and/or taskDefinition; new deployment on task def change; propagates networkConfiguration/platformVersion                                                                                        | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateService.html)                 |
| `UpdateServicePrimaryTaskSet`   | ✅ Supported   | Promotes target to PRIMARY; demotes all other task sets to ACTIVE                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateServicePrimaryTaskSet.html)   |
| `UpdateTaskSet`                 | ✅ Supported   | Updates Scale and recalculates ComputedDesiredCount                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_UpdateTaskSet.html)                 |

<!-- END overcast:capabilities -->
