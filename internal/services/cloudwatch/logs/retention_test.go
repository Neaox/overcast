package logs

// Tests for storage-plan.md 3.4 — CloudWatch Logs RetentionInDays enforcement
// via a periodic background sweep that performs a ranged DELETE on the
// logs_events table (or an equivalent in-memory filter for memEventBackend).

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// ---- eventBackend.deleteEventsOlderThan: memory/SQL parity -----------------

// TestEventBackend_DeleteEventsOlderThan_MemoryAndSQL_Parity exercises the
// new eventBackend method directly against both implementations, proving:
//   - events at/after the cutoff survive, events strictly before it don't
//   - the delete is scoped to (region, group) — other groups and the same
//     group name in a different region are untouched
//   - behavior is identical between memEventBackend and sqlEventBackend
func TestEventBackend_DeleteEventsOlderThan_MemoryAndSQL_Parity(t *testing.T) {
	mem, sqlBackend := newTestBackends(t)

	for name, b := range map[string]eventBackend{"memory": mem, "sql": sqlBackend} {
		b := b
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			const region, group = "us-east-1", "my-group"

			if err := b.appendEvents(ctx, region, group, "stream-a", []LogEvent{
				{Timestamp: 100, Message: "old-a"},
				{Timestamp: 500, Message: "new-a"},
			}); err != nil {
				t.Fatalf("appendEvents stream-a: %v", err)
			}
			if err := b.appendEvents(ctx, region, group, "stream-b", []LogEvent{
				{Timestamp: 200, Message: "old-b"},
				{Timestamp: 600, Message: "new-b"},
			}); err != nil {
				t.Fatalf("appendEvents stream-b: %v", err)
			}
			// A different group in the same region must be untouched.
			if err := b.appendEvents(ctx, region, "other-group", "stream-c", []LogEvent{
				{Timestamp: 1, Message: "untouched-group"},
			}); err != nil {
				t.Fatalf("appendEvents other-group: %v", err)
			}
			// The same group name in a different region must be untouched.
			if err := b.appendEvents(ctx, "eu-west-1", group, "stream-a", []LogEvent{
				{Timestamp: 1, Message: "untouched-region"},
			}); err != nil {
				t.Fatalf("appendEvents other-region: %v", err)
			}

			// Cutoff at ts=300: events at 100/200 are before it (deleted),
			// events at 500/600 are at/after it (kept).
			cutoff := time.UnixMilli(300)
			if err := b.deleteEventsOlderThan(ctx, region, group, cutoff); err != nil {
				t.Fatalf("deleteEventsOlderThan: %v", err)
			}

			gotA, err := b.getEvents(ctx, region, group, "stream-a")
			if err != nil {
				t.Fatalf("getEvents stream-a: %v", err)
			}
			if !eventsEqual(gotA, []LogEvent{{Timestamp: 500, Message: "new-a"}}) {
				t.Fatalf("stream-a after delete = %+v, want only new-a", gotA)
			}

			gotB, err := b.getEvents(ctx, region, group, "stream-b")
			if err != nil {
				t.Fatalf("getEvents stream-b: %v", err)
			}
			if !eventsEqual(gotB, []LogEvent{{Timestamp: 600, Message: "new-b"}}) {
				t.Fatalf("stream-b after delete = %+v, want only new-b", gotB)
			}

			gotOtherGroup, err := b.getEvents(ctx, region, "other-group", "stream-c")
			if err != nil {
				t.Fatalf("getEvents other-group: %v", err)
			}
			if len(gotOtherGroup) != 1 {
				t.Fatalf("expected a different group to be untouched by deleteEventsOlderThan, got %+v", gotOtherGroup)
			}

			gotOtherRegion, err := b.getEvents(ctx, "eu-west-1", group, "stream-a")
			if err != nil {
				t.Fatalf("getEvents other-region: %v", err)
			}
			if len(gotOtherRegion) != 1 {
				t.Fatalf("expected the same group name in a different region to be untouched, got %+v", gotOtherRegion)
			}
		})
	}
}

// ---- logsStore.sweepExpiredEventsOnce: memory/SQL parity -------------------

// newTestLogsStores returns a memory-backed and a hybrid(SQL)-backed
// *logsStore sharing the same mock clock, for parity-testing
// sweepExpiredEventsOnce against both eventBackend implementations — mirrors
// event_backend_test.go's newTestBackends, one layer up.
func newTestLogsStores(t *testing.T) (memStore, sqlStore *logsStore, mock *clock.Mock) {
	t.Helper()
	mock = clock.NewMock()

	mem := state.NewMemoryStore()
	memStore = newLogsStore(mem, mock, "us-east-1")
	t.Cleanup(memStore.flushBgCancel)

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
	sqlStore = newLogsStore(hybrid, mock, "us-east-1")
	t.Cleanup(sqlStore.flushBgCancel)
	if _, ok := sqlStore.backend.(*sqlEventBackend); !ok {
		t.Fatalf("expected sqlEventBackend for a SQLite-backed store, got %T", sqlStore.backend)
	}

	return memStore, sqlStore, mock
}

// TestLogsStore_SweepExpiredEventsOnce_RespectsRetentionInDaysPerGroup proves:
//   - a group with RetentionInDays set prunes events older than that many
//     days, keeping recent ones
//   - a group with RetentionInDays == 0 (unset / DeleteRetentionPolicy's
//     reset value) is NEVER swept, however old its events are — real
//     CloudWatch Logs treats 0 as "never expire"
//   - different groups' retention windows are independent
//
// Run against both memEventBackend and sqlEventBackend.
func TestLogsStore_SweepExpiredEventsOnce_RespectsRetentionInDaysPerGroup(t *testing.T) {
	memStore, sqlStore, mock := newTestLogsStores(t)

	for name, s := range map[string]*logsStore{"memory": memStore, "sql": sqlStore} {
		s := s
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			base := mock.Now().UTC()

			// 1-day retention: a 2-day-old event must be pruned, a fresh one kept.
			if aerr := s.putLogGroup(ctx, &LogGroup{Name: "short-lived-" + name, RetentionInDays: 1}); aerr != nil {
				t.Fatalf("putLogGroup short-lived: %v", aerr)
			}
			if err := s.backend.appendEvents(ctx, "us-east-1", "short-lived-"+name, "stream", []LogEvent{
				{Timestamp: base.Add(-48 * time.Hour).UnixMilli(), Message: "old"},
				{Timestamp: base.UnixMilli(), Message: "recent"},
			}); err != nil {
				t.Fatalf("seed short-lived events: %v", err)
			}

			// RetentionInDays unset (0): must survive no matter how old.
			if aerr := s.putLogGroup(ctx, &LogGroup{Name: "forever-" + name, RetentionInDays: 0}); aerr != nil {
				t.Fatalf("putLogGroup forever: %v", aerr)
			}
			if err := s.backend.appendEvents(ctx, "us-east-1", "forever-"+name, "stream", []LogEvent{
				{Timestamp: base.Add(-365 * 24 * time.Hour).UnixMilli(), Message: "ancient"},
			}); err != nil {
				t.Fatalf("seed forever events: %v", err)
			}

			// 30-day retention: a 2-day-old event is well within the window and
			// must survive even though the short-lived group's sweep already ran.
			if aerr := s.putLogGroup(ctx, &LogGroup{Name: "long-lived-" + name, RetentionInDays: 30}); aerr != nil {
				t.Fatalf("putLogGroup long-lived: %v", aerr)
			}
			if err := s.backend.appendEvents(ctx, "us-east-1", "long-lived-"+name, "stream", []LogEvent{
				{Timestamp: base.Add(-48 * time.Hour).UnixMilli(), Message: "two-days-old"},
			}); err != nil {
				t.Fatalf("seed long-lived events: %v", err)
			}

			s.sweepExpiredEventsOnce(ctx)

			gotShort, err := s.backend.getEvents(ctx, "us-east-1", "short-lived-"+name, "stream")
			if err != nil {
				t.Fatalf("getEvents short-lived: %v", err)
			}
			if !eventsEqual(gotShort, []LogEvent{{Timestamp: base.UnixMilli(), Message: "recent"}}) {
				t.Fatalf("short-lived events after sweep = %+v, want only 'recent'", gotShort)
			}

			gotForever, err := s.backend.getEvents(ctx, "us-east-1", "forever-"+name, "stream")
			if err != nil {
				t.Fatalf("getEvents forever: %v", err)
			}
			if len(gotForever) != 1 {
				t.Fatalf("expected RetentionInDays=0 group to never be swept, got %+v", gotForever)
			}

			gotLong, err := s.backend.getEvents(ctx, "us-east-1", "long-lived-"+name, "stream")
			if err != nil {
				t.Fatalf("getEvents long-lived: %v", err)
			}
			if len(gotLong) != 1 {
				t.Fatalf("expected long-lived retention group's recent-enough event to survive, got %+v", gotLong)
			}
		})
	}
}

// ---- logsStore.sweepExpiredEventsOnce: expired empty stream metadata -------

// TestLogsStore_SweepExpiredEventsOnce_DeletesEmptyExpiredStreamMetadata
// proves the retention sweep also removes a log stream's *metadata* record
// once its LastEventTimestamp has aged out of the group's retention window
// and the stream has no events left anywhere (persisted or buffered) —
// mirroring real CloudWatch Logs, which eventually removes empty log
// streams. It also proves the sweep is conservative:
//   - a stream with LastEventTimestamp == 0 (never written, or metadata
//     lost) is never removed, however long the group's retention has been
//     in effect
//   - a stream that still has buffered (unflushed) events in its write
//     cache is never removed even if LastEventTimestamp looks expired
//   - a stream whose persisted events don't match its (stale) metadata is
//     never removed while it still has persisted events newer than cutoff
func TestLogsStore_SweepExpiredEventsOnce_DeletesEmptyExpiredStreamMetadata(t *testing.T) {
	memStore, sqlStore, mock := newTestLogsStores(t)

	for name, s := range map[string]*logsStore{"memory": memStore, "sql": sqlStore} {
		s := s
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			base := mock.Now().UTC()
			group := "g-" + name
			const region = "us-east-1"

			if aerr := s.putLogGroup(ctx, &LogGroup{Name: group, RetentionInDays: 1}); aerr != nil {
				t.Fatalf("putLogGroup: %v", aerr)
			}

			// expired-empty: LastEventTimestamp is well past the 1-day retention
			// window and the stream has no events anywhere. Must be deleted.
			if aerr := s.putLogStream(ctx, group, &LogStream{
				Name:               "expired-empty",
				LastEventTimestamp: base.Add(-48 * time.Hour).UnixMilli(),
			}); aerr != nil {
				t.Fatalf("putLogStream expired-empty: %v", aerr)
			}

			// never-written: LastEventTimestamp is the zero value. Must survive
			// no matter how long the group's retention has been in effect.
			if aerr := s.putLogStream(ctx, group, &LogStream{
				Name: "never-written",
			}); aerr != nil {
				t.Fatalf("putLogStream never-written: %v", aerr)
			}

			// recent: LastEventTimestamp is within the retention window. Must
			// survive.
			if aerr := s.putLogStream(ctx, group, &LogStream{
				Name:               "recent",
				LastEventTimestamp: base.UnixMilli(),
			}); aerr != nil {
				t.Fatalf("putLogStream recent: %v", aerr)
			}

			// expired-buffered: LastEventTimestamp is expired and there are no
			// persisted events, but the write cache still holds an unflushed
			// event. Must survive.
			if aerr := s.putLogStream(ctx, group, &LogStream{
				Name:               "expired-buffered",
				LastEventTimestamp: base.Add(-48 * time.Hour).UnixMilli(),
			}); aerr != nil {
				t.Fatalf("putLogStream expired-buffered: %v", aerr)
			}
			bufferedCache := s.loadEventCache(ctx, group, "expired-buffered")
			bufferedCache.mu.Lock()
			bufferedCache.buffer = []LogEvent{{Timestamp: base.Add(-48 * time.Hour).UnixMilli(), Message: "unflushed"}}
			bufferedCache.dirty = true
			bufferedCache.mu.Unlock()

			// expired-stale-metadata: LastEventTimestamp looks expired but the
			// backend still has a persisted event newer than cutoff (stale
			// metadata field). Must survive.
			if aerr := s.putLogStream(ctx, group, &LogStream{
				Name:               "expired-stale-metadata",
				LastEventTimestamp: base.Add(-48 * time.Hour).UnixMilli(),
			}); aerr != nil {
				t.Fatalf("putLogStream expired-stale-metadata: %v", aerr)
			}
			if err := s.backend.appendEvents(ctx, region, group, "expired-stale-metadata", []LogEvent{
				{Timestamp: base.UnixMilli(), Message: "still-here"},
			}); err != nil {
				t.Fatalf("seed expired-stale-metadata events: %v", err)
			}

			s.sweepExpiredEventsOnce(ctx)

			if _, aerr := s.getLogStream(ctx, group, "expired-empty"); aerr == nil {
				t.Fatalf("expected expired-empty stream metadata to be deleted")
			}
			for _, keep := range []string{"never-written", "recent", "expired-buffered", "expired-stale-metadata"} {
				if _, aerr := s.getLogStream(ctx, group, keep); aerr != nil {
					t.Fatalf("expected stream %q to survive the sweep, got error: %v", keep, aerr)
				}
			}
		})
	}
}

// ---- Service lifecycle: ticker-driven retention sweeper --------------------

// TestService_RetentionSweeper_TickDeletesExpiredEvents proves the actual
// ticker-driven background loop (not just sweepExpiredEventsOnce called
// directly) deletes expired events, using a mock clock so no real sleep is
// needed for the retention window/sweep interval — only a short bounded poll
// for the goroutine to observe the mock tick.
func TestService_RetentionSweeper_TickDeletesExpiredEvents(t *testing.T) {
	mock := clock.NewMock()
	store := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, store, zap.NewNop(), mock)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})

	ctx := context.Background()
	if aerr := svc.handler.store.putLogGroup(ctx, &LogGroup{Name: "g", RetentionInDays: 1}); aerr != nil {
		t.Fatalf("putLogGroup: %v", aerr)
	}
	base := mock.Now().UTC()
	if err := svc.handler.store.backend.appendEvents(ctx, "us-east-1", "g", "s", []LogEvent{
		{Timestamp: base.Add(-48 * time.Hour).UnixMilli(), Message: "old"},
	}); err != nil {
		t.Fatalf("seed events: %v", err)
	}

	// Advance past both the sweep interval and the group's retention window
	// in one jump so a single tick's sweep sees an already-expired event.
	mock.Add(retentionSweepInterval + 24*time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := svc.handler.store.backend.getEvents(ctx, "us-east-1", "g", "s")
		if err != nil {
			t.Fatalf("getEvents: %v", err)
		}
		if len(got) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background retention sweeper did not delete the expired event within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestService_Stop_WaitsForRetentionSweeperGoroutine proves Service.Stop
// (which calls logsStore.Stop) waits for the retention sweeper goroutine to
// exit — not just the debounce-flush goroutines — before returning. Run with
// -race to also catch data races between the sweeper and concurrent store
// access.
func TestService_Stop_WaitsForRetentionSweeperGoroutine(t *testing.T) {
	mock := clock.NewMock()
	store := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, store, zap.NewNop(), mock)

	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Stop(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Service.Stop did not return — retention sweeper goroutine likely leaked")
	}

	// A second Stop must be a safe no-op (logsStore.stopped guards re-entry).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.Stop(ctx)
}
