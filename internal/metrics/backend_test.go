package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// newTestBucketBackends returns one bucketBackend per implementation
// available in this build, mirroring
// internal/services/sqs/message_backend_test.go's newTestMessageBackends. The
// "sql" entry only appears when SQLite is compiled in; "wal" is always
// available (WALStore has no build tag gate).
func newTestBucketBackends(t *testing.T) map[string]bucketBackend {
	t.Helper()
	backends := map[string]bucketBackend{"memory": newMemBucketBackend()}

	walDir := t.TempDir()
	wal, err := state.NewWALStore(walDir, state.WALOptions{})
	if err != nil {
		t.Fatalf("NewWALStore: %v", err)
	}
	t.Cleanup(func() {
		if err := wal.Close(); err != nil {
			t.Logf("wal.Close: %v", err)
		}
	})
	walBackend := newBucketBackendFor(state.Unwrap(wal, "metrics"))
	if _, ok := walBackend.(*kvBucketBackend); !ok {
		t.Fatalf("expected kvBucketBackend for a WALStore-backed store, got %T", walBackend)
	}
	backends["wal"] = walBackend

	if !config.SQLiteSupported() {
		return backends
	}

	dir := t.TempDir()
	hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() {
		if err := hybrid.Close(); err != nil {
			t.Logf("hybrid.Close: %v", err)
		}
	})
	backendStore := state.Unwrap(hybrid, "metrics")
	sqlBackend := newBucketBackendFor(backendStore)
	if _, ok := sqlBackend.(*sqlBucketBackend); !ok {
		t.Fatalf("expected sqlBucketBackend for a SQLite-backed store, got %T", sqlBackend)
	}
	backends["sql"] = sqlBackend

	return backends
}

func TestBucketBackend_MemoryAndSQL_Parity(t *testing.T) {
	for name, b := range newTestBucketBackends(t) {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			id := seriesID("AWS/Lambda", "Invocations", []Dimension{{Name: "FunctionName", Value: "fn-a"}})
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			pb := persistedBucket{
				SeriesID: id,
				Identity: Metric{Namespace: "AWS/Lambda", Name: "Invocations",
					Dimensions: []Dimension{{Name: "FunctionName", Value: "fn-a"}}},
				ResolutionSec: resolutionSeconds,
				StartMs:       start.UnixMilli(),
				Count:         3, Sum: 3, Min: 1, Max: 1, Unit: "Count",
			}
			if err := b.upsertBuckets(ctx, []persistedBucket{pb}); err != nil {
				t.Fatalf("upsertBuckets: %v", err)
			}

			// Re-upserting the same (series,resolution,start) with a larger
			// total must replace, not add (activeBucket's contract).
			pb.Count, pb.Sum = 5, 5
			if err := b.upsertBuckets(ctx, []persistedBucket{pb}); err != nil {
				t.Fatalf("upsertBuckets (replace): %v", err)
			}

			got, err := b.rangeQuery(ctx, id, resolutionSeconds, start.UnixMilli(), start.Add(time.Minute).UnixMilli())
			if err != nil {
				t.Fatalf("rangeQuery: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 bucket, got %d", len(got))
			}
			if got[0].Count != 5 || got[0].Sum != 5 {
				t.Errorf("expected replaced totals count=5 sum=5, got count=%v sum=%v", got[0].Count, got[0].Sum)
			}
			if !got[0].Start.Equal(start) {
				t.Errorf("expected start %v, got %v", start, got[0].Start)
			}

			// Out-of-range query returns nothing.
			none, err := b.rangeQuery(ctx, id, resolutionSeconds, start.Add(time.Hour).UnixMilli(), start.Add(2*time.Hour).UnixMilli())
			if err != nil {
				t.Fatalf("rangeQuery (out of range): %v", err)
			}
			if len(none) != 0 {
				t.Errorf("expected no buckets out of range, got %d", len(none))
			}

			series, err := b.listSeries(ctx, "AWS/Lambda")
			if err != nil {
				t.Fatalf("listSeries: %v", err)
			}
			if len(series) != 1 || series[0].Name != "Invocations" {
				t.Fatalf("expected 1 Invocations series, got %+v", series)
			}
			if len(series[0].Dimensions) != 1 || series[0].Dimensions[0].Value != "fn-a" {
				t.Errorf("expected FunctionName=fn-a dimension round-trip, got %+v", series[0].Dimensions)
			}

			seriesOtherNS, err := b.listSeries(ctx, "AWS/SQS")
			if err != nil {
				t.Fatalf("listSeries (other namespace): %v", err)
			}
			if len(seriesOtherNS) != 0 {
				t.Errorf("expected no series for unrelated namespace, got %d", len(seriesOtherNS))
			}

			removed, err := b.deleteOlderThan(ctx, resolutionSeconds, start.Add(time.Minute).UnixMilli())
			if err != nil {
				t.Fatalf("deleteOlderThan: %v", err)
			}
			if removed != 1 {
				t.Errorf("expected to delete 1 expired bucket, got %d", removed)
			}
			postDelete, err := b.rangeQuery(ctx, id, resolutionSeconds, start.UnixMilli(), start.Add(time.Minute).UnixMilli())
			if err != nil {
				t.Fatalf("rangeQuery (post delete): %v", err)
			}
			if len(postDelete) != 0 {
				t.Errorf("expected bucket to be gone after retention delete, got %d", len(postDelete))
			}
		})
	}
}
