package cloudformation_test

// AWS::EC2::LaunchTemplate (#518). This is the CDK path: recent
// autoscaling.AutoScalingGroup constructs synthesize a launch template and an
// Auto Scaling group that references it, so the two resources are provisioned
// together here rather than in isolation.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

var cfnLaunchTemplateIDPattern = regexp.MustCompile(`^lt-[0-9a-f]{17}$`)

// describeLaunchTemplateByID returns one launch template's record from EC2.
func describeLaunchTemplateByID(t *testing.T, srv *helpers.TestServer, id string) struct {
	LaunchTemplateID     string `xml:"launchTemplateId"`
	LaunchTemplateName   string `xml:"launchTemplateName"`
	DefaultVersionNumber int64  `xml:"defaultVersionNumber"`
	LatestVersionNumber  int64  `xml:"latestVersionNumber"`
} {
	t.Helper()
	resp := ec2Query(t, srv, "DescribeLaunchTemplates", url.Values{"LaunchTemplateId.1": {id}})
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeLaunchTemplates: status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		Templates []struct {
			LaunchTemplateID     string `xml:"launchTemplateId"`
			LaunchTemplateName   string `xml:"launchTemplateName"`
			DefaultVersionNumber int64  `xml:"defaultVersionNumber"`
			LatestVersionNumber  int64  `xml:"latestVersionNumber"`
		} `xml:"launchTemplates>item"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode DescribeLaunchTemplatesResponse: %v\n%s", err, body)
	}
	if len(result.Templates) != 1 {
		t.Fatalf("DescribeLaunchTemplates returned %d templates, want 1: %s", len(result.Templates), body)
	}
	return result.Templates[0]
}

const launchTemplateStackTemplate = `{
  "Resources": {
    "LaunchTemplate": {
      "Type": "AWS::EC2::LaunchTemplate",
      "Properties": {
        "LaunchTemplateData": {
          "ImageId": "ami-0123456789abcdef0",
          "InstanceType": "t3.micro",
          "TagSpecifications": [
            {
              "ResourceType": "instance",
              "Tags": [{ "Key": "Name", "Value": "cdk-asg" }]
            }
          ]
        }
      }
    },
    "ASG": {
      "Type": "AWS::AutoScaling::AutoScalingGroup",
      "Properties": {
        "MinSize": "1",
        "MaxSize": "3",
        "DesiredCapacity": "2",
        "AvailabilityZones": ["us-east-1a"],
        "LaunchTemplate": {
          "LaunchTemplateId": { "Ref": "LaunchTemplate" },
          "Version": { "Fn::GetAtt": ["LaunchTemplate", "LatestVersionNumber"] }
        }
      }
    }
  },
  "Outputs": {
    "TemplateRef": { "Value": { "Ref": "LaunchTemplate" } },
    "TemplateId": { "Value": { "Fn::GetAtt": ["LaunchTemplate", "LaunchTemplateId"] } },
    "LatestVersion": { "Value": { "Fn::GetAtt": ["LaunchTemplate", "LatestVersionNumber"] } },
    "DefaultVersion": { "Value": { "Fn::GetAtt": ["LaunchTemplate", "DefaultVersionNumber"] } }
  }
}`

// TestCreateStack_EC2LaunchTemplateAndAutoScalingGroup provisions the pair CDK
// emits and asserts Ref, the three GetAtt attributes, and that the group the
// template feeds actually converges.
func TestCreateStack_EC2LaunchTemplateAndAutoScalingGroup(t *testing.T) {
	// Given: a stack with a launch template and a group that references it
	srv := helpers.NewTestServer(t)
	const stackName = "launch-template-stack"

	// When: the stack is created
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {launchTemplateStackTemplate},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// Then: Ref and the physical ID are the launch template's id
	physicalID := stackResourcePhysicalID(t, srv, stackName, "LaunchTemplate")
	if !cfnLaunchTemplateIDPattern.MatchString(physicalID) {
		t.Errorf("physical ID = %q, want an lt-… launch template id", physicalID)
	}
	outputs := describeStackOutputs(t, srv, stackName)
	if outputs["TemplateRef"] != physicalID {
		t.Errorf("Ref = %q, want the launch template id %q", outputs["TemplateRef"], physicalID)
	}
	if outputs["TemplateId"] != physicalID {
		t.Errorf("GetAtt LaunchTemplateId = %q, want %q", outputs["TemplateId"], physicalID)
	}
	if outputs["LatestVersion"] != "1" || outputs["DefaultVersion"] != "1" {
		t.Errorf("latest/default GetAtt = %q/%q, want 1/1", outputs["LatestVersion"], outputs["DefaultVersion"])
	}

	// And: the template really exists in EC2, named by CloudFormation
	tmpl := describeLaunchTemplateByID(t, srv, physicalID)
	if !strings.HasPrefix(tmpl.LaunchTemplateName, stackName+"-LaunchTemplate-") {
		t.Errorf("LaunchTemplateName = %q, want a CloudFormation-generated name", tmpl.LaunchTemplateName)
	}

	// And: the Auto Scaling group the template feeds converges
	instances := asgInstancesForGroup(t, srv, stackResourcePhysicalID(t, srv, stackName, "ASG"), 2)
	for _, inst := range instances {
		if inst.InstanceType != "t3.micro" {
			t.Errorf("InstanceType = %q, want the template's t3.micro", inst.InstanceType)
		}
	}
}

// TestUpdateStack_EC2LaunchTemplateDataCreatesANewVersion pins CloudFormation's
// update behaviour: changing LaunchTemplateData creates a new version of the
// same template rather than replacing the resource.
func TestUpdateStack_EC2LaunchTemplateDataCreatesANewVersion(t *testing.T) {
	// Given: a stack with one launch template
	srv := helpers.NewTestServer(t)
	const stackName = "launch-template-update-stack"
	const initial = `{
  "Resources": {
    "LaunchTemplate": {
      "Type": "AWS::EC2::LaunchTemplate",
      "Properties": {
        "LaunchTemplateName": "update-me",
        "LaunchTemplateData": { "ImageId": "ami-0123456789abcdef0", "InstanceType": "t3.micro" }
      }
    }
  }
}`
	const updated = `{
  "Resources": {
    "LaunchTemplate": {
      "Type": "AWS::EC2::LaunchTemplate",
      "Properties": {
        "LaunchTemplateName": "update-me",
        "LaunchTemplateData": { "ImageId": "ami-0123456789abcdef0", "InstanceType": "m5.large" }
      }
    }
  }
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {initial},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	before := stackResourcePhysicalID(t, srv, stackName, "LaunchTemplate")

	// When: the launch template data changes
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {updated},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: the same template gained a version, and it is the default
	after := stackResourcePhysicalID(t, srv, stackName, "LaunchTemplate")
	if after != before {
		t.Errorf("physical ID changed from %q to %q; a data-only change updates in place", before, after)
	}
	tmpl := describeLaunchTemplateByID(t, srv, after)
	if tmpl.LatestVersionNumber != 2 {
		t.Errorf("latestVersionNumber = %d, want 2", tmpl.LatestVersionNumber)
	}
	if tmpl.DefaultVersionNumber != 2 {
		t.Errorf("defaultVersionNumber = %d, want 2", tmpl.DefaultVersionNumber)
	}
}

// TestDeleteStack_EC2LaunchTemplateIsRemoved pins that stack teardown deletes
// the template rather than leaving it behind.
func TestDeleteStack_EC2LaunchTemplateIsRemoved(t *testing.T) {
	// Given: a stack holding one launch template
	srv := helpers.NewTestServer(t)
	const stackName = "launch-template-teardown-stack"
	const template = `{
  "Resources": {
    "LaunchTemplate": {
      "Type": "AWS::EC2::LaunchTemplate",
      "Properties": {
        "LaunchTemplateData": { "ImageId": "ami-0123456789abcdef0", "InstanceType": "t3.micro" }
      }
    }
  }
}`
	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {template},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	physicalID := stackResourcePhysicalID(t, srv, stackName, "LaunchTemplate")

	// When: the stack is deleted
	deleteResp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")

	// Then: EC2 no longer knows the template
	resp := ec2Query(t, srv, "DescribeLaunchTemplates", url.Values{"LaunchTemplateId.1": {physicalID}})
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("DescribeLaunchTemplates after teardown: status %d, want 400: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "InvalidLaunchTemplateId.NotFound") {
		t.Errorf("body does not name InvalidLaunchTemplateId.NotFound: %s", body)
	}
}

// asgXMLInstance is one member of a group's Instances set.
type asgXMLInstance struct {
	InstanceID   string `xml:"InstanceId"`
	InstanceType string `xml:"InstanceType"`
	State        string `xml:"LifecycleState"`
}

// asgInstancesForGroup polls DescribeAutoScalingGroups until the named group
// reports want instances InService. The reconciler is a background loop, so a
// wire-level test observes its effect rather than driving it.
func asgInstancesForGroup(t *testing.T, srv *helpers.TestServer, group string, want int) []asgXMLInstance {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last []asgXMLInstance
	for time.Now().Before(deadline) {
		resp := asQuery(t, srv, "DescribeAutoScalingGroups", url.Values{
			"AutoScalingGroupNames.member.1": {group},
		})
		body := readBody(t, resp)
		resp.Body.Close()
		var out struct {
			Instances []asgXMLInstance `xml:"DescribeAutoScalingGroupsResult>AutoScalingGroups>member>Instances>member"`
		}
		if err := xml.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode DescribeAutoScalingGroupsResponse: %v\n%s", err, body)
		}
		last = out.Instances
		inService := 0
		for _, inst := range last {
			if inst.State == "InService" {
				inService++
			}
		}
		if inService == want {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("group %q never reached %d InService instances; last was %+v", group, want, last)
	return nil
}

// asQuery issues an Auto Scaling Query-protocol request.
func asQuery(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2011-01-01")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("asQuery build request %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("asQuery %s: %v", action, err)
	}
	return resp
}
