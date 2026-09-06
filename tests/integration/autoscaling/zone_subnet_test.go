package autoscaling_test

// Zone and subnet handling (#1840). A group's AvailabilityZones and its
// VPCZoneIdentifier subnets have to agree, because the reconciler launches into
// one of those subnets and EC2 places the instance in that subnet's own zone
// (#1839). These tests pin the placement each launch ends up with.

import (
	"encoding/xml"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// newVPC creates a VPC and returns its id.
func newVPC(t *testing.T, srv *helpers.TestServer, cidr string) string {
	t.Helper()
	resp := ec2Call(t, srv, "CreateVpc", map[string]string{"CidrBlock": cidr})
	body := xmlText(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateVpc: %d: %s", resp.StatusCode, body)
	}
	var out struct {
		VpcID string `xml:"vpc>vpcId"`
	}
	if err := xml.Unmarshal([]byte(body), &out); err != nil || out.VpcID == "" {
		t.Fatalf("CreateVpc: cannot read vpcId: %v\n%s", err, body)
	}
	return out.VpcID
}

// newSubnet creates one subnet in a zone and returns its id, so a group's
// VPCZoneIdentifier can name subnets EC2 really holds.
func newSubnet(t *testing.T, srv *helpers.TestServer, vpcID, cidr, zone string) string {
	t.Helper()
	resp := ec2Call(t, srv, "CreateSubnet", map[string]string{
		"VpcId": vpcID, "CidrBlock": cidr, "AvailabilityZone": zone,
	})
	body := xmlText(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateSubnet: %d: %s", resp.StatusCode, body)
	}
	var out struct {
		SubnetID string `xml:"subnet>subnetId"`
	}
	if err := xml.Unmarshal([]byte(body), &out); err != nil || out.SubnetID == "" {
		t.Fatalf("CreateSubnet: cannot read subnetId: %v\n%s", err, body)
	}
	return out.SubnetID
}

// ec2Placement returns one instance's zone and subnet as EC2 itself reports
// them, which is the only place the pair can be seen to agree.
func ec2Placement(t *testing.T, srv *helpers.TestServer, instanceID string) (zone, subnet string) {
	t.Helper()
	resp := ec2Call(t, srv, "DescribeInstances", map[string]string{"InstanceId.1": instanceID})
	body := xmlText(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeInstances: %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Instances []struct {
			Placement struct {
				AvailabilityZone string `xml:"availabilityZone"`
			} `xml:"placement"`
			SubnetID string `xml:"subnetId"`
		} `xml:"reservationSet>item>instancesSet>item"`
	}
	if err := xml.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("DescribeInstances: unmarshal: %v\n%s", err, body)
	}
	if len(out.Instances) != 1 {
		t.Fatalf("DescribeInstances returned %d instances for %s: %s", len(out.Instances), instanceID, body)
	}
	return out.Instances[0].Placement.AvailabilityZone, out.Instances[0].SubnetID
}

// newLaunchConfig creates a launch configuration the reconciler can launch from.
func newLaunchConfig(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": name,
		"ImageId":                 "ami-0123456789abcdef0",
		"InstanceType":            "t3.micro",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateLaunchConfiguration: %d: %s", resp.StatusCode, xmlText(t, resp))
	}
	resp.Body.Close()
}

// ─── Placement ────────────────────────────────────────────────────────────────

// TestCreateAutoScalingGroup_launchesIntoTheSubnetsOwnZone is the other half of
// #1840: a group whose subnets and zones correspond launches each instance into
// the zone of the subnet it actually landed in, so an instance's placement can
// never contradict its own subnetId.
//
// The two lists are deliberately in opposite orders. They are a valid pair —
// the same set of zones either way, which is all AWS requires — but a
// reconciler that round-robins them independently by the same index pairs
// subnet i with zone i and hands EC2 a contradiction on the very first launch.
func TestCreateAutoScalingGroup_launchesIntoTheSubnetsOwnZone(t *testing.T) {
	// Given: one subnet per zone, listed in the reverse of the zone order
	srv := helpers.NewTestServer(t)
	newLaunchConfig(t, srv, "subnet-spread-lc")
	vpc := newVPC(t, srv, "10.0.0.0/16")
	subnetA := newSubnet(t, srv, vpc, "10.0.1.0/24", "us-east-1a")
	subnetB := newSubnet(t, srv, vpc, "10.0.2.0/24", "us-east-1b")
	zoneOf := map[string]string{subnetA: "us-east-1a", subnetB: "us-east-1b"}

	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "subnet-spread-asg",
		"LaunchConfigurationName":    "subnet-spread-lc",
		"MinSize":                    "2",
		"MaxSize":                    "4",
		"DesiredCapacity":            "2",
		"AvailabilityZones.member.1": "us-east-1a",
		"AvailabilityZones.member.2": "us-east-1b",
		"VPCZoneIdentifier":          subnetB + "," + subnetA,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When: the reconciler converges the group
	instances := asGroupInstances(t, srv, "subnet-spread-asg", func(in []asgInstanceXML) bool {
		return inServiceCount(in) == 2
	})

	// Then: every instance's EC2 placement is its own subnet's zone, and Auto
	// Scaling recorded the same zone
	seen := map[string]int{}
	for _, inst := range instances {
		zone, subnet := ec2Placement(t, srv, inst.InstanceID)
		want, known := zoneOf[subnet]
		if !known {
			t.Errorf("%s launched into %q, which is not one of the group's subnets", inst.InstanceID, subnet)
			continue
		}
		if zone != want {
			t.Errorf("%s: EC2 zone = %q, but its subnet %s is in %q", inst.InstanceID, zone, subnet, want)
		}
		if inst.AvailabilityZone != zone {
			t.Errorf("%s: Auto Scaling recorded zone %q, EC2 reports %q", inst.InstanceID, inst.AvailabilityZone, zone)
		}
		seen[zone]++
	}
	if len(seen) != 2 {
		t.Errorf("expected the two instances spread across both zones, got %+v", seen)
	}
}
