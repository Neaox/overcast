// Package autoscaling_test — reconciliation, lifecycle and scaling-policy tests.
//
// These cover the wire contract of a group that actually converges: what
// DescribeAutoScalingGroups/DescribeAutoScalingInstances report once the
// reconciler has run, which configurations are refused rather than stored and
// ignored (docs/plans/full-emulation-priority.md §2.1), and what
// DescribeScalingActivities records. The reconciler's own arithmetic is
// unit-tested against a mock clock in internal/services/autoscaling.
package autoscaling_test

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── XML shapes ───────────────────────────────────────────────────────────────

type asgInstanceXML struct {
	InstanceID              string `xml:"InstanceId"`
	InstanceType            string `xml:"InstanceType"`
	AvailabilityZone        string `xml:"AvailabilityZone"`
	LifecycleState          string `xml:"LifecycleState"`
	HealthStatus            string `xml:"HealthStatus"`
	LaunchConfigurationName string `xml:"LaunchConfigurationName"`
	ProtectedFromScaleIn    bool   `xml:"ProtectedFromScaleIn"`
}

type describeGroupsXML struct {
	XMLName xml.Name `xml:"DescribeAutoScalingGroupsResponse"`
	Groups  []struct {
		Name            string           `xml:"AutoScalingGroupName"`
		MinSize         int              `xml:"MinSize"`
		MaxSize         int              `xml:"MaxSize"`
		DesiredCapacity int              `xml:"DesiredCapacity"`
		Instances       []asgInstanceXML `xml:"Instances>member"`
	} `xml:"DescribeAutoScalingGroupsResult>AutoScalingGroups>member"`
}

type describeInstancesXML struct {
	XMLName   xml.Name         `xml:"DescribeAutoScalingInstancesResponse"`
	Instances []asgInstanceXML `xml:"DescribeAutoScalingInstancesResult>AutoScalingInstances>member"`
}

type describeActivitiesXML struct {
	XMLName    xml.Name `xml:"DescribeScalingActivitiesResponse"`
	Activities []struct {
		ActivityID  string `xml:"ActivityId"`
		GroupName   string `xml:"AutoScalingGroupName"`
		Description string `xml:"Description"`
		Cause       string `xml:"Cause"`
		StatusCode  string `xml:"StatusCode"`
		Progress    int    `xml:"Progress"`
	} `xml:"DescribeScalingActivitiesResult>Activities>member"`
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// asDecode issues a Query call and unmarshals the XML body into out.
func asDecode(t *testing.T, srv *helpers.TestServer, action string, params map[string]string, out any) {
	t.Helper()
	resp := asCall(t, srv, action, params)
	body := xmlText(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d: %s", action, resp.StatusCode, body)
	}
	if err := xml.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("%s: unmarshal: %v\n%s", action, err, body)
	}
}

// ec2Call issues an EC2 Query-protocol request against the same server, so a
// test can confirm the instances Auto Scaling reports really exist in EC2.
func ec2Call(t *testing.T, srv *helpers.TestServer, action string, params map[string]string) *http.Response {
	t.Helper()
	form := url.Values{}
	form.Set("Action", action)
	form.Set("Version", "2016-11-15")
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("ec2Call build request %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ec2Call %s: %v", action, err)
	}
	return resp
}

// asGroupInstances polls DescribeAutoScalingGroups until pred is satisfied.
// The reconciler is a background loop, so a wire-level test observes its
// effect rather than driving it.
func asGroupInstances(t *testing.T, srv *helpers.TestServer, group string, pred func([]asgInstanceXML) bool) []asgInstanceXML {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last []asgInstanceXML
	for time.Now().Before(deadline) {
		var out describeGroupsXML
		asDecode(t, srv, "DescribeAutoScalingGroups", map[string]string{
			"AutoScalingGroupNames.member.1": group,
		}, &out)
		if len(out.Groups) == 1 {
			last = out.Groups[0].Instances
			if pred(last) {
				return last
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("group %q never reached the expected instance set; last was %+v", group, last)
	return nil
}

func inServiceCount(instances []asgInstanceXML) int {
	n := 0
	for _, i := range instances {
		if i.LifecycleState == "InService" {
			n++
		}
	}
	return n
}

// newGroup creates a launch configuration and a group referencing it.
func newGroup(t *testing.T, srv *helpers.TestServer, name string, min, max, desired int) {
	t.Helper()
	lc := name + "-lc"
	resp := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": lc,
		"ImageId":                 "ami-0123456789abcdef0",
		"InstanceType":            "t3.micro",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateLaunchConfiguration: %d: %s", resp.StatusCode, xmlText(t, resp))
	}
	resp.Body.Close()

	resp = asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       name,
		"LaunchConfigurationName":    lc,
		"MinSize":                    itoa(min),
		"MaxSize":                    itoa(max),
		"DesiredCapacity":            itoa(desired),
		"AvailabilityZones.member.1": "us-east-1a",
		"AvailabilityZones.member.2": "us-east-1b",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateAutoScalingGroup: %d: %s", resp.StatusCode, xmlText(t, resp))
	}
	resp.Body.Close()
}

func itoa(n int) string { return strconv.Itoa(n) }

// ─── Reconciliation ───────────────────────────────────────────────────────────

// TestCreateAutoScalingGroup_launchesInstancesToDesiredCapacity is the core of
// #474: a group with DesiredCapacity 2 ends up owning two real EC2 instances,
// reported through DescribeAutoScalingGroups with AWS's per-instance fields.
func TestCreateAutoScalingGroup_launchesInstancesToDesiredCapacity(t *testing.T) {
	// Given: a launch configuration and a group asking for two instances
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "web-asg", 1, 4, 2)

	// When: the reconciler converges the group
	instances := asGroupInstances(t, srv, "web-asg", func(in []asgInstanceXML) bool {
		return inServiceCount(in) == 2
	})

	// Then: each instance carries the fields real Auto Scaling reports
	for _, inst := range instances {
		if !strings.HasPrefix(inst.InstanceID, "i-") {
			t.Errorf("InstanceId = %q, want an i-… EC2 instance id", inst.InstanceID)
		}
		if inst.HealthStatus != "Healthy" {
			t.Errorf("HealthStatus = %q, want Healthy", inst.HealthStatus)
		}
		if inst.AvailabilityZone == "" {
			t.Errorf("AvailabilityZone is empty for %s", inst.InstanceID)
		}
		if inst.LaunchConfigurationName != "web-asg-lc" {
			t.Errorf("LaunchConfigurationName = %q, want web-asg-lc", inst.LaunchConfigurationName)
		}
		if inst.InstanceType != "t3.micro" {
			t.Errorf("InstanceType = %q, want t3.micro", inst.InstanceType)
		}
	}

	// And: the instances really exist in EC2
	ec2Resp := ec2Call(t, srv, "DescribeInstances", nil)
	body := xmlText(t, ec2Resp)
	for _, inst := range instances {
		if !strings.Contains(body, inst.InstanceID) {
			t.Errorf("EC2 DescribeInstances does not know about %s", inst.InstanceID)
		}
	}

	// And: DescribeAutoScalingInstances reports the same set
	var out describeInstancesXML
	asDecode(t, srv, "DescribeAutoScalingInstances", nil, &out)
	if len(out.Instances) != 2 {
		t.Errorf("DescribeAutoScalingInstances returned %d instances, want 2", len(out.Instances))
	}
}

// TestCreateAutoScalingGroup_spreadsInstancesAcrossZones is the reproducer for
// #1722: the reconciler round-robins launches across the group's configured
// zones via nextAvailabilityZone, sending each one to EC2 as
// Placement.AvailabilityZone. RunInstances is routed through EC2's typed
// body, which hardcoded the zone instead of honouring that parameter — so
// every instance of a multi-AZ group silently landed in the same zone
// (region+"a") and Auto Scaling's own bookkeeping (this response is what it
// stores as each instance's AvailabilityZone) recorded the wrong zone too.
func TestCreateAutoScalingGroup_spreadsInstancesAcrossZones(t *testing.T) {
	// Given: a launch configuration and a group spanning three zones, asking
	// for one instance per zone
	srv := helpers.NewTestServer(t)
	lc := "spread-asg-lc"
	resp := asCall(t, srv, "CreateLaunchConfiguration", map[string]string{
		"LaunchConfigurationName": lc,
		"ImageId":                 "ami-0123456789abcdef0",
		"InstanceType":            "t3.micro",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "spread-asg",
		"LaunchConfigurationName":    lc,
		"MinSize":                    "1",
		"MaxSize":                    "5",
		"DesiredCapacity":            "3",
		"AvailabilityZones.member.1": "us-east-1a",
		"AvailabilityZones.member.2": "us-east-1b",
		"AvailabilityZones.member.3": "us-east-1c",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When: the reconciler converges the group
	instances := asGroupInstances(t, srv, "spread-asg", func(in []asgInstanceXML) bool {
		return inServiceCount(in) == 3
	})

	// Then: the three instances are spread one-per-zone, not all in one zone
	seen := map[string]int{}
	for _, inst := range instances {
		seen[inst.AvailabilityZone]++
	}
	want := map[string]int{"us-east-1a": 1, "us-east-1b": 1, "us-east-1c": 1}
	for zone, count := range want {
		if seen[zone] != count {
			t.Errorf("zone %s: got %d instances, want %d (full spread: %+v)", zone, seen[zone], count, seen)
		}
	}
	if len(seen) != 3 {
		t.Errorf("expected instances spread across 3 distinct zones, got %+v", seen)
	}

	// And: EC2 itself reports each instance in its assigned zone, since
	// DescribeAutoScalingGroups reflects what the reconciler recorded rather
	// than proving what EC2 actually did with Placement.AvailabilityZone.
	for _, inst := range instances {
		ec2Resp := ec2Call(t, srv, "DescribeInstances", map[string]string{
			"InstanceId.1": inst.InstanceID,
		})
		body := xmlText(t, ec2Resp)
		if !strings.Contains(body, "<availabilityZone>"+inst.AvailabilityZone+"</availabilityZone>") {
			t.Errorf("EC2 DescribeInstances for %s does not report zone %s:\n%s", inst.InstanceID, inst.AvailabilityZone, body)
		}
	}
}

// TestSetDesiredCapacity_scalesInAndOut pins that SetDesiredCapacity actually
// moves the instance count in both directions.
func TestSetDesiredCapacity_scalesInAndOut(t *testing.T) {
	// Given: a converged group of one
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "sdc-asg", 0, 4, 1)
	asGroupInstances(t, srv, "sdc-asg", func(in []asgInstanceXML) bool { return inServiceCount(in) == 1 })

	// When: desired capacity goes up
	resp := asCall(t, srv, "SetDesiredCapacity", map[string]string{
		"AutoScalingGroupName": "sdc-asg",
		"DesiredCapacity":      "3",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the group launches two more
	asGroupInstances(t, srv, "sdc-asg", func(in []asgInstanceXML) bool { return inServiceCount(in) == 3 })

	// When: desired capacity goes down
	resp = asCall(t, srv, "SetDesiredCapacity", map[string]string{
		"AutoScalingGroupName": "sdc-asg",
		"DesiredCapacity":      "1",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the group terminates the excess
	asGroupInstances(t, srv, "sdc-asg", func(in []asgInstanceXML) bool {
		return len(in) == 1 && inServiceCount(in) == 1
	})
}

// TestSetDesiredCapacity_rejectsOutOfRange pins AWS's ValidationError text for
// a desired capacity outside the group's min/max.
func TestSetDesiredCapacity_rejectsOutOfRange(t *testing.T) {
	// Given: a group with max size 2
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "clamp-asg", 1, 2, 1)

	// When: a caller asks for more than the maximum
	resp := asCall(t, srv, "SetDesiredCapacity", map[string]string{
		"AutoScalingGroupName": "clamp-asg",
		"DesiredCapacity":      "5",
	})
	body := xmlText(t, resp)

	// Then: AWS's ValidationError comes back, not a silent clamp
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "ValidationError") {
		t.Errorf("body does not name ValidationError: %s", body)
	}
	if !strings.Contains(body, "is above max value 2") {
		t.Errorf("body does not carry AWS's message: %s", body)
	}
}

// TestDescribeScalingActivities_recordsLaunches pins that every launch is
// recorded as a scaling activity with AWS's StatusCode and Cause shape.
func TestDescribeScalingActivities_recordsLaunches(t *testing.T) {
	// Given: a converged group of two
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "act-asg", 0, 4, 2)
	asGroupInstances(t, srv, "act-asg", func(in []asgInstanceXML) bool { return inServiceCount(in) == 2 })

	// When: the activities are described
	var out describeActivitiesXML
	asDecode(t, srv, "DescribeScalingActivities", map[string]string{
		"AutoScalingGroupName": "act-asg",
	}, &out)

	// Then: there is one launch activity per instance, successful
	if len(out.Activities) < 2 {
		t.Fatalf("got %d activities, want at least 2: %+v", len(out.Activities), out.Activities)
	}
	for _, a := range out.Activities {
		if a.GroupName != "act-asg" {
			t.Errorf("activity group = %q, want act-asg", a.GroupName)
		}
		if !strings.HasPrefix(a.Description, "Launching a new EC2 instance: i-") {
			t.Errorf("Description = %q, want AWS's launch wording", a.Description)
		}
		if a.StatusCode != "Successful" {
			t.Errorf("StatusCode = %q, want Successful", a.StatusCode)
		}
		if !strings.Contains(a.Cause, "difference between desired and actual capacity") {
			t.Errorf("Cause = %q, want AWS's capacity-difference wording", a.Cause)
		}
	}
}

// TestDeleteAutoScalingGroup_refusesWhileInstancesRemain pins AWS's
// ResourceInUse guard and that ForceDelete terminates the instances.
func TestDeleteAutoScalingGroup_refusesWhileInstancesRemain(t *testing.T) {
	// Given: a converged group of one
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "del-asg", 1, 2, 1)
	asGroupInstances(t, srv, "del-asg", func(in []asgInstanceXML) bool { return inServiceCount(in) == 1 })

	// When: a plain delete is attempted
	resp := asCall(t, srv, "DeleteAutoScalingGroup", map[string]string{
		"AutoScalingGroupName": "del-asg",
	})
	body := xmlText(t, resp)

	// Then: AWS's ResourceInUse comes back
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "ResourceInUse") {
		t.Errorf("body does not name ResourceInUse: %s", body)
	}

	// When: the delete is forced
	resp = asCall(t, srv, "DeleteAutoScalingGroup", map[string]string{
		"AutoScalingGroupName": "del-asg",
		"ForceDelete":          "true",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the group is gone
	var out describeGroupsXML
	asDecode(t, srv, "DescribeAutoScalingGroups", nil, &out)
	for _, g := range out.Groups {
		if g.Name == "del-asg" {
			t.Errorf("group still present after ForceDelete")
		}
	}
}

// ─── §2.1 refusals ────────────────────────────────────────────────────────────

// TestCreateAutoScalingGroup_refusesConfigurationsItCannotReconcile pins
// docs/plans/full-emulation-priority.md §2.1 for Auto Scaling: a group shape
// the reconciler cannot converge is refused with a 501 rather than stored and
// left reporting a desired capacity it never acts on.
func TestCreateAutoScalingGroup_refusesConfigurationsItCannotReconcile(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]string
	}{
		// Both carry a zone because a group has to name a zone or a subnet at
		// all (#1843); it is not what either case is testing.
		{
			name: "mixed instances policy",
			params: map[string]string{
				"AutoScalingGroupName": "mip-asg",
				"MixedInstancesPolicy.LaunchTemplate.LaunchTemplateSpecification.LaunchTemplateName": "my-template",
				"MinSize":                    "1",
				"MaxSize":                    "2",
				"AvailabilityZones.member.1": "us-east-1a",
			},
		},
		{
			name: "launch from a running instance",
			params: map[string]string{
				"AutoScalingGroupName":       "inst-asg",
				"InstanceId":                 "i-0123456789abcdef0",
				"MinSize":                    "1",
				"MaxSize":                    "2",
				"AvailabilityZones.member.1": "us-east-1a",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an empty store
			srv := helpers.NewTestServer(t)

			// When: an unreconcilable group is created
			resp := asCall(t, srv, "CreateAutoScalingGroup", tc.params)
			body := xmlText(t, resp)

			// Then: it is refused as unsupported
			if resp.StatusCode != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501: %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get("x-emulator-unsupported"); got != "true" {
				t.Errorf("x-emulator-unsupported = %q, want true", got)
			}

			// And: nothing was created, so nothing looks armed
			var out describeGroupsXML
			asDecode(t, srv, "DescribeAutoScalingGroups", nil, &out)
			if len(out.Groups) != 0 {
				t.Errorf("refused group was still created: %+v", out.Groups)
			}
		})
	}
}

// TestCreateAutoScalingGroup_requiresALaunchSource pins AWS's ValidationError
// for a group with nothing to launch from — the shape that would otherwise
// report a desired capacity it can never satisfy.
func TestCreateAutoScalingGroup_requiresALaunchSource(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a group is created with no launch configuration. It names a zone
	// because a group has to name a zone or a subnet at all (#1843), so the
	// missing launch source is the only thing wrong with it.
	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":       "bare-asg",
		"MinSize":                    "1",
		"MaxSize":                    "2",
		"AvailabilityZones.member.1": "us-east-1a",
	})
	body := xmlText(t, resp)

	// Then: AWS's ValidationError comes back
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "ValidationError") {
		t.Errorf("body does not name ValidationError: %s", body)
	}
	if !strings.Contains(body, "Valid requests must contain either LaunchTemplate") {
		t.Errorf("body is not the launch-source refusal: %s", body)
	}
}

// TestPutScalingPolicy_refusesPolicyTypesItCannotEvaluate pins §2.1 for
// scaling policies: a policy type the emulator does not execute is refused,
// not stored to sit there looking configured.
func TestPutScalingPolicy_refusesPolicyTypesItCannotEvaluate(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]string
	}{
		{
			name: "target tracking",
			params: map[string]string{
				"PolicyName": "keep-cpu-at-50", "PolicyType": "TargetTrackingScaling",
				"TargetTrackingConfiguration.TargetValue":                                        "50",
				"TargetTrackingConfiguration.PredefinedMetricSpecification.PredefinedMetricType": "ASGAverageCPUUtilization",
			},
		},
		{
			name:   "predictive scaling",
			params: map[string]string{"PolicyName": "forecast-weekday-load", "PolicyType": "PredictiveScaling"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a group
			srv := helpers.NewTestServer(t)
			newGroup(t, srv, "pol-asg", 1, 4, 1)

			// When: an unexecutable policy type is configured
			params := map[string]string{"AutoScalingGroupName": "pol-asg"}
			for k, v := range tc.params {
				params[k] = v
			}
			resp := asCall(t, srv, "PutScalingPolicy", params)
			body := xmlText(t, resp)

			// Then: it is refused as unsupported
			if resp.StatusCode != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501: %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get("x-emulator-unsupported"); got != "true" {
				t.Errorf("x-emulator-unsupported = %q, want true", got)
			}

			// And: no policy was stored
			list := asCall(t, srv, "DescribePolicies", map[string]string{"AutoScalingGroupName": "pol-asg"})
			listBody := xmlText(t, list)
			if strings.Contains(listBody, tc.params["PolicyName"]) {
				t.Errorf("refused policy was still stored: %s", listBody)
			}
		})
	}
}

// ─── Scaling policies ─────────────────────────────────────────────────────────

// TestExecutePolicy_simpleScalingChangesCapacity pins that a simple-scaling
// policy actually moves desired capacity and launches instances.
func TestExecutePolicy_simpleScalingChangesCapacity(t *testing.T) {
	// Given: a converged group of one with a +2 simple-scaling policy
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "exec-asg", 1, 5, 1)
	asGroupInstances(t, srv, "exec-asg", func(in []asgInstanceXML) bool { return inServiceCount(in) == 1 })

	resp := asCall(t, srv, "PutScalingPolicy", map[string]string{
		"AutoScalingGroupName": "exec-asg",
		"PolicyName":           "scale-out",
		"PolicyType":           "SimpleScaling",
		"AdjustmentType":       "ChangeInCapacity",
		"ScalingAdjustment":    "2",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When: the policy is executed
	resp = asCall(t, srv, "ExecutePolicy", map[string]string{
		"AutoScalingGroupName": "exec-asg",
		"PolicyName":           "scale-out",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the group converges on three instances
	asGroupInstances(t, srv, "exec-asg", func(in []asgInstanceXML) bool { return inServiceCount(in) == 3 })
}

// ─── Lifecycle hooks ──────────────────────────────────────────────────────────

// TestPutLifecycleHook_pausesLaunchUntilCompleted pins that a launch hook
// really holds the instance in Pending:Wait, and that CompleteLifecycleAction
// releases it.
func TestPutLifecycleHook_pausesLaunchUntilCompleted(t *testing.T) {
	// Given: a group with a launch lifecycle hook and no capacity yet
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "hook-asg", 0, 4, 0)

	resp := asCall(t, srv, "PutLifecycleHook", map[string]string{
		"AutoScalingGroupName": "hook-asg",
		"LifecycleHookName":    "wait-for-config",
		"LifecycleTransition":  "autoscaling:EC2_INSTANCE_LAUNCHING",
		"HeartbeatTimeout":     "3600",
		"DefaultResult":        "CONTINUE",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// When: capacity is requested
	resp = asCall(t, srv, "SetDesiredCapacity", map[string]string{
		"AutoScalingGroupName": "hook-asg",
		"DesiredCapacity":      "1",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the instance parks in Pending:Wait rather than going InService
	instances := asGroupInstances(t, srv, "hook-asg", func(in []asgInstanceXML) bool {
		return len(in) == 1 && in[0].LifecycleState == "Pending:Wait"
	})

	// When: the lifecycle action is completed
	resp = asCall(t, srv, "CompleteLifecycleAction", map[string]string{
		"AutoScalingGroupName":  "hook-asg",
		"LifecycleHookName":     "wait-for-config",
		"InstanceId":            instances[0].InstanceID,
		"LifecycleActionResult": "CONTINUE",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: the instance goes into service
	asGroupInstances(t, srv, "hook-asg", func(in []asgInstanceXML) bool {
		return len(in) == 1 && in[0].LifecycleState == "InService"
	})
}

// TestSetInstanceHealth_replacesUnhealthyInstances pins that marking an
// instance unhealthy makes the reconciler terminate and replace it.
func TestSetInstanceHealth_replacesUnhealthyInstances(t *testing.T) {
	// Given: a converged group of one
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "health-asg", 1, 3, 1)
	instances := asGroupInstances(t, srv, "health-asg", func(in []asgInstanceXML) bool {
		return inServiceCount(in) == 1
	})
	original := instances[0].InstanceID

	// When: the instance is marked unhealthy
	resp := asCall(t, srv, "SetInstanceHealth", map[string]string{
		"InstanceId":               original,
		"HealthStatus":             "Unhealthy",
		"ShouldRespectGracePeriod": "false",
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Then: it is replaced by a different instance
	asGroupInstances(t, srv, "health-asg", func(in []asgInstanceXML) bool {
		return inServiceCount(in) == 1 && in[0].InstanceID != original
	})
}
