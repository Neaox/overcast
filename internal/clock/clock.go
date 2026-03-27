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
// No goroutines are started; all operations are synchronous.
type Mock = clock.Mock

// New returns a real wall-clock backed Clock.
// Use this in production NewXxx constructors and in main.go.
func New() Clock { return clock.New() }

// NewMock returns a test clock stopped at the Unix epoch.
// Call mock.Add(d) to advance time; use mock.Set(t) to jump.
func NewMock() *Mock { return clock.NewMock() }
