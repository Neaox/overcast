//go:build !nosqlite

package cloudwatch

// Benchmark for storage-plan.md A5, split from metric_range_test.go because
// it needs a real HybridStore (SQLite-backed) and so carries the same
// !nosqlite build tag as metric_burst_bench_test.go — the property/boundary
// tests in metric_range_test.go only need MemoryStore and must still build
// and run under `-tags nosqlite`.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// benchmarkGetMetricStatisticsHybrid measures the full GetMetricStatistics
// handler (query-protocol form-encoded request, XML response) over a fixed
// 5-minute window, as retainedOutsideWindow historical points for the same
// metric accumulate outside that window but still inside the 1h retention
// period. Before A5, listMetricDataPoints scanned and JSON-decoded every one
// of those points on every call; after A5 the ScanPage range walk should
// never touch them, so ns/op and allocs/op should stay flat across the
// 0/2000/8000 variants (allocs/op is the signal — see AGENTS.md on
// benchmarking under concurrent agents; wall-clock ns/op is noisier).
//
// Preload writes rows via store.Set directly, mirroring
// metric_burst_bench_test.go's benchmarkPutMetricDataHybrid, so setup cost
// is linear in retainedOutsideWindow regardless of what the read path does.
func benchmarkGetMetricStatisticsHybrid(b *testing.B, retainedOutsideWindow int) {
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

	const namespace, metricName = "BenchNS", "BurstMetric"
	now := time.Now().UTC()
	windowStart := now.Add(-5 * time.Minute)
	windowEnd := now

	dims := dimensionsKey(canonicalizeDimensions(nil))
	prefix := metricDataPrefix(namespace, metricName, dims)

	preloadPoint := func(ts time.Time, value float64) {
		dp := &MetricDataPoint{
			Namespace: namespace, MetricName: metricName,
			Timestamp: ts, SampleCount: 1, Sum: value, Minimum: value, Maximum: value,
		}
		raw, err := json.Marshal(dp)
		if err != nil {
			b.Fatalf("marshal preload point: %v", err)
		}
		key := metricDataKeyForNanos(prefix, ts.UnixNano())
		if err := hybrid.Set(ctx, nsMetricData, key, string(raw)); err != nil {
			b.Fatalf("preload Set: %v", err)
		}
	}

	// Spread retainedOutsideWindow points across roughly [-55min, -5min] —
	// well within the 1h retention window, but strictly before windowStart,
	// so the range read must skip them entirely rather than decode-then-
	// discard them.
	if retainedOutsideWindow > 0 {
		spread := 50 * time.Minute
		step := spread / time.Duration(retainedOutsideWindow)
		if step <= 0 {
			step = time.Nanosecond
		}
		start := now.Add(-55 * time.Minute)
		for i := 0; i < retainedOutsideWindow; i++ {
			preloadPoint(start.Add(time.Duration(i)*step), float64(i))
		}
	}

	// A handful of points actually inside the 5-minute query window — flat
	// cost across variants, isolating what varies (retainedOutsideWindow).
	for i := 0; i < 5; i++ {
		preloadPoint(windowStart.Add(time.Duration(i)*time.Second), float64(1000+i))
	}

	if err := hybrid.Flush(ctx); err != nil {
		b.Fatalf("Flush preload: %v", err)
	}

	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, hybrid, zap.NewNop(), clock.New())
	b.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(stopCtx)
	})

	form := url.Values{
		"Namespace":           {namespace},
		"MetricName":          {metricName},
		"StartTime":           {windowStart.Format(time.RFC3339)},
		"EndTime":             {windowEnd.Format(time.RFC3339)},
		"Period":              {"60"},
		"Statistics.member.1": {"Average"},
	}
	body := form.Encode()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		svc.getMetricStatistics(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("GetMetricStatistics: status %d, body %s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkCloudWatch_GetMetricStatisticsHybrid_0Retained(b *testing.B) {
	benchmarkGetMetricStatisticsHybrid(b, 0)
}

func BenchmarkCloudWatch_GetMetricStatisticsHybrid_2000Retained(b *testing.B) {
	benchmarkGetMetricStatisticsHybrid(b, 2000)
}

func BenchmarkCloudWatch_GetMetricStatisticsHybrid_8000Retained(b *testing.B) {
	benchmarkGetMetricStatisticsHybrid(b, 8000)
}
