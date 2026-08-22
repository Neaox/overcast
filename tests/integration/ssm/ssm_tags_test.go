package ssm_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestPutParameter_tagsAppliedAtCreate: Tags passed to PutParameter (issue
// #1196, Axis B) must be stored and readable via ListTagsForResource
// immediately, without a follow-up AddTagsToResource call. Covers both the
// legacy JSON1.1 path and the CBOR typed path, since PutParameter used to
// have two independent implementations.
func TestPutParameter_tagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ssmCall(t, srv, "PutParameter", map[string]any{
		"Name": "/tag/created", "Value": "v", "Type": "String", "Overwrite": false,
		"Tags": []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	list := ssmCall(t, srv, "ListTagsForResource", map[string]any{
		"ResourceType": "Parameter", "ResourceId": "/tag/created",
	})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		TagList []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"TagList"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.TagList) != 1 || out.TagList[0].Key != "env" || out.TagList[0].Value != "prod" {
		t.Errorf("TagList: got %v, want env=prod", out.TagList)
	}
}

// TestPutParameterCBOR_tagsAppliedAtCreate is TestPutParameter_tagsAppliedAtCreate
// over the CBOR typed path.
func TestPutParameterCBOR_tagsAppliedAtCreate(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ssmCBORCall(t, srv, "PutParameter", map[string]any{
		"Name": "/tag/created-cbor", "Value": "v", "Type": "String", "Overwrite": false,
		"Tags": []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	list := ssmCall(t, srv, "ListTagsForResource", map[string]any{
		"ResourceType": "Parameter", "ResourceId": "/tag/created-cbor",
	})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		TagList []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"TagList"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.TagList) != 1 || out.TagList[0].Key != "env" || out.TagList[0].Value != "prod" {
		t.Errorf("TagList: got %v, want env=prod", out.TagList)
	}
}

// TestPutParameter_invalidTagRejected: an invalid tag passed to PutParameter
// must be rejected before the parameter is created.
func TestPutParameter_invalidTagRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ssmCall(t, srv, "PutParameter", map[string]any{
		"Name": "/tag/rejected", "Value": "v", "Type": "String", "Overwrite": false,
		"Tags": []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)

	get := ssmCall(t, srv, "GetParameter", map[string]any{"Name": "/tag/rejected"})
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusBadRequest)
}

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
