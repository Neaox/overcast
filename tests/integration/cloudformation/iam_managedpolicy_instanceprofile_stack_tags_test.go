package cloudformation_test

// iam_managedpolicy_instanceprofile_stack_tags_test.go — #1310:
// AWS::IAM::ManagedPolicy and AWS::IAM::InstanceProfile join the
// effective-stack-tag mechanism (#1143). Neither forwarded any Tags at all
// before #1310 (a gap #1308 left for this follow-up); now both merge the
// stack's own Tags at create and reconcile a stack-tag-only change on update,
// via TagPolicy/UntagPolicy and TagInstanceProfile/UntagInstanceProfile
// respectively — the same mechanism IAM::Role and IAM::User already used
// (extended here to also merge stack tags, so all four IAM principal types
// that carry Tags stay on one mechanism rather than three separate ones).

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

type iamTagMember struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func listIAMPolicyTags(t *testing.T, srv *helpers.TestServer, policyArn string) map[string]string {
	t.Helper()
	resp := iamQuery(t, srv, "ListPolicyTags", url.Values{"PolicyArn": {policyArn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			Tags struct {
				Members []iamTagMember `xml:"member"`
			} `xml:"Tags"`
		} `xml:"ListPolicyTagsResult"`
	}
	body := readBody(t, resp)
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal ListPolicyTagsResponse: %v\nbody: %s", err, body)
	}
	out := map[string]string{}
	for _, m := range result.Result.Tags.Members {
		out[m.Key] = m.Value
	}
	return out
}

func listIAMInstanceProfileTags(t *testing.T, srv *helpers.TestServer, profileName string) map[string]string {
	t.Helper()
	resp := iamQuery(t, srv, "ListInstanceProfileTags", url.Values{"InstanceProfileName": {profileName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Result struct {
			Tags struct {
				Members []iamTagMember `xml:"member"`
			} `xml:"Tags"`
		} `xml:"ListInstanceProfileTagsResult"`
	}
	body := readBody(t, resp)
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal ListInstanceProfileTagsResponse: %v\nbody: %s", err, body)
	}
	out := map[string]string{}
	for _, m := range result.Result.Tags.Members {
		out[m.Key] = m.Value
	}
	return out
}

const iamManagedPolicyStackTagsTemplate = `{
  "Resources": {
    "Policy": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "cfn-stack-tags-policy",
        "PolicyDocument": {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]},
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`

func TestCreateStack_IAMManagedPolicy_stackTagsMerge(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "iam-managed-policy-stack-tags-create"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {iamManagedPolicyStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	policyArn := describeStackResourceIDs(t, srv, stackName)["Policy"]
	if got := listIAMPolicyTags(t, srv, policyArn); got["env"] != "dev" || got["owner"] != "resource" {
		t.Fatalf("policy tags = %#v, want env=dev and owner=resource merged", got)
	}
}

func TestUpdateStack_IAMManagedPolicy_stackTagChangeReconciles(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "iam-managed-policy-stack-tags-update"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {iamManagedPolicyStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	policyArn := describeStackResourceIDs(t, srv, stackName)["Policy"]

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {iamManagedPolicyStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listIAMPolicyTags(t, srv, policyArn); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled policy tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}

const iamInstanceProfileStackTagsTemplate = `{
  "Resources": {
    "Profile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "cfn-stack-tags-profile",
        "Tags": [{"Key": "owner", "Value": "resource"}]
      }
    }
  }
}`

func TestCreateStack_IAMInstanceProfile_stackTagsMerge(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "iam-instance-profile-stack-tags-create"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {iamInstanceProfileStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	if got := listIAMInstanceProfileTags(t, srv, "cfn-stack-tags-profile"); got["env"] != "dev" || got["owner"] != "resource" {
		t.Fatalf("instance profile tags = %#v, want env=dev and owner=resource merged", got)
	}
}

func TestUpdateStack_IAMInstanceProfile_stackTagChangeReconciles(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "iam-instance-profile-stack-tags-update"

	createResp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {iamInstanceProfileStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"dev"},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	updateResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":           {stackName},
		"TemplateBody":        {iamInstanceProfileStackTagsTemplate},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer updateResp.Body.Close()
	helpers.AssertStatus(t, updateResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	if got := listIAMInstanceProfileTags(t, srv, "cfn-stack-tags-profile"); got["env"] != "prod" || got["owner"] != "resource" {
		t.Fatalf("reconciled instance profile tags = %#v, want env=prod (reconciled) and owner=resource (unchanged)", got)
	}
}
