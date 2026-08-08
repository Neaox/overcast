package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// iamQuery performs an IAM Query-protocol request against the test server.
func iamQuery(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2010-05-08")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("iamQuery: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("iamQuery: do: %v", err)
	}
	return resp
}

// TestDeleteStack_iamRoleWithInlinePolicy exercises the teardown order IAM's
// DeleteConflict now demands: AWS::IAM::Policy writes an inline policy onto the
// role, and AWS refuses DeleteRole while that document is still there. The
// AWS::IAM::Policy resource Refs the role, so CloudFormation deletes the policy
// first — and its provider has to actually remove the document, not assume the
// role delete will take it along.
func TestDeleteStack_iamRoleWithInlinePolicy(t *testing.T) {
	// Given: a stack with a role and an inline policy attached to it
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-teardown-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "lambda.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "AppPolicy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "cfn-teardown-policy",
        "Roles": [{"Ref": "AppRole"}],
        "PolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}]
        }
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-teardown-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-teardown-stack", "CREATE_COMPLETE")

	// And: the inline policy really is on the role
	pol := iamQuery(t, srv, "GetRolePolicy", url.Values{
		"RoleName": []string{"cfn-teardown-role"}, "PolicyName": []string{"cfn-teardown-policy"},
	})
	pol.Body.Close()
	helpers.AssertStatus(t, pol, http.StatusOK)

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"iam-teardown-stack"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: teardown completes rather than stalling on a DeleteConflict
	waitForStackStatus(t, srv, "iam-teardown-stack", "DELETE_COMPLETE")

	// And: the role is gone
	role := iamQuery(t, srv, "GetRole", url.Values{"RoleName": []string{"cfn-teardown-role"}})
	defer role.Body.Close()
	helpers.AssertStatus(t, role, http.StatusNotFound)
}

// TestDeleteStack_iamRoleInInstanceProfile covers the other ordering IAM now
// enforces: a role that is still in an instance profile cannot be deleted. The
// profile Refs the role, so CloudFormation removes the profile first.
func TestDeleteStack_iamRoleInInstanceProfile(t *testing.T) {
	// Given: a stack with a role and an instance profile holding it
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AppRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-profile-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "ec2.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "AppProfile": {
      "Type": "AWS::IAM::InstanceProfile",
      "Properties": {
        "InstanceProfileName": "cfn-profile",
        "Roles": [{"Ref": "AppRole"}]
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-profile-stack"},
		"TemplateBody": []string{template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "iam-profile-stack", "CREATE_COMPLETE")

	// When: the stack is deleted
	del := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"iam-profile-stack"},
	})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: teardown completes and the role is gone
	waitForStackStatus(t, srv, "iam-profile-stack", "DELETE_COMPLETE")
	role := iamQuery(t, srv, "GetRole", url.Values{"RoleName": []string{"cfn-profile-role"}})
	defer role.Body.Close()
	helpers.AssertStatus(t, role, http.StatusNotFound)
}
