---
title: "EC2 / VPC limitations"
description: "What EC2 stores and returns without acting on it — instances, security groups, subnets, route tables and the rest — and the exact filter rules every Describe* operation applies."
section: "Service Reference"
tags:
  - docs
  - ec2
  - limitations
  - services
  - vpc
---

# EC2 / VPC limitations

What the EC2 emulation records without acting on it, and how `Describe*` filters
behave, behind [EC2 / VPC](../ec2.md). The Docker mechanics underneath a VPC —
the backing network, internet gateways and overlapping CIDRs — are in
[How a VPC is backed by a Docker network](../../networking/vpc-backing.md).

## What the metadata does not do

| Resource | Stored and returned | Not done |
| --- | --- | --- |
| Instances | IDs, state transitions, type, subnet and security group placement, tags | No VM or container is launched |
| Security group rules | Ingress and egress `IpPermissions` | No packet is filtered. Containers on one network reach each other regardless |
| Subnets | CIDR, AZ, `MapPublicIpOnLaunch`, tags | No address-space partitioning, no inter-subnet routing |
| Route tables | Routes, associations, the main-table flag | Under `open` and `none`, `CreateRoute`, `AssociateRouteTable` and `DisassociateRouteTable` change no packet's path. Under `routed` the `0.0.0.0/0` route decides whether the subnet's containers reach the internet, and nothing else about a route is applied |
| NAT gateways | State, subnet, addresses | No address translation of its own. Under `routed` its existence and state decide whether a route to it is a route out or a blackhole |
| VPN gateways, transit gateways | State and associations | No tunnel. A `0.0.0.0/0` route to one is not egress under `routed` either |
| Elastic IPs | Allocation, association, release | The addresses are synthetic. Containers get Docker's own IPs |
| VPC peering | `pending-acceptance` → `active` → `deleted` | No cross-network routing. Peered VPCs cannot communicate |
| VPC endpoints | Gateway and interface types, always `available` | No private-link path |

Not emulated at all: network ACLs, VPC flow logs, and DHCP option sets beyond a
fabricated default response.

Lambda is the one place a VPC has a real data plane: a function with a
`VpcConfig` is attached to that VPC's Docker network. Subnet-level and
security-group-level isolation is still not enforced, so a Lambda "in" one
subnet reaches everything on the VPC's network.

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

| Part | Rule |
| --- | --- |
| Filter name | Matched exactly, as AWS matches it: `Name=VPC-ID` is refused, because real EC2 refuses it too |
| Filter value | A pattern, as on AWS: `*` stands for any run of characters including none, `?` for exactly one, and a backslash escapes either |
| Two or more filters | AND-ed with each other |
| Two or more values in one filter | OR-ed |
| A `<Resource>Id.N` parameter | AND-ed with the filters — all as on AWS |

```bash
aws ec2 describe-vpcs    --filters 'Name=tag:Name,Values=overcast-*'
aws ec2 describe-images  --filters 'Name=name,Values=Amazon Linux 2*'
aws ec2 describe-subnets --filters 'Name=availability-zone,Values=us-east-1?'
```

## Related

- [EC2 / VPC](../ec2.md) — quick start and what works
- [EC2 / VPC operations](./operations.md) — per-operation status and supported filters
- [How a VPC is backed by a Docker network](../../networking/vpc-backing.md) — the Docker bridge, gateways and CIDR strategies
- [Local VPCs for CDK](../../cdk/local-vpc.md)
- [Configuration reference](../../configuration.md)
