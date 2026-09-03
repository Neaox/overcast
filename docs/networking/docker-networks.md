---
title: "The Docker networks Overcast uses"
description: "The data plane, the control plane and the per-VPC networks: what each one carries, which one a resource joins, and how to attach your own containers to them."
section: "Networking"
tags:
  - docker
  - docs
  - networking
  - vpc
---

# The Docker networks Overcast uses

Back to [Networking](../networking.md).

Everything Overcast starts as a container sits on two Docker networks, and you
normally never have to think about either. **To attach your own container** — a
Compose service that needs to reach a database Overcast started — join
`overcast`.

| Network | What it carries |
| --- | --- |
| `overcast` (`OVERCAST_NETWORK`) | The **data plane**: where resources reach each other. A Lambda function resolving an RDS endpoint, an ECS task reaching a cache node |
| `overcast_control` | Overcast's own channel to the containers it starts — the Lambda Runtime API, and the `AWS_ENDPOINT_URL` calls your function and task code make back into the emulator. Derived from `OVERCAST_NETWORK`; not separately configurable |
| `overcast-vpc-<vpc-id>` | One VPC's **plane** — where the resources in that VPC reach each other |
| `overcast-vpc-<vpc-id>-egress` | Only under `OVERCAST_VPC_EGRESS=routed`: that VPC's **route out**, joined by the containers whose subnet's route table has a `0.0.0.0/0` route. Created on the first placement that earns one — see [`routed`](./routed-egress.md) |

A resource created in a VPC joins that VPC's network (named from
`OVERCAST_NETWORK`, like the others) **instead of** the shared one, so only
things in the same VPC reach it by name — see
[Lambda, ECS and VPCs](./vpcs.md) for what that costs and how to opt out.

Two exceptions put a resource on the shared plane as well:

- **The default VPC has no network of its own.** It *is* the shared plane, so
  everything in it sits where everything with no VPC already is. Attaching an
  internet gateway to it changes nothing, and says so in the log.
- **On a native Windows or macOS host the restriction is not applied at all.** It
  is only safe where a forbidden connection fails by name, which needs Overcast's
  DNS resolver, which needs an `/etc/resolv.conf` those hosts do not have. A
  VPC-attached container there joins its VPC network *and* the shared plane, and
  `docker inspect` shows it on three networks. Run Overcast in a container to get
  the restriction.

## Related

- [Egress modes](./egress.md) — whether these networks reach the internet
- [Network state verification](./network-state.md) — what happens when one is not as configured
- [Lambda, ECS and VPCs](./vpcs.md) — what VPC membership restricts
