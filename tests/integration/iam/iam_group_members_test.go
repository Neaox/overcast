package iam_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// getGroupResult is the decoded GetGroup response body. It mirrors the AWS
// wire shape: the group itself, the resolved members, and the pagination
// fields a caller pages with.
type getGroupResult struct {
	GroupName   string   `xml:"GetGroupResult>Group>GroupName"`
	UserNames   []string `xml:"GetGroupResult>Users>member>UserName"`
	UserArns    []string `xml:"GetGroupResult>Users>member>Arn"`
	IsTruncated bool     `xml:"GetGroupResult>IsTruncated"`
	Marker      string   `xml:"GetGroupResult>Marker"`
}

// getGroup calls GetGroup and decodes the result. Extra query parameters
// (Marker, MaxItems) are merged in.
func getGroup(t *testing.T, srv *helpers.TestServer, name string, extra url.Values) getGroupResult {
	t.Helper()
	params := url.Values{"GroupName": {name}}
	for k, vs := range extra {
		for _, v := range vs {
			params.Add(k, v)
		}
	}
	resp := iamCall(t, srv, "GetGroup", params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)
	var out getGroupResult
	if err := xml.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("GetGroup: decode response: %v (body: %s)", err, body)
	}
	return out
}

func TestGetGroup_returnsStoredMembers(t *testing.T) {
	// Given: a group with two members
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")
	createUser(t, srv, "bob")
	addUserToGroup(t, srv, "alice", "developers")
	addUserToGroup(t, srv, "bob", "developers")

	// When: GetGroup is called
	got := getGroup(t, srv, "developers", nil)

	// Then: both members are resolved into the Users collection
	if got.GroupName != "developers" {
		t.Errorf("GroupName = %q, want developers", got.GroupName)
	}
	if len(got.UserNames) != 2 {
		t.Fatalf("Users = %v, want 2 members", got.UserNames)
	}
	if got.UserNames[0] != "alice" || got.UserNames[1] != "bob" {
		t.Errorf("Users = %v, want [alice bob]", got.UserNames)
	}
	// And: each member carries its full user record, not just a name.
	for _, arn := range got.UserArns {
		if arn == "" {
			t.Errorf("member Arn is empty, got Arns %v", got.UserArns)
		}
	}
	if got.IsTruncated {
		t.Error("IsTruncated = true, want false for a single complete page")
	}
}

func TestGetGroup_reflectsRemovedMember(t *testing.T) {
	// Given: a group with two members
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")
	createUser(t, srv, "bob")
	addUserToGroup(t, srv, "alice", "developers")
	addUserToGroup(t, srv, "bob", "developers")

	// When: one member is removed
	rr := iamCall(t, srv, "RemoveUserFromGroup", url.Values{
		"GroupName": {"developers"},
		"UserName":  {"alice"},
	})
	rr.Body.Close()
	helpers.AssertStatus(t, rr, http.StatusOK)

	// Then: GetGroup returns only the remaining member
	got := getGroup(t, srv, "developers", nil)
	if len(got.UserNames) != 1 || got.UserNames[0] != "bob" {
		t.Errorf("Users = %v, want [bob]", got.UserNames)
	}
}

func TestGetGroup_paginatesWithMarkerAndMaxItems(t *testing.T) {
	// Given: a group with three members
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	for _, name := range []string{"alice", "bob", "carol"} {
		createUser(t, srv, name)
		addUserToGroup(t, srv, name, "developers")
	}

	// When: the first page is requested with MaxItems=2
	first := getGroup(t, srv, "developers", url.Values{"MaxItems": {"2"}})

	// Then: two members come back, truncated, with a Marker
	if len(first.UserNames) != 2 {
		t.Fatalf("page 1 Users = %v, want 2", first.UserNames)
	}
	if !first.IsTruncated {
		t.Error("page 1 IsTruncated = false, want true")
	}
	if first.Marker == "" {
		t.Fatal("page 1 Marker is empty, want a continuation token")
	}

	// And: the Marker returns the remainder, untruncated and without a Marker
	second := getGroup(t, srv, "developers", url.Values{
		"MaxItems": {"2"},
		"Marker":   {first.Marker},
	})
	if len(second.UserNames) != 1 {
		t.Fatalf("page 2 Users = %v, want 1", second.UserNames)
	}
	if second.IsTruncated {
		t.Error("page 2 IsTruncated = true, want false")
	}
	if second.Marker != "" {
		t.Errorf("page 2 Marker = %q, want empty on the last page", second.Marker)
	}

	// And: the two pages together are the whole membership, without overlap.
	all := append(append([]string{}, first.UserNames...), second.UserNames...)
	want := []string{"alice", "bob", "carol"}
	for i, name := range want {
		if all[i] != name {
			t.Errorf("paged members = %v, want %v", all, want)
			break
		}
	}
}

func TestGetGroup_invalidMarker(t *testing.T) {
	// Given: a group with a member
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	createUser(t, srv, "alice")
	addUserToGroup(t, srv, "alice", "developers")

	// When: GetGroup is called with a token that does not decode
	resp := iamCall(t, srv, "GetGroup", url.Values{
		"GroupName": {"developers"},
		"Marker":    {"not-a-real-marker"},
	})
	defer resp.Body.Close()

	// Then: the request is rejected rather than silently restarting at page 1
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidInput")
}

func TestGetGroup_skipsMissingAndMalformedMembers(t *testing.T) {
	// Given: a group whose membership references three users
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "developers")
	for _, name := range []string{"alice", "bob", "carol"} {
		createUser(t, srv, name)
		addUserToGroup(t, srv, name, "developers")
	}

	// And: one referenced user record has vanished and another is corrupt
	ctx := context.Background()
	if err := srv.Store.Delete(ctx, "iam:users", "bob"); err != nil {
		t.Fatalf("delete user record: %v", err)
	}
	if err := srv.Store.Set(ctx, "iam:users", "carol", "{not-json"); err != nil {
		t.Fatalf("corrupt user record: %v", err)
	}

	// When: GetGroup is called
	got := getGroup(t, srv, "developers", nil)

	// Then: the readable member is still returned — one bad record does not
	// make the whole group unreadable.
	if len(got.UserNames) != 1 || got.UserNames[0] != "alice" {
		t.Errorf("Users = %v, want [alice] with the missing and corrupt records skipped", got.UserNames)
	}
}

func TestGetGroup_manyMembersDefaultPageSize(t *testing.T) {
	// Given: a group with more members than one default page holds (100)
	srv := helpers.NewTestServer(t)
	createGroup(t, srv, "big")
	for i := 0; i < 101; i++ {
		name := fmt.Sprintf("user-%03d", i)
		createUser(t, srv, name)
		addUserToGroup(t, srv, name, "big")
	}

	// When: GetGroup is called without MaxItems
	got := getGroup(t, srv, "big", nil)

	// Then: AWS's documented default of 100 applies and the page is truncated
	if len(got.UserNames) != 100 {
		t.Errorf("Users = %d members, want the documented default page of 100", len(got.UserNames))
	}
	if !got.IsTruncated {
		t.Error("IsTruncated = false, want true when members exceed the default page size")
	}
}
