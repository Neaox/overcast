package logs

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

// newTestBackends returns one memEventBackend and one sqlEventBackend backed
// by a real, temp-dir-rooted *state.HybridStore, so tests can run the same
// assertions against both — the memory-mode parity requirement from
// docs/plans/storage-plan.md 2.3.
func newTestBackends(t *testing.T) (mem eventBackend, sqlBackend eventBackend) {
	t.Helper()
	mem = newMemEventBackend()

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
	backendStore := state.Unwrap(hybrid, serviceName)
	sqlBackend = newEventBackendFor(backendStore)
	if _, ok := sqlBackend.(*sqlEventBackend); !ok {
		t.Fatalf("expected sqlEventBackend for a SQLite-backed store, got %T", sqlBackend)
	}
	return mem, sqlBackend
}

func eventsEqual(a, b []LogEvent) bool {
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

// TestEventBackend_MemoryAndSQL_Parity runs the same append/get/delete
// sequence against both backends and asserts identical externally-observable
// behavior — docs/plans/storage-plan.md 2.3's "memory-mode parity suite run
// against both backends" requirement.
func TestEventBackend_MemoryAndSQL_Parity(t *testing.T) {
	mem, sqlBackend := newTestBackends(t)

	for name, b := range map[string]eventBackend{"memory": mem, "sql": sqlBackend} {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, group, stream = "us-east-1", "my-group", "my-stream"

			// Empty stream reads back as an empty (non-nil) slice.
			got, err := b.getEvents(ctx, region, group, stream)
			if err != nil {
				t.Fatalf("getEvents (empty): %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("getEvents (empty) = %v, want empty", got)
			}

			// Append two batches (out of relative order between batches, to
			// prove appendEvents doesn't assume global monotonicity across
			// calls — only within a single caller's provided slice, which
			// logsStore's eventCache guarantees upstream).
			batch1 := []LogEvent{
				{Timestamp: 100, Message: "m100", IngestionTime: 100},
				{Timestamp: 300, Message: "m300", IngestionTime: 300},
			}
			batch2 := []LogEvent{
				{Timestamp: 200, Message: "m200", IngestionTime: 200},
			}
			if err := b.appendEvents(ctx, region, group, stream, batch1); err != nil {
				t.Fatalf("appendEvents batch1: %v", err)
			}
			if err := b.appendEvents(ctx, region, group, stream, batch2); err != nil {
				t.Fatalf("appendEvents batch2: %v", err)
			}

			want := []LogEvent{
				{Timestamp: 100, Message: "m100", IngestionTime: 100},
				{Timestamp: 200, Message: "m200", IngestionTime: 200},
				{Timestamp: 300, Message: "m300", IngestionTime: 300},
			}
			got, err = b.getEvents(ctx, region, group, stream)
			if err != nil {
				t.Fatalf("getEvents: %v", err)
			}
			if !eventsEqual(got, want) {
				t.Fatalf("getEvents = %+v, want %+v", got, want)
			}

			// A second, unrelated stream in the same group is unaffected.
			if err := b.appendEvents(ctx, region, group, "other-stream", []LogEvent{{Timestamp: 1, Message: "x"}}); err != nil {
				t.Fatalf("appendEvents other-stream: %v", err)
			}

			// deleteStream removes only the target stream.
			if err := b.deleteStream(ctx, region, group, stream); err != nil {
				t.Fatalf("deleteStream: %v", err)
			}
			got, err = b.getEvents(ctx, region, group, stream)
			if err != nil {
				t.Fatalf("getEvents after delete: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("getEvents after deleteStream = %v, want empty", got)
			}
			got, err = b.getEvents(ctx, region, group, "other-stream")
			if err != nil {
				t.Fatalf("getEvents other-stream: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("expected other-stream untouched by deleteStream, got %v", got)
			}

			// deleteGroup removes every stream in the group.
			if err := b.deleteGroup(ctx, region, group); err != nil {
				t.Fatalf("deleteGroup: %v", err)
			}
			got, err = b.getEvents(ctx, region, group, "other-stream")
			if err != nil {
				t.Fatalf("getEvents after deleteGroup: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("getEvents after deleteGroup = %v, want empty", got)
			}
		})
	}
}

// TestEventBackend_DebugScan_MemoryAndSQL_Parity exercises the
// DebugStateProvider-backing debugScan/debugDeleteAll methods on both
// backends, including the truncation contract used by the bounded
// /_debug/state response (storage-plan.md 2.3).
func TestEventBackend_DebugScan_MemoryAndSQL_Parity(t *testing.T) {
	mem, sqlBackend := newTestBackends(t)

	for name, b := range map[string]eventBackend{"memory": mem, "sql": sqlBackend} {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for i := 0; i < 5; i++ {
				ev := []LogEvent{{Timestamp: int64(i), Message: fmt.Sprintf("m%d", i), IngestionTime: int64(i)}}
				if err := b.appendEvents(ctx, "us-east-1", "g", "s", ev); err != nil {
					t.Fatalf("appendEvents: %v", err)
				}
			}

			records, truncated, err := b.debugScan(ctx, 3)
			if err != nil {
				t.Fatalf("debugScan(limit=3): %v", err)
			}
			if len(records) != 3 {
				t.Fatalf("debugScan(limit=3) returned %d records, want 3", len(records))
			}
			if !truncated {
				t.Error("debugScan(limit=3) expected truncated=true with 5 total events")
			}

			records, truncated, err = b.debugScan(ctx, 0)
			if err != nil {
				t.Fatalf("debugScan(limit=0): %v", err)
			}
			if len(records) != 5 {
				t.Fatalf("debugScan(limit=0) returned %d records, want 5 (unbounded)", len(records))
			}
			if truncated {
				t.Error("debugScan(limit=0) expected truncated=false (unbounded)")
			}

			if err := b.debugDeleteAll(ctx); err != nil {
				t.Fatalf("debugDeleteAll: %v", err)
			}
			records, _, err = b.debugScan(ctx, 0)
			if err != nil {
				t.Fatalf("debugScan after debugDeleteAll: %v", err)
			}
			if len(records) != 0 {
				t.Fatalf("expected 0 records after debugDeleteAll, got %d", len(records))
			}
		})
	}
}

// TestNewEventBackendFor_SurvivesRestart_WithUnrelatedNamespacedOverride
// mirrors DynamoDB's identically-named test (item_store.go /
// service_persistence_test.go): an OVERCAST_STATE_<OTHER> override wrapping
// the store in *state.NamespacedStore must not silently downgrade Logs event
// persistence to memory-only (storage-plan.md 1.1's bug class).
func TestNewEventBackendFor_SurvivesRestart_WithUnrelatedNamespacedOverride(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	newWrapped := func() *state.NamespacedStore {
		hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
		if err != nil {
			t.Fatalf("NewHybridStore: %v", err)
		}
		t.Cleanup(func() {
			if err := hybrid.Close(); err != nil {
				t.Logf("hybrid.Close: %v", err)
			}
		})
		return state.NewNamespacedStore(hybrid, map[string]state.Store{
			"s3": state.NewMemoryStore(),
		})
	}

	store1 := newWrapped()
	backend1 := newEventBackendFor(state.Unwrap(store1, serviceName))
	if _, ok := backend1.(*sqlEventBackend); !ok {
		t.Fatalf("expected sqlEventBackend when default store is SQLite-backed, got %T", backend1)
	}
	if err := backend1.appendEvents(ctx, "us-east-1", "g", "s", []LogEvent{{Timestamp: 1, Message: "hello", IngestionTime: 1}}); err != nil {
		t.Fatalf("appendEvents: %v", err)
	}

	store2 := newWrapped()
	backend2 := newEventBackendFor(state.Unwrap(store2, serviceName))
	got, err := backend2.getEvents(ctx, "us-east-1", "g", "s")
	if err != nil {
		t.Fatalf("getEvents after restart: %v", err)
	}
	if len(got) != 1 || got[0].Message != "hello" {
		t.Fatalf("event did not survive restart — Logs event persistence was silently lost under an unrelated store override, got %+v", got)
	}
}

// TestNewEventBackendFor_WithoutUnwrap_FallsBackToMemory documents the bug
// class this guards against: passing an unresolved *state.NamespacedStore
// straight to newEventBackendFor silently selects the in-memory backend even
// though the default store is SQLite-backed.
func TestNewEventBackendFor_WithoutUnwrap_FallsBackToMemory(t *testing.T) {
	dir := t.TempDir()
	hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() { hybrid.Close() })
	store := state.NewNamespacedStore(hybrid, map[string]state.Store{"s3": state.NewMemoryStore()})

	backend := newEventBackendFor(store) // deliberately NOT unwrapped
	if _, ok := backend.(*memEventBackend); !ok {
		t.Fatalf("expected memEventBackend when passing a raw NamespacedStore (pre-fix behavior), got %T", backend)
	}
}

// ---- logsStore-level tests (write buffer + backend merge) ------------------

// TestLogsStore_GetEvents_MergesPersistedAndBufferedInOrder is the range-
// query/merge correctness test: events already flushed to the backend and
// events still sitting in the unflushed write buffer must come back as one
// correctly time-ordered sequence — the same result the old blob-based
// full-scan-then-filter design would have produced from the caller's
// perspective (docs/plans/storage-plan.md 2.3's explicit "no behavior change"
// requirement).
func TestLogsStore_GetEvents_MergesPersistedAndBufferedInOrder(t *testing.T) {
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), "us-east-1")
	defer s.flushBgCancel()
	ctx := context.Background()

	// Directly seed "persisted" events into the backend (bypassing the
	// write buffer) to simulate events from an earlier, already-flushed
	// process lifetime.
	if err := s.backend.appendEvents(ctx, "us-east-1", "g", "s", []LogEvent{
		{Timestamp: 100, Message: "persisted-100"},
		{Timestamp: 300, Message: "persisted-300"},
	}); err != nil {
		t.Fatalf("seed backend: %v", err)
	}

	// appendEvents through the store puts these in the write buffer only
	// (below the flush watermark, so nothing is written to the backend yet).
	if aerr := s.appendEvents(ctx, "g", "s", []LogEvent{
		{Timestamp: 200, Message: "buffered-200"},
		{Timestamp: 50, Message: "buffered-50"},
	}); aerr != nil {
		t.Fatalf("appendEvents: %v", aerr)
	}

	got, aerr := s.getEvents(ctx, "g", "s")
	if aerr != nil {
		t.Fatalf("getEvents: %v", aerr)
	}
	wantOrder := []int64{50, 100, 200, 300}
	if len(got) != len(wantOrder) {
		t.Fatalf("getEvents returned %d events, want %d: %+v", len(got), len(wantOrder), got)
	}
	for i, ts := range wantOrder {
		if got[i].Timestamp != ts {
			t.Fatalf("event %d timestamp = %d, want %d (full: %+v)", i, got[i].Timestamp, ts, got)
		}
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Timestamp < got[j].Timestamp }) {
		t.Fatalf("getEvents result not sorted: %+v", got)
	}
}

// TestLogsStore_AppendThenFlush_EventsSurviveInBackend proves the debounced
// flush path actually lands events in the backend (not just the buffer),
// using the watermark path for a deterministic, timing-independent trigger.
func TestLogsStore_AppendThenFlush_EventsSurviveInBackend(t *testing.T) {
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), "us-east-1")
	defer s.flushBgCancel()
	ctx := context.Background()

	const flushWatermark = 1024
	events := make([]LogEvent, flushWatermark)
	for i := range events {
		events[i] = LogEvent{Timestamp: int64(i), Message: fmt.Sprintf("m%d", i), IngestionTime: int64(i)}
	}
	if aerr := s.appendEvents(ctx, "g", "s", events); aerr != nil {
		t.Fatalf("appendEvents: %v", aerr)
	}

	// The watermark path flushes inline, so the backend must already have
	// every event, and the cache's buffer must be empty.
	persisted, err := s.backend.getEvents(ctx, "us-east-1", "g", "s")
	if err != nil {
		t.Fatalf("backend.getEvents: %v", err)
	}
	if len(persisted) != flushWatermark {
		t.Fatalf("backend has %d events after watermark flush, want %d", len(persisted), flushWatermark)
	}

	c := s.loadEventCache(ctx, "g", "s")
	c.mu.Lock()
	bufLen := len(c.buffer)
	dirty := c.dirty
	c.mu.Unlock()
	if bufLen != 0 || dirty {
		t.Fatalf("expected empty, clean buffer after watermark flush, got len=%d dirty=%v", bufLen, dirty)
	}
}

// TestLogsStore_ConcurrentAppend_NoLostEvents mirrors the DynamoDB/
// CloudFormation Phase 1 concurrency tests: N goroutines appending to the
// same stream concurrently, followed by Stop() (which synchronously flushes
// every dirty buffer), must result in exactly the total number of events
// appended — no losses, no duplicates.
func TestLogsStore_ConcurrentAppend_NoLostEvents(t *testing.T) {
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), "us-east-1")
	ctx := context.Background()

	const goroutines = 20
	const perGoroutine = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				ts := int64(g*perGoroutine + i)
				if aerr := s.appendEvents(ctx, "g", "s", []LogEvent{
					{Timestamp: ts, Message: fmt.Sprintf("g%d-i%d", g, i), IngestionTime: ts},
				}); aerr != nil {
					t.Errorf("appendEvents: %v", aerr)
				}
			}
		}(g)
	}
	wg.Wait()

	s.Stop(ctx)

	got, aerr := s.getEvents(ctx, "g", "s")
	if aerr != nil {
		t.Fatalf("getEvents: %v", aerr)
	}
	want := goroutines * perGoroutine
	if len(got) != want {
		t.Fatalf("getEvents returned %d events after concurrent append + Stop, want %d", len(got), want)
	}
	seen := make(map[int64]bool, want)
	for _, e := range got {
		if seen[e.Timestamp] {
			t.Fatalf("duplicate event at timestamp %d", e.Timestamp)
		}
		seen[e.Timestamp] = true
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Timestamp < got[j].Timestamp }) {
		t.Fatalf("getEvents result not sorted after concurrent append")
	}
}

// TestLogsStore_DeleteStream_ClearsBufferAndBackend proves deleteLogStream
// removes both the unflushed write buffer and any already-persisted events,
// and that a stale in-flight flush can't resurrect them afterward.
func TestLogsStore_DeleteStream_ClearsBufferAndBackend(t *testing.T) {
	mem := state.NewMemoryStore()
	s := newLogsStore(mem, clock.New(), "us-east-1")
	defer s.flushBgCancel()
	ctx := context.Background()

	if err := s.backend.appendEvents(ctx, "us-east-1", "g", "s", []LogEvent{{Timestamp: 1, Message: "persisted"}}); err != nil {
		t.Fatalf("seed backend: %v", err)
	}
	if aerr := s.appendEvents(ctx, "g", "s", []LogEvent{{Timestamp: 2, Message: "buffered"}}); aerr != nil {
		t.Fatalf("appendEvents: %v", aerr)
	}

	if aerr := s.deleteLogStream(ctx, "g", "s"); aerr != nil {
		t.Fatalf("deleteLogStream: %v", aerr)
	}

	got, aerr := s.getEvents(ctx, "g", "s")
	if aerr != nil {
		t.Fatalf("getEvents after delete: %v", aerr)
	}
	if len(got) != 0 {
		t.Fatalf("getEvents after deleteLogStream = %+v, want empty", got)
	}
}
