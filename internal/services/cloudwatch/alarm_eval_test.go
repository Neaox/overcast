package cloudwatch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// newAlarmTestService returns a Service wired to a mock clock parked at a
// period-aligned wall time, so tests can reason about aligned evaluation
// windows without epoch-boundary special cases.
//
// The background evaluation loop is stopped straight away: these tests drive
// evaluateAlarmsOnce directly so that advancing the mock clock to build a
// window cannot also race an unwanted tick through the same alarm. The loop
// itself is covered by TestAlarmEvaluatorLoop_EvaluatesOnTick.
func newAlarmTestService(t *testing.T) (*Service, *clock.Mock) {
	t.Helper()
	svc, mock := newRunningAlarmService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svc.Stop(ctx)
	return svc, mock
}

// newRunningAlarmService is newAlarmTestService with its evaluation loop left
// running.
func newRunningAlarmService(t *testing.T) (*Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	st := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, st, zap.NewNop(), mock)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc, mock
}

// storeAlarm persists a. Tests build the alarm directly rather than going
// through the wire handlers so that evaluation semantics are pinned
// independently of parameter parsing.
func storeAlarm(t *testing.T, svc *Service, a *MetricAlarm) {
	t.Helper()
	if err := svc.store.putAlarm(context.Background(), a); err != nil {
		t.Fatalf("putAlarm: %v", err)
	}
	svc.invalidateAlarmCount()
}

// publishPoint stores one datapoint at ts with the given value.
func publishPoint(t *testing.T, svc *Service, ns, name string, dims []Dimension, ts time.Time, value float64) {
	t.Helper()
	dp := &MetricDataPoint{
		Namespace:   ns,
		MetricName:  name,
		Dimensions:  dims,
		Timestamp:   ts.UTC(),
		SampleCount: 1,
		Sum:         value,
		Minimum:     value,
		Maximum:     value,
	}
	if err := svc.store.putMetricDataPoint(context.Background(), dp); err != nil {
		t.Fatalf("putMetricDataPoint: %v", err)
	}
}

func readAlarm(t *testing.T, svc *Service, name string) *MetricAlarm {
	t.Helper()
	a, found := svc.store.getAlarm(context.Background(), name)
	if !found {
		t.Fatalf("alarm %q not found", name)
	}
	return a
}

func baseAlarm(name string) *MetricAlarm {
	return &MetricAlarm{
		AlarmName:          name,
		AlarmArn:           "arn:aws:cloudwatch:us-east-1:000000000000:alarm:" + name,
		Namespace:          "TestNS",
		MetricName:         "Latency",
		Statistic:          "Average",
		Period:             60,
		EvaluationPeriods:  1,
		DatapointsToAlarm:  1,
		Threshold:          100,
		ComparisonOperator: "GreaterThanThreshold",
		ActionsEnabled:     true,
		StateValue:         "INSUFFICIENT_DATA",
		StateReason:        "Unchecked: Initial alarm creation",
	}
}

// TestEvaluateAlarms_MatchesDimensions pins that an alarm scoped to a
// dimension set only ever sees that dimension set's datapoints. The previous
// evaluator always queried with nil dimensions, so a breach published against
// the alarm's own dimensions was invisible to it and the alarm never fired.
func TestEvaluateAlarms_MatchesDimensions(t *testing.T) {
	// Given: an alarm on Latency{Service=api} with a threshold of 100
	svc, mock := newAlarmTestService(t)
	dims := []Dimension{{Name: "Service", Value: "api"}}
	alarm := baseAlarm("dimensioned")
	alarm.Dimensions = dims
	storeAlarm(t, svc, alarm)

	// And: a breaching datapoint published against those dimensions, plus a
	// non-breaching one on the same metric with no dimensions at all.
	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", dims, now.Add(-30*time.Second), 250)
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 1)

	// When: the evaluator runs after that period closes
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	// Then: the alarm is in ALARM, driven by its own dimension set
	if got := readAlarm(t, svc, "dimensioned").StateValue; got != "ALARM" {
		t.Fatalf("StateValue = %q, want ALARM", got)
	}
}

// TestEvaluateAlarms_DatapointsToAlarm pins the "M out of N" rule: with
// DatapointsToAlarm below EvaluationPeriods, a minority of breaching
// datapoints is enough to raise the alarm.
func TestEvaluateAlarms_DatapointsToAlarm(t *testing.T) {
	// Given: an alarm requiring 2 breaching datapoints out of the last 3
	svc, mock := newAlarmTestService(t)
	alarm := baseAlarm("m-of-n")
	alarm.EvaluationPeriods = 3
	alarm.DatapointsToAlarm = 2
	storeAlarm(t, svc, alarm)

	// And: 2 of the last 3 periods breach
	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-150*time.Second), 500)
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-90*time.Second), 1)
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 500)

	// When: the evaluator runs
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	// Then: the alarm is in ALARM even though the majority did not breach
	if got := readAlarm(t, svc, "m-of-n").StateValue; got != "ALARM" {
		t.Fatalf("StateValue = %q, want ALARM", got)
	}
}

// TestEvaluateAlarms_TreatMissingData covers all four AWS missing-data modes
// over an evaluation range with no datapoints at all.
func TestEvaluateAlarms_TreatMissingData(t *testing.T) {
	cases := []struct {
		mode string
		want string
	}{
		{mode: "", want: "INSUFFICIENT_DATA"},
		{mode: "missing", want: "INSUFFICIENT_DATA"},
		{mode: "breaching", want: "ALARM"},
		{mode: "notBreaching", want: "OK"},
		{mode: "ignore", want: "OK"},
	}
	for _, tc := range cases {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			// Given: an alarm with the mode under test, parked in OK so that
			// "ignore" has a state worth retaining
			svc, mock := newAlarmTestService(t)
			alarm := baseAlarm("missing-data")
			alarm.TreatMissingData = tc.mode
			alarm.StateValue = "OK"
			alarm.StateReason = "seeded"
			storeAlarm(t, svc, alarm)

			// When: the evaluator runs over a range with no datapoints
			mock.Set(mock.Now().Add(5 * time.Minute))
			svc.evaluateAlarmsOnce(context.Background())

			// Then: the state matches the mode's documented outcome
			if got := readAlarm(t, svc, "missing-data").StateValue; got != tc.want {
				t.Fatalf("mode %q: StateValue = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

// TestEvaluateAlarms_StateReasonData pins the AWS StateReasonData JSON shape
// so SDK consumers that parse it keep working.
func TestEvaluateAlarms_StateReasonData(t *testing.T) {
	// Given: an alarm and one breaching datapoint
	svc, mock := newAlarmTestService(t)
	storeAlarm(t, svc, baseAlarm("reason-data"))
	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 250)

	// When: the evaluator runs
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	// Then: StateReasonData parses and carries AWS's documented fields
	got := readAlarm(t, svc, "reason-data")
	var data struct {
		Version          string    `json:"version"`
		QueryDate        string    `json:"queryDate"`
		StartDate        string    `json:"startDate"`
		Statistic        string    `json:"statistic"`
		Period           int       `json:"period"`
		RecentDatapoints []float64 `json:"recentDatapoints"`
		Threshold        float64   `json:"threshold"`
	}
	if err := json.Unmarshal([]byte(got.StateReasonData), &data); err != nil {
		t.Fatalf("StateReasonData is not JSON (%q): %v", got.StateReasonData, err)
	}
	if data.Version != "1.0" {
		t.Errorf("version = %q, want 1.0", data.Version)
	}
	if data.Statistic != "Average" || data.Period != 60 || data.Threshold != 100 {
		t.Errorf("statistic/period/threshold = %q/%d/%v, want Average/60/100", data.Statistic, data.Period, data.Threshold)
	}
	if len(data.RecentDatapoints) != 1 || data.RecentDatapoints[0] != 250 {
		t.Errorf("recentDatapoints = %v, want [250]", data.RecentDatapoints)
	}
	if data.QueryDate == "" || data.StartDate == "" {
		t.Errorf("queryDate/startDate = %q/%q, want both populated", data.QueryDate, data.StartDate)
	}
	// And: the human-readable reason names the datapoint count and threshold
	if want := "Threshold Crossed"; len(got.StateReason) == 0 || got.StateReason[:len(want)] != want {
		t.Errorf("StateReason = %q, want it to start with %q", got.StateReason, want)
	}
}

// TestEvaluateAlarms_RecordsHistoryOnTransitionOnly pins that alarm history
// gains one StateUpdate per transition — not one per evaluation.
func TestEvaluateAlarms_RecordsHistoryOnTransitionOnly(t *testing.T) {
	// Given: an alarm and a breaching datapoint
	svc, mock := newAlarmTestService(t)
	storeAlarm(t, svc, baseAlarm("history"))
	now := mock.Now().UTC()
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(-30*time.Second), 250)

	// When: the evaluator runs over two consecutive breaching windows
	mock.Set(now.Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())
	publishPoint(t, svc, "TestNS", "Latency", nil, now.Add(30*time.Second), 250)
	mock.Set(now.Add(90 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	// Then: exactly one StateUpdate entry was recorded
	items, err := svc.store.listAlarmHistory(context.Background(), "history")
	if err != nil {
		t.Fatalf("listAlarmHistory: %v", err)
	}
	stateUpdates := 0
	for _, it := range items {
		if it.HistoryItemType == "StateUpdate" {
			stateUpdates++
		}
	}
	if stateUpdates != 1 {
		t.Fatalf("StateUpdate history entries = %d, want 1", stateUpdates)
	}
}

// TestEvaluateAlarms_ManualStateIsHeld pins the DoD requirement that
// SetAlarmState remains usable: the evaluator must not immediately stomp a
// manually-set state.
func TestEvaluateAlarms_ManualStateIsHeld(t *testing.T) {
	// Given: an alarm whose metric is quiet (so evaluation would say
	// INSUFFICIENT_DATA) that has been manually forced into ALARM
	svc, mock := newAlarmTestService(t)
	alarm := baseAlarm("manual")
	storeAlarm(t, svc, alarm)
	svc.applyAlarmState(context.Background(), alarm, "ALARM", "manual test", "", true)

	// When: the evaluator runs within the hold window
	mock.Set(mock.Now().Add(30 * time.Second))
	svc.evaluateAlarmsOnce(context.Background())

	// Then: the manual state survives
	if got := readAlarm(t, svc, "manual").StateValue; got != "ALARM" {
		t.Fatalf("StateValue during hold = %q, want ALARM", got)
	}

	// When: the hold expires
	mock.Set(mock.Now().Add(10 * time.Minute))
	svc.evaluateAlarmsOnce(context.Background())

	// Then: the evaluator takes over again
	if got := readAlarm(t, svc, "manual").StateValue; got != "INSUFFICIENT_DATA" {
		t.Fatalf("StateValue after hold = %q, want INSUFFICIENT_DATA", got)
	}
}

// TestEvaluateAlarms_SkipsWhenNoAlarms pins the idle cost of the loop: with
// no alarms configured, a tick must not touch the store at all.
func TestEvaluateAlarms_SkipsWhenNoAlarms(t *testing.T) {
	// Given: a service with no alarms, whose alarm count has been learned
	svc, _ := newAlarmTestService(t)
	counting := &countingStore{Store: state.NewMemoryStore()}
	svc.store.store = counting
	svc.evaluateAlarmsOnce(context.Background())
	before := counting.scans

	// When: further ticks run
	svc.evaluateAlarmsOnce(context.Background())
	svc.evaluateAlarmsOnce(context.Background())

	// Then: no further store scans were issued
	if counting.scans != before {
		t.Fatalf("store scans grew from %d to %d with no alarms configured", before, counting.scans)
	}
}

// countingStore counts Scan calls so the idle-cost test can assert that an
// empty alarm set costs no store I/O per tick.
type countingStore struct {
	state.Store
	scans int
}

func (c *countingStore) Scan(ctx context.Context, namespace, prefix string) ([]state.KV, error) {
	c.scans++
	return c.Store.Scan(ctx, namespace, prefix)
}
