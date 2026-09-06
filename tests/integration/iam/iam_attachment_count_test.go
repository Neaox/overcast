// Package iam_test — the two usage counters on a managed policy (#1717).
//
// AttachmentCount is "the number of entities (users, groups, and roles) that
// the policy is attached to" and PermissionsBoundaryUsageCount "the number of
// entities (users and roles) for which the policy is used to set the
// permissions boundary" — IAM API Reference, API_Policy.html. They are the
// documented way to answer "is this policy in use?", which is exactly the
// check a cleanup script or a DeletePolicy guard makes before removing
// something, so a stuck 0 makes such a script delete a policy that is in use.
//
// Run: go test ./tests/integration/iam/...
package iam_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

var (
	attachmentCountRE = regexp.MustCompile(`<AttachmentCount>(\d+)</AttachmentCount>`)
	boundaryCountRE   = regexp.MustCompile(`<PermissionsBoundaryUsageCount>(\d+)</PermissionsBoundaryUsageCount>`)
	isAttachableRE    = regexp.MustCompile(`<IsAttachable>(true|false)</IsAttachable>`)
)

// policyCounts reads a policy's two usage counters and IsAttachable back
// through GetPolicy.
func policyCounts(t *testing.T, srv *helpers.TestServer, arn string) (attachments, boundaries int, attachable bool) {
	t.Helper()
	resp := iamCall(t, srv, "GetPolicy", url.Values{"PolicyArn": {arn}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)

	num := func(re *regexp.Regexp, field string) int {
		m := re.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("GetPolicy response has no %s: %s", field, body)
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("GetPolicy %s is not a number: %v", field, err)
		}
		return n
	}
	m := isAttachableRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("GetPolicy response has no IsAttachable: %s", body)
	}
	return num(attachmentCountRE, "AttachmentCount"), num(boundaryCountRE, "PermissionsBoundaryUsageCount"), m[1] == "true"
}

// assertCounts checks both counters at once, so a fix to one that quietly
// moved the other is caught.
func assertCounts(t *testing.T, srv *helpers.TestServer, arn string, wantAttachments, wantBoundaries int, when string) {
	t.Helper()
	attachments, boundaries, attachable := policyCounts(t, srv, arn)
	if attachments != wantAttachments {
		t.Errorf("%s: AttachmentCount = %d, want %d", when, attachments, wantAttachments)
	}
	if boundaries != wantBoundaries {
		t.Errorf("%s: PermissionsBoundaryUsageCount = %d, want %d", when, boundaries, wantBoundaries)
	}
	if !attachable {
		t.Errorf("%s: IsAttachable = false, want true for a customer managed policy", when)
	}
}

func TestAttachmentCount_followsAttachAndDetachAcrossEntityKinds(t *testing.T) {
	// Given: a policy and one of each entity that can carry an attachment
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "counted-policy")
	createRole(t, srv, "counted-role")
	createUser(t, srv, "counted-user")
	createGroup(t, srv, "counted-group")

	assertCounts(t, srv, arn, 0, 0, "freshly created")

	// When: it is attached to each in turn
	for i, tc := range []struct{ action, nameKey, entity string }{
		{"AttachRolePolicy", "RoleName", "counted-role"},
		{"AttachUserPolicy", "UserName", "counted-user"},
		{"AttachGroupPolicy", "GroupName", "counted-group"},
	} {
		assertIAMOK(t, srv, tc.action, url.Values{tc.nameKey: {tc.entity}, "PolicyArn": {arn}})
		assertCounts(t, srv, arn, i+1, 0, "after "+tc.action)
	}

	// And: attaching again to an entity that already has it is a no-op, as on
	// AWS — the counter counts entities, not calls.
	assertIAMOK(t, srv, "AttachRolePolicy", url.Values{"RoleName": {"counted-role"}, "PolicyArn": {arn}})
	assertCounts(t, srv, arn, 3, 0, "after a repeated AttachRolePolicy")

	// Then: each detach takes it back down
	for i, tc := range []struct{ action, nameKey, entity string }{
		{"DetachGroupPolicy", "GroupName", "counted-group"},
		{"DetachUserPolicy", "UserName", "counted-user"},
		{"DetachRolePolicy", "RoleName", "counted-role"},
	} {
		assertIAMOK(t, srv, tc.action, url.Values{tc.nameKey: {tc.entity}, "PolicyArn": {arn}})
		assertCounts(t, srv, arn, 2-i, 0, "after "+tc.action)
	}
}

// TestPermissionsBoundaryUsageCount_isCountedSeparately pins AWS's split: a
// boundary is not an attachment, and the two counters move independently.
func TestPermissionsBoundaryUsageCount_isCountedSeparately(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "boundary-policy")
	createRole(t, srv, "bounded-role")
	createUser(t, srv, "bounded-user")

	assertIAMOK(t, srv, "PutRolePermissionsBoundary", url.Values{
		"RoleName": {"bounded-role"}, "PermissionsBoundary": {arn},
	})
	assertCounts(t, srv, arn, 0, 1, "after PutRolePermissionsBoundary")

	assertIAMOK(t, srv, "PutUserPermissionsBoundary", url.Values{
		"UserName": {"bounded-user"}, "PermissionsBoundary": {arn},
	})
	assertCounts(t, srv, arn, 0, 2, "after PutUserPermissionsBoundary")

	// And: an attachment on top moves the other counter only.
	assertIAMOK(t, srv, "AttachRolePolicy", url.Values{"RoleName": {"bounded-role"}, "PolicyArn": {arn}})
	assertCounts(t, srv, arn, 1, 2, "with a boundary and an attachment")

	assertIAMOK(t, srv, "DeleteRolePermissionsBoundary", url.Values{"RoleName": {"bounded-role"}})
	assertCounts(t, srv, arn, 1, 1, "after DeleteRolePermissionsBoundary")

	assertIAMOK(t, srv, "DeleteUserPermissionsBoundary", url.Values{"UserName": {"bounded-user"}})
	assertCounts(t, srv, arn, 1, 0, "after DeleteUserPermissionsBoundary")
}

// TestListPolicies_carriesAttachmentCount pins that the listing shows the
// counts too — AWS's ListPolicies sample response carries both, and it is the
// call a cleanup script sweeps with.
func TestListPolicies_carriesAttachmentCount(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "listed-policy")
	createRole(t, srv, "listed-role")
	assertIAMOK(t, srv, "AttachRolePolicy", url.Values{"RoleName": {"listed-role"}, "PolicyArn": {arn}})

	resp := iamCall(t, srv, "ListPolicies", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)

	if m := attachmentCountRE.FindStringSubmatch(body); m == nil || m[1] != "1" {
		t.Errorf("ListPolicies AttachmentCount = %v, want 1: %s", m, body)
	}
	if boundaryCountRE.FindStringSubmatch(body) == nil {
		t.Errorf("ListPolicies omits PermissionsBoundaryUsageCount: %s", body)
	}
}

// TestAttachmentCount_survivesEntityDeletion pins the counter against the one
// way a stored counter would drift: an entity that goes away. DeleteUser
// refuses while an attachment remains, so the only route to zero is a detach —
// and after it, the count must be zero rather than stuck at one.
func TestAttachmentCount_survivesEntityDeletion(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "orphan-policy")
	createUser(t, srv, "temp-user")
	assertIAMOK(t, srv, "AttachUserPolicy", url.Values{"UserName": {"temp-user"}, "PolicyArn": {arn}})
	assertCounts(t, srv, arn, 1, 0, "while attached")

	resp := iamCall(t, srv, "DeleteUser", url.Values{"UserName": {"temp-user"}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusConflict)

	assertIAMOK(t, srv, "DetachUserPolicy", url.Values{"UserName": {"temp-user"}, "PolicyArn": {arn}})
	assertIAMOK(t, srv, "DeleteUser", url.Values{"UserName": {"temp-user"}})
	assertCounts(t, srv, arn, 0, 0, "after the user was detached and deleted")
}
