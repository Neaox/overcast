// tag_validation_test.go — KMS CreateKey/TagResource tag validation (#1052).
//
// CreateKey's inline Tags and TagResource used to store whatever a caller
// sent without checking AWS's own tag constraints — a reserved `aws:` key
// prefix, an over-length key, or more than 50 tags on one key all had to be
// rejected the way real AWS rejects them, and none of it was, on either the
// JSON1.1 legacy path or the CBOR typed-operation path.
package kms_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func kmsTagMap(t *testing.T, srv *helpers.TestServer, keyID string) map[string]string {
	t.Helper()
	resp := kmsCall(t, srv, "ListResourceTags", map[string]any{"KeyId": keyID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags []struct {
			TagKey   string `json:"TagKey"`
			TagValue string `json:"TagValue"`
		} `json:"Tags"`
	}
	decodeJSON(t, resp, &out)
	got := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		got[tag.TagKey] = tag.TagValue
	}
	return got
}

func TestCreateKey_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := kmsCall(t, srv, "CreateKey", map[string]any{
		"Description": "invalid-tag-create",
		"Tags":        []map[string]string{{"TagKey": "aws:reserved", "TagValue": "x"}},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}

func TestTagResource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "invalid-tag-resource")

	resp := kmsCall(t, srv, "TagResource", map[string]any{
		"KeyId": keyID,
		"Tags":  []map[string]string{{"TagKey": "aws:reserved", "TagValue": "x"}},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")

	if got := kmsTagMap(t, srv, keyID); len(got) != 0 {
		t.Fatalf("tags = %#v after a rejected TagResource, want none stored", got)
	}
}

// The CBOR typed-operation path must reject exactly the same way — CreateKey
// and TagResource are implemented twice in this package (JSON1.1 legacy and
// CBOR typed), and the legacy copy is where the JSON test above already runs.
func TestTagResource_reservedTagPrefixRejected_cbor(t *testing.T) {
	srv := helpers.NewTestServer(t)
	keyID := createKeyCBOR(t, srv, "invalid-tag-resource-cbor")

	resp := kmsCBORCall(t, srv, "TagResource", map[string]any{
		"KeyId": keyID,
		"Tags":  []map[string]string{{"TagKey": "aws:reserved", "TagValue": "x"}},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTagResource_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "valid-tag-resource")

	resp := kmsCall(t, srv, "TagResource", map[string]any{
		"KeyId": keyID,
		"Tags":  []map[string]string{{"TagKey": "env", "TagValue": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	if got := kmsTagMap(t, srv, keyID); got["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", got)
	}
}

// The 50-tag limit is checked against the existing plus incoming set, not
// just what one TagResource call adds.
func TestTagResource_tagLimitEnforcedOnMergedSet(t *testing.T) {
	srv := helpers.NewTestServer(t)
	keyID := createKey(t, srv, "tag-limit")

	seedTags := make([]map[string]string, 0, 50)
	for i := 0; i < 50; i++ {
		seedTags = append(seedTags, map[string]string{"TagKey": fmt.Sprintf("k%d", i), "TagValue": "v"})
	}
	seed := kmsCall(t, srv, "TagResource", map[string]any{"KeyId": keyID, "Tags": seedTags})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	resp := kmsCall(t, srv, "TagResource", map[string]any{
		"KeyId": keyID,
		"Tags":  []map[string]string{{"TagKey": "one-too-many", "TagValue": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ValidationException")
}
