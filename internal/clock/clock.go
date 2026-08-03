// Package clock provides a thin re-export of github.com/benbjohnson/clock so
// the rest of the codebase imports a single internal path and is insulated from
// upstream API changes.
//
// # Usage — production code
//
//	type Handler struct {
//	    clk clock.Clock
//	    // ...
//	}
//
//	func NewHandler(clk clock.Clock /* ... */) *Handler {
//	    return &Handler{clk: clk}
//	}
//
//	// Use clk.Now() everywhere, never time.Now() directly.
//
// # Usage — tests
//
//	mock := clock.NewMock()
//	h := NewHandler(mock)
//
//	mock.Add(35 * time.Second)  // instant — no real sleep required
package clock

import "github.com/benbjohnson/clock"

// Clock is the injectable time-source interface.
// Production code receives clock.New(); tests receive clock.NewMock().
//
//   - Now()              — current time
//   - After(d)           — channel that fires after d (like time.After)
//   - NewTicker(d)       — like time.NewTicker; must be stopped
//   - NewTimer(d)        — like time.NewTimer; must be stopped or reset
//   - Sleep(d)           — like time.Sleep (instantly skippable in tests)
//   - Since(t)           — shorthand for Now().Sub(t)
//   - Until(t)           — shorthand for t.Sub(Now())
type Clock = clock.Clock

// Mock is a manually-controlled clock for use in tests.
// Advance time with mock.Add(d) or jump with mock.Set(t).
//
// # AfterFunc callbacks are not synchronous — mock.Add does not wait for them
//
// mock.Add runs each due timer in turn, but an AfterFunc callback it fires runs
// on a goroutine of its own; all Add does before returning is sleep one
// millisecond. So this is a race, not a sequence:
//
//	mock.Add(time.Second) // fires the transition
//	got := store.Get(...) // may run before the callback does
//
// It passes on an idle machine and fails on a loaded one, which makes it a
// flake that only ever reproduces in CI — and one that reads like whatever the
// callback was supposed to have done being broken, rather than not having
// happened yet. Anything scheduled through lifecycle.Scheduler should be
// advanced with its AdvanceAndSettle, which waits for the callbacks it fired.
// Elsewhere, wait on something the callback itself signals; never on a sleep.
//
// Timer and Ticker channels are different: a mock sends on the channel from
// inside Add, so a test blocked on a receive is ordered after the tick. Both
// channels hold a single value — see Ticker for what that costs a ticker.
type Mock = clock.Mock

// Timer is the handle returned by Clock.AfterFunc / Clock.Timer.
type Timer = clock.Timer

// Ticker is the handle returned by Clock.Ticker. Name it when a background
// loop must create its ticker on the constructing goroutine rather than
// inside the spawned one — a mock clock only delivers ticks to tickers that
// already existed when it was advanced. A mock also drops a tick rather than
// queueing it when the one-value channel is still full, so advancing by several
// intervals at once delivers one tick and not several.
type Ticker = clock.Ticker

// New returns a real wall-clock backed Clock.
// Use this in production NewXxx constructors and in main.go.
func New() Clock { return clock.New() }

// NewMock returns a test clock stopped at the Unix epoch.
// Call mock.Add(d) to advance time; use mock.Set(t) to jump.
func NewMock() *Mock { return clock.NewMock() }
