package cloudwatch_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// cwTagsPayload is the ListTagsForResource JSON response shape.
type cwTagsPayload struct {
	Tags []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
}

// decodeCWTags reads a JSON-protocol ListTagsForResource response into a
// key→value map.
func decodeCWTags(t *testing.T, resp *http.Response) map[string]string {
	t.Helper()
	var payload cwTagsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode ListTagsForResource: %v", err)
	}
	out := make(map[string]string, len(payload.Tags))
	for _, tag := range payload.Tags {
		out[tag.Key] = tag.Value
	}
	return out
}

// alarmARN reads an alarm's ARN back over the JSON protocol, so the test
// never hard-codes the ARN format the service mints.
func alarmARN(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := cwTargetCall(t, srv, "DescribeAlarms", map[string]any{"AlarmNames": []string{name}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var payload struct {
		MetricAlarms []struct {
			AlarmName string `json:"AlarmName"`
			AlarmArn  string `json:"AlarmArn"`
		} `json:"MetricAlarms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode DescribeAlarms: %v", err)
	}
	for _, a := range payload.MetricAlarms {
		if a.AlarmName == name {
			return a.AlarmArn
		}
	}
	t.Fatalf("DescribeAlarms: alarm %q not found", name)
	return ""
}

// TestListTagsForResource_jsonTarget pins the JSON dispatch path for
// ListTagsForResource. The AWS CLI and the SDKs send CloudWatch calls as
// X-Amz-Target: GraniteServiceVersion20100801.<Op>, so tags applied by
// PutMetricAlarm must be readable over the same protocol that set them —
// the Query form working is not enough (issue #794).
func TestListTagsForResource_jsonTarget(t *testing.T) {
	// Given: an alarm created over the JSON protocol with tags
	srv := helpers.NewTestServer(t)
	put := cwTargetCall(t, srv, "PutMetricAlarm", map[string]any{
		"AlarmName":          "tagged",
		"Namespace":          "TestNS",
		"MetricName":         "TestMetric",
		"ComparisonOperator": "GreaterThanThreshold",
		"EvaluationPeriods":  1,
		"Threshold":          100,
		"Period":             60,
		"Statistic":          "Average",
		"Tags": []map[string]string{
			{"Key": "env", "Value": "dev"},
			{"Key": "team", "Value": "platform"},
		},
	})
	defer put.Body.Close()
	helpers.AssertStatus(t, put, http.StatusOK)

	arn := alarmARN(t, srv, "tagged")

	// When: ListTagsForResource is called over the JSON target protocol
	resp := cwTargetCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer resp.Body.Close()

	// Then: the tags set at creation come back
	helpers.AssertStatus(t, resp, http.StatusOK)
	tags := decodeCWTags(t, resp)
	if tags["env"] != "dev" || tags["team"] != "platform" {
		t.Fatalf("ListTagsForResource: tags = %v, want env=dev team=platform", tags)
	}
}

// TestTagResource_jsonTarget pins the JSON dispatch path for TagResource,
// including the read-back over the same protocol.
func TestTagResource_jsonTarget(t *testing.T) {
	// Given: an untagged alarm
	srv := helpers.NewTestServer(t)
	cr := putAlarm(t, srv, "tag-me")
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	arn := alarmARN(t, srv, "tag-me")

	// When: TagResource is called over the JSON target protocol
	resp := cwTargetCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags": []map[string]string{
			{"Key": "owner", "Value": "team-a"},
			{"Key": "empty", "Value": ""},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the tags are readable back over JSON, empty values included
	list := cwTargetCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	tags := decodeCWTags(t, list)
	if tags["owner"] != "team-a" {
		t.Fatalf("TagResource: tags = %v, want owner=team-a", tags)
	}
	if v, ok := tags["empty"]; !ok || v != "" {
		t.Fatalf("TagResource: tags = %v, want an empty-valued \"empty\" tag", tags)
	}
}

// TestUntagResource_jsonTarget pins the JSON dispatch path for
// UntagResource, and that it removes only the named keys.
func TestUntagResource_jsonTarget(t *testing.T) {
	// Given: an alarm carrying two tags
	srv := helpers.NewTestServer(t)
	cr := putAlarm(t, srv, "untag-me")
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	arn := alarmARN(t, srv, "untag-me")

	tag := cwTargetCall(t, srv, "TagResource", map[string]any{
		"ResourceARN": arn,
		"Tags": []map[string]string{
			{"Key": "keep", "Value": "yes"},
			{"Key": "drop", "Value": "no"},
		},
	})
	defer tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	// When: UntagResource removes one key over the JSON target protocol
	resp := cwTargetCall(t, srv, "UntagResource", map[string]any{
		"ResourceARN": arn,
		"TagKeys":     []string{"drop"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: only the named key is gone
	list := cwTargetCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	tags := decodeCWTags(t, list)
	if _, ok := tags["drop"]; ok {
		t.Fatalf("UntagResource: tags = %v, want \"drop\" removed", tags)
	}
	if tags["keep"] != "yes" {
		t.Fatalf("UntagResource: tags = %v, want keep=yes retained", tags)
	}
}

// TestTagOperations_crossProtocol proves the two protocols share one tag
// store: a tag written over Query is visible over JSON and vice versa.
func TestTagOperations_crossProtocol(t *testing.T) {
	// Given: an alarm tagged over the Query protocol
	srv := helpers.NewTestServer(t)
	cr := putAlarm(t, srv, "cross-protocol")
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	arn := alarmARN(t, srv, "cross-protocol")

	tag := cwCall(t, srv, "TagResource", url.Values{
		"ResourceARN":         {arn},
		"Tags.member.1.Key":   {"via"},
		"Tags.member.1.Value": {"query"},
		"Tags.member.2.Key":   {"gone"},
		"Tags.member.2.Value": {"soon"},
	})
	defer tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	// When: the tag is read and then removed over the JSON protocol
	list := cwTargetCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	if tags := decodeCWTags(t, list); tags["via"] != "query" {
		t.Fatalf("cross-protocol read: tags = %v, want via=query", tags)
	}

	untag := cwTargetCall(t, srv, "UntagResource", map[string]any{
		"ResourceARN": arn,
		"TagKeys":     []string{"gone"},
	})
	defer untag.Body.Close()
	helpers.AssertStatus(t, untag, http.StatusOK)

	// Then: the Query protocol sees the removal too
	queryList := cwCall(t, srv, "ListTagsForResource", url.Values{"ResourceARN": {arn}})
	defer queryList.Body.Close()
	helpers.AssertStatus(t, queryList, http.StatusOK)
	body := helpers.ReadBody(t, queryList)
	if !strings.Contains(body, "<Key>via</Key>") {
		t.Fatalf("Query ListTagsForResource: want the via tag, got: %s", body)
	}
	if strings.Contains(body, "<Key>gone</Key>") {
		t.Fatalf("Query ListTagsForResource: want the gone tag removed, got: %s", body)
	}
}
