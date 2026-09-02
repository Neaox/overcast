---
title: "Troubleshooting"
description: "Start here when something is wrong: a symptom-to-guide index, then every startup warning and runtime advisory Overcast raises, with what each one means and the fix."
section: "Troubleshooting"
tags:
  - docs
  - troubleshooting
  - preflight
  - warnings
  - debugging
---

# Troubleshooting

Find the symptom, follow the link. If nothing matches, start the daemon with
`OVERCAST_DEBUG=true` and read the request's full trace in the web console or at
[`/_overcast/debug/trace/{requestId}`](./debug-endpoints.md).

| Symptom | Where the answer is |
| --- | --- |
| Overcast logged a `WARN` at startup | [Startup warnings](#startup-warnings), below |
| A resource I created is gone after a restart | [Storage and persistence](./storage.md) — and [Builds without SQLite](./storage.md#builds-without-sqlite) if you run the slim image or `overcastd` |
| A hostname will not resolve, or works from the shell but not a container | [Networking](./networking.md#docker-compose-and-sibling-containers) |
| A Lambda or task cannot reach a database | [Lambda, ECS and VPCs](./networking.md#lambda-ecs-and-vpcs) |
| A function cannot reach the internet or real AWS (`ENETUNREACH`) | [A function in a VPC fails with `ENETUNREACH`](#a-function-in-a-vpc-fails-with-enetunreach), below |
| `cdk deploy` fails, or a stack sits in `CREATE_IN_PROGRESS` | [CDK § Troubleshooting](./cdk.md#troubleshooting) |
| CDK reports no private subnet groups, or stale VPC IDs | [Local VPCs for CDK § Troubleshooting](./cdk/local-vpc.md#troubleshooting) |
| A browser will not trust the certificate | [HTTPS and HTTP/2](./https.md) |
| The console freezes while Lambdas run | [HTTPS and HTTP/2](./https.md) — it is the browser's 6-connection limit |
| My file edits are not reaching a container | [The inner loop § When it does not work](./local-dev.md#when-it-does-not-work) |
| It all feels slow | [Performance](./performance.md) — usually the client, not the emulator |
| Something that worked on LocalStack does not here | [Migrating from LocalStack § Troubleshooting](./migration-from-localstack.md#troubleshooting) |
| An operation returns `501 Not Implemented` | The service's page under [Services](./README.md#services) — it is not emulated yet |

## Startup warnings

A handful of environment mistakes cost real time because they do not look like
environment mistakes — Overcast answers normally, and the symptom (an empty
console list, a container that never starts, data that is not where you left it)
reads exactly like a bug in the emulator. Where Overcast can tell, it says so:
one actionable `WARN`, the moment the symptom appears, never on a healthy setup.

| Message names… | Means |
| --- | --- |
| `Docker is not reachable for: ...` | Container-backed services (ECS, RDS, Lambda, MSK, …) found no Docker daemon and will run metadata-only: creates succeed, nothing starts. Start Docker, or check the socket is readable by this user. |
| `the API is published on a different host port (N) than it listens on (N)` | The container remapped its port (`-p 4580:4566`). Overcast rewrites the common cases already; publish 1:1 if something still compares the port literally, such as a Cognito token's `iss`. |
| `a request arrived addressed to "..." — a real AWS hostname` | A hosts-file entry, DNS override, or proxy is sending `*.amazonaws.com` traffic here. Point `AWS_ENDPOINT_URL` at Overcast explicitly, or remove the redirect. |
| `OVERCAST_HOSTNAME=... does not resolve` | This host's resolver cannot resolve `OVERCAST_HOSTNAME` or its subdomains, which breaks virtual-hosted S3 and `cdk deploy` asset publishing. The message names the fix for this host; see [Networking](./networking.md). |
| `this run is memory-only, but an existing Overcast database was found` | `OVERCAST_STATE=memory` — explicitly, or a build without SQLite resolving `auto` to memory — is ignoring a database that already holds data. Set `auto`, `hybrid` or `wal` to use it. |
| `control plane network: internal=true while OVERCAST_VPC_EGRESS is not none` | The deprecated `OVERCAST_CONTROL_PLANE_INTERNAL=true` isolated one network while the mode says egress is open, so containers lose their route out anyway. Set `OVERCAST_VPC_EGRESS=none` if that is what you meant, or unset the deprecated variable. |
| `OVERCAST_VPC_EGRESS=none asked for an isolated control plane, but ...` | This host (Docker Desktop) cannot isolate the control plane without stranding every invocation at INIT, so it was left routable and the stack is not hermetic. Run Overcast in a container, or against a native Linux daemon. See [Egress modes](./networking.md#egress-modes). |
| `OVERCAST_VPC_EGRESS=routed asked for an isolated control plane, but ...`, or `... decides egress from each subnet's route table, but ...` | Same host limit, costing `routed` the other half of its job: every container has a route out whatever its route table says, so a subnet with no default route still reaches the internet. Run Overcast in a container, or against a native Linux daemon. See [`routed`](./networking.md#routed-egress-from-your-route-tables). |
| `OVERCAST_VPC_EGRESS_POOL ... has no free /24 left for another VPC's egress network` | Under `routed`, every VPC with egress takes one `/24` from the pool, and 256 of them fit in the `198.18.0.0/16` default. Delete VPCs that no longer need one, or set a wider `OVERCAST_VPC_EGRESS_POOL`. See [The address-pool ceiling](./networking.md#the-address-pool-ceiling). |
| `Docker network is not in the state this configuration asks for` | A network Overcast reuses differs from what this configuration would create, and has containers on it. `overcast network reset --dry-run`, then `overcast network reset` — see [Network state verification](./networking.md#network-state-verification). |

A port Overcast wants that is already taken is not on this list because it needs
no diagnosis: startup fails immediately with the OS's own
`bind: address already in use` rather than falling back silently.

## Runtime advisories

Some conditions only surface once Overcast is running. Those are raised as
advisories, on the web console's **Metrics & Health** page and in the
`advisories` array of `GET /_overcast/debug/metrics`:

| Advisory | Means |
| --- | --- |
| `Running in memory-only mode` | Nothing is mounted and no `OVERCAST_DATA_DIR` is set, so state will not survive a restart. Expected outside a persistent setup — see [Storage and persistence](./storage.md). |
| `Data directory filesystem is slow` | An fsync probe of the data dir took over 75 ms, the signature of a Docker Desktop bind mount. See [Performance § Data dir placement](./performance.md#data-dir-placement--avoid-host-bind-mounts-on-docker-desktop). |
| `Storage degraded to memory-only` | The SQLite file became unreadable; `hybrid` keeps serving from memory for the rest of the run rather than crashing. |
| `Memory mode is ignoring an existing database` | The runtime form of the startup warning above. |
| `Docker network is not in its configured state` | A network Overcast reuses differs from what this configuration would create — usually one made by an older version, or with a different `OVERCAST_VPC_EGRESS`. It has containers on it, so Docker cannot change it in place. Run `overcast network reset --dry-run`, then `overcast network reset` — see [Network state verification](./networking.md#network-state-verification). |

## A function in a VPC fails with `ENETUNREACH`

**Symptom.** A Lambda or ECS task with a `VpcConfig` cannot reach an external
API, a real AWS endpoint, or anything else outside Docker. Code that works
without a VPC — and works on LocalStack — fails here, usually as
`ENETUNREACH`, sometimes as a DNS failure because the resolver is unreachable too.

**Cause.** Almost always one of two things.

| | |
| --- | --- |
| `OVERCAST_VPC_EGRESS=none` | That is the mode working: no container Overcast starts reaches anything outside this machine. On a host where an internal control plane would sever the Lambda Runtime API — Docker Desktop, with Overcast running outside a container — that one network is left routable, a startup warning says so, and containers keep a route out. See [Egress modes](./networking.md#egress-modes) |
| `OVERCAST_VPC_EGRESS=routed` and the container is in a subnet with no `0.0.0.0/0` route | That is the mode working too — the missing NAT gateway, caught locally. `overcast logs` names the subnet and route table that decided it. Add a NAT gateway and a route to grant egress; containers placed afterwards get it, and running ones are moved onto it. On the hosts where `none` cannot isolate the control plane, `routed` cannot withhold either, and reports `routed-egress-not-enforced`. See [`routed`](./networking.md#routed-egress-from-your-route-tables) |
| A network drifted | A network Overcast reuses kept a setting from an older version or a different mode, because Docker never applies `--internal` to an existing network. Overcast repairs one with nothing attached and warns about one with containers on it |

A container with a `VpcConfig` joins exactly two networks — its VPC's network
and the control plane — so if both are `--internal`, Docker installs no default
route and it has no way out. That is what `none` does deliberately, and what
drift can do by accident.

**Check which.** The startup log and `GET /_overcast/health` both say what each
network ended up as, and why:

```
network isolation  network=overcast_control internal=true
                   reason="OVERCAST_VPC_EGRESS=none"
```

```sh
overcast network status
```

A network reported as `NOT in the configured state` is drift; one reported `ok`
with `internal=true` under `OVERCAST_VPC_EGRESS=none` is the mode.

**Fixes:**

| | |
| --- | --- |
| Restore egress | Unset `OVERCAST_VPC_EGRESS`, or set it to `open`, and restart. `open` is the default |
| The network kept an old setting | `overcast network reset --dry-run` to see what it would do, then `overcast network reset`. It stops Overcast's own containers, disconnects yours, and rebuilds the network to spec |
| You want a hermetic stack | Then `ENETUNREACH` is the correct answer. Keep `none` — and check the startup log, because on Docker Desktop `none` leaves the control plane routable and the stack is not hermetic |
| The function does not need the VPC locally | Drop the `VpcConfig` for the local stage — but note that under `none` even a non-VPC function has no egress, which is the point of the mode |

**Reaching real AWS from a local function** — a hybrid stack whose code calls a
real regional endpoint or a third-party API — works under the default `open`
mode with no extra configuration. Overcast injects `AWS_ENDPOINT_URL` into every
container it starts, so an SDK client picks Overcast up by default; construct
the one client that should talk to real AWS with an explicit endpoint (or none)
and real credentials, and leave the rest pointing at the emulator. There is a
worked example in [Lambda examples](./services/lambda/examples.md#reaching-real-aws-from-a-local-function).

One more advisory is served on demand rather than raised: `GET /_overcast/preflight/region`
answers whether resources of a given `?kind=` exist in some region other than the
caller's. An empty console list with `There are N in <region>` behind it is an
`AWS_REGION` mismatch, not missing data.
