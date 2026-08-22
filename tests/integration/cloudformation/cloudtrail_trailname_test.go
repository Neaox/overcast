package cloudformation_test

// cloudtrail_trailname_test.go — #1309: AWS::CloudTrail::Trail's schema names
// the trail-name property TrailName, not Name. The handler used to read
// "Name", so a real template (which always sets TrailName, per
// https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-cloudtrail-trail.html)
// silently got an auto-generated name instead, and Ref/GetAtt consumers saw an
// identifier the template never asked for.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const cloudtrailTargetPrefix = "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101."

func cloudtrailJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, cloudtrailTargetPrefix, action, "application/x-amz-json-1.1", body)
}

// describeTrailNames returns every trail name CloudTrail currently knows
// about, read through DescribeTrails — never through CloudFormation's own
// bookkeeping — so the test exercises the real code path a template's Ref or
// GetAtt would.
func describeTrailNames(t *testing.T, srv *helpers.TestServer) []string {
	t.Helper()
	resp := cloudtrailJSONCall(t, srv, "DescribeTrails", map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		TrailList []struct {
			Name string `json:"Name"`
		} `json:"trailList"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode DescribeTrails: %v", err)
	}
	names := make([]string, 0, len(result.TrailList))
	for _, tr := range result.TrailList {
		names = append(names, tr.Name)
	}
	return names
}

func trailNameTemplate(trailName string) string {
	return `{
  "Resources": {
    "Trail": {
      "Type": "AWS::CloudTrail::Trail",
      "Properties": {
        "TrailName": "` + trailName + `",
        "S3BucketName": "cloudtrail-logs-bucket",
        "IsLogging": true
      }
    }
  },
  "Outputs": {
    "TrailRef": {"Value": {"Ref": "Trail"}}
  }
}`
}

// TestCreateStack_CloudTrailTrailName_provisionsTheSchemaName asserts that a
// template using the real schema property TrailName provisions a trail with
// that exact name, and that Ref returns it — matching AWS, where "Ref returns
// the name of the trail, such as MyTrail."
func TestCreateStack_CloudTrailTrailName_provisionsTheSchemaName(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "cloudtrail-trailname-create-stack"
	const trailName = "cfn-trailname-create-trail"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {trailNameTemplate(trailName)},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	names := describeTrailNames(t, srv)
	found := false
	for _, n := range names {
		if n == trailName {
			found = true
		}
	}
	if !found {
		t.Fatalf("DescribeTrails did not contain %q: got %v", trailName, names)
	}

	outputs := describeStackOutputs(t, srv, stackName)
	if outputs["TrailRef"] != trailName {
		t.Fatalf("Ref(Trail) = %q, want %q", outputs["TrailRef"], trailName)
	}
}

// TestCreateStack_CloudTrailName_isNotAcceptedAsAnAlias asserts the alpha
// no-shims policy: a template that sets the wrong property key (Name, not
// TrailName) does not get its value silently honored. Overcast has no
// per-property "unrecognised property" diagnostic channel (only a
// resource-type-level one — see fidelity.go), so the observable outcome is
// the same one real AWS's schema validation would produce for this
// emulator's level of fidelity: the property is not recognised and the trail
// gets Overcast's auto-generated name instead of the template's requested one.
func TestCreateStack_CloudTrailName_isNotAcceptedAsAnAlias(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "cloudtrail-name-not-alias-stack"
	const wrongName = "cfn-name-alias-trail"
	template := `{
  "Resources": {
    "Trail": {
      "Type": "AWS::CloudTrail::Trail",
      "Properties": {
        "Name": "` + wrongName + `",
        "S3BucketName": "cloudtrail-logs-bucket"
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

	names := describeTrailNames(t, srv)
	for _, n := range names {
		if n == wrongName {
			t.Fatalf("Name was honored as an alias for TrailName: trail %q exists", wrongName)
		}
	}
}

// TestUpdateStack_CloudTrailTrailNameChange_replaces asserts that TrailName is
// create-only in the real schema: changing it must replace the trail, not
// rename it in place (CloudTrail's UpdateTrail has no way to rename a trail
// anyway).
func TestUpdateStack_CloudTrailTrailNameChange_replaces(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "cloudtrail-trailname-update-stack"
	const originalName = "cfn-trailname-v1"
	const changedName = "cfn-trailname-v2"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {trailNameTemplate(originalName)},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	resourceIDs := describeStackResourceIDs(t, srv, stackName)
	originalPhysicalID := resourceIDs["Trail"]

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {stackName},
		"TemplateBody": {trailNameTemplate(changedName)},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	names := describeTrailNames(t, srv)
	var haveOriginal, haveChanged bool
	for _, n := range names {
		if n == originalName {
			haveOriginal = true
		}
		if n == changedName {
			haveChanged = true
		}
	}
	if haveOriginal {
		t.Fatalf("original trail %q still exists after a TrailName change; it should have been replaced", originalName)
	}
	if !haveChanged {
		t.Fatalf("DescribeTrails did not contain the replacement trail %q: got %v", changedName, names)
	}

	newResourceIDs := describeStackResourceIDs(t, srv, stackName)
	if newResourceIDs["Trail"] == originalPhysicalID {
		t.Fatalf("Trail physical ID unchanged (%q) after a TrailName change; expected replacement", originalPhysicalID)
	}

	outputs := describeStackOutputs(t, srv, stackName)
	if outputs["TrailRef"] != changedName {
		t.Fatalf("Ref(Trail) after replacement = %q, want %q", outputs["TrailRef"], changedName)
	}
}
