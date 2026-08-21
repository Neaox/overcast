//go:build !nosqlite

package state

// Baseline benchmarks for HybridStore, captured ahead of the Phase 2 storage
// work (docs/plans/storage-plan.md item 2.1) so the CloudWatch Logs table rewrite
// (2.3) and any future HybridStore changes have a measured "before" to diff
// against. These are relative/indicative numbers, not absolute production
// numbers — see docs/dev/performance.md "Documenting performance claims".
//
// What is measured:
//   - Set/Get throughput for a TierHot namespace ("sqs:queues", served from
//     the in-memory layer once seeded) and a TierCached namespace
//     ("sqs:messages", read straight from SQLite through the 1.2 read pool,
//     overlaid with any not-yet-flushed writes).
//   - Get latency for a TierCached namespace while a background goroutine
//     continuously writes and calls Flush, so a flush transaction is
//     perpetually in flight on the writer connection — this is precisely the
//     scenario the 1.2 read-pool split (a dedicated read-only *sql.DB,
//     hybrid.go's readDB()) was meant to keep off the critical path for
//     readers. Compare against a build with the read pool disabled (or a
//     pre-1.2 checkout) to see the effect in numbers instead of the
//     qualitative description already in hybrid.go's comments.
//   - Cold-start hydration time (NewHybridStoreWithOptions -> seedFromSQLite
//     completion, polled via the unexported isLoaded() since HybridStore does
//     not currently expose a public "seed complete" signal — WaitReady only
//     covers SQLite open+migrate, not the TierHot memory seed) for a small vs.
//     a larger pre-seeded TierHot dataset. This file lives in package `state`
//     (not `state_test`, unlike memory_bench_test.go) specifically so it can
//     reach isLoaded(); every other benchmark here only needs the exported
//     Store contract and could run from state_test just as well.
//
// How it is measured: `go test -run=^$ -bench=. -benchmem ./internal/state/...`.
//
// Environment: run in the project's devcontainer (Debian-based image) under
// Docker Desktop on Windows 11 / WSL2, not bare metal — absolute ns/op and
// B/op numbers will differ on other hosts/hardware and especially on a
// bind-mounted /data (see docs/performance.md "Data dir placement"); these
// benchmarks use b.TempDir(), which is the container's native filesystem, not
// a host bind mount, so they do not exercise that specific tax. Treat the
// numbers as indicative and relative to each other (e.g. TierHot vs
// TierCached, small vs large dataset), not as absolute production latencies.

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// newBenchHybridStore returns a HybridStore over a fresh temp dir with a long
// flush interval, so timer-driven background flushes don't compete with the
// benchmark loop unless the benchmark explicitly drives flushing itself.
func newBenchHybridStore(b *testing.B) *HybridStore {
	b.Helper()
	s, err := NewHybridStoreWithOptions(b.TempDir(), HybridOptions{FlushInterval: 5 * time.Second}, nil)
	if err != nil {
		b.Fatalf("NewHybridStoreWithOptions: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s
}

// ---- Sustained write throughput --------------------------------------------

func BenchmarkHybridStore_Set_TierHot(b *testing.B) {
	s := newBenchHybridStore(b)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("queue-%d", i)
		value := fmt.Sprintf(`{"name":%q,"url":"http://local/%s"}`, key, key)
		if err := s.Set(ctx, "sqs:queues", key, value); err != nil {
			b.Fatalf("Set: %v", err)
		}
	}
}

func BenchmarkHybridStore_Set_TierCached(b *testing.B) {
	s := newBenchHybridStore(b)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("my-queue/msg-%d", i)
		if err := s.Set(ctx, "sqs:messages", key, `{"body":"hello world","attributes":{}}`); err != nil {
			b.Fatalf("Set: %v", err)
		}
	}
}

// ---- Read throughput --------------------------------------------------------

func BenchmarkHybridStore_Get_TierHot(b *testing.B) {
	s := newBenchHybridStore(b)
	ctx := context.Background()
	const n = 1000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("queue-%d", i)
		if err := s.Set(ctx, "sqs:queues", key, fmt.Sprintf(`{"name":%q}`, key)); err != nil {
			b.Fatalf("seed Set: %v", err)
		}
	}
	// Let the initial (empty-DB) seed finish so Get serves from memory rather
	// than the SQLite fallback path used before seeding completes.
	for !s.isLoaded() {
		time.Sleep(100 * time.Microsecond)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("queue-%d", i%n)
		if _, _, err := s.Get(ctx, "sqs:queues", key); err != nil {
			b.Fatalf("Get: %v", err)
		}
	}
}

func BenchmarkHybridStore_Get_TierCached(b *testing.B) {
	s := newBenchHybridStore(b)
	ctx := context.Background()
	const n = 1000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("my-queue/msg-%d", i)
		if err := s.Set(ctx, "sqs:messages", key, `{"body":"hello world","attributes":{}}`); err != nil {
			b.Fatalf("seed Set: %v", err)
		}
	}
	// Push everything to SQLite so reads exercise the 1.2 read pool
	// (sqliteGet) instead of being served entirely from the pending overlay.
	if err := s.Flush(ctx); err != nil {
		b.Fatalf("Flush: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("my-queue/msg-%d", i%n)
		if _, _, err := s.Get(ctx, "sqs:messages", key); err != nil {
			b.Fatalf("Get: %v", err)
		}
	}
}

// ---- Mixed read/write during an in-flight flush (1.2 read-pool scenario) --

// BenchmarkHybridStore_MixedReadWriteDuringFlush_TierCached measures
// TierCached Get latency while a background goroutine continuously writes
// and synchronously Flushes, keeping a flush transaction perpetually
// in-flight on the writer connection. This is the exact scenario 1.2's
// dedicated read-only connection pool (hybrid.go readDB()) was added for:
// before that change, all TierCached reads shared the single writer
// connection and queued behind flush transactions.
func BenchmarkHybridStore_MixedReadWriteDuringFlush_TierCached(b *testing.B) {
	s := newBenchHybridStore(b)
	ctx := context.Background()
	const n = 2000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("my-queue/msg-%d", i)
		if err := s.Set(ctx, "sqs:messages", key, `{"body":"hello world","attributes":{}}`); err != nil {
			b.Fatalf("seed Set: %v", err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		b.Fatalf("Flush: %v", err)
	}

	stop := make(chan struct{})
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			key := fmt.Sprintf("my-queue/burst-%d", i)
			i++
			if err := s.Set(ctx, "sqs:messages", key, `{"body":"burst write","attributes":{}}`); err != nil {
				return
			}
			// Flush synchronously so a writer-connection transaction is
			// in-flight as continuously as possible for the duration of the
			// benchmark, instead of relying on timer cadence.
			_ = s.Flush(ctx)
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("my-queue/msg-%d", i%n)
			i++
			if _, _, err := s.Get(ctx, "sqs:messages", key); err != nil {
				b.Fatalf("Get: %v", err)
			}
		}
	})
	b.StopTimer()

	close(stop)
	writerWG.Wait()
}

// ---- Cold-start hydration time vs. seeded dataset size ---------------------

// seedHybridDataDirForBench populates a fresh data dir with n TierHot rows,
// flushing them to SQLite (via a normal Set + Close, which performs a final
// synchronous flush) and returns the dir for a later cold-start benchmark to
// reopen.
func seedHybridDataDirForBench(b *testing.B, n int) string {
	b.Helper()
	dir := b.TempDir()
	seed, err := NewHybridStoreWithOptions(dir, HybridOptions{FlushInterval: 20 * time.Millisecond}, nil)
	if err != nil {
		b.Fatalf("seed NewHybridStoreWithOptions: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("queue-%d", i)
		value := fmt.Sprintf(`{"name":%q,"url":"http://local/%s","attributes":{"VisibilityTimeout":"30"}}`, key, key)
		if err := seed.Set(ctx, "sqs:queues", key, value); err != nil {
			b.Fatalf("seed Set: %v", err)
		}
	}
	if err := seed.Close(); err != nil {
		b.Fatalf("seed Close: %v", err)
	}
	return dir
}

// benchmarkHybridColdStartHydration times NewHybridStoreWithOptions ->
// seedFromSQLite completion (polling isLoaded(), since WaitReady only
// signals SQLite open+migrate, not the TierHot memory seed) for a data dir
// pre-populated with n TierHot rows.
func benchmarkHybridColdStartHydration(b *testing.B, n int) {
	dir := seedHybridDataDirForBench(b, n)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s, err := NewHybridStoreWithOptions(dir, HybridOptions{FlushInterval: 10 * time.Second}, nil)
		if err != nil {
			b.Fatalf("NewHybridStoreWithOptions: %v", err)
		}
		for !s.isLoaded() {
			time.Sleep(100 * time.Microsecond)
		}
		if err := s.Close(); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}

func BenchmarkHybridStore_ColdStartHydration_1kEntries(b *testing.B) {
	benchmarkHybridColdStartHydration(b, 1_000)
}

func BenchmarkHybridStore_ColdStartHydration_50kEntries(b *testing.B) {
	benchmarkHybridColdStartHydration(b, 50_000)
}

// ---- Retained heap after Set + Flush for a TierCached namespace (#785) ----

// BenchmarkHybridStore_Set_TierCached_RetainedHeapAfterFlush measures the
// exact term #785 changed: how much heap a sustained run of cold-tier writes
// leaves resident AFTER those writes are durably flushed to SQLite, not just
// the per-op allocation cost during the Set loop (already covered by
// BenchmarkHybridStore_Set_TierCached above). Before the fix, HybridStore.Set
// wrote every value into s.mem unconditionally regardless of tier, so a
// TierCached namespace's data stayed pinned in memory for the rest of the
// process's life even once durably in SQLite and the pending overlay entry
// had been cleared — memory scaled with everything ever written, not the
// working set. After the fix, Set skips mem entirely for TierCached
// namespaces (mem is never the source of truth for them — every read either
// hits the pending overlay or SQLite), so once Flush clears the overlay
// there should be near-zero heap retained per entry.
//
// The value size (8 KiB) is deliberately large relative to the namespace's
// own bookkeeping overhead, per this project's benchmark-shape rule (a
// wrong-shape benchmark hid a 52x allocation regression before — see
// docs/plans/storage-plan.md's PR #938/#945 history): retained bytes must be
// dominated by the payload the fix stops duplicating, not by map/btree
// entry overhead, or an improvement here could be lost in the noise floor.
// Each entry's payload is also given DISTINCT content (index suffix on a
// repeated filler), not one shared string reused across every Set call —
// identical string content across iterations would let every dirty-overlay
// and (pre-fix) mem entry alias the same underlying byte array, hiding
// exactly the per-entry retention this benchmark exists to measure. This
// was caught empirically: an earlier version of this benchmark reused one
// value for all iterations and measured ~130 B retained per entry on the
// pre-fix code — the shared backing array, not the fix — instead of the
// ~8 KiB actually pinned per entry.
//
// Reported via b.ReportMetric as bytes retained per entry after Flush,
// alongside the standard -benchmem allocs/op and B/op for the Set loop
// itself (a live runtime.MemStats delta, not the testing.B allocation
// counters, since those measure gross bytes allocated during the timed
// loop, which includes memory that IS expected to be freed — retention is
// what this benchmark exists to isolate).
func BenchmarkHybridStore_Set_TierCached_RetainedHeapAfterFlush(b *testing.B) {
	s := newBenchHybridStore(b)
	ctx := context.Background()
	filler := strings.Repeat("x", 8<<10) // 8 KiB — deployment-package/S3-object-shaped payload

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("us-east-1/bucket/obj-%d", i)
		value := filler + fmt.Sprintf("-%d", i) // distinct content per entry, see doc comment
		if err := s.Set(ctx, "s3:objects", key, value); err != nil {
			b.Fatalf("Set: %v", err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		b.Fatalf("Flush: %v", err)
	}
	b.StopTimer()

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if b.N > 0 {
		retainedPerEntry := (int64(after.HeapAlloc) - int64(before.HeapAlloc)) / int64(b.N)
		b.ReportMetric(float64(retainedPerEntry), "B/retained-entry-after-flush")
	}
}
