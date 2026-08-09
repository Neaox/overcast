package cloudwatch_test

// AWS caps a CloudWatch resource at 50 tags and rejects reserved or
// over-long tag keys. Both protocols have to enforce the same rules, and
// both entry points have to: TagResource after the fact, and the Tags
// parameter that PutMetricAlarm applies at creation.

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// manyTags builds n distinct tags.
func manyTags(n int) map[string]string {
	out := make(map[string]string, n)
	for i := range n {
		out["key"+strconv.Itoa(i)] = "v"
	}
	return out
}

// tagJSONList renders a tag map as the JSON protocol's TagList.
func tagJSONList(tags map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(tags))
	for _, k := range sortedKeys(tags) {
		out = append(out, map[string]string{"Key": k, "Value": tags[k]})
	}
	return out
}

// tagQueryValues renders a tag map as the Query protocol's member list.
func tagQueryValues(arn string, tags map[string]string) url.Values {
	params := url.Values{"ResourceARN": {arn}}
	for i, k := range sortedKeys(tags) {
		params.Set("Tags.member."+strconv.Itoa(i+1)+".Key", k)
		params.Set("Tags.member."+strconv.Itoa(i+1)+".Value", tags[k])
	}
	return params
}

// TestTagResource_rejectsInvalidTagSets pins the constraints AWS enforces on
// a tag set, on both protocols. Accepting them locally is the permissive
// divergence: the call works here and is rejected in the caller's account.
func TestTagResource_rejectsInvalidTagSets(t *testing.T) {
	cases := map[string]struct {
		tags map[string]string
		// queryExpressible is false for a tag set the Query protocol cannot
		// encode: its flattened member list ends at the first absent Key, so
		// an empty key is indistinguishable from the end of the list and
		// never reaches a validator on either AWS or here.
		queryExpressible bool
	}{
		"over the 50-tag limit": {manyTags(51), true},
		"reserved aws: prefix":  {map[string]string{"aws:created-by": "me"}, true},
		"empty key":             {map[string]string{"": "value"}, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)
			putAlarmJSONTagged(t, srv, "limits", nil)
			arn := alarmARN(t, srv, "limits")

			jsonResp := cwTargetCall(t, srv, "TagResource", map[string]any{
				"ResourceARN": arn,
				"Tags":        tagJSONList(tc.tags),
			})
			defer jsonResp.Body.Close()
			assertJSONError(t, jsonResp, http.StatusBadRequest, "InvalidParameterValueException")

			if tc.queryExpressible {
				queryResp := cwCall(t, srv, "TagResource", tagQueryValues(arn, tc.tags))
				defer queryResp.Body.Close()
				assertQueryError(t, queryResp, http.StatusBadRequest, "InvalidParameterValue")
			}

			// And: nothing was written — a rejected call leaves the
			// resource exactly as it was.
			if got := listTagsBothProtocols(t, srv, arn); len(got) != 0 {
				t.Fatalf("rejected TagResource still wrote tags: %v", got)
			}
		})
	}
}

// TestPutMetricAlarm_rejectsInvalidTagSets pins the same constraints on the
// other way tags reach a resource — the create call's Tags parameter.
func TestPutMetricAlarm_rejectsInvalidTagSets(t *testing.T) {
	tags := manyTags(51)

	t.Run("json", func(t *testing.T) {
		srv := helpers.NewTestServer(t)
		resp := cwTargetCall(t, srv, "PutMetricAlarm", map[string]any{
			"AlarmName": "too-many-tags", "Namespace": "TestNS", "MetricName": "TestMetric",
			"ComparisonOperator": "GreaterThanThreshold", "EvaluationPeriods": 1,
			"Threshold": 100, "Period": 60, "Statistic": "Average",
			"Tags": tagJSONList(tags),
		})
		defer resp.Body.Close()
		assertJSONError(t, resp, http.StatusBadRequest, "InvalidParameterValueException")
	})

	t.Run("query", func(t *testing.T) {
		srv := helpers.NewTestServer(t)
		params := url.Values{
			"AlarmName":          {"too-many-tags"},
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
		assertQueryError(t, resp, http.StatusBadRequest, "InvalidParameterValue")
	})
}
