// Package iam_test contains integration tests for the IAM emulator.
//
// Run: go test ./tests/integration/iam/...
package iam_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// iamCall performs an IAM Query-protocol request.
func iamCall(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2010-05-08")
	body := strings.NewReader(params.Encode())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	if err != nil {
		t.Fatalf("iamCall: new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("iamCall: do: %v", err)
	}
	return resp
}

// createUser creates an IAM user and asserts success.
func createUser(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := iamCall(t, srv, "CreateUser", url.Values{"UserName": {name}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// createRole creates an IAM role and asserts success.
func createRole(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {name},
		"AssumeRolePolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// createPolicy creates a managed policy and returns its ARN.
func createPolicy(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {name},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	start := strings.Index(body, "<Arn>")
	end := strings.Index(body, "</Arn>")
	if start == -1 || end == -1 {
		t.Fatal("CreatePolicy response missing Arn element")
	}
	return body[start+5 : end]
}

// createGroup creates an IAM group and asserts success.
func createGroup(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := iamCall(t, srv, "CreateGroup", url.Values{"GroupName": {name}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// createInstanceProfile creates an instance profile and asserts success.
func createInstanceProfile(t *testing.T, srv *helpers.TestServer, name string) {
	t.Helper()
	resp := iamCall(t, srv, "CreateInstanceProfile", url.Values{"InstanceProfileName": {name}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── CreateUser ───────────────────────────────────────────────────────────────

func TestCreateUser_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateUser is called
	resp := iamCall(t, srv, "CreateUser", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK and response contains user fields
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<UserName>alice</UserName>") {
		t.Errorf("expected UserName in response, got: %s", body)
	}
	if !strings.Contains(body, "<UserId>") {
		t.Errorf("expected UserId in response, got: %s", body)
	}
	if !strings.Contains(body, "<Arn>") {
		t.Errorf("expected Arn in response, got: %s", body)
	}
}

func TestCreateUser_withPath(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateUser is called with a custom Path
	resp := iamCall(t, srv, "CreateUser", url.Values{
		"UserName": {"bob"},
		"Path":     {"/engineering/"},
	})
	defer resp.Body.Close()

	// Then: 200 OK and response contains the path
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<Path>/engineering/</Path>") {
		t.Errorf("expected Path in response, got: %s", body)
	}
}

func TestCreateUser_duplicate(t *testing.T) {
	// Given: a user already exists
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: CreateUser is called with the same name
	resp := iamCall(t, srv, "CreateUser", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 409 Conflict (EntityAlreadyExists)
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

// ─── GetUser ──────────────────────────────────────────────────────────────────

func TestGetUser_success(t *testing.T) {
	// Given: a user exists
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: GetUser is called
	resp := iamCall(t, srv, "GetUser", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK with user details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<UserName>alice</UserName>") {
		t.Errorf("expected UserName in response, got: %s", body)
	}
}

func TestGetUser_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: GetUser is called for a non-existent user
	resp := iamCall(t, srv, "GetUser", url.Values{"UserName": {"ghost"}})
	defer resp.Body.Close()

	// Then: 404 NoSuchEntity
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── ListUsers ────────────────────────────────────────────────────────────────

func TestListUsers_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: ListUsers is called
	resp := iamCall(t, srv, "ListUsers", nil)
	defer resp.Body.Close()

	// Then: 200 OK with empty list
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<IsTruncated>false</IsTruncated>") {
		t.Errorf("expected IsTruncated=false, got: %s", body)
	}
}

func TestListUsers_afterCreate(t *testing.T) {
	// Given: two users exist
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	createUser(t, srv, "bob")

	// When: ListUsers is called
	resp := iamCall(t, srv, "ListUsers", nil)
	defer resp.Body.Close()

	// Then: 200 OK and both users are listed
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "alice") {
		t.Errorf("expected alice in response, got: %s", body)
	}
	if !strings.Contains(body, "bob") {
		t.Errorf("expected bob in response, got: %s", body)
	}
}

// ─── UpdateUser ───────────────────────────────────────────────────────────────

func TestUpdateUser_rename(t *testing.T) {
	// Given: a user exists
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: UpdateUser renames the user
	resp := iamCall(t, srv, "UpdateUser", url.Values{
		"UserName":    {"alice"},
		"NewUserName": {"alice2"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: old name is gone
	r2 := iamCall(t, srv, "GetUser", url.Values{"UserName": {"alice"}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusNotFound)

	// And: new name works
	r3 := iamCall(t, srv, "GetUser", url.Values{"UserName": {"alice2"}})
	defer r3.Body.Close()
	helpers.AssertStatus(t, r3, http.StatusOK)
}

func TestUpdateUser_changePath(t *testing.T) {
	// Given: a user exists
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: UpdateUser changes the path
	resp := iamCall(t, srv, "UpdateUser", url.Values{
		"UserName": {"alice"},
		"NewPath":  {"/admin/"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: GetUser shows the new path
	r2 := iamCall(t, srv, "GetUser", url.Values{"UserName": {"alice"}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusOK)
	body := helpers.ReadBody(t, r2)
	if !strings.Contains(body, "<Path>/admin/</Path>") {
		t.Errorf("expected updated path, got: %s", body)
	}
}

// ─── DeleteUser ───────────────────────────────────────────────────────────────

func TestDeleteUser_success(t *testing.T) {
	// Given: a user exists
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: DeleteUser is called
	resp := iamCall(t, srv, "DeleteUser", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the user no longer exists
	r2 := iamCall(t, srv, "GetUser", url.Values{"UserName": {"alice"}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusNotFound)
}

func TestDeleteUser_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: DeleteUser is called for a non-existent user
	resp := iamCall(t, srv, "DeleteUser", url.Values{"UserName": {"ghost"}})
	defer resp.Body.Close()

	// Then: 404 NoSuchEntity
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── CreateAccessKey ──────────────────────────────────────────────────────────

func TestCreateAccessKey_success(t *testing.T) {
	// Given: a user exists
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: CreateAccessKey is called
	resp := iamCall(t, srv, "CreateAccessKey", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK with key details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<AccessKeyId>") {
		t.Errorf("expected AccessKeyId in response, got: %s", body)
	}
	if !strings.Contains(body, "<SecretAccessKey>") {
		t.Errorf("expected SecretAccessKey in response, got: %s", body)
	}
	if !strings.Contains(body, "<Status>Active</Status>") {
		t.Errorf("expected Status=Active in response, got: %s", body)
	}
}

// ─── ListAccessKeys ───────────────────────────────────────────────────────────

func TestListAccessKeys_afterCreate(t *testing.T) {
	// Given: a user with an access key
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	cr := iamCall(t, srv, "CreateAccessKey", url.Values{"UserName": {"alice"}})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: ListAccessKeys is called
	resp := iamCall(t, srv, "ListAccessKeys", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK with key metadata
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<AccessKeyId>") {
		t.Errorf("expected AccessKeyId in list response, got: %s", body)
	}
}

func TestListAccessKeys_empty(t *testing.T) {
	// Given: a user with no access keys
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: ListAccessKeys is called
	resp := iamCall(t, srv, "ListAccessKeys", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── DeleteAccessKey ──────────────────────────────────────────────────────────

func TestDeleteAccessKey_success(t *testing.T) {
	// Given: a user with an access key
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	cr := iamCall(t, srv, "CreateAccessKey", url.Values{"UserName": {"alice"}})
	crBody := helpers.ReadBody(t, cr)
	start := strings.Index(crBody, "<AccessKeyId>")
	end := strings.Index(crBody, "</AccessKeyId>")
	if start == -1 || end == -1 {
		t.Fatal("CreateAccessKey response missing AccessKeyId")
	}
	keyID := crBody[start+len("<AccessKeyId>") : end]

	// When: DeleteAccessKey is called
	resp := iamCall(t, srv, "DeleteAccessKey", url.Values{
		"UserName":    {"alice"},
		"AccessKeyId": {keyID},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the key is no longer listed
	lr := iamCall(t, srv, "ListAccessKeys", url.Values{"UserName": {"alice"}})
	defer lr.Body.Close()
	lrBody := helpers.ReadBody(t, lr)
	if strings.Contains(lrBody, keyID) {
		t.Errorf("deleted key %s still appears in list", keyID)
	}
}

// ─── PutUserPolicy ────────────────────────────────────────────────────────────

func TestPutUserPolicy_success(t *testing.T) {
	// Given: a user exists
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: PutUserPolicy is called
	resp := iamCall(t, srv, "PutUserPolicy", url.Values{
		"UserName":       {"alice"},
		"PolicyName":     {"read-s3"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── GetUserPolicy ────────────────────────────────────────────────────────────

func TestGetUserPolicy_success(t *testing.T) {
	// Given: a user with an inline policy
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	pr := iamCall(t, srv, "PutUserPolicy", url.Values{
		"UserName":       {"alice"},
		"PolicyName":     {"read-s3"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`},
	})
	defer pr.Body.Close()
	helpers.AssertStatus(t, pr, http.StatusOK)

	// When: GetUserPolicy is called
	resp := iamCall(t, srv, "GetUserPolicy", url.Values{
		"UserName":   {"alice"},
		"PolicyName": {"read-s3"},
	})
	defer resp.Body.Close()

	// Then: 200 OK with policy document
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<PolicyName>read-s3</PolicyName>") {
		t.Errorf("expected PolicyName in response, got: %s", body)
	}
	if !strings.Contains(body, "PolicyDocument") {
		t.Errorf("expected PolicyDocument in response, got: %s", body)
	}
}

// ─── DeleteUserPolicy ─────────────────────────────────────────────────────────

func TestDeleteUserPolicy_success(t *testing.T) {
	// Given: a user with an inline policy
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	pr := iamCall(t, srv, "PutUserPolicy", url.Values{
		"UserName":       {"alice"},
		"PolicyName":     {"read-s3"},
		"PolicyDocument": {`{"Version":"2012-10-17"}`},
	})
	defer pr.Body.Close()
	helpers.AssertStatus(t, pr, http.StatusOK)

	// When: DeleteUserPolicy is called
	resp := iamCall(t, srv, "DeleteUserPolicy", url.Values{
		"UserName":   {"alice"},
		"PolicyName": {"read-s3"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── CreateRole ───────────────────────────────────────────────────────────────

func TestCreateRole_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateRole is called
	resp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"test-role"},
		"AssumeRolePolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`},
	})
	defer resp.Body.Close()

	// Then: 200 OK with role details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<RoleName>test-role</RoleName>") {
		t.Errorf("expected RoleName in response, got: %s", body)
	}
	if !strings.Contains(body, "<RoleId>") {
		t.Errorf("expected RoleId in response, got: %s", body)
	}
	if !strings.Contains(body, "<Arn>") {
		t.Errorf("expected Arn in response, got: %s", body)
	}
}

func TestCreateRole_withPath(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateRole is called with a Path
	resp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"path-role"},
		"AssumeRolePolicyDocument": {`{}`},
		"Path":                     {"/service-role/"},
	})
	defer resp.Body.Close()

	// Then: 200 OK and response contains the path
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<Path>/service-role/</Path>") {
		t.Errorf("expected Path in response, got: %s", body)
	}
}

func TestCreateRole_duplicate(t *testing.T) {
	// Given: a role already exists
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")

	// When: CreateRole is called with the same name
	resp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"test-role"},
		"AssumeRolePolicyDocument": {`{}`},
	})
	defer resp.Body.Close()

	// Then: 409 Conflict
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

// ─── GetRole ──────────────────────────────────────────────────────────────────

func TestGetRole_success(t *testing.T) {
	// Given: a role exists
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")

	// When: GetRole is called
	resp := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"test-role"}})
	defer resp.Body.Close()

	// Then: 200 OK with role details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<RoleName>test-role</RoleName>") {
		t.Errorf("expected RoleName in response, got: %s", body)
	}
}

func TestGetRole_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: GetRole is called for a non-existent role
	resp := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"ghost"}})
	defer resp.Body.Close()

	// Then: 404 NoSuchEntity
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── ListRoles ────────────────────────────────────────────────────────────────

func TestListRoles_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: ListRoles is called
	resp := iamCall(t, srv, "ListRoles", nil)
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
}

func TestListRoles_afterCreate(t *testing.T) {
	// Given: a role exists
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "role-a")

	// When: ListRoles is called
	resp := iamCall(t, srv, "ListRoles", nil)
	defer resp.Body.Close()

	// Then: 200 OK and role is listed
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "role-a") {
		t.Errorf("expected role-a in response, got: %s", body)
	}
}

// ─── DeleteRole ───────────────────────────────────────────────────────────────

func TestDeleteRole_success(t *testing.T) {
	// Given: a role exists
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")

	// When: DeleteRole is called
	resp := iamCall(t, srv, "DeleteRole", url.Values{"RoleName": {"test-role"}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the role no longer exists
	r2 := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"test-role"}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusNotFound)
}

func TestDeleteRole_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: DeleteRole is called for a non-existent role
	resp := iamCall(t, srv, "DeleteRole", url.Values{"RoleName": {"ghost"}})
	defer resp.Body.Close()

	// Then: 404 NoSuchEntity
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── PutRolePolicy ────────────────────────────────────────────────────────────

func TestPutRolePolicy_success(t *testing.T) {
	// Given: a role exists
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")

	// When: PutRolePolicy is called
	resp := iamCall(t, srv, "PutRolePolicy", url.Values{
		"RoleName":       {"test-role"},
		"PolicyName":     {"inline-pol"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── GetRolePolicy ────────────────────────────────────────────────────────────

func TestGetRolePolicy_success(t *testing.T) {
	// Given: a role with an inline policy
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")
	pr := iamCall(t, srv, "PutRolePolicy", url.Values{
		"RoleName":       {"test-role"},
		"PolicyName":     {"inline-pol"},
		"PolicyDocument": {`{"Version":"2012-10-17"}`},
	})
	defer pr.Body.Close()
	helpers.AssertStatus(t, pr, http.StatusOK)

	// When: GetRolePolicy is called
	resp := iamCall(t, srv, "GetRolePolicy", url.Values{
		"RoleName":   {"test-role"},
		"PolicyName": {"inline-pol"},
	})
	defer resp.Body.Close()

	// Then: 200 OK with policy details
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<PolicyName>inline-pol</PolicyName>") {
		t.Errorf("expected PolicyName in response, got: %s", body)
	}
	if !strings.Contains(body, "PolicyDocument") {
		t.Errorf("expected PolicyDocument in response, got: %s", body)
	}
}

// ─── ListRolePolicies ─────────────────────────────────────────────────────────

func TestListRolePolicies_success(t *testing.T) {
	// Given: a role with an inline policy
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")
	pr := iamCall(t, srv, "PutRolePolicy", url.Values{
		"RoleName":       {"test-role"},
		"PolicyName":     {"inline-pol"},
		"PolicyDocument": {`{"Version":"2012-10-17"}`},
	})
	defer pr.Body.Close()
	helpers.AssertStatus(t, pr, http.StatusOK)

	// When: ListRolePolicies is called
	resp := iamCall(t, srv, "ListRolePolicies", url.Values{"RoleName": {"test-role"}})
	defer resp.Body.Close()

	// Then: 200 OK with policy names
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "inline-pol") {
		t.Errorf("expected inline-pol in response, got: %s", body)
	}
}

func TestListRolePolicies_empty(t *testing.T) {
	// Given: a role with no inline policies
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")

	// When: ListRolePolicies is called
	resp := iamCall(t, srv, "ListRolePolicies", url.Values{"RoleName": {"test-role"}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── DeleteRolePolicy ─────────────────────────────────────────────────────────

func TestDeleteRolePolicy_success(t *testing.T) {
	// Given: a role with an inline policy
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")
	pr := iamCall(t, srv, "PutRolePolicy", url.Values{
		"RoleName":       {"test-role"},
		"PolicyName":     {"inline-pol"},
		"PolicyDocument": {`{"Version":"2012-10-17"}`},
	})
	defer pr.Body.Close()
	helpers.AssertStatus(t, pr, http.StatusOK)

	// When: DeleteRolePolicy is called
	resp := iamCall(t, srv, "DeleteRolePolicy", url.Values{
		"RoleName":   {"test-role"},
		"PolicyName": {"inline-pol"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the policy is no longer listed
	lr := iamCall(t, srv, "ListRolePolicies", url.Values{"RoleName": {"test-role"}})
	defer lr.Body.Close()
	lrBody := helpers.ReadBody(t, lr)
	if strings.Contains(lrBody, "inline-pol") {
		t.Error("deleted policy still appears in list")
	}
}

// ─── AttachRolePolicy ─────────────────────────────────────────────────────────

func TestAttachRolePolicy_success(t *testing.T) {
	// Given: a role and a managed policy exist
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")
	policyArn := createPolicy(t, srv, "test-policy")

	// When: AttachRolePolicy is called
	resp := iamCall(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  {"test-role"},
		"PolicyArn": {policyArn},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAttachRolePolicy_idempotent(t *testing.T) {
	// Given: a role with a policy already attached
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")
	policyArn := createPolicy(t, srv, "test-policy")
	ar := iamCall(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  {"test-role"},
		"PolicyArn": {policyArn},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: AttachRolePolicy is called again with the same policy
	resp := iamCall(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  {"test-role"},
		"PolicyArn": {policyArn},
	})
	defer resp.Body.Close()

	// Then: 200 OK (idempotent)
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── ListAttachedRolePolicies ─────────────────────────────────────────────────

func TestListAttachedRolePolicies_success(t *testing.T) {
	// Given: a role with an attached policy
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")
	policyArn := createPolicy(t, srv, "test-policy")
	ar := iamCall(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  {"test-role"},
		"PolicyArn": {policyArn},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: ListAttachedRolePolicies is called
	resp := iamCall(t, srv, "ListAttachedRolePolicies", url.Values{"RoleName": {"test-role"}})
	defer resp.Body.Close()

	// Then: 200 OK with the attached policy
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "test-policy") {
		t.Errorf("expected test-policy in response, got: %s", body)
	}
}

func TestListAttachedRolePolicies_empty(t *testing.T) {
	// Given: a role with no attached policies
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")

	// When: ListAttachedRolePolicies is called
	resp := iamCall(t, srv, "ListAttachedRolePolicies", url.Values{"RoleName": {"test-role"}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── DetachRolePolicy ─────────────────────────────────────────────────────────

func TestDetachRolePolicy_success(t *testing.T) {
	// Given: a role with an attached policy
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "test-role")
	policyArn := createPolicy(t, srv, "test-policy")
	ar := iamCall(t, srv, "AttachRolePolicy", url.Values{
		"RoleName":  {"test-role"},
		"PolicyArn": {policyArn},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: DetachRolePolicy is called
	resp := iamCall(t, srv, "DetachRolePolicy", url.Values{
		"RoleName":  {"test-role"},
		"PolicyArn": {policyArn},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the policy is no longer attached
	lr := iamCall(t, srv, "ListAttachedRolePolicies", url.Values{"RoleName": {"test-role"}})
	defer lr.Body.Close()
	lrBody := helpers.ReadBody(t, lr)
	if strings.Contains(lrBody, "test-policy") {
		t.Error("detached policy still appears in list")
	}
}

// ─── CreateInstanceProfile ────────────────────────────────────────────────────

func TestCreateInstanceProfile_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateInstanceProfile is called
	resp := iamCall(t, srv, "CreateInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
	})
	defer resp.Body.Close()

	// Then: 200 OK with instance profile details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<InstanceProfileName>test-profile</InstanceProfileName>") {
		t.Errorf("expected InstanceProfileName in response, got: %s", body)
	}
	if !strings.Contains(body, "<InstanceProfileId>") {
		t.Errorf("expected InstanceProfileId in response, got: %s", body)
	}
	if !strings.Contains(body, "<Arn>") {
		t.Errorf("expected Arn in response, got: %s", body)
	}
}

func TestCreateInstanceProfile_duplicate(t *testing.T) {
	// Given: an instance profile already exists
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "test-profile")

	// When: CreateInstanceProfile is called with the same name
	resp := iamCall(t, srv, "CreateInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
	})
	defer resp.Body.Close()

	// Then: 409 Conflict
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

// ─── GetInstanceProfile ───────────────────────────────────────────────────────

func TestGetInstanceProfile_success(t *testing.T) {
	// Given: an instance profile exists
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "test-profile")

	// When: GetInstanceProfile is called
	resp := iamCall(t, srv, "GetInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
	})
	defer resp.Body.Close()

	// Then: 200 OK with profile details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<InstanceProfileName>test-profile</InstanceProfileName>") {
		t.Errorf("expected InstanceProfileName in response, got: %s", body)
	}
}

func TestGetInstanceProfile_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: GetInstanceProfile is called for a non-existent profile
	resp := iamCall(t, srv, "GetInstanceProfile", url.Values{
		"InstanceProfileName": {"ghost"},
	})
	defer resp.Body.Close()

	// Then: 404 NoSuchEntity
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestGetInstanceProfile_withRole(t *testing.T) {
	// Given: an instance profile with a role attached
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "test-profile")
	createRole(t, srv, "test-role")
	ar := iamCall(t, srv, "AddRoleToInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
		"RoleName":            {"test-role"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: GetInstanceProfile is called
	resp := iamCall(t, srv, "GetInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
	})
	defer resp.Body.Close()

	// Then: 200 OK and the role is included
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "test-role") {
		t.Errorf("expected role in instance profile response, got: %s", body)
	}
}

// ─── DeleteInstanceProfile ────────────────────────────────────────────────────

func TestDeleteInstanceProfile_success(t *testing.T) {
	// Given: an instance profile exists
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "test-profile")

	// When: DeleteInstanceProfile is called
	resp := iamCall(t, srv, "DeleteInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: profile is gone
	r2 := iamCall(t, srv, "GetInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
	})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusNotFound)
}

// ─── AddRoleToInstanceProfile ─────────────────────────────────────────────────

func TestAddRoleToInstanceProfile_success(t *testing.T) {
	// Given: an instance profile and a role exist
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "test-profile")
	createRole(t, srv, "test-role")

	// When: AddRoleToInstanceProfile is called
	resp := iamCall(t, srv, "AddRoleToInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
		"RoleName":            {"test-role"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAddRoleToInstanceProfile_idempotent(t *testing.T) {
	// Given: a role is already added to the instance profile
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "test-profile")
	createRole(t, srv, "test-role")
	ar := iamCall(t, srv, "AddRoleToInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
		"RoleName":            {"test-role"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: AddRoleToInstanceProfile is called again
	resp := iamCall(t, srv, "AddRoleToInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
		"RoleName":            {"test-role"},
	})
	defer resp.Body.Close()

	// Then: 200 OK (idempotent)
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── RemoveRoleFromInstanceProfile ────────────────────────────────────────────

func TestRemoveRoleFromInstanceProfile_success(t *testing.T) {
	// Given: a role is attached to an instance profile
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "test-profile")
	createRole(t, srv, "test-role")
	ar := iamCall(t, srv, "AddRoleToInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
		"RoleName":            {"test-role"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: RemoveRoleFromInstanceProfile is called
	resp := iamCall(t, srv, "RemoveRoleFromInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
		"RoleName":            {"test-role"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the role is no longer in the profile
	gr := iamCall(t, srv, "GetInstanceProfile", url.Values{
		"InstanceProfileName": {"test-profile"},
	})
	defer gr.Body.Close()
	grBody := helpers.ReadBody(t, gr)
	if strings.Contains(grBody, "<RoleName>test-role</RoleName>") {
		t.Error("removed role still appears in instance profile")
	}
}

// ─── ListInstanceProfiles ─────────────────────────────────────────────────────

func TestListInstanceProfiles_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: ListInstanceProfiles is called
	resp := iamCall(t, srv, "ListInstanceProfiles", nil)
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
}

func TestListInstanceProfiles_afterCreate(t *testing.T) {
	// Given: instance profiles exist
	srv := helpers.NewTestServer(t)
	createInstanceProfile(t, srv, "profile-a")
	createInstanceProfile(t, srv, "profile-b")

	// When: ListInstanceProfiles is called
	resp := iamCall(t, srv, "ListInstanceProfiles", nil)
	defer resp.Body.Close()

	// Then: 200 OK and both profiles are listed
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "profile-a") {
		t.Errorf("expected profile-a in response, got: %s", body)
	}
	if !strings.Contains(body, "profile-b") {
		t.Errorf("expected profile-b in response, got: %s", body)
	}
}

// ─── CreatePolicy ─────────────────────────────────────────────────────────────

func TestCreatePolicy_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreatePolicy is called
	resp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {"test-policy"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
	})
	defer resp.Body.Close()

	// Then: 200 OK with policy details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<PolicyName>test-policy</PolicyName>") {
		t.Errorf("expected PolicyName in response, got: %s", body)
	}
	if !strings.Contains(body, "<PolicyId>") {
		t.Errorf("expected PolicyId in response, got: %s", body)
	}
	if !strings.Contains(body, "<Arn>") {
		t.Errorf("expected Arn in response, got: %s", body)
	}
}

func TestCreatePolicy_withPath(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreatePolicy is called with a Path
	resp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {"path-policy"},
		"PolicyDocument": {`{"Version":"2012-10-17"}`},
		"Path":           {"/org/"},
	})
	defer resp.Body.Close()

	// Then: 200 OK and response contains the path
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<Path>/org/</Path>") {
		t.Errorf("expected Path in response, got: %s", body)
	}
}

func TestCreatePolicy_duplicate(t *testing.T) {
	// Given: a policy already exists
	srv := helpers.NewTestServer(t)
	createPolicy(t, srv, "test-policy")

	// When: CreatePolicy is called with the same name
	resp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {"test-policy"},
		"PolicyDocument": {`{"Version":"2012-10-17"}`},
	})
	defer resp.Body.Close()

	// Then: 409 Conflict
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

// ─── GetPolicy ────────────────────────────────────────────────────────────────

func TestGetPolicy_success(t *testing.T) {
	// Given: a policy exists
	srv := helpers.NewTestServer(t)
	policyArn := createPolicy(t, srv, "test-policy")

	// When: GetPolicy is called
	resp := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {policyArn}})
	defer resp.Body.Close()

	// Then: 200 OK with policy details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<PolicyName>test-policy</PolicyName>") {
		t.Errorf("expected PolicyName in response, got: %s", body)
	}
}

func TestGetPolicy_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: GetPolicy is called for a non-existent policy
	resp := iamCall(t, srv, "GetPolicy", url.Values{
		"PolicyArn": {"arn:aws:iam::123456789012:policy/ghost"},
	})
	defer resp.Body.Close()

	// Then: 404 NoSuchEntity
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── ListPolicies ─────────────────────────────────────────────────────────────

func TestListPolicies_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: ListPolicies is called
	resp := iamCall(t, srv, "ListPolicies", nil)
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
}

func TestListPolicies_afterCreate(t *testing.T) {
	// Given: a policy exists
	srv := helpers.NewTestServer(t)
	createPolicy(t, srv, "test-policy")

	// When: ListPolicies is called
	resp := iamCall(t, srv, "ListPolicies", nil)
	defer resp.Body.Close()

	// Then: 200 OK and the policy is listed
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "test-policy") {
		t.Errorf("expected test-policy in response, got: %s", body)
	}
}

// ─── DeletePolicy ─────────────────────────────────────────────────────────────

func TestDeletePolicy_success(t *testing.T) {
	// Given: a policy exists
	srv := helpers.NewTestServer(t)
	policyArn := createPolicy(t, srv, "test-policy")

	// When: DeletePolicy is called
	resp := iamCall(t, srv, "DeletePolicy", url.Values{"PolicyArn": {policyArn}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the policy no longer exists
	r2 := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {policyArn}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusNotFound)
}

// ─── CreateGroup ──────────────────────────────────────────────────────────────

func TestCreateGroup_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateGroup is called
	resp := iamCall(t, srv, "CreateGroup", url.Values{"GroupName": {"developers"}})
	defer resp.Body.Close()

	// Then: 200 OK with group details
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<GroupName>developers</GroupName>") {
		t.Errorf("expected GroupName in response, got: %s", body)
	}
	if !strings.Contains(body, "<GroupId>") {
		t.Errorf("expected GroupId in response, got: %s", body)
	}
	if !strings.Contains(body, "<Arn>") {
		t.Errorf("expected Arn in response, got: %s", body)
	}
}

func TestCreateGroup_withPath(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateGroup is called with a Path
	resp := iamCall(t, srv, "CreateGroup", url.Values{
		"GroupName": {"admins"},
		"Path":      {"/org/"},
	})
	defer resp.Body.Close()

	// Then: 200 OK and response contains the path
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<Path>/org/</Path>") {
		t.Errorf("expected Path in response, got: %s", body)
	}
}

func TestCreateGroup_duplicate(t *testing.T) {
	// Given: a group already exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")

	// When: CreateGroup is called with the same name
	resp := iamCall(t, srv, "CreateGroup", url.Values{"GroupName": {"developers"}})
	defer resp.Body.Close()

	// Then: 409 Conflict
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

// ─── GetGroup ─────────────────────────────────────────────────────────────────

func TestGetGroup_success(t *testing.T) {
	// Given: a group exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")

	// When: GetGroup is called
	resp := iamCall(t, srv, "GetGroup", url.Values{"GroupName": {"developers"}})
	defer resp.Body.Close()

	// Then: 200 OK with group details and Users
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "<GroupName>developers</GroupName>") {
		t.Errorf("expected GroupName in response, got: %s", body)
	}
}

func TestGetGroup_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: GetGroup is called for a non-existent group
	resp := iamCall(t, srv, "GetGroup", url.Values{"GroupName": {"ghost"}})
	defer resp.Body.Close()

	// Then: 404 NoSuchEntity
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestGetGroup_withMembers(t *testing.T) {
	// Given: a group with a user
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")
	ar := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: GetGroup is called
	resp := iamCall(t, srv, "GetGroup", url.Values{"GroupName": {"developers"}})
	defer resp.Body.Close()

	// Then: 200 OK and the group is returned (user details are not resolved)
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "developers") {
		t.Errorf("expected group name in response, got: %s", body)
	}
}

// ─── DeleteGroup ──────────────────────────────────────────────────────────────

func TestDeleteGroup_success(t *testing.T) {
	// Given: a group exists
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")

	// When: DeleteGroup is called
	resp := iamCall(t, srv, "DeleteGroup", url.Values{"GroupName": {"developers"}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the group is gone
	r2 := iamCall(t, srv, "GetGroup", url.Values{"GroupName": {"developers"}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusNotFound)
}

// ─── ListGroups ───────────────────────────────────────────────────────────────

func TestListGroups_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: ListGroups is called
	resp := iamCall(t, srv, "ListGroups", nil)
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
}

func TestListGroups_afterCreate(t *testing.T) {
	// Given: groups exist
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createGroup(t, srv, "admins")

	// When: ListGroups is called
	resp := iamCall(t, srv, "ListGroups", nil)
	defer resp.Body.Close()

	// Then: 200 OK and both groups are listed
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "developers") {
		t.Errorf("expected developers in response, got: %s", body)
	}
	if !strings.Contains(body, "admins") {
		t.Errorf("expected admins in response, got: %s", body)
	}
}

// ─── AddUserToGroup ───────────────────────────────────────────────────────────

func TestAddUserToGroup_success(t *testing.T) {
	// Given: a group and a user exist
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")

	// When: AddUserToGroup is called
	resp := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAddUserToGroup_idempotent(t *testing.T) {
	// Given: a user is already in the group
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")
	ar := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: AddUserToGroup is called again
	resp := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer resp.Body.Close()

	// Then: 200 OK (idempotent)
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── RemoveUserFromGroup ──────────────────────────────────────────────────────

func TestRemoveUserFromGroup_success(t *testing.T) {
	// Given: a user is in a group
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")
	ar := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: RemoveUserFromGroup is called
	resp := iamCall(t, srv, "RemoveUserFromGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: the user is no longer in the group
	gr := iamCall(t, srv, "GetGroup", url.Values{"GroupName": {"developers"}})
	defer gr.Body.Close()
	grBody := helpers.ReadBody(t, gr)
	if strings.Contains(grBody, "<UserName>alice</UserName>") {
		t.Error("removed user still appears in group")
	}
}

// ─── ListGroupsForUser ────────────────────────────────────────────────────────

func TestListGroupsForUser_success(t *testing.T) {
	// Given: a user is in a group
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")
	ar := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	// When: ListGroupsForUser is called
	resp := iamCall(t, srv, "ListGroupsForUser", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK and the group is listed
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "developers") {
		t.Errorf("expected developers in response, got: %s", body)
	}
}

func TestListGroupsForUser_empty(t *testing.T) {
	// Given: a user in no groups
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	// When: ListGroupsForUser is called
	resp := iamCall(t, srv, "ListGroupsForUser", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestListGroupsForUser_multipleGroups(t *testing.T) {
	// Given: a user is in two groups
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createGroup(t, srv, "admins")
	createUser(t, srv, "alice")
	ar1 := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	defer ar1.Body.Close()
	helpers.AssertStatus(t, ar1, http.StatusOK)
	ar2 := iamCall(t, srv, "AddUserToGroup", url.Values{
		"GroupName": {"admins"},
		"UserName":  {"alice"},
	})
	defer ar2.Body.Close()
	helpers.AssertStatus(t, ar2, http.StatusOK)

	// When: ListGroupsForUser is called
	resp := iamCall(t, srv, "ListGroupsForUser", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()

	// Then: 200 OK and both groups are listed
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "developers") {
		t.Errorf("expected developers in response, got: %s", body)
	}
	if !strings.Contains(body, "admins") {
		t.Errorf("expected admins in response, got: %s", body)
	}
}

// ─── Group Inline Policies ────────────────────────────────────────────────────

func TestPutGroupPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")

	resp := iamCall(t, srv, "PutGroupPolicy", url.Values{
		"GroupName":      {"devs"},
		"PolicyName":     {"s3-access"},
		"PolicyDocument": {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestGetGroupPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")
	pr := iamCall(t, srv, "PutGroupPolicy", url.Values{
		"GroupName":      {"devs"},
		"PolicyName":     {"s3-access"},
		"PolicyDocument": {"test-doc"},
	})
	defer pr.Body.Close()
	helpers.AssertStatus(t, pr, http.StatusOK)

	resp := iamCall(t, srv, "GetGroupPolicy", url.Values{
		"GroupName":  {"devs"},
		"PolicyName": {"s3-access"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "s3-access") {
		t.Errorf("expected policy name in response, got: %s", body)
	}
}

func TestGetGroupPolicy_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")

	resp := iamCall(t, srv, "GetGroupPolicy", url.Values{
		"GroupName":  {"devs"},
		"PolicyName": {"nonexistent"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

func TestDeleteGroupPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")
	pr := iamCall(t, srv, "PutGroupPolicy", url.Values{
		"GroupName":      {"devs"},
		"PolicyName":     {"s3-access"},
		"PolicyDocument": {"test-doc"},
	})
	defer pr.Body.Close()

	resp := iamCall(t, srv, "DeleteGroupPolicy", url.Values{
		"GroupName":  {"devs"},
		"PolicyName": {"s3-access"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestListGroupPolicies_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")
	pr := iamCall(t, srv, "PutGroupPolicy", url.Values{
		"GroupName":      {"devs"},
		"PolicyName":     {"s3-access"},
		"PolicyDocument": {"test-doc"},
	})
	defer pr.Body.Close()

	resp := iamCall(t, srv, "ListGroupPolicies", url.Values{"GroupName": {"devs"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "s3-access") {
		t.Errorf("expected policy name in list, got: %s", body)
	}
}

func TestListGroupPolicies_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")

	resp := iamCall(t, srv, "ListGroupPolicies", url.Values{"GroupName": {"devs"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── Managed Group Policies ──────────────────────────────────────────────────

func TestAttachGroupPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")

	resp := iamCall(t, srv, "AttachGroupPolicy", url.Values{
		"GroupName": {"devs"},
		"PolicyArn": {"arn:aws:iam::aws:policy/ReadOnlyAccess"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAttachGroupPolicy_idempotent(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")
	arn := "arn:aws:iam::aws:policy/ReadOnlyAccess"

	r1 := iamCall(t, srv, "AttachGroupPolicy", url.Values{"GroupName": {"devs"}, "PolicyArn": {arn}})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	r2 := iamCall(t, srv, "AttachGroupPolicy", url.Values{"GroupName": {"devs"}, "PolicyArn": {arn}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusOK)
}

func TestDetachGroupPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")
	arn := "arn:aws:iam::aws:policy/ReadOnlyAccess"

	ar := iamCall(t, srv, "AttachGroupPolicy", url.Values{"GroupName": {"devs"}, "PolicyArn": {arn}})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	resp := iamCall(t, srv, "DetachGroupPolicy", url.Values{"GroupName": {"devs"}, "PolicyArn": {arn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestListAttachedGroupPolicies_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")
	arn := "arn:aws:iam::aws:policy/ReadOnlyAccess"

	ar := iamCall(t, srv, "AttachGroupPolicy", url.Values{"GroupName": {"devs"}, "PolicyArn": {arn}})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	resp := iamCall(t, srv, "ListAttachedGroupPolicies", url.Values{"GroupName": {"devs"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "ReadOnlyAccess") {
		t.Errorf("expected policy in list, got: %s", body)
	}
}

func TestListAttachedGroupPolicies_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "devs")

	resp := iamCall(t, srv, "ListAttachedGroupPolicies", url.Values{"GroupName": {"devs"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── Managed User Policies ───────────────────────────────────────────────────

func TestAttachUserPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	resp := iamCall(t, srv, "AttachUserPolicy", url.Values{
		"UserName":  {"alice"},
		"PolicyArn": {"arn:aws:iam::aws:policy/ReadOnlyAccess"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestAttachUserPolicy_idempotent(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	arn := "arn:aws:iam::aws:policy/ReadOnlyAccess"

	r1 := iamCall(t, srv, "AttachUserPolicy", url.Values{"UserName": {"alice"}, "PolicyArn": {arn}})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	r2 := iamCall(t, srv, "AttachUserPolicy", url.Values{"UserName": {"alice"}, "PolicyArn": {arn}})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusOK)
}

func TestDetachUserPolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	arn := "arn:aws:iam::aws:policy/ReadOnlyAccess"

	ar := iamCall(t, srv, "AttachUserPolicy", url.Values{"UserName": {"alice"}, "PolicyArn": {arn}})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	resp := iamCall(t, srv, "DetachUserPolicy", url.Values{"UserName": {"alice"}, "PolicyArn": {arn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestListAttachedUserPolicies_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	arn := "arn:aws:iam::aws:policy/ReadOnlyAccess"

	ar := iamCall(t, srv, "AttachUserPolicy", url.Values{"UserName": {"alice"}, "PolicyArn": {arn}})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	resp := iamCall(t, srv, "ListAttachedUserPolicies", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "ReadOnlyAccess") {
		t.Errorf("expected policy in list, got: %s", body)
	}
}

func TestListAttachedUserPolicies_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	resp := iamCall(t, srv, "ListAttachedUserPolicies", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── ListUserPolicies ────────────────────────────────────────────────────────

func TestListUserPolicies_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	pr := iamCall(t, srv, "PutUserPolicy", url.Values{
		"UserName":       {"alice"},
		"PolicyName":     {"s3-access"},
		"PolicyDocument": {"test-doc"},
	})
	defer pr.Body.Close()
	helpers.AssertStatus(t, pr, http.StatusOK)

	resp := iamCall(t, srv, "ListUserPolicies", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "s3-access") {
		t.Errorf("expected policy name in list, got: %s", body)
	}
}

func TestListUserPolicies_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	resp := iamCall(t, srv, "ListUserPolicies", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── Role Tagging ─────────────────────────────────────────────────────────────

func TestTagRole_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "my-role")

	resp := iamCall(t, srv, "TagRole", url.Values{
		"RoleName":            {"my-role"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
		"Tags.member.2.Key":   {"team"},
		"Tags.member.2.Value": {"platform"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestListRoleTags_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "my-role")

	tr := iamCall(t, srv, "TagRole", url.Values{
		"RoleName":            {"my-role"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer tr.Body.Close()
	helpers.AssertStatus(t, tr, http.StatusOK)

	resp := iamCall(t, srv, "ListRoleTags", url.Values{"RoleName": {"my-role"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "env") || !strings.Contains(body, "prod") {
		t.Errorf("expected tag in response, got: %s", body)
	}
}

func TestListRoleTags_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "my-role")

	resp := iamCall(t, srv, "ListRoleTags", url.Values{"RoleName": {"my-role"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestUntagRole_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "my-role")

	tr := iamCall(t, srv, "TagRole", url.Values{
		"RoleName":            {"my-role"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
		"Tags.member.2.Key":   {"team"},
		"Tags.member.2.Value": {"platform"},
	})
	defer tr.Body.Close()
	helpers.AssertStatus(t, tr, http.StatusOK)

	ur := iamCall(t, srv, "UntagRole", url.Values{
		"RoleName":         {"my-role"},
		"TagKeys.member.1": {"env"},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)

	// Verify only "team" remains
	resp := iamCall(t, srv, "ListRoleTags", url.Values{"RoleName": {"my-role"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if strings.Contains(body, "env") {
		t.Errorf("expected env tag to be removed, got: %s", body)
	}
	if !strings.Contains(body, "team") {
		t.Errorf("expected team tag to remain, got: %s", body)
	}
}

// ─── User Tagging ─────────────────────────────────────────────────────────────

func TestTagUser_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	resp := iamCall(t, srv, "TagUser", url.Values{
		"UserName":            {"alice"},
		"Tags.member.1.Key":   {"dept"},
		"Tags.member.1.Value": {"engineering"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestListUserTags_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	tr := iamCall(t, srv, "TagUser", url.Values{
		"UserName":            {"alice"},
		"Tags.member.1.Key":   {"dept"},
		"Tags.member.1.Value": {"engineering"},
	})
	defer tr.Body.Close()
	helpers.AssertStatus(t, tr, http.StatusOK)

	resp := iamCall(t, srv, "ListUserTags", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "dept") || !strings.Contains(body, "engineering") {
		t.Errorf("expected tag in response, got: %s", body)
	}
}

func TestUntagUser_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")

	tr := iamCall(t, srv, "TagUser", url.Values{
		"UserName":            {"alice"},
		"Tags.member.1.Key":   {"dept"},
		"Tags.member.1.Value": {"engineering"},
		"Tags.member.2.Key":   {"level"},
		"Tags.member.2.Value": {"senior"},
	})
	defer tr.Body.Close()
	helpers.AssertStatus(t, tr, http.StatusOK)

	ur := iamCall(t, srv, "UntagUser", url.Values{
		"UserName":         {"alice"},
		"TagKeys.member.1": {"dept"},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)

	resp := iamCall(t, srv, "ListUserTags", url.Values{"UserName": {"alice"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if strings.Contains(body, "dept") {
		t.Errorf("expected dept tag to be removed, got: %s", body)
	}
	if !strings.Contains(body, "level") {
		t.Errorf("expected level tag to remain, got: %s", body)
	}
}

// ─── CreateServiceLinkedRole ─────────────────────────────────────────────────

func TestCreateServiceLinkedRole_success(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := iamCall(t, srv, "CreateServiceLinkedRole", url.Values{
		"AWSServiceName": {"ecs.amazonaws.com"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "AWSServiceRoleForEcs") {
		t.Errorf("expected service-linked role name, got: %s", body)
	}
	if !strings.Contains(body, "/aws-service-role/ecs.amazonaws.com/") {
		t.Errorf("expected service-linked role path, got: %s", body)
	}
}

func TestCreateServiceLinkedRole_duplicate(t *testing.T) {
	srv := helpers.NewTestServer(t)

	r1 := iamCall(t, srv, "CreateServiceLinkedRole", url.Values{
		"AWSServiceName": {"ecs.amazonaws.com"},
	})
	defer r1.Body.Close()
	helpers.AssertStatus(t, r1, http.StatusOK)

	r2 := iamCall(t, srv, "CreateServiceLinkedRole", url.Values{
		"AWSServiceName": {"ecs.amazonaws.com"},
	})
	defer r2.Body.Close()
	helpers.AssertStatus(t, r2, http.StatusConflict)
}

// ─── ListInstanceProfilesForRole ─────────────────────────────────────────────

func TestListInstanceProfilesForRole_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "web-role")
	createInstanceProfile(t, srv, "web-profile")
	ar := iamCall(t, srv, "AddRoleToInstanceProfile", url.Values{
		"InstanceProfileName": {"web-profile"},
		"RoleName":            {"web-role"},
	})
	defer ar.Body.Close()
	helpers.AssertStatus(t, ar, http.StatusOK)

	resp := iamCall(t, srv, "ListInstanceProfilesForRole", url.Values{"RoleName": {"web-role"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "web-profile") {
		t.Errorf("expected profile in response, got: %s", body)
	}
}

func TestListInstanceProfilesForRole_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "lonely-role")

	resp := iamCall(t, srv, "ListInstanceProfilesForRole", url.Values{"RoleName": {"lonely-role"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── UpdateAssumeRolePolicy ──────────────────────────────────────────────────

func TestUpdateAssumeRolePolicy_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "my-role")

	newDoc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	resp := iamCall(t, srv, "UpdateAssumeRolePolicy", url.Values{
		"RoleName":       {"my-role"},
		"PolicyDocument": {newDoc},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify the trust policy was updated
	gr := iamCall(t, srv, "GetRole", url.Values{"RoleName": {"my-role"}})
	defer gr.Body.Close()
	helpers.AssertStatus(t, gr, http.StatusOK)
	body := helpers.ReadBody(t, gr)
	if !strings.Contains(body, "lambda.amazonaws.com") {
		t.Errorf("expected updated trust policy, got: %s", body)
	}
}

func TestUpdateAssumeRolePolicy_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := iamCall(t, srv, "UpdateAssumeRolePolicy", url.Values{
		"RoleName":       {"nonexistent"},
		"PolicyDocument": {"{}"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── SimulatePrincipalPolicy ─────────────────────────────────────────────────

func TestSimulatePrincipalPolicy_alwaysAllowed(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := iamCall(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":      {"arn:aws:iam::000000000000:user/alice"},
		"ActionNames.member.1": {"s3:GetObject"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "allowed") {
		t.Errorf("expected allowed decision, got: %s", body)
	}
	if !strings.Contains(body, "s3:GetObject") {
		t.Errorf("expected action name in response, got: %s", body)
	}
}

func TestSimulatePrincipalPolicy_multipleActions(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := iamCall(t, srv, "SimulatePrincipalPolicy", url.Values{
		"PolicySourceArn":      {"arn:aws:iam::000000000000:role/my-role"},
		"ActionNames.member.1": {"s3:GetObject"},
		"ActionNames.member.2": {"dynamodb:PutItem"},
		"ActionNames.member.3": {"lambda:InvokeFunction"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if strings.Count(body, "<EvalActionName>") != 3 {
		t.Errorf("expected 3 evaluation results, got body: %s", body)
	}
}

func TestSimulatePrincipalPolicy_missingPolicySourceArn(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := iamCall(t, srv, "SimulatePrincipalPolicy", url.Values{
		"ActionNames.member.1": {"s3:GetObject"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ─── GetAccountAuthorizationDetails ──────────────────────────────────────────

func TestGetAccountAuthorizationDetails_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := iamCall(t, srv, "GetAccountAuthorizationDetails", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "GetAccountAuthorizationDetailsResult") {
		t.Errorf("expected result wrapper, got: %s", body)
	}
}

func TestGetAccountAuthorizationDetails_includesUsers(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createUser(t, srv, "alice")
	createUser(t, srv, "bob")

	resp := iamCall(t, srv, "GetAccountAuthorizationDetails", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "alice") {
		t.Errorf("expected user alice, got: %s", body)
	}
	if !strings.Contains(body, "bob") {
		t.Errorf("expected user bob, got: %s", body)
	}
}

func TestGetAccountAuthorizationDetails_includesRolesAndGroups(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createRole(t, srv, "deploy-role")
	iamCall(t, srv, "CreateGroup", url.Values{"GroupName": {"devs"}}).Body.Close()

	resp := iamCall(t, srv, "GetAccountAuthorizationDetails", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "deploy-role") {
		t.Errorf("expected role in response, got: %s", body)
	}
	if !strings.Contains(body, "devs") {
		t.Errorf("expected group in response, got: %s", body)
	}
}

func TestGetAccountAuthorizationDetails_includesPolicies(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createPolicy(t, srv, "s3-read")

	resp := iamCall(t, srv, "GetAccountAuthorizationDetails", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	if !strings.Contains(body, "s3-read") {
		t.Errorf("expected managed policy in response, got: %s", body)
	}
}
