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
| `control plane network: internal=true — ... NO egress at all` | The control plane was created `--internal`. A container in a VPC whose own network is also internal then has no route out at all. See [A function in a VPC fails with ENETUNREACH](#a-function-in-a-vpc-fails-with-enetunreach). |

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

## A function in a VPC fails with `ENETUNREACH`

**Symptom.** A Lambda or ECS task with a `VpcConfig` cannot reach an external
API, a real AWS endpoint, or anything else outside Docker. Code that works
without a VPC — and works on LocalStack — fails here, usually as
`ENETUNREACH`, sometimes as a DNS failure because the resolver is unreachable too.

**Cause.** A container with a `VpcConfig` joins exactly two networks: its VPC's
network and the control plane. If both are `--internal`, Docker installs no
default route and the container has no way out.

Two things put it there, and they compound:

| | |
| --- | --- |
| The VPC's own network is `--internal` unless an internet gateway is attached | Route tables, `0.0.0.0/0` routes and NAT gateways are metadata only — nothing reads them. A private-with-egress subnet behind a NAT gateway is not distinguished from an isolated one. See [EC2 limitations](./services/ec2/limitations.md) |
| The control plane is `--internal` when `OVERCAST_CONTROL_PLANE_INTERNAL` resolves to true | Under the default `auto` that depends on the host — see [Control-plane isolation](./networking.md#control-plane-isolation) |

**Check which applies.** The startup log and `GET /_overcast/health` both say
what the control plane ended up as, and why:

```
control plane network isolation  network=overcast_control internal=true
                                 reason="auto: Overcast is containerised"
```

```sh
docker network inspect overcast_control --format '{{.Internal}}'
docker network inspect overcast-vpc-<vpcId> --format '{{.Internal}}'
```

**Fixes**, in the order worth trying:

| | |
| --- | --- |
| Restore egress | Set `OVERCAST_CONTROL_PLANE_INTERNAL=false` and restart. The control plane then carries egress for VPC-attached containers |
| The network kept an old setting | Docker never changes `--internal` on an existing network. Overcast recreates one that disagrees when nothing is attached, and warns when containers are. Stop what is attached, `docker network rm overcast_control`, restart |
| You are deliberately testing a gateway-less VPC | Then `ENETUNREACH` is the correct answer and matches AWS. Keep the isolation |
| The function does not need the VPC locally | Drop the `VpcConfig` for the local stage. It then joins the default data plane, which is not internal |

**Reaching real AWS from a local function** — a hybrid stack whose code calls a
real regional endpoint or a third-party API — needs the same egress
prerequisite: the control plane must not be internal, or the function must not
be VPC-attached. Overcast injects `AWS_ENDPOINT_URL` into every container it
starts, so an SDK client picks Overcast up by default; construct the one client
that should talk to real AWS with an explicit endpoint (or none) and real
credentials, and leave the rest pointing at the emulator.

One more advisory is served on demand rather than raised: `GET /_overcast/preflight/region`
answers whether resources of a given `?kind=` exist in some region other than the
caller's. An empty console list with `There are N in <region>` behind it is an
`AWS_REGION` mismatch, not missing data.
