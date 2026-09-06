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
| A `<Resource>Id.N` parameter | AND-ed with the filters — all as on AWS. Unlike a filter, an ID here has to resolve — see [Resource IDs](#resource-ids) |

```bash
aws ec2 describe-vpcs    --filters 'Name=tag:Name,Values=overcast-*'
aws ec2 describe-images  --filters 'Name=name,Values=Amazon Linux 2*'
aws ec2 describe-subnets --filters 'Name=availability-zone,Values=us-east-1?'
```

## Resource IDs

A `Describe*` handed an explicit `<Resource>Id.N` list resolves every ID in it
and fails on the first one it cannot, as AWS does. Naming a resource is an
assertion that it exists, and the answer comes back as the error rather than as
the length of the list:

```
$ aws ec2 describe-vpcs --vpc-ids vpc-00000000000000000
An error occurred (InvalidVpcID.NotFound): The vpc ID 'vpc-00000000000000000' does not exist

$ aws ec2 describe-vpcs --vpc-ids not-an-id
An error occurred (InvalidVpcID.Malformed): Invalid id: "not-an-id" (expecting "vpc-...")
```

A **filter** that matches nothing is the opposite case and still answers `200`
with an empty list — that is a question about which resources look a certain
way, and "none of them" is an answer.

An ID is eight or seventeen lowercase hex characters after the prefix, the two
forms EC2 issues; `DescribeAddresses`' `--public-ips` is the one selector that
is an address rather than an ID, and its shape is a dotted quad. Anything else
is `.Malformed` before the lookup happens, so a request naming both a malformed
ID and an unknown one reports the malformed one.

| Operation | Selector | Unknown ID | Malformed ID |
| --- | --- | --- | --- |
| `DescribeVpcs` | `VpcId.N` | `InvalidVpcID.NotFound` | `InvalidVpcID.Malformed` |
| `DescribeSubnets` | `SubnetId.N` | `InvalidSubnetID.NotFound` | `InvalidSubnetID.Malformed` |
| `DescribeSecurityGroups` | `GroupId.N` | `InvalidGroup.NotFound` | `InvalidGroupId.Malformed` |
| `DescribeRouteTables` | `RouteTableId.N` | `InvalidRouteTableID.NotFound` | `InvalidRouteTableId.Malformed` |
| `DescribeInternetGateways` | `InternetGatewayId.N` | `InvalidInternetGatewayID.NotFound` | `InvalidInternetGatewayId.Malformed` |
| `DescribeNetworkInterfaces` | `NetworkInterfaceId.N` | `InvalidNetworkInterfaceID.NotFound` | `InvalidNetworkInterfaceId.Malformed` |
| `DescribeInstances` | `InstanceId.N` | `InvalidInstanceID.NotFound` | `InvalidInstanceID.Malformed` |
| `DescribeAddresses` | `AllocationId.N` | `InvalidAllocationID.NotFound` | none — see below |
| `DescribeAddresses` | `PublicIp.N` | `InvalidAddress.NotFound` | `InvalidAddress.Malformed` |
| `DescribeNatGateways` | `NatGatewayId.N` | `NatGatewayNotFound` | `NatGatewayMalformed` |
| `DescribeVpnGateways` | `VpnGatewayId.N` | `InvalidVpnGatewayID.NotFound` | none — see below |
| `DescribeVpcEndpoints` | `VpcEndpointId.N` | `InvalidVpcEndpointId.NotFound` | `InvalidVpcEndpointId.Malformed` |
| `DescribeVpcPeeringConnections` | `VpcPeeringConnectionId.N` | `InvalidVpcPeeringConnectionID.NotFound` | `InvalidVpcPeeringConnectionId.Malformed` |

The casing is AWS's own and varies by resource — `InvalidVpcID.Malformed` beside
`InvalidGroupId.Malformed`, and `NatGatewayNotFound` with no prefix or dot at
all — so match the exact string rather than deriving it.

`DescribeAddresses` is the one operation with two selectors. They are AND-ed
with each other and with the filters, and each is resolved on its own: an
allocation ID that is not there is `InvalidAllocationID.NotFound` whichever
addresses `--public-ips` named.

**Two selectors have no Malformed code.** The EC2 API reference documents none
for an allocation ID or a virtual private gateway, so Overcast does not invent
one: a wrongly shaped ID there is reported as one the region does not hold.

```
$ aws ec2 describe-addresses --allocation-ids not-an-id
An error occurred (InvalidAllocationID.NotFound): The allocation ID 'not-an-id' does not exist
```

## Not-found codes outside a `Describe*`

An operation that names one resource — `DeleteVpc`, `DeleteSubnet`,
`DeleteSecurityGroup`, `StopInstances`, `ModifySubnetAttribute`,
`AuthorizeSecurityGroupIngress`, `ReleaseAddress` and the rest — answers with
the same per-resource code as the table above, from the same source. There is no
generic Overcast code for "that ID is not here":

```
$ aws ec2 delete-vpc --vpc-id vpc-00000000
An error occurred (InvalidVpcID.NotFound): The vpc ID 'vpc-00000000' does not exist

$ aws ec2 delete-security-group --group-id sg-00000000
An error occurred (InvalidGroup.NotFound): The security group 'sg-00000000' does not exist
```

Shape is not checked on this path — the resource either resolves or it does
not — so a malformed ID here is a `.NotFound` rather than a `.Malformed`.

## Related

- [EC2 / VPC](../ec2.md) — quick start and what works
- [EC2 / VPC operations](./operations.md) — per-operation status and supported filters
- [How a VPC is backed by a Docker network](../../networking/vpc-backing.md) — the Docker bridge, gateways and CIDR strategies
- [Egress modes](../../networking/egress.md) — what `open`, `routed` and `none` decide
- [Local VPCs for CDK](../../cdk/local-vpc.md)
- [Configuration reference](../../configuration.md)
