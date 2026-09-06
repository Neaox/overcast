package autoscaling_test

// Zone and subnet handling (#1840). A group's AvailabilityZones and its
// VPCZoneIdentifier subnets have to agree, because the reconciler launches into
// one of those subnets and EC2 places the instance in that subnet's own zone
// (#1839). These tests pin the validation that refuses a group whose two lists
// contradict each other, the derivation that fills the zones in from the
// subnets when only subnets are given, and the placement each launch ends up
// with.

import (
	"encoding/xml"
	"net/http"
	"strings"
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

// asgZones reads back one group's AvailabilityZones.
func asgZones(t *testing.T, srv *helpers.TestServer, group string) []string {
	t.Helper()
	var out struct {
		XMLName xml.Name `xml:"DescribeAutoScalingGroupsResponse"`
		Groups  []struct {
			Zones []string `xml:"AvailabilityZones>member"`
		} `xml:"DescribeAutoScalingGroupsResult>AutoScalingGroups>member"`
	}
	asDecode(t, srv, "DescribeAutoScalingGroups", map[string]string{
		"AutoScalingGroupNames.member.1": group,
	}, &out)
	if len(out.Groups) != 1 {
		t.Fatalf("DescribeAutoScalingGroups returned %d groups, want 1", len(out.Groups))
	}
	return out.Groups[0].Zones
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

// ─── Validation ───────────────────────────────────────────────────────────────

// TestCreateAutoScalingGroup_rejectsSubnetOutsideAvailabilityZones pins the
// rule the CreateAutoScalingGroup reference states for VPCZoneIdentifier: "If
// you specify VPCZoneIdentifier with AvailabilityZones, the subnets that you
// specify must reside in those Availability Zones." Without it the reconciler
// round-robins the two lists independently, and a group whose lists disagree
// has its launches refused one at a time by EC2 (#1839) instead of the group
// being refused where AWS refuses it.
func TestCreateAutoScalingGroup_rejectsSubnetOutsideAvailabilityZones(t *testing.T) {
	// Given: a subnet in us-east-1b
	srv := helpers.NewTestServer(t)
	newLaunchConfig(t, srv, "mismatch-lc")
	vpc := newVPC(t, srv, "10.0.0.0/16")
	subnetB := newSubnet(t, srv, vpc, "10.0.1.0/24", "us-east-1b")

	// When: a group names that subnet but only us-east-1a as its zone
	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "mismatch-asg",
		"LaunchConfigurationName":    "mismatch-lc",
		"MinSize":                    "1",
		"MaxSize":                    "2",
		"DesiredCapacity":            "1",
		"AvailabilityZones.member.1": "us-east-1a",
		"VPCZoneIdentifier":          subnetB,
	})
	body := xmlText(t, resp)

	// Then: AWS's ValidationError comes back and nothing is stored
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "ValidationError") {
		t.Errorf("body does not name ValidationError: %s", body)
	}
	if !strings.Contains(body, "The availability zones of the specified subnets and the AutoScalingGroup do not match") {
		t.Errorf("body does not carry AWS's message: %s", body)
	}

	var groups describeGroupsXML
	asDecode(t, srv, "DescribeAutoScalingGroups", nil, &groups)
	for _, g := range groups.Groups {
		if g.Name == "mismatch-asg" {
			t.Error("the refused group was stored anyway")
		}
	}
}

// TestUpdateAutoScalingGroup_rejectsSubnetOutsideAvailabilityZones pins the
// same rule on update, which UpdateAutoScalingGroup's reference states in the
// same words.
func TestUpdateAutoScalingGroup_rejectsSubnetOutsideAvailabilityZones(t *testing.T) {
	// Given: a group whose zone and subnet agree
	srv := helpers.NewTestServer(t)
	newLaunchConfig(t, srv, "update-mismatch-lc")
	vpc := newVPC(t, srv, "10.0.0.0/16")
	subnetA := newSubnet(t, srv, vpc, "10.0.1.0/24", "us-east-1a")
	subnetB := newSubnet(t, srv, vpc, "10.0.2.0/24", "us-east-1b")

	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "update-mismatch-asg",
		"LaunchConfigurationName":    "update-mismatch-lc",
		"MinSize":                    "0",
		"MaxSize":                    "2",
		"DesiredCapacity":            "0",
		"AvailabilityZones.member.1": "us-east-1a",
		"VPCZoneIdentifier":          subnetA,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When: an update moves the subnet into a zone the group does not list
	resp = asCall(t, srv, "UpdateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "update-mismatch-asg",
		"AvailabilityZones.member.1": "us-east-1a",
		"VPCZoneIdentifier":          subnetB,
	})
	body := xmlText(t, resp)

	// Then: the same ValidationError comes back and the group is unchanged
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "The availability zones of the specified subnets and the AutoScalingGroup do not match") {
		t.Errorf("body does not carry AWS's message: %s", body)
	}
	if zones := asgZones(t, srv, "update-mismatch-asg"); len(zones) != 1 || zones[0] != "us-east-1a" {
		t.Errorf("zones = %v after a refused update, want [us-east-1a]", zones)
	}
}

// TestUpdateAutoScalingGroup_rejectsZonesOutsideTheStoredSubnets covers the
// other direction: the request changes only the zones, and they have to agree
// with the subnets the group already has.
func TestUpdateAutoScalingGroup_rejectsZonesOutsideTheStoredSubnets(t *testing.T) {
	// Given: a group pinned to a subnet in us-east-1a
	srv := helpers.NewTestServer(t)
	newLaunchConfig(t, srv, "update-zones-lc")
	vpc := newVPC(t, srv, "10.0.0.0/16")
	subnetA := newSubnet(t, srv, vpc, "10.0.1.0/24", "us-east-1a")

	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":    "update-zones-asg",
		"LaunchConfigurationName": "update-zones-lc",
		"MinSize":                 "0",
		"MaxSize":                 "2",
		"DesiredCapacity":         "0",
		"VPCZoneIdentifier":       subnetA,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When: an update names a zone the stored subnet is not in
	resp = asCall(t, srv, "UpdateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "update-zones-asg",
		"AvailabilityZones.member.1": "us-east-1b",
	})
	body := xmlText(t, resp)

	// Then: it is refused
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "The availability zones of the specified subnets and the AutoScalingGroup do not match") {
		t.Errorf("body does not carry AWS's message: %s", body)
	}
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

// TestCreateAutoScalingGroup_derivesZonesFromVPCZoneIdentifier pins the shape
// CDK emits: subnets and no AvailabilityZones at all. AWS derives the zones
// from the subnets, so DescribeAutoScalingGroups reports them and the group
// still spreads.
func TestCreateAutoScalingGroup_derivesZonesFromVPCZoneIdentifier(t *testing.T) {
	// Given: two subnets in two zones
	srv := helpers.NewTestServer(t)
	newLaunchConfig(t, srv, "derived-lc")
	vpc := newVPC(t, srv, "10.0.0.0/16")
	subnetA := newSubnet(t, srv, vpc, "10.0.1.0/24", "us-east-1a")
	subnetC := newSubnet(t, srv, vpc, "10.0.3.0/24", "us-east-1c")

	// When: a group names only the subnets
	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":    "derived-asg",
		"LaunchConfigurationName": "derived-lc",
		"MinSize":                 "2",
		"MaxSize":                 "4",
		"DesiredCapacity":         "2",
		"VPCZoneIdentifier":       subnetA + "," + subnetC,
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the group reports the subnets' own zones
	zones := asgZones(t, srv, "derived-asg")
	if len(zones) != 2 || zones[0] != "us-east-1a" || zones[1] != "us-east-1c" {
		t.Errorf("AvailabilityZones = %v, want [us-east-1a us-east-1c] derived from the subnets", zones)
	}

	// And: the instances really land in them
	instances := asGroupInstances(t, srv, "derived-asg", func(in []asgInstanceXML) bool {
		return inServiceCount(in) == 2
	})
	seen := map[string]int{}
	for _, inst := range instances {
		zone, _ := ec2Placement(t, srv, inst.InstanceID)
		seen[zone]++
	}
	if seen["us-east-1a"] != 1 || seen["us-east-1c"] != 1 {
		t.Errorf("instances placed %+v, want one in each of us-east-1a and us-east-1c", seen)
	}
}

// ─── At least one of the two ──────────────────────────────────────────────────

// TestCreateAutoScalingGroup_requiresZonesOrSubnets pins #1843: a group naming
// neither AvailabilityZones nor VPCZoneIdentifier has nowhere to launch, and
// real Auto Scaling refuses it rather than storing it. Accepted without the
// refusal, the reconciler placed such a group nowhere in particular —
// nextPlacement returns an empty subnet and an empty zone — and EC2's
// RunInstances fell back to <region>a, a shape AWS cannot produce.
//
// Neither parameter is marked Required in the CreateAutoScalingGroup
// reference, and its Errors section names no code for the combination, so the
// code is the Auto Scaling common-error one for input that "doesn't meet the
// required format or constraints": ValidationError, HTTP 400. The message is
// the one real Auto Scaling returns, reported verbatim from the Ruby SDK as
// "AWS::AutoScaling::Errors::ValidationError: At least one Availability Zone
// or VPC Subnet is required." (chef-boneyard/chef-provisioning-aws#135); moto
// raises the same string in _set_azs_and_vpcs (moto/autoscaling/models.py).
//
// Either parameter alone is enough, which is what the two accepting subtests
// hold: AvailabilityZones is documented as the way to launch "into the default
// VPC subnet in each Availability Zone when not using the VPCZoneIdentifier
// property", and CloudFormation types VPCZoneIdentifier "Required: Conditional
// — Required to launch instances into a nondefault VPC".
func TestCreateAutoScalingGroup_requiresZonesOrSubnets(t *testing.T) {
	// Given: a launch configuration and one real subnet to name
	srv := helpers.NewTestServer(t)
	newLaunchConfig(t, srv, "placement-lc")
	vpc := newVPC(t, srv, "10.0.0.0/16")
	subnetA := newSubnet(t, srv, vpc, "10.0.1.0/24", "us-east-1a")

	cases := []struct {
		name       string
		group      string
		placement  map[string]string
		wantStatus int
	}{
		{
			name:       "neither is refused",
			group:      "no-placement-asg",
			placement:  nil,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "availability zones alone are enough",
			group:      "zones-only-asg",
			placement:  map[string]string{"AvailabilityZones.member.1": "us-east-1a"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "subnets alone are enough",
			group:      "subnets-only-asg",
			placement:  map[string]string{"VPCZoneIdentifier": subnetA},
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the group is created with that placement
			params := map[string]string{
				"AutoScalingGroupName":    tc.group,
				"LaunchConfigurationName": "placement-lc",
				"MinSize":                 "0",
				"MaxSize":                 "2",
				"DesiredCapacity":         "0",
			}
			for k, v := range tc.placement {
				params[k] = v
			}
			resp := asCall(t, srv, "CreateAutoScalingGroup", params)
			body := xmlText(t, resp)

			// Then: it is refused with AWS's error, or accepted
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.wantStatus, body)
			}
			if tc.wantStatus == http.StatusBadRequest {
				if !strings.Contains(body, "ValidationError") {
					t.Errorf("body does not name ValidationError: %s", body)
				}
				if !strings.Contains(body, "At least one Availability Zone or VPC Subnet is required.") {
					t.Errorf("body does not carry AWS's message: %s", body)
				}
			}

			// And: only an accepted group is stored
			var groups describeGroupsXML
			asDecode(t, srv, "DescribeAutoScalingGroups", nil, &groups)
			stored := false
			for _, g := range groups.Groups {
				if g.Name == tc.group {
					stored = true
				}
			}
			if want := tc.wantStatus == http.StatusOK; stored != want {
				t.Errorf("group stored = %v, want %v", stored, want)
			}
		})
	}
}

// TestCreateAutoScalingGroup_whitespaceOnlyVPCZoneIdentifierIsNoPlacement
// covers the boundary the comma-separated form creates: VPCZoneIdentifier is
// split on commas and each entry trimmed (subnetIDs), so a value that is only
// separators and spaces names no subnet at all and is not a placement.
func TestCreateAutoScalingGroup_whitespaceOnlyVPCZoneIdentifierIsNoPlacement(t *testing.T) {
	// Given: a launch configuration
	srv := helpers.NewTestServer(t)
	newLaunchConfig(t, srv, "blank-subnets-lc")

	// When: a group's only placement is a VPCZoneIdentifier naming no subnet
	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":    "blank-subnets-asg",
		"LaunchConfigurationName": "blank-subnets-lc",
		"MinSize":                 "0",
		"MaxSize":                 "2",
		"VPCZoneIdentifier":       " , ",
	})
	body := xmlText(t, resp)

	// Then: it is refused like a group with no placement at all
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "At least one Availability Zone or VPC Subnet is required.") {
		t.Errorf("body does not carry AWS's message: %s", body)
	}
}
