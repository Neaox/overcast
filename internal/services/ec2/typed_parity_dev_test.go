//go:build dev

package ec2

// typed_parity_dev_test.go — the differential test that lets an operation be
// routed to the typed registry.
//
// Every operation in ec2TypedOps is answered by DispatchQuery from
// h.typedOp instead of h.ops. What makes that a safe change is not that the
// typed body looks right, but that both dispatch paths, handed the same
// request against the same state, produce the same bytes: same status, same
// XML, same error envelope. That is what this file checks, per operation, over
// a fixture seeded to make every filter and ID selection below actually
// discriminate.
//
// It is written as a table because the table is the audit. An operation cannot
// be in ec2TypedOps without a row here (TestTypedParity_coversEveryRoutedOp),
// so "which operations have been checked against legacy, with what request" is
// a question the file answers rather than a claim in a commit message.
//
// The `dev` tag is where Overcast's cross-check tests live; CI runs them in the
// `Test suite (-tags slim,dev)` job. Run locally with
// `go test -tags dev ./internal/services/ec2/...`.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
)

// parityCase is one request put to both dispatch paths.
type parityCase struct {
	// name distinguishes the case in test output.
	name string
	// params are the request's parameters, less Action.
	params url.Values
}

// parityCases is every routed operation and the requests it is checked with.
//
// A case earns its place by discriminating: a filter that selects a subset of
// the fixture, an ID list that names one of several, a filter name the
// operation does not implement. A bare no-parameter call proves only that two
// unfiltered listings agree, which was already true before #754 for most of
// these — it is the filtered cases that were silently wrong.
func parityCases() map[string][]parityCase {
	unknown := filterParams("not-a-filter", "anything")
	return map[string][]parityCase{
		"DescribeRegions": {
			{name: "all", params: url.Values{}},
			{name: "by-name", params: filterParams("region-name", "eu-west-1")},
			{name: "wildcard", params: filterParams("region-name", "us-*")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeAvailabilityZones": {
			{name: "all", params: url.Values{}},
			{name: "by-zone", params: filterParams("zone-name", "us-east-1b")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeInstances": {
			{name: "all", params: url.Values{}},
			{name: "by-state", params: filterParams("instance-state-name", "running")},
			{name: "by-id", params: indexedParams("InstanceId", "i-parity-running")},
			{name: "by-vpc", params: filterParams("vpc-id", "vpc-parity-a")},
			{name: "by-tag", params: filterParams("tag:Role", "web")},
			{name: "tag-key", params: filterParams("tag-key", "Role")},
			{name: "no-match", params: filterParams("instance-state-name", "terminated")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeInstanceTypes": {
			{name: "none-requested", params: url.Values{}},
			{name: "one", params: indexedParams("InstanceType", "t3.micro")},
			{name: "unknown-type", params: indexedParams("InstanceType", "zz.unknown")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeVpcs": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("VpcId", "vpc-parity-a")},
			{name: "by-cidr", params: filterParams("cidr", "10.0.0.0/16")},
			{name: "is-default", params: filterParams("isDefault", "true")},
			{name: "by-tag", params: filterParams("tag:Name", "parity-*")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeSubnets": {
			{name: "all", params: url.Values{}},
			{name: "by-vpc", params: filterParams("vpc-id", "vpc-parity-a")},
			{name: "by-id", params: indexedParams("SubnetId", "subnet-parity-a")},
			{name: "by-az", params: filterParams("availability-zone", "us-east-1b")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeSecurityGroups": {
			{name: "all", params: url.Values{}},
			{name: "by-vpc", params: filterParams("vpc-id", "vpc-parity-a")},
			{name: "by-id", params: indexedParams("GroupId", "sg-parity-a")},
			{name: "by-name", params: filterParams("group-name", "parity-web")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeImages": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("ImageId", "ami-12345678")},
			{name: "by-owner", params: filterParams("owner-id", "137112412989")},
			{name: "is-public", params: filterParams("is-public", "True")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeKeyPairs": {
			{name: "all", params: url.Values{}},
			{name: "by-name", params: indexedParams("KeyName", "parity-key")},
			{name: "by-filter", params: filterParams("key-name", "parity-key")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeRouteTables": {
			{name: "all", params: url.Values{}},
			{name: "by-vpc", params: filterParams("vpc-id", "vpc-parity-a")},
			{name: "main", params: filterParams("association.main", "true")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeInternetGateways": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("InternetGatewayId", "igw-parity")},
			{name: "by-attachment", params: filterParams("attachment.vpc-id", "vpc-parity-a")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeVpcPeeringConnections": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("VpcPeeringConnectionId", "pcx-parity")},
			{name: "by-status", params: filterParams("status-code", "active")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeTags": {
			{name: "all", params: url.Values{}},
			{name: "by-resource", params: filterParams("resource-id", "vpc-parity-a")},
			{name: "by-key", params: filterParams("key", "Name")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeAddresses": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("AllocationId", "eipalloc-parity")},
			{name: "by-public-ip", params: filterParams("public-ip", "203.0.113.7")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeNatGateways": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("NatGatewayId", "nat-parity")},
			{name: "by-vpc", params: filterParams("vpc-id", "vpc-parity-a")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeNetworkInterfaces": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("NetworkInterfaceId", "eni-parity")},
			{name: "by-subnet", params: filterParams("subnet-id", "subnet-parity-a")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeVpnGateways": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("VpnGatewayId", "vgw-parity")},
			{name: "by-state", params: filterParams("state", "available")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeVpcEndpoints": {
			{name: "all", params: url.Values{}},
			{name: "by-id", params: indexedParams("VpcEndpointId", "vpce-parity")},
			{name: "by-service", params: filterParams("service-name", "com.amazonaws.*.s3")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeDhcpOptions": {
			{name: "all", params: url.Values{}},
			// dhcpOptionsFilters implements nothing, so every filter name is
			// refused — including ones AWS models.
			{name: "modelled-filter", params: filterParams("dhcp-options-id", "dopt-1")},
			{name: "unknown-filter", params: unknown},
		},
		"DescribeAccountAttributes": {
			{name: "all", params: url.Values{}},
		},
		"DescribeVpcAttribute": {
			{name: "dns-support", params: attributeParams("vpc-parity-a", "enableDnsSupport")},
			{name: "dns-hostnames", params: attributeParams("vpc-parity-a", "enableDnsHostnames")},
			{name: "unknown-vpc", params: attributeParams("vpc-nope", "enableDnsSupport")},
			{name: "missing-vpc-id", params: attributeParams("", "enableDnsSupport")},
		},
	}
}

func indexedParams(param string, values ...string) url.Values {
	p := url.Values{}
	for i, v := range values {
		p.Set(param+"."+strconv.Itoa(i+1), v)
	}
	return p
}

func attributeParams(vpcID, attribute string) url.Values {
	p := url.Values{}
	if vpcID != "" {
		p.Set("VpcId", vpcID)
	}
	p.Set("Attribute", attribute)
	return p
}

// ── The fixture ─────────────────────────────────────────────────────────────

// seedParityFixture fills a region with two of everything the routed describes
// read, so a filter naming one of them is a filter that can be seen to work.
func seedParityFixture(t *testing.T, h *Handler) {
	t.Helper()
	ctx := context.Background()
	fail := func(what string, aerr *protocol.AWSError) {
		t.Helper()
		if aerr != nil {
			t.Fatalf("%s: %s", what, aerr.Message)
		}
	}

	fail("putVPC a", h.store.putVPC(ctx, &VPC{
		VpcID: "vpc-parity-a", CidrBlock: "10.0.0.0/16", State: "available",
		EnableDnsSupport: true, EnableDnsHostnames: false,
	}))
	fail("putVPC b", h.store.putVPC(ctx, &VPC{
		VpcID: "vpc-parity-b", CidrBlock: "10.1.0.0/16", State: "available",
	}))

	fail("putSubnet a", h.store.putSubnet(ctx, &Subnet{
		SubnetID: "subnet-parity-a", VpcID: "vpc-parity-a", CidrBlock: "10.0.1.0/24",
		AvailabilityZone: "us-east-1a", State: "available", MapPublicIpOnLaunch: true,
	}))
	fail("putSubnet b", h.store.putSubnet(ctx, &Subnet{
		SubnetID: "subnet-parity-b", VpcID: "vpc-parity-b", CidrBlock: "10.1.1.0/24",
		AvailabilityZone: "us-east-1b", State: "available",
	}))

	fail("putSG a", h.store.putSecurityGroup(ctx, &SecurityGroup{
		GroupID: "sg-parity-a", GroupName: "parity-web", Description: "parity web", VpcID: "vpc-parity-a",
		IpPermissions:       []IpPermission{{IpProtocol: "tcp", FromPort: 443, ToPort: 443, IpRanges: []IpRange{{CidrIp: "0.0.0.0/0"}}}},
		IpPermissionsEgress: []IpPermission{{IpProtocol: "-1", IpRanges: []IpRange{{CidrIp: "0.0.0.0/0"}}}},
	}))
	fail("putSG b", h.store.putSecurityGroup(ctx, &SecurityGroup{
		GroupID: "sg-parity-b", GroupName: "parity-db", Description: "parity db", VpcID: "vpc-parity-b",
	}))

	fail("putInstance running", h.store.putInstance(ctx, &Instance{
		InstanceID: "i-parity-running", ImageID: "ami-12345678", InstanceType: "t3.micro",
		State: InstanceState{Code: 16, Name: "running"}, LaunchTime: "2026-01-01T00:00:00Z",
		SubnetID: "subnet-parity-a", VpcID: "vpc-parity-a", PrivateIPAddress: "10.0.1.10",
		Placement: Placement{AvailabilityZone: "us-east-1a"},
	}))
	fail("putInstance stopped", h.store.putInstance(ctx, &Instance{
		InstanceID: "i-parity-stopped", ImageID: "ami-12345678", InstanceType: "t3.small",
		State: InstanceState{Code: 80, Name: "stopped"}, LaunchTime: "2026-01-01T00:00:00Z",
		SubnetID: "subnet-parity-b", VpcID: "vpc-parity-b", PrivateIPAddress: "10.1.1.10",
		Placement: Placement{AvailabilityZone: "us-east-1b"},
	}))

	fail("putKeyPair", h.store.putKeyPair(ctx, &KeyPair{
		KeyName: "parity-key", KeyFingerprint: "aa:bb:cc", KeyPairID: "key-parity",
	}))
	fail("putKeyPair other", h.store.putKeyPair(ctx, &KeyPair{
		KeyName: "other-key", KeyFingerprint: "dd:ee:ff", KeyPairID: "key-other",
	}))

	fail("putRouteTable", h.store.putRouteTable(ctx, &RouteTable{
		RouteTableID: "rtb-parity", VpcID: "vpc-parity-a",
		Routes:       []Route{{DestinationCidrBlock: "10.0.0.0/16", GatewayID: "local", Origin: "CreateRouteTable"}},
		Associations: []RouteTableAssociation{{AssociationID: "rtbassoc-parity", RouteTableID: "rtb-parity", Main: true}},
	}))

	fail("putIGW", h.store.putInternetGateway(ctx, &InternetGateway{
		InternetGatewayID: "igw-parity",
		Attachments:       []IGWAttachment{{VpcID: "vpc-parity-a", State: "attached"}},
	}))
	fail("putIGW other", h.store.putInternetGateway(ctx, &InternetGateway{InternetGatewayID: "igw-parity-detached"}))

	fail("putPeering", h.store.putVpcPeeringConnection(ctx, &VpcPeeringConnection{
		VpcPeeringConnectionID: "pcx-parity",
		RequesterVpcInfo:       VpcPeeringConnectionVpcInfo{VpcID: "vpc-parity-a", CidrBlock: "10.0.0.0/16", Region: "us-east-1"},
		AccepterVpcInfo:        VpcPeeringConnectionVpcInfo{VpcID: "vpc-parity-b", CidrBlock: "10.1.0.0/16", Region: "us-east-1"},
		Status:                 VpcPeeringConnectionStatus{Code: "active", Message: "Active"},
	}))

	fail("putElasticIP", h.store.putElasticIP(ctx, &ElasticIP{
		AllocationID: "eipalloc-parity", PublicIP: "203.0.113.7", Domain: "vpc",
	}))
	fail("putElasticIP other", h.store.putElasticIP(ctx, &ElasticIP{
		AllocationID: "eipalloc-other", PublicIP: "203.0.113.8", Domain: "vpc",
	}))

	fail("putNatGateway", h.store.putNatGateway(ctx, &NatGateway{
		NatGatewayID: "nat-parity", SubnetID: "subnet-parity-a", VpcID: "vpc-parity-a",
		State: "available", CreateTime: "2026-01-01T00:00:00Z",
		AllocationID: "eipalloc-parity", PublicIP: "203.0.113.7", PrivateIP: "10.0.1.20",
	}))

	fail("putENI", h.store.putNetworkInterface(ctx, &NetworkInterface{
		NetworkInterfaceID: "eni-parity", SubnetID: "subnet-parity-a", VpcID: "vpc-parity-a",
		AvailabilityZone: "us-east-1a", Description: "parity", PrivateIPAddress: "10.0.1.30",
		Status: "available", MacAddress: "02:aa:bb:cc:dd:ee",
	}))

	fail("putVpnGateway", h.store.putVpnGateway(ctx, &VpnGateway{
		VpnGatewayID: "vgw-parity", State: "available", Type: "ipsec.1",
		AvailabilityZone: "us-east-1a", AmazonSideAsn: 64512,
		Attachments: []VpnGatewayAttachment{{VpcID: "vpc-parity-a", State: "attached"}},
	}))

	fail("putVpcEndpoint", h.store.putVpcEndpoint(ctx, &VpcEndpoint{
		VpcEndpointID: "vpce-parity", VpcID: "vpc-parity-a",
		ServiceName: "com.amazonaws.us-east-1.s3", State: "available", VpcEndpointType: "Gateway",
	}))

	// The region's default VPC is seeded on first read by DescribeVpcs and
	// DescribeSubnets. Seeding it here instead means both runs of a request see
	// the same one, identifiers included.
	h.ensureDefaultVPCQuietly(ctx)

	fail("tag vpc a", h.putResourceTags(ctx, "vpc-parity-a", []Tag{{Key: "Name", Value: "parity-a"}}))
	fail("tag vpc b", h.putResourceTags(ctx, "vpc-parity-b", []Tag{{Key: "Name", Value: "other-b"}}))
	fail("tag instance", h.putResourceTags(ctx, "i-parity-running", []Tag{{Key: "Role", Value: "web"}}))
	fail("tag subnet", h.putResourceTags(ctx, "subnet-parity-a", []Tag{{Key: "Tier", Value: "public"}}))
}

// ── The comparison ──────────────────────────────────────────────────────────

// perCallIDs are the identifiers a describe mints fresh on every call, which
// therefore differ between two runs of the same request without either being
// wrong. Both are fabricated rather than stored: DescribeInstances' reservation
// ID and DescribeDhcpOptions' option-set ID.
var perCallIDs = regexp.MustCompile(`(r|dopt)-[0-9a-f]{8}`)

// stable removes what differs between any two calls, leaving what a divergence
// would show up in. The request ID is already pinned by driving both paths with
// the same request context.
func stable(body string) string {
	return perCallIDs.ReplaceAllString(body, "$1-XXXXXXXX")
}

// answerBoth puts one request to both dispatch paths against one seeded region
// and returns (legacy, typed) as status plus body.
//
// One handler rather than two, because two would disagree on the identifiers
// the default-VPC seeding mints — they are random per handler — and masking
// those would blind the comparison to exactly the field a describe is most
// likely to get wrong. A routed describe is read-only apart from that seeding,
// and seedParityFixture does the seeding up front, so the second run sees the
// state the first one did.
func answerBoth(t *testing.T, action string, params url.Values) (legacy, typed string) {
	t.Helper()

	h := defaultVPCHandler(t)
	seedParityFixture(t, h)

	run := func(useTyped bool) string {
		p := url.Values{}
		for k, vs := range params {
			for _, v := range vs {
				p.Add(k, v)
			}
		}
		p.Set("Action", action)
		p.Set("Version", "2016-11-15")

		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(p.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(protocol.ContextWithRequestID(r.Context(), "parity-request-id"))
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		w := httptest.NewRecorder()

		if useTyped {
			operation, ok := h.typedOp[action]
			if !ok {
				t.Fatalf("no typed operation %s", action)
			}
			operation.Invoke(w, r, ec2ErrorCodec{Codec: codec.QueryXML})
		} else {
			handler, ok := h.ops[action]
			if !ok {
				t.Fatalf("no legacy handler %s", action)
			}
			handler(w, r)
		}
		return strconv.Itoa(w.Code) + "\n" + stable(w.Body.String())
	}

	return run(false), run(true)
}

// TestTypedParity is the gate every operation in ec2TypedOps passed to get
// there: the same request, put to both dispatch paths against the same state,
// answers with the same bytes.
func TestTypedParity(t *testing.T) {
	cases := parityCases()
	actions := make([]string, 0, len(cases))
	for action := range cases {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			for _, c := range cases[action] {
				t.Run(c.name, func(t *testing.T) {
					legacy, typed := answerBoth(t, action, c.params)
					if legacy != typed {
						t.Errorf("the two dispatch paths disagree.\nlegacy:\n%s\n\ntyped:\n%s", legacy, typed)
					}
				})
			}
		})
	}
}

// An operation routed to the typed path with no parity row is an operation
// nobody checked — which is the state #754 records, arrived at one unnoticed
// registration at a time.
func TestTypedParity_coversEveryRoutedOp(t *testing.T) {
	cases := parityCases()
	for action := range ec2TypedOps {
		rows, ok := cases[action]
		if !ok || len(rows) == 0 {
			t.Errorf("%s is routed to the typed registry with no row in parityCases", action)
		}
	}
	for action := range cases {
		if !ec2TypedOps[action] {
			t.Errorf("parityCases has a row for %s, which is not routed — drop the row or route the operation", action)
		}
	}
}

// Every routed operation that declares a filterSpec is checked with a filter
// name it does not implement, because #1032's rule is the one most easily lost
// in a decode change: an ignored filter answers 200 with the wrong set rather
// than failing.
func TestTypedParity_everyFilteredOpIsCheckedWithAnUnknownFilter(t *testing.T) {
	for action, rows := range parityCases() {
		if _, declared := supportedFilters[action]; !declared {
			continue
		}
		found := false
		for _, row := range rows {
			if row.params.Get("Filter.1.Name") == "not-a-filter" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s declares a filterSpec but has no parity row sending an unrecognised filter", action)
		}
	}
}
