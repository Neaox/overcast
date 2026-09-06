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
//
// Entries are held and compared by pointer, so a callback that was superseded
// while it was firing can tell that the entry now filed under its key is not
// the one it belongs to.
type cancelEntry struct {
	timer    *clock.Timer
	deadline time.Time
	done     chan struct{}
	// settled records that some party has taken responsibility for finishing
	// this entry — balancing the wg.Add After made for it, and closing done.
	// Exactly one party ever does; see claimLocked. Guarded by Scheduler.mu.
	settled bool
}

// Scheduler manages keyed delayed callbacks. Each service creates its own
// Scheduler instance (no global state, DI-friendly).
type Scheduler struct {
	clk     clock.Clock
	mu      sync.Mutex
	pending map[string]*cancelEntry
	// inflight holds the completion channels of callbacks that have fired but
	// not finished. A callback leaves pending before it runs fn, so without
	// this a settle that arrives mid-callback would find nothing to wait for
	// and return while the transition was still working.
	inflight map[chan struct{}]struct{}
	wg       sync.WaitGroup
	// stopped is set under mu by Stop, before it spawns the goroutine that
	// waits on wg. Every After checks it under the same mu before it ever
	// touches wg, so once Stop's critical section has run, no After call still
	// to come can call wg.Add: it either finished its own critical section
	// (and so its Add, if any) strictly before Stop's ran, or it is blocked on
	// mu until Stop's has, and then observes stopped and skips wg entirely.
	// That gives wg.Add and wg.Wait — which Stop calls without holding mu, so
	// it does not block callbacks already in flight — a happens-before edge
	// that mu alone would not, closing the race in #1282 where a callback
	// still in flight rescheduled itself (After calling After) concurrently
	// with Stop's wait.
	stopped bool
}

// NewScheduler creates a Scheduler using the given clock.
// Production: clock.New(). Tests: clock.NewMock() for instant time skips.
//
// A Scheduler built on a mock clock records itself against that clock, so a
// test that holds only the clock can settle it — see mockclock.go. One built
// on a real clock records nothing.
func NewScheduler(clk clock.Clock) *Scheduler {
	s := &Scheduler{
		clk:      clk,
		pending:  make(map[string]*cancelEntry),
		inflight: make(map[chan struct{}]struct{}),
	}
	if mock, ok := clk.(*clock.Mock); ok {
		registerMockScheduler(mock, s)
	}
	return s
}

// claimLocked takes ownership of finishing entry, and reports whether this
// caller is the one that got it. The winner — and only the winner — balances
// the wg.Add After made for the entry and closes its done channel.
//
// Ownership has to be settled here rather than inferred from Timer.Stop,
// because Stop's answer is not the proof it looks like. The mock clock chooses
// the next timer, releases its lock, and only then ticks it, so a Stop landing
// in that window reports true for a timer whose callback is already on its way;
// a real timer's Stop reports false once its function has started but does not
// wait for it. Either way both parties can believe they own the entry, and the
// second close of done panics the process — which is how this reached CI, as a
// bare `panic: close of closed channel` with no failing test to name it.
//
// s.mu must be held.
func (s *Scheduler) claimLocked(entry *cancelEntry) bool {
	if entry.settled {
		return false
	}
	entry.settled = true
	return true
}

// releaseLocked is the cancelling half of that rule, shared by Cancel, Stop
// and After's replacement of an existing entry: stop the timer if it has not
// fired, and finish the entry if no callback has claimed it first. A caller
// that loses the claim does nothing — the callback owns the entry and will
// finish it — and a callback that loses to this one stands down without
// running fn, so a cancelled transition stays cancelled.
//
// s.mu must be held, and the entry must already be out of s.pending.
func (s *Scheduler) releaseLocked(entry *cancelEntry) {
	entry.timer.Stop()
	if s.claimLocked(entry) {
		s.wg.Done()
		close(entry.done)
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
//
// After after Stop is a defined no-op: it neither runs fn nor schedules it,
// including the 0-delay inline fast path. That is deliberate — a service's
// shutdown must be able to rely on nothing it manages through this Scheduler
// starting new work once Stop has been called, and a callback that
// reschedules itself (as readiness.Watch's health-check loop does) must stop
// doing so rather than run forever underneath a stopped service.
func (s *Scheduler) After(key string, delay time.Duration, fn func()) {
	s.mu.Lock()

	if s.stopped {
		s.mu.Unlock()
		return
	}

	// Cancel existing timer for this key if present.
	if existing, ok := s.pending[key]; ok {
		delete(s.pending, key)
		s.releaseLocked(existing)
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
	entry := &cancelEntry{deadline: s.clk.Now().Add(delay), done: done}
	entry.timer = s.clk.AfterFunc(delay, func() {
		// Claim the entry and move it from pending to inflight in one step, so
		// a settle running alongside this sees the transition in exactly one of
		// them and never in neither.
		s.mu.Lock()
		// Only if this key still holds this entry: a replacement scheduled
		// while this callback was firing owns the key now, and taking it out
		// of pending would orphan it — still armed, no longer cancellable, and
		// holding a wg.Add nothing will balance.
		if s.pending[key] == entry {
			delete(s.pending, key)
		}
		if !s.claimLocked(entry) {
			// A Cancel, a Stop, or a replacement got here first and has
			// already finished the entry. It cancelled this transition, so fn
			// must not run — and this callback must neither close done nor
			// touch the WaitGroup, both of which the winner has done.
			s.mu.Unlock()
			return
		}
		s.inflight[done] = struct{}{}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.inflight, done)
			s.mu.Unlock()
			close(done)
			s.wg.Done()
		}()
		fn()
	})

	s.pending[key] = entry
	s.mu.Unlock()
}

// Cancel cancels a pending transition by key. Returns true if a pending
// transition was found and cancelled.
//
// True means fn will not run. A transition whose timer has fired but whose
// callback has not yet claimed the entry is still pending, so it is cancelled
// too and its callback stands down. One already running fn is not pending:
// Cancel reports false and does not interrupt it.
func (s *Scheduler) Cancel(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.pending[key]
	if !ok {
		return false
	}
	delete(s.pending, key)
	s.releaseLocked(entry)
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

// Settle blocks until every transition outstanding when it was called has run
// to completion or been cancelled. It cancels nothing — on a real clock it
// waits the delays out — so a test can let the real transitions happen and then
// read what they produced, rather than polling until they show up.
//
// Outstanding covers both the transitions still pending and the callbacks that
// have already fired and are still running, so a test whose setup ran long
// enough for some of its transitions to come due does not silently get a
// partial wait. Transitions scheduled after the call are not waited for.
func (s *Scheduler) Settle() {
	for _, done := range s.dueBy(time.Time{}) {
		<-done
	}
}

// dueBy returns the completion channels of the transitions due at or before
// target. A zero target means every pending transition.
//
// Everything in flight is included whatever the target: a callback only reaches
// inflight by coming due, so it is due by definition, and it is no longer in
// pending to be found there.
func (s *Scheduler) dueBy(target time.Time) []chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	due := make([]chan struct{}, 0, len(s.pending)+len(s.inflight))
	for done := range s.inflight {
		due = append(due, done)
	}
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

// Stop marks the scheduler stopped, cancels all pending transitions, and
// waits for any in-flight callbacks to complete. Respects ctx for timeout on
// the wait.
//
// stopped is set inside this same critical section, before any pending
// transition is cancelled and before the wg.Wait below. That ordering is what
// makes wg.Add and wg.Wait race-free without either taking mu: every After
// call that has not yet reached its own critical section when Stop reaches
// this one will, once it does, see stopped and return without calling
// wg.Add — so by the time this critical section ends, no more Adds are
// coming, and it is safe to wait.
func (s *Scheduler) Stop(ctx context.Context) {
	s.mu.Lock()
	s.stopped = true
	for key, entry := range s.pending {
		delete(s.pending, key)
		s.releaseLocked(entry)
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
