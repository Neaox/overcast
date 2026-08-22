package cloudwatch

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/state"
)

// newLambdaAlarmHarness returns a cloudwatch Service wired to a real
// internal/metrics.Recorder over a shared mock clock and store, with the
// evaluation loop stopped so the test drives evaluateAlarmsOnce directly —
// mirroring newAlarmTestService in alarm_eval_test.go.
func newLambdaAlarmHarness(t *testing.T) (*Service, *metrics.Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	st := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, st, zap.NewNop(), mock)
	rec := metrics.NewRecorder(st, mock, zap.NewNop())
	svc.InitMetrics(rec)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(ctx)
		rec.Stop(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svc.Stop(ctx) // stop the background evaluator; tests call evaluateAlarmsOnce directly
	return svc, rec, mock
}

// TestLambdaErrorsAlarm_FiresFromAutomaticMetrics is the acceptance test for
// docs/plans/service-metrics-platform.md's Lambda pilot: an AWS/Lambda Errors
// alarm, evaluated by the *existing, unmodified* alarm evaluator, transitions
// to ALARM purely from internal/metrics observations — no PutMetricData call
// anywhere in this test. This is what "an alarm on AWS/Lambda Errors
// evaluates and fires end-to-end" means: Lambda's recorder writes through
// metrics.Recorder.Observe, exactly as its recordInvocationOutcome helper
// does, and CloudWatch's read-through (metrics_bridge.go) is the only new
// code an alarm ever needed.
func TestLambdaErrorsAlarm_FiresFromAutomaticMetrics(t *testing.T) {
	svc, rec, mock := newLambdaAlarmHarness(t)
	dims := []Dimension{{Name: "FunctionName", Value: "my-function"}}

	alarm := &MetricAlarm{
		AlarmName:          "lambda-errors",
		AlarmArn:           "arn:aws:cloudwatch:us-east-1:000000000000:alarm:lambda-errors",
		Namespace:          "AWS/Lambda",
		MetricName:         "Errors",
		Dimensions:         dims,
		Statistic:          "Sum",
		Period:             60,
		EvaluationPeriods:  1,
		DatapointsToAlarm:  1,
		Threshold:          0,
		ComparisonOperator: "GreaterThanThreshold",
		ActionsEnabled:     true,
		StateValue:         "INSUFFICIENT_DATA",
		StateReason:        "Unchecked: Initial alarm creation",
	}
	storeAlarm(t, svc, alarm)

	// Given: no Errors recorded yet, the alarm stays INSUFFICIENT_DATA.
	svc.evaluateAlarmsOnce(context.Background())
	if got := readAlarm(t, svc, "lambda-errors").StateValue; got != "INSUFFICIENT_DATA" {
		t.Fatalf("StateValue before any invocation = %q, want INSUFFICIENT_DATA", got)
	}

	// When: a failing Lambda invocation records Invocations=1 and Errors=1 —
	// exactly what invokeSync's recordInvocationOutcome does on a function
	// error, threaded through the same metrics.Recorder the router injects.
	now := mock.Now().UTC()
	mdims := toMetricsDimensions(dims)
	if err := rec.Observe(context.Background(), metrics.Observation{
		Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: mdims, Unit: "Count", Value: 1, Timestamp: now,
	}); err != nil {
		t.Fatalf("Observe Invocations: %v", err)
	}
	if err := rec.Observe(context.Background(), metrics.Observation{
		Namespace: "AWS/Lambda", Name: "Errors", Dimensions: mdims, Unit: "Count", Value: 1, Timestamp: now,
	}); err != nil {
		t.Fatalf("Observe Errors: %v", err)
	}

	// And: the evaluator runs once the period has closed.
	mock.Set(now.Add(90 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	got := readAlarm(t, svc, "lambda-errors")
	if got.StateValue != "ALARM" {
		t.Fatalf("StateValue after a failing invocation = %q (reason %q), want ALARM", got.StateValue, got.StateReason)
	}

	// And: GetMetricStatistics-equivalent read-through reports the same
	// datapoint an alarm-firing customer's dashboard would see — Invocations
	// queried with Sum reports 1, matching AWS's count-metric convention.
	points, err := svc.store.mergedMetricDataPoints(context.Background(), "AWS/Lambda", "Invocations", dims, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("mergedMetricDataPoints: %v", err)
	}
	if len(points) != 1 || points[0].SampleCount != 1 || points[0].Sum != 1 {
		t.Fatalf("expected exactly one Invocations datapoint with Sum=SampleCount=1, got %+v", points)
	}

	// And: ListMetrics surfaces the automatically-recorded series alongside
	// (an empty, in this test) custom PutMetricData catalogue.
	listed, err := svc.store.mergedListMetrics(context.Background(), "AWS/Lambda")
	if err != nil {
		t.Fatalf("mergedListMetrics: %v", err)
	}
	foundErrors := false
	for _, m := range listed {
		if m.MetricName == "Errors" && len(m.Dimensions) == 1 && m.Dimensions[0].Value == "my-function" {
			foundErrors = true
		}
	}
	if !foundErrors {
		t.Fatalf("expected AWS/Lambda Errors{FunctionName=my-function} in ListMetrics, got %+v", listed)
	}
}

// TestMergedMetricDataPoints_CombinesCustomAndAutomatic pins that a custom
// PutMetricData point and an automatically-recorded observation on the same
// series both appear in the merged read, matching the plan's "Keep custom
// PutMetricData fully supported through the same repository" requirement
// (implemented here as a read-through rather than a shared storage engine —
// see metrics_bridge.go's doc comment for why).
func TestMergedMetricDataPoints_CombinesCustomAndAutomatic(t *testing.T) {
	svc, rec, mock := newLambdaAlarmHarness(t)
	now := mock.Now().UTC()
	dims := []Dimension{{Name: "FunctionName", Value: "my-function"}}

	publishPoint(t, svc, "AWS/Lambda", "Duration", dims, now, 42) // custom PutMetricData point
	if err := rec.Observe(context.Background(), metrics.Observation{
		Namespace: "AWS/Lambda", Name: "Duration", Dimensions: toMetricsDimensions(dims), Unit: "Milliseconds", Value: 8, Timestamp: now,
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	points, err := svc.store.mergedMetricDataPoints(context.Background(), "AWS/Lambda", "Duration", dims, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("mergedMetricDataPoints: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 1 custom point + 1 automatic bucket = 2 points, got %d: %+v", len(points), points)
	}
}
