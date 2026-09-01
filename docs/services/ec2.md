---
title: "EC2 — Elastic Compute Cloud"
description: "VPCs are real Docker networks, so containers launched into one can reach each other. Instances, security group rules and route tables are metadata."
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

**Status:** ⚠️ Partial

## Quick start

```sh
export AWS_ENDPOINT_URL=http://localhost:4566
aws ec2 describe-vpcs --filters Name=isDefault,Values=true --query 'Vpcs[0].VpcId'

VPC=$(aws ec2 create-vpc --cidr-block 10.0.0.0/16 --query Vpc.VpcId --output text)
aws ec2 create-subnet --vpc-id "$VPC" --cidr-block 10.0.1.0/24 \
  --availability-zone us-east-1a --query Subnet.SubnetId

docker network ls --filter label=overcast.vpc-id="$VPC"
```

## What works

| Area | Behaviour |
| --- | --- |
| Default VPC | Seeded per region on first read: `172.31.0.0/16`, `IsDefault: true`, a subnet per AZ, an attached internet gateway, a main route table and a `default` security group |
| VPCs | Each non-default VPC gets a Docker bridge network whose subnet is the VPC's CIDR. Attaching an internet gateway takes the network out of `--internal` mode |
| Lambda in a VPC | A function with a `VpcConfig` is attached to that VPC's network, alongside the control plane — so it can reach an RDS instance or an ECS task in the same VPC |
| CDK lookups | `Vpc.fromLookup`, subnet `tagSet`, NAT gateway routes in `DescribeRouteTables`, and `MapPublicIpOnLaunch` are all present, so subnet-group classification works |
| Instances | `RunInstances` records state with async `pending` → `running`, emitting `EC2 Instance State-change Notification` to the default EventBridge bus |
| Reconciliation | On startup, stored VPCs are reconciled against actual Docker networks: missing ones recreated, drifted IDs updated, orphans removed |
| Dependencies | `DeleteVpc`, `DeleteSubnet` and `DeleteSecurityGroup` fail with `DependencyViolation` while something still references them, as on AWS |

> [!NOTE]
> The default VPC's backing network is Overcast's own shared data plane
> (`OVERCAST_NETWORK`), where every container that named no VPC already sits.
> "No VPC" and "the default VPC" are the same place by construction.

## Differences from AWS

| Area | Overcast |
| --- | --- |
| Instances | Metadata only. No VM or container is launched; there is no compute behind an instance ID |
| Security group rules | Stored and returned, never enforced. Everything on a VPC's network can talk to everything else |
| Subnets | Recorded as metadata. A VPC is one flat bridge — there is no per-subnet isolation or inter-subnet routing |
| Route tables, NAT gateways, VPN and transit gateways | Metadata only. Only an internet gateway changes the network topology |
| Elastic IPs | Allocated and associated, but the addresses are synthetic and not routable |
| VPC peering | The state machine runs; no cross-network routing is established |
| NACLs, VPC Flow Logs, DHCP option sets | Not emulated. `DescribeDhcpOptions` returns a fabricated default |
| `Describe*` filters | A filter name Overcast has not implemented is **refused**, not ignored |
| Without Docker | Every networking feature degrades to metadata-only. API responses stay correct; container connectivity is lost |

Overlapping CIDRs, the Docker-network model behind all of this, and the full
filter rules are in [EC2 limitations](ec2/limitations.md).

## Gotchas

> [!WARNING]
> A `Describe*` call refuses a filter name it does not implement, with AWS's
> `InvalidParameterValue: The filter '<name>' is invalid`. That is stricter than
> AWS, deliberately: a filter accepted and then ignored makes `describe-vpcs
> --filters Name=tag:Name,…` return every VPC in the region, which reads as
> "your VPC exists" to a find-or-create script. The error names every filter
> that operation does support.

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

- [EC2 limitations](ec2/limitations.md) — the networking model, CIDR strategies and filter rules
- [Local VPCs for CDK](../cdk/local-vpc.md) — the VPC-per-stack pattern that works locally
- [AWS API reference](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/Welcome.html)
- [All service pages](README.md)
