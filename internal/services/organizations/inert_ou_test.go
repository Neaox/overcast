package organizations

// Behavioural coverage for the Tier 1 root and organizational-unit surface,
// over the real Dispatch method and the real store — the sibling of
// inert_policy_test.go for the resource added by #1813.
//
// Unlike the policy pilot, this resource *does* carry compat suite coverage:
// compat/model/recipes/organizations.json declares an `ou` lifecycle and a
// `root` read, and cmd/compatgen generates the three suites' tests from it
// (#1818). Those groups exercise the same operations through real SDK
// clients; these tests exercise the branches an SDK-driven happy path never
// reaches — a missing parent, a duplicate name, a non-empty delete, a
// malformed page token.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// createTestOU creates one organizational unit under parentID and returns the
// OrganizationalUnit object the response carried.
func createTestOU(t *testing.T, s *Service, parentID, name string) map[string]any {
	t.Helper()
	body := dispatchJSON(t, s, "CreateOrganizationalUnit", map[string]any{
		"Name":     name,
		"ParentId": parentID,
	})
	ou, ok := body["OrganizationalUnit"].(map[string]any)
	if !ok {
		t.Fatalf("CreateOrganizationalUnit returned %v, want an OrganizationalUnit", body)
	}
	return ou
}

// testRootID is the root id the service under test derives, obtained the way
// a client would rather than by calling the derivation directly.
func testRootID(t *testing.T, s *Service) string {
	t.Helper()
	body := dispatchJSON(t, s, "ListRoots", map[string]any{})
	roots, ok := body["Roots"].([]any)
	if !ok || len(roots) == 0 {
		t.Fatalf("ListRoots returned %v, want one root", body)
	}
	root, ok := roots[0].(map[string]any)
	if !ok {
		t.Fatalf("ListRoots returned %v, want a Root object", roots[0])
	}
	id, _ := root["Id"].(string)
	if id == "" {
		t.Fatalf("ListRoots returned a root with no Id: %v", root)
	}
	return id
}

// TestListRoots_ReturnsTheSingleOrganizationRoot pins the whole Root
// projection: an organization has exactly one root, its identifier and ARN
// match the modeled patterns, and it is named "Root" as AWS names the root it
// creates with an organization.
func TestListRoots_ReturnsTheSingleOrganizationRoot(t *testing.T) {
	// Given: a service with nothing created in it.
	s := newTestService(t)

	// When: ListRoots is called.
	body := dispatchJSON(t, s, "ListRoots", map[string]any{})

	// Then: exactly one AWS-shaped root comes back.
	roots, ok := body["Roots"].([]any)
	if !ok || len(roots) != 1 {
		t.Fatalf("ListRoots returned %v, want exactly one root", body)
	}
	root, _ := roots[0].(map[string]any)
	id, _ := root["Id"].(string)
	if !rootIDPattern.MatchString(id) {
		t.Fatalf("Root.Id = %q, want a match for %s", id, rootIDPattern.String())
	}
	arn, _ := root["Arn"].(string)
	if !rootArnPattern.MatchString(arn) {
		t.Fatalf("Root.Arn = %q, want a match for %s", arn, rootArnPattern.String())
	}
	if name, _ := root["Name"].(string); name != "Root" {
		t.Fatalf("Root.Name = %q, want %q", name, "Root")
	}
	// PolicyTypes is the set of policy types *enabled on this root*.
	// EnablePolicyType and DisablePolicyType are Tier 0 here, so nothing can
	// ever have enabled one, and the honest answer is the empty list rather
	// than a claim that SCPs are switched on.
	types, ok := root["PolicyTypes"].([]any)
	if !ok || len(types) != 0 {
		t.Fatalf("Root.PolicyTypes = %v, want an empty list", root["PolicyTypes"])
	}
	if token, present := body["NextToken"]; present {
		t.Fatalf("ListRoots returned NextToken %v for a single root", token)
	}
}

// TestListRoots_IsStableAcrossRestarts holds the property every ARN this
// resource mints depends on: the root id is derived, not minted, so a service
// rebuilt over a different store still names the same root.
func TestListRoots_IsStableAcrossRestarts(t *testing.T) {
	first := testRootID(t, newTestService(t))
	second := testRootID(t, newTestService(t))
	if first != second {
		t.Fatalf("root id moved between two services on the same account: %q then %q", first, second)
	}
}

// TestCreateOrganizationalUnit_DerivesTheModeledIdentifierARNAndPath pins the
// three §3.5 derivations against the shapes the model declares for them.
func TestCreateOrganizationalUnit_DerivesTheModeledIdentifierARNAndPath(t *testing.T) {
	// Given: a service and its root.
	s := newTestService(t)
	rootID := testRootID(t, s)

	// When: an organizational unit is created under the root.
	ou := createTestOU(t, s, rootID, "engineering")

	// Then: every identifier it carries matches the model.
	id, _ := ou["Id"].(string)
	if !organizationalUnitIDPattern.MatchString(id) {
		t.Fatalf("OrganizationalUnit.Id = %q, want a match for %s", id, organizationalUnitIDPattern.String())
	}
	// The id's first segment is the root's four characters, which is what
	// makes an OU id name the tree it belongs to, as AWS's do.
	if want := "ou-" + strings.TrimPrefix(rootID, "r-") + "-"; !strings.HasPrefix(id, want) {
		t.Fatalf("OrganizationalUnit.Id = %q, want it to start with %q", id, want)
	}
	arn, _ := ou["Arn"].(string)
	if !organizationalUnitArnPattern.MatchString(arn) {
		t.Fatalf("OrganizationalUnit.Arn = %q, want a match for %s", arn, organizationalUnitArnPattern.String())
	}
	path, _ := ou["Path"].(string)
	if !pathPattern.MatchString(path) {
		t.Fatalf("OrganizationalUnit.Path = %q, want a match for %s", path, pathPattern.String())
	}
	if want := s.organizationID() + "/" + rootID + "/" + id + "/"; path != want {
		t.Fatalf("OrganizationalUnit.Path = %q, want %q", path, want)
	}
	if name, _ := ou["Name"].(string); name != "engineering" {
		t.Fatalf("OrganizationalUnit.Name = %q, want %q", name, "engineering")
	}
}

// TestCreateOrganizationalUnit_NestsUnderAnotherOU: ParentId accepts an OU as
// readily as the root, and the path grows a segment for each level.
func TestCreateOrganizationalUnit_NestsUnderAnotherOU(t *testing.T) {
	// Given: an OU under the root.
	s := newTestService(t)
	rootID := testRootID(t, s)
	parent := createTestOU(t, s, rootID, "platform")
	parentID, _ := parent["Id"].(string)

	// When: a second OU is created under the first.
	child := createTestOU(t, s, parentID, "platform-data")

	// Then: its path names the whole chain, and the model's Path pattern
	// accepts it.
	path, _ := child["Path"].(string)
	childID, _ := child["Id"].(string)
	want := s.organizationID() + "/" + rootID + "/" + parentID + "/" + childID + "/"
	if path != want {
		t.Fatalf("nested OU Path = %q, want %q", path, want)
	}
	if !pathPattern.MatchString(path) {
		t.Fatalf("nested OU Path = %q, want a match for %s", path, pathPattern.String())
	}
}

// TestCreateOrganizationalUnit_RejectsAParentThatIsNotThere is
// CreateOrganizationalUnit's own declared ParentNotFoundException: a parent
// that does not resolve is a modeled 404, never a fabricated success under
// some invented root.
func TestCreateOrganizationalUnit_RejectsAParentThatIsNotThere(t *testing.T) {
	s := newTestService(t)
	for _, parentID := range []string{
		"r-zzzz",              // root-shaped, but not this organization's root
		"ou-zzzz-99999999",    // OU-shaped, but never created
		"not-a-parent-at-all", // not an identifier this service mints at all
	} {
		rec := dispatch(t, s, "CreateOrganizationalUnit", map[string]any{"Name": "orphan", "ParentId": parentID})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("CreateOrganizationalUnit under %q returned %d, want 404", parentID, rec.Code)
		}
		if code := errorCode(t, rec.Body.Bytes()); code != "ParentNotFoundException" {
			t.Fatalf("CreateOrganizationalUnit under %q returned %q, want ParentNotFoundException", parentID, code)
		}
	}
}

// TestCreateOrganizationalUnit_ValidatesNameAgainstTheModel is §3.4: exactly
// the checks the model states — @required presence and @length 1..128 — and
// nothing else.
func TestCreateOrganizationalUnit_ValidatesNameAgainstTheModel(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)

	for _, tc := range []struct {
		name  string
		input map[string]any
	}{
		{"missing Name", map[string]any{"ParentId": rootID}},
		{"empty Name", map[string]any{"Name": "", "ParentId": rootID}},
		{"Name over the modeled 128", map[string]any{"Name": strings.Repeat("n", 129), "ParentId": rootID}},
		{"missing ParentId", map[string]any{"Name": "no-parent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := dispatch(t, s, "CreateOrganizationalUnit", tc.input)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("CreateOrganizationalUnit returned %d, want 400", rec.Code)
			}
			if code := errorCode(t, rec.Body.Bytes()); code != "InvalidInputException" {
				t.Fatalf("CreateOrganizationalUnit returned %q, want InvalidInputException", code)
			}
		})
	}

	// The boundary itself is legal: 128 characters is the modeled maximum,
	// not one past it.
	if ou := createTestOU(t, s, rootID, strings.Repeat("n", 128)); ou["Id"] == nil {
		t.Fatalf("a 128-character name was refused, but @length max is 128")
	}
}

// TestCreateOrganizationalUnit_RefusesADuplicateNameUnderTheSameParent is
// DuplicateOrganizationalUnitException, which AWS scopes to the parent: the
// same name under a *different* parent is legal, and the second half of this
// test is what stops the check being written as a global name index.
func TestCreateOrganizationalUnit_RefusesADuplicateNameUnderTheSameParent(t *testing.T) {
	// Given: an OU named "shared" under the root.
	s := newTestService(t)
	rootID := testRootID(t, s)
	createTestOU(t, s, rootID, "shared")

	// When: a second OU of the same name is created under the same parent.
	rec := dispatch(t, s, "CreateOrganizationalUnit", map[string]any{"Name": "shared", "ParentId": rootID})

	// Then: the modeled conflict comes back.
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate CreateOrganizationalUnit returned %d, want 409", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "DuplicateOrganizationalUnitException" {
		t.Fatalf("duplicate CreateOrganizationalUnit returned %q, want DuplicateOrganizationalUnitException", code)
	}

	// And: the same name under another parent is accepted, because the
	// constraint is per-parent.
	other := createTestOU(t, s, rootID, "other-parent")
	otherID, _ := other["Id"].(string)
	if nested := createTestOU(t, s, otherID, "shared"); nested["Id"] == nil {
		t.Fatalf("the same OU name under a different parent was refused")
	}
}

// TestDescribeOrganizationalUnit_ReadsBackWhatCreateStored is §3.1's
// create-read pair for this resource, plus its modeled not-found.
func TestDescribeOrganizationalUnit_ReadsBackWhatCreateStored(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)
	created := createTestOU(t, s, rootID, "readback")
	id, _ := created["Id"].(string)

	body := dispatchJSON(t, s, "DescribeOrganizationalUnit", map[string]any{"OrganizationalUnitId": id})
	read, ok := body["OrganizationalUnit"].(map[string]any)
	if !ok {
		t.Fatalf("DescribeOrganizationalUnit returned %v, want an OrganizationalUnit", body)
	}
	for _, field := range []string{"Id", "Arn", "Name", "Path"} {
		if read[field] != created[field] {
			t.Fatalf("DescribeOrganizationalUnit returned %s=%v, want %v", field, read[field], created[field])
		}
	}
}

func TestDescribeOrganizationalUnit_NotFound(t *testing.T) {
	s := newTestService(t)
	rec := dispatch(t, s, "DescribeOrganizationalUnit", map[string]any{"OrganizationalUnitId": "ou-zzzz-99999999"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DescribeOrganizationalUnit of an unknown id returned %d, want 404", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "OrganizationalUnitNotFoundException" {
		t.Fatalf("DescribeOrganizationalUnit of an unknown id returned %q, want OrganizationalUnitNotFoundException", code)
	}
}

// TestUpdateOrganizationalUnit_RenamesAndKeepsTheIdentityStable: on AWS an
// OU's id and ARN are fixed for its life, so a rename must move Name and
// nothing else.
func TestUpdateOrganizationalUnit_RenamesAndKeepsTheIdentityStable(t *testing.T) {
	// Given: an OU.
	s := newTestService(t)
	rootID := testRootID(t, s)
	created := createTestOU(t, s, rootID, "before")
	id, _ := created["Id"].(string)

	// When: it is renamed.
	body := dispatchJSON(t, s, "UpdateOrganizationalUnit", map[string]any{
		"OrganizationalUnitId": id,
		"Name":                 "after",
	})
	updated, ok := body["OrganizationalUnit"].(map[string]any)
	if !ok {
		t.Fatalf("UpdateOrganizationalUnit returned %v, want an OrganizationalUnit", body)
	}

	// Then: the name moved and the identity did not.
	if name, _ := updated["Name"].(string); name != "after" {
		t.Fatalf("UpdateOrganizationalUnit returned Name %q, want %q", name, "after")
	}
	if updated["Id"] != created["Id"] || updated["Arn"] != created["Arn"] || updated["Path"] != created["Path"] {
		t.Fatalf("a rename moved the OU's identity: %v then %v", created, updated)
	}

	// And: the read agrees, so the rename was persisted rather than only
	// echoed.
	read := dispatchJSON(t, s, "DescribeOrganizationalUnit", map[string]any{"OrganizationalUnitId": id})
	ou, _ := read["OrganizationalUnit"].(map[string]any)
	if name, _ := ou["Name"].(string); name != "after" {
		t.Fatalf("DescribeOrganizationalUnit after the rename returned Name %q, want %q", name, "after")
	}
}

// TestUpdateOrganizationalUnit_RefusesASiblingsName: UpdateOrganizationalUnit
// declares DuplicateOrganizationalUnitException too, and a rename onto a
// sibling's name is the only way to reach it.
func TestUpdateOrganizationalUnit_RefusesASiblingsName(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)
	createTestOU(t, s, rootID, "taken")
	mover := createTestOU(t, s, rootID, "mover")
	moverID, _ := mover["Id"].(string)

	rec := dispatch(t, s, "UpdateOrganizationalUnit", map[string]any{
		"OrganizationalUnitId": moverID,
		"Name":                 "taken",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("renaming onto a sibling's name returned %d, want 409", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "DuplicateOrganizationalUnitException" {
		t.Fatalf("renaming onto a sibling's name returned %q, want DuplicateOrganizationalUnitException", code)
	}

	// Renaming an OU to the name it already has is a no-op, not a conflict
	// with itself.
	if body := dispatchJSON(t, s, "UpdateOrganizationalUnit", map[string]any{
		"OrganizationalUnitId": moverID, "Name": "mover",
	}); body["OrganizationalUnit"] == nil {
		t.Fatalf("renaming an OU to its own name was refused: %v", body)
	}
}

// TestDeleteOrganizationalUnit_RefusesANonEmptyUnit is
// OrganizationalUnitNotEmptyException. Accounts are not modeled here, so a
// child OU is the only thing that can make a unit non-empty — and the error
// stays reachable, rather than being unreachable by construction the way
// DeletePolicy's PolicyInUseException is.
func TestDeleteOrganizationalUnit_RefusesANonEmptyUnit(t *testing.T) {
	// Given: an OU with a child.
	s := newTestService(t)
	rootID := testRootID(t, s)
	parent := createTestOU(t, s, rootID, "has-children")
	parentID, _ := parent["Id"].(string)
	child := createTestOU(t, s, parentID, "the-child")
	childID, _ := child["Id"].(string)

	// When: the parent is deleted.
	rec := dispatch(t, s, "DeleteOrganizationalUnit", map[string]any{"OrganizationalUnitId": parentID})

	// Then: the modeled conflict comes back and the parent is still there.
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting a non-empty OU returned %d, want 409", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "OrganizationalUnitNotEmptyException" {
		t.Fatalf("deleting a non-empty OU returned %q, want OrganizationalUnitNotEmptyException", code)
	}
	if body := dispatchJSON(t, s, "DescribeOrganizationalUnit", map[string]any{"OrganizationalUnitId": parentID}); body["OrganizationalUnit"] == nil {
		t.Fatalf("the refused delete removed the OU anyway: %v", body)
	}

	// And: emptying it first makes the delete succeed.
	if rec := dispatch(t, s, "DeleteOrganizationalUnit", map[string]any{"OrganizationalUnitId": childID}); rec.Code != http.StatusOK {
		t.Fatalf("deleting the child returned %d, want 200", rec.Code)
	}
	if rec := dispatch(t, s, "DeleteOrganizationalUnit", map[string]any{"OrganizationalUnitId": parentID}); rec.Code != http.StatusOK {
		t.Fatalf("deleting the now-empty parent returned %d, want 200", rec.Code)
	}
}

// TestDeleteOrganizationalUnit_TakesItsTagsWithIt mirrors
// TestDeletePolicy_TakesItsTagsWithIt: namespaced tags have nothing tying
// them to the record's lifetime, so a delete that forgets them leaves
// ListTagsForResource answering for a unit that is gone.
func TestDeleteOrganizationalUnit_TakesItsTagsWithIt(t *testing.T) {
	// Given: a tagged OU.
	s := newTestService(t)
	rootID := testRootID(t, s)
	ou := createTestOU(t, s, rootID, "tagged-then-deleted")
	id, _ := ou["Id"].(string)
	if rec := dispatch(t, s, "TagResource", map[string]any{
		"ResourceId": id,
		"Tags":       []map[string]string{{"Key": "owner", "Value": "platform"}},
	}); rec.Code != http.StatusOK {
		t.Fatalf("TagResource returned %d", rec.Code)
	}

	// When: it is deleted and an identically named one is created again —
	// which, because the id derives from parent and name, lands on the same
	// identifier and so the same tag key.
	if rec := dispatch(t, s, "DeleteOrganizationalUnit", map[string]any{"OrganizationalUnitId": id}); rec.Code != http.StatusOK {
		t.Fatalf("DeleteOrganizationalUnit returned %d", rec.Code)
	}
	again := createTestOU(t, s, rootID, "tagged-then-deleted")
	if againID, _ := again["Id"].(string); againID != id {
		t.Fatalf("recreating the OU minted %q, want the derived %q", againID, id)
	}

	// Then: the new unit carries none of the old one's tags.
	body := dispatchJSON(t, s, "ListTagsForResource", map[string]any{"ResourceId": id})
	if tags := tagsFromResponse(t, body); len(tags) != 0 {
		t.Fatalf("the recreated OU inherited %v from the deleted one", tags)
	}
}

// TestOrganizationalUnitTags_ShareTheStoreWithPolicyTags is §7.3's "two
// stores for one resource is the failure mode to design against", applied
// across resource types: an OU's tags go through the same shared tag store,
// and tagging one resource must not touch another's.
func TestOrganizationalUnitTags_ShareTheStoreWithPolicyTags(t *testing.T) {
	// Given: a policy and an OU, each tagged.
	s := newTestService(t)
	rootID := testRootID(t, s)
	policy := createTestPolicy(t, s, "tag-isolation")
	policyID, _ := policy["Id"].(string)
	ou := createTestOU(t, s, rootID, "tag-isolation")
	ouID, _ := ou["Id"].(string)

	for id, value := range map[string]string{policyID: "policy", ouID: "ou"} {
		if rec := dispatch(t, s, "TagResource", map[string]any{
			"ResourceId": id,
			"Tags":       []map[string]string{{"Key": "kind", "Value": value}},
		}); rec.Code != http.StatusOK {
			t.Fatalf("TagResource on %q returned %d", id, rec.Code)
		}
	}

	// Then: each reads back only its own.
	for id, want := range map[string]string{policyID: "policy", ouID: "ou"} {
		tags := tagsFromResponse(t, dispatchJSON(t, s, "ListTagsForResource", map[string]any{"ResourceId": id}))
		if len(tags) != 1 || tags[0].Key != "kind" || tags[0].Value != want {
			t.Fatalf("ListTagsForResource(%q) = %v, want a single kind=%s", id, tags, want)
		}
	}

	// And: untagging one leaves the other alone.
	if rec := dispatch(t, s, "UntagResource", map[string]any{"ResourceId": ouID, "TagKeys": []string{"kind"}}); rec.Code != http.StatusOK {
		t.Fatalf("UntagResource returned %d", rec.Code)
	}
	if tags := tagsFromResponse(t, dispatchJSON(t, s, "ListTagsForResource", map[string]any{"ResourceId": policyID})); len(tags) != 1 {
		t.Fatalf("untagging the OU disturbed the policy's tags: %v", tags)
	}
}

// TestCreateOrganizationalUnitTags_AreVisibleToListTagsForResource: tags on
// the create input write through to the store the tag operations read, so the
// two can never disagree.
func TestCreateOrganizationalUnitTags_AreVisibleToListTagsForResource(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)
	body := dispatchJSON(t, s, "CreateOrganizationalUnit", map[string]any{
		"Name":     "tagged-at-birth",
		"ParentId": rootID,
		"Tags":     []map[string]string{{"Key": "team", "Value": "platform"}},
	})
	ou, ok := body["OrganizationalUnit"].(map[string]any)
	if !ok {
		t.Fatalf("CreateOrganizationalUnit returned %v", body)
	}
	id, _ := ou["Id"].(string)

	tags := tagsFromResponse(t, dispatchJSON(t, s, "ListTagsForResource", map[string]any{"ResourceId": id}))
	if len(tags) != 1 || tags[0].Key != "team" || tags[0].Value != "platform" {
		t.Fatalf("ListTagsForResource = %v, want the tag CreateOrganizationalUnit accepted", tags)
	}
}

// TestTagOperations_AcceptTheRoot: the root is a resource this service can
// name and describe, so refusing to tag it would be telling a caller that
// something ListRoots just returned does not exist.
func TestTagOperations_AcceptTheRoot(t *testing.T) {
	s := newTestService(t)
	rootID := testRootID(t, s)

	if rec := dispatch(t, s, "TagResource", map[string]any{
		"ResourceId": rootID,
		"Tags":       []map[string]string{{"Key": "scope", "Value": "root"}},
	}); rec.Code != http.StatusOK {
		t.Fatalf("TagResource on the root returned %d", rec.Code)
	}
	tags := tagsFromResponse(t, dispatchJSON(t, s, "ListTagsForResource", map[string]any{"ResourceId": rootID}))
	if len(tags) != 1 || tags[0].Key != "scope" {
		t.Fatalf("ListTagsForResource(root) = %v, want the tag just applied", tags)
	}

	// A root-shaped id that is not this organization's root is still unknown.
	rec := dispatch(t, s, "ListTagsForResource", map[string]any{"ResourceId": "r-zzzz"})
	if code := errorCode(t, rec.Body.Bytes()); code != "TargetNotFoundException" {
		t.Fatalf("ListTagsForResource on a foreign root returned %q, want TargetNotFoundException", code)
	}
}

// TestListOrganizationalUnitsForParent_FiltersByParent: the operation lists
// one parent's children, not every unit in the organization.
func TestListOrganizationalUnitsForParent_FiltersByParent(t *testing.T) {
	// Given: two units under the root, one of which has a child.
	s := newTestService(t)
	rootID := testRootID(t, s)
	first := createTestOU(t, s, rootID, "first")
	createTestOU(t, s, rootID, "second")
	firstID, _ := first["Id"].(string)
	child := createTestOU(t, s, firstID, "nested")
	childID, _ := child["Id"].(string)

	// When/Then: the root lists its two children and not the grandchild.
	rootChildren := ouIDsFromList(t, dispatchJSON(t, s, "ListOrganizationalUnitsForParent", map[string]any{"ParentId": rootID}))
	if len(rootChildren) != 2 {
		t.Fatalf("the root listed %v, want exactly its two direct children", rootChildren)
	}
	for _, id := range rootChildren {
		if id == childID {
			t.Fatalf("the root listed the grandchild %q", childID)
		}
	}

	// And: the first unit lists its own child.
	nested := ouIDsFromList(t, dispatchJSON(t, s, "ListOrganizationalUnitsForParent", map[string]any{"ParentId": firstID}))
	if len(nested) != 1 || nested[0] != childID {
		t.Fatalf("%q listed %v, want [%s]", firstID, nested, childID)
	}
}

// TestListOrganizationalUnitsForParent_RejectsAnUnknownParent: the operation
// declares ParentNotFoundException, so an unknown parent is a modeled 404
// rather than an empty list — the difference between "no children" and "no
// such parent" is one a caller acts on.
func TestListOrganizationalUnitsForParent_RejectsAnUnknownParent(t *testing.T) {
	s := newTestService(t)
	rec := dispatch(t, s, "ListOrganizationalUnitsForParent", map[string]any{"ParentId": "ou-zzzz-99999999"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("listing an unknown parent returned %d, want 404", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "ParentNotFoundException" {
		t.Fatalf("listing an unknown parent returned %q, want ParentNotFoundException", code)
	}
}

// TestListOrganizationalUnitsForParent_PaginatesAndRejectsAGarbageToken
// covers both halves of §3.1's List class: MaxResults and NextToken are
// honoured, and an undecodable token is the modeled error rather than a
// silent restart at page 1 (pagination-plan H1/G3).
func TestListOrganizationalUnitsForParent_PaginatesAndRejectsAGarbageToken(t *testing.T) {
	// Given: five units under the root.
	s := newTestService(t)
	rootID := testRootID(t, s)
	want := map[string]bool{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		ou := createTestOU(t, s, rootID, name)
		id, _ := ou["Id"].(string)
		want[id] = true
	}

	// When: they are paged two at a time.
	seen := map[string]int{}
	token, pages := "", 0
	for {
		params := map[string]any{"ParentId": rootID, "MaxResults": 2}
		if token != "" {
			params["NextToken"] = token
		}
		body := dispatchJSON(t, s, "ListOrganizationalUnitsForParent", params)
		ids := ouIDsFromList(t, body)
		if len(ids) > 2 {
			t.Fatalf("a page carried %d units, want at most the requested 2", len(ids))
		}
		for _, id := range ids {
			seen[id]++
		}
		pages++
		token, _ = body["NextToken"].(string)
		if token == "" || pages > 10 {
			break
		}
	}

	// Then: every unit appeared exactly once, across more than one page.
	if pages < 3 {
		t.Fatalf("five units at page size 2 came back in %d pages — the limit is being ignored", pages)
	}
	for id := range want {
		if seen[id] != 1 {
			t.Fatalf("unit %q appeared %d times across the page sequence, want exactly 1", id, seen[id])
		}
	}

	// And: a token that decodes to nothing is refused.
	rec := dispatch(t, s, "ListOrganizationalUnitsForParent", map[string]any{"ParentId": rootID, "NextToken": "not-a-real-token"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a garbage NextToken returned %d, want 400", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "InvalidInputException" {
		t.Fatalf("a garbage NextToken returned %q, want InvalidInputException", code)
	}
}

// TestListRoots_RejectsAGarbageToken holds the same rule for the other
// paginated operation this file adds.
func TestListRoots_RejectsAGarbageToken(t *testing.T) {
	s := newTestService(t)
	rec := dispatch(t, s, "ListRoots", map[string]any{"NextToken": "not-a-real-token"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a garbage NextToken returned %d, want 400", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "InvalidInputException" {
		t.Fatalf("a garbage NextToken returned %q, want InvalidInputException", code)
	}
}

// TestOrganizationalUnitTimestampsComeFromTheClock is §3.5's timestamp rule
// for this resource. OrganizationalUnit models no timestamp member, so the
// conformance clause has nothing on the wire to look at and the rule is held
// here, against the persisted record.
func TestOrganizationalUnitTimestampsComeFromTheClock(t *testing.T) {
	// Given: a service whose clock is frozen at a known instant.
	clk := clock.NewMock()
	fixed := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	clk.Set(fixed)
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clk)

	// When: a unit is created, then renamed 48h later on that clock.
	rootID := testRootID(t, s)
	ou := createTestOU(t, s, rootID, "clocked")
	id, _ := ou["Id"].(string)
	clk.Add(48 * time.Hour)
	if body := dispatchJSON(t, s, "UpdateOrganizationalUnit", map[string]any{
		"OrganizationalUnitId": id, "Name": "clocked-again",
	}); body["OrganizationalUnit"] == nil {
		t.Fatalf("UpdateOrganizationalUnit returned %v", body)
	}

	// Then: both timestamps track the injected clock, not wall time.
	rec, found, err := s.ous.Get(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("reading back the record: (%v, %v, %v)", rec, found, err)
	}
	if !rec.CreatedAt.Equal(fixed) {
		t.Fatalf("CreatedAt = %v, want the injected %v — the handler is reading time.Now()", rec.CreatedAt, fixed)
	}
	if !rec.UpdatedAt.Equal(fixed.Add(48 * time.Hour)) {
		t.Fatalf("UpdatedAt = %v, want %v", rec.UpdatedAt, fixed.Add(48*time.Hour))
	}
}

// TestMalformedOrganizationalUnitRecord_ReadsAsNotFound: one corrupt
// persisted blob must not turn a read into a 500, nor take the parent's whole
// listing with it (AGENTS.md § "Malformed persisted state must be isolated").
func TestMalformedOrganizationalUnitRecord_ReadsAsNotFound(t *testing.T) {
	// Given: two units, one of which has been corrupted in the store.
	st := state.NewMemoryStore()
	t.Cleanup(func() { _ = st.Close() })
	s := New(&config.Config{AccountID: "000000000000"}, st, zap.NewNop(), clock.NewMock())
	rootID := testRootID(t, s)
	good := createTestOU(t, s, rootID, "intact")
	bad := createTestOU(t, s, rootID, "corrupt")
	badID, _ := bad["Id"].(string)
	if err := st.Set(context.Background(), nsOrganizationalUnits, badID, "{not json"); err != nil {
		t.Fatalf("corrupting the record: %v", err)
	}

	// When/Then: reading the corrupt one is the modeled not-found, not a 500.
	rec := dispatch(t, s, "DescribeOrganizationalUnit", map[string]any{"OrganizationalUnitId": badID})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reading a malformed record returned %d, want 404", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "OrganizationalUnitNotFoundException" {
		t.Fatalf("reading a malformed record returned %q, want OrganizationalUnitNotFoundException", code)
	}

	// And: the intact unit still lists.
	ids := ouIDsFromList(t, dispatchJSON(t, s, "ListOrganizationalUnitsForParent", map[string]any{"ParentId": rootID}))
	if len(ids) != 1 || ids[0] != good["Id"] {
		t.Fatalf("the parent listed %v, want only the intact unit %v", ids, good["Id"])
	}
}

// ouIDsFromList pulls the identifiers out of a
// ListOrganizationalUnitsForParent response, in the order they came back.
func ouIDsFromList(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, ok := body["OrganizationalUnits"].([]any)
	if !ok {
		t.Fatalf("response carried no OrganizationalUnits list: %v", body)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		ou, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("OrganizationalUnits carried %v, want objects", item)
		}
		id, _ := ou["Id"].(string)
		out = append(out, id)
	}
	return out
}
