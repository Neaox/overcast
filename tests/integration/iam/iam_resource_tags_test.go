// Package iam_test — tags on the resource itself, not just the Tag*/List*Tags
// operations (#1717).
//
// Tags supplied to a Create* call were stored and readable through
// List<Resource>Tags, but the resource AWS returns carries them as a member of
// its own shape: Role, User, Policy and InstanceProfile all document Tags (IAM
// API Reference, API_Role.html and siblings). Tag-based tooling — cost
// attribution, ABAC-style filtering, cleanup by owner tag — reads them from
// exactly there, and got nothing.
//
// The listing operations are the deliberate exception. AWS returns a subset of
// each resource's attributes from them: "this operation does not return the
// following attributes, even though they are an attribute of the returned
// object" (API_ListRoles.html, naming Tags), and the same on ListUsers,
// ListPolicies and ListInstanceProfiles, each pointing the caller at the
// matching Get. So the tests below assert their absence as carefully as they
// assert the presence elsewhere.
//
// Run: go test ./tests/integration/iam/...
package iam_test

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// iamTagsIn parses the <Tags> member list out of any IAM response body.
func iamTagsIn(body string) map[string]string {
	out := map[string]string{}
	for _, m := range iamTagMemberRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// iamCallBody runs an IAM call, asserts 200 and returns the body.
func iamCallBody(t *testing.T, srv *helpers.TestServer, action string, params url.Values) string {
	t.Helper()
	resp := iamCall(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return helpers.ReadBody(t, resp)
}

// createTagParams builds the inline Tags.member.N form encoding a Create* or
// Tag* call takes.
func createTagParams(base url.Values, tags map[string]string) url.Values {
	params := url.Values{}
	for k, v := range base {
		params[k] = v
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		params.Set("Tags.member."+strconv.Itoa(i+1)+".Key", key)
		params.Set("Tags.member."+strconv.Itoa(i+1)+".Value", tags[key])
	}
	return params
}

// assertTags checks the tag set parsed from a response body.
func assertTags(t *testing.T, body string, want map[string]string, when string) {
	t.Helper()
	got := iamTagsIn(body)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: tag %q = %q, want %q (tags: %v)", when, k, got[k], v, got)
		}
	}
}

// assertNoTags checks a response carries no <Tags> member at all.
func assertNoTags(t *testing.T, body string, when string) {
	t.Helper()
	if got := iamTagsIn(body); len(got) != 0 {
		t.Errorf("%s: expected no tags on a listing operation, got %v", when, got)
	}
}

// taggableResource describes one of IAM's four taggable resource types well
// enough to drive the same round-trip against each.
type taggableResource struct {
	name        string
	create      string // Create* action
	get         string // Get* action
	list        string // List* action, which AWS returns without tags
	listTags    string // List<Resource>Tags action
	tag, untag  string // Tag*/Untag* actions
	createParam func() url.Values
	refParam    func() url.Values
}

func taggableResources() []taggableResource {
	return []taggableResource{
		{
			name: "role", create: "CreateRole", get: "GetRole", list: "ListRoles",
			listTags: "ListRoleTags", tag: "TagRole", untag: "UntagRole",
			createParam: func() url.Values {
				return url.Values{"RoleName": {"tagged-role"}, "AssumeRolePolicyDocument": {validAssumePolicy}}
			},
			refParam: func() url.Values { return url.Values{"RoleName": {"tagged-role"}} },
		},
		{
			name: "user", create: "CreateUser", get: "GetUser", list: "ListUsers",
			listTags: "ListUserTags", tag: "TagUser", untag: "UntagUser",
			createParam: func() url.Values { return url.Values{"UserName": {"tagged-user"}} },
			refParam:    func() url.Values { return url.Values{"UserName": {"tagged-user"}} },
		},
		{
			name: "policy", create: "CreatePolicy", get: "GetPolicy", list: "ListPolicies",
			listTags: "ListPolicyTags", tag: "TagPolicy", untag: "UntagPolicy",
			createParam: func() url.Values {
				return url.Values{"PolicyName": {"tagged-policy"}, "PolicyDocument": {validIdentityPolicy}}
			},
			refParam: func() url.Values {
				return url.Values{"PolicyArn": {"arn:aws:iam::000000000000:policy/tagged-policy"}}
			},
		},
		{
			name: "instance profile", create: "CreateInstanceProfile", get: "GetInstanceProfile",
			list: "ListInstanceProfiles", listTags: "ListInstanceProfileTags",
			tag: "TagInstanceProfile", untag: "UntagInstanceProfile",
			createParam: func() url.Values {
				return url.Values{"InstanceProfileName": {"tagged-profile"}}
			},
			refParam: func() url.Values { return url.Values{"InstanceProfileName": {"tagged-profile"}} },
		},
	}
}

// TestCreateWithTags_roundTripsThroughGet is the issue's headline case, run
// against all four taggable resource types rather than roles alone.
func TestCreateWithTags_roundTripsThroughGet(t *testing.T) {
	for _, r := range taggableResources() {
		t.Run(r.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			want := map[string]string{"owner": "platform", "stage": "dev"}

			// Given: the resource is created with inline Tags
			createBody := iamCallBody(t, srv, r.create, createTagParams(r.createParam(), want))
			assertTags(t, createBody, want, r.create+" response")

			ref := r.refParam()

			// Then: Get returns them on the resource itself
			assertTags(t, iamCallBody(t, srv, r.get, ref), want, r.get+" response")

			// And: the dedicated tag listing agrees
			assertTags(t, iamCallBody(t, srv, r.listTags, ref), want, r.listTags+" response")
		})
	}
}

// TestTagAndUntag_showThroughGet pins that the resource view tracks later tag
// mutations, not just what Create was handed.
func TestTagAndUntag_showThroughGet(t *testing.T) {
	for _, r := range taggableResources() {
		t.Run(r.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			iamCallBody(t, srv, r.create, r.createParam())
			ref := r.refParam()

			// A resource created without tags carries none.
			assertNoTags(t, iamCallBody(t, srv, r.get, ref), r.get+" on an untagged resource")

			// Tag* adds them.
			iamCallBody(t, srv, r.tag, createTagParams(ref, map[string]string{"owner": "platform", "stage": "dev"}))
			assertTags(t, iamCallBody(t, srv, r.get, ref), map[string]string{"owner": "platform", "stage": "dev"}, "after "+r.tag)

			// Untag* removes only the key it names.
			untag := url.Values{}
			for k, v := range ref {
				untag[k] = v
			}
			untag.Set("TagKeys.member.1", "stage")
			iamCallBody(t, srv, r.untag, untag)

			body := iamCallBody(t, srv, r.get, ref)
			assertTags(t, body, map[string]string{"owner": "platform"}, "after "+r.untag)
			if _, still := iamTagsIn(body)["stage"]; still {
				t.Errorf("%s left the removed key on the resource: %s", r.untag, body)
			}
		})
	}
}

// TestListOperations_omitTags pins AWS's listing subset. Adding tags to the
// resource shape must not add them here: a caller that wants them is told to
// call Get.
func TestListOperations_omitTags(t *testing.T) {
	for _, r := range taggableResources() {
		t.Run(r.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			want := map[string]string{"owner": "platform", "stage": "dev"}
			iamCallBody(t, srv, r.create, createTagParams(r.createParam(), want))

			assertNoTags(t, iamCallBody(t, srv, r.list, nil), r.list+" response")
		})
	}
}
