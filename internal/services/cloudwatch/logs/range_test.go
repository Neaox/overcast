package logs

// Tests for storage-access-plan.md A4 — GetLogEvents/FilterLogEvents range +
// limit pushdown — proving the new getEventsRangeMerged/
// getGroupEventsRangeMerged storage-layer methods produce exactly the same
// results the old full-read-then-filter approach would, for both the
// persisted-only case and the write-buffer-merge case (P4). Mirrors the
// oracle-property-test style established by
// internal/services/cloudwatch/metric_range_test.go for the A5 metrics
// item: an oracle re-implements the OLD approach directly (never touching
// the new range-pushdown code path), and a property test compares the two
// over randomized event sets and windows.
//
// The FilterLogEvents benchmark (flat vs. group size at a fixed window)
// lives in range_bench_test.go.

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

// oracleGetEventsRangeMerged re-implements getEventsRangeMerged's pre-A4
// behavior directly: fetch the stream's FULL merged (persisted+buffered)
// history via the old getEvents path, filter to [startTs, endTs] inclusive
// both ends, then select the first/last `limit` depending on direction —
// exactly what getLogEventsTyped used to do before this item landed.
func oracleGetEventsRangeMerged(t *testing.T, ctx context.Context, s *logsStore, group, stream string, startTs, endTs int64, limit int, forward bool) []LogEvent {
	t.Helper()
	all, aerr := s.getEvents(ctx, group, stream)
	if aerr != nil {
		t.Fatalf("oracle getEvents: %v", aerr)
	}
	var filtered []LogEvent
	for _, e := range all {
		if e.Timestamp < startTs || e.Timestamp > endTs {
			continue
		}
		filtered = append(filtered, e)
	}
	if limit <= 0 || len(filtered) <= limit {
		return filtered
	}
	if forward {
		return filtered[:limit]
	}
	return filtered[len(filtered)-limit:]
}

func logEventTimestamps(events []LogEvent) []int64 {
	out := make([]int64, len(events))
	for i, e := range events {
		out[i] = e.Timestamp
	}
	return out
}

func rangedToLogEvents(events []RangedEvent) []LogEvent {
	out := make([]LogEvent, len(events))
	for i, e := range events {
		out[i] = e.LogEvent
	}
	return out
}

func assertSameLogEvents(t *testing.T, label string, want, got []LogEvent) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: got %d events, want %d\n got=%v\nwant=%v", label, len(got), len(want), logEventTimestamps(got), logEventTimestamps(want))
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s: event %d mismatch\n got=%+v\nwant=%+v", label, i, got[i], want[i])
		}
	}
}

// TestGetEventsRangeMerged_WindowEqualityProperty_PersistedOnly seeds a
// random number of persisted-only events at random timestamps, then asserts
// getEventsRangeMerged (forward and backward, various limits and windows)
// returns exactly what the oracle's full-read-then-filter approach would.
func TestGetEventsRangeMerged_WindowEqualityProperty_PersistedOnly(t *testing.T) {
	rng := rand.New(rand.NewSource(20260724))
	ctx := context.Background()

	const trials = 40
	for trial := 0; trial < trials; trial++ {
		mem := state.NewMemoryStore()
		s := newLogsStore(mem, clock.New(), "us-east-1")

		n := rng.Intn(200)
		for i := 0; i < n; i++ {
			ts := int64(rng.Intn(2000))
			if err := s.backend.appendEvents(ctx, "us-east-1", "g", "s", []LogEvent{
				{Timestamp: ts, Message: fmt.Sprintf("m%d-%d", trial, i), IngestionTime: ts},
			}); err != nil {
				t.Fatalf("trial %d: seed appendEvents: %v", trial, err)
			}
		}

		winStart := int64(rng.Intn(2200) - 100)
		winEnd := winStart + int64(rng.Intn(1500))
		limit := 0
		if rng.Intn(3) != 0 {
			limit = 1 + rng.Intn(n+5)
		}
		forward := rng.Intn(2) == 0

		label := fmt.Sprintf("trial=%d n=%d window=[%d,%d] limit=%d forward=%v", trial, n, winStart, winEnd, limit, forward)

		want := oracleGetEventsRangeMerged(t, ctx, s, "g", "s", winStart, winEnd, limit, forward)
		got, aerr := s.getEventsRangeMerged(ctx, "g", "s", winStart, winEnd, eventCursor{}, limit, forward)
		if aerr != nil {
			t.Fatalf("%s: getEventsRangeMerged: %v", label, aerr)
		}
		assertSameLogEvents(t, label, want, rangedToLogEvents(got))

		s.flushBgCancel()
	}
}

// TestGetEventsRangeMerged_BufferedVisibility proves the storage-access-plan.md
// P4 requirement directly: an event written through the normal appendEvents
// path (landing in the write buffer, below the flush watermark — i.e. an
// event ingested less than one debounce interval ago) is visible through
// getEventsRangeMerged exactly as it already was through the old
// getEvents-based path, merged in correct timestamp order alongside
// already-persisted events.
func TestGetEventsRangeMerged_BufferedVisibility(t *testing.T) {
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), "us-east-1")
	defer s.flushBgCancel()
	ctx := context.Background()

	if err := s.backend.appendEvents(ctx, "us-east-1", "g", "s", []LogEvent{
		{Timestamp: 100, Message: "persisted-100"},
		{Timestamp: 300, Message: "persisted-300"},
	}); err != nil {
		t.Fatalf("seed backend: %v", err)
	}

	// appendEvents through the store: below the 1024-event watermark, so
	// this lands in the write buffer only, not yet flushed to the backend.
	if aerr := s.appendEvents(ctx, "g", "s", []LogEvent{
		{Timestamp: 200, Message: "buffered-200"},
	}); aerr != nil {
		t.Fatalf("appendEvents (buffered): %v", aerr)
	}

	// Sanity: the event really is still only in the buffer.
	persistedOnly, err := s.backend.getEvents(ctx, "us-east-1", "g", "s")
	if err != nil {
		t.Fatalf("backend.getEvents: %v", err)
	}
	if len(persistedOnly) != 2 {
		t.Fatalf("expected the new event to still be buffered (not flushed), backend has %d events", len(persistedOnly))
	}

	got, aerr := s.getEventsRangeMerged(ctx, "g", "s", 0, 1000, eventCursor{}, 0, true)
	if aerr != nil {
		t.Fatalf("getEventsRangeMerged: %v", aerr)
	}
	want := []int64{100, 200, 300}
	if !int64sEqual(rangedTimestamps(got), want) {
		t.Fatalf("getEventsRangeMerged = %v, want %v (buffered event not visible in range read)", rangedTimestamps(got), want)
	}
}

// oracleGetGroupEventsRangeMerged re-implements getGroupEventsRangeMerged's
// pre-A4 behavior directly: loop every stream, fetch its full merged
// history via getEvents, filter to the window, tag with its stream name,
// then sort everything by (Timestamp, StreamName) — exactly what
// filterLogEventsTyped used to do (N full per-stream reads) before this
// item landed.
func oracleGetGroupEventsRangeMerged(t *testing.T, ctx context.Context, s *logsStore, group string, streamNames []string, startTs, endTs int64) []GroupRangedEvent {
	t.Helper()
	var out []GroupRangedEvent
	for _, name := range streamNames {
		events, aerr := s.getEvents(ctx, group, name)
		if aerr != nil {
			t.Fatalf("oracle getEvents(%s): %v", name, aerr)
		}
		for _, e := range events {
			if e.Timestamp < startTs || e.Timestamp > endTs {
				continue
			}
			out = append(out, GroupRangedEvent{LogEvent: e, StreamName: name})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Timestamp != out[j].Timestamp {
			return out[i].Timestamp < out[j].Timestamp
		}
		return out[i].StreamName < out[j].StreamName
	})
	return out
}

func groupRangedTimestampsAndStreams(events []GroupRangedEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = fmt.Sprintf("%d/%s", e.Timestamp, e.StreamName)
	}
	return out
}

// TestGetGroupEventsRangeMerged_WindowEqualityProperty seeds a random
// number of events across several streams in one group, then asserts
// getGroupEventsRangeMerged (unlimited, so the full window is returned in
// one call) returns exactly the same (Timestamp, StreamName) sequence the
// oracle's old N-full-stream-reads approach would.
func TestGetGroupEventsRangeMerged_WindowEqualityProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20260725))
	ctx := context.Background()
	streamNames := []string{"stream-a", "stream-b", "stream-c"}

	const trials = 30
	for trial := 0; trial < trials; trial++ {
		mem := state.NewMemoryStore()
		s := newLogsStore(mem, clock.New(), "us-east-1")

		for _, name := range streamNames {
			n := rng.Intn(60)
			for i := 0; i < n; i++ {
				ts := int64(rng.Intn(1000))
				if err := s.backend.appendEvents(ctx, "us-east-1", "g", name, []LogEvent{
					{Timestamp: ts, Message: fmt.Sprintf("m%d-%d", trial, i), IngestionTime: ts},
				}); err != nil {
					t.Fatalf("trial %d: seed %s: %v", trial, name, err)
				}
			}
		}

		winStart := int64(rng.Intn(1100) - 50)
		winEnd := winStart + int64(rng.Intn(800))
		label := fmt.Sprintf("trial=%d window=[%d,%d]", trial, winStart, winEnd)

		want := oracleGetGroupEventsRangeMerged(t, ctx, s, "g", streamNames, winStart, winEnd)
		got, aerr := s.getGroupEventsRangeMerged(ctx, "g", "", streamNames, winStart, winEnd, groupCursor{}, 0)
		if aerr != nil {
			t.Fatalf("%s: getGroupEventsRangeMerged: %v", label, aerr)
		}
		wantKeys := groupRangedTimestampsAndStreams(want)
		gotKeys := groupRangedTimestampsAndStreams(got)
		if !equalStrings(wantKeys, gotKeys) {
			t.Fatalf("%s:\n got:  %v\n want: %v", label, gotKeys, wantKeys)
		}

		s.flushBgCancel()
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestGetGroupEventsRangeMerged_BufferedVisibility is
// TestGetEventsRangeMerged_BufferedVisibility's group-wide analogue: a
// freshly-buffered (not yet flushed) event in one of several streams must
// appear in the group-range merge, correctly interleaved with already-
// persisted events from other streams.
func TestGetGroupEventsRangeMerged_BufferedVisibility(t *testing.T) {
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), "us-east-1")
	defer s.flushBgCancel()
	ctx := context.Background()

	if err := s.backend.appendEvents(ctx, "us-east-1", "g", "alpha", []LogEvent{
		{Timestamp: 100, Message: "alpha-persisted"},
	}); err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	if err := s.backend.appendEvents(ctx, "us-east-1", "g", "beta", []LogEvent{
		{Timestamp: 300, Message: "beta-persisted"},
	}); err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	// Buffered (unflushed) event in "beta", timestamped between the two
	// persisted events above.
	if aerr := s.appendEvents(ctx, "g", "beta", []LogEvent{
		{Timestamp: 200, Message: "beta-buffered"},
	}); aerr != nil {
		t.Fatalf("appendEvents (buffered): %v", aerr)
	}

	got, aerr := s.getGroupEventsRangeMerged(ctx, "g", "", []string{"alpha", "beta"}, 0, 1000, groupCursor{}, 0)
	if aerr != nil {
		t.Fatalf("getGroupEventsRangeMerged: %v", aerr)
	}
	want := []string{"100/alpha", "200/beta", "300/beta"}
	gotKeys := groupRangedTimestampsAndStreams(got)
	if !equalStrings(want, gotKeys) {
		t.Fatalf("getGroupEventsRangeMerged = %v, want %v (buffered event not visible/misordered)", gotKeys, want)
	}
}
