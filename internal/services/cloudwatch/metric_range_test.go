package cloudwatch

// Tests for storage-plan.md A5 — listMetricDataPoints becomes a key-range
// ScanPage walk instead of a full-prefix Scan-and-decode, since
// cloudwatch:metricdata keys (`<ns>/<metric>/<dims>/<UnixNano>`) are already
// time-ordered within one metric's prefix (see metricDataPrefix's doc
// comment on service.go).
//
// Two correctness properties are exercised here:
//
//   - Window equality: the new range read must return exactly the same
//     points as the old "Scan the whole prefix, decode everything, filter
//     by [start,end] and retention cutoff" approach, for randomized point
//     sets and windows (oracleListMetricDataPoints below re-implements the
//     old approach directly against the store, without going through the
//     production code path being replaced).
//   - Boundary exactness: a point whose timestamp equals startTime or
//     endTime exactly must be included (aggregateMetricBuckets' own filter
//     is `ts.Before(startTime) || ts.After(endTime)` — i.e. inclusive both
//     ends — and listMetricDataPoints must not narrow that).
//
// The GetMetricStatistics benchmark (0/2000/8000 retained-outside-window
// points) lives in metric_range_bench_test.go, which needs a real
// HybridStore and so carries the same !nosqlite build tag as
// metric_burst_bench_test.go; this file only needs MemoryStore and must
// build under -tags nosqlite too.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

// oracleListMetricDataPoints re-implements listMetricDataPoints' pre-A5
// behavior directly: Scan the entire metric prefix, decode every point, drop
// anything older than the retention cutoff, then apply the same inclusive
// [startTime, endTime] filter aggregateMetricBuckets applies downstream.
// This is the "old full-scan+filter" oracle the window-equality property
// checks the new range-read implementation against. It deliberately does
// not delete expired rows (unlike the production retention-on-read
// behavior) so it can be called without disturbing what the real
// listMetricDataPoints subsequently sees — the delete side effect itself is
// covered separately by metric_retention_test.go.
func oracleListMetricDataPoints(t *testing.T, ctx context.Context, s *cloudwatchStore, namespace, metricName string, dimensions []Dimension, startTime, endTime time.Time) []*MetricDataPoint {
	t.Helper()
	metricDims := canonicalizeDimensions(dimensions)
	prefix := metricDataPrefix(namespace, metricName, dimensionsKey(metricDims))
	pairs, err := s.store.Scan(ctx, nsMetricData, prefix)
	if err != nil {
		t.Fatalf("oracle Scan: %v", err)
	}
	cutoff := s.metricDataRetentionCutoff()
	startNanos := startTime.UnixNano()
	endNanos := endTime.UnixNano()

	out := make([]*MetricDataPoint, 0, len(pairs))
	for _, kv := range pairs {
		var dp MetricDataPoint
		if err := json.Unmarshal([]byte(kv.Value), &dp); err != nil {
			continue
		}
		if dp.Timestamp.UTC().Before(cutoff) {
			continue
		}
		nanos := dp.Timestamp.UnixNano()
		if nanos < startNanos || nanos > endNanos {
			continue
		}
		out = append(out, &dp)
	}
	return out
}

// sortDataPointsByTimestamp orders points by timestamp (then Sum, to break
// ties between points sharing a timestamp deterministically) so two
// differently-ordered result sets can be compared point-by-point. Neither
// Scan nor ScanPage's ordering is part of the Store interface's documented
// contract for Scan (only ScanPage promises key order), so the property
// test must not assume a particular order out of either code path.
func sortDataPointsByTimestamp(points []*MetricDataPoint) {
	sort.Slice(points, func(i, j int) bool {
		if !points[i].Timestamp.Equal(points[j].Timestamp) {
			return points[i].Timestamp.Before(points[j].Timestamp)
		}
		return points[i].Sum < points[j].Sum
	})
}

// assertSameDataPoints compares two point sets field-by-field after sorting
// both into the same order, failing with a diagnostic identifying the
// property-test trial and window on mismatch.
func assertSameDataPoints(t *testing.T, label string, want, got []*MetricDataPoint) {
	t.Helper()
	sortDataPointsByTimestamp(want)
	sortDataPointsByTimestamp(got)
	if len(want) != len(got) {
		t.Fatalf("%s: got %d points, want %d\n got=%v\nwant=%v", label, len(got), len(want), summarizeDataPoints(got), summarizeDataPoints(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if !w.Timestamp.Equal(g.Timestamp) || w.Sum != g.Sum || w.Namespace != g.Namespace || w.MetricName != g.MetricName {
			t.Fatalf("%s: point %d mismatch\n got=%+v\nwant=%+v", label, i, g, w)
		}
	}
}

func summarizeDataPoints(points []*MetricDataPoint) []string {
	out := make([]string, len(points))
	for i, p := range points {
		out[i] = p.Timestamp.Format(time.RFC3339Nano)
	}
	return out
}

// ---- Window-equality property ---------------------------------------------

// TestListMetricDataPoints_WindowEqualityProperty seeds a random number of
// datapoints at random offsets (some inside, some outside retention, some
// inside/outside the query window) around a fixed, realistic base time —
// realistic matters here because cloudwatch:metricdata's key-range read
// relies on UnixNano() producing a fixed-width 19-digit decimal string
// (true for dates roughly 2001-2286; see metricDataPrefix's doc comment),
// which a mock clock's default Unix-epoch start would violate — then
// asserts the new range-read implementation returns exactly the same points
// the pre-A5 full-scan-and-filter oracle would.
func TestListMetricDataPoints_WindowEqualityProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20240501))
	base := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)

	const trials = 40
	for trial := 0; trial < trials; trial++ {
		store := state.NewMemoryStore()
		mock := clock.NewMock()
		mock.Set(base)
		s := newCloudwatchStore(store, mock)
		ctx := context.Background()

		namespace := "PropNS"
		metricName := "PropMetric"

		n := rng.Intn(150)
		for i := 0; i < n; i++ {
			// Spread points across roughly [-90min, +90min] around base, at
			// sub-second granularity, so some points land before the 1h
			// retention cutoff and some land outside whatever window this
			// trial picks below.
			offset := time.Duration(rng.Int63n(int64(180*time.Minute))) - 90*time.Minute
			ts := base.Add(offset).Add(time.Duration(rng.Int63n(1_000_000_000)))
			dp := freshDataPoint(namespace, metricName, ts, float64(i))
			if err := s.putMetricDataPoint(ctx, dp); err != nil {
				t.Fatalf("trial %d: putMetricDataPoint: %v", trial, err)
			}
		}

		// Random window, independent of the point spread above — sometimes
		// wider, sometimes narrower, sometimes not overlapping at all.
		winStart := base.Add(time.Duration(rng.Int63n(int64(180*time.Minute))) - 90*time.Minute)
		winLen := time.Duration(rng.Int63n(int64(120 * time.Minute)))
		winEnd := winStart.Add(winLen)

		label := fmt.Sprintf("trial=%d n=%d window=[%s,%s]", trial, n,
			winStart.Format(time.RFC3339Nano), winEnd.Format(time.RFC3339Nano))

		want := oracleListMetricDataPoints(t, ctx, s, namespace, metricName, nil, winStart, winEnd)
		got, err := s.listMetricDataPoints(ctx, namespace, metricName, nil, winStart, winEnd)
		if err != nil {
			t.Fatalf("%s: listMetricDataPoints: %v", label, err)
		}

		assertSameDataPoints(t, label, want, got)
	}
}

// ---- Boundary exactness -----------------------------------------------------

// TestListMetricDataPoints_BoundaryExactness pins the current inclusive-both-
// ends semantics: a point at exactly startTime or exactly endTime must be
// returned, and a point 1ns outside either edge must not be — matching
// aggregateMetricBuckets' `ts.Before(startTime) || ts.After(endTime)` filter
// exactly, which is what actually governs GetMetricStatistics/GetMetricData
// output today. The range-read implementation must reproduce this, not
// redefine it.
func TestListMetricDataPoints_BoundaryExactness(t *testing.T) {
	store := state.NewMemoryStore()
	mock := clock.NewMock()
	base := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)
	mock.Set(base)
	s := newCloudwatchStore(store, mock)
	ctx := context.Background()

	namespace, metricName := "BoundaryNS", "BoundaryMetric"
	winStart := base.Add(-2 * time.Minute)
	winEnd := base.Add(2 * time.Minute)

	atStart := freshDataPoint(namespace, metricName, winStart, 1)
	atEnd := freshDataPoint(namespace, metricName, winEnd, 2)
	beforeStart := freshDataPoint(namespace, metricName, winStart.Add(-time.Nanosecond), 3)
	afterEnd := freshDataPoint(namespace, metricName, winEnd.Add(time.Nanosecond), 4)
	inside := freshDataPoint(namespace, metricName, base, 5)

	for _, dp := range []*MetricDataPoint{atStart, atEnd, beforeStart, afterEnd, inside} {
		if err := s.putMetricDataPoint(ctx, dp); err != nil {
			t.Fatalf("putMetricDataPoint: %v", err)
		}
	}

	got, err := s.listMetricDataPoints(ctx, namespace, metricName, nil, winStart, winEnd)
	if err != nil {
		t.Fatalf("listMetricDataPoints: %v", err)
	}

	gotSums := map[float64]bool{}
	for _, dp := range got {
		gotSums[dp.Sum] = true
	}

	for _, want := range []float64{1, 2, 5} {
		if !gotSums[want] {
			t.Errorf("expected in-window point with Sum=%v to be included, got sums=%v", want, gotSums)
		}
	}
	for _, unwanted := range []float64{3, 4} {
		if gotSums[unwanted] {
			t.Errorf("expected out-of-window point with Sum=%v to be excluded, got sums=%v", unwanted, gotSums)
		}
	}
	if len(got) != 3 {
		t.Errorf("expected exactly 3 points in [start,end], got %d: %v", len(got), summarizeDataPoints(got))
	}
}
