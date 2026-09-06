package autoscaling

// Placement validation (#1843): a group has to name at least one Availability
// Zone or at least one VPC subnet, on create and after every update.

import (
	"net/http"
	"testing"
)

func TestValidatePlacement_requiresZonesOrSubnets(t *testing.T) {
	cases := []struct {
		name    string
		zones   []string
		subnets string
		wantErr bool
	}{
		{name: "neither", wantErr: true},
		{name: "an empty subnet list is no placement", subnets: " , ", wantErr: true},
		{name: "zones alone", zones: []string{"us-east-1a"}},
		{name: "subnets alone", subnets: "subnet-1"},
		{name: "both", zones: []string{"us-east-1a"}, subnets: "subnet-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aerr := validatePlacement(tc.zones, tc.subnets)
			if !tc.wantErr {
				if aerr != nil {
					t.Fatalf("validatePlacement(%v, %q) = %v, want nil", tc.zones, tc.subnets, aerr)
				}
				return
			}
			if aerr == nil {
				t.Fatalf("validatePlacement(%v, %q) = nil, want AWS's ValidationError", tc.zones, tc.subnets)
			}
			if aerr.Code != "ValidationError" || aerr.HTTPStatus != http.StatusBadRequest {
				t.Errorf("error = %s/%d, want ValidationError/400", aerr.Code, aerr.HTTPStatus)
			}
			if want := "At least one Availability Zone or VPC Subnet is required."; aerr.Message != want {
				t.Errorf("message = %q, want %q", aerr.Message, want)
			}
		})
	}
}

// TestUpdateASGTyped_refusesLeavingAGroupWithNoPlacement covers the one shape
// that can still reach UpdateAutoScalingGroup holding neither: a group
// persisted by an Overcast from before this validation existed, which
// CreateAutoScalingGroup would refuse today. The update is checked against the
// group as it would be left, not against the request, so an update that says
// nothing about placement is refused on such a group while an update that
// supplies one heals it.
func TestUpdateASGTyped_refusesLeavingAGroupWithNoPlacement(t *testing.T) {
	// Given: a stored group naming neither zones nor subnets
	f := newFixture(t)
	if _, aerr := f.svc.handler.createLaunchConfigTyped(f.ctx, &createLaunchConfigReq{
		LaunchConfigurationName: "legacy-lc", ImageId: "ami-1", InstanceType: "t3.micro",
	}); aerr != nil {
		t.Fatalf("CreateLaunchConfiguration: %v", aerr)
	}
	if err := f.svc.st.putGroup(f.ctx, &AutoScalingGroup{
		AutoScalingGroupName:    "legacy-asg",
		LaunchConfigurationName: "legacy-lc",
		MinSize:                 0,
		MaxSize:                 4,
		DesiredCapacity:         0,
	}); err != nil {
		t.Fatalf("putGroup: %v", err)
	}

	// When: an update touches only the capacity
	desired := 2
	_, aerr := f.svc.handler.updateASGTyped(f.ctx, &updateASGReq{
		AutoScalingGroupName: "legacy-asg",
		DesiredCapacity:      &desired,
	})

	// Then: it is refused with AWS's error and the group is left alone
	if aerr == nil {
		t.Fatal("update left the group with no placement, want AWS's ValidationError")
	}
	if aerr.Code != "ValidationError" || aerr.HTTPStatus != http.StatusBadRequest {
		t.Errorf("error = %s/%d, want ValidationError/400", aerr.Code, aerr.HTTPStatus)
	}
	if want := "At least one Availability Zone or VPC Subnet is required."; aerr.Message != want {
		t.Errorf("message = %q, want %q", aerr.Message, want)
	}
	stored, found := f.svc.st.getGroup(f.ctx, "legacy-asg")
	if !found {
		t.Fatal("group vanished")
	}
	if stored.DesiredCapacity != 0 {
		t.Errorf("DesiredCapacity = %d after a refused update, want 0", stored.DesiredCapacity)
	}

	// And: the same update carrying a placement is accepted and heals the group
	if _, aerr := f.svc.handler.updateASGTyped(f.ctx, &updateASGReq{
		AutoScalingGroupName: "legacy-asg",
		DesiredCapacity:      &desired,
		AvailabilityZones:    []string{"us-east-1a"},
	}); aerr != nil {
		t.Fatalf("update supplying zones: %v", aerr)
	}
	stored, _ = f.svc.st.getGroup(f.ctx, "legacy-asg")
	if len(stored.AvailabilityZones) != 1 || stored.AvailabilityZones[0] != "us-east-1a" {
		t.Errorf("AvailabilityZones = %v, want [us-east-1a]", stored.AvailabilityZones)
	}
	if stored.DesiredCapacity != 2 {
		t.Errorf("DesiredCapacity = %d, want 2", stored.DesiredCapacity)
	}
}

// TestUpdateASGTyped_leavesAPlacedGroupAlone is the other side: an update that
// says nothing about placement is the ordinary case and must stay accepted.
func TestUpdateASGTyped_leavesAPlacedGroupAlone(t *testing.T) {
	// Given: a group created with zones
	f := newFixture(t)
	f.group(t, "placed-asg", 0, 4, 0)

	// When: an update touches only the capacity
	desired := 2
	if _, aerr := f.svc.handler.updateASGTyped(f.ctx, &updateASGReq{
		AutoScalingGroupName: "placed-asg",
		DesiredCapacity:      &desired,
	}); aerr != nil {
		t.Fatalf("UpdateAutoScalingGroup: %v", aerr)
	}

	// Then: it is applied and the zones are untouched
	stored, found := f.svc.st.getGroup(f.ctx, "placed-asg")
	if !found {
		t.Fatal("group vanished")
	}
	if stored.DesiredCapacity != 2 {
		t.Errorf("DesiredCapacity = %d, want 2", stored.DesiredCapacity)
	}
	if len(stored.AvailabilityZones) != 2 {
		t.Errorf("AvailabilityZones = %v, want the two the group was created with", stored.AvailabilityZones)
	}
}
