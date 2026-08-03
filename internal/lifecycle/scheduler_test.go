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

func TestScheduler_AdvanceAndSettle_waitsForTheCallbackToFinish(t *testing.T) {
	// Given a transition whose callback takes longer than the millisecond a
	// bare mock.Add yields for
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var fired atomic.Bool
	s.After("slow", 1*time.Second, func() {
		time.Sleep(50 * time.Millisecond)
		fired.Store(true)
	})

	// When we advance the clock through it and settle
	s.AdvanceAndSettle(mock, 1*time.Second)

	// Then the callback has finished, not merely started
	if !fired.Load() {
		t.Fatal("AdvanceAndSettle returned while the callback was still running")
	}
	if s.PendingCount() != 0 {
		t.Fatalf("expected 0 pending, got %d", s.PendingCount())
	}
}

func TestScheduler_AdvanceAndSettle_transitionNotYetDue(t *testing.T) {
	// Given one transition inside the advance and one well beyond it
	mock := clock.NewMock()
	s := NewScheduler(mock)

	var due, later atomic.Bool
	s.After("due", 1*time.Second, func() { due.Store(true) })
	s.After("later", 1*time.Hour, func() { later.Store(true) })

	// When we advance past the first only
	s.AdvanceAndSettle(mock, 2*time.Second)

	// Then it settled the one that came due and did not wait for the other
	if !due.Load() {
		t.Fatal("expected the due transition to have run")
	}
	if later.Load() {
		t.Fatal("expected the later transition not to have run")
	}
	if s.PendingCount() != 1 {
		t.Fatalf("expected 1 pending, got %d", s.PendingCount())
	}
}

func TestScheduler_Settle_realClock(t *testing.T) {
	// Given a transition on a real clock
	s := NewScheduler(clock.New())

	var fired atomic.Bool
	s.After("real", 10*time.Millisecond, func() { fired.Store(true) })

	// When we settle, which waits the delay out
	s.Settle()

	// Then the callback has run
	if !fired.Load() {
		t.Fatal("Settle returned before the transition ran")
	}
}

func TestScheduler_Settle_callbackAlreadyRunning(t *testing.T) {
	// Given a transition that has already fired and is still in its callback —
	// so it has left the pending map, which is where a settle looks
	s := NewScheduler(clock.New())

	started := make(chan struct{})
	var finished atomic.Bool
	s.After("inflight", time.Millisecond, func() {
		close(started)
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
	})
	<-started

	// When we settle
	s.Settle()

	// Then it waited for the callback in flight rather than finding nothing
	// pending and returning straight away
	if !finished.Load() {
		t.Fatal("Settle returned while a fired callback was still running")
	}
}

func TestScheduler_Settle_transitionCancelled(t *testing.T) {
	// Given a waiter on a transition that is never going to come due
	mock := clock.NewMock()
	s := NewScheduler(mock)
	s.After("never", 1*time.Hour, func() {})

	settled := make(chan struct{})
	go func() {
		s.Settle()
		close(settled)
	}()

	// When the scheduler is stopped, cancelling it
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Stop(ctx)

	// Then the waiter is released rather than waiting for a callback that will
	// never run
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("Settle did not return after the transition was cancelled")
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
