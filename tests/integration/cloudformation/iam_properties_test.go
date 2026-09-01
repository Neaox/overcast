package cloudformation_test

// Integration coverage for issue #521: CloudFormation used to parse the IAM
// principal-attachment properties (ManagedPolicyArns, Policies, Groups, the
// ManagedPolicy Roles/Users/Groups lists) and the remaining CreateRole scalars
// into nothing. These tests provision through CloudFormation and then read the
// result back through IAM's own list operations, so template and service must
// agree. Test shapes derived from the earlier salvage branch
// codex/issue-521-iam-cfn.

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const (
	iamReadOnlyPolicyArn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
	iamAdminPolicyArn    = "arn:aws:iam::aws:policy/AdministratorAccess"
	iamPowerUserArn      = "arn:aws:iam::aws:policy/PowerUserAccess"
)

func TestCreateStack_IAMRole_reconcilesPoliciesAndCreateProperties(t *testing.T) {
	// Given: a role with managed and inline permissions plus every CreateRole scalar.
	srv := helpers.NewTestServer(t)
	stackName := "iam-role-properties"
	createIAMStack(t, srv, stackName, iamRoleTemplate(iamReadOnlyPolicyArn, "old-inline", "s3:GetObject", "created"))

	// Then: CreateRole/GetRole round-trip the scalar properties.
	role := getIAMRole(t, srv, "cfn-policy-role")
	if role.Path != "/application/" || role.Description != "created by CloudFormation" || role.MaxSessionDuration != 7200 {
		t.Fatalf("role scalar properties: %+v", role)
	}
	boundaryArn := "arn:aws:iam::000000000000:policy/cfn-boundary-policy"
	if role.PermissionsBoundaryArn != boundaryArn {
		t.Fatalf("role permissions boundary: got %q, want %q", role.PermissionsBoundaryArn, boundaryArn)
	}
	assertIAMTags(t, srv, "ListRoleTags", "RoleName", "cfn-policy-role", map[string]string{"stage": "created"})

	// And: the template's managed and inline permissions actually reached IAM.
	assertIAMStringMembers(t, srv, "ListRolePolicies", url.Values{"RoleName": {"cfn-policy-role"}}, "old-inline")
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-policy-role", iamReadOnlyPolicyArn)
	assertIAMInlinePolicyDocument(t, srv, "GetRolePolicy", "RoleName", "cfn-policy-role", "old-inline", "s3:GetObject")

	// And: out-of-band additions that the template does not own.
	assertIAMSuccess(t, srv, "AttachRolePolicy", url.Values{"RoleName": {"cfn-policy-role"}, "PolicyArn": {iamPowerUserArn}})
	assertIAMSuccess(t, srv, "PutRolePolicy", url.Values{
		"RoleName": {"cfn-policy-role"}, "PolicyName": {"external-inline"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"logs:CreateLogGroup","Resource":"*"}]}`},
	})

	// When: the stack replaces both permission sets in place.
	updateIAMStack(t, srv, stackName, iamRoleTemplate(iamAdminPolicyArn, "new-inline", "sqs:SendMessage", "updated"))

	// Then: additions and removals agree with the updated template, and the
	// out-of-band attachments survive untouched.
	assertIAMStringMembers(t, srv, "ListRolePolicies", url.Values{"RoleName": {"cfn-policy-role"}}, "new-inline")
	assertIAMStringMembersAbsent(t, srv, "ListRolePolicies", url.Values{"RoleName": {"cfn-policy-role"}}, "old-inline")
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-policy-role", iamAdminPolicyArn)
	assertIAMAttachedPoliciesAbsent(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-policy-role", iamReadOnlyPolicyArn)
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-policy-role", iamPowerUserArn)
	assertIAMInlinePolicyDocument(t, srv, "GetRolePolicy", "RoleName", "cfn-policy-role", "new-inline", "sqs:SendMessage")
	assertIAMInlinePolicyDocument(t, srv, "GetRolePolicy", "RoleName", "cfn-policy-role", "external-inline", "logs:CreateLogGroup")
	assertIAMTags(t, srv, "ListRoleTags", "RoleName", "cfn-policy-role", map[string]string{"stage": "updated"})

	// When: the optional scalar properties are removed from the template.
	updateIAMStack(t, srv, stackName, iamRoleResetTemplate())

	// Then: IAM receives explicit resets, including AWS's 3600-second default.
	role = getIAMRole(t, srv, "cfn-policy-role")
	if role.Description != "" || role.MaxSessionDuration != 3600 || role.PermissionsBoundaryArn != "" {
		t.Fatalf("reset role scalar properties: %+v", role)
	}
}

// TestCreateStack_IAMRole_commaDelimitedListPolicyParameter is the CDK
// bootstrap shape: CloudFormationExecutionPolicies is a CommaDelimitedList
// parameter whose Ref feeds ManagedPolicyArns directly, and the CLI passes a
// single ARN as its value. The Ref must resolve to a list for the role to
// provision at all (this is what `cdk bootstrap` deploys).
func TestCreateStack_IAMRole_commaDelimitedListPolicyParameter(t *testing.T) {
	// Given: a role whose ManagedPolicyArns is a CommaDelimitedList parameter.
	srv := helpers.NewTestServer(t)
	template := `{
  "Parameters": {
    "ExecutionPolicies": {"Type": "CommaDelimitedList", "Default": ""}
  },
  "Resources": {
    "Role": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-list-param-role",
        "AssumeRolePolicyDocument": {"Version":"2012-10-17","Statement":[]},
        "ManagedPolicyArns": {"Ref": "ExecutionPolicies"}
      }
    }
  }
}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":                          {"iam-list-param-stack"},
		"TemplateBody":                       {template},
		"Parameters.member.1.ParameterKey":   {"ExecutionPolicies"},
		"Parameters.member.1.ParameterValue": {iamAdminPolicyArn + ", " + iamReadOnlyPolicyArn},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "iam-list-param-stack", "CREATE_COMPLETE")

	// Then: each comma-separated entry is attached.
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-list-param-role", iamAdminPolicyArn)
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-list-param-role", iamReadOnlyPolicyArn)

	// And: the parameter's empty default resolves to the empty list, not to a
	// scalar that fails validation.
	empty := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"iam-list-param-empty"},
		"TemplateBody": {strings.ReplaceAll(template, "cfn-list-param-role", "cfn-empty-param-role")},
	})
	defer empty.Body.Close()
	helpers.AssertStatus(t, empty, http.StatusOK)
	waitForStackStatus(t, srv, "iam-list-param-empty", "CREATE_COMPLETE")
	assertIAMAttachedPoliciesAbsent(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-empty-param-role", iamAdminPolicyArn)
}

func TestCreateStack_IAMGroup_reconcilesManagedAndInlinePolicies(t *testing.T) {
	// Given: a group with template-owned managed and inline policies.
	srv := helpers.NewTestServer(t)
	stackName := "iam-group-properties"
	createIAMStack(t, srv, stackName, iamGroupTemplate(iamReadOnlyPolicyArn, "old-inline", "s3:GetObject"))
	assertIAMAttachedPolicies(t, srv, "ListAttachedGroupPolicies", "GroupName", "cfn-policy-group", iamReadOnlyPolicyArn)
	assertIAMStringMembers(t, srv, "ListGroupPolicies", url.Values{"GroupName": {"cfn-policy-group"}}, "old-inline")

	// When: CloudFormation replaces both policy sets.
	updateIAMStack(t, srv, stackName, iamGroupTemplate(iamAdminPolicyArn, "new-inline", "sqs:SendMessage"))

	// Then: old relationships are removed and replacements are visible.
	assertIAMAttachedPolicies(t, srv, "ListAttachedGroupPolicies", "GroupName", "cfn-policy-group", iamAdminPolicyArn)
	assertIAMAttachedPoliciesAbsent(t, srv, "ListAttachedGroupPolicies", "GroupName", "cfn-policy-group", iamReadOnlyPolicyArn)
	assertIAMStringMembers(t, srv, "ListGroupPolicies", url.Values{"GroupName": {"cfn-policy-group"}}, "new-inline")
	assertIAMStringMembersAbsent(t, srv, "ListGroupPolicies", url.Values{"GroupName": {"cfn-policy-group"}}, "old-inline")

	// When: the stack is deleted.
	deleteResp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)

	// Then: the group's own attachments were detached first, so IAM's
	// DeleteConflict does not strand the group.
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")
	getResp := iamQuery(t, srv, "GetGroup", url.Values{"GroupName": {"cfn-policy-group"}})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
}

func TestCreateStack_IAMUser_reconcilesGroupsAndPolicies(t *testing.T) {
	// Given: a user assigned to a group with managed and inline permissions.
	srv := helpers.NewTestServer(t)
	stackName := "iam-user-properties"
	createIAMStack(t, srv, stackName, iamUserTemplate("FirstGroup", iamReadOnlyPolicyArn, "old-inline", "s3:GetObject", "created"))

	// Then: IAM exposes the CloudFormation-created user configuration.
	user := getIAMUser(t, srv, "cfn-policy-user")
	if user.Path != "/application/" {
		t.Fatalf("user scalar properties: %+v", user)
	}
	boundaryArn := "arn:aws:iam::000000000000:policy/cfn-user-boundary"
	if user.PermissionsBoundaryArn != boundaryArn {
		t.Fatalf("user permissions boundary: got %q, want %q", user.PermissionsBoundaryArn, boundaryArn)
	}
	assertIAMStringMembers(t, srv, "ListGroupsForUser", url.Values{"UserName": {"cfn-policy-user"}}, "cfn-first-group")
	assertIAMStringMembers(t, srv, "ListUserPolicies", url.Values{"UserName": {"cfn-policy-user"}}, "old-inline")
	assertIAMAttachedPolicies(t, srv, "ListAttachedUserPolicies", "UserName", "cfn-policy-user", iamReadOnlyPolicyArn)
	assertIAMInlinePolicyDocument(t, srv, "GetUserPolicy", "UserName", "cfn-policy-user", "old-inline", "s3:GetObject")
	assertIAMTags(t, srv, "ListUserTags", "UserName", "cfn-policy-user", map[string]string{"stage": "created"})

	// When: the user moves to the other group and receives replacement policies.
	updateIAMStack(t, srv, stackName, iamUserTemplate("SecondGroup", iamAdminPolicyArn, "new-inline", "sqs:SendMessage", "updated"))

	// Then: group, policy and tag removals/additions match the new template.
	assertIAMStringMembers(t, srv, "ListGroupsForUser", url.Values{"UserName": {"cfn-policy-user"}}, "cfn-second-group")
	assertIAMStringMembersAbsent(t, srv, "ListGroupsForUser", url.Values{"UserName": {"cfn-policy-user"}}, "cfn-first-group")
	assertIAMStringMembers(t, srv, "ListUserPolicies", url.Values{"UserName": {"cfn-policy-user"}}, "new-inline")
	assertIAMStringMembersAbsent(t, srv, "ListUserPolicies", url.Values{"UserName": {"cfn-policy-user"}}, "old-inline")
	assertIAMAttachedPolicies(t, srv, "ListAttachedUserPolicies", "UserName", "cfn-policy-user", iamAdminPolicyArn)
	assertIAMAttachedPoliciesAbsent(t, srv, "ListAttachedUserPolicies", "UserName", "cfn-policy-user", iamReadOnlyPolicyArn)
	assertIAMTags(t, srv, "ListUserTags", "UserName", "cfn-policy-user", map[string]string{"stage": "updated"})
}

func TestCreateStack_IAMManagedPolicy_reconcilesPrincipalAttachments(t *testing.T) {
	// Given: a managed policy attached to the first role, user, and group.
	srv := helpers.NewTestServer(t)
	stackName := "iam-managed-policy-attachments"
	createIAMStack(t, srv, stackName, iamManagedPolicyTemplate("FirstRole", "FirstUser", "FirstGroup", "s3:GetObject"))
	policyArn := "arn:aws:iam::000000000000:policy/cfn-owned-policy"

	// Then: IAM exposes every principal attachment and the description.
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-first-role", policyArn)
	assertIAMAttachedPolicies(t, srv, "ListAttachedUserPolicies", "UserName", "cfn-first-user", policyArn)
	assertIAMAttachedPolicies(t, srv, "ListAttachedGroupPolicies", "GroupName", "cfn-first-group", policyArn)
	policy := getIAMPolicy(t, srv, policyArn)
	if policy.Description != "owned by CloudFormation" {
		t.Fatalf("policy description: got %q", policy.Description)
	}
	if policy.DefaultVersionID != "v1" {
		t.Fatalf("policy default version: got %q, want v1", policy.DefaultVersionID)
	}

	// When: CloudFormation moves the policy to the second principal set and
	// changes the document.
	updateIAMStack(t, srv, stackName, iamManagedPolicyTemplate("SecondRole", "SecondUser", "SecondGroup", "sqs:SendMessage"))

	// Then: new principals are attached, old principals are detached, and the
	// document was updated in place under the same ARN.
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-second-role", policyArn)
	assertIAMAttachedPoliciesAbsent(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-first-role", policyArn)
	assertIAMAttachedPolicies(t, srv, "ListAttachedUserPolicies", "UserName", "cfn-second-user", policyArn)
	assertIAMAttachedPoliciesAbsent(t, srv, "ListAttachedUserPolicies", "UserName", "cfn-first-user", policyArn)
	assertIAMAttachedPolicies(t, srv, "ListAttachedGroupPolicies", "GroupName", "cfn-second-group", policyArn)
	assertIAMAttachedPoliciesAbsent(t, srv, "ListAttachedGroupPolicies", "GroupName", "cfn-first-group", policyArn)
	if policy := getIAMPolicy(t, srv, policyArn); policy.DefaultVersionID != "v2" {
		t.Fatalf("updated policy default version: got %q, want v2", policy.DefaultVersionID)
	}

	// When: the stack is deleted.
	deleteResp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")

	// Then: the policy is actually gone rather than leaving dangling attachments.
	getResp := iamQuery(t, srv, "GetPolicy", url.Values{"PolicyArn": {policyArn}})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
}

func TestCreateStack_IAMUser_unsupportedLoginProfileFailsLoudly(t *testing.T) {
	// Given: a user property whose IAM operation is not emulated.
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"User":{"Type":"AWS::IAM::User","Properties":{"UserName":"login-user","LoginProfile":{"Password":"Password123!","PasswordResetRequired":true}}}}}`

	// When: CloudFormation creates the stack.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {"iam-login-profile"}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the unsupported property fails the resource instead of being
	// silently dropped, and the half-created user is cleaned up.
	waitForStackStatus(t, srv, "iam-login-profile", "ROLLBACK_COMPLETE")
	getResp := iamQuery(t, srv, "GetUser", url.Values{"UserName": {"login-user"}})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
}

func TestDeleteStack_IAMUser_preservesOutOfBandGroupMembership(t *testing.T) {
	// Given: a stack-owned user later added to an out-of-band group.
	srv := helpers.NewTestServer(t)
	stackName := "iam-user-external-group"
	createIAMStack(t, srv, stackName, iamUserTemplate("FirstGroup", iamReadOnlyPolicyArn, "owned-inline", "s3:GetObject", "owned"))
	assertIAMSuccess(t, srv, "CreateGroup", url.Values{"GroupName": {"external-group"}})
	assertIAMSuccess(t, srv, "AddUserToGroup", url.Values{"UserName": {"cfn-policy-user"}, "GroupName": {"external-group"}})

	// When: stack deletion removes only relationships the template recorded.
	resp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_FAILED")

	// Then: the external relationship survives, and the refused deletion
	// restored the stack-owned membership it had removed.
	assertIAMStringMembers(t, srv, "ListGroupsForUser", url.Values{"UserName": {"cfn-policy-user"}}, "external-group")
	assertIAMStringMembers(t, srv, "ListGroupsForUser", url.Values{"UserName": {"cfn-policy-user"}}, "cfn-first-group")
}

func TestDeleteStack_IAMRoleWithTemplateAttachments_completesTeardown(t *testing.T) {
	// Given: a role whose managed and inline permissions come from its own
	// properties (the #710 hazard: honouring them on Create must not break
	// teardown, because DeleteRole answers DeleteConflict while they remain).
	srv := helpers.NewTestServer(t)
	stackName := "iam-role-attachment-teardown"
	createIAMStack(t, srv, stackName, iamRoleTemplate(iamReadOnlyPolicyArn, "owned-inline", "s3:GetObject", "owned"))

	// When: the stack is deleted.
	resp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: teardown completes — the handler detached what it attached.
	waitForStackStatus(t, srv, stackName, "DELETE_COMPLETE")
	getResp := iamQuery(t, srv, "GetRole", url.Values{"RoleName": {"cfn-policy-role"}})
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
}

func TestDeleteStack_IAMManagedPolicy_preservesOutOfBandPrincipal(t *testing.T) {
	// Given: a stack-owned policy later attached to an out-of-band role.
	srv := helpers.NewTestServer(t)
	stackName := "iam-policy-external-role"
	createIAMStack(t, srv, stackName, iamManagedPolicyTemplate("FirstRole", "FirstUser", "FirstGroup", "s3:GetObject"))
	policyArn := "arn:aws:iam::000000000000:policy/cfn-owned-policy"
	assertIAMSuccess(t, srv, "CreateRole", url.Values{"RoleName": {"external-role"}, "AssumeRolePolicyDocument": {"{}"}})
	assertIAMSuccess(t, srv, "AttachRolePolicy", url.Values{"RoleName": {"external-role"}, "PolicyArn": {policyArn}})

	// When: CloudFormation attempts to delete the policy.
	resp := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_FAILED")

	// Then: no out-of-band attachment was swept, and the refused deletion
	// restored the template-owned attachment it had detached.
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "external-role", policyArn)
	assertIAMAttachedPolicies(t, srv, "ListAttachedRolePolicies", "RoleName", "cfn-first-role", policyArn)
}

// ─── Templates ──────────────────────────────────────────────────────────────

func iamRoleTemplate(managedPolicyArn, policyName, action, tagValue string) string {
	return `{
  "Resources": {
    "BoundaryPolicy": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "cfn-boundary-policy",
        "PolicyDocument": {"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}
      }
    },
    "Role": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-policy-role",
        "Path": "/application/",
        "Description": "created by CloudFormation",
        "MaxSessionDuration": 7200,
        "PermissionsBoundary": {"Ref": "BoundaryPolicy"},
        "Tags": [{"Key":"stage","Value":"` + tagValue + `"}],
        "AssumeRolePolicyDocument": {"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]},
        "ManagedPolicyArns": ["` + managedPolicyArn + `"],
        "Policies": [{"PolicyName":"` + policyName + `","PolicyDocument":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"` + action + `","Resource":"*"}]}}]
      }
    }
  }
}`
}

func iamRoleResetTemplate() string {
	return `{
  "Resources": {
    "BoundaryPolicy": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "cfn-boundary-policy",
        "PolicyDocument": {"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}
      }
    },
    "Role": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-policy-role",
        "Path": "/application/",
        "AssumeRolePolicyDocument": {"Version":"2012-10-17","Statement":[]},
        "ManagedPolicyArns": ["` + iamAdminPolicyArn + `"],
        "Policies": [{"PolicyName":"new-inline","PolicyDocument":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:SendMessage","Resource":"*"}]}}]
      }
    }
  }
}`
}

func iamGroupTemplate(managedPolicyArn, policyName, action string) string {
	return `{"Resources":{"Group":{"Type":"AWS::IAM::Group","Properties":{"GroupName":"cfn-policy-group","ManagedPolicyArns":["` + managedPolicyArn + `"],"Policies":[{"PolicyName":"` + policyName + `","PolicyDocument":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"` + action + `","Resource":"*"}]}}]}}}}`
}

func iamUserTemplate(groupLogicalID, managedPolicyArn, policyName, action, tagValue string) string {
	return `{
  "Resources": {
    "UserBoundary": {
      "Type": "AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName": "cfn-user-boundary",
        "PolicyDocument": {"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}
      }
    },
    "FirstGroup": {"Type":"AWS::IAM::Group","Properties":{"GroupName":"cfn-first-group"}},
    "SecondGroup": {"Type":"AWS::IAM::Group","Properties":{"GroupName":"cfn-second-group"}},
    "User": {
      "Type": "AWS::IAM::User",
      "Properties": {
        "UserName": "cfn-policy-user",
        "Path": "/application/",
        "PermissionsBoundary": {"Ref": "UserBoundary"},
        "Tags": [{"Key":"stage","Value":"` + tagValue + `"}],
        "Groups": [{"Ref":"` + groupLogicalID + `"}],
        "ManagedPolicyArns": ["` + managedPolicyArn + `"],
        "Policies": [{"PolicyName":"` + policyName + `","PolicyDocument":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"` + action + `","Resource":"*"}]}}]
      }
    }
  }
}`
}

func iamManagedPolicyTemplate(roleLogicalID, userLogicalID, groupLogicalID, action string) string {
	return `{
  "Resources": {
    "FirstRole": {"Type":"AWS::IAM::Role","Properties":{"RoleName":"cfn-first-role","AssumeRolePolicyDocument":{}}},
    "SecondRole": {"Type":"AWS::IAM::Role","Properties":{"RoleName":"cfn-second-role","AssumeRolePolicyDocument":{}}},
    "FirstUser": {"Type":"AWS::IAM::User","Properties":{"UserName":"cfn-first-user"}},
    "SecondUser": {"Type":"AWS::IAM::User","Properties":{"UserName":"cfn-second-user"}},
    "FirstGroup": {"Type":"AWS::IAM::Group","Properties":{"GroupName":"cfn-first-group"}},
    "SecondGroup": {"Type":"AWS::IAM::Group","Properties":{"GroupName":"cfn-second-group"}},
    "Policy": {
      "Type":"AWS::IAM::ManagedPolicy",
      "Properties": {
        "ManagedPolicyName":"cfn-owned-policy",
        "Description":"owned by CloudFormation",
        "PolicyDocument":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"` + action + `","Resource":"*"}]},
        "Roles":[{"Ref":"` + roleLogicalID + `"}],
        "Users":[{"Ref":"` + userLogicalID + `"}],
        "Groups":[{"Ref":"` + groupLogicalID + `"}]
      }
    }
  }
}`
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func createIAMStack(t *testing.T, srv *helpers.TestServer, stackName, template string) {
	t.Helper()
	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "CREATE_COMPLETE", "ROLLBACK_COMPLETE", "ROLLBACK_FAILED"); status != "CREATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("create stack ended in %s: %s", status, helpers.ReadBody(t, events))
	}
}

func updateIAMStack(t *testing.T, srv *helpers.TestServer, stackName, template string) {
	t.Helper()
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if status := waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE", "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_ROLLBACK_FAILED"); status != "UPDATE_COMPLETE" {
		events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {stackName}})
		defer events.Body.Close()
		t.Fatalf("update stack ended in %s: %s", status, helpers.ReadBody(t, events))
	}
}

func assertIAMSuccess(t *testing.T, srv *helpers.TestServer, action string, params url.Values) {
	t.Helper()
	resp := iamQuery(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

type iamRoleProperties struct {
	Path                   string `xml:"GetRoleResult>Role>Path"`
	Description            string `xml:"GetRoleResult>Role>Description"`
	MaxSessionDuration     int    `xml:"GetRoleResult>Role>MaxSessionDuration"`
	PermissionsBoundaryArn string `xml:"GetRoleResult>Role>PermissionsBoundary>PermissionsBoundaryArn"`
}

func getIAMRole(t *testing.T, srv *helpers.TestServer, roleName string) iamRoleProperties {
	t.Helper()
	resp := iamQuery(t, srv, "GetRole", url.Values{"RoleName": {roleName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out iamRoleProperties
	helpers.DecodeXML(t, resp, &out)
	return out
}

type iamUserProperties struct {
	Path                   string `xml:"GetUserResult>User>Path"`
	PermissionsBoundaryArn string `xml:"GetUserResult>User>PermissionsBoundary>PermissionsBoundaryArn"`
}

func getIAMUser(t *testing.T, srv *helpers.TestServer, userName string) iamUserProperties {
	t.Helper()
	resp := iamQuery(t, srv, "GetUser", url.Values{"UserName": {userName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out iamUserProperties
	helpers.DecodeXML(t, resp, &out)
	return out
}

type iamPolicyProperties struct {
	Description      string `xml:"GetPolicyResult>Policy>Description"`
	DefaultVersionID string `xml:"GetPolicyResult>Policy>DefaultVersionId"`
}

func getIAMPolicy(t *testing.T, srv *helpers.TestServer, policyArn string) iamPolicyProperties {
	t.Helper()
	resp := iamQuery(t, srv, "GetPolicy", url.Values{"PolicyArn": {policyArn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out iamPolicyProperties
	helpers.DecodeXML(t, resp, &out)
	return out
}

func assertIAMStringMembers(t *testing.T, srv *helpers.TestServer, action string, params url.Values, want string) {
	t.Helper()
	if !slices.Contains(iamStringMembers(t, srv, action, params), want) {
		t.Fatalf("%s: missing %q", action, want)
	}
}

func assertIAMStringMembersAbsent(t *testing.T, srv *helpers.TestServer, action string, params url.Values, unwanted string) {
	t.Helper()
	if slices.Contains(iamStringMembers(t, srv, action, params), unwanted) {
		t.Fatalf("%s: unexpectedly contains %q", action, unwanted)
	}
}

func iamStringMembers(t *testing.T, srv *helpers.TestServer, action string, params url.Values) []string {
	t.Helper()
	resp := iamQuery(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	element := "member"
	if action == "ListGroupsForUser" {
		element = "GroupName"
	}
	dec := xml.NewDecoder(strings.NewReader(body))
	values := make([]string, 0)
	for {
		token, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != element {
			continue
		}
		var value string
		if err := dec.DecodeElement(&value, &start); err == nil && value != "" {
			values = append(values, value)
		}
	}
	return values
}

func assertIAMAttachedPolicies(t *testing.T, srv *helpers.TestServer, action, principalParam, principal, want string) {
	t.Helper()
	if !slices.Contains(iamAttachedPolicyArns(t, srv, action, principalParam, principal), want) {
		t.Fatalf("%s %s: missing %q", action, principal, want)
	}
}

func assertIAMAttachedPoliciesAbsent(t *testing.T, srv *helpers.TestServer, action, principalParam, principal, unwanted string) {
	t.Helper()
	if slices.Contains(iamAttachedPolicyArns(t, srv, action, principalParam, principal), unwanted) {
		t.Fatalf("%s %s: unexpectedly contains %q", action, principal, unwanted)
	}
}

func iamAttachedPolicyArns(t *testing.T, srv *helpers.TestServer, action, principalParam, principal string) []string {
	t.Helper()
	resp := iamQuery(t, srv, action, url.Values{principalParam: {principal}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	arns := make([]string, 0)
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		token, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "PolicyArn" {
			continue
		}
		var arn string
		if err := dec.DecodeElement(&arn, &start); err == nil {
			arns = append(arns, arn)
		}
	}
	return arns
}

func assertIAMInlinePolicyDocument(t *testing.T, srv *helpers.TestServer, action, principalParam, principal, policyName, wantAction string) {
	t.Helper()
	resp := iamQuery(t, srv, action, url.Values{principalParam: {principal}, "PolicyName": {policyName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	start := strings.Index(body, "<PolicyDocument>")
	end := strings.Index(body, "</PolicyDocument>")
	if start < 0 || end < 0 {
		t.Fatalf("%s response missing PolicyDocument: %s", action, body)
	}
	encoded := body[start+len("<PolicyDocument>") : end]
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		t.Fatalf("decode policy document: %v", err)
	}
	if !json.Valid([]byte(decoded)) {
		t.Fatalf("policy document is not JSON: %q", decoded)
	}
	if !strings.Contains(decoded, wantAction) {
		t.Fatalf("policy document does not contain %q: %s", wantAction, decoded)
	}
}

func assertIAMTags(t *testing.T, srv *helpers.TestServer, action, principalParam, principal string, want map[string]string) {
	t.Helper()
	resp := iamQuery(t, srv, action, url.Values{principalParam: {principal}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	for key, value := range want {
		if !strings.Contains(body, "<Key>"+key+"</Key>") || !strings.Contains(body, "<Value>"+value+"</Value>") {
			t.Fatalf("%s: missing tag %s=%s in %s", action, key, value, body)
		}
	}
}
