// Package autoscaling_test contains integration tests for the Auto Scaling emulator.
//
// Run: go test ./tests/integration/autoscaling/...
package autoscaling_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// asCall issues an AWS Auto Scaling Query-protocol request.
func asCall(t *testing.T, srv *helpers.TestServer, action string, params map[string]string) *http.Response {
	t.Helper()
	form := url.Values{}
	form.Set("Action", action)
	form.Set("Version", "2011-01-01")
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("asCall build request %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("asCall %s: %v", action, err)
	}
	return resp
}

func xmlText(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// ─── Launch Configurations ────────────────────────────────────────────────────

func TestCreateLaunchConfiguration_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))

	// When: CreateLaunchConfiguration is called
	resp := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestDescribeLaunchConfigurations_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))

	// When: DescribeLaunchConfigurations is called
	resp := asCall(t, srv, "DescribeLaunchConfigurations", nil)
	body := xmlText(t, resp)

	// Then: 200 with empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "DescribeLaunchConfigurationsResult") {
		t.Errorf("expected DescribeLaunchConfigurationsResult in body, got: %s", body)
	}
}

func TestDescribeLaunchConfigurations_afterCreate(t *testing.T) {
	// Given: a launch configuration exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()

	// When: DescribeLaunchConfigurations is called
	resp := asCall(t, srv, "DescribeLaunchConfigurations", nil)
	body := xmlText(t, resp)

	// Then: 200 with the launch configuration present
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "my-lc") {
		t.Errorf("expected my-lc in body, got: %s", body)
	}
	if !strings.Contains(body, "ami-12345678") {
		t.Errorf("expected ami-12345678 in body, got: %s", body)
	}
}

func TestDeleteLaunchConfiguration_success(t *testing.T) {
	// Given: a launch configuration exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()

	// When: DeleteLaunchConfiguration is called
	resp := asCall(t, srv, "DeleteLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: it is no longer listed
	resp2 := asCall(t, srv, "DescribeLaunchConfigurations", nil)
	body := xmlText(t, resp2)
	if strings.Contains(body, "my-lc") {
		t.Errorf("expected my-lc to be deleted, but still present in body: %s", body)
	}
}

// ─── Auto Scaling Groups ──────────────────────────────────────────────────────

func TestCreateAutoScalingGroup_success(t *testing.T) {
	// Given: a launch configuration exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()

	// When: CreateAutoScalingGroup is called
	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "3",
		"DesiredCapacity":            "2",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestDescribeAutoScalingGroups_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))

	// When: DescribeAutoScalingGroups is called
	resp := asCall(t, srv, "DescribeAutoScalingGroups", nil)
	body := xmlText(t, resp)

	// Then: 200 with empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "DescribeAutoScalingGroupsResult") {
		t.Errorf("expected DescribeAutoScalingGroupsResult in body, got: %s", body)
	}
}

func TestDescribeAutoScalingGroups_afterCreate(t *testing.T) {
	// Given: an ASG exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "3",
		"DesiredCapacity":            "2",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: DescribeAutoScalingGroups is called
	resp := asCall(t, srv, "DescribeAutoScalingGroups", nil)
	body := xmlText(t, resp)

	// Then: the ASG is present
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "my-asg") {
		t.Errorf("expected my-asg in body, got: %s", body)
	}
}

func TestUpdateAutoScalingGroup_desiredCapacity(t *testing.T) {
	// Given: an ASG with desired=2
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "5",
		"DesiredCapacity":            "2",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: UpdateAutoScalingGroup changes desired to 4
	resp := asCall(t, srv, "UpdateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName": "my-asg",
		"DesiredCapacity":      "4",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeAutoScalingGroups reflects the new desired capacity
	resp2 := asCall(t, srv, "DescribeAutoScalingGroups", nil)
	body := xmlText(t, resp2)
	if !strings.Contains(body, "<DesiredCapacity>4</DesiredCapacity>") {
		t.Errorf("expected DesiredCapacity=4 in body, got: %s", body)
	}
}

func TestDeleteAutoScalingGroup_success(t *testing.T) {
	// Given: an ASG exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "0",
		"MaxSize":                    "1",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: DeleteAutoScalingGroup is called with ForceDelete=true
	resp := asCall(t, srv, "DeleteAutoScalingGroup", map[string]string{
		"AutoScalingGroupName": "my-asg",
		"ForceDelete":          "true",
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: it is no longer listed
	resp2 := asCall(t, srv, "DescribeAutoScalingGroups", nil)
	body := xmlText(t, resp2)
	if strings.Contains(body, "my-asg") {
		t.Errorf("expected my-asg to be deleted, but still present: %s", body)
	}
}

// ─── Scaling Policies ─────────────────────────────────────────────────────────

func TestPutScalingPolicy_success(t *testing.T) {
	// Given: an ASG exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "5",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: PutScalingPolicy is called
	resp := asCall(t, srv, "PutScalingPolicy", map[string]string{
		"AutoScalingGroupName": "my-asg",
		"PolicyName":           "scale-up",
		"PolicyType":           "SimpleScaling",
		"AdjustmentType":       "ChangeInCapacity",
		"ScalingAdjustment":    "1",
	})
	body := xmlText(t, resp)

	// Then: 200 with a PolicyARN
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "PolicyARN") {
		t.Errorf("expected PolicyARN in body, got: %s", body)
	}
}

func TestDescribePolicies_afterPut(t *testing.T) {
	// Given: a scaling policy exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "5",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()
	r3 := asCall(t, srv, "PutScalingPolicy", map[string]string{
		"AutoScalingGroupName": "my-asg",
		"PolicyName":           "scale-up",
		"PolicyType":           "SimpleScaling",
		"AdjustmentType":       "ChangeInCapacity",
		"ScalingAdjustment":    "1",
	})
	r3.Body.Close()

	// When: DescribePolicies is called
	resp := asCall(t, srv, "DescribePolicies", nil)
	body := xmlText(t, resp)

	// Then: the policy is present
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "scale-up") {
		t.Errorf("expected scale-up in body, got: %s", body)
	}
}

// ─── Lifecycle Hooks ──────────────────────────────────────────────────────────

func TestPutLifecycleHook_success(t *testing.T) {
	// Given: an ASG exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "3",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: PutLifecycleHook is called
	resp := asCall(t, srv, "PutLifecycleHook", map[string]string{
		"AutoScalingGroupName": "my-asg",
		"LifecycleHookName":    "my-hook",
		"LifecycleTransition":  "autoscaling:EC2_INSTANCE_LAUNCHING",
		"DefaultResult":        "CONTINUE",
		"HeartbeatTimeout":     "300",
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestDescribeLifecycleHooks_afterPut(t *testing.T) {
	// Given: a lifecycle hook exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "3",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()
	r3 := asCall(t, srv, "PutLifecycleHook", map[string]string{
		"AutoScalingGroupName": "my-asg",
		"LifecycleHookName":    "my-hook",
		"LifecycleTransition":  "autoscaling:EC2_INSTANCE_LAUNCHING",
		"DefaultResult":        "CONTINUE",
	})
	r3.Body.Close()

	// When: DescribeLifecycleHooks is called
	resp := asCall(t, srv, "DescribeLifecycleHooks", map[string]string{
		"AutoScalingGroupName": "my-asg",
	})
	body := xmlText(t, resp)

	// Then: the hook is present
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "my-hook") {
		t.Errorf("expected my-hook in body, got: %s", body)
	}
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

func TestCreateOrUpdateTags_success(t *testing.T) {
	// Given: an ASG exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "3",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: CreateOrUpdateTags is called
	resp := asCall(t, srv, "CreateOrUpdateTags", map[string]string{
		"Tags.member.1.ResourceId":        "my-asg",
		"Tags.member.1.ResourceType":      "auto-scaling-group",
		"Tags.member.1.Key":               "Environment",
		"Tags.member.1.Value":             "test",
		"Tags.member.1.PropagateAtLaunch": "true",
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestDescribeTags_afterCreate(t *testing.T) {
	// Given: tags exist on an ASG
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "3",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()
	r3 := asCall(t, srv, "CreateOrUpdateTags", map[string]string{
		"Tags.member.1.ResourceId":        "my-asg",
		"Tags.member.1.ResourceType":      "auto-scaling-group",
		"Tags.member.1.Key":               "Environment",
		"Tags.member.1.Value":             "test",
		"Tags.member.1.PropagateAtLaunch": "true",
	})
	r3.Body.Close()

	// When: DescribeTags is called
	resp := asCall(t, srv, "DescribeTags", nil)
	body := xmlText(t, resp)

	// Then: the tag is present
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "Environment") {
		t.Errorf("expected Environment tag in body, got: %s", body)
	}
}

// ─── SetDesiredCapacity ───────────────────────────────────────────────────────

func TestSetDesiredCapacity_success(t *testing.T) {
	// Given: an ASG with desired=1
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "0",
		"MaxSize":                    "5",
		"DesiredCapacity":            "1",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: SetDesiredCapacity is called with 3
	resp := asCall(t, srv, "SetDesiredCapacity", map[string]string{
		"AutoScalingGroupName": "my-asg",
		"DesiredCapacity":      "3",
	})
	resp.Body.Close()

	// Then: 200 and DescribeAutoScalingGroups reflects desired=3
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp2 := asCall(t, srv, "DescribeAutoScalingGroups", nil)
	body := xmlText(t, resp2)
	if !strings.Contains(body, "<DesiredCapacity>3</DesiredCapacity>") {
		t.Errorf("expected DesiredCapacity=3 in body, got: %s", body)
	}
}

// ─── DescribeAutoScalingInstances ─────────────────────────────────────────────

func TestDescribeAutoScalingInstances_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))

	// When: DescribeAutoScalingInstances is called
	resp := asCall(t, srv, "DescribeAutoScalingInstances", nil)
	body := xmlText(t, resp)

	// Then: 200 with empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
	if !strings.Contains(body, "DescribeAutoScalingInstancesResult") {
		t.Errorf("expected DescribeAutoScalingInstancesResult in body, got: %s", body)
	}
}

// ─── Invalid XML verifier ─────────────────────────────────────────────────────

func TestXMLResponseIsWellFormed(t *testing.T) {
	// Given: an ASG exists
	srv := helpers.NewTestServer(t, helpers.WithServices("autoscaling"))
	r := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": "my-lc",
		"ImageId":                 "ami-12345678",
		"InstanceType":            "t3.micro",
	})
	r.Body.Close()
	r2 := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "my-asg",
		"LaunchConfigurationName":    "my-lc",
		"MinSize":                    "1",
		"MaxSize":                    "3",
		"DesiredCapacity":            "2",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	r2.Body.Close()

	// When: DescribeAutoScalingGroups response is parsed as XML
	resp := asCall(t, srv, "DescribeAutoScalingGroups", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Then: no XML parse error
	if err := xml.Unmarshal(body, new(struct{ XMLName xml.Name })); err != nil {
		t.Errorf("XML response is not well-formed: %v\nbody: %s", err, body)
	}
}
