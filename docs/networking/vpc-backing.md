---
title: "How a VPC is backed by a Docker network"
description: "Each non-default VPC becomes a real Docker bridge: the labels it carries, what an internet gateway does to it, and what OVERCAST_EC2_VPC_STRATEGY does about overlapping CIDRs."
section: "Networking"
tags:
  - docker
  - docs
  - ec2
  - networking
  - vpc
---

# How a VPC is backed by a Docker network

Each non-default VPC is backed by a real Docker bridge network, which is what
makes containers in it reach each other. The VPC's CIDR becomes the Docker
subnet, and the network's `--internal` flag reflects whether an internet gateway
is attached.

The default VPC is the exception: it has no network of its own and *is* the
shared data plane — see
[The Docker networks Overcast uses](./docker-networks.md).

## Labels on a VPC network

Networks are named `{OVERCAST_NETWORK}-vpc-{vpcID}` (`overcast-vpc-{vpcID}` by
default). Docker network lifecycle events — create, destroy, connect,
disconnect — are forwarded through the event bus, so they appear on the
console's activity feed.

| Label | Value |
| --- | --- |
| `overcast.managed` | `true` |
| `overcast.service` | `ec2` |
| `overcast.resource-id` | The VPC ID |
| `overcast.vpc-id` | The VPC ID |
| `overcast.instance` | The Overcast instance that created it. An instance only ever removes networks carrying its own value, so two instances on one daemon leave each other's alone |
| `overcast.network.spec-hash` | The state the network was created in, checked on every start — see [Network state verification](./network-state.md) |
| `overcast.network.vpc-role` | `plane` for the VPC's own network, `egress` for the routable one beside it under `OVERCAST_VPC_EGRESS=routed`. Both carry the same VPC ID, so this is what tells them apart |

Whether a container on one of these networks reaches the internet is decided by
`OVERCAST_VPC_EGRESS`, not by the `--internal` flag on its own — see
[Egress modes](./egress.md) and [`routed`](./routed-egress.md).

## Internet gateways and isolation

Under `OVERCAST_VPC_EGRESS=open` — the default — a VPC's network is created
`--internal` and stays that way until an internet gateway is attached. Under
`none` every network Overcast creates is `--internal` whatever the template
says, and attaching a gateway changes nothing.

Docker fixes that flag when a network is created, so `AttachInternetGateway` and
`DetachInternetGateway` **recreate the network** to change it, under whatever is
already on it: every container is disconnected, the network is recreated with
the new flag, and each is reconnected at the address and DNS aliases it had.
That is the same rebuild-under-them repair
[network state verification](./network-state.md) describes, so the same thing
holds — the control-plane attachment is untouched and an in-flight invocation
keeps its Runtime API connection; only connections across the VPC bridge drop,
as on AWS when routing changes under a live ENI. Gateway changes on one network
are serialised, so two stacks attaching gateways to VPCs that share a network
take turns.

This matters because of the order every CloudFormation template produces:
`AWS::EC2::VPC` first, `AWS::EC2::VPCGatewayAttachment` later, often after a
function or task has already been placed in the VPC.

| Outcome | What you get |
| --- | --- |
| The flip cannot be completed | `InternalError` naming what Docker refused, and nothing recorded. `DescribeInternetGateways` never reports a gateway the network does not reflect, and the call can be retried |
| The network was recreated but a container could not rejoin | The gateway is recorded — the network is in the state asked for — and the container is reported through the advisory below |
| A mismatch found at startup | Reconcile checks every adopted network's flag against the gateway state and repairs it the same way |
| Reconcile cannot repair it | The `vpc-network-isolation-stale` advisory on the console's Metrics & Health page (and in `GET /_overcast/debug/metrics`), naming the VPC and Docker's reason, until a later flip succeeds or the VPC is deleted |

What fails a flip is the daemon itself — a container it will not disconnect, an
address pool it cannot allocate the subnet from, an API error — so the reason
quoted is the thing to look at.

A gateway change on the default VPC is recorded as metadata and the network is
left alone: it is the shared data plane, which already has the internet.

## Overlapping CIDRs

Most setups can skip this. It matters when you deliberately create VPCs with
overlapping CIDRs.

In AWS, two VPCs in one account may share a CIDR — `10.0.0.0/16` twice is legal,
and overlap only matters when you connect them. Every Docker bridge on a host
shares one kernel routing table, and Linux refuses two bridges claiming
overlapping subnets (`Pool overlaps with other one on this address space`).
AWS's model assumes per-VPC isolation; Docker's assumes host-global uniqueness.

`OVERCAST_EC2_VPC_STRATEGY` picks how Overcast behaves when the two disagree.

| Strategy | Status | On overlapping CIDRs |
| --- | --- | --- |
| `shared` *(default)* | ✅ Implemented | VPCs with the same CIDR share one Docker network. Isolation between sharers is not enforced |
| `strict` | ✅ Implemented | `CreateVpc` rejects an overlapping CIDR with `InvalidVpc.Range`. Startup still tolerates pre-existing overlaps: first one wins, losers are marked `conflict` |
| `remapped` | ✅ Implemented | A shadow `/16` is allocated from `100.64.0.0/10` when the requested CIDR collides. API responses still report the CIDR you asked for |
| `netns` | ❌ Not implemented | Per-VPC Linux network namespace — real overlap with real isolation. Needs root or `CAP_NET_ADMIN` |

`OVERCAST_EC2_VPC_STRATEGY=netns` fails startup with a configuration error
naming the strategies that do exist. It is the one value this variable refuses
outright; an unrecognised value — a typo — falls back to `shared` with a logged
warning.

Overlap is judged within a region. VPCs are stored per region, and every
strategy compares a CIDR against the VPCs of the region it is created in — so
the same CIDR in two regions, which is what CDK's default of `10.0.0.0/16` in
every stack produces, is not a conflict to `strict`, is not shared under
`shared`, and is not remapped under `remapped`. The host's routing table is
still one: Docker refuses the second region's bridge, and that VPC lands as
`unbacked`. Give each region its own CIDR when more than one region needs a
backed VPC.

### Choosing one

| Situation | Use |
| --- | --- |
| One VPC, or several with distinct CIDRs | `shared` — it never reaches the sharing path and costs nothing |
| CI that should fail loudly on an accidental CIDR collision | `strict` |
| CDK or Terraform apps with legitimate overlap that read API-visible IPs | `remapped` |
| Testing real container-level isolation between same-CIDR VPCs | Not available |

`shared` is the default because the overwhelmingly common workload is one VPC,
or a handful with distinct CIDRs, where it is indistinguishable from perfect
isolation. Where it does trigger, a container in `vpc-A` (10.0.0.0/16) can reach
one in `vpc-B` (10.0.0.0/16) because they are on the same bridge.

`remapped` has one cost worth knowing: containers that address each other by raw
private IP, hardcoded rather than resolved, will not reach the fabricated
address — only the shadow one is real. Anything using DNS, service discovery, or
an RDS/ELB endpoint name is unaffected.

### Edge behaviour under `shared`

| Event | What happens |
| --- | --- |
| `CreateVpc` with Docker unavailable | The VPC is stored as `unbacked` and reconcile picks it up later. If Docker is available and the create fails, the API call still succeeds and the network is best-effort |
| `DeleteVpc` | The Docker network is torn down only when the VPC being deleted was the last one using it |
| Internet gateway attach or detach | Flips the network for every sharer at once: a shared network is external while *any* VPC on it has a gateway attached, and goes back to `--internal` only when the last gateway is detached. Every sharer's record follows the recreated network |

Isolation between sharers is not enforced anyway, so the most permissive VPC
sets the mode — one VPC's detach cutting off another's internet is a surprise
nothing in the AWS model prepares a stack for.

## Inspecting network state

Every VPC carries a `NetworkStatus`, surfaced three ways: as an
`overcast:network-status` tag on `DescribeVpcs`, in the startup reconcile logs,
and on `/_overcast/debug/ec2/vpcs` alongside `DockerNetworkID` and
`DockerCidrBlock`.

| Value | Meaning |
| --- | --- |
| `ok` | This VPC owns its backing Docker network |
| `shared` | It reuses a network owned by another VPC |
| `unbacked` | No Docker network — Docker was unavailable, the last create failed, or a VPC in another region already holds the CIDR |
| `conflict` | `strict` mode: its CIDR collided with another VPC. Container-backed operations on it are refused with `InvalidVpc.NetworkStatus` |
| `remapped` | `remapped` mode: backed by a shadow CIDR |

## Related

- [The Docker networks Overcast uses](./docker-networks.md) — the planes a VPC network sits beside
- [Egress modes](./egress.md) — what decides whether a VPC's containers reach the internet
- [`routed`: egress from your route tables](./routed-egress.md) — the second network per VPC
- [Network state verification](./network-state.md) — what happens when a network is not as configured
- [Lambda, ECS and VPCs](./vpcs.md) — what VPC membership restricts
- [EC2 / VPC limitations](../services/ec2/limitations.md) — what the stored VPC metadata does and does not do
