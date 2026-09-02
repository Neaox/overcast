---
title: "EC2 / VPC limitations"
description: "How Overcast maps VPCs onto Docker networks, what the OVERCAST_EC2_VPC_STRATEGY values do about overlapping CIDRs, and the exact filter rules Describe* operations apply."
section: "Service Reference"
tags:
  - docs
  - ec2
  - limitations
  - services
  - vpc
---

# EC2 / VPC limitations

How the VPC emulation actually works, and where it stops. The working set is on
[EC2 / VPC](../ec2.md).

## The Docker backing

Each non-default VPC is backed by a real Docker bridge network. The VPC's CIDR
becomes the Docker subnet, and the network's isolation mode (`--internal`)
reflects whether an internet gateway is attached — see
[Internet gateways and isolation](#internet-gateways-and-isolation) for how
that is kept true.

That isolation only bites when Overcast's control plane is internal too, since
every container sits on both. `OVERCAST_CONTROL_PLANE_INTERNAL` decides it, and
by default it is decided from the host — so whether a gateway-less VPC actually
withholds the internet can differ between two machines running the same
Overcast. See
[Networking § Control-plane isolation](../../networking.md#control-plane-isolation).

| Label | Value |
| --- | --- |
| `overcast.managed` | `true` |
| `overcast.service` | `ec2` |
| `overcast.resource-id` | The VPC ID |
| `overcast.vpc-id` | The VPC ID |

Networks are named `overcast-vpc-{vpcID}`. Docker network lifecycle events —
create, destroy, connect, disconnect — are forwarded through the event bus, so
they appear on the console's activity feed.

The default VPC is the exception: its backing network is the shared data plane
(`OVERCAST_NETWORK`), which is where every container that named no VPC already
sits.

## Internet gateways and isolation

A VPC's network is created `--internal` and stays that way until an internet
gateway is attached. Docker fixes that flag when a network is created, so
`AttachInternetGateway` and `DetachInternetGateway` recreate the network to
change it — and they do so under whatever is already on it. Every container on
the network (a Lambda function, an ECS task, an RDS instance, and one you
attached by hand with `docker run --network overcast-vpc-…`) is disconnected,
the network is recreated with the new flag, and each is reconnected with the
address and DNS aliases it had. Their control-plane attachment is untouched, so
an in-flight invocation keeps its Runtime API connection; only connections
across the VPC bridge itself are dropped, as on AWS when routing changes under
a live ENI. Gateway changes on one network are serialised, so two stacks
attaching gateways to VPCs that share a network take turns.

This matters because of the order every CloudFormation template produces:
`AWS::EC2::VPC` first, `AWS::EC2::VPCGatewayAttachment` later, often after a
function or task has already been placed in the VPC.

A flip that cannot be completed fails the call with `InternalError`, naming
what Docker refused, and records nothing: `DescribeInternetGateways` never
reports a gateway the network does not reflect, and the same call can be
retried. What still fails is the daemon itself — a container it will not
disconnect, an address pool it cannot allocate the subnet from, an API error —
so the reason quoted is the thing to look at. One partial case is treated as
done: if the network was recreated but a container could not rejoin it, the
gateway is recorded (the network is in the state asked for) and the container
is reported through the advisory below.

On startup, reconcile checks every adopted network's flag against the gateway
state and repairs a mismatch the same way. One it cannot repair — or a
container that could not rejoin — is reported as the
`vpc-network-isolation-stale` advisory on the console's Metrics & Health page
(and in `GET /_overcast/debug/metrics`), naming the VPC and Docker's reason,
until a later flip succeeds or the VPC is deleted.

The default VPC is the exception: its network is the shared data plane, which
already has the internet, so a gateway change on it is recorded as metadata
and the network is left alone.

## What the metadata does not do

| Resource | Stored and returned | Not done |
| --- | --- | --- |
| Instances | IDs, state transitions, type, subnet and security group placement, tags | No VM or container is launched |
| Security group rules | Ingress and egress `IpPermissions` | No packet is filtered. Containers on one network reach each other regardless |
| Subnets | CIDR, AZ, `MapPublicIpOnLaunch`, tags | No address-space partitioning, no inter-subnet routing |
| Route tables | Routes, associations, the main-table flag | `CreateRoute`, `AssociateRouteTable` and `DisassociateRouteTable` change no packet's path |
| NAT gateways, VPN gateways, transit gateways | State and associations | No NAT, no tunnel |
| Elastic IPs | Allocation, association, release | The addresses are synthetic. Containers get Docker's own IPs |
| VPC peering | `pending-acceptance` → `active` → `deleted` | No cross-network routing. Peered VPCs cannot communicate |
| VPC endpoints | Gateway and interface types, always `available` | No private-link path |

Not emulated at all: network ACLs, VPC flow logs, and DHCP option sets beyond a
fabricated default response.

Lambda is the one place a VPC has a real data plane: a function with a
`VpcConfig` is attached to that VPC's Docker network. Subnet-level and
security-group-level isolation is still not enforced, so a Lambda "in" one
subnet reaches everything on the VPC's network.

## VPC networking strategies

Most setups can skip this section. It matters when you deliberately create VPCs
with overlapping CIDRs.

In AWS, two VPCs in one account may share a CIDR — `10.0.0.0/16` twice is legal,
and overlap only matters when you connect them. Overcast backs each VPC with a
Docker bridge so real containers can talk, and every bridge on a host shares one
kernel routing table: Linux refuses two bridges claiming overlapping subnets
(`Pool overlaps with other one on this address space`). AWS's model assumes
per-VPC isolation; Docker's assumes host-global uniqueness.

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

### Choosing one

| Situation | Use |
| --- | --- |
| One VPC, or several with distinct CIDRs | `shared` — it never reaches the sharing path and costs nothing |
| CI that should fail loudly on an accidental CIDR collision | `strict` |
| CDK or Terraform apps with legitimate overlap that read API-visible IPs | `remapped` |
| Testing real container-level isolation between same-CIDR VPCs | Not available |

`shared` is the default because the overwhelmingly common workload is one VPC,
or a handful with distinct CIDRs — in which case it is indistinguishable from
perfect isolation. Where it does trigger, a container in `vpc-A` (10.0.0.0/16)
can reach one in `vpc-B` (10.0.0.0/16) because they are on the same bridge.

`remapped` has one cost worth knowing: containers that address each other by
raw private IP, hardcoded rather than resolved, will not reach the fabricated
address — only the shadow one is real. Anything using DNS, service discovery,
or an RDS/ELB endpoint name is unaffected.

### Edge behaviour under `shared`

- **`CreateVpc` with Docker unavailable** stores the VPC as `unbacked`;
  reconcile picks it up later. If Docker is available and the create fails, the
  API call still succeeds and the network is best-effort.
- **`DeleteVpc`** tears the Docker network down only when the VPC being deleted
  was the last one using it.
- **Internet gateway attach or detach** flips the network for every sharer at
  once: a shared network is external while *any* VPC on it has a gateway
  attached, and goes back to `--internal` only when the last gateway is
  detached. Isolation between sharers is not enforced anyway, so the most
  permissive one sets the mode — the alternative, one VPC's detach cutting off
  another's internet, is a surprise nothing in the AWS model prepares a stack
  for. Every sharer's record follows the recreated network.

### Inspecting network state

Every VPC carries a `NetworkStatus`, surfaced three ways: as an
`overcast:network-status` tag on `DescribeVpcs`, in the startup reconcile logs,
and on `/_overcast/debug/ec2/vpcs` alongside `DockerNetworkID` and
`DockerCidrBlock`.

| Value | Meaning |
| --- | --- |
| `ok` | This VPC owns its backing Docker network |
| `shared` | It reuses a network owned by another VPC |
| `unbacked` | No Docker network — Docker was unavailable, or the last create failed |
| `conflict` | `strict` mode: its CIDR collided with another VPC. Container-backed operations on it are refused with `InvalidVpc.NetworkStatus` |
| `remapped` | `remapped` mode: backed by a shadow CIDR |

## Filters

Every `Describe*` refuses a filter name it does not implement, with AWS's
`InvalidParameterValue: The filter '<name>' is invalid`. The error names every
filter that operation does support, and the same sets are in the Notes column of
the [operations table](./operations.md).

That is stricter than AWS in one direction: AWS refuses a name it does not
model, and Overcast additionally refuses a name AWS models but Overcast has not
implemented. A filter accepted and then ignored answers a question the emulator
could not answer — a `describe-vpcs` filtered on `tag:Name` that returns every
VPC in the region reads as "your VPC exists" to a find-or-create script, which
then adopts the wrong one. If you hit one, drop the filter or narrow the call by
resource ID.

A filter **name** is matched exactly, as AWS matches it: `Name=VPC-ID` is
refused, because real EC2 refuses it too.

A filter **value** is a pattern, as on AWS: `*` stands for any run of characters
including none, `?` for exactly one, and a backslash escapes either.

```bash
aws ec2 describe-vpcs    --filters 'Name=tag:Name,Values=overcast-*'
aws ec2 describe-images  --filters 'Name=name,Values=Amazon Linux 2*'
aws ec2 describe-subnets --filters 'Name=availability-zone,Values=us-east-1?'
```

Filters are AND-ed with each other, values within one are OR-ed, and a
`<Resource>Id.N` parameter is AND-ed with them — all as on AWS.

## Related

- [EC2 / VPC](../ec2.md) — quick start and what works
- [EC2 / VPC operations](./operations.md) — per-operation status and supported filters
- [Local VPCs for CDK](../../cdk/local-vpc.md)
- [Configuration reference](../../configuration.md)
