package s3

// Benchmark for storage-access-plan.md item A6 — ListObjectsV2 should page
// through the store instead of materializing the whole bucket
// (handler_bucket.go's buildListPage, backed by s3Store.listObjectsPage /
// ScanPage). At a fixed MaxKeys, allocs/op should stay flat as the bucket
// grows, since each request now only ever holds internalListPageChunk
// objects in memory at a time rather than the full bucket.
//
// Preload writes objects directly via store.Set (bypassing HTTP and the
// on-disk body path), mirroring the convention in sqs/receive_bench_test.go
// and cloudwatch/metric_range_bench_test.go, so setup cost stays linear and
// out of the measured loop.
//
// Measurement conventions per docs/plans/storage-test-plan.md: allocs/op is
// the deterministic signal; wall time (ns/op) is machine/load-dependent,
// especially under concurrent agents sharing this machine — treat ns/op
// here as indicative only.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// benchmarkListObjectsV2Flat preloads bucketSize objects under a flat
// (delimiter-free) keyspace and repeatedly lists the first page at a fixed
// MaxKeys=100.
func benchmarkListObjectsV2Flat(b *testing.B, bucketSize int) {
	b.Helper()
	store := state.NewMemoryStore()
	ctx := context.Background()
	const bucket = "bench-bucket"

	now := time.Now().UTC()
	for i := 0; i < bucketSize; i++ {
		obj := &Object{
			Bucket:        bucket,
			Key:           fmt.Sprintf("key-%08d.txt", i),
			ContentType:   "text/plain",
			ContentLength: 3,
			ETag:          `"deadbeefdeadbeefdeadbeefdeadbeef"`,
			LastModified:  now,
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			b.Fatalf("marshal preload object: %v", err)
		}
		if err := store.Set(ctx, nsObjects, objectStoreKey(bucket, obj.Key), string(raw)); err != nil {
			b.Fatalf("preload Set: %v", err)
		}
	}
	bkt := &Bucket{Name: bucket, Region: "us-east-1", CreationDate: now}
	rawBkt, err := json.Marshal(bkt)
	if err != nil {
		b.Fatalf("marshal preload bucket: %v", err)
	}
	if err := store.Set(ctx, nsBuckets, bucket, string(rawBkt)); err != nil {
		b.Fatalf("preload Set bucket: %v", err)
	}

	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", DataDir: b.TempDir()}
	log := serviceutil.NewServiceLogger(zap.NewNop(), "s3")
	h := newHandler(cfg, store, log, clock.New(), events.NewBus())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/"+bucket+"?list-type=2&max-keys=100", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("bucket", bucket)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.ListObjectsV2(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("ListObjectsV2: status %d, body %s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkListObjectsV2_MaxKeys100_Bucket100(b *testing.B) {
	benchmarkListObjectsV2Flat(b, 100)
}

func BenchmarkListObjectsV2_MaxKeys100_Bucket1000(b *testing.B) {
	benchmarkListObjectsV2Flat(b, 1000)
}

func BenchmarkListObjectsV2_MaxKeys100_Bucket10000(b *testing.B) {
	benchmarkListObjectsV2Flat(b, 10000)
}
