package cloudwatch_test

// CloudWatch's pinned model (cloudwatch-2010-08-01) says alarms, dashboards,
// metric streams and Contributor Insights rules are taggable. Overcast
// emulates alarms only, so the alarm is the whole taggable surface here.
//
// Every case below runs over the Query protocol *and* the JSON protocol.
// One dispatch table being right while the other was not is exactly what hid
// issue #794, so a tagging behaviour proven on one protocol is not proven.

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// sortedKeys returns a map's keys in a stable order, so the Query member
// indices a test builds line up with the JSON list it compares against.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// putAlarmQueryTagged creates or updates an alarm over the Query protocol,
// passing tags as the protocol's flattened member list.
func putAlarmQueryTagged(t *testing.T, srv *helpers.TestServer, name string, tags map[string]string) {
	t.Helper()
	params := url.Values{
		"AlarmName":          {name},
		"Namespace":          {"TestNS"},
		"MetricName":         {"TestMetric"},
		"ComparisonOperator": {"GreaterThanThreshold"},
		"EvaluationPeriods":  {"1"},
		"Threshold":          {"100"},
		"Period":             {"60"},
		"Statistic":          {"Average"},
	}
	for i, k := range sortedKeys(tags) {
		params.Set("Tags.member."+strconv.Itoa(i+1)+".Key", k)
		params.Set("Tags.member."+strconv.Itoa(i+1)+".Value", tags[k])
	}
	resp := cwCall(t, srv, "PutMetricAlarm", params)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// putAlarmJSONTagged creates or updates an alarm over the JSON protocol.
func putAlarmJSONTagged(t *testing.T, srv *helpers.TestServer, name string, tags map[string]string) {
	t.Helper()
	list := make([]map[string]string, 0, len(tags))
	for _, k := range sortedKeys(tags) {
		list = append(list, map[string]string{"Key": k, "Value": tags[k]})
	}
	resp := cwTargetCall(t, srv, "PutMetricAlarm", map[string]any{
		"AlarmName":          name,
		"Namespace":          "TestNS",
		"MetricName":         "TestMetric",
		"ComparisonOperator": "GreaterThanThreshold",
		"EvaluationPeriods":  1,
		"Threshold":          100,
		"Period":             60,
		"Statistic":          "Average",
		"Tags":               list,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// createTagged is the tag-on-create call for each protocol, so a test can run
// the same scenario twice without duplicating it.
var createTagged = map[string]func(*testing.T, *helpers.TestServer, string, map[string]string){
	"query": putAlarmQueryTagged,
	"json":  putAlarmJSONTagged,
}

// listTagsBothProtocols reads a resource's tags over JSON and over Query and
// fails if the two disagree, returning the tags they agreed on.
func listTagsBothProtocols(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()

	jsonResp := cwTargetCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
	defer jsonResp.Body.Close()
	helpers.AssertStatus(t, jsonResp, http.StatusOK)
	tags := decodeCWTags(t, jsonResp)

	queryResp := cwCall(t, srv, "ListTagsForResource", url.Values{"ResourceARN": {arn}})
	defer queryResp.Body.Close()
	helpers.AssertStatus(t, queryResp, http.StatusOK)
	body := helpers.ReadBody(t, queryResp)

	for k, v := range tags {
		if !strings.Contains(body, "<Key>"+k+"</Key><Value>"+v+"</Value>") {
			t.Fatalf("Query ListTagsForResource disagrees with JSON (%v): %s", tags, body)
		}
	}
	if got, want := strings.Count(body, "<member>"), len(tags); got != want {
		t.Fatalf("Query ListTagsForResource returned %d tags, JSON returned %d: %s", got, want, body)
	}
	return tags
}

// TestPutMetricAlarm_tagsAppliedAtCreation covers tag-on-create from both
// directions: the Tags parameter is accepted on the create call over either
// protocol, and the result reads back identically over either protocol.
func TestPutMetricAlarm_tagsAppliedAtCreation(t *testing.T) {
	for _, proto := range []string{"query", "json"} {
		t.Run(proto, func(t *testing.T) {
			// Given: an alarm created with tags over this protocol
			srv := helpers.NewTestServer(t)
			createTagged[proto](t, srv, "created-tagged", map[string]string{"env": "dev", "team": "platform"})

			// Then: both protocols read the same tags back
			tags := listTagsBothProtocols(t, srv, alarmARN(t, srv, "created-tagged"))
			if tags["env"] != "dev" || tags["team"] != "platform" {
				t.Fatalf("tags = %v, want env=dev team=platform", tags)
			}
		})
	}
}

// TestPutMetricAlarm_tagsIgnoredOnUpdate pins the AWS rule the service doc
// states: Tags apply at creation only, and a PutMetricAlarm that updates an
// existing alarm leaves them alone — on both protocols.
func TestPutMetricAlarm_tagsIgnoredOnUpdate(t *testing.T) {
	for _, proto := range []string{"query", "json"} {
		t.Run(proto, func(t *testing.T) {
			// Given: an alarm created with a tag
			srv := helpers.NewTestServer(t)
			createTagged[proto](t, srv, "update-me", map[string]string{"env": "dev"})

			// When: the same call updates it with different tags
			createTagged[proto](t, srv, "update-me", map[string]string{"env": "ignored", "new": "alsoignored"})

			// Then: the creation-time tags stand, and nothing was added
			tags := listTagsBothProtocols(t, srv, alarmARN(t, srv, "update-me"))
			if tags["env"] != "dev" {
				t.Errorf("tags after update = %v, want the create-time env=dev", tags)
			}
			if _, ok := tags["new"]; ok {
				t.Errorf("tags after update = %v, want no tag added by the update", tags)
			}
		})
	}
}

// TestDeleteAlarms_dropsTags proves tags die with the resource over the wire
// on both protocols: an alarm recreated under the same name starts clean.
func TestDeleteAlarms_dropsTags(t *testing.T) {
	for _, proto := range []string{"query", "json"} {
		t.Run(proto, func(t *testing.T) {
			// Given: a tagged alarm
			srv := helpers.NewTestServer(t)
			createTagged[proto](t, srv, "doomed", map[string]string{"env": "dev"})
			arn := alarmARN(t, srv, "doomed")

			// When: it is deleted over this protocol
			if proto == "query" {
				del := cwCall(t, srv, "DeleteAlarms", url.Values{"AlarmNames.member.1": {"doomed"}})
				defer del.Body.Close()
				helpers.AssertStatus(t, del, http.StatusOK)
			} else {
				del := cwTargetCall(t, srv, "DeleteAlarms", map[string]any{"AlarmNames": []string{"doomed"}})
				defer del.Body.Close()
				helpers.AssertStatus(t, del, http.StatusOK)
			}

			// And: an alarm of the same name is recreated, untagged
			putAlarmJSONTagged(t, srv, "doomed", nil)

			// Then: it carries none of the deleted alarm's tags
			if tags := listTagsBothProtocols(t, srv, arn); len(tags) != 0 {
				t.Fatalf("tags survived the delete: %v", tags)
			}
		})
	}
}

// TestDescribeAlarms_omitsTags keeps tags out of a response shape that has no
// Tags member. The model's MetricAlarm carries none, so an SDK that saw one
// would be reading a field AWS never sends.
func TestDescribeAlarms_omitsTags(t *testing.T) {
	srv := helpers.NewTestServer(t)
	putAlarmJSONTagged(t, srv, "described", map[string]string{"secret": "leak"})

	jsonResp := cwTargetCall(t, srv, "DescribeAlarms", map[string]any{"AlarmNames": []string{"described"}})
	defer jsonResp.Body.Close()
	helpers.AssertStatus(t, jsonResp, http.StatusOK)
	if body := helpers.ReadBody(t, jsonResp); strings.Contains(body, "Tags") || strings.Contains(body, "secret") {
		t.Fatalf("DescribeAlarms (JSON) leaked tags: %s", body)
	}

	queryResp := cwCall(t, srv, "DescribeAlarms", url.Values{"AlarmNames.member.1": {"described"}})
	defer queryResp.Body.Close()
	helpers.AssertStatus(t, queryResp, http.StatusOK)
	if body := helpers.ReadBody(t, queryResp); strings.Contains(body, "Tags") || strings.Contains(body, "secret") {
		t.Fatalf("DescribeAlarms (Query) leaked tags: %s", body)
	}
}

// assertQueryError asserts a Query-protocol AWS error code and HTTP status.
func assertQueryError(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	helpers.AssertStatus(t, resp, status)
	if body := helpers.ReadBody(t, resp); !strings.Contains(body, "<Code>"+code+"</Code>") {
		t.Fatalf("want Query error code %s, got: %s", code, body)
	}
}

// assertJSONError asserts a JSON-protocol AWS error __type and HTTP status.
func assertJSONError(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	helpers.AssertStatus(t, resp, status)
	if body := helpers.ReadBody(t, resp); !strings.Contains(body, `"`+code+`"`) {
		t.Fatalf("want JSON error type %s, got: %s", code, body)
	}
}

// TestTagOperations_unknownResource pins the ResourceNotFoundException the
// model declares on all three tagging operations. A 200 instead lets a caller
// tag an alarm that does not exist — it succeeds here and fails on AWS, which
// is the expensive direction of divergence.
func TestTagOperations_unknownResource(t *testing.T) {
	missing := "arn:aws:cloudwatch:us-east-1:000000000000:alarm:no-such-alarm"

	t.Run("json", func(t *testing.T) {
		srv := helpers.NewTestServer(t)
		for _, tc := range []struct {
			op      string
			payload map[string]any
		}{
			{"ListTagsForResource", map[string]any{"ResourceARN": missing}},
			{"TagResource", map[string]any{"ResourceARN": missing, "Tags": []map[string]string{{"Key": "a", "Value": "b"}}}},
			{"UntagResource", map[string]any{"ResourceARN": missing, "TagKeys": []string{"a"}}},
		} {
			t.Run(tc.op, func(t *testing.T) {
				resp := cwTargetCall(t, srv, tc.op, tc.payload)
				defer resp.Body.Close()
				assertJSONError(t, resp, http.StatusNotFound, "ResourceNotFoundException")
			})
		}
	})

	t.Run("query", func(t *testing.T) {
		srv := helpers.NewTestServer(t)
		for _, tc := range []struct {
			op     string
			params url.Values
		}{
			{"ListTagsForResource", url.Values{"ResourceARN": {missing}}},
			{"TagResource", url.Values{"ResourceARN": {missing}, "Tags.member.1.Key": {"a"}, "Tags.member.1.Value": {"b"}}},
			{"UntagResource", url.Values{"ResourceARN": {missing}, "TagKeys.member.1": {"a"}}},
		} {
			t.Run(tc.op, func(t *testing.T) {
				resp := cwCall(t, srv, tc.op, tc.params)
				defer resp.Body.Close()
				assertQueryError(t, resp, http.StatusNotFound, "ResourceNotFoundException")
			})
		}
	})
}

// TestTagOperations_invalidResourceARN pins InvalidParameterValue for a
// ResourceARN that is not a CloudWatch ARN — including the empty string,
// which the model marks required on all three operations.
//
// The two protocols spell the error differently, and that is AWS's own
// doing: the model gives InvalidParameterValueException an awsQueryError
// trait whose code is InvalidParameterValue.
func TestTagOperations_invalidResourceARN(t *testing.T) {
	srv := helpers.NewTestServer(t)

	for _, arn := range []string{"", "not-an-arn", "arn:aws:sqs:us-east-1:000000000000:some-queue"} {
		name := arn
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			jsonResp := cwTargetCall(t, srv, "ListTagsForResource", map[string]any{"ResourceARN": arn})
			defer jsonResp.Body.Close()
			assertJSONError(t, jsonResp, http.StatusBadRequest, "InvalidParameterValueException")

			queryResp := cwCall(t, srv, "ListTagsForResource", url.Values{"ResourceARN": {arn}})
			defer queryResp.Body.Close()
			assertQueryError(t, queryResp, http.StatusBadRequest, "InvalidParameterValue")
		})
	}
}
