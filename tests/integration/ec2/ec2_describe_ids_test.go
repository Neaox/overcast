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
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
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
		code   string
	}{
		{"DescribeVpcs", "VpcId", "InvalidVpcID.Malformed"},
		{"DescribeSubnets", "SubnetId", "InvalidSubnetID.Malformed"},
		{"DescribeSecurityGroups", "GroupId", "InvalidGroupId.Malformed"},
		{"DescribeRouteTables", "RouteTableId", "InvalidRouteTableId.Malformed"},
		{"DescribeInternetGateways", "InternetGatewayId", "InvalidInternetGatewayId.Malformed"},
		{"DescribeNetworkInterfaces", "NetworkInterfaceId", "InvalidNetworkInterfaceId.Malformed"},
		{"DescribeInstances", "InstanceId", "InvalidInstanceID.Malformed"},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := ec2Query(t, srv, tc.action, url.Values{tc.param + ".1": []string{"not-an-id"}})
			defer resp.Body.Close()

			assertEC2QueryError(t, resp, http.StatusBadRequest, tc.code)
		})
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
