//go:build !nosqlite

package state_test

// Regression coverage for storage-pressure-handling item 0: a paced,
// realistic write rate must not starve unrelated point reads on HybridStore.
//
// Background: the reported symptom was hybrid TierCached point Gets failing
// with "context deadline exceeded" (exhausting hybridSQLiteReadRetryTimeout)
// under a background writer doing only ~2,000 Sets/sec — an order of
// magnitude below what should trouble a WAL-mode SQLite database, where
// readers never block on a writer. Root cause (see
// TestHybridStore_JournalModeIsWAL in hybrid_journalmode_internal_test.go):
// the writer and read-pool DSNs built their pragmas as bare
// "_journal_mode=WAL&_synchronous=NORMAL" query parameters, a spelling
// modernc.org/sqlite (this project's driver) silently does not recognize —
// so every persistent backend had actually been running in the default
// rollback-journal mode the whole time. Rollback-journal commits take a
// brief process-wide EXCLUSIVE file lock that blocks concurrent readers;
// WAL mode removes that contention entirely. Fixed in sqlite.go's
// newSQLiteStoreFile and hybrid.go's openReadPool by switching to the
// driver's actual "_pragma=name(value)" DSN form.
//
// Safety: the background writer here is paced with a 500us ticker
// (~2,000 Sets/sec) for a bounded duration — never an unpaced loop. An
// earlier unpaced benchmark saturated the store at >100k Sets/sec and OOM'd
// this machine's Docker VM; do not remove the pacing when editing this test.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// pressureTestPayload returns a ~1KB JSON value, roughly modeling the shape
// of per-request metadata records (s3:objects, kinesis:records) that take
// HybridStore's TierCached read/write path.
func pressureTestPayload(i int) string {
	b, _ := json.Marshal(map[string]any{
		"key": fmt.Sprintf("obj-%08d", i), "size": 1024, "etag": "abc123",
		"body": fmt.Sprintf("%0512d", i), // padding toward ~1KB
	})
	return string(b)
}

// TestHybridStore_PacedWriteLoadDoesNotStarveReads is the regression pin for
// item 0: with the WAL fix in place, point Gets against one TierCached
// namespace must complete quickly and without error while a background
// writer sustains a realistic ~2,000 Sets/sec against a different TierCached
// namespace on the same database file.
//
// This asserts on the FRACTION of reads that are slow, not the single
// worst sample (max latency) — max is dominated by one-off tail events
// (a GC pause, a scheduler hiccup, one unlucky neighbor process on shared
// CI hardware) that have nothing to do with storage pressure and make a
// max-based bound flaky regardless of how generous it is. Pre-fix,
// starvation was never a rare tail outlier: over a 12s run, 14 of 61
// completed Gets (23%) took >500ms, and total throughput collapsed to
// ~5-20 Gets in a 3s window (vs. hundreds to low thousands post-fix). A
// small slow-fraction ceiling (see maxSlowFraction below) plus the
// throughput floor catch that failure mode decisively while tolerating
// ordinary machine noise, including the occasional single slow sample —
// this test observed a lone 1.4s outlier out of ~2,000 otherwise-fast Gets
// on a heavily loaded shared box, with zero errors, which a max-based bound
// would (and did) flag as a false regression. See docs/performance.md "Data
// dir placement" for why bind mounts are excluded from this kind of
// assertion — this test uses t.TempDir(), the container's native
// filesystem.
//
// Under `-race` the slow-fraction ceiling (and the minimum-throughput floor
// below) widen further: the race detector's per-memory-access
// instrumentation adds a large, fairly constant multiplier to every
// operation. That's a `-race` tax, not storage-pressure starvation, so it
// needs its own tolerance rather than either flaking `-race` runs or hiding
// a real regression behind a ceiling loose enough to tolerate `-race`
// unconditionally.
func TestHybridStore_PacedWriteLoadDoesNotStarveReads(t *testing.T) {
	s, err := state.NewHybridStoreWithOptions(t.TempDir(), state.HybridOptions{FlushInterval: 200 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	const rows = 10_000
	const readNS = "s3:objects"       // TierCached
	const writeNS = "kinesis:records" // TierCached, unrelated to readNS
	key := func(i int) string { return fmt.Sprintf("us-east-1/bench-bucket/obj-%08d", i) }

	for i := 0; i < rows; i++ {
		if err := s.Set(ctx, readNS, key(i), pressureTestPayload(i)); err != nil {
			t.Fatalf("preload Set %d: %v", i, err)
		}
	}
	if err := state.Flush(ctx, s); err != nil {
		t.Fatalf("preload Flush: %v", err)
	}

	// Paced background writer: ~2,000 Sets/sec (500us ticker), bounded by
	// stop — never an unpaced loop (see file-level safety comment).
	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		tick := time.NewTicker(500 * time.Microsecond)
		defer tick.Stop()
		payload := pressureTestPayload(0)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			_ = s.Set(ctx, writeNS, fmt.Sprintf("us-east-1/stream/shard-0/%019d", i), payload)
		}
	}()

	const readDuration = 3 * time.Second
	const slowThreshold = 300 * time.Millisecond // "slow" for a single read; see maxSlowFraction below
	maxSlowFraction := 0.05
	minGets := 100
	if raceDetectorEnabled {
		maxSlowFraction = 0.20
		minGets = 20
	}
	deadline := time.Now().Add(readDuration)
	var gets, errs, slow int
	var maxLatency time.Duration
	for i := 0; time.Now().Before(deadline); i++ {
		k := key((i * 7919) % rows)
		start := time.Now()
		_, found, getErr := s.Get(ctx, readNS, k)
		elapsed := time.Since(start)
		if elapsed > maxLatency {
			maxLatency = elapsed
		}
		if elapsed > slowThreshold {
			slow++
		}
		gets++
		if getErr != nil {
			errs++
			if errs <= 5 {
				t.Logf("Get error #%d after %v: %v", errs, elapsed, getErr)
			}
			continue
		}
		if !found {
			t.Fatalf("Get(%q): not found", k)
		}
	}

	close(stop)
	<-writerDone

	slowFraction := float64(slow) / float64(gets)
	t.Logf("gets=%d errs=%d slow(>%v)=%d (%.1f%%, ceiling %.0f%%) maxLatency=%v",
		gets, errs, slowThreshold, slow, slowFraction*100, maxSlowFraction*100, maxLatency)
	if errs > 0 {
		t.Errorf("observed %d/%d Get errors under paced write load (~2k/sec) — read starvation regressed", errs, gets)
	}
	if gets > 0 && slowFraction > maxSlowFraction {
		t.Errorf("%.1f%% of Gets exceeded %v under paced write load (ceiling %.0f%%) — read starvation regressed", slowFraction*100, slowThreshold, maxSlowFraction*100)
	}
	if gets < minGets {
		t.Errorf("only completed %d Gets in %v (want >= %d) — throughput collapsed under paced write load", gets, readDuration, minGets)
	}
}

// BenchmarkHybridStore_TierCachedGet_WithPacedWriteLoad is the ongoing
// measurement counterpart to the regression test above: ns/op and
// allocs/op for a point Get while the same paced ~2,000 Sets/sec background
// load runs. Run with `-benchtime` bounded (e.g. -benchtime=3s) — b.N left
// uncapped would let Go's benchmark calibration ramp iteration counts far
// beyond what the paced writer models; that is a benchmark-harness risk, not
// a store bug, so this benchmark is for manual/CI-timed runs, not part of
// the default `go test` suite.
func BenchmarkHybridStore_TierCachedGet_WithPacedWriteLoad(b *testing.B) {
	s, err := state.NewHybridStoreWithOptions(b.TempDir(), state.HybridOptions{FlushInterval: 200 * time.Millisecond}, nil)
	if err != nil {
		b.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		b.Fatalf("WaitReady: %v", err)
	}

	const rows = 10_000
	const readNS = "s3:objects"
	key := func(i int) string { return fmt.Sprintf("us-east-1/bench-bucket/obj-%08d", i) }
	for i := 0; i < rows; i++ {
		if err := s.Set(ctx, readNS, key(i), pressureTestPayload(i)); err != nil {
			b.Fatalf("preload Set %d: %v", i, err)
		}
	}
	if err := state.Flush(ctx, s); err != nil {
		b.Fatalf("preload Flush: %v", err)
	}

	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		tick := time.NewTicker(500 * time.Microsecond)
		defer tick.Stop()
		payload := pressureTestPayload(0)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			_ = s.Set(ctx, "kinesis:records", fmt.Sprintf("us-east-1/stream/shard-0/%019d", i), payload)
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := key((i * 7919) % rows)
		if _, found, err := s.Get(ctx, readNS, k); err != nil || !found {
			b.Fatalf("Get(%q): found=%v err=%v", k, found, err)
		}
	}
	b.StopTimer()

	close(stop)
	<-writerDone
}
