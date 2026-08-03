// Package lifecycle provides a shared, testable scheduler for async state
// transitions. EC2 instances, ECS tasks, and RDS instances all need delayed
// state changes (e.g. pending → running, creating → available). This package
// provides a single abstraction instead of each service hand-rolling
// time.AfterFunc/goroutine/cancel patterns.
package lifecycle

import (
	"context"
	"sync"

	"github.com/benbjohnson/clock"

	"time"
)

// cancelEntry holds the cancel mechanism for a pending transition, plus what
// AdvanceAndSettle needs to wait for it: when it comes due, and a channel
// closed once it is no longer outstanding — because it ran, or because it was
// cancelled before it could.
type cancelEntry struct {
	timer    *clock.Timer
	deadline time.Time
	done     chan struct{}
}

// Scheduler manages keyed delayed callbacks. Each service creates its own
// Scheduler instance (no global state, DI-friendly).
type Scheduler struct {
	clk     clock.Clock
	mu      sync.Mutex
	pending map[string]cancelEntry
	wg      sync.WaitGroup
}

// NewScheduler creates a Scheduler using the given clock.
// Production: clock.New(). Tests: clock.NewMock() for instant time skips.
func NewScheduler(clk clock.Clock) *Scheduler {
	return &Scheduler{
		clk:     clk,
		pending: make(map[string]cancelEntry),
	}
}

// After schedules fn to run after delay. Key identifies the transition
// (e.g. "i-abc123:terminate"). If a transition with the same key is already
// pending, it is cancelled before the new one is scheduled.
//
// When delay is 0 and the clock is a real clock (not a mock), fn is executed
// synchronously within this call.  This ensures that subsequent API calls
// immediately see the updated state instead of racing with a goroutine.
// With a mock clock, 0-delay timers remain pending until clock.Add is called,
// preserving test-time control.
func (s *Scheduler) After(key string, delay time.Duration, fn func()) {
	s.mu.Lock()

	// Cancel existing timer for this key if present.
	if existing, ok := s.pending[key]; ok {
		if existing.timer.Stop() {
			s.wg.Done()
			close(existing.done)
		}
		delete(s.pending, key)
	}

	// Fast path: 0-delay + real clock → run inline.
	if delay == 0 {
		if _, isMock := s.clk.(*clock.Mock); !isMock {
			s.mu.Unlock()
			fn()
			return
		}
	}

	s.wg.Add(1)
	done := make(chan struct{})
	timer := s.clk.AfterFunc(delay, func() {
		defer s.wg.Done()
		defer close(done)
		s.mu.Lock()
		delete(s.pending, key)
		s.mu.Unlock()
		fn()
	})

	s.pending[key] = cancelEntry{timer: timer, deadline: s.clk.Now().Add(delay), done: done}
	s.mu.Unlock()
}

// Cancel cancels a pending transition by key. Returns true if a pending
// transition was found and cancelled.
func (s *Scheduler) Cancel(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.pending[key]
	if !ok {
		return false
	}
	stopped := entry.timer.Stop()
	delete(s.pending, key)
	if stopped {
		// Timer was stopped before firing — balance the wg.Add from After.
		s.wg.Done()
		close(entry.done)
	}
	return true
}

// AdvanceAndSettle advances a mock clock by d and returns only once every
// transition that came due has run to completion. mock must be the clock this
// Scheduler was built with.
//
// It exists because a bare mock.Add is not enough to observe the effects of a
// transition. The mock clock runs an AfterFunc callback on a goroutine of its
// own and then yields for a single millisecond, so a test that advances the
// clock and reads the store on the next line is racing the callback: it wins on
// an idle machine and loses on a loaded one, which is a flake that only ever
// reproduces in CI. Waiting on the callback itself is deterministic regardless
// of load.
//
// Transitions a callback schedules while this runs are not waited for, even if
// they fall due within d — settling is defined over the transitions that were
// outstanding when the call began.
func (s *Scheduler) AdvanceAndSettle(mock *clock.Mock, d time.Duration) {
	due := s.dueBy(mock.Now().Add(d))
	mock.Add(d)
	for _, done := range due {
		<-done
	}
}

// Settle blocks until every transition pending when it was called has run to
// completion or been cancelled. It cancels nothing — on a real clock it waits
// the delays out — so a test can let the real transitions happen and then read
// what they produced, rather than polling until they show up.
//
// Only transitions still pending when Settle is called are waited for; one that
// has already fired is no longer pending and is not waited for. Call it before
// the delays elapse, which for a test that has just made the request that
// scheduled them is where it naturally goes.
func (s *Scheduler) Settle() {
	for _, done := range s.dueBy(time.Time{}) {
		<-done
	}
}

// dueBy returns the completion channels of the pending transitions due at or
// before target. A zero target means every pending transition.
func (s *Scheduler) dueBy(target time.Time) []chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	due := make([]chan struct{}, 0, len(s.pending))
	for _, entry := range s.pending {
		if target.IsZero() || !entry.deadline.After(target) {
			due = append(due, entry.done)
		}
	}
	return due
}

// PendingCount returns the number of currently scheduled transitions.
func (s *Scheduler) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// Stop cancels all pending transitions and waits for any in-flight callbacks
// to complete. Respects ctx for timeout on the wait.
func (s *Scheduler) Stop(ctx context.Context) {
	s.mu.Lock()
	for key, entry := range s.pending {
		if entry.timer.Stop() {
			s.wg.Done()
			close(entry.done)
		}
		delete(s.pending, key)
	}
	s.mu.Unlock()

	// Wait for in-flight callbacks with context timeout.
	// NOTE: If ctx expires before wg.Wait() completes, the waiter goroutine
	// persists until all in-flight callbacks finish (not a permanent leak).
	// Stop() should only be called once per Scheduler.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}
