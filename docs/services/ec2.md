---
title: "EC2 — Elastic Compute Cloud"
description: "Quick start, the VPC behaviour CDK and SDK workflows depend on, and where the emulation stops: overlapping-CIDR strategies, strict filter matching, and what stays metadata."
section: "Service Reference"
tags:
  - cloud
  - compute
  - docs
  - ec2
  - elastic
  - services
  - vpc
---

# EC2 — Elastic Compute Cloud

A VPC is a real Docker bridge network, so containers launched into one — Lambda,
ECS, RDS — actually reach each other. Instances, security group rules and route
tables are metadata.

**Status:** ✅ Supported

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
aws ec2 describe-vpcs --filters Name=isDefault,Values=true --query 'Vpcs[0].VpcId'

VPC=$(aws ec2 create-vpc --cidr-block 10.0.0.0/16 --query Vpc.VpcId --output text)
aws ec2 create-subnet --vpc-id "$VPC" --cidr-block 10.0.1.0/24 \
  --availability-zone us-east-1a --query Subnet.SubnetId

docker network ls --filter label=overcast.vpc-id="$VPC"
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

## What works

| Area | Behaviour |
| --- | --- |
| Default VPC | Seeded per region on first read: `172.31.0.0/16`, `IsDefault: true`, a subnet per AZ, an attached internet gateway, a main route table and a `default` security group |
| VPCs | Each non-default VPC gets a Docker bridge network whose subnet is the VPC's CIDR. `AttachInternetGateway` recreates that network to change its isolation, and fails rather than recording a gateway the network does not reflect |
| Lambda in a VPC | A function with a `VpcConfig` is attached to that VPC's network, alongside the control plane — so it can reach an RDS instance or an ECS task in the same VPC |
| CDK lookups | `Vpc.fromLookup`, subnet `tagSet`, NAT gateway routes in `DescribeRouteTables`, and `MapPublicIpOnLaunch` are all present, so subnet-group classification works |
| Instances | `RunInstances` records state with async `pending` → `running`, emitting `EC2 Instance State-change Notification` to the default EventBridge bus |
| Reconciliation | Stored VPCs are reconciled against actual Docker networks at startup: missing networks recreated, drifted IDs updated, networks left behind by VPCs that no longer exist removed |
| Dependencies | `DeleteVpc`, `DeleteSubnet` and `DeleteSecurityGroup` fail with `DependencyViolation` while something still references them, as on AWS |

Whether a VPC's containers reach the internet is decided by
[`OVERCAST_VPC_EGRESS`](../networking/egress.md), not by the gateway alone. The
startup pass reconciles every region; one it did not reach is reconciled on the
first placement into it, which adopts and recreates networks without repairing
their isolation until the next startup. A network another instance on the same
daemon created is left alone, and an unlabelled one is adopted and rebuilt to
spec but never removed — see
[How a VPC is backed by a Docker network](../networking/vpc-backing.md).

> [!NOTE]
> The default VPC's backing network is Overcast's own shared data plane
> (`OVERCAST_NETWORK`), where every container that named no VPC already sits.
> "No VPC" and "the default VPC" are the same place by construction.

## Differences from AWS

| Area                                                 | On AWS                                              | Overcast                                                                                                       |
| ---------------------------------------------------- | --------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Instances                                            | A VM boots from the AMI                             | Metadata only; no VM or container is launched, and no compute sits behind an instance ID                       |
| Security group rules                                 | Enforced on every packet                            | Stored and returned, never enforced — everything on a VPC's network can talk to everything else                |
| Subnets                                              | Each subnet is its own routed network               | Metadata only; a VPC is one flat bridge, with no per-subnet isolation or routing                               |
| Route tables, NAT gateways, VPN and transit gateways | Shape the network path                              | Metadata only; only an internet gateway changes the topology                                                   |
| Elastic IPs                                          | Routable public addresses                           | Allocated and associated, but synthetic and not routable                                                       |
| VPC peering                                          | An accepted peering routes traffic between the VPCs | The state machine runs; no cross-network routing is established                                                |
| NACLs, VPC Flow Logs, DHCP option sets               | Full API                                            | Not emulated; `DescribeDhcpOptions` returns a fabricated default                                               |
| `Describe*` filters                                  | Every documented filter name is honoured            | A filter name Overcast has not implemented is **refused**, not ignored                                         |
| Without Docker                                       | Not applicable                                      | Every networking feature degrades to metadata only: API responses stay correct, container connectivity is lost |

Overlapping CIDRs, the Docker-network model behind all of this, and the full
filter rules are in [EC2 limitations](./ec2/limitations.md); the Docker
network underneath a VPC is in
[How a VPC is backed by a Docker network](../networking/vpc-backing.md).

## Gotchas

A `Describe*` call refuses a filter name it does not implement, with AWS's
`InvalidParameterValue: The filter '<name>' is invalid`, naming every filter
that operation does support — see
[the filter rules](./ec2/limitations.md#filters).

> [!CAUTION]
> `DeleteVpc` on the default VPC removes the record and leaves the network
> alone, and attaching or detaching an internet gateway on it is ignored with a
> warning. Both would otherwise recreate or destroy the network every container
> Overcast started is attached to.

<!-- BEGIN overcast:capabilities -->

## Operations

All 72 listed operations are implemented.
Per-operation status, notes and AWS API links: [EC2 / VPC operations](ec2/operations.md).

<!-- END overcast:capabilities -->

## Related

- [EC2 limitations](./ec2/limitations.md) — what the stored metadata does not do, and the filter rules
- [How a VPC is backed by a Docker network](../networking/vpc-backing.md) — the Docker bridge, gateways and CIDR strategies
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [Local VPCs for CDK](../cdk/local-vpc.md) — the VPC-per-stack pattern that works locally
- [AWS API reference](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/Welcome.html)
