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
//
// A connection opened with a `since` is a replay: the daemon sends back every
// line from that timestamp on, so its first lines — the ones at or before the
// watermark as it stood when the connection opened — are ones already
// delivered. Once a line past that watermark has arrived the replay is over and
// the connection is live. A connection opened from the start of the container
// is live from its first line.
//
// A live connection never repeats a line, so nothing on it is dropped — not
// even a line stamped earlier than the one before it. That ordering is real:
// a container's stdout and stderr are separate pipes copied by separate daemon
// goroutines, and the log file's order is the order they won the race, not the
// order of their timestamps. A Python handler printing to both shows the
// stderr line first in `docker logs`. Treating "older than the watermark" as
// "already seen" on a live connection dropped that stdout line from CloudWatch
// and the tail.
type cursorAdmission struct {
	cursor *cursor
	// replaying is true while this connection may still be re-sending lines
	// the follower delivered before it opened: at most those at or before
	// resumeNanos, of which resumeCount at exactly that timestamp were seen.
	replaying   bool
	resumeNanos int64
	resumeCount int
	equalSeen   int
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return &cursorAdmission{
		cursor:      c,
		replaying:   replay && c.hwNanos != 0,
		resumeNanos: c.hwNanos,
		resumeCount: c.hwCount,
	}
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
	if a.replaying {
		switch {
		case nanos < a.resumeNanos:
			// Delivered before the break; the daemon replays it because its
			// `since` is inclusive and by timestamp.
			return false
		case nanos == a.resumeNanos:
			// The lines sharing the resume nanosecond: the first resumeCount
			// of them were delivered, the rest are new.
			a.equalSeen++
			if a.equalSeen <= a.resumeCount {
				return false
			}
			a.cursor.hwCount++
			return true
		default:
			// Past the resume watermark: the replay is over and from here on
			// the connection is live.
			a.replaying = false
		}
	}
	switch {
	case nanos > a.cursor.hwNanos:
		a.cursor.hwNanos = nanos
		a.cursor.hwCount = 1
	case nanos == a.cursor.hwNanos:
		a.cursor.hwCount++
	default:
		// Older than the newest line delivered, on a live connection: the
		// daemon wrote it out of timestamp order. Deliver it; the watermark
		// stays where it is.
	}
	return true
}
