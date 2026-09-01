package containerlogs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
)

var base = time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

// running is a follower on a goroutine, with the context that stops it.
type running struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func start(f *Follower) *running {
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		f.Follow(ctx)
	}()
	return r
}

func (r *running) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("follower did not return after its context was cancelled")
	}
}

// advanceUntil moves the mock clock on in steps until cond holds. Every wait
// inside the follower is on the injected clock, so this is what "time passed"
// means to it.
func advanceUntil(t *testing.T, mock *clock.Mock, step time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		mock.Add(step)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newFollower(t *testing.T, streamer *fakeStreamer, sink LineSink, clk clock.Clock) *Follower {
	t.Helper()
	return New(Config{
		Client:      streamer,
		ContainerID: "c0ffeec0ffeec0ffee",
		Sink:        sink,
		Clock:       clk,
		Logger:      zap.NewNop(),
	})
}

// A daemon that drops a follow mid-container is the failure this package exists
// for: the container is still running and still printing, and everything it
// prints from the break onwards used to be lost. The follower has to come back
// — and come back without re-delivering what the daemon replays on the way in.
func TestFollower_reconnectsAndDeliversTheRestOfTheOutput(t *testing.T) {
	// Given: a stream that ends after three lines, and a reconnect that replays
	// the last of them before serving a fourth.
	var first, second bytes.Buffer
	first.Write(stdoutLine(base, "one"))
	first.Write(stdoutLine(base.Add(time.Millisecond), "two"))
	first.Write(stdoutLine(base.Add(2*time.Millisecond), "three"))
	second.Write(stdoutLine(base.Add(2*time.Millisecond), "three"))
	second.Write(stdoutLine(base.Add(3*time.Millisecond), "four"))

	streamer := &fakeStreamer{scripts: []streamScript{
		{payload: first.Bytes()},
		{payload: second.Bytes(), hold: true},
	}}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower runs across the break.
	run := start(follower)
	advanceUntil(t, mock, 2*time.Second, "the fourth line", func() bool {
		return len(sink.messages()) >= 4
	})
	run.stop(t)

	// Then: every line arrives exactly once, and the reconnect asked the daemon
	// to resume from just before the last line it had.
	if got := strings.Join(sink.messages(), ","); got != "one,two,three,four" {
		t.Fatalf("messages = %q, want one,two,three,four", got)
	}
	since := streamer.sinceValues()
	if len(since) < 2 {
		t.Fatalf("the follower opened %d streams, want at least 2", len(since))
	}
	if !since[0].IsZero() {
		t.Errorf("first open used since = %v, want the zero time", since[0])
	}
	if want := base.Add(2 * time.Millisecond).Add(-time.Nanosecond); !since[1].Equal(want) {
		t.Errorf("reconnect used since = %v, want %v", since[1], want)
	}
}

// Resuming by timestamp cannot separate lines that share one, and a runtime
// printing a traceback shares one every time. The count in the cursor is what
// tells the replayed copies from the new line behind them.
func TestFollower_reconnectKeepsNewLinesSharingAReplayedTimestamp(t *testing.T) {
	// Given: two lines written in the same nanosecond, and a reconnect that
	// replays both and adds a third with that same timestamp.
	ts := base.Add(time.Second)
	var first, second bytes.Buffer
	first.Write(stdoutLine(ts, "trace-1"))
	first.Write(stdoutLine(ts, "trace-2"))
	second.Write(stdoutLine(ts, "trace-1"))
	second.Write(stdoutLine(ts, "trace-2"))
	second.Write(stdoutLine(ts, "trace-3"))

	streamer := &fakeStreamer{scripts: []streamScript{
		{payload: first.Bytes()},
		{payload: second.Bytes(), hold: true},
	}}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower runs across the break.
	run := start(follower)
	advanceUntil(t, mock, 2*time.Second, "the third line", func() bool {
		return len(sink.messages()) >= 3
	})
	run.stop(t)

	// Then: the two replayed lines are recognised and the new one is kept.
	if got := strings.Join(sink.messages(), ","); got != "trace-1,trace-2,trace-3" {
		t.Fatalf("messages = %q, want trace-1,trace-2,trace-3", got)
	}
}

// Both of a container's output streams reach the same log stream, in the order
// the daemon framed them.
func TestFollower_deliversStdoutAndStderr(t *testing.T) {
	// Given: one connection carrying interleaved stdout and stderr frames.
	var payload bytes.Buffer
	payload.Write(dockerFrame(1, base.Format(time.RFC3339Nano)+" out\n"))
	payload.Write(dockerFrame(2, base.Add(time.Millisecond).Format(time.RFC3339Nano)+" err\n"))
	payload.Write(dockerFrame(1, base.Add(2*time.Millisecond).Format(time.RFC3339Nano)+" out again\n"))

	streamer := &fakeStreamer{scripts: []streamScript{{payload: payload.Bytes(), hold: true}}}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower reads the connection.
	run := start(follower)
	waitFor(t, "all three lines", func() bool { return len(sink.messages()) == 3 })
	run.stop(t)

	// Then: the frame headers are gone and the order is the daemon's.
	if got := strings.Join(sink.messages(), ","); got != "out,err,out again" {
		t.Fatalf("messages = %q, want out,err,out again", got)
	}
}

// A container that writes without ever printing a newline must not be able to
// grow the follower's memory until the process dies, and the line after an
// oversized one must still arrive whole.
func TestFollower_truncatesAnOversizedLine(t *testing.T) {
	// Given: a line past CloudWatch's per-event limit, then an ordinary one.
	huge := strings.Repeat("x", maxMessageBytes+4096)
	var payload bytes.Buffer
	payload.Write(stdoutLine(base, huge))
	payload.Write(stdoutLine(base.Add(time.Millisecond), "after"))

	streamer := &fakeStreamer{scripts: []streamScript{{payload: payload.Bytes(), hold: true}}}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower reads them.
	run := start(follower)
	waitFor(t, "both lines", func() bool { return len(sink.messages()) == 2 })
	run.stop(t)

	// Then: the oversized line is cut to the limit and the next one is intact.
	lines, _ := sink.snapshot()
	if len(lines[0].Message) != maxMessageBytes {
		t.Errorf("truncated line = %d bytes, want %d", len(lines[0].Message), maxMessageBytes)
	}
	if lines[1].Message != "after" {
		t.Errorf("second line = %q, want after", lines[1].Message)
	}
}

// Twenty-five lines is a batch, and a burst of output must not wait on the
// clock to be written.
func TestFollower_flushesAFullBatchWithoutWaiting(t *testing.T) {
	// Given: twenty-six lines on one connection.
	var payload bytes.Buffer
	for i := 0; i < batchMax+1; i++ {
		payload.Write(stdoutLine(base.Add(time.Duration(i)*time.Millisecond), "line"))
	}
	streamer := &fakeStreamer{scripts: []streamScript{{payload: payload.Bytes(), hold: true}}}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower reads them without any time passing.
	run := start(follower)
	waitFor(t, "the full batch to be flushed", func() bool {
		_, flushes := sink.snapshot()
		return len(flushes) == 1
	})

	// Then: exactly one batch of twenty-five went out, and the twenty-sixth
	// line is still waiting on the interval.
	_, flushes := sink.snapshot()
	if len(flushes) != 1 || flushes[0] != batchMax {
		t.Fatalf("flushes = %v, want one flush of %d", flushes, batchMax)
	}

	// When: the flush interval elapses.
	mock.Add(flushInterval)
	waitFor(t, "the trailing line to be flushed", func() bool {
		_, f := sink.snapshot()
		return len(f) == 2
	})
	run.stop(t)

	// Then: the odd line follows on its own.
	_, flushes = sink.snapshot()
	if len(flushes) != 2 || flushes[1] != 1 {
		t.Fatalf("flushes = %v, want a second flush of 1", flushes)
	}
}

// A single line printed on its own must not sit in a batch waiting for
// twenty-four more that are never coming.
func TestFollower_flushesAPartialBatchAfterTheInterval(t *testing.T) {
	// Given: two lines, well short of a batch.
	var payload bytes.Buffer
	payload.Write(stdoutLine(base, "alpha"))
	payload.Write(stdoutLine(base.Add(time.Millisecond), "beta"))
	streamer := &fakeStreamer{scripts: []streamScript{{payload: payload.Bytes(), hold: true}}}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower has read them but no time has passed.
	run := start(follower)
	waitFor(t, "both lines to reach the sink", func() bool { return len(sink.messages()) == 2 })
	if _, flushes := sink.snapshot(); len(flushes) != 0 {
		t.Fatalf("flushes = %v before the interval, want none", flushes)
	}

	// Then: the interval is what sends them.
	mock.Add(flushInterval)
	waitFor(t, "the interval flush", func() bool {
		_, flushes := sink.snapshot()
		return len(flushes) == 1
	})
	run.stop(t)
	if _, flushes := sink.snapshot(); flushes[0] != 2 {
		t.Fatalf("flush held %d lines, want 2", flushes[0])
	}
}

// Cancelling the follower's context closes the daemon connection, so anything
// held between the reader and the sink is the last chance to deliver it.
func TestFollower_deliversBufferedLinesWhenCancelled(t *testing.T) {
	// Given: five lines, and a sink that is held up on the first of them so the
	// rest queue behind it.
	var payload bytes.Buffer
	for i, msg := range []string{"a", "b", "c", "d", "e"} {
		payload.Write(stdoutLine(base.Add(time.Duration(i)*time.Millisecond), msg))
	}
	streamer := &fakeStreamer{scripts: []streamScript{{payload: payload.Bytes(), hold: true}}}
	gate := make(chan struct{})
	sink := &recordSink{gate: gate}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the context is cancelled while the queue is still full, and the
	// sink is then released.
	run := start(follower)
	waitFor(t, "all five lines to be assembled behind the held sink", func() bool {
		return streamer.consumed(0)
	})
	run.cancel()
	close(gate)
	select {
	case <-run.done:
	case <-time.After(5 * time.Second):
		t.Fatal("follower did not return after its context was cancelled")
	}

	// Then: nothing that had been read is dropped, and it was all written.
	if got := strings.Join(sink.messages(), ","); got != "a,b,c,d,e" {
		t.Fatalf("messages = %q, want a,b,c,d,e", got)
	}
	_, flushes := sink.snapshot()
	total := 0
	for _, n := range flushes {
		total += n
	}
	if total != 5 {
		t.Fatalf("flushed %d lines across %v, want 5", total, flushes)
	}
}

// A follow that died and never came back still has the daemon's persisted log
// file behind it, and once the container has stopped that file is complete.
func TestFollower_reconcileBackfillsWhatTheFollowMissed(t *testing.T) {
	// Given: a follow that delivered two lines, and a log file holding those
	// two plus a third.
	var live, file bytes.Buffer
	live.Write(stdoutLine(base, "one"))
	live.Write(stdoutLine(base.Add(time.Millisecond), "two"))
	file.Write(stdoutLine(base.Add(time.Millisecond), "two"))
	file.Write(stdoutLine(base.Add(2*time.Millisecond), "three"))

	streamer := &fakeStreamer{
		scripts:  []streamScript{{payload: live.Bytes(), hold: true}},
		backfill: file.Bytes(),
	}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	run := start(follower)
	waitFor(t, "the live lines", func() bool { return len(sink.messages()) == 2 })
	run.stop(t)

	// When: the container is gone and the follower reconciles.
	follower.Reconcile(context.Background())

	// Then: only the line the follow never saw is added, marked as backfill.
	lines, _ := sink.snapshot()
	if got := strings.Join(sink.messages(), ","); got != "one,two,three" {
		t.Fatalf("messages = %q, want one,two,three", got)
	}
	if lines[2].Backfill != true {
		t.Error("the reconciled line was not marked Backfill")
	}
	if lines[1].Backfill {
		t.Error("a live line was marked Backfill")
	}
}

// A daemon that refuses the connect has still answered. Nothing may be lost by
// it, and the follower must keep trying.
func TestFollower_retriesAfterTheDaemonRefusesTheConnect(t *testing.T) {
	// Given: a daemon that refuses every follow.
	streamer := &fakeStreamer{openErr: errors.New("daemon busy")}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower runs.
	run := start(follower)
	advanceUntil(t, mock, time.Second, "a second attempt", func() bool { return streamer.opens() == 0 && len(streamer.sinceValues()) >= 2 })
	run.stop(t)

	// Then: it kept asking — at least two attempts reached the daemon.
	if got := len(streamer.sinceValues()); got < 2 {
		t.Fatalf("the daemon was asked %d times, want at least 2", got)
	}
}

// CloudWatch refuses an empty event, and a container prints blank lines all the
// time — a runtime separating one traceback from the next.
func TestFollower_dropsAnEmptyMessage(t *testing.T) {
	// Given: a blank line between two ordinary ones.
	var payload bytes.Buffer
	payload.Write(stdoutLine(base, "before"))
	payload.Write(dockerFrame(1, base.Add(time.Millisecond).Format(time.RFC3339Nano)+" \n"))
	payload.Write(stdoutLine(base.Add(2*time.Millisecond), "after"))

	streamer := &fakeStreamer{scripts: []streamScript{{payload: payload.Bytes(), hold: true}}}
	sink := &recordSink{}
	mock := clock.NewMock()
	follower := newFollower(t, streamer, sink, mock)

	// When: the follower reads them.
	run := start(follower)
	waitFor(t, "both real lines", func() bool { return len(sink.messages()) == 2 })
	run.stop(t)

	// Then: the blank one never reaches the sink, and the lines around it do.
	if got := strings.Join(sink.messages(), ","); got != "before,after" {
		t.Fatalf("messages = %q, want before,after", got)
	}
}
