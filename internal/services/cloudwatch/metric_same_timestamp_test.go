package cloudwatch

// Store-level tests for putMetricDataPoint's same-timestamp merge. The
// end-to-end proof (both wire protocols, both state.Store implementations)
// lives in tests/integration/cloudwatch; these pin the merge arithmetic itself
// — which fields combine and how — where it can be asserted directly rather
// than inferred from an aggregated GetMetricStatistics response.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

// onlyStoredPoint returns the single datapoint stored for a metric, failing if
// there is not exactly one — the merge's defining property is that two datums
// at one timestamp stay one row.
func onlyStoredPoint(t *testing.T, ctx context.Context, s *cloudwatchStore, namespace, metricName string, at time.Time) *MetricDataPoint {
	t.Helper()
	points, err := s.listMetricDataPoints(ctx, namespace, metricName, nil, at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil {
		t.Fatalf("listMetricDataPoints: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("expected the two datums to share one stored point, got %d points", len(points))
	}
	return points[0]
}

func TestPutMetricDataPoint_sameTimestampMergesStatistics(t *testing.T) {
	mock := clock.NewMock()
	s := newCloudwatchStore(state.NewMemoryStore(), mock)
	ctx := context.Background()
	at := mock.Now().UTC()

	// Two single-value datums at one timestamp — the reported case.
	for _, v := range []float64{40, 60} {
		if err := s.putMetricDataPoint(ctx, freshDataPoint("TestNS", "CPUUtilization", at, v)); err != nil {
			t.Fatalf("putMetricDataPoint(%v): %v", v, err)
		}
	}

	got := onlyStoredPoint(t, ctx, s, "TestNS", "CPUUtilization", at)
	if got.SampleCount != 2 || got.Sum != 100 {
		t.Fatalf("expected merged SampleCount 2 / Sum 100, got %v / %v", got.SampleCount, got.Sum)
	}
	// Min/max extend across both datums rather than tracking only the last.
	if got.Minimum != 40 || got.Maximum != 60 {
		t.Fatalf("expected merged Minimum 40 / Maximum 60, got %v / %v", got.Minimum, got.Maximum)
	}
}

// Statistic sets merge the same way single values do: PutMetricData's
// StatisticValues form already carries a pre-aggregated count/sum/min/max, so
// merging two of them is the same arithmetic with counts above 1.
func TestPutMetricDataPoint_sameTimestampMergesStatisticSets(t *testing.T) {
	mock := clock.NewMock()
	s := newCloudwatchStore(state.NewMemoryStore(), mock)
	ctx := context.Background()
	at := mock.Now().UTC()

	first := &MetricDataPoint{
		Namespace: "TestNS", MetricName: "Latency", Timestamp: at, Unit: "Milliseconds",
		SampleCount: 10, Sum: 500, Minimum: 20, Maximum: 90,
	}
	second := &MetricDataPoint{
		Namespace: "TestNS", MetricName: "Latency", Timestamp: at, Unit: "Milliseconds",
		SampleCount: 5, Sum: 250, Minimum: 30, Maximum: 120,
	}
	for _, dp := range []*MetricDataPoint{first, second} {
		if err := s.putMetricDataPoint(ctx, dp); err != nil {
			t.Fatalf("putMetricDataPoint: %v", err)
		}
	}

	got := onlyStoredPoint(t, ctx, s, "TestNS", "Latency", at)
	if got.SampleCount != 15 || got.Sum != 750 {
		t.Fatalf("expected merged SampleCount 15 / Sum 750, got %v / %v", got.SampleCount, got.Sum)
	}
	if got.Minimum != 20 || got.Maximum != 120 {
		t.Fatalf("expected merged Minimum 20 / Maximum 120, got %v / %v", got.Minimum, got.Maximum)
	}
	if got.Unit != "Milliseconds" {
		t.Fatalf("expected merged Unit Milliseconds, got %q", got.Unit)
	}

	// The caller's own structs must not have been written back through: a
	// caller that reuses a MetricDataPoint across calls would otherwise
	// double-count the running total on its next put.
	if second.SampleCount != 5 || second.Sum != 250 {
		t.Fatalf("merge mutated the caller's datapoint: SampleCount %v / Sum %v", second.SampleCount, second.Sum)
	}
}

// Different timestamps must still produce separate points — the merge keys off
// the timestamp, so it must not collapse a metric's whole history into one row.
func TestPutMetricDataPoint_differentTimestampsStaySeparate(t *testing.T) {
	mock := clock.NewMock()
	s := newCloudwatchStore(state.NewMemoryStore(), mock)
	ctx := context.Background()
	at := mock.Now().UTC()

	if err := s.putMetricDataPoint(ctx, freshDataPoint("TestNS", "CPUUtilization", at, 40)); err != nil {
		t.Fatalf("putMetricDataPoint(first): %v", err)
	}
	if err := s.putMetricDataPoint(ctx, freshDataPoint("TestNS", "CPUUtilization", at.Add(time.Second), 60)); err != nil {
		t.Fatalf("putMetricDataPoint(second): %v", err)
	}

	points, err := s.listMetricDataPoints(ctx, "TestNS", "CPUUtilization", nil, at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil {
		t.Fatalf("listMetricDataPoints: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 separate points for 2 timestamps, got %d", len(points))
	}
}

// The merge is a read-modify-write over a store with no compare-and-swap, so
// concurrent puts to one key are exactly where a lost update would reappear.
// Run with -race for the data-race half; the count assertion is what catches a
// dropped contribution.
func TestPutMetricDataPoint_concurrentSameTimestampLosesNothing(t *testing.T) {
	mock := clock.NewMock()
	s := newCloudwatchStore(state.NewMemoryStore(), mock)
	ctx := context.Background()
	at := mock.Now().UTC()

	const writers = 16
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			if err := s.putMetricDataPoint(ctx, freshDataPoint("TestNS", "Requests", at, 1)); err != nil {
				t.Errorf("putMetricDataPoint: %v", err)
			}
		}()
	}
	wg.Wait()

	got := onlyStoredPoint(t, ctx, s, "TestNS", "Requests", at)
	if got.SampleCount != writers || got.Sum != writers {
		t.Fatalf("expected all %d concurrent datums to survive (SampleCount/Sum), got %v / %v", writers, got.SampleCount, got.Sum)
	}
}
