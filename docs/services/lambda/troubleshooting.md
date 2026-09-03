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

Symptom, cause and fix for functions that will not run behind
[Lambda](../lambda.md).

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

Overcast establishes this address rather than assuming it. At startup it tries
each candidate in turn — best first — by binding it and running a throwaway
`busybox` container that connects back, and keeps the first one a container
actually reaches:

| # | Candidate | When it wins |
| --- | --- | --- |
| 1 | Overcast's own address on the control plane | Overcast is itself a container |
| 2 | The control plane's gateway | A native Linux Docker daemon |
| 3 | `host.docker.internal` | Docker Desktop — the VM-side route a host firewall does not filter |
| 4 | The host's own routable interface address | Everything else |
| 5 | Every interface (`0.0.0.0`), advertising `host.docker.internal` | Last resort |

Verification uses a throwaway ephemeral port rather than the Runtime API port.
The causes it catches are port-independent: a Windows firewall rule is
per-program, Docker Desktop routes per-address, an `--internal` bridge severs a
path rather than a port. So `container_verified=true` means the address is
reachable, not that a port-specific rule was tested.

The result is remembered per Docker daemon and per control plane in your data
directory (`runtime-api-host-<network>.json`), so only the first startup against
a given daemon pays for the probe, and two instances sharing a data directory
with different `OVERCAST_NETWORK` values do not overwrite each other. It is
dropped automatically when a container later dies during INIT without having
reached the address.

If the probe cannot run at all — an air-gapped host with no cached `busybox`, or
a daemon that refuses container creates — Overcast keeps the ordering above,
reports nothing as broken, and logs that the address was chosen without a
check.

The startup log says which candidate won:

```
lambda: Runtime API address   addr=host.docker.internal:9001 mode=docker-internal container_verified=true
```

### When nothing is reachable

`/_overcast/health` goes degraded with the Runtime API listener in state
`unreachable`, a **critical** advisory appears on the console's Metrics & Health
page, and one error is logged naming every address tried:

```
no Runtime API address is reachable from a container — every Lambda invocation will fail during INIT
tried=["gateway 172.19.0.1: could not bind 172.19.0.1", "docker-internal host.docker.internal: wget: download timed out",
       "host 192.168.8.19: wget: download timed out", "wildcard host.docker.internal: wget: download timed out"]
```

The observed error is the diagnosis: `Connection refused` means nothing was
listening there, a timeout means the packets were dropped on the way in.

Two causes, in order of likelihood:

- **A host firewall blocks inbound connections to the Overcast binary.** On
  Windows this is the default for a binary with no allow rule — a freshly built
  `overcast.exe` is blocked until someone clicks Allow, and the rule follows the
  *exact path* of the binary, so a rebuild somewhere else is blocked again. Add
  an allow rule for that path, or set
  `LAMBDA_RUNTIME_API_HOST=host.docker.internal` to pin the candidate that does
  not take the filtered path, or run Overcast itself as a container
  (`overcast start --docker --mount-docker-socket`), which puts it on the same
  network as the functions and takes the host route out of the picture.
- **The `_control` network was created `internal`.** An internal network has no
  route off the bridge at all. Overcast warns at startup when the existing
  network's isolation differs from what it wants, and rebuilds it itself when
  nothing is attached. When something is, run `overcast network reset
  overcast_control` — it stops the containers Overcast started, disconnects the
  ones it did not and leaves them running, then rebuilds the network to spec.
  Add `--dry-run` to see the plan first. Or point this instance at its own
  network with `OVERCAST_NETWORK`.

To check it by hand, against the network Overcast starts containers on:

```bash
docker run --rm --network overcast_control busybox \
  wget -T 4 -qO- http://192.168.1.20:4566/_overcast/health
```

### The exit-139 signature

An unreachable address used to present only as this, and the number points at
the wrong subsystem — the Node runtime segfaults because `invocation/next` never
answers, not because the init binary is broken:

```
lambda container exited during INIT   runtime_api=192.168.1.20:54254 runtime_api_connections=0
runtime_api_mode=host runtime_api_container_verified=false
container_output=[overcast-init] runtime API proxy error: GET /2018-06-01/runtime/invocation/next: dial tcp 192.168.1.20:54254: i/o timeout
```

`runtime_api_connections=0` beside `runtime_api_container_verified=false` is the
signature. When the probe established the failure in advance, the
`Runtime.InitError` returned to the caller carries the same explanation.

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

## Related

- [Lambda](../lambda.md) — quick start and what works
- [Lambda limitations](./limitations.md) — concurrency, runtimes, logging, VPC placement
- [Lambda examples](./examples.md) — hot reload, container images, layers, extensions
- [CloudWatch Logs](../cloudwatch-logs.md) — where function output lands
