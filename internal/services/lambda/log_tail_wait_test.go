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

// newTailWaitInstance returns the minimum containerInstance waitForScannerIdle
// touches: a clock, and the atomics streamLogs would be updating.
func newTailWaitInstance(clk clock.Clock) *containerInstance {
	return &containerInstance{
		id:        "cafebabe1234deadbeef",
		logger:    zap.NewNop(),
		clk:       clk,
		logWriter: noopLogWriter{},
	}
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
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		ci.waitForScannerIdle(mark)
	}()
	<-started
	// Let the goroutine reach its blocking select and register its timers
	// against the frozen clock. Nothing can happen while it does: the wait has
	// no wall-clock path out, so this sleep bounds goroutine start-up only.
	time.Sleep(50 * time.Millisecond)

	delivered := deliverAt < 0
	giveUp := time.After(30 * time.Second)
	for {
		select {
		case <-done:
			return mock.Now().Sub(start)
		case <-giveUp:
			t.Fatal("waitForScannerIdle never returned")
			return 0
		default:
			if !delivered && mock.Now().Sub(start) >= deliverAt {
				deliverLine(ci, line)
				delivered = true
			}
			mock.Add(time.Millisecond)
		}
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

// TestWaitForScannerIdle_silentHandlerReturnsPromptly pins the cost of the
// ambiguity the fix introduces: a handler that prints nothing is
// indistinguishable from one whose output has not arrived, so it waits. It must
// wait out firstReadMax and stop there, not sit on the 100 ms deadline.
func TestWaitForScannerIdle_silentHandlerReturnsPromptly(t *testing.T) {
	// Given: a container invocation that will never produce a log line.
	mock := clock.NewMock()
	mock.Set(time.Unix(1_700_000_000, 0))
	ci := newTailWaitInstance(mock)

	// When: the wait runs with nothing ever delivered.
	elapsed := runScannerWait(t, mock, ci, ci.beginTail(), -1, "")

	// Then: it gives up at the first-read grace period rather than the deadline.
	if elapsed > 30*time.Millisecond {
		t.Errorf("silent handler waited %v, expected to give up around the 25ms first-read grace", elapsed)
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
