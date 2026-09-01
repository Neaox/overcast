package cloudwatch_test

// alarm_limitation_test.go — an alarm Overcast cannot evaluate is created, and
// says what it will not do.
//
// CDK's `Metric.createAlarm` on a MathExpression emits an AWS::CloudWatch::Alarm
// carrying Metrics, and a monitoring stack builds one per function. Refusing
// them with a 501 failed the resource, and with it the stack and the deploy:
//
//	PutMetricAlarm: HTTP 501: Metric-math and multi-metric alarms (the Metrics
//	parameter) is not emulated by Overcast, and this alarm is refused rather than
//	created in a state that looks armed but is never evaluated.
//
// The reasoning behind that refusal is sound — an alarm that looks armed and is
// never evaluated is worse than no alarm — but the price was the whole
// environment, for a resource whose only defect is one Overcast will not act on.
// It is created now, and every way of looking at it says so.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/tests/helpers"
)

// metricMathAlarm is the shape CDK emits for an alarm on a MathExpression: no
// Namespace, MetricName or Statistic of its own, because the Metrics list
// carries them.
func metricMathAlarm(name string) map[string]any {
	return map[string]any{
		"AlarmName":          name,
		"ComparisonOperator": "GreaterThanOrEqualToThreshold",
		"EvaluationPeriods":  1,
		"Threshold":          1,
		"Metrics": []map[string]any{
			{
				"Id":         "errorRate",
				"Expression": "errors / invocations * 100",
				"Label":      "Error rate",
				"ReturnData": "true",
			},
		},
	}
}

func TestPutMetricAlarm_metricMathIsCreatedAndDeclaresItself(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"), helpers.WithAccountID("000000000000"))

	// When: the alarm CDK emits for a metric-math expression is put.
	resp := cwTargetCall(t, srv, "PutMetricAlarm", metricMathAlarm("errors-rate"))
	defer resp.Body.Close()

	// Then: it is created, not refused — the deploy carries on.
	if resp.StatusCode != http.StatusOK {
		body := make([]byte, 400)
		n, _ := resp.Body.Read(body)
		t.Fatalf("PutMetricAlarm = %d, want 200: %s", resp.StatusCode, body[:n])
	}

	// Then: the response says what will not happen to it, for whoever is
	// listening on the wire.
	limitation := protocol.LimitationText(resp.Header)
	if !strings.Contains(limitation, "metric-math") {
		t.Errorf("%s = %q, want it to name metric-math", protocol.EmulationLimitationHeader, limitation)
	}

	// Then: so does the alarm itself, which is where anyone looking at its
	// state will be looking.
	alarm := describeAlarm(t, srv, "errors-rate")
	if got := alarm["StateValue"]; got != "INSUFFICIENT_DATA" {
		t.Errorf("StateValue = %v, want INSUFFICIENT_DATA", got)
	}
	reason, _ := alarm["StateReason"].(string)
	if !strings.Contains(reason, "does not evaluate") {
		t.Errorf("StateReason = %q, want it to say the state is never computed", reason)
	}

	// The Metrics definition is stored with the alarm but DescribeAlarms does
	// not echo it back yet — its response is a hand-built projection rather
	// than the stored alarm. That is a gap in describe, not in this change, and
	// it is left to its own fix rather than asserted here as if it worked.
}

func TestPutMetricAlarm_singleMetricAlarmSaysNothing(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"), helpers.WithAccountID("000000000000"))

	// When: an ordinary alarm — the kind the evaluator really does decide.
	resp := cwTargetCall(t, srv, "PutMetricAlarm", map[string]any{
		"AlarmName":          "ordinary",
		"Namespace":          "AWS/SQS",
		"MetricName":         "ApproximateNumberOfMessagesVisible",
		"Statistic":          "Maximum",
		"ComparisonOperator": "GreaterThanOrEqualToThreshold",
		"Period":             300,
		"EvaluationPeriods":  2,
		"Threshold":          1,
	})
	defer resp.Body.Close()

	// Then: nothing is declared. The header is for the exceptions, and an
	// alarm that carries it when it is evaluated would teach people to ignore it.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PutMetricAlarm = %d, want 200", resp.StatusCode)
	}
	if got := protocol.LimitationText(resp.Header); got != "" {
		t.Errorf("%s = %q, want none on an alarm that is evaluated", protocol.EmulationLimitationHeader, got)
	}
	alarm := describeAlarm(t, srv, "ordinary")
	if reason, _ := alarm["StateReason"].(string); !strings.Contains(reason, "Initial alarm creation") {
		t.Errorf("StateReason = %q, want the ordinary initial-creation reason", reason)
	}
}

func TestPutMetricAlarm_stillRefusesWhatAWSRefuses(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"), helpers.WithAccountID("000000000000"))

	// A metric-math alarm that also names a metric at the top level is one AWS
	// rejects outright. Accepting the shapes Overcast cannot evaluate is not a
	// reason to accept the ones AWS would not have.
	body := metricMathAlarm("contradictory")
	body["Namespace"] = "AWS/Lambda"
	body["MetricName"] = "Errors"

	resp := cwTargetCall(t, srv, "PutMetricAlarm", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PutMetricAlarm = %d, want 400 for an alarm AWS itself rejects", resp.StatusCode)
	}
}

// describeAlarm reads one alarm back through DescribeAlarms.
func describeAlarm(t *testing.T, srv *helpers.TestServer, name string) map[string]any {
	t.Helper()
	resp := cwTargetCall(t, srv, "DescribeAlarms", map[string]any{"AlarmNames": []string{name}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DescribeAlarms = %d, want 200", resp.StatusCode)
	}
	var out struct {
		MetricAlarms []map[string]any `json:"MetricAlarms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode DescribeAlarms: %v", err)
	}
	if len(out.MetricAlarms) != 1 {
		t.Fatalf("DescribeAlarms returned %d alarms, want 1", len(out.MetricAlarms))
	}
	return out.MetricAlarms[0]
}
