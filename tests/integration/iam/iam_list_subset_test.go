// Package iam_test — the rest of AWS's listing subset, beyond Tags (#1850).
//
// "IAM resource-listing operations return a subset of the available attributes
// for the resource. This operation does not return the following attributes,
// even though they are an attribute of the returned object" — IAM API
// Reference, API_ListRoles.html, which names PermissionsBoundary, RoleLastUsed
// and Tags; API_ListUsers.html names PermissionsBoundary and Tags. #1717
// honoured the Tags half of those notes and left PermissionsBoundary behind.
//
// ListPolicies says it differently and means the same thing: its own note names
// only tags, but API_Policy.html's Description member reads "This element is
// included in the response to the GetPolicy operation. It is not included in
// the response to the ListPolicies operation."
//
// The subset is not cosmetic. A caller that reads a boundary from a listing
// sees one for every entity that has one and nothing for the rest, which is
// indistinguishable from AWS's answer for an entity that has none — so an
// inventory built against Overcast reports boundaries AWS would have made it
// call Get for, and the same script against real AWS silently finds none.
//
// GetAccountAuthorizationDetails is deliberately not in here: AWS documents its
// members as RoleDetail, UserDetail and ManagedPolicyDetail rather than Role,
// User and Policy, and RoleDetail and UserDetail both carry PermissionsBoundary
// while ManagedPolicyDetail carries Description. It is not a listing subset and
// must not be trimmed to look like one.
//
// Run: go test ./tests/integration/iam/...
package iam_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

var (
	boundaryArnRE       = regexp.MustCompile(`<PermissionsBoundaryArn>([^<]*)</PermissionsBoundaryArn>`)
	policyDescriptionRE = regexp.MustCompile(`<Description>([^<]*)</Description>`)
)

// boundaryArnsIn returns every PermissionsBoundaryArn in a response body.
func boundaryArnsIn(body string) []string {
	var out []string
	for _, m := range boundaryArnRE.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// seedBoundaryPolicy creates the managed policy an entity's boundary points at
// and returns its ARN. PutUser/RolePermissionsBoundary refuses an ARN that
// resolves to nothing, so the boundary needs a real policy behind it.
func seedBoundaryPolicy(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	return createPolicyWithDocument(t, srv, name, validIdentityPolicy)
}

// TestListRoles_omitsPermissionsBoundary pins API_ListRoles.html's note: the
// boundary is on the role and reachable through GetRole, and absent from the
// listing.
func TestListRoles_omitsPermissionsBoundary(t *testing.T) {
	// Given: a role with a permissions boundary
	srv := helpers.NewTestServer(t)
	boundary := seedBoundaryPolicy(t, srv, "role-boundary")
	createRole(t, srv, "bounded-role")
	putRolePermissionsBoundary(t, srv, "bounded-role", boundary)

	// Then: GetRole returns it — the listing note points the caller here
	getBody := iamCallBody(t, srv, "GetRole", url.Values{"RoleName": {"bounded-role"}})
	if got := boundaryArnsIn(getBody); len(got) != 1 || got[0] != boundary {
		t.Fatalf("GetRole PermissionsBoundaryArn = %v, want [%s]: %s", got, boundary, getBody)
	}

	// And: ListRoles does not
	listBody := iamCallBody(t, srv, "ListRoles", nil)
	if !strings.Contains(listBody, "bounded-role") {
		t.Fatalf("ListRoles did not return the role at all: %s", listBody)
	}
	if got := boundaryArnsIn(listBody); len(got) != 0 {
		t.Errorf("ListRoles returned PermissionsBoundary %v — AWS excludes it from the listing subset: %s", got, listBody)
	}
}

// TestListUsers_omitsPermissionsBoundary is the same contract on
// API_ListUsers.html.
func TestListUsers_omitsPermissionsBoundary(t *testing.T) {
	// Given: a user with a permissions boundary
	srv := helpers.NewTestServer(t)
	boundary := seedBoundaryPolicy(t, srv, "user-boundary")
	createUser(t, srv, "bounded-user")
	putUserPermissionsBoundary(t, srv, "bounded-user", boundary)

	// Then: GetUser returns it
	getBody := iamCallBody(t, srv, "GetUser", url.Values{"UserName": {"bounded-user"}})
	if got := boundaryArnsIn(getBody); len(got) != 1 || got[0] != boundary {
		t.Fatalf("GetUser PermissionsBoundaryArn = %v, want [%s]: %s", got, boundary, getBody)
	}

	// And: ListUsers does not
	listBody := iamCallBody(t, srv, "ListUsers", nil)
	if !strings.Contains(listBody, "bounded-user") {
		t.Fatalf("ListUsers did not return the user at all: %s", listBody)
	}
	if got := boundaryArnsIn(listBody); len(got) != 0 {
		t.Errorf("ListUsers returned PermissionsBoundary %v — AWS excludes it from the listing subset: %s", got, listBody)
	}
}

// TestListPolicies_omitsDescription pins API_Policy.html's Description note.
func TestListPolicies_omitsDescription(t *testing.T) {
	// Given: a managed policy created with a description
	srv := helpers.NewTestServer(t)
	const want = "grants read access to the reports bucket"
	resp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":     {"described-policy"},
		"PolicyDocument": {validIdentityPolicy},
		"Description":    {want},
	})
	resp.Body.Close()
	arn := "arn:aws:iam::000000000000:policy/described-policy"

	// Then: GetPolicy returns the description
	getBody := iamCallBody(t, srv, "GetPolicy", url.Values{"PolicyArn": {arn}})
	if m := policyDescriptionRE.FindStringSubmatch(getBody); m == nil || m[1] != want {
		t.Fatalf("GetPolicy Description = %v, want %q: %s", m, want, getBody)
	}

	// And: ListPolicies does not
	listBody := iamCallBody(t, srv, "ListPolicies", nil)
	if !strings.Contains(listBody, "described-policy") {
		t.Fatalf("ListPolicies did not return the policy at all: %s", listBody)
	}
	if m := policyDescriptionRE.FindStringSubmatch(listBody); m != nil {
		t.Errorf("ListPolicies returned Description %q — AWS excludes it from the listing subset: %s", m[1], listBody)
	}
}

// TestListPolicies_keepsTheCountersAndAttachability guards the trim: the two
// derived counters and IsAttachable are Policy members AWS's own ListPolicies
// sample response carries, so removing Description must not take them with it.
func TestListPolicies_keepsTheCountersAndAttachability(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicyWithDocument(t, srv, "counted-policy", validIdentityPolicy)
	createRole(t, srv, "counting-role")
	iamOK(t, srv, "AttachRolePolicy", url.Values{"RoleName": {"counting-role"}, "PolicyArn": {arn}})

	body := iamCallBody(t, srv, "ListPolicies", nil)
	if m := attachmentCountRE.FindStringSubmatch(body); m == nil || m[1] != "1" {
		t.Errorf("ListPolicies AttachmentCount = %v, want 1: %s", m, body)
	}
	if !strings.Contains(body, "<PermissionsBoundaryUsageCount>") {
		t.Errorf("ListPolicies omits PermissionsBoundaryUsageCount: %s", body)
	}
	if !strings.Contains(body, "<IsAttachable>true</IsAttachable>") {
		t.Errorf("ListPolicies omits IsAttachable: %s", body)
	}
}

// TestGetAccountAuthorizationDetails_keepsPermissionsBoundary pins the
// deliberate exception. RoleDetail and UserDetail both document
// PermissionsBoundary (API_RoleDetail.html, API_UserDetail.html), so this
// operation keeps what the listings drop.
func TestGetAccountAuthorizationDetails_keepsPermissionsBoundary(t *testing.T) {
	srv := helpers.NewTestServer(t)
	boundary := seedBoundaryPolicy(t, srv, "detail-boundary")
	createRole(t, srv, "detail-role")
	putRolePermissionsBoundary(t, srv, "detail-role", boundary)
	createUser(t, srv, "detail-user")
	putUserPermissionsBoundary(t, srv, "detail-user", boundary)

	body := iamCallBody(t, srv, "GetAccountAuthorizationDetails", nil)
	if got := boundaryArnsIn(body); len(got) != 2 {
		t.Errorf("GetAccountAuthorizationDetails PermissionsBoundaryArn = %v, want one for the role and one for the user: %s", got, body)
	}
}
