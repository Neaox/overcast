// Package cloudwatch_test — alarm configuration, refusal and history tests.
//
// The evaluator's own arithmetic is unit-tested against a mock clock in
// internal/services/cloudwatch; these tests cover the wire contract: which
// alarm configurations are accepted, which are refused and how, and what
// DescribeAlarms/DescribeAlarmHistory report back.
package cloudwatch_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── §2.1 refusals ────────────────────────────────────────────────────────────

// TestPutMetricAlarm_refusesConfigurationsItCannotEvaluate pins
// docs/plans/full-emulation-priority.md §2.1 for alarms: an alarm shape the
// evaluator cannot decide is refused with a 501 rather than created and left
// sitting in INSUFFICIENT_DATA looking armed.
func TestPutMetricAlarm_createsConfigurationsItCannotEvaluate(t *testing.T) {
	cases := []struct {
		name   string
		params url.Values
	}{
		{
			name: "metric math",
			params: url.Values{
				"AlarmName":                   {"math-alarm"},
				"Metrics.member.1.Id":         {"e1"},
				"Metrics.member.1.Expression": {"m1/m2"},
				"ComparisonOperator":          {"GreaterThanThreshold"},
				"EvaluationPeriods":           {"1"},
				"Threshold":                   {"1"},
			},
		},
		{
			name: "anomaly detection",
			params: url.Values{
				"AlarmName":          {"anomaly-alarm"},
				"Namespace":          {"TestNS"},
				"MetricName":         {"TestMetric"},
				"ThresholdMetricId":  {"ad1"},
				"Statistic":          {"Average"},
				"Period":             {"60"},
				"ComparisonOperator": {"LessThanLowerOrGreaterThanUpperThreshold"},
				"EvaluationPeriods":  {"1"},
			},
		},
		{
			name: "extended statistic",
			params: url.Values{
				"AlarmName":          {"p99-alarm"},
				"Namespace":          {"TestNS"},
				"MetricName":         {"TestMetric"},
				"ExtendedStatistic":  {"p99"},
				"ComparisonOperator": {"GreaterThanThreshold"},
				"EvaluationPeriods":  {"1"},
				"Threshold":          {"1"},
				"Period":             {"60"},
			},
		},
		{
			name: "anomaly comparison operator",
			params: url.Values{
				"AlarmName":          {"band-alarm"},
				"Namespace":          {"TestNS"},
				"MetricName":         {"TestMetric"},
				"ComparisonOperator": {"GreaterThanUpperThreshold"},
				"Statistic":          {"Average"},
				"Threshold":          {"1"},
				"EvaluationPeriods":  {"1"},
				"Period":             {"60"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an empty store
			srv := helpers.NewTestServer(t)

			// When: an alarm of an unevaluable shape is created
			resp := cwCall(t, srv, "PutMetricAlarm", tc.params)
			defer resp.Body.Close()

			// Then: it is created. These were refused with a 501 until a CDK
			// monitoring stack could not deploy for it: the refusal failed the
			// CloudFormation resource, and with it the stack, over alarms whose
			// only defect is that nothing here computes them.
			helpers.AssertStatus(t, resp, http.StatusOK)

			// And: the response says what will not be done with it, which is
			// what the refusal was protecting and is now carried instead of it.
			limitation := protocol.LimitationText(resp.Header)
			if !strings.Contains(limitation, "does not evaluate") {
				t.Errorf("%s = %q, want it to say the state is never computed",
					protocol.EmulationLimitationHeader, limitation)
			}

			// And: the alarm exists, saying the same thing where its state is read,
			// so nothing that looks at it mistakes it for armed.
			list := cwCall(t, srv, "DescribeAlarms", nil)
			defer list.Body.Close()
			body := helpers.ReadBody(t, list)
			if !strings.Contains(body, tc.params.Get("AlarmName")) {
				t.Fatalf("alarm %q was not created: %s", tc.params.Get("AlarmName"), body)
			}
			if !strings.Contains(body, "does not evaluate") {
				t.Errorf("alarm %q does not say its state is never computed: %s", tc.params.Get("AlarmName"), body)
			}
		})
	}
}

// TestPutMetricAlarm_rejectsInvalidEnums pins that input real CloudWatch
// itself rejects gets AWS's 400 ValidationError, not a 501 — the two claims
// are different and a caller acts on them differently.
func TestPutMetricAlarm_rejectsInvalidEnums(t *testing.T) {
	cases := []struct {
		name   string
		params url.Values
	}{
		{"unknown statistic", url.Values{"Statistic": {"Median"}}},
		{"unknown comparison operator", url.Values{"ComparisonOperator": {"AboutEqualTo"}}},
		{"unknown treat-missing-data mode", url.Values{"TreatMissingData": {"whatever"}}},
		{"period that is not a valid resolution", url.Values{"Period": {"45"}}},
		{"more datapoints than evaluation periods", url.Values{"DatapointsToAlarm": {"5"}, "EvaluationPeriods": {"2"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: an empty store
			srv := helpers.NewTestServer(t)

			// When: the invalid alarm is created
			params := url.Values{
				"AlarmName":          {"invalid"},
				"Namespace":          {"TestNS"},
				"MetricName":         {"TestMetric"},
				"ComparisonOperator": {"GreaterThanThreshold"},
				"EvaluationPeriods":  {"1"},
				"Period":             {"60"},
				"Threshold":          {"1"},
			}
			for k, v := range tc.params {
				params[k] = v
			}
			resp := cwCall(t, srv, "PutMetricAlarm", params)
			defer resp.Body.Close()

			// Then: AWS's 400 ValidationError comes back, in the Query
			// protocol's ErrorResponse envelope
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			body := helpers.ReadBody(t, resp)
			if !strings.Contains(body, "ValidationError") {
				t.Errorf("expected ValidationError, got: %s", body)
			}
			if !strings.Contains(body, "<ErrorResponse") {
				t.Errorf("expected the Query-protocol ErrorResponse envelope, got: %s", body)
			}
		})
	}
}

// ─── Round-tripping the full alarm shape ─────────────────────────────────────

// TestPutMetricAlarm_roundTripsFullConfiguration pins that the parameters the
// evaluator needs actually survive PutMetricAlarm — dimensions, actions and
// DatapointsToAlarm were all silently dropped before alarms were evaluated.
func TestPutMetricAlarm_roundTripsFullConfiguration(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: an alarm is created with the full single-metric parameter set
	resp := cwCall(t, srv, "PutMetricAlarm", url.Values{
		"AlarmName":                        {"full"},
		"Namespace":                        {"TestNS"},
		"MetricName":                       {"Latency"},
		"Statistic":                        {"Maximum"},
		"Dimensions.member.1.Name":         {"Service"},
		"Dimensions.member.1.Value":        {"api"},
		"Period":                           {"60"},
		"EvaluationPeriods":                {"3"},
		"DatapointsToAlarm":                {"2"},
		"Threshold":                        {"100"},
		"ComparisonOperator":               {"GreaterThanOrEqualToThreshold"},
		"TreatMissingData":                 {"notBreaching"},
		"Unit":                             {"Milliseconds"},
		"AlarmActions.member.1":            {"arn:aws:sns:us-east-1:000000000000:alerts"},
		"OKActions.member.1":               {"arn:aws:sns:us-east-1:000000000000:recovered"},
		"InsufficientDataActions.member.1": {"arn:aws:sns:us-east-1:000000000000:unknown"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: DescribeAlarms reports every one of them back
	list := cwCall(t, srv, "DescribeAlarms", nil)
	defer list.Body.Close()
	body := helpers.ReadBody(t, list)

	for _, want := range []string{
		"<DatapointsToAlarm>2</DatapointsToAlarm>",
		"<Name>Service</Name><Value>api</Value>",
		"<Unit>Milliseconds</Unit>",
		"<TreatMissingData>notBreaching</TreatMissingData>",
		"arn:aws:sns:us-east-1:000000000000:alerts",
		"arn:aws:sns:us-east-1:000000000000:recovered",
		"arn:aws:sns:us-east-1:000000000000:unknown",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("DescribeAlarms missing %q; body = %s", want, body)
		}
	}
}

// TestPutMetricAlarm_JSON_refusesMetricMath pins that the JSON protocol
// applies the same §2.1 refusals as the Query protocol — the two must not
// drift.
func TestPutMetricAlarm_JSON_createsMetricMath(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a metric-math alarm is created over the JSON protocol
	resp := cwTargetCall(t, srv, "PutMetricAlarm", map[string]any{
		"AlarmName":          "math-alarm",
		"ComparisonOperator": "GreaterThanThreshold",
		"EvaluationPeriods":  1,
		"Threshold":          1,
		"Metrics":            []any{map[string]any{"Id": "e1", "Expression": "m1/m2"}},
	})
	defer resp.Body.Close()

	// Then: the same answer as the Query protocol gives — created, and saying
	// what will not be done with it. The two protocols parse into one input
	// precisely so this cannot drift between them.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := protocol.LimitationText(resp.Header); !strings.Contains(got, "metric-math") {
		t.Errorf("%s = %q, want it to name metric-math", protocol.EmulationLimitationHeader, got)
	}
}

// ─── SetAlarmState and history ───────────────────────────────────────────────

// TestSetAlarmState_recordsHistoryAndSurvivesEvaluation pins the DoD's manual
// override requirement, and that a transition lands in DescribeAlarmHistory.
func TestSetAlarmState_recordsHistoryAndSurvivesEvaluation(t *testing.T) {
	// Given: an alarm
	srv := helpers.NewTestServer(t)
	create := putAlarm(t, srv, "manual-alarm")
	create.Body.Close()

	// When: its state is forced to ALARM
	set := cwCall(t, srv, "SetAlarmState", url.Values{
		"AlarmName":   {"manual-alarm"},
		"StateValue":  {"ALARM"},
		"StateReason": {"testing the downstream wiring"},
	})
	defer set.Body.Close()
	helpers.AssertStatus(t, set, http.StatusOK)

	// Then: DescribeAlarms reports the forced state and reason
	list := cwCall(t, srv, "DescribeAlarms", url.Values{"AlarmNames.member.1": {"manual-alarm"}})
	defer list.Body.Close()
	body := helpers.ReadBody(t, list)
	if !strings.Contains(body, "<StateValue>ALARM</StateValue>") {
		t.Fatalf("expected the forced ALARM state; body = %s", body)
	}
	if !strings.Contains(body, "testing the downstream wiring") {
		t.Errorf("expected the supplied StateReason; body = %s", body)
	}

	// And: the transition is in the alarm's history
	history := cwCall(t, srv, "DescribeAlarmHistory", url.Values{"AlarmName": {"manual-alarm"}})
	defer history.Body.Close()
	helpers.AssertStatus(t, history, http.StatusOK)
	historyBody := helpers.ReadBody(t, history)
	if !strings.Contains(historyBody, "<HistoryItemType>StateUpdate</HistoryItemType>") {
		t.Errorf("expected a StateUpdate history item; body = %s", historyBody)
	}
	if !strings.Contains(historyBody, "INSUFFICIENT_DATA to ALARM") {
		t.Errorf("expected the history summary to name both states; body = %s", historyBody)
	}
}

// TestSetAlarmState_rejectsUnknownState pins AWS's validation of StateValue.
func TestSetAlarmState_rejectsUnknownState(t *testing.T) {
	// Given: an alarm
	srv := helpers.NewTestServer(t)
	create := putAlarm(t, srv, "state-validation")
	create.Body.Close()

	// When: an invalid state is requested
	resp := cwCall(t, srv, "SetAlarmState", url.Values{
		"AlarmName":  {"state-validation"},
		"StateValue": {"BROKEN"},
	})
	defer resp.Body.Close()

	// Then: AWS's ValidationError comes back
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	if body := helpers.ReadBody(t, resp); !strings.Contains(body, "ValidationError") {
		t.Errorf("expected ValidationError; body = %s", body)
	}
}

// TestDescribeAlarmHistory_JSON pins the JSON protocol's history shape, which
// is what the web UI's alarms view reads.
func TestDescribeAlarmHistory_JSON(t *testing.T) {
	// Given: an alarm that has transitioned once
	srv := helpers.NewTestServer(t)
	create := putAlarm(t, srv, "json-history")
	create.Body.Close()
	set := cwCall(t, srv, "SetAlarmState", url.Values{
		"AlarmName":  {"json-history"},
		"StateValue": {"OK"},
	})
	set.Body.Close()

	// When: history is read over the JSON protocol
	resp := cwTargetCall(t, srv, "DescribeAlarmHistory", map[string]any{"AlarmName": "json-history"})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the AlarmHistoryItems carry AWS's field names
	var out struct {
		AlarmHistoryItems []struct {
			AlarmName       string  `json:"AlarmName"`
			AlarmType       string  `json:"AlarmType"`
			Timestamp       float64 `json:"Timestamp"`
			HistoryItemType string  `json:"HistoryItemType"`
			HistorySummary  string  `json:"HistorySummary"`
		} `json:"AlarmHistoryItems"`
	}
	if err := json.Unmarshal([]byte(helpers.ReadBody(t, resp)), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, item := range out.AlarmHistoryItems {
		if item.HistoryItemType == "StateUpdate" && item.AlarmName == "json-history" {
			found = true
			if item.AlarmType != "MetricAlarm" || item.Timestamp == 0 {
				t.Errorf("history item is missing AlarmType/Timestamp: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("no StateUpdate item returned; got %+v", out.AlarmHistoryItems)
	}
}

// TestDeleteAlarms_removesHistory pins that a deleted alarm leaves nothing
// behind for a later alarm of the same name to inherit.
func TestDeleteAlarms_removesHistory(t *testing.T) {
	// Given: an alarm with history
	srv := helpers.NewTestServer(t)
	create := putAlarm(t, srv, "recycled")
	create.Body.Close()
	set := cwCall(t, srv, "SetAlarmState", url.Values{"AlarmName": {"recycled"}, "StateValue": {"ALARM"}})
	set.Body.Close()

	// When: the alarm is deleted
	del := cwCall(t, srv, "DeleteAlarms", url.Values{"AlarmNames.member.1": {"recycled"}})
	del.Body.Close()

	// Then: its history is gone too
	history := cwCall(t, srv, "DescribeAlarmHistory", url.Values{"AlarmName": {"recycled"}})
	defer history.Body.Close()
	if body := helpers.ReadBody(t, history); strings.Contains(body, "StateUpdate") {
		t.Errorf("deleted alarm left history behind: %s", body)
	}
}
