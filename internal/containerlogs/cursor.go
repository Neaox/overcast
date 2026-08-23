package containerlogs

import (
	"sync"
	"time"
)

// cursor is a follower's high watermark in one container's log history: the
// timestamp of the newest line delivered, and how many lines carried exactly
// that timestamp.
//
// The count is the whole point. Resuming a follow is by timestamp and nothing
// else — the daemon's `since` takes no sequence number — and it is inclusive,
// so asking to resume from the newest line replays that line. Asking to resume
// from one nanosecond earlier replays every line sharing that nanosecond
// instead, which is a set the follower can recognise by counting: it already
// delivered hwCount of them, so the first hwCount that come back are the ones
// it has seen and the rest are new. Two lines written in the same nanosecond is
// not a hypothetical — a runtime that prints a multi-line traceback does it
// every time.
type cursor struct {
	mu      sync.Mutex
	hwNanos int64
	hwCount int
}

// cursorAdmission decides, for one connection, which of its lines are new.
// A connection opened with a `since` is a replay: its first lines at the high
// watermark are ones already delivered. A connection opened from the start of
// the container is not, and admits everything it is offered.
type cursorAdmission struct {
	cursor    *cursor
	replay    bool
	equalSeen int
}

// Since is where to resume a follow: one nanosecond before the newest line
// delivered, so the lines sharing its timestamp come back to be counted. The
// zero time means nothing has been delivered and the follow starts at the
// beginning of the container.
func (c *cursor) Since() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hwNanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, c.hwNanos-1)
}

// newAdmission starts admitting lines from one connection. Pass replay=true
// when the connection was opened with a `since`.
func (c *cursor) newAdmission(replay bool) *cursorAdmission {
	return &cursorAdmission{cursor: c, replay: replay}
}

// Admit reports whether a line with this timestamp has yet to be delivered, and
// advances the watermark when it has not. A line with no timestamp is always
// admitted: there is nothing to compare it with, and it is a continuation of
// the line before it.
func (a *cursorAdmission) Admit(ts time.Time) bool {
	if ts.IsZero() {
		return true
	}
	nanos := ts.UnixNano()
	a.cursor.mu.Lock()
	defer a.cursor.mu.Unlock()
	switch {
	case nanos < a.cursor.hwNanos:
		return false
	case nanos > a.cursor.hwNanos:
		a.cursor.hwNanos = nanos
		a.cursor.hwCount = 1
		a.equalSeen = 0
		return true
	default:
		if a.replay {
			a.equalSeen++
			if a.equalSeen <= a.cursor.hwCount {
				return false
			}
		}
		a.cursor.hwCount++
		return true
	}
}
