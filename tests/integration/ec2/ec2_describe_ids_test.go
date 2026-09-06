package ec2_test

// ec2_describe_ids_test.go — a Describe*'s explicit `<Resource>Id.N` list is a
// question about named resources, not a filter (#1708).
//
// EC2 answers the two differently on purpose. A filter that matches nothing is
// a legitimate empty 200. An id the caller named and the region does not hold
// is an error, and it is the error — not the list length — that a Terraform
// refresh, a CloudFormation drift check or a waiter reads. Every case here
// pins one half of that distinction, including the half that must *not* have
// changed: an unmatched filter is still an empty 200.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// createVPC makes a VPC and returns its (real, AWS-shaped) ID, so a case can
// name an ID that exists alongside one that does not.
func createVPC(t *testing.T, srv *helpers.TestServer, cidr string) string {
	t.Helper()
	resp := ec2Query(t, srv, "CreateVpc", url.Values{"CidrBlock": []string{cidr}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		VpcID string `xml:"vpc>vpcId"`
	}
	body := readBody(t, resp)
	if err := xml.Unmarshal(body, &created); err != nil {
		t.Fatalf("unmarshal CreateVpcResponse: %v\nbody: %s", err, body)
	}
	if created.VpcID == "" {
		t.Fatalf("CreateVpc returned no vpcId: %s", body)
	}
	return created.VpcID
}

// Every operation in #1708's table, with an ID the region does not hold.
//
// The codes are AWS's, copied from the EC2 API reference's client error table
// (errors-overview.html) — including the casing, which is not consistent
// between resources and which clients match on exactly.
func TestDescribe_unknownExplicitIDIsNotFound(t *testing.T) {
	cases := []struct {
		action string
		param  string
		id     string
		code   string
	}{
		{"DescribeVpcs", "VpcId", "vpc-00000000", "InvalidVpcID.NotFound"},
		{"DescribeSubnets", "SubnetId", "subnet-00000000", "InvalidSubnetID.NotFound"},
		{"DescribeSecurityGroups", "GroupId", "sg-00000000", "InvalidGroup.NotFound"},
		{"DescribeRouteTables", "RouteTableId", "rtb-00000000", "InvalidRouteTableID.NotFound"},
		{"DescribeInternetGateways", "InternetGatewayId", "igw-00000000", "InvalidInternetGatewayID.NotFound"},
		{"DescribeNetworkInterfaces", "NetworkInterfaceId", "eni-00000000", "InvalidNetworkInterfaceID.NotFound"},
		{"DescribeInstances", "InstanceId", "i-00000000000000000", "InvalidInstanceID.NotFound"},

		// #1847's five. DescribeAddresses appears twice because it selects on
		// two things, and AWS answers them with different codes: an allocation
		// ID with InvalidAllocationID.NotFound, a public address with
		// InvalidAddress.NotFound.
		{"DescribeAddresses", "AllocationId", "eipalloc-00000000", "InvalidAllocationID.NotFound"},
		{"DescribeAddresses", "PublicIp", "192.0.2.55", "InvalidAddress.NotFound"},
		{"DescribeNatGateways", "NatGatewayId", "nat-00000000", "NatGatewayNotFound"},
		{"DescribeVpnGateways", "VpnGatewayId", "vgw-00000000", "InvalidVpnGatewayID.NotFound"},
		{"DescribeVpcEndpoints", "VpcEndpointId", "vpce-00000000", "InvalidVpcEndpointId.NotFound"},
		{"DescribeVpcPeeringConnections", "VpcPeeringConnectionId", "pcx-00000000", "InvalidVpcPeeringConnectionID.NotFound"},
	}

	for _, tc := range cases {
		t.Run(tc.action+"/"+tc.param, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := ec2Query(t, srv, tc.action, url.Values{tc.param + ".1": []string{tc.id}})
			defer resp.Body.Close()

			result := assertEC2QueryError(t, resp, http.StatusBadRequest, tc.code)
			if !strings.Contains(result.Errors[0].Message, tc.id) {
				t.Errorf("message %q does not name the ID %q", result.Errors[0].Message, tc.id)
			}
		})
	}
}

// The same operations with an ID that is not an ID at all. AWS separates this
// from "not there" because a typo, a truncated ID or an ARN passed where an ID
// belongs should fail as a bad request rather than look like a deleted
// resource.
func TestDescribe_malformedExplicitIDIsMalformed(t *testing.T) {
	cases := []struct {
		action string
		param  string
		value  string
		code   string
	}{
		{"DescribeVpcs", "VpcId", "not-an-id", "InvalidVpcID.Malformed"},
		{"DescribeSubnets", "SubnetId", "not-an-id", "InvalidSubnetID.Malformed"},
		{"DescribeSecurityGroups", "GroupId", "not-an-id", "InvalidGroupId.Malformed"},
		{"DescribeRouteTables", "RouteTableId", "not-an-id", "InvalidRouteTableId.Malformed"},
		{"DescribeInternetGateways", "InternetGatewayId", "not-an-id", "InvalidInternetGatewayId.Malformed"},
		{"DescribeNetworkInterfaces", "NetworkInterfaceId", "not-an-id", "InvalidNetworkInterfaceId.Malformed"},
		{"DescribeInstances", "InstanceId", "not-an-id", "InvalidInstanceID.Malformed"},

		// #1847's three that have a documented Malformed code. The public
		// address is the one selector in EC2 that is not a `<prefix>-<hex>`
		// ID, so what "malformed" means for it is "not an IPv4 address".
		{"DescribeNatGateways", "NatGatewayId", "not-an-id", "NatGatewayMalformed"},
		{"DescribeVpcEndpoints", "VpcEndpointId", "not-an-id", "InvalidVpcEndpointId.Malformed"},
		{"DescribeVpcPeeringConnections", "VpcPeeringConnectionId", "not-an-id", "InvalidVpcPeeringConnectionId.Malformed"},
		{"DescribeAddresses", "PublicIp", "not-an-ip", "InvalidAddress.Malformed"},
		{"DescribeAddresses", "PublicIp", "203.0.113.999", "InvalidAddress.Malformed"},
	}

	for _, tc := range cases {
		t.Run(tc.action+"/"+tc.param+"="+tc.value, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := ec2Query(t, srv, tc.action, url.Values{tc.param + ".1": []string{tc.value}})
			defer resp.Body.Close()

			assertEC2QueryError(t, resp, http.StatusBadRequest, tc.code)
		})
	}
}

// The two selectors the EC2 API reference lists no `.Malformed` code for:
// allocation IDs (`InvalidAllocationID.NotFound` has no Malformed sibling —
// `InvalidAddress.Malformed` is about the *address*, not the allocation) and
// virtual private gateways. Rather than invent a code AWS does not document,
// shape checking is left to the NotFound path, so an ID of the wrong shape is
// reported as one the region does not hold.
func TestDescribe_selectorsWithNoMalformedCodeAnswerNotFound(t *testing.T) {
	cases := []struct {
		action string
		param  string
		code   string
	}{
		{"DescribeAddresses", "AllocationId", "InvalidAllocationID.NotFound"},
		{"DescribeVpnGateways", "VpnGatewayId", "InvalidVpnGatewayID.NotFound"},
	}

	for _, tc := range cases {
		t.Run(tc.action+"/"+tc.param, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := ec2Query(t, srv, tc.action, url.Values{tc.param + ".1": []string{"not-an-id"}})
			defer resp.Body.Close()

			result := assertEC2QueryError(t, resp, http.StatusBadRequest, tc.code)
			if !strings.Contains(result.Errors[0].Message, "not-an-id") {
				t.Errorf("message %q does not name the ID", result.Errors[0].Message)
			}
		})
	}
}

// DescribeAddresses is the only describe with two ID selectors, so it is the
// only place the "shape before existence" rule has to hold across parameters
// as well as within one. AWS raises `.Malformed` out of request parsing, which
// sees the whole request, so a malformed address wins over an allocation that
// is merely absent — whichever order they arrive in.
func TestDescribeAddresses_malformedAddressBeatsAnUnknownAllocation(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ec2Query(t, srv, "DescribeAddresses", url.Values{
		"AllocationId.1": []string{"eipalloc-00000000"},
		"PublicIp.1":     []string{"not-an-ip"},
	})
	defer resp.Body.Close()

	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidAddress.Malformed")
}

// An allocation ID and a public address that both resolve select the address
// they name, and a describe naming only one of the two still resolves the
// other's list as empty — the selectors are independent.
func TestDescribeAddresses_bothSelectorsResolveTheirOwnList(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ec2Query(t, srv, "AllocateAddress", nil)
	body := readBody(t, resp)
	resp.Body.Close()
	var allocated struct {
		AllocationID string `xml:"allocationId"`
		PublicIP     string `xml:"publicIp"`
	}
	if err := xml.Unmarshal(body, &allocated); err != nil {
		t.Fatalf("unmarshal AllocateAddressResponse: %v\nbody: %s", err, body)
	}

	for _, params := range []url.Values{
		{"AllocationId.1": []string{allocated.AllocationID}},
		{"PublicIp.1": []string{allocated.PublicIP}},
		{"AllocationId.1": []string{allocated.AllocationID}, "PublicIp.1": []string{allocated.PublicIP}},
	} {
		resp := ec2Query(t, srv, "DescribeAddresses", params)
		helpers.AssertStatus(t, resp, http.StatusOK)
		got := string(readBody(t, resp))
		resp.Body.Close()
		if !strings.Contains(got, allocated.AllocationID) {
			t.Errorf("DescribeAddresses %v did not return %s: %s", params, allocated.AllocationID, got)
		}
	}
}

// A VPC ID of the right shape but the wrong length is malformed too: AWS
// accepts the eight-character form it used to issue and the seventeen-character
// form it issues now, and nothing in between.
func TestDescribeVpcs_wrongLengthIDIsMalformed(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for _, id := range []string{"vpc-0000", "vpc-000000000", "vpc-0000000000000000", "vpc-0000000g"} {
		t.Run(id, func(t *testing.T) {
			resp := ec2Query(t, srv, "DescribeVpcs", url.Values{"VpcId.1": []string{id}})
			defer resp.Body.Close()
			assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidVpcID.Malformed")
		})
	}
}

// A mixed request fails rather than answering with the part that resolved.
// A caller that named two VPCs and got one back would have to compare the
// result to its own request to notice, which is the check AWS's error exists to
// spare it.
func TestDescribeVpcs_mixedKnownAndUnknownIDsFails(t *testing.T) {
	srv := helpers.NewTestServer(t)
	vpcID := createVPC(t, srv, "10.0.0.0/16")

	resp := ec2Query(t, srv, "DescribeVpcs", url.Values{
		"VpcId.1": []string{vpcID},
		"VpcId.2": []string{"vpc-00000000"},
	})
	defer resp.Body.Close()

	result := assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidVpcID.NotFound")
	if !strings.Contains(result.Errors[0].Message, "vpc-00000000") {
		t.Errorf("message %q names the wrong ID", result.Errors[0].Message)
	}
}

// Shape is checked across the whole list before any of it is resolved, which
// is where AWS checks it: `.Malformed` comes out of request parsing, ahead of
// the lookup. So a well-formed unknown ID listed *first* does not get to answer
// before a malformed one listed second.
func TestDescribeVpcs_malformedIDWinsOverAnUnknownOne(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ec2Query(t, srv, "DescribeVpcs", url.Values{
		"VpcId.1": []string{"vpc-00000000"},
		"VpcId.2": []string{"not-an-id"},
	})
	defer resp.Body.Close()

	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidVpcID.Malformed")
}

// The regression risk #1708 names, and the reason this is not "Describe should
// error when it finds nothing": a filter is a question about which resources
// look like this, and "none of them" is a legitimate answer with a 200 on it.
func TestDescribe_filterMatchingNothingIsStillAnEmpty200(t *testing.T) {
	cases := []struct {
		action string
		filter string
		value  string
	}{
		{"DescribeVpcs", "cidr", "10.99.99.0/24"},
		{"DescribeSubnets", "cidr-block", "10.99.99.0/24"},
		{"DescribeSecurityGroups", "group-id", "sg-99999999"},
		{"DescribeRouteTables", "route-table-id", "rtb-99999999"},
		{"DescribeInternetGateways", "internet-gateway-id", "igw-99999999"},
		{"DescribeNetworkInterfaces", "network-interface-id", "eni-99999999"},
		{"DescribeInstances", "instance-id", "i-99999999"},
		{"DescribeAddresses", "public-ip", "203.0.113.199"},
		{"DescribeNatGateways", "nat-gateway-id", "nat-99999999"},
		{"DescribeVpnGateways", "vpn-gateway-id", "vgw-99999999"},
		{"DescribeVpcEndpoints", "vpc-endpoint-id", "vpce-99999999"},
		{"DescribeVpcPeeringConnections", "vpc-peering-connection-id", "pcx-99999999"},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			createVPC(t, srv, "10.0.0.0/16")

			resp := ec2Query(t, srv, tc.action, url.Values{
				"Filter.1.Name":    []string{tc.filter},
				"Filter.1.Value.1": []string{tc.value},
			})
			defer resp.Body.Close()

			helpers.AssertStatus(t, resp, http.StatusOK)
			body := string(readBody(t, resp))
			if strings.Contains(body, "<item>") {
				t.Errorf("%s with an unmatched filter returned items: %s", tc.action, body)
			}
		})
	}
}

// A describe with no ID list and no filters still answers with the region, so
// the new resolution cannot have turned an unselected describe into an error.
func TestDescribeVpcs_noSelectorsStillListsTheRegion(t *testing.T) {
	srv := helpers.NewTestServer(t)
	vpcID := createVPC(t, srv, "10.0.0.0/16")

	resp := ec2Query(t, srv, "DescribeVpcs", nil)
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	if body := string(readBody(t, resp)); !strings.Contains(body, vpcID) {
		t.Errorf("DescribeVpcs with no selectors did not return %s: %s", vpcID, body)
	}
}

// ReleaseAddress names the allocation ID it could not resolve with the code
// real EC2 answers: InvalidAllocationID.NotFound, not InvalidAddressID.NotFound
// (#1708). The reference's description column reads as though the latter is
// the release-specific one; the wire disagrees.
func TestReleaseAddress_unknownAllocationIsInvalidAllocationID(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ec2Query(t, srv, "ReleaseAddress", url.Values{
		"AllocationId": []string{"eipalloc-00000000"},
	})
	defer resp.Body.Close()

	result := assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidAllocationID.NotFound")
	if !strings.Contains(result.Errors[0].Message, "eipalloc-00000000") {
		t.Errorf("message %q does not name the allocation ID", result.Errors[0].Message)
	}
}
