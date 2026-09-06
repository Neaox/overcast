package iam_test

// Coverage for the IAM operations CloudFormation's in-place updates dispatch
// (issue #521): UpdateRole, CreatePolicyVersion, and the CreateRole /
// CreatePolicy scalar properties that used to be dropped. Test shapes derived
// from the earlier salvage branch codex/issue-521-iam-cfn.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestUpdateRole_scalarPropertiesAndBoundaryRemoval(t *testing.T) {
	// Given: a role with non-default scalar properties and a boundary.
	srv := helpers.NewTestServer(t)
	boundaryArn := createPolicy(t, srv, "role-boundary")
	create := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"updated-role"},
		"AssumeRolePolicyDocument": {validAssumePolicy},
		"Description":              {"before"},
		"MaxSessionDuration":       {"7200"},
		"PermissionsBoundary":      {boundaryArn},
		"Tags.member.1.Key":        {"environment"},
		"Tags.member.1.Value":      {"test"},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	// When: the scalar properties are updated and the boundary is removed.
	update := iamCall(t, srv, "UpdateRole", url.Values{
		"RoleName":           {"updated-role"},
		"Description":        {"after"},
		"MaxSessionDuration": {"3600"},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	if body := helpers.ReadBody(t, update); !strings.Contains(body, "<UpdateRoleResponse") || !strings.Contains(body, "<UpdateRoleResult>") || strings.Contains(body, "<Role>") {
		t.Fatalf("UpdateRole body = %s", body)
	}
	removeBoundary := iamCall(t, srv, "DeleteRolePermissionsBoundary", url.Values{"RoleName": {"updated-role"}})
	defer removeBoundary.Body.Close()
	helpers.AssertStatus(t, removeBoundary, http.StatusOK)

	// Then: GetRole exposes the new values and no boundary.
	get := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"updated-role"}})
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	body := helpers.ReadBody(t, get)
	for _, want := range []string{"<Description>after</Description>", "<MaxSessionDuration>3600</MaxSessionDuration>"} {
		if !strings.Contains(body, want) {
			t.Errorf("GetRole body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "PermissionsBoundaryArn") {
		t.Fatalf("GetRole retained removed permissions boundary: %s", body)
	}

	// And: the tags applied at creation are still there.
	tags := iamCall(t, srv, "ListRoleTags", url.Values{"RoleName": {"updated-role"}})
	defer tags.Body.Close()
	helpers.AssertStatus(t, tags, http.StatusOK)
	if body := helpers.ReadBody(t, tags); !strings.Contains(body, "<Key>environment</Key>") || !strings.Contains(body, "<Value>test</Value>") {
		t.Fatalf("ListRoleTags body = %s", body)
	}
}

func TestUpdateRole_distinguishesClearFromOmit(t *testing.T) {
	// Given: a role with a description.
	srv := helpers.NewTestServer(t)
	create := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"described-role"},
		"AssumeRolePolicyDocument": {validAssumePolicy},
		"Description":              {"keep or clear"},
	})
	create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	// When: UpdateRole omits Description entirely.
	omitted := iamCall(t, srv, "UpdateRole", url.Values{"RoleName": {"described-role"}, "MaxSessionDuration": {"7200"}})
	omitted.Body.Close()
	helpers.AssertStatus(t, omitted, http.StatusOK)

	// Then: the description is left alone.
	get := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"described-role"}})
	body := helpers.ReadBody(t, get)
	get.Body.Close()
	if !strings.Contains(body, "<Description>keep or clear</Description>") {
		t.Fatalf("omitted Description was not preserved: %s", body)
	}

	// When: UpdateRole sends an explicitly empty Description.
	cleared := iamCall(t, srv, "UpdateRole", url.Values{"RoleName": {"described-role"}, "Description": {""}})
	cleared.Body.Close()
	helpers.AssertStatus(t, cleared, http.StatusOK)

	// Then: the description is cleared, as AWS's UpdateRole does.
	get = iamCall(t, srv, "GetRole", url.Values{"RoleName": {"described-role"}})
	body = helpers.ReadBody(t, get)
	get.Body.Close()
	if strings.Contains(body, "<Description>") {
		t.Fatalf("explicit empty Description was not cleared: %s", body)
	}
}

func TestUpdateRole_rejectsBadInput(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "constrained-role")
	tests := map[string]struct {
		params url.Values
		status int
		code   string
	}{
		"missing role name":  {url.Values{"Description": {"x"}}, http.StatusBadRequest, "MissingParameter"},
		"unknown role":       {url.Values{"RoleName": {"missing-role"}, "Description": {"x"}}, http.StatusNotFound, "NoSuchEntity"},
		"duration too short": {url.Values{"RoleName": {"constrained-role"}, "MaxSessionDuration": {"600"}}, http.StatusBadRequest, "ValidationError"},
		"duration too long":  {url.Values{"RoleName": {"constrained-role"}, "MaxSessionDuration": {"90000"}}, http.StatusBadRequest, "ValidationError"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp := iamCall(t, srv, "UpdateRole", tc.params)
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, tc.status)
			if body := helpers.ReadBody(t, resp); !strings.Contains(body, "<Code>"+tc.code+"</Code>") {
				t.Fatalf("UpdateRole body = %s", body)
			}
		})
	}
}

func TestCreateRole_rejectsOutOfRangeMaxSessionDuration(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"short-session-role"},
		"AssumeRolePolicyDocument": {validAssumePolicy},
		"MaxSessionDuration":       {"60"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	if body := helpers.ReadBody(t, resp); !strings.Contains(body, "<Code>ValidationError</Code>") {
		t.Fatalf("CreateRole body = %s", body)
	}
}

func TestGetRole_defaultsMaxSessionDuration(t *testing.T) {
	// Given: a role created without MaxSessionDuration.
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "default-duration-role")

	// Then: GetRole reports AWS's 3600-second default.
	get := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"default-duration-role"}})
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	if body := helpers.ReadBody(t, get); !strings.Contains(body, "<MaxSessionDuration>3600</MaxSessionDuration>") {
		t.Fatalf("GetRole body = %s", body)
	}
}

func TestManagedPolicy_versionsDriveDefaultDocument(t *testing.T) {
	// Given: a customer managed policy.
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "versioned-policy")
	updatedDocument := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sqs:*","Resource":"*"}]}`

	// When: a second version is created and made default.
	createVersion := iamCall(t, srv, "CreatePolicyVersion", url.Values{
		"PolicyArn":      {arn},
		"PolicyDocument": {updatedDocument},
		"SetAsDefault":   {"true"},
	})
	defer createVersion.Body.Close()
	helpers.AssertStatus(t, createVersion, http.StatusOK)
	versionBody := helpers.ReadBody(t, createVersion)
	if !strings.Contains(versionBody, "<CreatePolicyVersionResponse") || !strings.Contains(versionBody, "<VersionId>v2</VersionId>") || !strings.Contains(versionBody, "<IsDefaultVersion>true</IsDefaultVersion>") {
		t.Fatalf("CreatePolicyVersion response = %s", versionBody)
	}

	// Then: GetPolicy identifies the new operative version.
	get := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {arn}})
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	if body := helpers.ReadBody(t, get); !strings.Contains(body, "<DefaultVersionId>v2</DefaultVersionId>") {
		t.Fatalf("GetPolicy body = %s", body)
	}
}

func TestCreatePolicyVersion_nonDefaultLeavesOperativeDocument(t *testing.T) {
	// Given: a customer managed policy.
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "non-default-version")

	// When: a version is created without SetAsDefault.
	created := iamCall(t, srv, "CreatePolicyVersion", url.Values{
		"PolicyArn":      {arn},
		"PolicyDocument": {validIdentityPolicy},
	})
	defer created.Body.Close()
	helpers.AssertStatus(t, created, http.StatusOK)
	if body := helpers.ReadBody(t, created); !strings.Contains(body, "<VersionId>v2</VersionId>") || !strings.Contains(body, "<IsDefaultVersion>false</IsDefaultVersion>") {
		t.Fatalf("CreatePolicyVersion response = %s", body)
	}

	// Then: v1 stays the operative version.
	get := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {arn}})
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	if body := helpers.ReadBody(t, get); !strings.Contains(body, "<DefaultVersionId>v1</DefaultVersionId>") {
		t.Fatalf("GetPolicy body = %s", body)
	}
}

func TestCreatePolicyVersion_missingPolicyIsNoSuchEntity(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := iamCall(t, srv, "CreatePolicyVersion", url.Values{
		"PolicyArn":      {"arn:aws:iam::000000000000:policy/absent"},
		"PolicyDocument": {validIdentityPolicy},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	if body := helpers.ReadBody(t, resp); !strings.Contains(body, "<Code>NoSuchEntity</Code>") {
		t.Fatalf("CreatePolicyVersion body = %s", body)
	}
}

func TestCreatePolicyAndRole_descriptionRoundTrips(t *testing.T) {
	// Given: a policy and a role created with descriptions.
	srv := helpers.NewTestServer(t)
	createResp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {"described-policy"},
		"PolicyDocument": {validIdentityPolicy},
		"Description":    {"policy description"},
	})
	createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	roleResp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"described-role"},
		"AssumeRolePolicyDocument": {validAssumePolicy},
		"Description":              {"role description"},
	})
	roleResp.Body.Close()
	helpers.AssertStatus(t, roleResp, http.StatusOK)

	// Then: both descriptions read back.
	getPolicy := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {"arn:aws:iam::000000000000:policy/described-policy"}})
	policyBody := helpers.ReadBody(t, getPolicy)
	getPolicy.Body.Close()
	if !strings.Contains(policyBody, "<Description>policy description</Description>") {
		t.Fatalf("GetPolicy body = %s", policyBody)
	}
	getRole := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"described-role"}})
	roleBody := helpers.ReadBody(t, getRole)
	getRole.Body.Close()
	if !strings.Contains(roleBody, "<Description>role description</Description>") {
		t.Fatalf("GetRole body = %s", roleBody)
	}
}
