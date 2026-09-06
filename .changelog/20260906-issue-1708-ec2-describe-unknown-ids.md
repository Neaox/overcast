*! [ec2] `Describe*` raises `Invalid<Resource>ID.NotFound` or `.Malformed` for an explicit resource ID it cannot resolve, instead of an empty 200.
  covers DescribeVpcs, DescribeSubnets, DescribeSecurityGroups, DescribeRouteTables, DescribeInternetGateways, DescribeNetworkInterfaces and DescribeInstances.
  a filter matching nothing is unchanged: still 200 with an empty list. AWS's per-resource casing is exact, so `InvalidVpcID.Malformed` sits beside `InvalidGroupId.Malformed`.
  `ReleaseAddress` on an allocation that does not exist answers `InvalidAllocationID.NotFound`, the code real EC2 sends, rather than `InvalidAddressID.NotFound`.
  migration: code that named an ID and read the empty list as "it is gone" must catch the error instead, which is what it already has to do against AWS.
