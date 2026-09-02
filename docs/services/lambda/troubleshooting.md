---
title: "Lambda troubleshooting"
description: "Symptom, cause and fix for stub responses, throttles, layer init errors, extensions that reach real AWS, and hot reload that stops noticing edits."
section: "Service Reference"
tags:
  - docs
  - lambda
  - services
  - troubleshooting
---

# Lambda troubleshooting

Back to [Lambda](../lambda.md).

| Symptom | Cause | Fix |
| --- | --- | --- |
| `Invoke` returns a stub payload and the handler never runs | No Docker | Start Docker; bind-mount the socket when Overcast runs in a container |
| Every function fails with `Runtime.InitError` — "exited during init" or "did not initialize within 10s" | Containers cannot reach the Runtime API address Overcast bound | See [Containers cannot reach the Runtime API](#containers-cannot-reach-the-runtime-api) |
| `429 TooManyRequestsException`, `Reason: ConcurrentInvocationLimitExceeded` | The invocation waited for a container until the function's timeout | Raise `LAMBDA_MAX_INSTANCES` / `LAMBDA_MAX_INSTANCES_PER_FUNCTION`, or `LAMBDA_MAX_MEMORY_MB` |
| `429`, `Reason: ReservedFunctionConcurrentInvocationLimitExceeded` | `ReservedConcurrentExecutions` is set — and `0` disables the function | Raise or remove the reservation |
| `501 NotImplemented` from `CreateFunction` | The runtime is modelled and accepted by AWS, but Overcast has no execution image for it | Use a runtime AWS still accepts for `CreateFunction` |
| `400 InvalidParameterValueException` naming a successor runtime | The runtime is past AWS's block-create or block-update date | Move to the named successor |
| `Runtime.InitError`, "layer version not found" | The layer ARN is neither published locally nor in the cache | Publish it, or drop `{LayerName}_{Version}.zip` in the layer cache — see [Examples](./examples.md#layers) |
| A `PackageType=Image` function cannot pull its image | The registry is not running, so the ECR URI was left as written and resolves to real AWS | Start Docker and push the image — see [ECR](../ecr.md) |
| Provisioned concurrency stuck at `Status: FAILED` | Nothing can be allocated without Docker | Start Docker; the `StatusReason` says so |
| An extension still reaches real AWS or reports credential errors | The layer version predates endpoint-variable support, or the architecture is wrong | Use a recent layer version, matched to the **function's** architecture, and leave user-defined endpoint and credential variables unset |
| SQS calls from inside a function go to the container's own loopback | The queue URL was minted on the host and passed in | Overcast rewrites loopback URLs in the function environment; for a hand-built client pass `endpoint: process.env.AWS_ENDPOINT_URL` |
| Hot reload picks up the first edit and nothing after | Overcast cannot read the mounted path itself, so it cannot fingerprint the tree | When Overcast runs in a container, mount the source into it at the same path. A warning names the path |
| An edit inside `node_modules` is ignored | Dependency directories are fingerprinted by name only | Touch a file in your own source, or call `UpdateFunctionCode` |
| Two quick saves and only the first is seen | Filesystem timestamps are 1–2 second granular and the file kept its size | Make a size-changing edit, or wait a tick |
| Docker reports `mounts denied` or an invalid bind mount | The directory is not shared with Docker | Allow it in Docker Desktop's File Sharing settings |
| `platform.*` records are missing from CloudWatch Logs | The init-phase and `runtimeDone` records are `DEBUG` | Set `SystemLogLevel: DEBUG`. Telemetry and Logs API subscribers get them regardless |
| A 128 MB function shows ~80 ms p95 stalls under a sustained burst | The in-container init shares the function's CPU quota, which a burst exhausts | Give the function more memory, or leave a gap between invocations. It is real Lambda behaviour, not an emulator defect |

## Containers cannot reach the Runtime API

Every function fails the same way, zip and container image alike, and Overcast's
log carries a warning naming the endpoint and how many connections reached it:

```
lambda container exited during INIT   runtime_api=192.168.1.20:54254 runtime_api_connections=0
container_output=[overcast-init] runtime API proxy error: GET /2018-06-01/runtime/invocation/next: dial tcp 192.168.1.20:54254: i/o timeout
```

`runtime_api_connections=0` with an `i/o timeout` in the container's output is
the signature: the address Overcast bound is not reachable from containers.
Confirm it directly, against the network Overcast starts them on:

```bash
docker run --rm --network overcast_control busybox \
  wget -T 4 -qO- http://192.168.1.20:4566/_overcast/health
```

Two causes, in order of likelihood:

- **A host firewall blocks the Docker subnet.** On Docker Desktop, Overcast binds
  the host's own interface address, and containers reach it through the daemon's
  VM — which a firewall rule for the private network profile can drop. Allow the
  Docker subnet inbound, or run Overcast itself as a container
  (`overcast start --docker --mount-docker-socket`), which puts it on the same
  network as the functions and takes the host route out of the picture.
- **The `_control` network was created `internal`.** An internal network has no
  route off the bridge at all. Overcast warns at startup when the existing
  network's isolation differs from what it wants — remove it while nothing is
  attached (`docker network rm overcast_control`) and it is recreated, or point
  this instance at its own with `OVERCAST_NETWORK`.

## Working out why a warm environment went away

`GET /_overcast/lambda/instances` lists one entry per execution environment, and
the `lambda:InstanceEvicted` event carries an `evictedReason`:

| Reason | Meaning |
| --- | --- |
| `idle-ttl` | Nothing invoked it for 15 minutes |
| `config-change` | Code, environment, memory, timeout, handler, layers, logging config or VPC attachment changed |
| `function-deleted` | `DeleteFunction` |
| `container-died` | Docker reported the container gone without Overcast asking |
| `unhealthy` | The environment stopped answering |
| `surplus` | Above `LAMBDA_MAX_WARM_INSTANCES` for that function |
| `memory-pressure` | The aggregate memory budget was near its limit |
| `shutdown` | Overcast is stopping |
