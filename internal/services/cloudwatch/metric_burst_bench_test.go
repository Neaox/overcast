//go:build !nosqlite

package cloudwatch

// Benchmarks the storage cost of a PutMetricData burst in hybrid mode as the
// number of already-retained points for the *same* metric grows
// (storage-plan.md 2.1 conventions: what's measured is putMetricDataPoint
// against a real HybridStore on a tmpfs-backed TempDir, with preExisting
// fresh points already flushed to SQLite; ns/op is dominated by whatever the
// write path does per put — before the inline-prune removal that included a
// TierCached Scan + JSON decode of every retained point, so ns/op grew
// linearly with preExisting; after it, ns/op should be flat).
//
// The preload writes rows via store.Set directly (same key layout as
// putMetricDataPoint) rather than through putMetricDataPoint itself, so
// setup cost stays linear regardless of what the write path does.

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

func benchmarkPutMetricDataHybrid(b *testing.B, preExisting int) {
	b.Helper()
	dir := b.TempDir()
	hybrid, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		b.Fatalf("NewHybridStore: %v", err)
	}
	b.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	if err := hybrid.WaitReady(ctx); err != nil {
		b.Fatalf("WaitReady: %v", err)
	}

	s := newCloudwatchStore(hybrid, clock.New())
	base := time.Now().UTC()
	dims := dimensionsKey(canonicalizeDimensions(nil))
	for i := 0; i < preExisting; i++ {
		ts := base.Add(time.Duration(i) * time.Millisecond)
		dp := &MetricDataPoint{
			Namespace: "BenchNS", MetricName: "BurstMetric",
			Timestamp: ts, SampleCount: 1, Sum: 1,
		}
		raw, err := json.Marshal(dp)
		if err != nil {
			b.Fatalf("marshal preload point: %v", err)
		}
		key := "BenchNS/BurstMetric/" + dims + "/" + strconv.FormatInt(ts.UnixNano(), 10)
		if err := hybrid.Set(ctx, nsMetricData, key, string(raw)); err != nil {
			b.Fatalf("preload Set: %v", err)
		}
	}
	if err := hybrid.Flush(ctx); err != nil {
		b.Fatalf("Flush preload: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dp := &MetricDataPoint{
			Namespace: "BenchNS", MetricName: "BurstMetric",
			Timestamp:   base.Add(time.Duration(preExisting+i) * time.Millisecond),
			SampleCount: 1, Sum: 1,
		}
		if err := s.putMetricDataPoint(ctx, dp); err != nil {
			b.Fatalf("putMetricDataPoint: %v", err)
		}
	}
}

func BenchmarkCloudWatch_PutMetricDataHybrid_0Retained(b *testing.B) {
	benchmarkPutMetricDataHybrid(b, 0)
}

func BenchmarkCloudWatch_PutMetricDataHybrid_2000Retained(b *testing.B) {
	benchmarkPutMetricDataHybrid(b, 2000)
}

func BenchmarkCloudWatch_PutMetricDataHybrid_8000Retained(b *testing.B) {
	benchmarkPutMetricDataHybrid(b, 8000)
}
