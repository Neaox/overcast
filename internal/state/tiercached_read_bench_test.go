//go:build !nosqlite

package state_test

// Qualifying benchmark for docs/plans/storage-plan.md item 3.3 (LRU tier for
// TierCached namespaces — "do not implement without a benchmark").
//
// The gate question is ATTRIBUTION, not raw speed: a TierCached read as a
// service sees it costs (SQLite point-SELECT round trip) + (JSON unmarshal in
// the service's store.go). An LRU inside state.Store can only remove the
// SQLite round trip — the unmarshal happens per read regardless, because the
// store's contract returns the raw string. So:
//
//   - If Get round-trip cost >> Unmarshal cost  → an LRU would move the
//     number; the gate TRIPS.
//   - If Unmarshal cost is comparable or larger → an LRU can at best halve a
//     cost that is already micro-scale, and the invalidation complexity
//     (dirty-overlay precedence, budget knob, eviction) isn't paid for;
//     the item CLOSES.
//
// Post-storage-access-plan reality check (why this ran only after those
// waves): the highest-volume namespaces the 3.3 row originally listed no
// longer take this path at all — sqs:messages and logs:events moved to
// dedicated tables, and kinesis:records / cloudwatch:metricdata /
// s3:objects *listings* became bounded ScanPage range reads, which a
// point-Get LRU does not serve. What remains on the TierCached point-Get
// path is per-request metadata: s3:objects Get (GetObject/HeadObject),
// sqs:dedup (one Get per FIFO send), lambda counters. The benchmark
// therefore models that shape: point Gets of 0.5–5 KB JSON values from
// namespaces of 10k–100k rows ("what a busy local CDK/SDK session actually
// produces" per the plan).
//
// Measurement conventions per docs/plans/storage-test-plan.md: allocs/op and
// B/op are the deterministic signals; wall time is machine/load-dependent.
// Record results + conditions in storage-plan.md §"Phase 3 — what's left"
// (3.3) and storage-test-plan.md §T4.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// benchObjectMeta mirrors the shape and field mix of the per-request
// metadata records that still take the TierCached point-Get path (an
// s3:objects record is the model: ~10 mixed scalar fields plus a map).
type benchObjectMeta struct {
	Key          string            `json:"key"`
	Bucket       string            `json:"bucket"`
	ETag         string            `json:"etag"`
	Size         int64             `json:"size"`
	StorageClass string            `json:"storage_class"`
	ContentType  string            `json:"content_type"`
	LastModified time.Time         `json:"last_modified"`
	VersionID    string            `json:"version_id"`
	Metadata     map[string]string `json:"metadata"`
	Body         string            `json:"body,omitempty"` // padding to reach the target payload size
}

// benchPayload builds one marshaled record padded to approximately
// targetBytes (the 0.5–5 KB band the plan names).
func benchPayload(b *testing.B, i, targetBytes int) string {
	b.Helper()
	m := benchObjectMeta{
		Key:          fmt.Sprintf("prefix/dir-%03d/object-%08d.dat", i%97, i),
		Bucket:       "bench-bucket",
		ETag:         "9b2cf535f27731c974343645a3985328",
		Size:         int64(targetBytes),
		StorageClass: "STANDARD",
		ContentType:  "application/octet-stream",
		LastModified: time.Unix(1750000000+int64(i), 0).UTC(),
		VersionID:    fmt.Sprintf("v-%08d", i),
		Metadata:     map[string]string{"x-amz-meta-owner": "bench", "x-amz-meta-run": "3.3"},
	}
	raw, err := json.Marshal(&m)
	if err != nil {
		b.Fatalf("marshal probe: %v", err)
	}
	if pad := targetBytes - len(raw); pad > 0 {
		filler := make([]byte, pad)
		for j := range filler {
			filler[j] = 'a' + byte(j%23)
		}
		m.Body = string(filler)
	}
	raw, err = json.Marshal(&m)
	if err != nil {
		b.Fatalf("marshal payload: %v", err)
	}
	return string(raw)
}

const benchTierCachedNS = "s3:objects" // a real TierCached namespace (tier.go)

func benchKey(i int) string {
	return fmt.Sprintf("us-east-1/bench-bucket/prefix/dir-%03d/object-%08d.dat", i%97, i)
}

// newPreloadedHybrid builds a HybridStore with rows preloaded rows of
// ~valueBytes each in the TierCached namespace, flushed to SQLite so
// measured Gets take the SQLite path, not the pending overlay.
func newPreloadedHybrid(b *testing.B, rows, valueBytes int) state.Store {
	b.Helper()
	return newPreloadedHybridFlush(b, rows, valueBytes, time.Hour)
}

// newPreloadedHybridFlush is newPreloadedHybrid with an explicit flush
// interval. The quiet read benchmarks use time.Hour (no timer flush can
// intrude on the measurement); the WithWriteLoad benchmarks use a
// production-shaped interval instead — with an hour-long timer, sustained
// ingest only ever flushes via size triggers, ballooning the pending
// structures into a shape no real deployment produces.
func newPreloadedHybridFlush(b *testing.B, rows, valueBytes int, flushInterval time.Duration) state.Store {
	b.Helper()
	s, err := state.NewHybridStore(b.TempDir(), flushInterval)
	if err != nil {
		b.Fatalf("NewHybridStore: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		b.Fatalf("WaitReady: %v", err)
	}
	for i := 0; i < rows; i++ {
		if err := s.Set(ctx, benchTierCachedNS, benchKey(i), benchPayload(b, i, valueBytes)); err != nil {
			b.Fatalf("preload Set: %v", err)
		}
	}
	if err := state.Flush(ctx, s); err != nil {
		b.Fatalf("flush preload: %v", err)
	}
	return s
}

// benchmarkTierCachedGet measures the end-to-end store.Get — the exact cost
// an LRU tier would (partially) remove.
func benchmarkTierCachedGet(b *testing.B, rows, valueBytes int) {
	b.Helper()
	s := newPreloadedHybrid(b, rows, valueBytes)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Stride through the keyspace so successive reads don't hammer one
		// hot SQLite page; the multiplier is coprime with typical row counts.
		key := benchKey((i * 7919) % rows)
		v, found, err := s.Get(ctx, benchTierCachedNS, key)
		if err != nil || !found || len(v) == 0 {
			b.Fatalf("Get(%q): found=%v err=%v", key, found, err)
		}
	}
}

func BenchmarkTierCachedGet_Hybrid_10kRows_512B(b *testing.B) { benchmarkTierCachedGet(b, 10_000, 512) }
func BenchmarkTierCachedGet_Hybrid_10kRows_5KB(b *testing.B) {
	benchmarkTierCachedGet(b, 10_000, 5*1024)
}
func BenchmarkTierCachedGet_Hybrid_100kRows_512B(b *testing.B) {
	benchmarkTierCachedGet(b, 100_000, 512)
}
func BenchmarkTierCachedGet_Hybrid_100kRows_5KB(b *testing.B) {
	benchmarkTierCachedGet(b, 100_000, 5*1024)
}

// benchmarkUnmarshalOnly is the attribution twin: json.Unmarshal alone on
// exactly the payloads the Get benchmarks serve. This is the cost an LRU
// tier CANNOT remove.
func benchmarkUnmarshalOnly(b *testing.B, valueBytes int) {
	b.Helper()
	payloads := make([]string, 1024)
	for i := range payloads {
		payloads[i] = benchPayload(b, i, valueBytes)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var m benchObjectMeta
		if err := json.Unmarshal([]byte(payloads[i%len(payloads)]), &m); err != nil {
			b.Fatalf("unmarshal: %v", err)
		}
	}
}

func BenchmarkTierCachedUnmarshalOnly_512B(b *testing.B) { benchmarkUnmarshalOnly(b, 512) }
func BenchmarkTierCachedUnmarshalOnly_5KB(b *testing.B)  { benchmarkUnmarshalOnly(b, 5*1024) }

// benchmarkTierCachedGetWithWriteLoad is the "realistic mixed read/write
// shape" variant the plan asks for: point Gets while a background writer
// keeps dirtying an unrelated TierCached namespace (steady ingest — the
// post-wave dedicated read pool should keep reads from queuing behind
// flush transactions; this measures whether that holds on the point-Get
// path).
func benchmarkTierCachedGetWithWriteLoad(b *testing.B, rows, valueBytes int) {
	b.Helper()
	s := newPreloadedHybridFlush(b, rows, valueBytes, 200*time.Millisecond)
	ctx := context.Background()

	// Paced writer: ~2 000 writes/sec — a busy-but-realistic local ingest
	// rate (an unpaced loop here saturates the store at >100k Set/s, a
	// regime no emulator workload produces; measured consequences of that
	// saturation regime are recorded in the 3.3 verdict note: reads
	// eventually fail with an internal read timeout rather than queuing
	// forever).
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		payload := benchPayload(b, 0, valueBytes)
		tick := time.NewTicker(500 * time.Microsecond)
		defer tick.Stop()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			_ = s.Set(ctx, "kinesis:records", fmt.Sprintf("us-east-1/bench-stream/shard-0/%019d", i), payload)
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := benchKey((i * 7919) % rows)
		v, found, err := s.Get(ctx, benchTierCachedNS, key)
		if err != nil || !found || len(v) == 0 {
			b.Fatalf("Get(%q): found=%v err=%v", key, found, err)
		}
	}
	b.StopTimer()
	close(stop)
	<-done
}

func BenchmarkTierCachedGet_WithWriteLoad_10kRows_512B(b *testing.B) {
	benchmarkTierCachedGetWithWriteLoad(b, 10_000, 512)
}
func BenchmarkTierCachedGet_WithWriteLoad_10kRows_5KB(b *testing.B) {
	benchmarkTierCachedGetWithWriteLoad(b, 10_000, 5*1024)
}
