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
// Wave 2 (#754) closes the remainder: every mutation whose typed body was a
// stub (validated a parameter, touched no store) now does the write — state
// transitions, security-group rules, tag deletes, DNS/public-IP attribute
// persistence — and every create that dropped part of its input now carries
// all of it (RunInstances' SecurityGroupId.N/TagSpecification.N, CreateVpc's
// EnableDnsSupport default, NAT/VPN gateway TagSpecification.N). The three
// deletes DeleteSecurityGroup/DeleteSubnet/DeleteVpc now run the #1233
// dependency checks (delete_dependencies.go) on both paths, so a
// DependencyViolation answer is byte-identical whichever decoder read the
// request. The rest of the remainder turned out, on reading both bodies side
// by side, to already be correct twins — same store calls, same validation,
// same (absence of) event-bus publish — and needed only a parity row to prove
// it, not a code change.
//
// ec2TypedDispatchRemainder is kept, empty, as the ledger's terminal state:
// TestTypedDispatch_everyOpIsClassified still fails on an operation in
// neither map, so a newly registered typed operation cannot arrive
// unclassified in the future.

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

	// Wave 2: the 12 stubbed mutations, now performing the write.
	"TerminateInstances":            true,
	"StartInstances":                true,
	"StopInstances":                 true,
	"DeleteTags":                    true,
	"DeleteVpcEndpoints":            true,
	"ModifyInstanceAttribute":       true,
	"AuthorizeSecurityGroupIngress": true,
	"AuthorizeSecurityGroupEgress":  true,
	"RevokeSecurityGroupIngress":    true,
	"RevokeSecurityGroupEgress":     true,
	"ModifySubnetAttribute":         true,
	"ModifyVpcAttribute":            true,

	// Wave 2: the 5 creates that dropped part of their input, now carrying
	// all of it.
	"RunInstances":        true,
	"CreateVpc":           true,
	"CreateSubnet":        true,
	"CreateSecurityGroup": true,
	"CreateTags":          true,

	// Wave 2: DeleteVpc/DeleteSubnet/DeleteSecurityGroup now run the #1233
	// dependency checks and publish the same lifecycle event legacy does.
	"DeleteVpc":           true,
	"DeleteSubnet":        true,
	"DeleteSecurityGroup": true,

	// Wave 2: read against legacy line by line and confirmed correct twins
	// (same store calls, same validation, same absence of event-bus
	// publish); CreateNatGateway/CreateVpnGateway additionally gained the
	// TagSpecification.N support legacy already had, and CreateVpcEndpoint
	// gained the VpcEndpointType default legacy already honoured.
	"CreateKeyPair":              true,
	"DeleteKeyPair":              true,
	"CreateRouteTable":           true,
	"DeleteRouteTable":           true,
	"CreateRoute":                true,
	"DeleteRoute":                true,
	"AssociateRouteTable":        true,
	"DisassociateRouteTable":     true,
	"CreateInternetGateway":      true,
	"DeleteInternetGateway":      true,
	"AttachInternetGateway":      true,
	"DetachInternetGateway":      true,
	"CreateVpcPeeringConnection": true,
	"AcceptVpcPeeringConnection": true,
	"DeleteVpcPeeringConnection": true,
	"AllocateAddress":            true,
	"ReleaseAddress":             true,
	"AssociateAddress":           true,
	"DisassociateAddress":        true,
	"CreateNatGateway":           true,
	"DeleteNatGateway":           true,
	"CreateVpnGateway":           true,
	"AttachVpnGateway":           true,
	"DetachVpnGateway":           true,
	"DeleteVpnGateway":           true,
	"CreateNetworkInterface":     true,
	"DeleteNetworkInterface":     true,
	"CreateVpcEndpoint":          true,
}

// ec2TypedDispatchRemainder is every registered typed operation still answered
// by legacy, and what its typed body would have to gain first.
//
// It is a work ledger, not an exemption list: each entry names a real defect in
// the typed implementation, and an operation leaves the map by being fixed and
// moved to ec2TypedOps, never by being excused. TestTypedDispatch_everyOpIsClassified
// fails if an operation is in neither map, so a newly registered typed
// operation cannot arrive unclassified — which is how 69 of them came to be
// unreachable without anything failing.
//
// Empty as of wave 2 (#754): every operation this map once named has either
// had its typed body completed to match legacy (the 12 stubs, the 5
// input-dropping creates) or been read against legacy and found already
// equivalent (the remaining 31). #754 is closed by this map staying empty.
var ec2TypedDispatchRemainder = map[string]string{}
