package organizations_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// The identifier patterns below mirror the ones in
// models/aws/shapes/organizations.json (see also
// internal/services/organizations/aws_id_pattern_test.go, which pins every
// identifier this service mints against the model). All of them are derived
// rather than fixed, so these tests assert shape, not a literal value.
var (
	organizationIDPattern        = regexp.MustCompile(`^o-[a-z0-9]{10,32}$`)
	rootIDPattern                = regexp.MustCompile(`^r-[0-9a-z]{4,32}$`)
	organizationalUnitIDPattern  = regexp.MustCompile(`^ou-[0-9a-z]{4,32}-[a-z0-9]{8,32}$`)
	organizationalUnitArnPattern = regexp.MustCompile(`^arn:aws:organizations::\d{12}:ou\/o-[a-z0-9]{10,32}\/ou-[0-9a-z]{4,32}-[0-9a-z]{8,32}$`)
)

// orgsCall sends one AWS JSON 1.1 request the way an SDK client would: a POST
// to / carrying X-Amz-Target, through the whole router rather than straight
// at the service.
func orgsCall(t *testing.T, srv *helpers.TestServer, opName string, body any) *http.Response {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSOrganizationsV20161128."+opName)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http request: %v", err)
	}
	return resp
}

// orgsJSON calls opName, requires the expected status, and decodes the body.
func orgsJSON(t *testing.T, srv *helpers.TestServer, opName string, body any, wantStatus int) map[string]any {
	t.Helper()
	resp := orgsCall(t, srv, opName, body)
	if resp.StatusCode != wantStatus {
		defer resp.Body.Close()
		t.Fatalf("%s returned %d, want %d", opName, resp.StatusCode, wantStatus)
	}
	return decodeBody(t, resp)
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	body := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func TestDescribeOrganization(t *testing.T) {
	srv := helpers.NewTestServer(t)

	body := orgsJSON(t, srv, "DescribeOrganization", nil, http.StatusOK)
	org, ok := body["Organization"].(map[string]any)
	if !ok {
		t.Fatalf("expected Organization object, got %#v", body)
	}
	id, _ := org["Id"].(string)
	if !organizationIDPattern.MatchString(id) {
		t.Fatalf("org id %q does not match the modeled OrganizationId pattern %s", id, organizationIDPattern.String())
	}
	if org["MasterAccountId"] != "000000000000" {
		t.Fatalf("unexpected master account id: %v", org["MasterAccountId"])
	}
	if org["Arn"] == "" {
		t.Fatalf("expected non-empty org ARN")
	}
}

// TestOrganizationalUnitLifecycle walks the whole tree surface end to end
// through the router — the same sequence compat/model's `ou` lifecycle drives
// through three real SDK clients, run here in-process so a break shows up in
// `go test` rather than only in the compat suites.
func TestOrganizationalUnitLifecycle(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// The root is the only legal parent for a top-level unit, and ListRoots
	// is how a client finds it.
	roots, ok := orgsJSON(t, srv, "ListRoots", map[string]any{}, http.StatusOK)["Roots"].([]any)
	if !ok || len(roots) != 1 {
		t.Fatalf("ListRoots returned %v, want exactly one root", roots)
	}
	root, _ := roots[0].(map[string]any)
	rootID, _ := root["Id"].(string)
	if !rootIDPattern.MatchString(rootID) {
		t.Fatalf("root id %q does not match the modeled RootId pattern %s", rootID, rootIDPattern.String())
	}

	// Create.
	created := orgsJSON(t, srv, "CreateOrganizationalUnit", map[string]any{
		"Name":     "integration-workloads",
		"ParentId": rootID,
	}, http.StatusOK)
	unit, ok := created["OrganizationalUnit"].(map[string]any)
	if !ok {
		t.Fatalf("CreateOrganizationalUnit returned %v, want an OrganizationalUnit", created)
	}
	id, _ := unit["Id"].(string)
	arn, _ := unit["Arn"].(string)
	if !organizationalUnitIDPattern.MatchString(id) {
		t.Fatalf("OU id %q does not match the modeled pattern %s", id, organizationalUnitIDPattern.String())
	}
	if !organizationalUnitArnPattern.MatchString(arn) {
		t.Fatalf("OU ARN %q does not match the modeled pattern %s", arn, organizationalUnitArnPattern.String())
	}

	// Read back.
	read := orgsJSON(t, srv, "DescribeOrganizationalUnit", map[string]any{"OrganizationalUnitId": id}, http.StatusOK)
	if got, _ := read["OrganizationalUnit"].(map[string]any); got["Name"] != "integration-workloads" {
		t.Fatalf("DescribeOrganizationalUnit returned %v, want the name that was created", got)
	}

	// List under the parent.
	listed := orgsJSON(t, srv, "ListOrganizationalUnitsForParent", map[string]any{"ParentId": rootID}, http.StatusOK)
	if units, _ := listed["OrganizationalUnits"].([]any); len(units) != 1 {
		t.Fatalf("the root listed %v, want the one unit just created", units)
	}

	// Rename.
	renamed := orgsJSON(t, srv, "UpdateOrganizationalUnit", map[string]any{
		"OrganizationalUnitId": id,
		"Name":                 "integration-renamed",
	}, http.StatusOK)
	after, _ := renamed["OrganizationalUnit"].(map[string]any)
	if after["Name"] != "integration-renamed" {
		t.Fatalf("UpdateOrganizationalUnit returned %v, want the new name", after)
	}
	if after["Arn"] != arn || after["Id"] != id {
		t.Fatalf("the rename moved the unit's identity: %v", after)
	}

	// Tag, list tags, untag — the generic Organizations tag operations
	// accept an OU id as readily as a policy id.
	orgsJSON(t, srv, "TagResource", map[string]any{
		"ResourceId": id,
		"Tags":       []map[string]string{{"Key": "env", "Value": "integration"}},
	}, http.StatusOK)
	if tags, _ := orgsJSON(t, srv, "ListTagsForResource", map[string]any{"ResourceId": id}, http.StatusOK)["Tags"].([]any); len(tags) != 1 {
		t.Fatalf("ListTagsForResource returned %v, want the single tag just applied", tags)
	}
	orgsJSON(t, srv, "UntagResource", map[string]any{"ResourceId": id, "TagKeys": []string{"env"}}, http.StatusOK)
	if tags, _ := orgsJSON(t, srv, "ListTagsForResource", map[string]any{"ResourceId": id}, http.StatusOK)["Tags"].([]any); len(tags) != 0 {
		t.Fatalf("ListTagsForResource returned %v after the untag, want none", tags)
	}

	// Delete, and prove it took effect through the modeled error rather than
	// through an empty response.
	orgsJSON(t, srv, "DeleteOrganizationalUnit", map[string]any{"OrganizationalUnitId": id}, http.StatusOK)
	gone := orgsJSON(t, srv, "DescribeOrganizationalUnit", map[string]any{"OrganizationalUnitId": id}, http.StatusNotFound)
	if code, _ := gone["__type"].(string); code != "OrganizationalUnitNotFoundException" {
		t.Fatalf("reading a deleted unit returned %v, want OrganizationalUnitNotFoundException", gone)
	}

	// And the parent no longer lists it — the "zero trace" the compat run
	// asserts after its teardown.
	listed = orgsJSON(t, srv, "ListOrganizationalUnitsForParent", map[string]any{"ParentId": rootID}, http.StatusOK)
	if units, _ := listed["OrganizationalUnits"].([]any); len(units) != 0 {
		t.Fatalf("the root still lists %v after the delete", units)
	}
}

// TestCreateOrganizationalUnit_UnknownParentIsModeled: a parent that does not
// resolve is the operation's own 404, not a unit created under some invented
// root.
func TestCreateOrganizationalUnit_UnknownParentIsModeled(t *testing.T) {
	srv := helpers.NewTestServer(t)

	body := orgsJSON(t, srv, "CreateOrganizationalUnit", map[string]any{
		"Name":     "orphan",
		"ParentId": "r-zzzz",
	}, http.StatusNotFound)
	if code, _ := body["__type"].(string); code != "ParentNotFoundException" {
		t.Fatalf("creating under an unknown parent returned %v, want ParentNotFoundException", body)
	}
}

// TestOrganizationsUnimplementedOperationStays501: the account half of
// Organizations is still Tier 0, and has to say so rather than fabricate a
// success now that its neighbours in the same tree are implemented.
func TestOrganizationsUnimplementedOperationStays501(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := orgsCall(t, srv, "ListAccountsForParent", map[string]any{"ParentId": "r-zzzz"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("ListAccountsForParent returned %d, want 501", resp.StatusCode)
	}
	if resp.Header.Get("x-emulator-unsupported") != "true" {
		t.Fatalf("a 501 came back without x-emulator-unsupported: %v", resp.Header)
	}
}
