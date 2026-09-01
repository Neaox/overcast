package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/state"
)

// newBenchRecorder returns a Service over a real wall clock (not a mock —
// the flush/sweep tickers must not fire mid-benchmark and a mock clock never
// advances on its own, so there is nothing to gain from one here) backed by
// MemoryStore, so these benchmarks isolate the Observe hot path itself from
// any backend I/O (per AGENTS.md: Observe never touches state.Store).
func newBenchRecorder(b *testing.B) *Service {
	b.Helper()
	svc := NewRecorder(state.NewMemoryStore(), clock.New(), zap.NewNop())
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc
}

// BenchmarkObserve_SingleSeries sizes the term this package actually adds to
// every Lambda invocation: one Observe call against one hot, already-resident
// series (docs/plans/benchmark-the-shape-that-exposes-the-cost.md's rule —
// this is the steady-state shape, not a cold one-shot).
func BenchmarkObserve_SingleSeries(b *testing.B) {
	svc := newBenchRecorder(b)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "bench-fn"}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dims, Unit: "Count", Value: 1})
	}
}

// BenchmarkObserve_HighCardinality sizes Observe under many concurrently-hot
// series (one per function name), which is what exercises shard fan-out and
// the active-bucket map's growth rather than a single always-warm bucket.
func BenchmarkObserve_HighCardinality(b *testing.B) {
	svc := newBenchRecorder(b)
	ctx := context.Background()
	const cardinality = 500
	dimsFor := make([][]Dimension, cardinality)
	for i := range dimsFor {
		dimsFor[i] = []Dimension{{Name: "FunctionName", Value: fmt.Sprintf("bench-fn-%d", i)}}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dimsFor[i%cardinality], Unit: "Count", Value: 1})
	}
}

// BenchmarkObserve_Parallel sizes sharded-lock contention under concurrent
// invocations of a handful of distinct functions — the realistic burst shape
// a benchmark of a single always-serial series would hide.
func BenchmarkObserve_Parallel(b *testing.B) {
	svc := newBenchRecorder(b)
	ctx := context.Background()
	const functions = 8
	dimsFor := make([][]Dimension, functions)
	for i := range dimsFor {
		dimsFor[i] = []Dimension{{Name: "FunctionName", Value: fmt.Sprintf("parallel-fn-%d", i)}}
	}

	b.ReportAllocs()
	b.ResetTimer()
	var counter int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			counter++
			_ = svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dimsFor[counter%functions], Unit: "Count", Value: 1})
		}
	})
}

// BenchmarkQueryRange sizes the read side CloudWatch's GetMetricStatistics/
// GetMetricData/alarm-evaluation window read pays per call, against a series
// with a realistic one-hour history of persisted one-minute buckets plus the
// current, still-active one — the shape a real alarm evaluation window
// actually queries, not an empty or single-bucket series.
func BenchmarkQueryRange(b *testing.B) {
	svc := newBenchRecorder(b)
	ctx := context.Background()
	dims := []Dimension{{Name: "FunctionName", Value: "bench-fn"}}
	now := time.Now().UTC()

	// Seed 60 persisted one-minute buckets (a full hour of history) plus one
	// still-active (unflushed) bucket for the current minute.
	for m := 60; m >= 1; m-- {
		ts := now.Add(-time.Duration(m) * time.Minute)
		_ = svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Duration", Dimensions: dims, Unit: "Milliseconds", Value: 42, Timestamp: ts})
	}
	if err := svc.Flush(ctx); err != nil {
		b.Fatalf("seed flush: %v", err)
	}
	_ = svc.Observe(ctx, Observation{Namespace: "AWS/Lambda", Name: "Duration", Dimensions: dims, Unit: "Milliseconds", Value: 42, Timestamp: now})

	start := now.Add(-time.Hour)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.QueryRange(ctx, "AWS/Lambda", "Duration", dims, start, now); err != nil {
			b.Fatalf("QueryRange: %v", err)
		}
	}
}
