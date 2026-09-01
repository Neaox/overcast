//go:build !nosqlite

package cloudwatch

// The hybrid/persistent half of storage-plan.md 3.4's metric data retention
// tests, split from metric_retention_test.go because both need a real
// SQLite-backed store. Under -tags nosqlite, state.NewHybridStore and
// state.NewSQLiteStore are stubs that always return "not compiled with SQLite
// support" (internal/state/sqlite_hybrid_nosqlite.go), so these two cannot
// run there — the memory-backend baseline in metric_retention_test.go still
// does. Splitting mirrors metric_range_bench_test.go in this package.

import (
	"context"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/state"
)

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
