//go:build !nosqlite

package state

// Failing-first coverage for storage-pressure-handling items 1 (throttling
// sentinel tagging) and 4 (read-retry/timeout observability counters).
// White-box (package state) because it needs direct access to
// wrapStorePressure, retrySQLiteGet, and the readRetryCount/readTimeoutCount
// counters — none of which are exported, by design (see ErrStorePressure's
// doc comment in store.go: the only thing any other package needs is the
// exported sentinel and the DebugMetrics fields).

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestHybridStore_WrapStorePressure_TagsExhaustedRetryableErrors proves
// wrapStorePressure only tags the retryable-busy/deadline class
// shouldRetryHybridSQLiteRead recognizes, and increments readTimeoutCount
// exactly when it does — a genuine (non-retryable) error must pass through
// unchanged and must NOT bump the counter, per the "do NOT tag genuine
// corruption/infrastructure errors" rule.
func TestHybridStore_WrapStorePressure_TagsExhaustedRetryableErrors(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()

	// Given: a retryable-class error (what retrySQLiteGet's exhaustion path
	// returns after busy_timeout/context deadline).
	retryable := fmt.Errorf("hybrid sqlite get [ns/key]: %w", context.DeadlineExceeded)

	// When: it goes through wrapStorePressure.
	wrapped := s.wrapStorePressure(retryable)

	// Then: the sentinel is attached and the timeout counter increments.
	if !errors.Is(wrapped, ErrStorePressure) {
		t.Fatalf("wrapStorePressure(%v) = %v, want it to wrap ErrStorePressure", retryable, wrapped)
	}
	if !errors.Is(wrapped, context.DeadlineExceeded) {
		t.Fatalf("wrapStorePressure(%v) = %v, want the original cause preserved in the chain", retryable, wrapped)
	}
	if got := s.readTimeoutCount.Load(); got != 1 {
		t.Fatalf("readTimeoutCount after one exhausted retryable read = %d, want 1", got)
	}

	// Given/When: a genuine, non-retryable infrastructure error.
	genuine := errors.New("disk I/O error")
	unchanged := s.wrapStorePressure(genuine)

	// Then: it is NOT tagged, and the counter does not move.
	if errors.Is(unchanged, ErrStorePressure) {
		t.Fatalf("wrapStorePressure(%v) = %v, must not tag a genuine infrastructure error", genuine, unchanged)
	}
	if unchanged != genuine {
		t.Fatalf("wrapStorePressure(%v) = %v, want the genuine error returned unchanged", genuine, unchanged)
	}
	if got := s.readTimeoutCount.Load(); got != 1 {
		t.Fatalf("readTimeoutCount after a genuine (non-pressure) error = %d, want unchanged at 1", got)
	}

	// And: nil passes through untouched.
	if s.wrapStorePressure(nil) != nil {
		t.Fatal("wrapStorePressure(nil) must return nil")
	}
}

// TestHybridStore_RetrySQLiteGet_IncrementsReadRetryCount proves the
// per-attempt read-retry counter increments once per retry iteration.
// Uses an already-canceled context passed directly to retrySQLiteGet (the
// unexported retry-loop function, not the public Get path) to force exactly
// one deterministic retry-then-give-up cycle without waiting on real SQLite
// lock contention or the full hybridSQLiteReadRetryTimeout window.
func TestHybridStore_RetrySQLiteGet_IncrementsReadRetryCount(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	if err := s.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	before := s.readRetryCount.Load()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = s.retrySQLiteGet(canceled, "svc", "missing-key")

	// A canceled context makes the very first raw attempt fail with
	// context.Canceled, which shouldRetryHybridSQLiteRead treats as
	// retryable; sleepHybridSQLiteRetry then observes the same canceled
	// context and returns immediately, ending the loop after exactly one
	// counted attempt.
	if err == nil {
		t.Fatal("retrySQLiteGet over an already-canceled context should have returned an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retrySQLiteGet error = %v, want it to carry context.Canceled", err)
	}
	if after := s.readRetryCount.Load(); after != before+1 {
		t.Fatalf("readRetryCount = %d, want %d (exactly one retry attempt)", after, before+1)
	}
}

// TestHybridStore_DebugMetrics_ExposesReadCounters proves the read-retry and
// read-timeout counters surface through DebugMetrics (and therefore
// GET /_overcast/debug/metrics — see internal/router/debug.go's debugMetrics, which
// forwards state.DebugMetrics verbatim).
func TestHybridStore_DebugMetrics_ExposesReadCounters(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	zero := s.DebugMetrics(ctx, DebugMetricsOptions{})
	if zero.ReadRetryCount != 0 || zero.ReadTimeoutCount != 0 {
		t.Fatalf("DebugMetrics before any contention = %+v, want both counters at 0", zero)
	}

	// Inject one retry attempt and one exhausted (pressure-tagged) read via
	// the same mechanisms the two tests above exercise directly.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, _, _ = s.retrySQLiteGet(canceled, "svc", "missing-key")
	_ = s.wrapStorePressure(fmt.Errorf("hybrid sqlite get [ns/key]: %w", context.DeadlineExceeded))

	after := s.DebugMetrics(ctx, DebugMetricsOptions{})
	if after.ReadRetryCount != 1 {
		t.Errorf("DebugMetrics.ReadRetryCount = %d, want 1", after.ReadRetryCount)
	}
	if after.ReadTimeoutCount != 1 {
		t.Errorf("DebugMetrics.ReadTimeoutCount = %d, want 1", after.ReadTimeoutCount)
	}
}
