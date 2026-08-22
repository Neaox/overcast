package ec2

// typed_dispatch.go — which EC2 operations the typed registry answers.
//
// EC2 registers 69 typed operations and, until #754, dispatched to none of
// them: Service.DispatchQuery went straight to the legacy handler map. That was
// the right call at the time and the reason is worth keeping. Resolving the
// Query-protocol Action made the registry reachable for the first time
// (docs/plans/level2-codegen.md Track 1.1), the integration suite was run
// against it, and the typed twins turned out to ignore filters across the
// Describe* operations and to drop mutations outright — a service-wide spread,
// so the whole branch was switched off rather than patched op by op.
//
// It comes back as an allow-list rather than a denylist, and the direction is
// the point. A denylist re-enables everything not yet found broken; the
// evidence from the first attempt is that "not yet found broken" is not the
// same as "checked". An allow-list re-enables exactly what has been checked,
// and an operation earns its line here by having its legacy body and its typed
// body be the *same* body — see describe.go — with a row in the differential
// parity table (typed_parity_dev_test.go) proving the two dispatch paths answer
// one request identically, byte for byte.
//
// Everything absent stays on legacy, unchanged, and is listed in
// ec2TypedDispatchRemainder below with what it needs first. #754 stays open
// until that map is empty.

// ec2TypedOps names the operations Service.DispatchQuery answers from
// h.typedOp. Each shares one body with its legacy counterpart, so routing here
// changes which decoder read the request and nothing about the answer.
var ec2TypedOps = map[string]bool{
	// Describe* operations, all of which now decode `<Resource>Id.N` and
	// `Filter.N.Name`/`Filter.N.Value.M` into the same describeQuery the
	// legacy handler builds off the form, and hand it to the same function.
	// Before this, every one of them answered with the whole region however
	// the caller filtered.
	"DescribeAccountAttributes":     true,
	"DescribeAddresses":             true,
	"DescribeAvailabilityZones":     true,
	"DescribeDhcpOptions":           true,
	"DescribeImages":                true,
	"DescribeInstanceTypes":         true,
	"DescribeInstances":             true,
	"DescribeInternetGateways":      true,
	"DescribeKeyPairs":              true,
	"DescribeNatGateways":           true,
	"DescribeNetworkInterfaces":     true,
	"DescribeRegions":               true,
	"DescribeRouteTables":           true,
	"DescribeSecurityGroups":        true,
	"DescribeSubnets":               true,
	"DescribeTags":                  true,
	"DescribeVpcAttribute":          true,
	"DescribeVpcEndpoints":          true,
	"DescribeVpcPeeringConnections": true,
	"DescribeVpcs":                  true,
	"DescribeVpnGateways":           true,
}

// ec2TypedDispatchRemainder is every registered typed operation still answered
// by legacy, and what its typed body would have to gain first.
//
// It is a work ledger, not an exemption list: each entry names a real defect in
// the typed implementation, and an operation leaves the map by being fixed and
// moved to ec2TypedOps, never by being excused. TestTypedDispatch_everyOpIsRouted
// fails if an operation is in neither map, so a newly registered typed
// operation cannot arrive unclassified — which is how 69 of them came to be
// unreachable without anything failing.
var ec2TypedDispatchRemainder = map[string]string{
	// Mutations whose typed body validates its parameters and returns success
	// without touching the store. Routing any of these would silently drop the
	// write.
	"TerminateInstances": "typed body is a stub: ignores InstanceId.N, changes no state, returns an empty instancesSet",
	"StartInstances":     "typed body is a stub: ignores InstanceId.N and changes no state",
	"StopInstances":      "typed body is a stub: ignores InstanceId.N and changes no state",
	"DeleteTags":         "typed body is a stub: ResourceId.N / Tag.N.* are never applied and nothing is deleted",
	"DeleteVpcEndpoints": "typed body is a stub: VpcEndpointId.N is never read and nothing is deleted",
	"ModifyInstanceAttribute": "typed body fetches the instance and discards it (`_ = inst`); " +
		"InstanceType.Value is never applied or persisted",
	"AuthorizeSecurityGroupIngress": "typed body checks GroupId and returns true; IpPermissions.N.* is never appended to the group",
	"AuthorizeSecurityGroupEgress":  "typed body checks GroupId and returns true; IpPermissions.N.* is never appended to the group",
	"RevokeSecurityGroupIngress":    "typed body checks GroupId and returns true; no rule is removed and InvalidPermission.NotFound is never raised",
	"RevokeSecurityGroupEgress":     "typed body checks GroupId and returns true; no rule is removed",
	"ModifySubnetAttribute":         "typed body does not persist MapPublicIpOnLaunch.Value, which #1144 made a stored field",
	"ModifyVpcAttribute":            "typed body does not persist EnableDnsSupport.Value / EnableDnsHostnames.Value, which #1144 made stored fields",

	// Creates and deletes whose typed body drops part of what legacy stores or
	// renders. Smaller gaps than the stubs above, but each is a wrong answer.
	"RunInstances": "runInstancesReq has no SecurityGroupId.N or TagSpecification.N, so the instance is stored with " +
		"neither and the response omits groupSet/tagSet; the lifecycle event is not published either",
	"CreateVpc":                  "typed body omits EnableDnsSupport=true, which is real AWS's CreateVpc default and a stored field since #1144",
	"CreateSubnet":               "typed body does not apply TagSpecification.N, and skips the subnet-created lifecycle event",
	"CreateSecurityGroup":        "typed body does not apply TagSpecification.N, and skips the security-group-created lifecycle event",
	"CreateTags":                 "typed body is correct on the wire but skips the tag lifecycle event legacy publishes",
	"DeleteVpc":                  "typed body skips the vpc-deleted lifecycle event",
	"DeleteSubnet":               "typed body skips the subnet-deleted lifecycle event",
	"DeleteSecurityGroup":        "typed body skips the security-group-deleted lifecycle event",
	"CreateKeyPair":              "not yet compared against legacy line by line",
	"DeleteKeyPair":              "not yet compared against legacy line by line",
	"CreateRouteTable":           "not yet compared against legacy line by line",
	"DeleteRouteTable":           "not yet compared against legacy line by line",
	"CreateRoute":                "not yet compared against legacy line by line",
	"DeleteRoute":                "not yet compared against legacy line by line",
	"AssociateRouteTable":        "not yet compared against legacy line by line",
	"DisassociateRouteTable":     "not yet compared against legacy line by line",
	"CreateInternetGateway":      "not yet compared against legacy line by line",
	"DeleteInternetGateway":      "not yet compared against legacy line by line",
	"AttachInternetGateway":      "not yet compared against legacy line by line",
	"DetachInternetGateway":      "not yet compared against legacy line by line",
	"CreateVpcPeeringConnection": "not yet compared against legacy line by line",
	"AcceptVpcPeeringConnection": "not yet compared against legacy line by line",
	"DeleteVpcPeeringConnection": "not yet compared against legacy line by line",
	"AllocateAddress":            "not yet compared against legacy line by line",
	"ReleaseAddress":             "not yet compared against legacy line by line",
	"AssociateAddress":           "not yet compared against legacy line by line",
	"DisassociateAddress":        "not yet compared against legacy line by line",
	"CreateNatGateway":           "not yet compared against legacy line by line",
	"DeleteNatGateway":           "not yet compared against legacy line by line",
	"CreateVpnGateway":           "not yet compared against legacy line by line",
	"AttachVpnGateway":           "not yet compared against legacy line by line",
	"DetachVpnGateway":           "not yet compared against legacy line by line",
	"DeleteVpnGateway":           "not yet compared against legacy line by line",
	"CreateNetworkInterface":     "not yet compared against legacy line by line",
	"DeleteNetworkInterface":     "not yet compared against legacy line by line",
	"CreateVpcEndpoint":          "not yet compared against legacy line by line",
}
