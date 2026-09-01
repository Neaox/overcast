// tag_validation_test.go — AppRegistry CreateApplication/CreateAttributeGroup
// tag validation (#1052).
//
// Both used to store whatever a caller sent without checking AWS's own tag
// constraints — a reserved `aws:` key prefix had to be rejected the way real
// AWS rejects it, and neither was. TagResource itself is already covered:
// it shares apigateway's ARN-keyed store and validator (#1052's apigateway
// fix), proven by TestTagResource_roundTripViaSharedStore staying green.
package appregistry_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestCreateApplication_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := arDo(t, srv, http.MethodPost, "/applications", map[string]any{
		"name": "invalid-tag-app",
		"tags": map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")

	// And: no application was left behind for the rejected tags to strand.
	list := arDo(t, srv, http.MethodGet, "/applications", nil)
	defer list.Body.Close()
	var out struct {
		Applications []map[string]any `json:"applications"`
	}
	helpers.DecodeJSON(t, list, &out)
	for _, app := range out.Applications {
		if app["name"] == "invalid-tag-app" {
			t.Fatalf("an application named invalid-tag-app exists despite the rejected create")
		}
	}
}

func TestCreateAttributeGroup_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := arDo(t, srv, http.MethodPost, "/attribute-groups", map[string]any{
		"name": "invalid-tag-ag",
		"tags": map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestCreateApplication_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := arDo(t, srv, http.MethodPost, "/applications", map[string]any{
		"name": "valid-tag-app",
		"tags": map[string]string{"env": "prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		Application map[string]any `json:"application"`
	}
	helpers.DecodeJSON(t, resp, &created)
	tags, _ := created.Application["tags"].(map[string]any)
	if tags["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", tags)
	}
}
