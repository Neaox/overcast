package cloudwatch

// alarm_optional_test.go — PutMetricAlarm's optional parameters.
//
// "Optional" on the wire is not the same as "has a sensible default". Three of
// these parameters AWS genuinely defaults (ActionsEnabled, DatapointsToAlarm,
// TreatMissingData) and Overcast must default them identically. Five more are
// optional only because a PromQL alarm carries them inside EvaluationCriteria
// instead; for an alarm on a metric AWS rejects the request without them, and
// substituting a value would arm an alarm nobody configured.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/protocol/codec"
)

// putAlarmForm issues a Query-protocol PutMetricAlarm and returns the response.
func putAlarmForm(t *testing.T, svc *Service, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	params.Set("Action", "PutMetricAlarm")
	params.Set("Version", "2010-08-01")
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	svc.DispatchQuery(w, r)
	return w
}

// completeAlarmParams is a PutMetricAlarm request with every parameter AWS
// requires for a metric alarm, and none of the optional ones.
func completeAlarmParams(name string) url.Values {
	return url.Values{
		"AlarmName":          {name},
		"Namespace":          {"TestNS"},
		"MetricName":         {"Latency"},
		"Statistic":          {"Average"},
		"Period":             {"60"},
		"EvaluationPeriods":  {"2"},
		"Threshold":          {"100"},
		"ComparisonOperator": {"GreaterThanThreshold"},
	}
}

func TestPutMetricAlarm_MissingRequiredForAMetricAlarm_IsRejected(t *testing.T) {
	// Each of these is "Required: No" on PutMetricAlarm, but only because an
	// EvaluationCriteria alarm supplies it elsewhere. Dropping one from a
	// metric alarm has to fail rather than be filled in.
	for _, tc := range []struct {
		omit string
		want string
	}{
		{"Statistic", "Statistic or the parameter ExtendedStatistic"},
		{"ComparisonOperator", "comparisonOperator"},
		{"Period", "The parameter Period must be specified"},
		{"EvaluationPeriods", "evaluationPeriods"},
		{"Threshold", "The parameter Threshold must be specified"},
	} {
		t.Run(tc.omit, func(t *testing.T) {
			svc := newTestService(t)
			params := completeAlarmParams("omit-" + tc.omit)
			params.Del(tc.omit)

			w := putAlarmForm(t, svc, params)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("error does not name %s; body = %s", tc.omit, w.Body.String())
			}
			if _, found := svc.store.getAlarm(context.Background(), "omit-"+tc.omit); found {
				t.Error("a rejected alarm was persisted anyway")
			}
		})
	}
}

func TestPutMetricAlarm_ZeroThreshold_IsAValueNotAnOmission(t *testing.T) {
	// "greater than zero errors" is the commonest alarm there is, so 0 has to
	// survive the has-it-been-set check that rejects an absent Threshold.
	svc := newTestService(t)
	params := completeAlarmParams("zero-threshold")
	params.Set("Threshold", "0")

	w := putAlarmForm(t, svc, params)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	alarm := readAlarm(t, svc, "zero-threshold")
	if alarm.Threshold != 0 {
		t.Errorf("Threshold = %v, want 0", alarm.Threshold)
	}
}

func TestPutMetricAlarm_OmittedOptionals_TakeAWSDefaults(t *testing.T) {
	// The three parameters AWS does document a default for.
	svc := newTestService(t)

	w := putAlarmForm(t, svc, completeAlarmParams("defaults"))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	alarm := readAlarm(t, svc, "defaults")
	if !alarm.ActionsEnabled {
		t.Error("ActionsEnabled defaulted to false, want true")
	}
	// DatapointsToAlarm and TreatMissingData are stored unset and resolved at
	// evaluation time — to EvaluationPeriods and "missing" respectively.
	if alarm.DatapointsToAlarm != 0 {
		t.Errorf("DatapointsToAlarm stored as %d, want unset", alarm.DatapointsToAlarm)
	}
	if got := alarm.effectiveDatapointsToAlarm(); got != 2 {
		t.Errorf("effective DatapointsToAlarm = %d, want EvaluationPeriods (2)", got)
	}
	if alarm.TreatMissingData != "" {
		t.Errorf("TreatMissingData stored as %q, want unset", alarm.TreatMissingData)
	}
}

func TestPutMetricAlarm_ActionsEnabledFalse_IsHonoured(t *testing.T) {
	// The default is true, so "false" has to be distinguishable from absent.
	svc := newTestService(t)
	params := completeAlarmParams("actions-off")
	params.Set("ActionsEnabled", "false")

	if w := putAlarmForm(t, svc, params); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if readAlarm(t, svc, "actions-off").ActionsEnabled {
		t.Error("ActionsEnabled = true, want false")
	}
}

func TestPutMetricAlarm_Tags_AppliedOnCreateIgnoredOnUpdate(t *testing.T) {
	// AWS applies the Tags parameter when the alarm is created and ignores it
	// when the same call updates an existing alarm, pointing callers at
	// TagResource instead.
	svc := newTestService(t)
	params := completeAlarmParams("tagged")
	params.Set("Tags.member.1.Key", "Team")
	params.Set("Tags.member.1.Value", "platform")

	if w := putAlarmForm(t, svc, params); w.Code != http.StatusOK {
		t.Fatalf("create: status = %d; body = %s", w.Code, w.Body.String())
	}
	arn := "arn:aws:cloudwatch:us-east-1:000000000000:alarm:tagged"
	tags, aerr := svc.resourceTags(context.Background(), arn)
	if aerr != nil {
		t.Fatalf("resourceTags: %v", aerr)
	}
	if tags["Team"] != "platform" {
		t.Fatalf("tags after create = %v, want Team=platform", tags)
	}

	// When: the alarm is updated with a different tag value
	params.Set("Tags.member.1.Value", "ignored")
	if w := putAlarmForm(t, svc, params); w.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body = %s", w.Code, w.Body.String())
	}

	// Then: the original tag stands
	tags, aerr = svc.resourceTags(context.Background(), arn)
	if aerr != nil {
		t.Fatalf("resourceTags: %v", aerr)
	}
	if tags["Team"] != "platform" {
		t.Errorf("tags after update = %v, want the create-time Team=platform", tags)
	}
}

func TestDeleteAlarms_DropsTags(t *testing.T) {
	// A recreated alarm has to start clean, tags included.
	svc := newTestService(t)
	params := completeAlarmParams("tag-cleanup")
	params.Set("Tags.member.1.Key", "Team")
	params.Set("Tags.member.1.Value", "platform")
	if w := putAlarmForm(t, svc, params); w.Code != http.StatusOK {
		t.Fatalf("create: status = %d; body = %s", w.Code, w.Body.String())
	}

	svc.removeAlarm(context.Background(), "tag-cleanup")

	tags, aerr := svc.resourceTags(context.Background(), "arn:aws:cloudwatch:us-east-1:000000000000:alarm:tag-cleanup")
	if aerr != nil {
		t.Fatalf("resourceTags: %v", aerr)
	}
	if len(tags) != 0 {
		t.Errorf("tags survived the delete: %v", tags)
	}
}

func TestPutMetricAlarm_EvaluationCriteria_IsRefused(t *testing.T) {
	// A PromQL alarm has no evaluator behind it, so it is refused rather than
	// created inert — and the refusal has to be the same on both protocols.
	t.Run("json", func(t *testing.T) {
		svc := newTestService(t)
		body := `{"AlarmName":"promql","EvaluationCriteria":{"PromQLCriteria":{"Query":"up > 0","PendingPeriod":300}},"EvaluationInterval":30}`
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		r = r.WithContext(codec.WithDispatch(r.Context(), codec.JSON10, "PutMetricAlarm"))
		w := httptest.NewRecorder()
		svc.Dispatch(w, r)

		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501; body = %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "PromQL") {
			t.Errorf("refusal does not name PromQL; body = %s", w.Body.String())
		}
	})

	t.Run("query", func(t *testing.T) {
		svc := newTestService(t)
		params := completeAlarmParams("promql-query")
		params.Set("EvaluationCriteria.PromQLCriteria.Query", "up > 0")

		w := putAlarmForm(t, svc, params)

		if w.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501; body = %s", w.Code, w.Body.String())
		}
	})
}

func TestEvaluateAlarms_UnitSelectsWhichDatapointsCount(t *testing.T) {
	// Given: an alarm on Latency in Milliseconds with a threshold of 100
	svc, mock := newAlarmTestService(t)
	alarm := baseAlarm("unit-scoped")
	alarm.Unit = "Milliseconds"
	storeAlarm(t, svc, alarm)

	// And: a breaching datapoint published in Seconds — a different unit, so a
	// different series as far as AWS is concerned — inside the window
	now := mock.Now().UTC()
	publishPointWithUnit(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 500, "Seconds")

	// When: the alarm is evaluated over a closed period
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	// Then: it has no data of its own unit to judge, rather than alarming on
	// another unit's numbers
	if got := readAlarm(t, svc, "unit-scoped").StateValue; got != stateInsufficientData {
		t.Errorf("StateValue = %q, want %q — a Seconds datapoint fed a Milliseconds alarm", got, stateInsufficientData)
	}
}

func TestEvaluateAlarms_UnitlessDatapointFeedsAUnitScopedAlarm(t *testing.T) {
	// Deliberate divergence from AWS (which files an unqualified datapoint
	// under "None" and would leave this alarm in INSUFFICIENT_DATA): locally
	// published metrics routinely omit the unit while the CDK construct that
	// created the alarm supplied one.
	svc, mock := newAlarmTestService(t)
	alarm := baseAlarm("unitless-feed")
	alarm.Unit = "Milliseconds"
	storeAlarm(t, svc, alarm)

	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 500)

	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	if got := readAlarm(t, svc, "unitless-feed").StateValue; got != stateAlarm {
		t.Errorf("StateValue = %q, want %q", got, stateAlarm)
	}
}

// publishPointWithUnit is publishPoint with an explicit unit.
func publishPointWithUnit(t *testing.T, svc *Service, ns, name string, dims []Dimension, ts time.Time, value float64, unit string) {
	t.Helper()
	dp := &MetricDataPoint{
		Namespace:   ns,
		MetricName:  name,
		Dimensions:  dims,
		Timestamp:   ts.UTC(),
		Unit:        unit,
		SampleCount: 1,
		Sum:         value,
		Minimum:     value,
		Maximum:     value,
	}
	if err := svc.store.putMetricDataPoint(context.Background(), dp); err != nil {
		t.Fatalf("putMetricDataPoint: %v", err)
	}
}

// TestPutMetricAlarm_ProtocolsAgreeOnOptionalHandling pins that the Query and
// JSON paths parse the same request into the same alarm — the drift
// alarm_input.go's shared alarmInput exists to prevent.
func TestPutMetricAlarm_ProtocolsAgreeOnOptionalHandling(t *testing.T) {
	svc := newTestService(t)

	params := completeAlarmParams("via-query")
	params.Set("TreatMissingData", "notBreaching")
	params.Set("DatapointsToAlarm", "1")
	params.Set("Unit", "Count")
	if w := putAlarmForm(t, svc, params); w.Code != http.StatusOK {
		t.Fatalf("query: status = %d; body = %s", w.Code, w.Body.String())
	}

	body := `{"AlarmName":"via-json","Namespace":"TestNS","MetricName":"Latency","Statistic":"Average",
	          "Period":60,"EvaluationPeriods":2,"Threshold":100,"ComparisonOperator":"GreaterThanThreshold",
	          "TreatMissingData":"notBreaching","DatapointsToAlarm":1,"Unit":"Count"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-amz-json-1.0")
	r = r.WithContext(codec.WithDispatch(r.Context(), codec.JSON10, "PutMetricAlarm"))
	w := httptest.NewRecorder()
	svc.Dispatch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("json: status = %d; body = %s", w.Code, w.Body.String())
	}

	viaQuery := readAlarm(t, svc, "via-query")
	viaJSON := readAlarm(t, svc, "via-json")
	// Name, ARN and the creation timestamps legitimately differ.
	viaQuery.AlarmName, viaJSON.AlarmName = "", ""
	viaQuery.AlarmArn, viaJSON.AlarmArn = "", ""
	viaQuery.StateUpdatedTimestamp, viaJSON.StateUpdatedTimestamp = "", ""
	viaQuery.AlarmConfigurationUpdatedTimestamp, viaJSON.AlarmConfigurationUpdatedTimestamp = "", ""

	gotQuery, _ := json.Marshal(viaQuery)
	gotJSON, _ := json.Marshal(viaJSON)
	if string(gotQuery) != string(gotJSON) {
		t.Errorf("protocols disagree:\n query = %s\n  json = %s", gotQuery, gotJSON)
	}
}
