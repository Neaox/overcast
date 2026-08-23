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

// TestCursor_liveLineOlderThanTheWatermarkIsNotADuplicate is the regression
// test for a line the follower dropped from a container that had printed it.
//
// A container's stdout and stderr are separate pipes, copied into the daemon's
// log by separate goroutines, so the log file's order is not its timestamp
// order: a stderr line can land in the file *after* a stdout line that was
// stamped earlier. That is what a Python handler printing to both produces
// every time — `docker logs` shows the stderr line first. On a live
// connection nothing is ever delivered twice, so an older timestamp is just
// the order the daemon wrote things in, never a duplicate — but the cursor
// treated "older than the watermark" as "already seen" whatever the
// connection, and the first stdout line of a cold-started Python function was
// gone from CloudWatch and the tail alike.
func TestCursor_liveLineOlderThanTheWatermarkIsNotADuplicate(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var c cursor
	live := c.newAdmission(false)
	if !live.Admit(base.Add(20)) {
		t.Fatal("stderr line (t+20) should be accepted")
	}
	if !live.Admit(base.Add(10)) {
		t.Fatal("stdout line stamped t+10 but delivered after t+20 was dropped as a duplicate; a live connection never repeats a line")
	}
	if !live.Admit(base.Add(30)) {
		t.Fatal("next line (t+30) should be accepted")
	}
}

// TestCursor_replayOnlyDeduplicatesUpToTheWatermarkItResumedFrom pins what a
// reconnect may and may not drop: the lines at or before the watermark as it
// stood when the connection opened — those the daemon replays because `since`
// is by timestamp — and nothing after it. Once the replay has passed that
// watermark the connection is live, and a live line is admitted whatever its
// timestamp.
func TestCursor_replayOnlyDeduplicatesUpToTheWatermarkItResumedFrom(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var c cursor
	live := c.newAdmission(false)
	for _, d := range []time.Duration{10, 20, 20} {
		if !live.Admit(base.Add(d)) {
			t.Fatalf("live line t+%d should be accepted", d)
		}
	}

	// When: the connection breaks and the follow resumes from t+19 — the
	// daemon replays both t+20 lines, then carries on with new output.
	replay := c.newAdmission(true)
	for i := 1; i <= 2; i++ {
		if replay.Admit(base.Add(20)) {
			t.Fatalf("replayed t+20 line %d should be dropped", i)
		}
	}
	// And: the daemon's `since` is by timestamp and inclusive, so a line from
	// the nanosecond the follow resumed at (t+19) can come back too; it was
	// delivered before the break and is dropped.
	if replay.Admit(base.Add(19)) {
		t.Fatal("a replayed line from before the resume watermark should be dropped")
	}
	// Then: a third t+20 line is new.
	if !replay.Admit(base.Add(20)) {
		t.Fatal("a third t+20 line is new and should be accepted")
	}
	// And: past the watermark the connection is live — a traceback's several
	// lines in one nanosecond are all new, and so is an out-of-order older one.
	if !replay.Admit(base.Add(40)) {
		t.Fatal("t+40 should be accepted")
	}
	if !replay.Admit(base.Add(40)) {
		t.Fatal("second line in the t+40 nanosecond was dropped — replay dedup leaked past the resume watermark")
	}
	if !replay.Admit(base.Add(35)) {
		t.Fatal("out-of-order t+35 after the resume watermark is live output and should be accepted")
	}
}
