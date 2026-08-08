package waf_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// wafTagPair mirrors the WAFv2 Tag structure: real WAFv2 sends and returns
// tags as a LIST of {Key,Value} structs, never as a JSON object.
type wafTagPair struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func createTagTestACL(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := wafCall(t, srv, "CreateWebACL", map[string]any{
		"Name":          name,
		"Scope":         "REGIONAL",
		"DefaultAction": map[string]any{"Allow": map[string]any{}},
		"VisibilityConfig": map[string]any{
			"SampledRequestsEnabled":   false,
			"CloudWatchMetricsEnabled": false,
			"MetricName":               name,
		},
		"Rules": []any{},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Summary struct {
			ARN string `json:"ARN"`
		} `json:"Summary"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.Summary.ARN == "" {
		t.Fatal("CreateWebACL returned no ARN")
	}
	return out.Summary.ARN
}

// TestWAFTagResource_sdkListShape sends the exact JSON shape the WAFv2 SDK
// produces — Tags as a list of {Key,Value} structs — and expects TagList back
// in the same list shape.
func TestWAFTagResource_sdkListShape(t *testing.T) {
	// Given: a web ACL
	srv := helpers.NewTestServer(t)
	arn := createTagTestACL(t, srv, "tag-list-acl")

	// When: TagResource is called with the SDK list shape
	resp := wafCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags": []wafTagPair{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "security"},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListTagsForResource returns TagList as a list of Tag structs
	listResp := wafCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)
	var list struct {
		TagInfoForResource struct {
			ResourceARN string       `json:"ResourceARN"`
			TagList     []wafTagPair `json:"TagList"`
		} `json:"TagInfoForResource"`
	}
	helpers.DecodeJSON(t, listResp, &list)
	if list.TagInfoForResource.ResourceARN != arn {
		t.Errorf("ResourceARN: got %q, want %q", list.TagInfoForResource.ResourceARN, arn)
	}
	got := map[string]string{}
	for _, p := range list.TagInfoForResource.TagList {
		got[p.Key] = p.Value
	}
	if got["env"] != "prod" || got["team"] != "security" {
		t.Errorf("TagList: got %v, want env=prod team=security", got)
	}
}

// TestWAFUntagResource_removesKey confirms the UntagResource round trip
// against list-shaped tags.
func TestWAFUntagResource_removesKey(t *testing.T) {
	// Given: a tagged web ACL
	srv := helpers.NewTestServer(t)
	arn := createTagTestACL(t, srv, "untag-acl")
	resp := wafCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags":        []wafTagPair{{Key: "env", Value: "prod"}, {Key: "team", Value: "sec"}},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: one key is removed
	untag := wafCall(t, srv, "UntagResource", map[string]any{
		"ResourceARN": arn,
		"TagKeys":     []string{"env"},
	})
	defer untag.Body.Close()
	helpers.AssertStatus(t, untag, http.StatusOK)

	// Then: only the remaining tag is returned
	listResp := wafCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer listResp.Body.Close()
	var list struct {
		TagInfoForResource struct {
			TagList []wafTagPair `json:"TagList"`
		} `json:"TagInfoForResource"`
	}
	helpers.DecodeJSON(t, listResp, &list)
	if len(list.TagInfoForResource.TagList) != 1 || list.TagInfoForResource.TagList[0].Key != "team" {
		t.Errorf("TagList after untag: got %v, want only team", list.TagInfoForResource.TagList)
	}
}
