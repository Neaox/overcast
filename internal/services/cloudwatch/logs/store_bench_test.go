package logs

// Benchmarks for the dedicated-table CloudWatch Logs event storage model
// (docs/plans/storage-plan.md Phase 2.3), re-run against the same shapes as the
// Phase 2.1 baseline that measured the OLD blob-per-stream design (see git
// history for this file's pre-2.3 version, and the PR description for the
// recorded before/after numbers). See docs/performance.md "Documenting
// performance claims" — treat everything below as relative/indicative, not
// an absolute production number.
//
// What changed vs. the 2.1 baseline, and why these benchmarks had to change
// shape too:
//   - The old design stored one JSON blob per stream directly in
//     state.Store (nsEvents = "logs:events"), and logsStore's in-memory
//     eventCache held that entire blob, lazily loaded on first access
//     (ensureLoaded) and rewritten whole on every flush (flushLocked). The
//     2.1 baseline's seedStreamEventsDirect wrote pre-existing data straight
//     into that state.Store blob, which logsStore would then lazily load.
//   - The new design (event_backend.go) makes event storage its own
//     component (eventBackend), decoupled from state.Store entirely in
//     memory-mode (memEventBackend is a bespoke in-process map, not backed
//     by state.Store at all) — so "pre-existing data" now has to be seeded
//     directly into a logsStore's already-constructed .backend (see
//     seedBackendDirect below), not into a state.Store namespace the store
//     would lazily discover. This mirrors seedStreamEventsDirect's own
//     "setup-only, bypasses the normal write path" character, just one
//     layer lower.
//   - flushLocked's cost no longer scales with total stream history (that
//     was the entire point of 2.3): a flush is now a batched INSERT of only
//     the unflushed buffer, whose size is capped by flushWatermark (1024)
//     regardless of how much history already exists. The old
//     BenchmarkLogsStore_FlushLocked_* group specifically existed to show a
//     flush's cost growing with N — that axis no longer applies, so it's
//     retired here rather than kept as a benchmark that would trivially show
//     "flat regardless of N" (which BenchmarkLogsStore_AppendEvents_* already
//     demonstrates end-to-end: unlike the 2.1 baseline, appendEvents' cost at
//     1,000,000 pre-existing events is no longer dramatically higher than at
//     100).
//
// All benchmarks use state.NewMemoryStore() as logsStore's state.Store
// (for log group/stream metadata — unchanged CRUD), which resolves to
// memEventBackend for events, deliberately isolating these numbers from
// SQLite disk I/O — same rationale as the 2.1 baseline. sqlEventBackend's
// own indexed-query cost is exercised separately by
// TestEventBackend_MemoryAndSQL_Parity and the concurrency/restart tests in
// event_backend_test.go, not by these throughput benchmarks.
//
// How it is measured: `go test -run=^$ -bench=. -benchmem
// ./internal/services/cloudwatch/logs/...`.
//
// Environment: run in the project's devcontainer (Debian-based image) under
// Docker Desktop on Windows 11 / WSL2, not bare metal. Absolute ns/op and
// B/op numbers will differ on other hosts/hardware; compare the shape of the
// curve across stream sizes and the before/after delta against the 2.1
// baseline, not the absolute numbers in isolation.

import (
	"context"
	"fmt"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

const (
	benchRegion = "us-east-1"
	benchGroup  = "bench-group"
	benchStream = "bench-stream"
)

// seedBackendDirect writes n synthetic LogEvents straight into s's already-
// resolved eventBackend, bypassing logsStore's public appendEvents/eventCache
// entirely (no debounce, no watermark). Setup-only — see file header.
func seedBackendDirect(b *testing.B, s *logsStore, region, group, stream string, n int) {
	b.Helper()
	const baseTS = int64(1700000000000) // 2023-11-14T22:13:20Z, arbitrary fixed epoch millis
	events := make([]LogEvent, n)
	for i := 0; i < n; i++ {
		events[i] = LogEvent{
			Timestamp:     baseTS + int64(i),
			Message:       fmt.Sprintf("log line %d - the quick brown fox jumps over the lazy dog", i),
			IngestionTime: baseTS + int64(i),
		}
	}
	if err := s.backend.appendEvents(context.Background(), region, group, stream, events); err != nil {
		b.Fatalf("seed appendEvents: %v", err)
	}
}

// ---- appendEvents throughput vs. pre-existing stream size ------------------
//
// The 2.1 baseline's headline finding: at 1,000,000 pre-existing events, the
// old design's appendEvents cost ~1.46s / ~903MB per call once the stream
// was past its flush watermark, because every flush re-marshaled the entire
// history. The new design's appendEvents cost should now be ~flat across
// these three sizes — pre-existing history no longer appears anywhere in the
// hot path (append only ever touches the small unflushed buffer; the
// backend's own storage is append-only, never rewritten).

func benchmarkAppendEvents(b *testing.B, preExisting int) {
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), benchRegion)
	defer s.flushBgCancel()
	seedBackendDirect(b, s, benchRegion, benchGroup, benchStream, preExisting)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ts := int64(1700000000000) + int64(preExisting) + int64(i)
		events := []LogEvent{{
			Timestamp:     ts,
			Message:       "appended log line - the quick brown fox jumps over the lazy dog",
			IngestionTime: ts,
		}}
		if aerr := s.appendEvents(ctx, benchGroup, benchStream, events); aerr != nil {
			b.Fatalf("appendEvents: %v", aerr)
		}
	}
}

func BenchmarkLogsStore_AppendEvents_100PreExisting(b *testing.B) {
	benchmarkAppendEvents(b, 100)
}

func BenchmarkLogsStore_AppendEvents_10000PreExisting(b *testing.B) {
	benchmarkAppendEvents(b, 10_000)
}

func BenchmarkLogsStore_AppendEvents_1000000PreExisting(b *testing.B) {
	benchmarkAppendEvents(b, 1_000_000)
}

// ---- getEvents (full-stream read) latency vs. stream size ------------------
//
// The 2.1 baseline measured a full blob Get+json.Unmarshal per call, scaling
// with stream size (O(history)). The new design queries the backend directly
// (an indexed, already-sorted read for sqlEventBackend; a map lookup +
// return-copy for memEventBackend) and merges in the (empty, in this
// benchmark) write buffer — still O(history) to read and copy N events out
// (unavoidable: the caller asked for all of them), but with no JSON decode
// step and, for the SQL backend not exercised here, no full-blob deserialize
// either.

func benchmarkGetEvents(b *testing.B, size int) {
	mem := state.NewMemoryStore()
	seedStore := newLogsStore(mem, clock.New(), benchRegion)
	seedBackendDirect(b, seedStore, benchRegion, benchGroup, benchStream, size)
	// Reuse the same backing eventBackend across iterations (memEventBackend
	// holds its data independent of any particular *logsStore instance's
	// state.Store, so a fresh logsStore wrapping the same underlying
	// memEventBackend would actually be empty — unlike the 2.1 baseline,
	// where state.Store itself was the persistence layer a fresh logsStore
	// would lazily rediscover). What we want to measure per-iteration is
	// getEvents' own cost, not backend construction, so reuse seedStore.
	seedStore.flushBgCancel()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		events, aerr := seedStore.getEvents(ctx, benchGroup, benchStream)
		if aerr != nil {
			b.Fatalf("getEvents: %v", aerr)
		}
		if len(events) != size {
			b.Fatalf("getEvents: got %d events, want %d", len(events), size)
		}
	}
}

func BenchmarkLogsStore_GetEvents_100Events(b *testing.B) {
	benchmarkGetEvents(b, 100)
}

func BenchmarkLogsStore_GetEvents_10000Events(b *testing.B) {
	benchmarkGetEvents(b, 10_000)
}

func BenchmarkLogsStore_GetEvents_1000000Events(b *testing.B) {
	benchmarkGetEvents(b, 1_000_000)
}

// ---- Memory growth for a fully-resident large stream ------------------------

// BenchmarkLogsStore_MemoryGrowth_1MEvents is the memory-mode analogue of the
// 2.1 baseline's memory characterization. memEventBackend still holds a
// stream's full history resident for the life of the process in memory-mode
// (unavoidable — there is no disk to spill to without SQLite), so this
// number is expected to look similar in shape to the 2.1 baseline's
// memory-mode numbers. The real win from 2.3 is in the SQL-backed
// (persistent/hybrid) case, where events no longer need to be memory-
// resident at all outside the small unflushed write buffer — not
// measurable via this in-process-map benchmark, but demonstrated
// structurally by sqlEventBackend never holding more than one flush batch
// in memory (see event_backend.go).
func BenchmarkLogsStore_MemoryGrowth_1MEvents(b *testing.B) {
	const n = 1_000_000

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		mem := state.NewMemoryStore()
		s := newLogsStore(mem, clock.New(), benchRegion)
		seedBackendDirect(b, s, benchRegion, benchGroup, benchStream, n)
		b.StartTimer()

		events, aerr := s.getEvents(context.Background(), benchGroup, benchStream)

		b.StopTimer()
		if aerr != nil {
			b.Fatalf("getEvents: %v", aerr)
		}
		if len(events) != n {
			b.Fatalf("getEvents: got %d events, want %d", len(events), n)
		}

		// LogEvent is 2 int64 fields (8 bytes each) + 1 string header
		// (16 bytes: data pointer + length) = 32 bytes of struct storage on
		// 64-bit, plus the actual message bytes each string header points at.
		const logEventStructBytes = 32
		var messageBytes int64
		for _, e := range events {
			messageBytes += int64(len(e.Message))
		}
		residentBytes := int64(len(events))*logEventStructBytes + messageBytes
		b.ReportMetric(float64(residentBytes), "cache_resident_bytes")
		b.ReportMetric(float64(residentBytes)/float64(n), "bytes_per_event")

		s.flushBgCancel()
		b.StartTimer()
	}
}
