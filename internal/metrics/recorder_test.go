package metrics

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

func newTestRecorder(t *testing.T) (*Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	svc := NewRecorder(state.NewMemoryStore(), mock, zap.NewNop())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc, mock
}

// TestRecorder_ObserveVisibleBeforeFlush pins the plan's "successful
// observations are query-visible before a flush" guarantee.
func TestRecorder_ObserveVisibleBeforeFlush(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	now := mock.Now()

	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}
	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	buckets, err := svc.QueryRange(ctx, "AWS/Lambda", "Invocations", dims, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d: %+v", len(buckets), buckets)
	}
	if buckets[0].Count != 2 || buckets[0].Sum != 2 {
		t.Errorf("expected Sum queried as Count=2 (AWS Invocations semantics), got count=%v sum=%v", buckets[0].Count, buckets[0].Sum)
	}
}

// TestRecorder_DurationStatistics pins that a duration-style metric keeps
// value/count/sum/min/max so Average/Minimum/Maximum/Sum all work.
func TestRecorder_DurationStatistics(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	now := mock.Now()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}

	for _, ms := range []float64{100, 200, 300} {
		if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Duration", Dimensions: dims, Unit: "Milliseconds", Value: ms, Timestamp: now}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	buckets, err := svc.QueryRange(ctx, "AWS/Lambda", "Duration", dims, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(buckets))
	}
	b := buckets[0]
	if b.Count != 3 || b.Sum != 600 || b.Min != 100 || b.Max != 300 {
		t.Errorf("expected count=3 sum=600 min=100 max=300, got %+v", b)
	}
	if avg := b.Sum / b.Count; avg != 200 {
		t.Errorf("expected average 200ms, got %v", avg)
	}
}

// TestRecorder_MissingMetricIsNotZero pins "a missing metric means no
// observation was emitted, not a synthetic zero" — an unrecorded series
// returns no buckets at all, never a zero-valued one.
func TestRecorder_MissingMetricIsNotZero(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	now := mock.Now()
	dims := []Dimension{{Name: "FunctionName", Value: "never-invoked"}}

	buckets, err := svc.QueryRange(ctx, "AWS/Lambda", "Errors", dims, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(buckets) != 0 {
		t.Fatalf("expected no buckets for a never-recorded series, got %+v", buckets)
	}
}

// TestRecorder_DifferentDimensionsAreDifferentSeries pins metric identity =
// (namespace, name, complete dimension set): two functions never merge.
func TestRecorder_DifferentDimensionsAreDifferentSeries(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	now := mock.Now()

	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations",
		Dimensions: []Dimension{{Name: "FunctionName", Value: "fn-a"}}, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe fn-a: %v", err)
	}
	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations",
		Dimensions: []Dimension{{Name: "FunctionName", Value: "fn-b"}}, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe fn-b: %v", err)
	}

	bucketsA, err := svc.QueryRange(ctx, "AWS/Lambda", "Invocations", []Dimension{{Name: "FunctionName", Value: "fn-a"}}, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange fn-a: %v", err)
	}
	if len(bucketsA) != 1 || bucketsA[0].Count != 1 {
		t.Fatalf("expected fn-a to have exactly its own count of 1, got %+v", bucketsA)
	}

	metrics, err := svc.ListMetrics(ctx, "AWS/Lambda")
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("expected 2 distinct series (fn-a, fn-b), got %d: %+v", len(metrics), metrics)
	}
}

// TestRecorder_FlushThenQueryStillVisible pins that persisted data (no
// longer in the active overlay) still answers a query, and that Flush is
// idempotent to call again with nothing dirty.
func TestRecorder_FlushThenQueryStillVisible(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	now := mock.Now()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}

	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Errors", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("second Flush (nothing dirty): %v", err)
	}

	buckets, err := svc.QueryRange(ctx, "AWS/Lambda", "Errors", dims, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Count != 1 {
		t.Fatalf("expected persisted bucket to remain queryable, got %+v", buckets)
	}
}

// TestRecorder_RetentionSweepExpiresOldBuckets pins the retention policy: a
// bucket older than the 60s tier's retention window (24h — extended from
// phase 1's 1h so the plan's 6h/24h chart controls, which stay at 1-minute
// resolution, have real data) is physically removed by the sweep and no
// longer answered by a query, even though the query window still covers it.
func TestRecorder_RetentionSweepExpiresOldBuckets(t *testing.T) {
	svc, mock := newTestRecorder(t)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "fn-a"}}
	// The observation is timestamped 25 hours before "now" directly, rather
	// than advancing the mock clock 25 hours forward: this Service's
	// background flush/sweep tickers are real (5s / 5m) intervals, and
	// mock.Add processes every intervening tick one at a time — advancing it
	// by a large multiple of a short ticker period is a real (if harmless)
	// multi-second stall in the test, not a hang, but there is no need to pay
	// it: sweepOnce is called directly below rather than through the ticker.
	start := mock.Now().Add(-25 * time.Hour)

	if err := svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: start}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := svc.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	svc.sweepOnce(ctx) // clock is still at mock.Now(), 25h after start — past the 24h retention window

	buckets, err := svc.QueryRange(ctx, "AWS/Lambda", "Invocations", dims, start.Add(-time.Minute), start.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(buckets) != 0 {
		t.Fatalf("expected expired bucket to be gone after retention sweep, got %+v", buckets)
	}
}

// TestRecorder_ObserveRejectsEmptyIdentity and
// TestRecorder_ObserveAfterStopIsRejected pin Observe's two non-storage error
// cases.
func TestRecorder_ObserveRejectsEmptyIdentity(t *testing.T) {
	svc, _ := newTestRecorder(t)
	if err := svc.Observe(context.Background(), Observation{Name: "Invocations"}); err == nil {
		t.Error("expected an error for a missing Namespace")
	}
	if err := svc.Observe(context.Background(), Observation{Namespace: "AWS/Lambda"}); err == nil {
		t.Error("expected an error for a missing Name")
	}
}

func TestRecorder_ObserveAfterStopIsRejected(t *testing.T) {
	mock := clock.NewMock()
	svc := NewRecorder(state.NewMemoryStore(), mock, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.Stop(ctx)

	if err := svc.Observe(context.Background(), Observation{Namespace: "AWS/Lambda", Name: "Invocations", Value: 1}); err != errRecorderStopped {
		t.Errorf("expected errRecorderStopped after Stop, got %v", err)
	}
}
