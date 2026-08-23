package lambda

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
)

// The X-Amz-Log-Result tail is assembled from two sources that arrive by
// different routes. START / END / REPORT are written straight into tailBuf by
// writeLogLine, so they are always there. The handler's own stdout travels
// container → Docker → the streamLogs reader, and lands whenever Docker gets
// round to flushing the pipe — which can be after the RIC has already POSTed
// the handler's response back over the Runtime API.
//
// waitForScannerIdle is what closes that gap. These tests drive it on a mock
// clock so the gap is a fact of the test rather than a property of the machine
// it runs on.

// noopLogWriter satisfies events.LogWriter. containerInstance only consults it
// for nil-ness on the tail path; nothing here asserts on CloudWatch delivery.
type noopLogWriter struct{}

func (noopLogWriter) EnsureLogGroup(context.Context, string) error          { return nil }
func (noopLogWriter) EnsureLogStream(context.Context, string, string) error { return nil }
func (noopLogWriter) WriteLogEvents(context.Context, string, string, []events.LogEntry) error {
	return nil
}

// newTailWaitInstance returns the minimum containerInstance the log waits touch
// — waitForScannerIdle here, waitForLogDrain in log_drain_wait_test.go: a clock,
// and the atomics streamLogs would be updating. Its reader
// starts parked in a Read on the Docker stream, which is where a reader that
// has caught up spends all of its time.
func newTailWaitInstance(clk clock.Clock) *containerInstance {
	ci := &containerInstance{
		id:        "cafebabe1234deadbeef",
		logger:    zap.NewNop(),
		clk:       clk,
		logWriter: noopLogWriter{},
	}
	readerParks(ci)
	return ci
}

// readerParks and readerWorks move the instance between the two states
// logReadTracker.Read moves it between: blocked in a Read on the Docker log
// stream, and away from it, doing something with what a Read returned.
func readerParks(ci *containerInstance) { ci.logParkedAt.Store(ci.clk.Now().UnixNano()) }
func readerWorks(ci *containerInstance) { ci.logParkedAt.Store(logReaderBetweenReads) }

// newNeverConnectedInstance returns a containerInstance in the state a
// container is actually in from the moment its streamLogs goroutine is
// scheduled until the moment ContainerLogsStream first returns:
// logParkedAt at its zero value (logReaderNotReading) and
// logStreamEverAnswered still false. newTailWaitInstance does not model this —
// readerParks always leaves the reader already parked in a Read, i.e. with a
// stream that has opened — so nothing before this test drove
// waitForScannerIdle through it.
//
// It is a real state, not a hypothetical one: opening the Docker log stream
// is a daemon round trip, and it races container start rather than following
// it. Under daemon contention that round trip can lose that race, and a
// cold-started invocation's tail wait can begin — and, before this fix, time
// out — before it clears.
func newNeverConnectedInstance(clk clock.Clock) *containerInstance {
	return &containerInstance{
		id:        "cafebabe1234deadbeef",
		logger:    zap.NewNop(),
		clk:       clk,
		logWriter: noopLogWriter{},
	}
}

// streamOpens does to the instance what streamOnce does the instant
// ContainerLogsStream returns: the connection now exists (logStreamEverAnswered
// becomes permanently true) and the reader is between Reads on it, having not
// reached its first one yet.
func streamOpens(ci *containerInstance) {
	ci.logStreamEverAnswered.Store(true)
	readerWorks(ci)
}

// streamOpenFails does to the instance what streamOnce does when
// ContainerLogsStream returns an error instead of a stream: Docker has answered
// the first connect — with a refusal — and the reader is not reading, waiting
// out the reconnect backoff. Nothing has opened, but nothing is in flight
// either, which is what tells this apart from newNeverConnectedInstance's
// still-unanswered connect.
func streamOpenFails(ci *containerInstance) {
	ci.logStreamEverAnswered.Store(true)
	ci.logParkedAt.Store(logReaderNotReading)
}

// deliverBytes does to the instance what logReadTracker does the instant Docker
// hands over bytes, and nothing more. That is the whole point of having it: a
// Read seldom lands a whole line, so this is a state the pipeline really passes
// through — the read watermark has moved and the tail buffer is still empty.
func deliverBytes(ci *containerInstance) {
	ci.logReadAt.Store(ci.clk.Now().UnixNano())
}

// deliverLine does to the instance exactly what streamOnce does when Docker
// finally hands over a whole line: logReadTracker stamps logReadAt on the read,
// and the parsed line is appended to the rolling tail buffer under the append
// watermark.
func deliverLine(ci *containerInstance, line string) {
	deliverBytes(ci)
	ci.tailMu.Lock()
	ci.tailBuf = append(ci.tailBuf, append([]byte(line), '\n')...)
	ci.tailAppendAt.Store(ci.clk.Now().UnixNano())
	ci.tailMu.Unlock()
}

func tailContents(ci *containerInstance) string {
	ci.tailMu.Lock()
	defer ci.tailMu.Unlock()
	return string(ci.tailBuf)
}

// runScannerWait runs waitForScannerIdle against a mock clock the test owns,
// delivering line at deliverAt of mock time (deliverAt < 0 delivers nothing,
// standing in for a handler that prints nothing). It returns how long the wait
// lasted in mock time.
//
// Mock time only moves when this function moves it, so the wait cannot race
// ahead of the delivery: it is parked on a ticker that will not fire until the
// loop below advances the clock past it.
func runScannerWait(t *testing.T, mock *clock.Mock, ci *containerInstance, mark tailMark, deliverAt time.Duration, line string) time.Duration {
	t.Helper()

	start := mock.Now()
	delivered := deliverAt < 0
	advanceUntilDone(t, mock, 0, func() { ci.waitForScannerIdle(mark) }, func() {
		if !delivered && mock.Now().Sub(start) >= deliverAt {
			deliverLine(ci, line)
			delivered = true
		}
	})
	return mock.Now().Sub(start)
}

// advanceUntilDone runs fn on its own goroutine and walks mock time forward a
// millisecond at a time until it returns, calling onTick (if any) before each
// step. Anything parked on a mock timer only makes progress here, which is what
// keeps these tests free of real sleeps — and what stops the code under test
// racing ahead of a delivery the test has not made yet.
//
// minAdvance keeps the clock walking after fn returns, up to that much mock
// time in total. That is what lets a test schedule something for a moment fn
// may well have finished before: Docker hands a line over when it hands it
// over, whether or not the code under test is still waiting for it, and a test
// that stops the clock the instant fn returns can only ever model the orderings
// where it was.
func advanceUntilDone(t *testing.T, mock *clock.Mock, minAdvance time.Duration, fn func(), onTick func()) {
	t.Helper()

	start := mock.Now()

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		fn()
	}()
	<-started
	// Let the goroutine reach its blocking select and register its timers
	// against the frozen clock. Nothing can happen while it does: the waits here
	// have no wall-clock path out, so this sleep bounds goroutine start-up only.
	time.Sleep(50 * time.Millisecond)

	finished := false
	giveUp := time.After(30 * time.Second)
	for {
		select {
		case <-done:
			finished = true
		case <-giveUp:
			t.Fatal("the wait under test never returned")
			return
		default:
		}
		if finished && mock.Now().Sub(start) >= minAdvance {
			return
		}
		if onTick != nil {
			onTick()
		}
		mock.Add(time.Millisecond)
	}
}

// TestWaitForScannerIdle_coldContainerWaitsForFirstOutput is the regression
// test for the tail that arrives without the handler's own log line.
//
// On the first invocation of a fresh container logReadAt is still 0, because
// nothing has been read yet. The wait used to read that as "the function
// emitted nothing this invocation" and return on its first tick, so the
// snapshot taken straight afterwards held only the lines Overcast writes
// itself. That is what made TestInvoke_logTail fail on unrelated PRs: it
// creates a function and invokes it once, so it takes this path every time.
func TestWaitForScannerIdle_coldContainerWaitsForFirstOutput(t *testing.T) {
	// Given: a container that has never produced a log line.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	mark := ci.beginTail()
	if mark.read != 0 || mark.appended != 0 {
		t.Fatalf("cold container should have no watermarks, got %+v", mark)
	}

	// When: Docker delivers the handler's line 15 ms after the handler replied.
	elapsed := runScannerWait(t, mock, ci, mark, 15*time.Millisecond, "hello from lambda")

	// Then: the wait held on for it, so the tail a snapshot would take is complete.
	if !strings.Contains(tailContents(ci), "hello from lambda") {
		t.Errorf("tail is missing the handler's output: %q", tailContents(ci))
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf("wait returned after %v, before the output arrived at 15ms", elapsed)
	}
}

// TestWaitForScannerIdle_warmContainerWaitsForItsOwnOutput covers the second
// face of the same defect. logReadAt is a container-lifetime watermark, so on a
// warm reuse it still holds the *previous* invocation's timestamp — long since
// older than idleThreshold, which made the idle test true on the first tick and
// the whole wait a no-op. Nothing covered this, which is why it went unnoticed.
func TestWaitForScannerIdle_warmContainerWaitsForItsOwnOutput(t *testing.T) {
	// Given: a container whose previous invocation logged a while ago.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	deliverLine(ci, "output from the previous invocation")
	mock.Add(500 * time.Millisecond)

	// And: a fresh invocation, which resets the tail buffer and takes its
	// watermarks from where the previous one left off.
	mark := ci.beginTail()

	// When: this invocation's line takes 15 ms to come through Docker.
	elapsed := runScannerWait(t, mock, ci, mark, 15*time.Millisecond, "hello from invocation two")

	// Then: the stale watermarks did not short-circuit the wait.
	if !strings.Contains(tailContents(ci), "hello from invocation two") {
		t.Errorf("tail is missing this invocation's output: %q", tailContents(ci))
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf("wait returned after %v — the previous invocation's watermarks satisfied it", elapsed)
	}
}

// TestWaitForScannerIdle_parkedReaderSurvivesContention is the regression test
// for issue #1325: a second timing window, one stage further down the pipeline
// than #1160/#1166's.
//
// #1166 fixed the case where the reader had never connected to Docker's log
// stream at all — a connect that races container start and can lose that race
// under daemon contention. But once a warm container's reader HAS connected,
// it spends essentially all of its time parked inside a live, blocking Read()
// call on that connection — the ordinary state between any two invocations,
// since the reader re-enters Read() the instant it hands the previous line
// off. dockerSilentSince used to time that state exactly like a reconnect
// backoff (logReaderNotReading): 25 ms of silence from this invocation's own
// wait start, then give up. But a reader blocked in an already-open Read() is
// not backing off from anything — it is waiting on the Docker daemon to
// actually flush this invocation's bytes over a connection that is already
// live, which is itself a round trip that daemon contention (a busy CI runner
// juggling many containers' log streams at once) can push past 25 ms even
// though the handler already wrote its line and nothing is actually stuck.
//
// This is what made TestInvoke_logTail's *second* (warm) invocation fail under
// contention on PR #1314, after #1166 had already closed the cold-start
// window: the container's log stream was already connected and parked, the
// invoke completed, and the handler's line simply took the pipeline longer
// than 25 ms to arrive.
func TestWaitForScannerIdle_parkedReaderSurvivesContention(t *testing.T) {
	// Given: a warm container whose reader has been parked in a live Read()
	// call on an already-open connection since well before this invocation's
	// wait begins — the ordinary state a warm container spends its idle time
	// in, not a reconnect backoff.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	deliverLine(ci, "output from the previous invocation")
	mock.Add(500 * time.Millisecond)
	mark := ci.beginTail()

	// When: this invocation's line takes 60 ms to come through Docker — well
	// past the 25 ms bound that correctly governs a reconnect backoff, but
	// still within the 80 ms a reader parked in a live Read is owed. Nothing
	// here involves a reconnect: the connection has been open and parked the
	// whole time.
	elapsed := runScannerWait(t, mock, ci, mark, 60*time.Millisecond, "hello from lambda")

	// Then: the wait held on for it. A live connection's own delivery delay
	// under daemon contention is not the same evidence as a reader that has no
	// connection to wait on at all, and must not be timed as harshly.
	if !strings.Contains(tailContents(ci), "hello from lambda") {
		t.Errorf("tail is missing the handler's output: %q", tailContents(ci))
	}
	if elapsed < 60*time.Millisecond {
		t.Errorf("wait returned after %v, before the delayed line arrived at 60ms — a live, already-open connection's own delivery delay was timed as though the reader had backed off from one", elapsed)
	}
}

// TestWaitForScannerIdle_partialLineIsNotIdle is the regression test for a tail
// that goes missing even though Docker delivered the output in good time.
//
// logReadAt is stamped by logReadTracker the instant Docker hands over bytes,
// which is several steps before those bytes are anything the snapshot can read.
// A Read seldom lands a whole line — the line reader keeps reading until it
// finds a newline, and logInFlight does not count the line until it has one — so
// the pipeline routinely sits in a state where the read watermark has moved and
// the tail buffer is still empty. Waiting on logReadAt read that state as
// "output arrived and the reader has gone quiet" and returned at the first tick
// past idleThreshold, snapshotting a tail holding only START/END/REPORT. The
// line then landed in whichever invocation's buffer was current when it was
// finally parsed — the next one.
func TestWaitForScannerIdle_partialLineIsNotIdle(t *testing.T) {
	// Given: a container invocation whose output is on its way.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	mark := ci.beginTail()

	// When: Docker hands over the front of the line straight away, and the rest
	// of it — the part carrying the newline — only 15 ms later.
	deliverBytes(ci)
	elapsed := runScannerWait(t, mock, ci, mark, 15*time.Millisecond, "hello from lambda")

	// Then: the wait held on for the line rather than treating the read as the
	// output itself.
	if !strings.Contains(tailContents(ci), "hello from lambda") {
		t.Errorf("tail is missing the handler's output: %q", tailContents(ci))
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf("wait returned after %v, before the line was assembled at 15ms", elapsed)
	}
}

// TestWaitForScannerIdle_readerThatHasNotAskedDockerIsNotSilence is the
// regression test for issue #873: the warm invocation of a two-invocation
// sequence returns a tail holding START / END / REPORT and nothing else.
//
// "Docker has handed over nothing" is the emulator's only evidence that the
// handler printed nothing, and it is worth nothing at all while the reader is
// between Reads. Having just delivered the previous invocation's line it is
// somewhere in the loop that takes it back to the Docker stream — through the
// line reader, a channel, the batching select, the tail append — and it is not
// going to hear about the next line until it gets there. On an idle machine
// that is microseconds and the distinction never shows; on a CI runner with the
// whole suite on it, it is tens of milliseconds, and the wait was spending its
// entire first-read grace inside it and then reporting the function silent.
//
// The two invocations in TestInvoke_logTail are back to back, so the second one
// starts inside exactly that window. That is why it is the warm one that fails.
func TestWaitForScannerIdle_readerThatHasNotAskedDockerIsNotSilence(t *testing.T) {
	// Given: an invocation that starts while the reader is away from the stream,
	// still working through what the previous one printed.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	deliverLine(ci, "output from the previous invocation")
	readerWorks(ci)
	mark := ci.beginTail()

	// When: the reader gets back to Docker 40 ms later — well past the 25 ms
	// first-read grace — and this invocation's line is sitting there waiting.
	start := mock.Now()
	back := false
	advanceUntilDone(t, mock, 0, func() { ci.waitForScannerIdle(mark) }, func() {
		if !back && mock.Now().Sub(start) >= 40*time.Millisecond {
			readerParks(ci)
			deliverLine(ci, "hello from invocation two")
			back = true
		}
	})
	elapsed := mock.Now().Sub(start)

	// Then: the wait was still there for it. Nothing about a reader that has not
	// asked Docker a question says the function had no answer to give.
	if !strings.Contains(tailContents(ci), "hello from invocation two") {
		t.Errorf("tail is missing this invocation's output: %q", tailContents(ci))
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("wait returned after %v — it read the reader's own backlog as the function's silence", elapsed)
	}
}

// TestWaitForScannerIdle_neverConnectedStreamIsNotSilence is the regression
// test for issue #1160: a cold-started invocation's tail wait can begin before
// streamLogs has completed its very first ContainerLogsStream call, because
// that call is a Docker daemon round trip racing container start rather than
// following it. dockerSilentSince used to read logParkedAt's zero value the
// same way whether or not the stream had ever opened, so a slow first connect
// was timed as "asked and told nothing" from the moment the wait began — the
// same 25 ms firstReadMax that correctly bounds an already-open, already-idle
// reader. Under daemon contention (a busy CI runner with many containers
// starting at once) the connect alone can take longer than that, and the wait
// gave up before the connection — let alone the handler's own line — had
// arrived, which is what left TestInvoke_logTail's cold-start tail holding
// only the synthetic START/END/REPORT lines it writes directly. See
// discardUnclaimedOutput's own account of why a truncated tail, though
// AWS-legal, is not the same failure as this one: this is the emulator
// answering "the reader asked and Docker said nothing" when the true answer is
// "the reader had not yet been able to ask".
func TestWaitForScannerIdle_neverConnectedStreamIsNotSilence(t *testing.T) {
	// Given: an invocation that begins before its container's log stream has
	// ever connected.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newNeverConnectedInstance(mock)
	mark := ci.beginTail()

	// When: the connection itself takes 60 ms to open — a daemon round trip
	// still racing container start, slow because the daemon is busy with other
	// containers — and the handler's line follows 10 ms after that: 70 ms after
	// the invocation began. That is past the 25 ms bound that governs an
	// already-open reader's silence — which is exactly what made this a false
	// red on real CI runs, where the connect alone can outrun 25 ms — and well
	// inside the 100 ms that then bounded the wait as a whole. (A connect
	// slower than that is the next test but one.)
	start := mock.Now()
	opened, delivered := false, false
	advanceUntilDone(t, mock, 0, func() { ci.waitForScannerIdle(mark) }, func() {
		elapsed := mock.Now().Sub(start)
		if !opened && elapsed >= 60*time.Millisecond {
			streamOpens(ci)
			opened = true
		}
		if !delivered && elapsed >= 70*time.Millisecond {
			deliverLine(ci, "hello from lambda")
			delivered = true
		}
	})
	elapsed := mock.Now().Sub(start)

	// Then: the wait held through the connect delay for the line, rather than
	// reading "no stream open yet" as proof the handler had nothing to say.
	if !strings.Contains(tailContents(ci), "hello from lambda") {
		t.Errorf("tail is missing the handler's output: %q", tailContents(ci))
	}
	if elapsed < 70*time.Millisecond {
		t.Errorf("wait returned after %v, before the delayed connection delivered the line at 70ms", elapsed)
	}
}

// TestWaitForScannerIdle_streamThatNeverConnectsGivesUpAtTheDeadline pins the
// cost side of the fix above: a container whose log stream never manages to
// connect at all — daemon wedged, the streaming request never answered — must
// not hold the invoke response hostage. A connect still in flight is bounded by
// nothing but deadlineMax (the 100 ms progress bound does not run until Docker
// has answered — see TestWaitForScannerIdle_slowFirstConnectIsNotTimedByTheProgressBound),
// so deadlineMax is what is left to catch it, and it must.
func TestWaitForScannerIdle_streamThatNeverConnectsGivesUpAtTheDeadline(t *testing.T) {
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newNeverConnectedInstance(mock)
	mark := ci.beginTail()

	elapsed := runScannerWait(t, mock, ci, mark, -1, "")

	if elapsed < time.Second {
		t.Errorf("wait for a stream that never connected returned after %v, expected to run out the 1s deadline", elapsed)
	}
	if elapsed > time.Second+10*time.Millisecond {
		t.Errorf("wait for a stream that never connected ran %v past the 1s deadline", elapsed)
	}
}

// TestWaitForScannerIdle_slowFirstConnectIsNotTimedByTheProgressBound is the
// regression test for the third cold-start miss (run 32622332545, after #1166
// and #1331): a cold-started invocation's tail wait begins before the
// container's very first ContainerLogsStream call has returned, and that call
// takes longer than 100 ms to come back — a Docker daemon round trip issued
// from Overcast's own goroutine, queued behind whatever else a loaded CI
// runner's daemon is doing. #1166 stopped timing that state as "asked and told
// nothing", but left it under the wait's absolute 100 ms deadline, measured
// from the moment the wait began. A connect that lands after that — followed
// by the handler's line a few milliseconds later — found nobody waiting.
//
// A connect still in flight is Overcast's own round trip, not evidence about
// the function, and its completion is an observable event. So it is bounded by
// the deadline alone; the progress bound starts running once Docker has
// answered.
func TestWaitForScannerIdle_slowFirstConnectIsNotTimedByTheProgressBound(t *testing.T) {
	// Given: an invocation that begins before its container's log stream has
	// ever connected.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newNeverConnectedInstance(mock)
	mark := ci.beginTail()

	// When: the connect takes 120 ms to come back — past the 100 ms that used
	// to bound the whole wait — the reader parks on it, and Docker hands over
	// the handler's line 10 ms after that.
	start := mock.Now()
	opened, delivered := false, false
	advanceUntilDone(t, mock, 0, func() { ci.waitForScannerIdle(mark) }, func() {
		elapsed := mock.Now().Sub(start)
		if !opened && elapsed >= 120*time.Millisecond {
			streamOpens(ci)
			readerParks(ci)
			opened = true
		}
		if !delivered && elapsed >= 130*time.Millisecond {
			deliverLine(ci, "hello from lambda")
			delivered = true
		}
	})
	elapsed := mock.Now().Sub(start)

	// Then: the wait was still there for it.
	if !strings.Contains(tailContents(ci), "hello from lambda") {
		t.Errorf("tail is missing the handler's output: %q", tailContents(ci))
	}
	if elapsed < 130*time.Millisecond {
		t.Errorf("wait returned after %v, before the slow connect delivered the line at 130ms", elapsed)
	}
}

// TestWaitForScannerIdle_lateParkKeepsItsFullSilenceBound pins the other half
// of the same change. parkedReadMax (80 ms) is the room a reader parked in a
// live Read gets for Docker to hand over the first bytes — but it used to be
// cut short by the absolute deadline whenever the park happened late in the
// wait: a reader that parked 60 ms in got 40 ms, not 80. The deadline now runs
// from the last observed progress — the park included — so the bound means
// what it says wherever the park lands.
func TestWaitForScannerIdle_lateParkKeepsItsFullSilenceBound(t *testing.T) {
	// Given: an invocation whose container's stream connects and parks 60 ms
	// into the wait.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newNeverConnectedInstance(mock)
	mark := ci.beginTail()

	// When: the line takes a further 55 ms to arrive — 115 ms into the wait,
	// past the old absolute deadline, but well inside the 80 ms the park is
	// owed.
	start := mock.Now()
	opened, delivered := false, false
	advanceUntilDone(t, mock, 0, func() { ci.waitForScannerIdle(mark) }, func() {
		elapsed := mock.Now().Sub(start)
		if !opened && elapsed >= 60*time.Millisecond {
			streamOpens(ci)
			readerParks(ci)
			opened = true
		}
		if !delivered && elapsed >= 115*time.Millisecond {
			deliverLine(ci, "hello from lambda")
			delivered = true
		}
	})
	elapsed := mock.Now().Sub(start)

	// Then: the wait held for it.
	if !strings.Contains(tailContents(ci), "hello from lambda") {
		t.Errorf("tail is missing the handler's output: %q", tailContents(ci))
	}
	if elapsed < 115*time.Millisecond {
		t.Errorf("wait returned after %v, before the line arrived at 115ms — the absolute deadline cut the parked reader's 80ms bound short", elapsed)
	}
}

// TestWaitForScannerIdle_silentHandlerAfterSlowConnectGivesUpAtParkedReadMax
// pins the cost of the slow-connect fix: a handler that prints nothing, on a
// container whose stream was slow to connect, pays the connect and then the
// ordinary parkedReadMax silence — not the 1 s deadline. The deadline is for a
// pipeline that never answers at all, not one that answered late.
func TestWaitForScannerIdle_silentHandlerAfterSlowConnectGivesUpAtParkedReadMax(t *testing.T) {
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newNeverConnectedInstance(mock)
	mark := ci.beginTail()

	start := mock.Now()
	opened := false
	advanceUntilDone(t, mock, 0, func() { ci.waitForScannerIdle(mark) }, func() {
		if !opened && mock.Now().Sub(start) >= 120*time.Millisecond {
			streamOpens(ci)
			readerParks(ci)
			opened = true
		}
	})
	elapsed := mock.Now().Sub(start)

	if elapsed < 200*time.Millisecond {
		t.Errorf("silent handler after a 120ms connect waited only %v, expected the connect plus the ~80ms parkedReadMax bound", elapsed)
	}
	if elapsed > 215*time.Millisecond {
		t.Errorf("silent handler after a 120ms connect waited %v, expected to give up around 200ms rather than run towards the 1s deadline", elapsed)
	}
}

// TestWaitForScannerIdle_refusedFirstConnectIsBoundedLikeABackoff pins the
// line between "Docker has not answered the first connect" and "Docker
// answered it with an error". The first is bounded by the deadline alone (see
// above); the second must not be. A daemon that refuses the logs endpoint
// outright — a log driver that cannot be read back, say — answers every
// attempt the same way, 50 ms of backoff apart, and a tail wait that held a
// second for each of them would turn a missing log tail into a slow invoke.
// Once refused, the reader is in the same position as a reconnect backoff —
// no live connection, nothing in flight — and is timed the same way.
func TestWaitForScannerIdle_refusedFirstConnectIsBoundedLikeABackoff(t *testing.T) {
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newNeverConnectedInstance(mock)
	streamOpenFails(ci)
	mark := ci.beginTail()

	elapsed := runScannerWait(t, mock, ci, mark, -1, "")

	if elapsed > 30*time.Millisecond {
		t.Errorf("refused first connect held the wait for %v, expected to give up around the 25ms firstReadMax like a reconnect backoff", elapsed)
	}
}

// TestWaitForScannerIdle_reconnectBackoffStillBoundedByFirstReadMax pins the
// distinction the fix above depends on: once a container's log stream has
// connected at least once, a later logParkedAt zero value means an ordinary
// reconnect backoff, not the racy first-connect window — and that must stay
// bounded by firstReadMax exactly as before. Losing this bound would leave
// every invocation that lands during a reconnect paying up to the full 100 ms
// progress bound instead of the 25 ms grace, on a case the reconnect backoff
// opening at 50 ms already covers.
func TestWaitForScannerIdle_reconnectBackoffStillBoundedByFirstReadMax(t *testing.T) {
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newNeverConnectedInstance(mock)
	// The stream connected once already and is now between connections — a
	// reconnect backoff, not a first connect.
	ci.logStreamEverAnswered.Store(true)
	mark := ci.beginTail()

	elapsed := runScannerWait(t, mock, ci, mark, -1, "")

	if elapsed > 30*time.Millisecond {
		t.Errorf("reconnect backoff wait held for %v, expected to give up around the 25ms firstReadMax", elapsed)
	}
}

// TestBeginTail_dropsThePreviousInvocationsStragglerOutput pins the boundary
// that keeps one request's output out of another's tail.
//
// When the wait gives up before this invocation's output arrives, that output
// is still coming, and it is still *this* invocation's. Whatever lands next
// therefore belongs to an invocation that has already answered — so the next
// tail must not inherit it. A tail short by a line is a state AWS documents
// (LogResult is "the last 4 KB of the execution log"); a tail carrying another
// request's line is a state AWS cannot produce, and it is the one that misleads
// whoever reads it.
func TestBeginTail_dropsThePreviousInvocationsStragglerOutput(t *testing.T) {
	// Given: an invocation whose output never arrived before its wait expired.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)
	mark := ci.beginTail()
	runScannerWait(t, mock, ci, mark, -1, "")

	// When: the next invocation opens its tail, and Docker hands the previous
	// one's output over 5 ms into that — after it answered its caller, and after
	// the point a tail that did not settle the account would already have reset
	// the buffer and started collecting into it.
	straggled := false
	start := mock.Now()
	advanceUntilDone(t, mock, 15*time.Millisecond, func() {
		ci.discardUnclaimedOutput()
		mark = ci.beginTail()
	}, func() {
		if !straggled && mock.Now().Sub(start) >= 5*time.Millisecond {
			deliverLine(ci, "hello from the invocation that already returned")
			straggled = true
		}
	})

	// Then: it starts clean, rather than opening with a line it never wrote.
	if got := tailContents(ci); got != "" {
		t.Errorf("new invocation inherited the previous one's output: %q", got)
	}

	// And: its own output still reaches it — the boundary drops stragglers, not
	// everything that follows one.
	elapsed := runScannerWait(t, mock, ci, mark, 5*time.Millisecond, "hello from the next invocation")
	if !strings.Contains(tailContents(ci), "hello from the next invocation") {
		t.Errorf("tail is missing this invocation's own output: %q", tailContents(ci))
	}
	if elapsed < 5*time.Millisecond {
		t.Errorf("wait returned after %v, before this invocation's output arrived", elapsed)
	}
}

// TestWaitForScannerIdle_silentHandlerReturnsPromptly pins the cost of the
// ambiguity the fix introduces: a handler that prints nothing is
// indistinguishable from one whose output has not arrived, so it waits. It must
// wait out its bound and stop there, not sit on the 100 ms progress bound — let
// alone the 1 s deadline.
//
// newTailWaitInstance leaves the reader parked inside a live Read() (the
// ordinary state a warm container is in), so this exercises the liveParked
// bound (parkedReadMax, 80 ms) rather than firstReadMax (25 ms) — see
// dockerSilentSince and waitForScannerIdle's docstring. Before issue #1325
// widened that bound, this test pinned 25 ms; the higher number is the
// deliberate cost of no longer bailing before a live, already-open
// connection's own delivery delay under contention has had a fair chance to
// clear.
func TestWaitForScannerIdle_silentHandlerReturnsPromptly(t *testing.T) {
	// Given: a container invocation that will never produce a log line.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)

	// When: the wait runs with nothing ever delivered.
	elapsed := runScannerWait(t, mock, ci, ci.beginTail(), -1, "")

	// Then: it gives up at the liveParked grace period rather than the 100ms
	// progress bound.
	if elapsed < 80*time.Millisecond {
		t.Errorf("silent handler waited only %v, expected to run out the ~80ms parkedReadMax bound", elapsed)
	}
	if elapsed > 90*time.Millisecond {
		t.Errorf("silent handler waited %v, expected to give up around the 80ms parkedReadMax bound rather than the 100ms progress bound", elapsed)
	}
}

// TestContainerInstanceInvoke_logTailIsOptIn pins the gate that keeps the wait
// off the paths that never look at LogResult. Warm invoke p50 is ~6 ms
// (docs/plans/lambda-cold-start.md), so a wait long enough to be correct is
// long enough to dominate it — asynchronous invokes, event-source mappings,
// function URLs and service-to-service calls all discard the tail and must not
// pay for one.
func TestContainerInstanceInvoke_logTailIsOptIn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Given: a container whose logs are being captured.
	ci, addr := newRICBackedContainerInstance(t)
	ci.logWriter = noopLogWriter{}
	ci.logCtx, ci.logCancel = context.WithCancel(context.Background())
	t.Cleanup(ci.logCancel)

	// When: the caller does not ask for a tail.
	go serveOneInvocation(t, addr)
	result, err := ci.Invoke(ctx, []byte(`{}`), InvokeOptions{})
	if err != nil {
		t.Fatalf("invoke without tail: %v", err)
	}

	// Then: no tail is produced, even though the buffer holds START/END/REPORT.
	if result.LogResult != "" {
		t.Errorf("LogResult should be empty when no tail was requested, got %q", result.LogResult)
	}

	// When: the caller asks for one.
	go serveOneInvocation(t, addr)
	result, err = ci.Invoke(ctx, []byte(`{}`), InvokeOptions{LogTail: true})
	if err != nil {
		t.Fatalf("invoke with tail: %v", err)
	}

	// Then: it carries this invocation's platform lines.
	decoded, err := base64.StdEncoding.DecodeString(result.LogResult)
	if err != nil {
		t.Fatalf("decode LogResult: %v", err)
	}
	for _, want := range []string{"START RequestId:", "END RequestId:", "REPORT RequestId:"} {
		if !strings.Contains(string(decoded), want) {
			t.Errorf("tail is missing %q:\n%s", want, decoded)
		}
	}
}
