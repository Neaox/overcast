package lifecycle

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

func TestScheduler_After_fires(t *testing.T) {
	// Given a scheduler with a mock clock
	mock := clock.NewMock()
	s := NewScheduler(mock)

	// When we schedule a callback after 1s
	var fired atomic.Bool
	s.After("test-key", 1*time.Second, func() {
		fired.Store(true)
	})

	// Then the callback has not fired yet
	if fired.Load() {
		t.Fatal("expected callback not to fire immediately")
	}
	if s.PendingCount() != 1 {
		t.Fatalf("expected 1 pending, got %d", s.PendingCount())
	}

	// When we advance the clock past the delay
	mock.Add(1*time.Second + time.Millisecond)

	// Then the callback fires (give goroutine a moment)
	time.Sleep(10 * time.Millisecond)
	if !fired.Load() {
		t.Fatal("expected callback to fire after delay")
	}
	if s.PendingCount() != 0 {
		t.Fatalf("expected 0 pending, got %d", s.PendingCount())
	}
}

func TestScheduler_Cancel(t *testing.T) {
	// Given a scheduler with a pending transition
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var fired atomic.Bool
	s.After("cancel-me", 5*time.Second, func() {
		fired.Store(true)
	})

	// When we cancel it
	ok := s.Cancel("cancel-me")
	if !ok {
		t.Fatal("expected Cancel to return true")
	}

	// Then advancing the clock does not fire it
	mock.Add(10 * time.Second)
	time.Sleep(10 * time.Millisecond)
	if fired.Load() {
		t.Fatal("expected cancelled callback not to fire")
	}
	if s.PendingCount() != 0 {
		t.Fatalf("expected 0 pending, got %d", s.PendingCount())
	}
}

func TestScheduler_Cancel_notFound(t *testing.T) {
	mock := clock.NewMock()
	s := NewScheduler(mock)

	if s.Cancel("nonexistent") {
		t.Fatal("expected Cancel to return false for nonexistent key")
	}
}

func TestScheduler_After_replaces(t *testing.T) {
	// Given a scheduler with a pending transition
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var firstFired, secondFired atomic.Bool
	s.After("same-key", 5*time.Second, func() {
		firstFired.Store(true)
	})

	// When we schedule a new one with the same key
	s.After("same-key", 3*time.Second, func() {
		secondFired.Store(true)
	})

	// Then only one is pending
	if s.PendingCount() != 1 {
		t.Fatalf("expected 1 pending, got %d", s.PendingCount())
	}

	// When we advance past the new delay
	mock.Add(3*time.Second + time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	// Then only the second callback fires
	if firstFired.Load() {
		t.Fatal("expected first callback to be cancelled")
	}
	if !secondFired.Load() {
		t.Fatal("expected second callback to fire")
	}
}

func TestScheduler_Stop(t *testing.T) {
	// Given a scheduler with multiple pending transitions
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var count atomic.Int32
	s.After("a", 1*time.Second, func() { count.Add(1) })
	s.After("b", 2*time.Second, func() { count.Add(1) })
	s.After("c", 3*time.Second, func() { count.Add(1) })

	// When we stop the scheduler
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Stop(ctx)

	// Then no callbacks fire even after advancing
	mock.Add(10 * time.Second)
	time.Sleep(10 * time.Millisecond)
	if count.Load() != 0 {
		t.Fatalf("expected 0 callbacks to fire, got %d", count.Load())
	}
	if s.PendingCount() != 0 {
		t.Fatalf("expected 0 pending, got %d", s.PendingCount())
	}
}

func TestScheduler_After_replace_does_not_hang_Stop(t *testing.T) {
	// Given a scheduler with a pending transition
	mock := clock.NewMock()
	s := NewScheduler(mock)

	s.After("x", 5*time.Second, func() {})

	// When we replace that key with a new transition
	s.After("x", 3*time.Second, func() {})

	// Then Stop completes without hanging (the replaced entry's WaitGroup is balanced)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Stop(ctx)

	if ctx.Err() != nil {
		t.Fatal("Stop() hung — WaitGroup imbalance when replacing a pending transition")
	}
}

func TestScheduler_multipleKeys(t *testing.T) {
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var a, b atomic.Bool
	s.After("key-a", 1*time.Second, func() { a.Store(true) })
	s.After("key-b", 2*time.Second, func() { b.Store(true) })

	if s.PendingCount() != 2 {
		t.Fatalf("expected 2 pending, got %d", s.PendingCount())
	}

	// Advance past first but not second
	mock.Add(1*time.Second + time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	if !a.Load() {
		t.Fatal("expected key-a to fire")
	}
	if b.Load() {
		t.Fatal("expected key-b not to fire yet")
	}
	if s.PendingCount() != 1 {
		t.Fatalf("expected 1 pending, got %d", s.PendingCount())
	}

	// Advance past second
	mock.Add(1*time.Second + time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	if !b.Load() {
		t.Fatal("expected key-b to fire")
	}
	if s.PendingCount() != 0 {
		t.Fatalf("expected 0 pending, got %d", s.PendingCount())
	}
}
