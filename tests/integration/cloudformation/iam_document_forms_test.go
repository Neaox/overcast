package cloudformation_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// CloudFormation types AWS::IAM::Policy's PolicyDocument and AWS::IAM::Role's
// AssumeRolePolicyDocument as `Json`, which accepts a JSON object or a string
// holding one. The handlers read only the object form until #1717 made IAM
// check documents, at which point a string form went from silently stored as
// "null" to a hard MalformedPolicyDocument failure. Both forms must deploy.
func TestCreateStack_IAMPolicyDocumentAsString(t *testing.T) {
	// Given: a role with its trust policy and an inline policy both written as
	// JSON strings rather than objects.
	srv := helpers.NewTestServer(t)
	template := `{
  "Resources": {
    "Role": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-string-doc-role",
        "AssumeRolePolicyDocument": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"Service\":\"ec2.amazonaws.com\"},\"Action\":\"sts:AssumeRole\"}]}"
      }
    },
    "Policy": {
      "Type": "AWS::IAM::Policy",
      "Properties": {
        "PolicyName": "cfn-string-doc-inline",
        "Roles": [{"Ref": "Role"}],
        "PolicyDocument": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"s3:GetObject\",\"Resource\":\"*\"}]}"
      }
    }
  }
}`

	// When: the stack is created.
	createIAMStack(t, srv, "iam-string-docs", template)

	// Then: both documents reached IAM as the author wrote them.
	assertIAMInlinePolicyDocument(t, srv, "GetRolePolicy", "RoleName", "cfn-string-doc-role", "cfn-string-doc-inline", "s3:GetObject")
}

// AWS::IAM::Role requires AssumeRolePolicyDocument. The handler used to default
// an omitted one to "{}", a trust policy AWS never accepted and IAM now refuses,
// so the stack failed with a MalformedPolicyDocument about a document the
// template never wrote. It fails on the missing property instead.
func TestCreateStack_IAMRoleWithoutTrustPolicyFailsOnTheProperty(t *testing.T) {
	// Given: a role template with no AssumeRolePolicyDocument.
	srv := helpers.NewTestServer(t)
	template := `{
  "Resources": {
    "Role": {
      "Type": "AWS::IAM::Role",
      "Properties": { "RoleName": "cfn-no-trust-role" }
    }
  }
}`

	// When: the stack is created.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"iam-no-trust"},
		"TemplateBody": []string{template},
	})
	cr.Body.Close()

	// Then: it rolls back, and the failure names the property.
	waitForStackStatus(t, srv, "iam-no-trust", "ROLLBACK_COMPLETE")
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": []string{"iam-no-trust"}})
	defer events.Body.Close()
	body := string(readBody(t, events))
	if !strings.Contains(body, "AssumeRolePolicyDocument is required") {
		t.Fatalf("stack events do not name the missing AssumeRolePolicyDocument: %s", body)
	}
}
