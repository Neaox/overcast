// tag_resource_test.go — CloudWatch Logs TagResource / UntagResource /
// ListTagsForResource (#1195).
//
// These are the modern, ARN-addressed siblings of TagLogGroup / UntagLogGroup
// / ListTagsLogGroup (tested alongside CreateLogGroup elsewhere in this
// package). Both spellings tag the same log group and must agree with each
// other, which is the property most of the tests below check.
package logs_test

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const logsDefaultAccount = "000000000000"

func logGroupResourceArn(name string) string {
	return "arn:aws:logs:us-east-1:" + logsDefaultAccount + ":log-group:" + name
}

func TestTagResource_success(t *testing.T) {
	// Given: a log group with no tags
	srv := helpers.NewTestServer(t)
	groupName := "/aws/tag-resource/one"
	createLogGroup(t, srv, groupName)

	// When: TagResource is called with the group's ARN
	resp := logsCall(t, srv, "TagResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
		"tags":        map[string]any{"env": "prod", "team": "platform"},
	})
	defer resp.Body.Close()

	// Then: it succeeds
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: ListTagsForResource sees them
	list := logsCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
	})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if out.Tags["env"] != "prod" || out.Tags["team"] != "platform" {
		t.Fatalf("tags = %#v, want env=prod team=platform", out.Tags)
	}
}

func TestTagResource_visibleThroughLegacySpelling(t *testing.T) {
	// Given: a log group tagged through the modern TagResource
	srv := helpers.NewTestServer(t)
	groupName := "/aws/tag-resource/legacy-bridge"
	createLogGroup(t, srv, groupName)
	tagResp := logsCall(t, srv, "TagResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
		"tags":        map[string]any{"owner": "team-a"},
	})
	defer tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)

	// When: the legacy ListTagsLogGroup reads it back
	list := logsCall(t, srv, "ListTagsLogGroup", map[string]any{
		"logGroupName": groupName,
	})
	defer list.Body.Close()

	// Then: it sees the same tag — the two spellings share one store, not two
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if out.Tags["owner"] != "team-a" {
		t.Fatalf("tags = %#v, want owner=team-a (from a legacy-spelling read)", out.Tags)
	}
}

func TestTagLogGroup_visibleThroughModernSpelling(t *testing.T) {
	// Given: a log group tagged through the legacy TagLogGroup
	srv := helpers.NewTestServer(t)
	groupName := "/aws/tag-resource/modern-bridge"
	createLogGroup(t, srv, groupName)
	tagResp := logsCall(t, srv, "TagLogGroup", map[string]any{
		"logGroupName": groupName,
		"tags":         map[string]any{"owner": "team-b"},
	})
	defer tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)

	// When: the modern ListTagsForResource reads it back by ARN
	list := logsCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
	})
	defer list.Body.Close()

	// Then: it sees the same tag
	helpers.AssertStatus(t, list, http.StatusOK)
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if out.Tags["owner"] != "team-b" {
		t.Fatalf("tags = %#v, want owner=team-b (from a modern-spelling read)", out.Tags)
	}
}

func TestTagResource_acceptsTrailingWildcardArn(t *testing.T) {
	// Given: a log group exists. AWS's own ARN for a log group carries a
	// trailing ":*" (protocol.LogGroupARN), and the docs say TagResource
	// accepts the ARN with or without it.
	srv := helpers.NewTestServer(t)
	groupName := "/aws/tag-resource/wildcard-arn"
	createLogGroup(t, srv, groupName)

	// When: TagResource is called with the ":*"-suffixed ARN form
	resp := logsCall(t, srv, "TagResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName) + ":*",
		"tags":        map[string]any{"k": "v"},
	})
	defer resp.Body.Close()

	// Then: it resolves to the same group as the bare-name ARN
	helpers.AssertStatus(t, resp, http.StatusOK)
	list := logsCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
	})
	defer list.Body.Close()
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if out.Tags["k"] != "v" {
		t.Fatalf("tags = %#v, want k=v", out.Tags)
	}
}

func TestTagResource_groupNotFound(t *testing.T) {
	// Given: no such log group
	srv := helpers.NewTestServer(t)

	// When: TagResource names it anyway
	resp := logsCall(t, srv, "TagResource", map[string]any{
		"resourceArn": logGroupResourceArn("/no/such/group"),
		"tags":        map[string]any{"k": "v"},
	})
	defer resp.Body.Close()

	// Then: ResourceNotFoundException, the same error TagLogGroup gives for a
	// missing group.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestTagResource_malformedArnNotFound(t *testing.T) {
	// Given: a running server
	srv := helpers.NewTestServer(t)

	// When: resourceArn does not name a log group at all — a destination ARN,
	// which real AWS accepts but this emulator does not implement destinations
	// for, and a plain garbage string alike.
	for _, arn := range []string{
		"arn:aws:logs:us-east-1:" + logsDefaultAccount + ":destination:some-destination",
		"not-an-arn",
		"",
	} {
		t.Run(arn, func(t *testing.T) {
			resp := logsCall(t, srv, "TagResource", map[string]any{
				"resourceArn": arn,
				"tags":        map[string]any{"k": "v"},
			})
			defer resp.Body.Close()
			// An empty resourceArn is a missing required parameter; a
			// wrong-shaped one is not-found. Either way it must not succeed.
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("resourceArn=%q: TagResource succeeded, want an error", arn)
			}
		})
	}
}

func TestTagResource_invalidTagsRejected(t *testing.T) {
	// Given: a log group with an existing tag
	srv := helpers.NewTestServer(t)
	groupName := "/aws/tag-resource/invalid"
	createLogGroup(t, srv, groupName)
	seed := logsCall(t, srv, "TagResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
		"tags":        map[string]any{"keep": "me"},
	})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	// When: TagResource is called with a reserved aws: key prefix
	resp := logsCall(t, srv, "TagResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
		"tags":        map[string]any{"aws:reserved": "x"},
	})
	defer resp.Body.Close()

	// Then: the call is rejected, with the same code CreateLogGroup/TagLogGroup
	// use for the shared validator's rejections.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")

	// And: the existing tag is untouched — a rejected call must not mutate.
	list := logsCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
	})
	defer list.Body.Close()
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.Tags) != 1 || out.Tags["keep"] != "me" {
		t.Fatalf("tags = %#v after a rejected TagResource, want unchanged {keep: me}", out.Tags)
	}
}

func TestUntagResource_success(t *testing.T) {
	// Given: a log group with two tags
	srv := helpers.NewTestServer(t)
	groupName := "/aws/tag-resource/untag"
	createLogGroup(t, srv, groupName)
	seed := logsCall(t, srv, "TagResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
		"tags":        map[string]any{"a": "1", "b": "2"},
	})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	// When: UntagResource removes one of them
	resp := logsCall(t, srv, "UntagResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
		"tagKeys":     []string{"a"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: only the other one remains
	list := logsCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
	})
	defer list.Body.Close()
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, list, &out)
	if len(out.Tags) != 1 || out.Tags["b"] != "2" {
		t.Fatalf("tags = %#v, want only {b: 2}", out.Tags)
	}
}

func TestUntagResource_groupNotFound(t *testing.T) {
	// Given: no such log group
	srv := helpers.NewTestServer(t)

	// When: UntagResource names it anyway
	resp := logsCall(t, srv, "UntagResource", map[string]any{
		"resourceArn": logGroupResourceArn("/no/such/group"),
		"tagKeys":     []string{"a"},
	})
	defer resp.Body.Close()

	// Then: ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestListTagsForResource_emptyForUntaggedGroup(t *testing.T) {
	// Given: a log group with no tags
	srv := helpers.NewTestServer(t)
	groupName := "/aws/tag-resource/empty"
	createLogGroup(t, srv, groupName)

	// When: ListTagsForResource is called
	resp := logsCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": logGroupResourceArn(groupName),
	})
	defer resp.Body.Close()

	// Then: an empty map, not null and not an error
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if len(out.Tags) != 0 {
		t.Fatalf("tags = %#v, want empty", out.Tags)
	}
}

func TestListTagsForResource_groupNotFound(t *testing.T) {
	// Given: no such log group
	srv := helpers.NewTestServer(t)

	// When: ListTagsForResource names it anyway
	resp := logsCall(t, srv, "ListTagsForResource", map[string]any{
		"resourceArn": logGroupResourceArn("/no/such/group"),
	})
	defer resp.Body.Close()

	// Then: ResourceNotFoundException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}
