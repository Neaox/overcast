// tag_validation_test.go — CreateSecret/TagResource tag validation (#1052).
//
// CreateSecret's inline Tags and TagResource used to store whatever a caller
// sent without checking AWS's own tag constraints — a reserved `aws:` key
// prefix, an over-length key, or more than 50 tags on one secret all had to
// be rejected the way real AWS rejects them, and none of it was.
package secretsmanager_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func smTagMap(t *testing.T, srv *helpers.TestServer, secretID string) map[string]string {
	t.Helper()
	resp := smCall(t, srv, "DescribeSecret", map[string]any{"SecretId": secretID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &out)
	got := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		got[tag.Key] = tag.Value
	}
	return got
}

func TestCreateSecret_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := smCall(t, srv, "CreateSecret", map[string]any{
		"Name":         "invalid-tag-create",
		"SecretString": "x",
		"Tags":         []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")

	// And: no secret was left behind for the rejected tags to strand.
	describe := smCall(t, srv, "DescribeSecret", map[string]any{"SecretId": "invalid-tag-create"})
	defer describe.Body.Close()
	if describe.StatusCode == http.StatusOK {
		t.Fatalf("a secret named invalid-tag-create exists despite the rejected create")
	}
}

func TestTagResource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "invalid-tag-resource", "x")

	resp := smCall(t, srv, "TagResource", map[string]any{
		"SecretId": "invalid-tag-resource",
		"Tags":     []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")

	if got := smTagMap(t, srv, "invalid-tag-resource"); len(got) != 0 {
		t.Fatalf("tags = %#v after a rejected TagResource, want none stored", got)
	}
}

func TestTagResource_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "valid-tag-resource", "x")

	resp := smCall(t, srv, "TagResource", map[string]any{
		"SecretId": "valid-tag-resource",
		"Tags":     []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	if got := smTagMap(t, srv, "valid-tag-resource"); got["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", got)
	}
}

// The 50-tag limit is checked against the existing plus incoming set, not
// just what one TagResource call adds.
func TestTagResource_tagLimitEnforcedOnMergedSet(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "tag-limit", "x")

	seedTags := make([]map[string]string, 0, 50)
	for i := 0; i < 50; i++ {
		seedTags = append(seedTags, map[string]string{"Key": fmt.Sprintf("k%d", i), "Value": "v"})
	}
	seed := smCall(t, srv, "TagResource", map[string]any{"SecretId": "tag-limit", "Tags": seedTags})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	resp := smCall(t, srv, "TagResource", map[string]any{
		"SecretId": "tag-limit",
		"Tags":     []map[string]string{{"Key": "one-too-many", "Value": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

// The CBOR typed-operation path shares the exact same implementation, so a
// rejection there proves the validator is reached regardless of protocol.
func TestTagResource_reservedTagPrefixRejected_cbor(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createSecret(t, srv, "invalid-tag-resource-cbor", "x")

	resp := smCBORCall(t, srv, "TagResource", map[string]any{
		"SecretId": "invalid-tag-resource-cbor",
		"Tags":     []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
