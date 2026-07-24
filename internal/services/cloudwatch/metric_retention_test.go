package cloudwatch

// Tests for storage-plan.md 3.4 — CloudWatch metric data retention enforced
// in every backend mode, via a periodic background sweep rather than only
// the memory-mode-only inline gate that used to exist.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func freshDataPoint(namespace, metricName string, ts time.Time, value float64) *MetricDataPoint {
	return &MetricDataPoint{
		Namespace:   namespace,
		MetricName:  metricName,
		Timestamp:   ts,
		SampleCount: 1,
		Sum:         value,
		Minimum:     value,
		Maximum:     value,
	}
}

// countMetricDataRows returns the number of raw persisted rows in
// cloudwatch:metricdata, bypassing any read-path filtering — used to prove
// the sweep performs a physical delete, not just hides stale rows from reads.
func countMetricDataRows(t *testing.T, ctx context.Context, s *cloudwatchStore) int {
	t.Helper()
	pairs, err := s.store.Scan(ctx, nsMetricData, "")
	if err != nil {
		t.Fatalf("Scan(%s): %v", nsMetricData, err)
	}
	return len(pairs)
}

// ---- sweepMetricDataOnce: memory backend (baseline / fast sanity check) ---

func TestSweepMetricDataOnce_MemoryBackend_DeletesStaleKeepsRecent(t *testing.T) {
	mock := clock.NewMock()
	store := state.NewMemoryStore()
	s := newCloudwatchStore(store, mock)
	ctx := context.Background()

	base := mock.Now().UTC()

	// A point written "now" (fresh, within the retention window).
	fresh := freshDataPoint("TestNS", "Fresh", base, 1)
	if err := s.putMetricDataPoint(ctx, fresh); err != nil {
		t.Fatalf("putMetricDataPoint(fresh): %v", err)
	}

	// Advance the clock so `fresh` is now old, WITHOUT any read on the
	// *same* metric — listMetricDataPoints deletes expired points as it
	// reads, which would mask whether the background sweep itself does the
	// work. (The write path deliberately never prunes — see
	// putMetricDataPoint.)
	mock.Add(memoryMetricDataRetention + time.Minute)

	// A point for a *different* metric, written after the clock advance, so
	// it stays within the (now-shifted) retention window and keeps the
	// pre-sweep row count deterministic at 2, isolating what the sweep
	// itself deletes.
	recent := freshDataPoint("TestNS", "Other", mock.Now().UTC(), 2)
	if err := s.putMetricDataPoint(ctx, recent); err != nil {
		t.Fatalf("putMetricDataPoint(recent): %v", err)
	}

	if got := countMetricDataRows(t, ctx, s); got != 2 {
		t.Fatalf("expected 2 raw rows before sweep, got %d", got)
	}

	s.sweepMetricDataOnce(ctx)

	if got := countMetricDataRows(t, ctx, s); got != 1 {
		t.Fatalf("expected 1 raw row after sweep (stale 'Fresh' point deleted, 'Other' kept), got %d", got)
	}
}

// ---- sweepMetricDataOnce: hybrid backend — proves the old backend-mode gate
// (removed) is actually gone, not just untested. ----------------------------

func TestSweepMetricDataOnce_HybridBackend_DeletesStalePoints(t *testing.T) {
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

	mock := clock.NewMock()
	s := newCloudwatchStore(hybrid, mock)
	ctx := context.Background()

	dp := freshDataPoint("TestNS", "CPUUtilization", mock.Now().UTC(), 42)
	if err := s.putMetricDataPoint(ctx, dp); err != nil {
		t.Fatalf("putMetricDataPoint: %v", err)
	}
	if got := countMetricDataRows(t, ctx, s); got != 1 {
		t.Fatalf("expected 1 raw row before advancing clock, got %d", got)
	}

	// Advance well past the retention window with no further reads/writes to
	// this metric, then sweep directly — no ticker involved.
	mock.Add(memoryMetricDataRetention + time.Minute)
	s.sweepMetricDataOnce(ctx)

	if got := countMetricDataRows(t, ctx, s); got != 0 {
		t.Fatalf("expected background sweep to physically delete the stale row in hybrid mode, got %d remaining", got)
	}
}

// ---- sweepMetricDataOnce: persistent (SQLite) backend ----------------------

func TestSweepMetricDataOnce_PersistentBackend_DeletesStalePoints(t *testing.T) {
	dir := t.TempDir()
	sqliteStore, err := state.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		if err := sqliteStore.Close(); err != nil {
			t.Logf("sqliteStore.Close: %v", err)
		}
	})

	mock := clock.NewMock()
	s := newCloudwatchStore(sqliteStore, mock)
	ctx := context.Background()

	dp := freshDataPoint("TestNS", "CPUUtilization", mock.Now().UTC(), 42)
	if err := s.putMetricDataPoint(ctx, dp); err != nil {
		t.Fatalf("putMetricDataPoint: %v", err)
	}
	if got := countMetricDataRows(t, ctx, s); got != 1 {
		t.Fatalf("expected 1 raw row before advancing clock, got %d", got)
	}

	mock.Add(memoryMetricDataRetention + time.Minute)
	s.sweepMetricDataOnce(ctx)

	if got := countMetricDataRows(t, ctx, s); got != 0 {
		t.Fatalf("expected background sweep to physically delete the stale row in persistent mode, got %d remaining", got)
	}
}

// ---- Service lifecycle: ticker-driven sweeper starts and stops cleanly ----

// TestService_MetricDataSweeper_TickDeletesStalePoints proves the actual
// ticker-driven background loop (not just the extracted sweep function)
// deletes stale points, using a mock clock so no real sleep is required for
// the retention window/interval math — only a short bounded poll for the
// goroutine to observe the mock tick.
func TestService_MetricDataSweeper_TickDeletesStalePoints(t *testing.T) {
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
	dp := freshDataPoint("TestNS", "CPUUtilization", mock.Now().UTC(), 42)
	if err := svc.store.putMetricDataPoint(ctx, dp); err != nil {
		t.Fatalf("putMetricDataPoint: %v", err)
	}

	// Advance past both the sweep interval and the retention window in one
	// jump so a single tick's sweep sees an already-stale point.
	mock.Add(metricDataSweepInterval + memoryMetricDataRetention + time.Minute)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if countMetricDataRows(t, ctx, svc.store) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background sweeper did not delete the stale point within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestService_Stop_NoGoroutineLeak proves Service.Stop waits for both
// background loops (alarm evaluator + metric data sweeper) to exit before
// returning — run with -race to also catch any data races between them and
// the store.
func TestService_Stop_NoGoroutineLeak(t *testing.T) {
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
		t.Fatal("Service.Stop did not return — background goroutine(s) likely leaked")
	}

	// Calling Stop again must be a safe no-op (stopOnce), not hang or panic.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.Stop(ctx)
}
