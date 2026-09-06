package ec2_test

// ec2_not_found_codes_test.go — the code an EC2 operation answers when the
// single resource it names is not there (#1847).
//
// This is the other half of the explicit-id rule in ec2_describe_ids_test.go.
// A Describe* resolves a list; every other operation resolves one id through
// the store, and the store used to answer all four of its own lookups with
// `InvalidId.NotFound` — a code AWS has never sent for anything. A client
// switching on the code (a Terraform destroy that tolerates "already gone", a
// CloudFormation teardown, a retry loop that only retries some codes) matches
// the per-resource string, so the generic one reads as an unknown failure.
//
// The codes here are the same ones describe_ids.go carries, deliberately: they
// come from one table rather than two, so a Describe and a Delete cannot drift
// apart on the same resource.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// Every operation that resolves one id through the store, with an id the
// region does not hold.
func TestOperation_unknownResourceAnswersItsOwnAWSCode(t *testing.T) {
	cases := []struct {
		name    string
		action  string
		params  url.Values
		id      string
		code    string
		message string
	}{
		{
			name:    "DeleteVpc",
			action:  "DeleteVpc",
			params:  url.Values{"VpcId": []string{"vpc-00000000"}},
			id:      "vpc-00000000",
			code:    "InvalidVpcID.NotFound",
			message: "The vpc ID 'vpc-00000000' does not exist",
		},
		{
			name:    "DeleteSubnet",
			action:  "DeleteSubnet",
			params:  url.Values{"SubnetId": []string{"subnet-00000000"}},
			id:      "subnet-00000000",
			code:    "InvalidSubnetID.NotFound",
			message: "The subnet ID 'subnet-00000000' does not exist",
		},
		{
			// AWS words this one without "ID", the same as
			// DescribeSecurityGroups does.
			name:    "DeleteSecurityGroup",
			action:  "DeleteSecurityGroup",
			params:  url.Values{"GroupId": []string{"sg-00000000"}},
			id:      "sg-00000000",
			code:    "InvalidGroup.NotFound",
			message: "The security group 'sg-00000000' does not exist",
		},
		{
			name:   "AuthorizeSecurityGroupIngress",
			action: "AuthorizeSecurityGroupIngress",
			params: url.Values{
				"GroupId":                           []string{"sg-00000000"},
				"IpPermissions.1.IpProtocol":        []string{"tcp"},
				"IpPermissions.1.FromPort":          []string{"443"},
				"IpPermissions.1.ToPort":            []string{"443"},
				"IpPermissions.1.IpRanges.1.CidrIp": []string{"0.0.0.0/0"},
			},
			id:      "sg-00000000",
			code:    "InvalidGroup.NotFound",
			message: "The security group 'sg-00000000' does not exist",
		},
		{
			name:   "RevokeSecurityGroupEgress",
			action: "RevokeSecurityGroupEgress",
			params: url.Values{
				"GroupId":                           []string{"sg-00000000"},
				"IpPermissions.1.IpProtocol":        []string{"-1"},
				"IpPermissions.1.IpRanges.1.CidrIp": []string{"0.0.0.0/0"},
			},
			id:      "sg-00000000",
			code:    "InvalidGroup.NotFound",
			message: "The security group 'sg-00000000' does not exist",
		},
		{
			name:    "ModifySubnetAttribute",
			action:  "ModifySubnetAttribute",
			params:  url.Values{"SubnetId": []string{"subnet-00000000"}, "MapPublicIpOnLaunch.Value": []string{"true"}},
			id:      "subnet-00000000",
			code:    "InvalidSubnetID.NotFound",
			message: "The subnet ID 'subnet-00000000' does not exist",
		},
		{
			name:    "StopInstances",
			action:  "StopInstances",
			params:  url.Values{"InstanceId.1": []string{"i-00000000000000000"}},
			id:      "i-00000000000000000",
			code:    "InvalidInstanceID.NotFound",
			message: "The instance ID 'i-00000000000000000' does not exist",
		},
		{
			name:    "ModifyInstanceAttribute",
			action:  "ModifyInstanceAttribute",
			params:  url.Values{"InstanceId": []string{"i-00000000000000000"}, "InstanceType.Value": []string{"t3.small"}},
			id:      "i-00000000000000000",
			code:    "InvalidInstanceID.NotFound",
			message: "The instance ID 'i-00000000000000000' does not exist",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			resp := ec2Query(t, srv, tc.action, tc.params)
			defer resp.Body.Close()

			result := assertEC2QueryError(t, resp, http.StatusBadRequest, tc.code)
			if got := result.Errors[0].Message; got != tc.message {
				t.Errorf("message = %q, want %q", got, tc.message)
			}
			if !strings.Contains(result.Errors[0].Message, tc.id) {
				t.Errorf("message %q does not name the ID", result.Errors[0].Message)
			}
		})
	}
}

// No operation answers the generic code any more. It was Overcast's own
// invention — AWS has no `InvalidId.NotFound` — and a single grep is what
// keeps a new store lookup from reaching for it again.
func TestOperation_noResourceAnswersTheGenericCode(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for _, tc := range []struct {
		action string
		params url.Values
	}{
		{"DeleteVpc", url.Values{"VpcId": []string{"vpc-00000000"}}},
		{"DeleteSubnet", url.Values{"SubnetId": []string{"subnet-00000000"}}},
		{"DeleteSecurityGroup", url.Values{"GroupId": []string{"sg-00000000"}}},
		{"StopInstances", url.Values{"InstanceId.1": []string{"i-00000000000000000"}}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			resp := ec2Query(t, srv, tc.action, tc.params)
			defer resp.Body.Close()
			if body := string(readBody(t, resp)); strings.Contains(body, "InvalidId.NotFound") {
				t.Errorf("%s still answers the generic code: %s", tc.action, body)
			}
		})
	}
}
