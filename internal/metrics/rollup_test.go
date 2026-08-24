package metrics

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// TestRollup_FineBucketsAggregateIntoCoarserTier pins the core rollup
// contract: several 60s buckets inside one closed 5-minute window compose
// into a single 300s bucket with the same total count/sum and folded min/max.
func TestRollup_FineBucketsAggregateIntoCoarserTier(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}

	windowStart := mock.Now().Truncate(5 * time.Minute)
	for i, ms := range []float64{100, 200, 50} {
		ts := windowStart.Add(time.Duration(i) * time.Minute)
		if err := svc.Observe(ctx, Observation{
			Namespace: "AWS/Lambda", Name: "Duration", Dimensions: dims,
			Unit: "Milliseconds", Value: ms, Timestamp: ts,
		}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// rollupOnce is called with an explicit "now" shortly after the window
	// closes and its safety margin elapses — exactly the real sweep tick that
	// would first pick this window up, rather than an arbitrary later time:
	// catchUpWindows only self-heals a handful of trailing windows relative
	// to "now", it is not "every window since the dawn of the series".
	svc.rollupOnce(ctx, windowStart.Add(5*time.Minute).Add(rollupSafetyMargin).Add(time.Second))

	coarse, err := svc.queryRangeAt(ctx, "AWS/Lambda", "Duration", dims, 300, windowStart.Add(-time.Minute), windowStart.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("queryRangeAt(300): %v", err)
	}
	if len(coarse) != 1 {
		t.Fatalf("expected exactly 1 rolled-up 300s bucket, got %d: %+v", len(coarse), coarse)
	}
	b := coarse[0]
	if b.Count != 3 || b.Sum != 350 || b.Min != 50 || b.Max != 200 {
		t.Errorf("expected count=3 sum=350 min=50 max=200, got %+v", b)
	}
	if !b.Start.Equal(windowStart) {
		t.Errorf("expected rolled-up bucket start=%v, got %v", windowStart, b.Start)
	}
}

// TestRollup_EmptyWindowWritesNothing pins "a missing metric means no
// observation was emitted, not a synthetic zero" applied to the rollup ladder
// itself: a series with no fine buckets in a window gets no coarse bucket for
// it, not a zero-valued one.
func TestRollup_EmptyWindowWritesNothing(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "never-invoked"}}

	// Give the series a presence (so listSeries finds it) via an observation
	// an hour before the empty window under test, then roll up the empty
	// window itself.
	windowStart := mock.Now().Truncate(5 * time.Minute)
	other := windowStart.Add(-time.Hour)
	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Errors", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: other}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	svc.rollupOnce(ctx, windowStart.Add(5*time.Minute).Add(rollupSafetyMargin).Add(time.Second))

	coarse, err := svc.queryRangeAt(ctx, "AWS/Lambda", "Errors", dims, 300, windowStart, windowStart.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("queryRangeAt(300): %v", err)
	}
	if len(coarse) != 0 {
		t.Fatalf("expected no rolled-up bucket for an empty window, got %+v", coarse)
	}
}

// TestRollup_HourlyTierBuildsFromFiveMinuteTier pins that the 3600s tier
// composes from already-rolled 300s buckets (not straight from 60s), and that
// running both rollup passes in one rollupOnce call sees the freshly written
// 300s data in the same tick.
func TestRollup_HourlyTierBuildsFromFiveMinuteTier(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "QueueName", Value: "q-a"}}

	hourStart := mock.Now().Truncate(time.Hour)
	// One observation per 5-minute slot across the hour (12 slots) is enough
	// to prove composition without a 60-observation test.
	for i := 0; i < 12; i++ {
		ts := hourStart.Add(time.Duration(i) * 5 * time.Minute)
		if err := svc.Observe(ctx, Observation{
			Namespace: "AWS/SQS", Name: "NumberOfMessagesSent", Dimensions: dims,
			Unit: "Count", Value: 1, Timestamp: ts,
		}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Simulate the real sweep ticking every 5 minutes (sweepInterval, which
	// matches the 300s spec's own window) across the hour: each tick's
	// catchUpWindows self-heals its own trailing windows, so by the last tick
	// (just after the hour itself has closed) every 5-minute slot has a 300s
	// bucket, and the 300->3600 pass in that same rollupOnce call can compose
	// the whole hour in one shot — a single far-future rollupOnce call could
	// not do this, since catchUpWindows only ever looks a few windows back
	// from "now", not arbitrarily far into a series' history.
	for k := 1; k <= 13; k++ {
		tick := hourStart.Add(time.Duration(k) * 5 * time.Minute).Add(rollupSafetyMargin).Add(time.Second)
		svc.rollupOnce(ctx, tick)
	}

	hourly, err := svc.queryRangeAt(ctx, "AWS/SQS", "NumberOfMessagesSent", dims, 3600, hourStart.Add(-time.Minute), hourStart.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("queryRangeAt(3600): %v", err)
	}
	if len(hourly) != 1 {
		t.Fatalf("expected exactly 1 rolled-up 3600s bucket, got %d: %+v", len(hourly), hourly)
	}
	if hourly[0].Count != 12 || hourly[0].Sum != 12 {
		t.Errorf("expected count=12 sum=12 (one send per 5-minute slot), got %+v", hourly[0])
	}
}

// TestRollup_IsIdempotent pins that recomputing the same window twice (the
// catch-up self-healing margin) never double-counts.
func TestRollup_IsIdempotent(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}

	windowStart := mock.Now().Truncate(5 * time.Minute)
	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: windowStart}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	tick := windowStart.Add(5 * time.Minute).Add(rollupSafetyMargin).Add(time.Second)
	svc.rollupOnce(ctx, tick)
	svc.rollupOnce(ctx, tick) // same windows recomputed again

	coarse, err := svc.queryRangeAt(ctx, "AWS/Lambda", "Invocations", dims, 300, windowStart, windowStart.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("queryRangeAt(300): %v", err)
	}
	if len(coarse) != 1 || coarse[0].Count != 1 {
		t.Fatalf("expected count still 1 after re-running the rollup, got %+v", coarse)
	}
}

// TestSelectResolution pins the planner's tier-selection rule: the finest
// resolution whose retention still covers the request's age.
func TestSelectResolution(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		age  time.Duration
		want int
	}{
		{"within 60s tier", time.Hour, 60},
		{"exactly at 60s tier boundary", 24 * time.Hour, 60},
		{"just past 60s tier", 24*time.Hour + time.Minute, 300},
		{"within 300s tier", 5 * 24 * time.Hour, 300},
		// The 300s tier is retained 30 days (#1307), so a month-scale request
		// still answers from it rather than dropping to hourly buckets.
		{"past the old 7d 300s retention", 7*24*time.Hour + time.Minute, 300},
		{"deep into 300s tier", 20 * 24 * time.Hour, 300},
		{"older than every tier", 90 * 24 * time.Hour, 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectResolution(now, now.Add(-tc.age))
			if got != tc.want {
				t.Errorf("SelectResolution(age=%v) = %d, want %d", tc.age, got, tc.want)
			}
		})
	}
}

// TestParseChartRange pins the plan's five coherent range/period controls and
// rejects anything else.
func TestParseChartRange(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		token      string
		wantSpan   time.Duration
		wantPeriod time.Duration
		wantOK     bool
	}{
		{"1h", time.Hour, time.Minute, true},
		{"6h", 6 * time.Hour, time.Minute, true},
		{"24h", 24 * time.Hour, 5 * time.Minute, true},
		{"7d", 7 * 24 * time.Hour, 5 * time.Minute, true},
		{"30d", 30 * 24 * time.Hour, 15 * time.Minute, true},
		{"1w", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.token, func(t *testing.T) {
			start, end, period, ok := ParseChartRange(tc.token, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if !end.Equal(now) {
				t.Errorf("end = %v, want %v", end, now)
			}
			if got := end.Sub(start); got != tc.wantSpan {
				t.Errorf("span = %v, want %v", got, tc.wantSpan)
			}
			if period != tc.wantPeriod {
				t.Errorf("period = %v, want %v", period, tc.wantPeriod)
			}
		})
	}
}

// TestQueryAuto_RegroupsFineBucketsIntoRequestedPeriod pins that a recent,
// still-60s-tier range requested at a coarser display period (e.g. 24h/5m)
// is regrouped at read time rather than requiring the rollup ladder to have
// already caught up.
func TestQueryAuto_RegroupsFineBucketsIntoRequestedPeriod(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}
	now := mock.Now()

	windowStart := now.Truncate(5 * time.Minute)
	for i, ms := range []float64{10, 20, 30} {
		ts := windowStart.Add(time.Duration(i) * time.Minute)
		if err := svc.Observe(ctx, Observation{
			Namespace: "AWS/Lambda", Name: "Duration", Dimensions: dims,
			Unit: "Milliseconds", Value: ms, Timestamp: ts,
		}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	buckets, resSec, err := svc.QueryAuto(ctx, "AWS/Lambda", "Duration", dims, windowStart, windowStart.Add(5*time.Minute), 5*time.Minute)
	if err != nil {
		t.Fatalf("QueryAuto: %v", err)
	}
	if resSec != 60 {
		t.Fatalf("expected the 60s tier to answer a recent request, got resolutionSeconds=%d", resSec)
	}
	if len(buckets) != 1 {
		t.Fatalf("expected the three 1-minute buckets regrouped into 1 five-minute point, got %d: %+v", len(buckets), buckets)
	}
	if buckets[0].Count != 3 || buckets[0].Sum != 60 {
		t.Errorf("expected count=3 sum=60, got %+v", buckets[0])
	}
}

// TestChartQuery_DerivesRequestedStatistic pins ChartQuery's statistic
// derivation for the two shapes the Monitor tab's catalogue needs: a
// count-style Sum and a duration-style Average/Maximum, both read from the
// same stored bucket.
func TestChartQuery_DerivesRequestedStatistic(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}
	now := mock.Now()

	for _, ms := range []float64{100, 300} {
		if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Duration", Dimensions: dims, Unit: "Milliseconds", Value: ms, Timestamp: now}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	avgPoints, _, err := svc.ChartQuery(ctx, "AWS/Lambda", "Duration", StatAverage, dims, now.Add(-time.Minute), now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("ChartQuery(Average): %v", err)
	}
	if len(avgPoints) != 1 || avgPoints[0].Value != 200 {
		t.Fatalf("expected average 200, got %+v", avgPoints)
	}

	maxPoints, _, err := svc.ChartQuery(ctx, "AWS/Lambda", "Duration", StatMaximum, dims, now.Add(-time.Minute), now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("ChartQuery(Maximum): %v", err)
	}
	if len(maxPoints) != 1 || maxPoints[0].Value != 300 {
		t.Fatalf("expected maximum 300, got %+v", maxPoints)
	}
}

// TestChartQuery_NoDataReturnsEmptyNotError pins that a series with no
// observations answers with an empty slice, never an error or a synthetic
// zero point — the Monitor tab's "No metric data in this range" state relies
// on distinguishing this from a fetch failure.
func TestChartQuery_NoDataReturnsEmptyNotError(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	now := mock.Now()
	dims := []Dimension{{Name: "FunctionName", Value: "never-invoked"}}

	points, resSec, err := svc.ChartQuery(ctx, "AWS/Lambda", "Errors", StatSum, dims, now.Add(-time.Hour), now, time.Minute)
	if err != nil {
		t.Fatalf("ChartQuery: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("expected no points for a never-recorded series, got %+v", points)
	}
	if resSec != 60 {
		t.Errorf("expected the 60s tier for a 1h-old request, got %d", resSec)
	}
}

// newTestRecorderSQLite mirrors newTestRecorder but backs the Service with a
// real SQLite-backed store, so the rollup ladder's persisted-tier behavior is
// also proven against the dedicated metric_buckets table, not only the
// in-memory backend every other test in this file uses. Under -tags
// nosqlite, state.NewSQLiteStore is a stub that always errors
// (internal/state/sqlite_hybrid_nosqlite.go) — config.SQLiteSupported() skips
// the test there rather than failing, matching
// internal/services/dynamodb/item_store_test.go's newTestItemBackends
// precedent (see tests/AGENTS.md § Build-tag-sensitive tests).
func newTestRecorderSQLite(t *testing.T) (*Service, *clock.Mock) {
	t.Helper()
	if !config.SQLiteSupported() {
		t.Skip("SQLite support not compiled in (-tags nosqlite)")
	}
	mock := clock.NewMock()
	store, err := state.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewRecorder(store, mock, zap.NewNop())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc, mock
}

// TestRollup_SQLiteBackend proves the rollup ladder against the dedicated
// SQLite metric_buckets table (SQLiteStore/HybridStore's real backend), not
// only the in-memory one every other test in this file exercises.
func TestRollup_SQLiteBackend(t *testing.T) {
	svc, mock := newTestRecorderSQLite(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}

	windowStart := mock.Now().Truncate(5 * time.Minute)
	for i, ms := range []float64{1, 2, 3} {
		ts := windowStart.Add(time.Duration(i) * time.Minute)
		if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dims, Unit: "Count", Value: ms, Timestamp: ts}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	svc.rollupOnce(ctx, windowStart.Add(5*time.Minute).Add(rollupSafetyMargin).Add(time.Second))

	coarse, err := svc.queryRangeAt(ctx, "AWS/Lambda", "Invocations", dims, 300, windowStart, windowStart.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("queryRangeAt(300): %v", err)
	}
	if len(coarse) != 1 || coarse[0].Count != 3 || coarse[0].Sum != 6 {
		t.Fatalf("expected one rolled-up bucket count=3 sum=6, got %+v", coarse)
	}
}
