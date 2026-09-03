---
title: "Networking and host-based addressing"
description: "One port, one process, and one question: what name does a caller use, and does that name resolve for them? The landing page for hostnames, VPC networks and egress."
section: "Networking"
tags:
  - docker
  - docs
  - guide
  - networking
  - vpc
---

# Networking and host-based addressing

Overcast listens on a single port (default `4566`) and dispatches every request
to the same process, whichever service it is for. Everything here follows from
one question: **what name does a caller use, and does that name resolve for
them?**

| If you are here about | Go to |
| --- | --- |
| An API Gateway, Lambda URL, AppSync or S3 hostname | [Host-routed addressing](./networking/host-routing.md) |
| A subdomain that will not resolve, on Windows or offline | [Hostnames that resolve for every caller](./networking/hostnames.md) |
| A URL that works from your shell but not from a container | [Hostnames that resolve for every caller](./networking/hostnames.md) |
| A port that differs depending on who asks | [What host and port a URL carries](./networking/urls.md) |
| An RDS or ElastiCache endpoint that will not connect | [Data-plane endpoints](./networking/data-plane-endpoints.md) |
| A Lambda that cannot reach a database | [Lambda, ECS and VPCs](./networking/vpcs.md) |
| Whether a container reaches the internet | [Egress modes](./networking/egress.md), [`routed`](./networking/routed-egress.md) |
| A Docker network that is not as you configured it | [Network state verification](./networking/network-state.md) |
| Something failing right now | [Networking troubleshooting](./networking/troubleshooting.md) |

Three settings decide almost all of it:

| Setting | Default | Decides |
| --- | --- | --- |
| `OVERCAST_HOSTNAME` | the host you called Overcast on | the name in every URL Overcast hands out. Set it to `localhost.overcast.sh` and one URL works from your shell and from inside a container |
| `OVERCAST_NETWORK` | `overcast` | the Docker network resources reach each other on |
| `OVERCAST_VPC_EGRESS` | `open` | whether the containers Overcast starts reach anything outside your machine |

## The pages

| Page | Answers |
| --- | --- |
| [Host-routed addressing](./networking/host-routing.md) | Which subdomain forms route where, and how a Host is matched to S3 or to a service |
| [Hostnames that resolve for every caller](./networking/hostnames.md) | What to set `OVERCAST_HOSTNAME` to, including in Docker Compose and offline |
| [What host and port a URL carries](./networking/urls.md) | Why a stack output shows one port on the host and another inside a function |
| [Data-plane endpoints](./networking/data-plane-endpoints.md) | RDS, Aurora and ElastiCache names, which point at a container rather than at Overcast |
| [The Docker networks Overcast uses](./networking/docker-networks.md) | The data plane, the control plane, the per-VPC bridges, and how to join them |
| [Egress modes](./networking/egress.md) | `open`, `none`, and what each one isolates |
| [`routed`: egress from your route tables](./networking/routed-egress.md) | Per-subnet egress, and the address pool it draws on |
| [Lambda, ECS and VPCs](./networking/vpcs.md) | What a `VpcConfig` takes away, and the three AWS fields that give it back |
| [Network state verification](./networking/network-state.md) | Why a reused Docker network is checked field by field, and what `overcast network reset` does |
| [Networking troubleshooting](./networking/troubleshooting.md) | Symptom, cause and fix |

## Related

- [Using AWS SDKs and CLI](./sdk-cli.md) — endpoint configuration for every SDK
- [Using AWS CDK](./cdk.md) — the S3 virtual-hosted-addressing and Windows DNS issue
- [Configuration](./configuration.md) — every environment variable
