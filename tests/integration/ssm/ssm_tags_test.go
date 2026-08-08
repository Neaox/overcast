package ssm_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestAddTagsToResource_missingResource: real SSM returns InvalidResourceId —
// not ParameterNotFound — when the tagged resource does not exist.
func TestAddTagsToResource_missingResource(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ssmCall(t, srv, "AddTagsToResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/no/such/param",
		"Tags":         []map[string]string{{"Key": "env", "Value": "test"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidResourceId")
}

// TestRemoveTagsFromResource_missingResource mirrors AddTags.
func TestRemoveTagsFromResource_missingResource(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ssmCall(t, srv, "RemoveTagsFromResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/no/such/param",
		"TagKeys":      []string{"env"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidResourceId")
}

// TestListTagsForResource_missingResource mirrors AddTags.
func TestListTagsForResource_missingResource(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ssmCall(t, srv, "ListTagsForResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/no/such/param",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidResourceId")
}

// TestAddTagsToResource_invalidTagRejected: reserved aws: keys are rejected.
func TestAddTagsToResource_invalidTagRejected(t *testing.T) {
	// Given: a stored parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/tag/validated", "v", "String", false)

	// When: adding a reserved key
	resp := ssmCall(t, srv, "AddTagsToResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/tag/validated",
		"Tags":         []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()

	// Then: the tag is rejected
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// TestAddTagsToResource_tooManyTagsRejected: SSM caps a parameter at 50 tags
// with TooManyTagsError.
func TestAddTagsToResource_tooManyTagsRejected(t *testing.T) {
	// Given: a stored parameter
	srv := helpers.NewTestServer(t)
	putParam(t, srv, "/tag/limit", "v", "String", false)

	tags := make([]map[string]string, 0, 51)
	for i := 0; i < 51; i++ {
		tags = append(tags, map[string]string{"Key": string(rune('a'+i%26)) + string(rune('a'+i/26)), "Value": "v"})
	}

	// When: adding 51 tags
	resp := ssmCall(t, srv, "AddTagsToResource", map[string]any{
		"ResourceType": "Parameter",
		"ResourceId":   "/tag/limit",
		"Tags":         tags,
	})
	defer resp.Body.Close()

	// Then: the request is rejected
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "TooManyTagsError")
}
