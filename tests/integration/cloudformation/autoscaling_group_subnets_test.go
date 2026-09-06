package cloudformation_test

// AWS::AutoScaling::AutoScalingGroup's VPCZoneIdentifier (#1840). CDK
// synthesizes a group with subnets and no AvailabilityZones at all, and types
// the property the way CloudFormation documents it — an array of subnet ids,
// each a Ref to a subnet in the same stack. Auto Scaling derives the group's
// zones from those subnets, so the stack has to reach it with the subnets
// intact.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// asgGroupZones reads back one group's AvailabilityZones.
func asgGroupZones(t *testing.T, srv *helpers.TestServer, group string) []string {
	t.Helper()
	resp := asQuery(t, srv, "DescribeAutoScalingGroups", url.Values{
		"AutoScalingGroupNames.member.1": {group},
	})
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeAutoScalingGroups: status %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Zones []string `xml:"DescribeAutoScalingGroupsResult>AutoScalingGroups>member>AvailabilityZones>member"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode DescribeAutoScalingGroupsResponse: %v\n%s", err, body)
	}
	return out.Zones
}

const asgSubnetStackTemplate = `{
  "Resources": {
    "Vpc": {
      "Type": "AWS::EC2::VPC",
      "Properties": { "CidrBlock": "10.0.0.0/16" }
    },
    "SubnetA": {
      "Type": "AWS::EC2::Subnet",
      "Properties": {
        "VpcId": { "Ref": "Vpc" },
        "CidrBlock": "10.0.1.0/24",
        "AvailabilityZone": "us-east-1a"
      }
    },
    "SubnetB": {
      "Type": "AWS::EC2::Subnet",
      "Properties": {
        "VpcId": { "Ref": "Vpc" },
        "CidrBlock": "10.0.2.0/24",
        "AvailabilityZone": "us-east-1b"
      }
    },
    "LaunchTemplate": {
      "Type": "AWS::EC2::LaunchTemplate",
      "Properties": {
        "LaunchTemplateData": {
          "ImageId": "ami-0123456789abcdef0",
          "InstanceType": "t3.micro"
        }
      }
    },
    "ASG": {
      "Type": "AWS::AutoScaling::AutoScalingGroup",
      "Properties": {
        "MinSize": "2",
        "MaxSize": "4",
        "DesiredCapacity": "2",
        "VPCZoneIdentifier": [{ "Ref": "SubnetA" }, { "Ref": "SubnetB" }],
        "LaunchTemplate": {
          "LaunchTemplateId": { "Ref": "LaunchTemplate" },
          "Version": { "Fn::GetAtt": ["LaunchTemplate", "LatestVersionNumber"] }
        }
      }
    }
  },
  "Outputs": {
    "SubnetA": { "Value": { "Ref": "SubnetA" } },
    "SubnetB": { "Value": { "Ref": "SubnetB" } }
  }
}`

// TestCreateStack_AutoScalingGroupDerivesZonesFromSubnets provisions the shape
// CDK emits — subnets, no AvailabilityZones — and asserts the group takes its
// zones from the subnets and converges into them.
//
// VPCZoneIdentifier is "Array of String" in the resource reference and a
// comma-separated string in the API, so the handler has to join the list; a
// group that reached Auto Scaling without its subnets would launch nowhere in
// particular and derive no zones at all.
func TestCreateStack_AutoScalingGroupDerivesZonesFromSubnets(t *testing.T) {
	// Given: a stack with two subnets in two zones and a group naming only them
	srv := helpers.NewTestServer(t)
	const stackName = "asg-subnet-stack"

	// When: the stack is created
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {asgSubnetStackTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: the group reports the subnets' own zones, derived rather than given
	group := stackResourcePhysicalID(t, srv, stackName, "ASG")
	zones := asgGroupZones(t, srv, group)
	if len(zones) != 2 || zones[0] != "us-east-1a" || zones[1] != "us-east-1b" {
		t.Errorf("AvailabilityZones = %v, want [us-east-1a us-east-1b] derived from the subnets", zones)
	}

	// And: it converges, one instance per zone
	seen := map[string]int{}
	for _, inst := range asgInstancesForGroup(t, srv, group, 2) {
		seen[inst.AvailabilityZone]++
	}
	if seen["us-east-1a"] != 1 || seen["us-east-1b"] != 1 {
		t.Errorf("instances placed %+v, want one in each of us-east-1a and us-east-1b", seen)
	}
}
