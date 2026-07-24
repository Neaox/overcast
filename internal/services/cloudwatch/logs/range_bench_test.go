package logs

// Benchmarks for storage-access-plan.md A4 — GetLogEvents/FilterLogEvents
// range + limit pushdown. Mirrors store_bench_test.go's conventions
// (state.NewMemoryStore backing, seed via backend.appendEvents directly to
// bypass the debounced write path, b.ReportAllocs — see that file's header
// for the full rationale and docs/performance.md's measurement-conditions
// requirement). Run: `go test -run=^$ -bench=. -benchmem
// ./internal/services/cloudwatch/logs/...`.
//
// "Group size" here means total EVENT VOLUME across the group's streams —
// the unbounded, data-plane axis the boundedness rule (storage-access-plan.md)
// calls out (log streams themselves are bounded, IaC/human-created metadata;
// log EVENTS are the workload-traffic-driven, unbounded quantity). This
// mirrors the A5 metrics benchmark's shape exactly (fixed window, varying
// how much data exists OUTSIDE it) rather than store_bench_test.go's
// stream-count axis.
//
// The headline property: before A4, FilterLogEvents read each stream's
// FULL history regardless of the requested time window — cost grew with
// total accumulated events. After A4, the group-range query only touches
// events inside [startTime, endTime] plus `limit`, so allocs/op should stay
// flat as pre-existing-but-out-of-window history grows.
//
// A companion benchmark below (BenchmarkFilterLogEvents_NStreams) varies
// stream COUNT instead, at a fixed small per-stream history and window
// covering everything — this is a secondary, informative measurement, not
// the primary flat-vs-history-size property: memEventBackend's
// getGroupEventsRange must still discover which streams belong to the
// group before it can k-way-merge them (an O(streams-in-group) setup cost,
// unlike sqlEventBackend's single indexed query), so this axis is expected
// to grow with stream count in memory-mode. Log streams are bounded
// metadata (dozens to low thousands, per the boundedness rule), so this is
// an accepted, documented characteristic rather than a regression.

import (
	"context"
	"fmt"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

// seedGroupHistoryDirect writes numStreams streams, each with
// eventsPerStream events spread one-per-millisecond starting at ts=0,
// straight into s's backend — bypassing logsStore's debounce/write-buffer
// path entirely (setup-only, same rationale as store_bench_test.go's
// seedBackendDirect).
func seedGroupHistoryDirect(b *testing.B, s *logsStore, region, group string, numStreams, eventsPerStream int) {
	b.Helper()
	ctx := context.Background()
	for si := 0; si < numStreams; si++ {
		streamName := fmt.Sprintf("stream-%04d", si)
		events := make([]LogEvent, eventsPerStream)
		for i := 0; i < eventsPerStream; i++ {
			ts := int64(i)
			events[i] = LogEvent{
				Timestamp:     ts,
				Message:       fmt.Sprintf("log line %d on %s - the quick brown fox jumps over the lazy dog", i, streamName),
				IngestionTime: ts,
			}
		}
		if err := s.backend.appendEvents(ctx, region, group, streamName, events); err != nil {
			b.Fatalf("seed appendEvents(%s): %v", streamName, err)
		}
	}
}

// benchmarkFilterLogEventsFlatVsHistorySize preloads a fixed number of
// streams, each with `eventsPerStream` events spanning [0, eventsPerStream)
// ms, then measures one FilterLogEvents call over a NARROW, FIXED 100ms
// window at the very end of that history — the axis under test is total
// accumulated history (eventsPerStream), not window width or stream count.
func benchmarkFilterLogEventsFlatVsHistorySize(b *testing.B, eventsPerStream int) {
	const region, group = "us-east-1", "bench-group"
	const numStreams = 5

	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), region)
	defer s.flushBgCancel()
	if aerr := s.putLogGroup(context.Background(), &LogGroup{Name: group, CreationTime: 1}); aerr != nil {
		b.Fatalf("putLogGroup: %v", aerr)
	}
	seedGroupHistoryDirect(b, s, region, group, numStreams, eventsPerStream)

	h := &Handler{store: s}
	ctx := context.Background()
	startTime := int64(eventsPerStream - 100)
	endTime := int64(eventsPerStream)
	req := &filterLogEventsRequest{
		LogGroupName:  group,
		FilterPattern: "quick",
		StartTime:     &startTime,
		EndTime:       &endTime,
		Limit:         50,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, aerr := h.filterLogEventsTyped(ctx, req)
		if aerr != nil {
			b.Fatalf("filterLogEventsTyped: %v", aerr)
		}
		if len(resp.Events) == 0 {
			b.Fatalf("expected matching events in the window, got 0")
		}
	}
}

func BenchmarkFilterLogEvents_100EventsPerStream(b *testing.B) {
	benchmarkFilterLogEventsFlatVsHistorySize(b, 100)
}

func BenchmarkFilterLogEvents_10000EventsPerStream(b *testing.B) {
	benchmarkFilterLogEventsFlatVsHistorySize(b, 10_000)
}

func BenchmarkFilterLogEvents_1000000EventsPerStream(b *testing.B) {
	benchmarkFilterLogEventsFlatVsHistorySize(b, 1_000_000)
}

// benchmarkFilterLogEventsVsStreamCount preloads numStreams streams with a
// small, fixed amount of history each, all within one fixed window covering
// everything, and measures FilterLogEvents as stream count grows — see the
// file header for why this axis is expected to grow (memory-mode only) and
// is reported for completeness rather than as an A4 acceptance signal.
func benchmarkFilterLogEventsVsStreamCount(b *testing.B, numStreams int) {
	const region, group = "us-east-1", "bench-group"
	const eventsPerStream = 20

	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), region)
	defer s.flushBgCancel()
	if aerr := s.putLogGroup(context.Background(), &LogGroup{Name: group, CreationTime: 1}); aerr != nil {
		b.Fatalf("putLogGroup: %v", aerr)
	}
	seedGroupHistoryDirect(b, s, region, group, numStreams, eventsPerStream)

	h := &Handler{store: s}
	ctx := context.Background()
	req := &filterLogEventsRequest{
		LogGroupName:  group,
		FilterPattern: "quick",
		Limit:         50,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, aerr := h.filterLogEventsTyped(ctx, req)
		if aerr != nil {
			b.Fatalf("filterLogEventsTyped: %v", aerr)
		}
		if len(resp.Events) == 0 {
			b.Fatalf("expected matching events, got 0")
		}
	}
}

func BenchmarkFilterLogEvents_10Streams(b *testing.B) {
	benchmarkFilterLogEventsVsStreamCount(b, 10)
}

func BenchmarkFilterLogEvents_100Streams(b *testing.B) {
	benchmarkFilterLogEventsVsStreamCount(b, 100)
}

func BenchmarkFilterLogEvents_1000Streams(b *testing.B) {
	benchmarkFilterLogEventsVsStreamCount(b, 1000)
}
