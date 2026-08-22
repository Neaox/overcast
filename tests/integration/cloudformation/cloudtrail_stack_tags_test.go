package cloudformation_test

// cloudtrail_stack_tags_test.go — #1310: AWS::CloudTrail::Trail joins the
// effective-stack-tag mechanism (#1143). Before this, the trail's own Tags
// property was forwarded to CreateTrail, but the stack's own Tags (the ones
// passed to CreateStack/UpdateStack) never merged in — a stack tagged
// env=dev produced a trail with no env tag at all.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func listCloudTrailTags(t *testing.T, srv *helpers.TestServer, resourceID string) map[string]string {
	t.Helper()
	resp := cloudtrailJSONCall(t, srv, "ListTags", map[string]any{"ResourceIdList": []string{resourceID}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		ResourceTagList []struct {
			ResourceId string `json:"ResourceId"`
			TagsList   []struct {
				Key   string `json:"Key"`
				Value string `json:"Value"`
			} `json:"TagsList"`
		} `json:"ResourceTagList"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode ListTags: %v", err)
	}
	out := map[string]string{}
	for _, entry := range result.ResourceTagList {
		if entry.ResourceId != resourceID {
			continue
		}
		for _, tag := range entry.TagsList {
			out[tag.Key] = tag.Value
		}
	}
	return out
}

func cloudtrailStackTagsTemplate(trailName string) string {
	return `{
  "Resources": {
    "Trail": {
      "Type": "AWS::CloudTrail::Trail",
      "Properties": {
        "TrailName": "` + trailName + `",
        "S3BucketName": "cloudtrail-stack-tags-bucket",
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`
}

func TestCreateStack_CloudTrailTrail_stackTagsMerge(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "cloudtrail-stack-tags-create"
	const trailName = "cfn-stack-tags-create-trail"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {cloudtrailStackTagsTemplate(trailName)},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	physicalIDs := describeStackResourceIDs(t, srv, stackName)
	arn := physicalIDs["Trail"]
	if got := listCloudTrailTags(t, srv, arn); got["env"] != "dev" || got["owner"] != "resource" {
		t.Fatalf("trail tags = %#v, want env=dev and owner=resource merged", got)
	}
}

func TestUpdateStack_CloudTrailTrail_stackTagChangeReconciles(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "cloudtrail-stack-tags-update"
	const trailName = "cfn-stack-tags-update-trail"
	template := cloudtrailStackTagsTemplate(trailName)

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	arn := describeStackResourceIDs(t, srv, stackName)["Trail"]
	if got := listCloudTrailTags(t, srv, arn); got["env"] != "dev" {
		t.Fatalf("initial trail tags = %#v, want env=dev", got)
	}

	// When: only the stack's own tags change — the template (and so the
	// trail's own Tags/TrailName/S3BucketName) is byte-for-byte identical.
	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {template},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listCloudTrailTags(t, srv, arn); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled trail tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}
