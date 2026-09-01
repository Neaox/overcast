// tag_validation_test.go — EFS CreateFileSystem/CreateAccessPoint/
// TagResource tag validation (#1052).
//
// All three used to store whatever a caller sent — merely checking for an
// empty tag key — without checking AWS's own tag constraints: a reserved
// `aws:` key prefix, or more than 50 tags on one resource, had to be
// rejected the way real AWS rejects them, and none of it was.
package efs_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func efsTagMap(t *testing.T, srv *helpers.TestServer, resourceID string) map[string]string {
	t.Helper()
	resp := efsCall(t, http.MethodGet, srv.URL+apiPrefix+"/resource-tags/"+resourceID, nil)
	body := expectJSONStatus(t, resp, http.StatusOK)
	tags, _ := body["Tags"].([]any)
	got := make(map[string]string, len(tags))
	for _, raw := range tags {
		tag, _ := raw.(map[string]any)
		key, _ := tag["Key"].(string)
		value, _ := tag["Value"].(string)
		got[key] = value
	}
	return got
}

func TestCreateFileSystem_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/file-systems", map[string]any{
		"CreationToken": "invalid-tag-create",
		"Tags":          []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	expectErrorCode(t, resp, http.StatusBadRequest, "BadRequest")
}

func TestTagResource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFileSystem(t, srv, "invalid-tag-resource", nil)
	fsID, _ := created["FileSystemId"].(string)

	resp := efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/resource-tags/"+fsID, map[string]any{
		"Tags": []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	expectErrorCode(t, resp, http.StatusBadRequest, "BadRequest")

	if got := efsTagMap(t, srv, fsID); len(got) != 0 {
		t.Fatalf("tags = %#v after a rejected TagResource, want none stored", got)
	}
}

func TestTagResource_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFileSystem(t, srv, "valid-tag-resource", nil)
	fsID, _ := created["FileSystemId"].(string)

	resp := efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/resource-tags/"+fsID, map[string]any{
		"Tags": []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	expectStatus(t, resp, http.StatusOK)

	if got := efsTagMap(t, srv, fsID); got["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", got)
	}
}

// The 50-tag limit is checked against the existing plus incoming set, not
// just what one TagResource call adds.
func TestTagResource_tagLimitEnforcedOnMergedSet(t *testing.T) {
	srv := helpers.NewTestServer(t)
	created := createFileSystem(t, srv, "tag-limit", nil)
	fsID, _ := created["FileSystemId"].(string)

	seedTags := make([]map[string]string, 0, 50)
	for i := 0; i < 50; i++ {
		seedTags = append(seedTags, map[string]string{"Key": fmt.Sprintf("k%d", i), "Value": "v"})
	}
	seed := efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/resource-tags/"+fsID, map[string]any{"Tags": seedTags})
	expectStatus(t, seed, http.StatusOK)

	resp := efsCall(t, http.MethodPost, srv.URL+apiPrefix+"/resource-tags/"+fsID, map[string]any{
		"Tags": []map[string]string{{"Key": "one-too-many", "Value": "x"}},
	})
	expectErrorCode(t, resp, http.StatusBadRequest, "BadRequest")
}
