package logs

// Benchmarks for the log-view access patterns the web UI actually issues
// (docs/plans/logs-view-performance.md; storage-access-plan.md A4), run
// against BOTH event backends — memEventBackend (compose default) and
// sqlEventBackend (OVERCAST_STATE=sqlite) — unlike range_bench_test.go /
// store_bench_test.go, which are memory-only throughput shapes.
//
// The properties under test, per pattern:
//
//   - Token-paged reads (GetLogEvents f/·b/· walking, FilterLogEvents'
//     internal scan-budget batches) must cost O(page), not O(distance from
//     the window edge): a cursor 400k events deep must not be 400k times
//     more expensive to resume from than one 200 events deep.
//   - Backward window-walking (FilterLogEvents over adjacent [a−δ, a−1]
//     chunks as the user scrolls into history — logs-view-performance.md
//     Phase 4's backward expansion) must cost O(chunk), not O(history):
//     a chunk near the beginning of a 500k-event history must cost the
//     same as one near the tail.
//   - DescribeLogStreams must be metadata-only: flat regardless of how many
//     events the group's streams hold.
//   - appendEvents (the PutLogEvents flush path) must not rescan the
//     stream's persisted history per write batch.
//
// Run paced and sequentially:
//   go test -run '^$' -bench BenchmarkAccess -benchmem -count 5 ./internal/services/cloudwatch/logs/
//
// SQL numbers use a HybridStore in b.TempDir() — absolute numbers include
// local SQLite I/O and are only comparable within one machine/run.

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

// newBenchBackend returns an eventBackend of the requested kind ("memory" or
// "sql"), skipping the benchmark when the build can't provide it. Mirrors
// event_backend_test.go's newTestBackends.
func newBenchBackend(b *testing.B, kind string) eventBackend {
	b.Helper()
	switch kind {
	case "memory":
		return newMemEventBackend()
	case "sql":
		if !config.SQLiteSupported() {
			b.Skip("SQLite not compiled into this build")
		}
		dir := b.TempDir()
		hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
		if err != nil {
			b.Fatalf("NewHybridStore: %v", err)
		}
		b.Cleanup(func() {
			if err := hybrid.Close(); err != nil {
				b.Logf("hybrid.Close: %v", err)
			}
		})
		backend := newEventBackendFor(state.Unwrap(hybrid, serviceName))
		if _, ok := backend.(*sqlEventBackend); !ok {
			b.Fatalf("expected sqlEventBackend, got %T", backend)
		}
		return backend
	default:
		b.Fatalf("unknown backend kind %q", kind)
		return nil
	}
}

// seedBenchStream appends n events (one per millisecond from ts=0) to one
// stream in batches, via the backend's own appendEvents — the same write
// shape the flush path produces.
func seedBenchStream(b *testing.B, be eventBackend, region, group, stream string, n int) {
	b.Helper()
	const batchSize = 10_000
	ctx := context.Background()
	for start := 0; start < n; start += batchSize {
		end := start + batchSize
		if end > n {
			end = n
		}
		events := make([]LogEvent, 0, end-start)
		for i := start; i < end; i++ {
			events = append(events, LogEvent{
				Timestamp:     int64(i),
				Message:       fmt.Sprintf("log line %d - the quick brown fox jumps over the lazy dog", i),
				IngestionTime: int64(i),
			})
		}
		if err := be.appendEvents(ctx, region, group, stream, events); err != nil {
			b.Fatalf("seed appendEvents: %v", err)
		}
	}
}

const (
	accessBenchRegion = "us-east-1"
	accessBenchGroup  = "bench-group"
	accessBenchStream = "bench-stream"
)

// ---- Pattern 2: GetLogEvents token paging at depth --------------------------
//
// One backward page of 200 whose resume cursor sits `depth` events before the
// stream's tail (the peek walking into history), and one forward page whose
// cursor sits `depth` events after the stream's head (resuming forward after
// a dead tail session). Cost must not grow with depth.

func benchmarkEventsRangePageAtDepth(b *testing.B, kind string, forward bool, depth int) {
	const n = 500_000
	const page = 200
	be := newBenchBackend(b, kind)
	seedBenchStream(b, be, accessBenchRegion, accessBenchGroup, accessBenchStream, n)
	ctx := context.Background()

	// Timestamps are 0..n-1, seq equals ts here (single seed order).
	var after eventCursor
	if forward {
		after = eventCursor{Valid: true, Timestamp: int64(depth), Seq: int64(depth)}
	} else {
		after = eventCursor{Valid: true, Timestamp: int64(n - depth), Seq: int64(n - depth)}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := be.getEventsRange(ctx, accessBenchRegion, accessBenchGroup, accessBenchStream,
			math.MinInt64, math.MaxInt64, after, page, forward)
		if err != nil {
			b.Fatalf("getEventsRange: %v", err)
		}
		if len(out) != page {
			b.Fatalf("got %d events, want %d", len(out), page)
		}
	}
}

func BenchmarkAccessGetEventsBackwardPage_Shallow_Memory(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "memory", false, 1_000)
}
func BenchmarkAccessGetEventsBackwardPage_Deep_Memory(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "memory", false, 400_000)
}
func BenchmarkAccessGetEventsBackwardPage_Shallow_SQL(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "sql", false, 1_000)
}
func BenchmarkAccessGetEventsBackwardPage_Deep_SQL(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "sql", false, 400_000)
}
func BenchmarkAccessGetEventsForwardPage_Shallow_Memory(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "memory", true, 1_000)
}
func BenchmarkAccessGetEventsForwardPage_Deep_Memory(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "memory", true, 400_000)
}
func BenchmarkAccessGetEventsForwardPage_Shallow_SQL(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "sql", true, 1_000)
}
func BenchmarkAccessGetEventsForwardPage_Deep_SQL(b *testing.B) {
	benchmarkEventsRangePageAtDepth(b, "sql", true, 400_000)
}

// ---- Pattern 1: FilterLogEvents backward window-walking ---------------------
//
// One [a, a+windowMs] chunk of the group's history, positioned either near
// the tail (where a fresh view starts) or deep in history (where the user
// has scrolled back to). 5 streams × 100k events, interleaved one event per
// stream per 5ms. Cost must be O(chunk), independent of position.

func benchmarkGroupWindowChunk(b *testing.B, kind string, deep bool) {
	const (
		numStreams      = 5
		eventsPerStream = 100_000
		windowMs        = 1_000 // 1000 group events per window (200/stream × 5)
	)
	be := newBenchBackend(b, kind)
	ctx := context.Background()
	// Interleave: stream si holds ts = si, si+5, si+10, ... so every window
	// of windowMs covers all five streams evenly.
	span := int64(numStreams * eventsPerStream)
	for si := 0; si < numStreams; si++ {
		stream := fmt.Sprintf("stream-%02d", si)
		events := make([]LogEvent, 0, eventsPerStream)
		for i := 0; i < eventsPerStream; i++ {
			ts := int64(si + i*numStreams)
			events = append(events, LogEvent{
				Timestamp:     ts,
				Message:       fmt.Sprintf("log line %d on %s - the quick brown fox", i, stream),
				IngestionTime: ts,
			})
		}
		if err := be.appendEvents(ctx, accessBenchRegion, accessBenchGroup, stream, events); err != nil {
			b.Fatalf("seed appendEvents: %v", err)
		}
	}

	start := span - windowMs // near the tail
	if deep {
		start = 1_000 // near the beginning of history
	}
	end := start + windowMs - 1

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := be.getGroupEventsRange(ctx, accessBenchRegion, accessBenchGroup, "",
			start, end, groupCursor{}, windowMs)
		if err != nil {
			b.Fatalf("getGroupEventsRange: %v", err)
		}
		if len(out) != windowMs {
			b.Fatalf("got %d events, want %d", len(out), windowMs)
		}
	}
}

func BenchmarkAccessGroupWindowChunk_Tail_Memory(b *testing.B) {
	benchmarkGroupWindowChunk(b, "memory", false)
}
func BenchmarkAccessGroupWindowChunk_Deep_Memory(b *testing.B) {
	benchmarkGroupWindowChunk(b, "memory", true)
}
func BenchmarkAccessGroupWindowChunk_Tail_SQL(b *testing.B) {
	benchmarkGroupWindowChunk(b, "sql", false)
}
func BenchmarkAccessGroupWindowChunk_Deep_SQL(b *testing.B) {
	benchmarkGroupWindowChunk(b, "sql", true)
}

// ---- Pattern 1/3: FilterLogEvents' internal batch cursor at depth ----------
//
// filterLogEventsTyped's scan-budget loop re-issues getGroupEventsRange with
// the last batch's (ts, stream, seq) cursor while startTs stays fixed — a
// narrow filter pattern over a wide window walks this cursor far from
// startTs. Resuming from a deep cursor must cost O(batch), not O(distance
// from startTs).

func benchmarkGroupCursorResumeAtDepth(b *testing.B, kind string, depth int64) {
	const (
		numStreams      = 5
		eventsPerStream = 100_000
		batch           = 1_000
	)
	be := newBenchBackend(b, kind)
	ctx := context.Background()
	span := int64(numStreams * eventsPerStream)
	for si := 0; si < numStreams; si++ {
		stream := fmt.Sprintf("stream-%02d", si)
		events := make([]LogEvent, 0, eventsPerStream)
		for i := 0; i < eventsPerStream; i++ {
			ts := int64(si + i*numStreams)
			events = append(events, LogEvent{
				Timestamp:     ts,
				Message:       "the quick brown fox jumps over the lazy dog",
				IngestionTime: ts,
			})
		}
		if err := be.appendEvents(ctx, accessBenchRegion, accessBenchGroup, stream, events); err != nil {
			b.Fatalf("seed appendEvents: %v", err)
		}
	}

	after := groupCursor{Valid: true, Timestamp: depth, StreamName: "stream-04", Seq: depth / numStreams}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out, err := be.getGroupEventsRange(ctx, accessBenchRegion, accessBenchGroup, "",
			0, span, after, batch)
		if err != nil {
			b.Fatalf("getGroupEventsRange: %v", err)
		}
		if len(out) != batch {
			b.Fatalf("got %d events, want %d", len(out), batch)
		}
	}
}

func BenchmarkAccessGroupCursorResume_Shallow_Memory(b *testing.B) {
	benchmarkGroupCursorResumeAtDepth(b, "memory", 1_000)
}
func BenchmarkAccessGroupCursorResume_Deep_Memory(b *testing.B) {
	benchmarkGroupCursorResumeAtDepth(b, "memory", 400_000)
}
func BenchmarkAccessGroupCursorResume_Shallow_SQL(b *testing.B) {
	benchmarkGroupCursorResumeAtDepth(b, "sql", 1_000)
}
func BenchmarkAccessGroupCursorResume_Deep_SQL(b *testing.B) {
	benchmarkGroupCursorResumeAtDepth(b, "sql", 400_000)
}

// ---- Pattern 1: single-stream FilterLogEvents (logStreamNames: [one]) -------
//
// The web UI's single-stream viewer issues FilterLogEvents with exactly one
// LogStreamNames entry per window chunk. The raw scan should be bounded by
// that stream's events in the window, not the whole group's — measured
// through the full typed handler (memory store) with 20 streams so the
// group-vs-stream factor is visible.

func BenchmarkAccessFilterSingleStreamWindow_Memory(b *testing.B) {
	const (
		numStreams      = 20
		eventsPerStream = 20_000
		windowMs        = 20_000 // 1000 events of the target stream per window
	)
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), accessBenchRegion)
	defer s.flushBgCancel()
	ctx := context.Background()
	if aerr := s.putLogGroup(ctx, &LogGroup{Name: accessBenchGroup, CreationTime: 1}); aerr != nil {
		b.Fatalf("putLogGroup: %v", aerr)
	}
	span := int64(numStreams * eventsPerStream)
	for si := 0; si < numStreams; si++ {
		stream := fmt.Sprintf("stream-%02d", si)
		if aerr := s.putLogStream(ctx, accessBenchGroup, &LogStream{
			Name: stream, CreationTime: 1,
			FirstEventTimestamp: int64(si), LastEventTimestamp: span - 1, LastIngestionTime: span - 1,
		}); aerr != nil {
			b.Fatalf("putLogStream: %v", aerr)
		}
		events := make([]LogEvent, 0, eventsPerStream)
		for i := 0; i < eventsPerStream; i++ {
			ts := int64(si + i*numStreams)
			events = append(events, LogEvent{Timestamp: ts, Message: "the quick brown fox jumps over the lazy dog", IngestionTime: ts})
		}
		if err := s.backend.appendEvents(ctx, accessBenchRegion, accessBenchGroup, stream, events); err != nil {
			b.Fatalf("seed appendEvents: %v", err)
		}
	}
	h := &Handler{store: s}
	start := span - int64(windowMs)
	end := span - 1
	req := &filterLogEventsRequest{
		LogGroupName:   accessBenchGroup,
		LogStreamNames: []string{"stream-07"},
		StartTime:      &start,
		EndTime:        &end,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, aerr := h.filterLogEventsTyped(ctx, req)
		if aerr != nil {
			b.Fatalf("filterLogEventsTyped: %v", aerr)
		}
		if len(resp.Events) != windowMs/numStreams {
			b.Fatalf("got %d events, want %d", len(resp.Events), windowMs/numStreams)
		}
	}
}

// ---- Pattern 5: appendEvents write cost vs. persisted history ---------------
//
// One 16-event batch (a typical coalesced flush) appended to a stream that
// already holds `preExisting` events. Cost must not grow with preExisting
// beyond index-maintenance log factors.

func benchmarkBackendAppendBatch(b *testing.B, kind string, preExisting int) {
	be := newBenchBackend(b, kind)
	seedBenchStream(b, be, accessBenchRegion, accessBenchGroup, accessBenchStream, preExisting)
	ctx := context.Background()
	const batchSize = 16

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		base := int64(preExisting) + int64(i*batchSize)
		events := make([]LogEvent, batchSize)
		for j := range events {
			ts := base + int64(j)
			events[j] = LogEvent{Timestamp: ts, Message: "appended log line - quick brown fox", IngestionTime: ts}
		}
		if err := be.appendEvents(ctx, accessBenchRegion, accessBenchGroup, accessBenchStream, events); err != nil {
			b.Fatalf("appendEvents: %v", err)
		}
	}
}

func BenchmarkAccessAppendBatch_1kPreExisting_Memory(b *testing.B) {
	benchmarkBackendAppendBatch(b, "memory", 1_000)
}
func BenchmarkAccessAppendBatch_500kPreExisting_Memory(b *testing.B) {
	benchmarkBackendAppendBatch(b, "memory", 500_000)
}
func BenchmarkAccessAppendBatch_1kPreExisting_SQL(b *testing.B) {
	benchmarkBackendAppendBatch(b, "sql", 1_000)
}
func BenchmarkAccessAppendBatch_500kPreExisting_SQL(b *testing.B) {
	benchmarkBackendAppendBatch(b, "sql", 500_000)
}

// ---- Pattern 4: DescribeLogStreams metadata read ----------------------------
//
// DescribeLogStreams' first/last/ingestion timestamps are maintained
// incrementally by putLogEventsTyped on the stream metadata record; a
// describe call must never scan events. Measured through the full typed
// handler with the memory store; the axis is event volume behind the
// metadata (1k vs 500k), and the expectation is flat.

func benchmarkDescribeLogStreams(b *testing.B, eventsPerStream int) {
	const numStreams = 5
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), accessBenchRegion)
	defer s.flushBgCancel()
	ctx := context.Background()
	if aerr := s.putLogGroup(ctx, &LogGroup{Name: accessBenchGroup, CreationTime: 1}); aerr != nil {
		b.Fatalf("putLogGroup: %v", aerr)
	}
	for si := 0; si < numStreams; si++ {
		stream := fmt.Sprintf("stream-%02d", si)
		if aerr := s.putLogStream(ctx, accessBenchGroup, &LogStream{
			Name:                stream,
			CreationTime:        1,
			FirstEventTimestamp: 0,
			LastEventTimestamp:  int64(eventsPerStream - 1),
			LastIngestionTime:   int64(eventsPerStream - 1),
		}); aerr != nil {
			b.Fatalf("putLogStream: %v", aerr)
		}
		seedBenchStream(b, s.backend, accessBenchRegion, accessBenchGroup, stream, eventsPerStream)
	}
	h := &Handler{store: s}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, aerr := h.describeLogStreamsTyped(ctx, &describeLogStreamsRequest{LogGroupName: accessBenchGroup})
		if aerr != nil {
			b.Fatalf("describeLogStreamsTyped: %v", aerr)
		}
		if len(resp.LogStreams) != numStreams {
			b.Fatalf("got %d streams, want %d", len(resp.LogStreams), numStreams)
		}
	}
}

func BenchmarkAccessDescribeLogStreams_1kEvents(b *testing.B) {
	benchmarkDescribeLogStreams(b, 1_000)
}
func BenchmarkAccessDescribeLogStreams_500kEvents(b *testing.B) {
	benchmarkDescribeLogStreams(b, 500_000)
}
