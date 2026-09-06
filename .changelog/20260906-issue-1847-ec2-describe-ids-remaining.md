*! [ec2] the last five `Describe*` operations resolve an explicit resource ID, and a store miss carries AWS's per-resource code.
  covers DescribeAddresses (`AllocationId.N` and `PublicIp.N`, previously ignored), DescribeNatGateways, DescribeVpnGateways, DescribeVpcEndpoints and DescribeVpcPeeringConnections.
  AWS documents no Malformed code for an allocation ID or a virtual private gateway, so a wrongly shaped one there is `.NotFound` rather than an invented `.Malformed`.
  `DeleteVpc`, `DeleteSecurityGroup`, `StopInstances` and every other single-ID lookup answer `InvalidVpcID.NotFound`, `InvalidGroup.NotFound` and their siblings, not `InvalidId.NotFound`.
  migration: an explicit unknown ID that answered 200 with an empty list now answers 400, and code matching `InvalidId.NotFound` must match AWS's per-resource code instead.
