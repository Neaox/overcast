package containerlogs

import (
	"testing"
	"time"
)

func TestCursor_equalTimestampReplay(t *testing.T) {
	// Given: two log lines with the exact same Docker timestamp were accepted.
	ts := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	var c cursor
	live := c.newAdmission(false)
	first := live.Admit(ts)
	second := live.Admit(ts)
	if !first || !second {
		t.Fatal("live equal-timestamp lines should both be accepted")
	}

	// When: a reconnect replays those same two lines plus a third with the
	// same timestamp.
	replay := c.newAdmission(true)

	// Then: only the already accepted equal-timestamp lines are skipped.
	if replay.Admit(ts) {
		t.Fatal("first replayed equal-timestamp line was accepted, want skipped")
	}
	if replay.Admit(ts) {
		t.Fatal("second replayed equal-timestamp line was accepted, want skipped")
	}
	if !replay.Admit(ts) {
		t.Fatal("third equal-timestamp line was skipped, want accepted")
	}
}

func TestCursor_sinceReplaysEqualTimestamp(t *testing.T) {
	// Given: a cursor has accepted a timestamped log line.
	ts := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	var c cursor
	if !c.newAdmission(false).Admit(ts) {
		t.Fatal("line was not accepted")
	}

	// When: the resume point is computed for a reconnect or a reconcile.
	since := c.Since()

	// Then: it asks the daemon to replay from just before the high watermark so
	// equal-timestamp lines can be disambiguated by count.
	if want := ts.Add(-time.Nanosecond); !since.Equal(want) {
		t.Fatalf("Since = %v, want %v", since, want)
	}
}

func TestCursor_untimestampedLinesAlwaysAdmitted(t *testing.T) {
	// Given: a cursor already past a timestamp.
	var c cursor
	adm := c.newAdmission(false)
	adm.Admit(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	// When: a line with no timestamp of its own arrives — the continuation of a
	// frame carrying several lines.
	// Then: it is admitted, because there is nothing to compare it with.
	if !adm.Admit(time.Time{}) {
		t.Fatal("an untimestamped continuation line was dropped")
	}
}

func TestCursor_beforeAnythingDeliveredSinceIsZero(t *testing.T) {
	var c cursor
	if got := c.Since(); !got.IsZero() {
		t.Fatalf("Since = %v on a fresh cursor, want the zero time so the follow starts at the beginning", got)
	}
}
