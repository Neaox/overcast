// Package iam_test — resource tagging beyond roles and users.
//
// Managed policies and instance profiles are taggable in real IAM, each with
// its own operation triple (TagPolicy/UntagPolicy/ListPolicyTags,
// TagInstanceProfile/UntagInstanceProfile/ListInstanceProfileTags) rather than
// a shared TagResource. Every Create* call that makes a taggable entity also
// accepts inline Tags, which AWS authorizes separately from the Tag* action.
//
// Run: go test ./tests/integration/iam/...
package iam_test

import (
	"net/http"
	"net/url"
	"regexp"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

var iamTagMemberRE = regexp.MustCompile(`(?s)<member>\s*<Key>([^<]*)</Key>\s*<Value>([^<]*)</Value>\s*</member>`)

// iamTagMap parses the <Tags> member list out of any IAM List*Tags response.
func iamTagMap(t *testing.T, srv *helpers.TestServer, action string, params url.Values) map[string]string {
	t.Helper()
	resp := iamCall(t, srv, action, params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	out := map[string]string{}
	for _, m := range iamTagMemberRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// ─── Managed policy tagging ───────────────────────────────────────────────────

func TestTagPolicy_roundTrips(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "tagged-policy")

	resp := iamCall(t, srv, "TagPolicy", url.Values{
		"PolicyArn":           {arn},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
		"Tags.member.2.Key":   {"team"},
		"Tags.member.2.Value": {"platform"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	got := iamTagMap(t, srv, "ListPolicyTags", url.Values{"PolicyArn": {arn}})
	if got["env"] != "prod" || got["team"] != "platform" {
		t.Fatalf("TagPolicy did not round-trip: got %v", got)
	}
}

func TestUntagPolicy_removesNamedKeysOnly(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "untagged-policy")

	tag := iamCall(t, srv, "TagPolicy", url.Values{
		"PolicyArn":           {arn},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
		"Tags.member.2.Key":   {"keep"},
		"Tags.member.2.Value": {"yes"},
	})
	defer tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	untag := iamCall(t, srv, "UntagPolicy", url.Values{
		"PolicyArn":        {arn},
		"TagKeys.member.1": {"env"},
	})
	defer untag.Body.Close()
	helpers.AssertStatus(t, untag, http.StatusOK)

	got := iamTagMap(t, srv, "ListPolicyTags", url.Values{"PolicyArn": {arn}})
	if _, still := got["env"]; still {
		t.Errorf("UntagPolicy left env in place: %v", got)
	}
	if got["keep"] != "yes" {
		t.Errorf("UntagPolicy removed an unrelated tag: %v", got)
	}
}

func TestTagPolicy_unknownPolicy(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := iamCall(t, srv, "TagPolicy", url.Values{
		"PolicyArn":           {"arn:aws:iam::000000000000:policy/does-not-exist"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
}

// ─── Instance profile tagging ─────────────────────────────────────────────────

func TestTagInstanceProfile_roundTrips(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := iamCall(t, srv, "CreateInstanceProfile", url.Values{"InstanceProfileName": {"tagged-profile"}})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	resp := iamCall(t, srv, "TagInstanceProfile", url.Values{
		"InstanceProfileName": {"tagged-profile"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	got := iamTagMap(t, srv, "ListInstanceProfileTags", url.Values{"InstanceProfileName": {"tagged-profile"}})
	if got["env"] != "prod" {
		t.Fatalf("TagInstanceProfile did not round-trip: got %v", got)
	}
}

func TestUntagInstanceProfile_removesNamedKeysOnly(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := iamCall(t, srv, "CreateInstanceProfile", url.Values{
		"InstanceProfileName": {"untagged-profile"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
		"Tags.member.2.Key":   {"keep"},
		"Tags.member.2.Value": {"yes"},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	untag := iamCall(t, srv, "UntagInstanceProfile", url.Values{
		"InstanceProfileName": {"untagged-profile"},
		"TagKeys.member.1":    {"env"},
	})
	defer untag.Body.Close()
	helpers.AssertStatus(t, untag, http.StatusOK)

	got := iamTagMap(t, srv, "ListInstanceProfileTags", url.Values{"InstanceProfileName": {"untagged-profile"}})
	if _, still := got["env"]; still {
		t.Errorf("UntagInstanceProfile left env in place: %v", got)
	}
	if got["keep"] != "yes" {
		t.Errorf("UntagInstanceProfile removed an unrelated tag: %v", got)
	}
}

// ─── Tag-on-create ────────────────────────────────────────────────────────────

func TestCreateUser_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := iamCall(t, srv, "CreateUser", url.Values{
		"UserName":            {"tagged-user"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	got := iamTagMap(t, srv, "ListUserTags", url.Values{"UserName": {"tagged-user"}})
	if got["env"] != "prod" {
		t.Errorf("CreateUser tags not applied at creation: got %v", got)
	}
}

func TestCreateRole_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := iamCall(t, srv, "CreateRole", url.Values{
		"RoleName":                 {"tagged-role"},
		"AssumeRolePolicyDocument": {`{"Version":"2012-10-17","Statement":[]}`},
		"Tags.member.1.Key":        {"env"},
		"Tags.member.1.Value":      {"prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	got := iamTagMap(t, srv, "ListRoleTags", url.Values{"RoleName": {"tagged-role"}})
	if got["env"] != "prod" {
		t.Errorf("CreateRole tags not applied at creation: got %v", got)
	}
}

func TestCreatePolicy_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := iamCall(t, srv, "CreatePolicy", url.Values{
		"PolicyName":          {"tagged-at-create"},
		"PolicyDocument":      {`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	arn := "arn:aws:iam::000000000000:policy/tagged-at-create"
	got := iamTagMap(t, srv, "ListPolicyTags", url.Values{"PolicyArn": {arn}})
	if got["env"] != "prod" {
		t.Errorf("CreatePolicy tags not applied at creation: got %v", got)
	}
}

func TestCreateInstanceProfile_appliesTagsAtCreation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := iamCall(t, srv, "CreateInstanceProfile", url.Values{
		"InstanceProfileName": {"tagged-at-create-profile"},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	got := iamTagMap(t, srv, "ListInstanceProfileTags", url.Values{"InstanceProfileName": {"tagged-at-create-profile"}})
	if got["env"] != "prod" {
		t.Errorf("CreateInstanceProfile tags not applied at creation: got %v", got)
	}
}

// Deleting an entity must take its tags with it, so a same-named replacement
// does not inherit them.
func TestDeletePolicy_dropsItsTags(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createPolicy(t, srv, "recycled-policy")
	tag := iamCall(t, srv, "TagPolicy", url.Values{
		"PolicyArn":           {arn},
		"Tags.member.1.Key":   {"env"},
		"Tags.member.1.Value": {"prod"},
	})
	defer tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	del := iamCall(t, srv, "DeletePolicy", url.Values{"PolicyArn": {arn}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	createPolicy(t, srv, "recycled-policy")
	if got := iamTagMap(t, srv, "ListPolicyTags", url.Values{"PolicyArn": {arn}}); len(got) != 0 {
		t.Errorf("recreated policy inherited tags from the deleted one: %v", got)
	}
}
