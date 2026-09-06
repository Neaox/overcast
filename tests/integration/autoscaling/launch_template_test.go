package autoscaling_test

// Launch-template Auto Scaling groups (#518). CDK's
// autoscaling.AutoScalingGroup emits a launch template rather than a launch
// configuration, so this is the modern IaC path: the group has to converge on
// its DesiredCapacity from a template, and DescribeAutoScalingGroups has to
// report the template it was configured with.

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

type describeGroupsLaunchTemplateXML struct {
	XMLName xml.Name `xml:"DescribeAutoScalingGroupsResponse"`
	Groups  []struct {
		Name           string `xml:"AutoScalingGroupName"`
		LaunchTemplate struct {
			LaunchTemplateID   string `xml:"LaunchTemplateId"`
			LaunchTemplateName string `xml:"LaunchTemplateName"`
			Version            string `xml:"Version"`
		} `xml:"LaunchTemplate"`
		Instances []asgInstanceXML `xml:"Instances>member"`
	} `xml:"DescribeAutoScalingGroupsResult>AutoScalingGroups>member"`
}

// createLaunchTemplate creates an EC2 launch template and returns its ID.
func createLaunchTemplate(t *testing.T, srv *helpers.TestServer, name, instanceType string) string {
	t.Helper()
	resp := ec2Call(t, srv, "CreateLaunchTemplate", map[string]string{
		"LaunchTemplateName":              name,
		"LaunchTemplateData.ImageId":      "ami-0123456789abcdef0",
		"LaunchTemplateData.InstanceType": instanceType,
	})
	body := xmlText(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateLaunchTemplate: %d: %s", resp.StatusCode, body)
	}
	var out struct {
		XMLName xml.Name `xml:"CreateLaunchTemplateResponse"`
		ID      string   `xml:"launchTemplate>launchTemplateId"`
	}
	if err := xml.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("CreateLaunchTemplate: unmarshal: %v\n%s", err, body)
	}
	if out.ID == "" {
		t.Fatalf("CreateLaunchTemplate returned no id: %s", body)
	}
	return out.ID
}

// TestCreateAutoScalingGroup_launchTemplateConvergesToDesiredCapacity is the
// point of #518: a group configured with a launch template rather than a
// launch configuration reaches its DesiredCapacity, with the template's
// InstanceType on every instance.
func TestCreateAutoScalingGroup_launchTemplateConvergesToDesiredCapacity(t *testing.T) {
	// Given: a launch template and a group referencing it by name
	srv := helpers.NewTestServer(t)
	templateID := createLaunchTemplate(t, srv, "lt-asg-template", "m5.large")

	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":              "lt-asg",
		"LaunchTemplate.LaunchTemplateName": "lt-asg-template",
		"LaunchTemplate.Version":            "$Latest",
		"MinSize":                           "1",
		"MaxSize":                           "4",
		"DesiredCapacity":                   "2",
		"AvailabilityZones.member.1":        "us-east-1a",
	})
	if body := xmlText(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("CreateAutoScalingGroup: %d: %s", resp.StatusCode, body)
	}

	// When: the reconciler converges the group
	instances := asGroupInstances(t, srv, "lt-asg", func(in []asgInstanceXML) bool {
		return inServiceCount(in) == 2
	})

	// Then: the instances carry the template's instance type
	for _, inst := range instances {
		if !strings.HasPrefix(inst.InstanceID, "i-") {
			t.Errorf("InstanceId = %q, want an i-… EC2 instance id", inst.InstanceID)
		}
		if inst.InstanceType != "m5.large" {
			t.Errorf("InstanceType = %q, want the template's m5.large", inst.InstanceType)
		}
	}

	// And: DescribeAutoScalingGroups reports the launch template it was given
	var out describeGroupsLaunchTemplateXML
	asDecode(t, srv, "DescribeAutoScalingGroups", map[string]string{
		"AutoScalingGroupNames.member.1": "lt-asg",
	}, &out)
	if len(out.Groups) != 1 {
		t.Fatalf("DescribeAutoScalingGroups returned %d groups, want 1", len(out.Groups))
	}
	lt := out.Groups[0].LaunchTemplate
	if lt.LaunchTemplateID != templateID {
		t.Errorf("LaunchTemplateId = %q, want %q", lt.LaunchTemplateID, templateID)
	}
	if lt.LaunchTemplateName != "lt-asg-template" {
		t.Errorf("LaunchTemplateName = %q, want lt-asg-template", lt.LaunchTemplateName)
	}
	if lt.Version != "$Latest" {
		t.Errorf("Version = %q, want $Latest", lt.Version)
	}

	// And: the instances really exist in EC2
	ec2Resp := ec2Call(t, srv, "DescribeInstances", nil)
	ec2Body := xmlText(t, ec2Resp)
	for _, inst := range instances {
		if !strings.Contains(ec2Body, inst.InstanceID) {
			t.Errorf("EC2 DescribeInstances does not know about %s", inst.InstanceID)
		}
	}
}

// TestCreateAutoScalingGroup_launchTemplateNotFound pins that a group naming a
// template that does not exist is refused rather than stored reporting a
// desired capacity it can never satisfy.
func TestCreateAutoScalingGroup_launchTemplateNotFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a group names a launch template that was never created. The zone
	// is not what this test is about; it is there because a group has to name
	// a zone or a subnet at all (#1843), and without one the refusal below
	// would be the placement error rather than the template one.
	resp := asCall(t, srv, "CreateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":              "missing-lt-asg",
		"LaunchTemplate.LaunchTemplateName": "never-created",
		"MinSize":                           "1",
		"MaxSize":                           "2",
		"AvailabilityZones.member.1":        "us-east-1a",
	})
	body := xmlText(t, resp)

	// Then: AWS's ValidationError comes back and nothing is stored
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "ValidationError") {
		t.Errorf("body does not name ValidationError: %s", body)
	}
	if !strings.Contains(body, "You must use a valid fully-formed launch template.") {
		t.Errorf("body is not the launch-template refusal: %s", body)
	}
	var out describeGroupsXML
	asDecode(t, srv, "DescribeAutoScalingGroups", nil, &out)
	if len(out.Groups) != 0 {
		t.Errorf("refused group was still created: %+v", out.Groups)
	}
}

// TestUpdateAutoScalingGroup_switchesToALaunchTemplate pins that a group can
// move from a launch configuration to a launch template.
func TestUpdateAutoScalingGroup_switchesToALaunchTemplate(t *testing.T) {
	// Given: a converged group launched from a launch configuration
	srv := helpers.NewTestServer(t)
	newGroup(t, srv, "switch-asg", 1, 4, 1)
	asGroupInstances(t, srv, "switch-asg", func(in []asgInstanceXML) bool { return inServiceCount(in) == 1 })
	templateID := createLaunchTemplate(t, srv, "switch-template", "c5.xlarge")

	// When: the group is updated to use a launch template
	resp := asCall(t, srv, "UpdateAutoScalingGroup", map[string]string{
		"AutoScalingGroupName":            "switch-asg",
		"LaunchTemplate.LaunchTemplateId": templateID,
	})
	if body := xmlText(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("UpdateAutoScalingGroup: %d: %s", resp.StatusCode, body)
	}

	// Then: the group reports the template
	var out describeGroupsLaunchTemplateXML
	asDecode(t, srv, "DescribeAutoScalingGroups", map[string]string{
		"AutoScalingGroupNames.member.1": "switch-asg",
	}, &out)
	if len(out.Groups) != 1 {
		t.Fatalf("DescribeAutoScalingGroups returned %d groups, want 1", len(out.Groups))
	}
	if got := out.Groups[0].LaunchTemplate.LaunchTemplateID; got != templateID {
		t.Errorf("LaunchTemplateId = %q, want %q", got, templateID)
	}
}
