// Package appregistry_test contains integration tests for the AppRegistry emulator.
package appregistry_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func arDo(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func createApp(t *testing.T, srv *helpers.TestServer, name string) map[string]any {
	t.Helper()
	resp := arDo(t, srv, http.MethodPost, "/applications", map[string]any{"name": name, "description": "test"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Application map[string]any `json:"application"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Application
}

func TestCreateApplication_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	app := createApp(t, srv, "payments-service")
	if app["name"] != "payments-service" {
		t.Errorf("expected name=payments-service, got %v", app["name"])
	}
	if app["id"] == "" || app["id"] == nil {
		t.Error("expected id to be set")
	}
	if app["arn"] == "" || app["arn"] == nil {
		t.Error("expected arn to be set")
	}
	tag, _ := app["applicationTag"].(map[string]any)
	if tag == nil || tag["awsApplication"] != app["arn"] {
		t.Errorf("expected applicationTag.awsApplication == arn, got %v", tag)
	}
}

func TestCreateApplication_missingName(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := arDo(t, srv, http.MethodPost, "/applications", map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestCreateApplication_duplicate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "dupe")
	resp := arDo(t, srv, http.MethodPost, "/applications", map[string]any{"name": "dupe"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusConflict)
	helpers.AssertJSONError(t, resp, "ConflictException")
}

func TestListApplications(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "alpha")
	createApp(t, srv, "beta")
	resp := arDo(t, srv, http.MethodGet, "/applications", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Applications []map[string]any `json:"applications"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Applications) != 2 {
		t.Errorf("expected 2 applications, got %d", len(result.Applications))
	}
}

func TestGetApplication_byName(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "by-name")
	resp := arDo(t, srv, http.MethodGet, "/applications/by-name", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	helpers.DecodeJSON(t, resp, &result)
	if result["name"] != "by-name" {
		t.Errorf("got %v", result)
	}
}

func TestGetApplication_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := arDo(t, srv, http.MethodGet, "/applications/missing", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestDeleteApplication(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "to-delete")
	resp := arDo(t, srv, http.MethodDelete, "/applications/to-delete", nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Subsequent GET should 404.
	resp2 := arDo(t, srv, http.MethodGet, "/applications/to-delete", nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusNotFound)
}

func TestAssociateResource_roundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "assoc-app")

	stackArn := "arn:aws:cloudformation:us-east-1:000000000000:stack/my-stack/" + "abcdef"
	escaped := url.PathEscape(stackArn)

	// Associate.
	resp := arDo(t, srv, http.MethodPut, "/applications/assoc-app/resources/CFN_STACK/"+escaped, map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// List.
	resp2 := arDo(t, srv, http.MethodGet, "/applications/assoc-app/resources", nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var list struct {
		Resources []map[string]any `json:"resources"`
	}
	helpers.DecodeJSON(t, resp2, &list)
	if len(list.Resources) != 1 || list.Resources[0]["arn"] != stackArn {
		t.Errorf("expected one resource with arn=%s, got %+v", stackArn, list.Resources)
	}

	// Disassociate.
	resp3 := arDo(t, srv, http.MethodDelete, "/applications/assoc-app/resources/CFN_STACK/"+escaped, nil)
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusOK)

	// List empty.
	resp4 := arDo(t, srv, http.MethodGet, "/applications/assoc-app/resources", nil)
	defer resp4.Body.Close()
	var list2 struct {
		Resources []map[string]any `json:"resources"`
	}
	helpers.DecodeJSON(t, resp4, &list2)
	if len(list2.Resources) != 0 {
		t.Errorf("expected empty, got %+v", list2.Resources)
	}
}

func TestAssociateResource_unknownApp(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := arDo(t, srv, http.MethodPut, "/applications/missing/resources/CFN_STACK/stack-foo", map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestUpdateApplication(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "to-update")

	resp := arDo(t, srv, http.MethodPatch, "/applications/to-update", map[string]any{
		"name":        "updated-name",
		"description": "new description",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Application map[string]any `json:"application"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Application["name"] != "updated-name" {
		t.Errorf("expected name=updated-name, got %v", result.Application["name"])
	}
	if result.Application["description"] != "new description" {
		t.Errorf("expected updated description, got %v", result.Application["description"])
	}

	// Old name should no longer resolve.
	resp2 := arDo(t, srv, http.MethodGet, "/applications/to-update", nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusNotFound)
}

func TestUpdateApplication_nameConflict(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "app-a")
	createApp(t, srv, "app-b")

	resp := arDo(t, srv, http.MethodPatch, "/applications/app-a", map[string]any{"name": "app-b"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusConflict)
}

// ─── Attribute groups (inert tier) ────────────────────────────────────────

func TestAttributeGroup_CRUD(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create.
	resp := arDo(t, srv, http.MethodPost, "/attribute-groups", map[string]any{
		"name":        "env-tags",
		"description": "env metadata",
		"attributes":  `{"environment":"prod"}`,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		AttributeGroup map[string]any `json:"attributeGroup"`
	}
	helpers.DecodeJSON(t, resp, &created)
	if created.AttributeGroup["name"] != "env-tags" {
		t.Fatalf("got %+v", created.AttributeGroup)
	}

	// Get by name.
	resp2 := arDo(t, srv, http.MethodGet, "/attribute-groups/env-tags", nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// List.
	resp3 := arDo(t, srv, http.MethodGet, "/attribute-groups", nil)
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusOK)
	var list struct {
		AttributeGroups []map[string]any `json:"attributeGroups"`
	}
	helpers.DecodeJSON(t, resp3, &list)
	if len(list.AttributeGroups) != 1 {
		t.Errorf("expected 1 attribute group, got %d", len(list.AttributeGroups))
	}

	// Update.
	resp4 := arDo(t, srv, http.MethodPatch, "/attribute-groups/env-tags", map[string]any{
		"description": "updated",
	})
	defer resp4.Body.Close()
	helpers.AssertStatus(t, resp4, http.StatusOK)

	// Delete.
	resp5 := arDo(t, srv, http.MethodDelete, "/attribute-groups/env-tags", nil)
	defer resp5.Body.Close()
	helpers.AssertStatus(t, resp5, http.StatusOK)

	resp6 := arDo(t, srv, http.MethodGet, "/attribute-groups/env-tags", nil)
	defer resp6.Body.Close()
	helpers.AssertStatus(t, resp6, http.StatusNotFound)
}

func TestAttributeGroup_Association(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createApp(t, srv, "ag-app")
	resp := arDo(t, srv, http.MethodPost, "/attribute-groups", map[string]any{"name": "ag-one"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Associate.
	resp2 := arDo(t, srv, http.MethodPut, "/applications/ag-app/attribute-groups/ag-one", map[string]any{})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// List associated attribute groups.
	resp3 := arDo(t, srv, http.MethodGet, "/applications/ag-app/attribute-groups", nil)
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusOK)
	var list struct {
		AttributeGroups []string `json:"attributeGroups"`
	}
	helpers.DecodeJSON(t, resp3, &list)
	if len(list.AttributeGroups) != 1 {
		t.Errorf("expected 1 attribute group association, got %+v", list.AttributeGroups)
	}

	// Disassociate.
	resp4 := arDo(t, srv, http.MethodDelete, "/applications/ag-app/attribute-groups/ag-one", nil)
	defer resp4.Body.Close()
	helpers.AssertStatus(t, resp4, http.StatusOK)
}

// ─── Tags (inert tier) ────────────────────────────────────────────────────

// AppRegistry tag APIs share the router's generic /tags/{resourceArn} mount
// with API Gateway's tag store — AppRegistry's SDK uses POST for TagResource
// while API Gateway uses PUT, but both land in the same ARN-keyed store.
func TestTagResource_roundTripViaSharedStore(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:servicecatalog:us-east-1:000000000000:/applications/abc"

	// POST (AppRegistry verb).
	resp := arDo(t, srv, http.MethodPost, "/tags/"+arn, map[string]any{
		"tags": map[string]string{"team": "platform", "env": "prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// Read back.
	resp2 := arDo(t, srv, http.MethodGet, "/tags/"+arn, nil)
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	var list struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, resp2, &list)
	if list.Tags["team"] != "platform" || list.Tags["env"] != "prod" {
		t.Errorf("expected both tags, got %+v", list.Tags)
	}

	// Untag env (tagKeys is comma-separated in the shared handler).
	resp3 := arDo(t, srv, http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	defer resp3.Body.Close()
	helpers.AssertStatus(t, resp3, http.StatusNoContent)

	resp4 := arDo(t, srv, http.MethodGet, "/tags/"+arn, nil)
	defer resp4.Body.Close()
	var list2 struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, resp4, &list2)
	if _, ok := list2.Tags["env"]; ok {
		t.Errorf("expected env to be removed, got %+v", list2.Tags)
	}
	if list2.Tags["team"] != "platform" {
		t.Errorf("expected team to remain, got %+v", list2.Tags)
	}
}
