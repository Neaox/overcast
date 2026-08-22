// tag_validation_test.go — Kinesis CreateStream/AddTagsToStream/TagResource
// tag validation (#1052).
//
// All three used to store whatever a caller sent without checking AWS's own
// tag constraints — a reserved `aws:` key prefix or more than 50 tags on one
// stream had to be rejected the way real AWS rejects them, and none of it
// was, on either the JSON1.1 legacy path or the CBOR typed-operation path.
package kinesis_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func kinesisTagMap(t *testing.T, srv *helpers.TestServer, streamName string) map[string]string {
	t.Helper()
	resp := kinesisCall(t, srv, "ListTagsForStream", map[string]any{"StreamName": streamName})
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	decodeJSON(t, resp, &out)
	got := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		got[tag.Key] = tag.Value
	}
	return got
}

func TestCreateStream_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := kinesisCall(t, srv, "CreateStream", map[string]any{
		"StreamName": "invalid-tag-create",
		"ShardCount": 1,
		"Tags":       map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidArgumentException")

	// And: no stream was left behind for the rejected tags to strand.
	describe := kinesisCall(t, srv, "DescribeStream", map[string]any{"StreamName": "invalid-tag-create"})
	defer describe.Body.Close()
	if describe.StatusCode == http.StatusOK {
		t.Fatalf("a stream named invalid-tag-create exists despite the rejected create")
	}
}

func TestAddTagsToStream_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := kinesisCall(t, srv, "CreateStream", map[string]any{"StreamName": "invalid-tag-add", "ShardCount": 1})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	resp := kinesisCall(t, srv, "AddTagsToStream", map[string]any{
		"StreamName": "invalid-tag-add",
		"Tags":       map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidArgumentException")

	if got := kinesisTagMap(t, srv, "invalid-tag-add"); len(got) != 0 {
		t.Fatalf("tags = %#v after a rejected AddTagsToStream, want none stored", got)
	}
}

func TestTagResource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := kinesisCall(t, srv, "CreateStream", map[string]any{"StreamName": "invalid-tag-resource", "ShardCount": 1})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	arn := "arn:aws:kinesis:us-east-1:000000000000:stream/invalid-tag-resource"

	resp := kinesisCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags":        map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidArgumentException")
}

func TestAddTagsToStream_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := kinesisCall(t, srv, "CreateStream", map[string]any{"StreamName": "valid-tag-add", "ShardCount": 1})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	resp := kinesisCall(t, srv, "AddTagsToStream", map[string]any{
		"StreamName": "valid-tag-add",
		"Tags":       map[string]string{"env": "prod"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	if got := kinesisTagMap(t, srv, "valid-tag-add"); got["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", got)
	}
}

// The 50-tag limit is checked against the existing plus incoming set, not
// just what one AddTagsToStream call adds.
func TestAddTagsToStream_tagLimitEnforcedOnMergedSet(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := kinesisCall(t, srv, "CreateStream", map[string]any{"StreamName": "tag-limit", "ShardCount": 1})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)

	seedTags := make(map[string]string, 50)
	for i := 0; i < 50; i++ {
		seedTags[fmt.Sprintf("k%d", i)] = "v"
	}
	seed := kinesisCall(t, srv, "AddTagsToStream", map[string]any{"StreamName": "tag-limit", "Tags": seedTags})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	resp := kinesisCall(t, srv, "AddTagsToStream", map[string]any{
		"StreamName": "tag-limit",
		"Tags":       map[string]string{"one-too-many": "x"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidArgumentException")
}

// The CBOR typed-operation path shares the exact same implementation as
// AddTagsToStream's legacy JSON handler now, so a rejection there proves the
// validator is reached regardless of protocol.
func TestTagResource_reservedTagPrefixRejected_cbor(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := kinesisCall(t, srv, "CreateStream", map[string]any{"StreamName": "invalid-tag-resource-cbor", "ShardCount": 1})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	arn := "arn:aws:kinesis:us-east-1:000000000000:stream/invalid-tag-resource-cbor"

	resp := kinesisCBORCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags":        map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
