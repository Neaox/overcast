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

| Page | Answers |
| --- | --- |
| [Host-routed addressing](./networking/host-routing.md) | Which subdomain forms route where: API Gateway, Lambda URL, AppSync and S3 hostnames |
| [Hostnames that resolve for every caller](./networking/hostnames.md) | A name that will not resolve, or resolves from your shell but not from a container |
| [What host and port a URL carries](./networking/urls.md) | A port that differs depending on who asks |
| [Data-plane endpoints](./networking/data-plane-endpoints.md) | An RDS, Aurora or ElastiCache endpoint that will not connect |
| [The Docker networks Overcast uses](./networking/docker-networks.md) | The data plane, the control plane, the per-VPC bridges, and how to join them |
| [Egress modes](./networking/egress.md) | Whether a container reaches the internet: `open`, `none`, and what each isolates |
| [`routed`: egress from your route tables](./networking/routed-egress.md) | Per-subnet egress, and the address pool it draws on |
| [Lambda, ECS and VPCs](./networking/vpcs.md) | A Lambda that cannot reach a database, and what a `VpcConfig` takes away |
| [Network state verification](./networking/network-state.md) | A Docker network that is not as you configured it, and what `overcast network reset` does |
| [Networking troubleshooting](./networking/troubleshooting.md) | Something failing right now: symptom, cause and fix |

Three settings decide almost all of it:

| Setting | Default | Decides |
| --- | --- | --- |
| `OVERCAST_HOSTNAME` | the host you called Overcast on | the name in every URL Overcast hands out. Set it to `localhost.overcast.sh` and one URL works from your shell and from inside a container |
| `OVERCAST_NETWORK` | `overcast` | the Docker network resources reach each other on |
| `OVERCAST_VPC_EGRESS` | `open` | whether the containers Overcast starts reach anything outside your machine |

## Related

- [Using AWS SDKs and CLI](./sdk-cli.md) — endpoint configuration for every SDK
- [Using AWS CDK](./cdk.md) — the S3 virtual-hosted-addressing and Windows DNS issue
- [Configuration](./configuration.md) — every environment variable
