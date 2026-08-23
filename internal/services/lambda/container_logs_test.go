package lambda

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/containerlogs"
	"github.com/Neaox/overcast/internal/events"
)

func newLogPipelineInstance(t *testing.T, writer events.LogWriter) *containerInstance {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ci := &containerInstance{
		id:           "container123456",
		functionARN:  "arn:aws:lambda:us-east-1:000000000000:function:fn",
		logGroupName: "/aws/lambda/fn",
		logStream:    "stream",
		logWriter:    writer,
		logger:       zap.NewNop(),
		clk:          clock.New(),
		logCtx:       ctx,
		logCancel:    cancel,
	}
	ci.logSink = &containerLogSink{ci: ci, batcher: ci.newCloudWatchBatcher()}
	return ci
}

func (ci *containerInstance) tailString() string {
	ci.tailMu.Lock()
	defer ci.tailMu.Unlock()
	return string(ci.tailBuf)
}

// A line off the live stream is the function's output for an invocation that is
// still open: it belongs in the tail buffer X-Amz-Log-Result reads, and in
// CloudWatch.
func TestContainerLogSink_liveLineReachesTheTailAndCloudWatch(t *testing.T) {
	writer := &stubLogWriter{}
	ci := newLogPipelineInstance(t, writer)

	if !ci.logSink.Line(containerlogs.Line{Time: time.UnixMilli(1700000000000), Message: "hello"}) {
		t.Fatal("the sink refused a live line")
	}
	ci.logSink.Flush()

	if got := ci.tailString(); got != "hello\n" {
		t.Errorf("tail buffer = %q, want %q", got, "hello\n")
	}
	if got := writer.messages(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("CloudWatch messages = %v, want [hello]", got)
	}
}

// A line recovered by the close-time reconciliation pass belongs to an
// invocation that answered long ago, and the container it came from is gone.
// Putting it in the tail buffer would open the next tail with a line the next
// invocation never wrote — so it goes to CloudWatch and no further, which is
// what the reconciliation pass has always done.
func TestContainerLogSink_backfilledLineSkipsTheTailBuffer(t *testing.T) {
	writer := &stubLogWriter{}
	ci := newLogPipelineInstance(t, writer)

	if !ci.logSink.Line(containerlogs.Line{Time: time.UnixMilli(1700000000000), Message: "last words", Backfill: true}) {
		t.Fatal("the sink refused a backfilled line")
	}
	ci.logSink.Flush()

	if got := ci.tailString(); got != "" {
		t.Errorf("tail buffer = %q after a backfilled line, want empty", got)
	}
	if got := writer.messages(); len(got) != 1 || got[0] != "last words" {
		t.Errorf("CloudWatch messages = %v, want [last words]", got)
	}
}

// ApplicationLogLevel filters what reaches CloudWatch, and a filtered line must
// not be counted as pending delivery either — a follower told a line is pending
// when it is not leaves a teardown drain waiting for a write that never comes.
func TestContainerLogSink_filteredLineIsNotDelivered(t *testing.T) {
	writer := &stubLogWriter{}
	ci := newLogPipelineInstance(t, writer)
	ci.logFormat = logFormatJSON
	ci.appLogLevel = logLevelWarn

	if ci.logSink.Line(containerlogs.Line{Message: `{"level":"DEBUG","msg":"noisy"}`}) {
		t.Fatal("the sink accepted a line the log level filters out")
	}
	if n := ci.logSink.Flush(); n != 0 {
		t.Fatalf("Flush = %d after a filtered line, want 0", n)
	}
	if got := writer.messages(); len(got) != 0 {
		t.Errorf("CloudWatch messages = %v, want none", got)
	}
}

// The waits that answer X-Amz-Log-Result read four atomics, and the observer is
// the only thing that moves them. Where each one moves relative to the reads and
// lines it describes is what those waits are built on — see logObserver.
func TestLogObserver_movesThePipelineWatermarks(t *testing.T) {
	ci := newLogPipelineInstance(t, &stubLogWriter{})
	obs := logObserver{ci: ci}

	// Given: a fresh container, before the daemon has been asked anything.
	if ci.logStreamEverAnswered.Load() || ci.logParkedAt.Load() != logReaderNotReading || ci.logReadAt.Load() != 0 {
		t.Fatal("a fresh container should look like nothing has been asked of the daemon")
	}

	// When: the daemon answers and the stream opens.
	obs.StreamAnswered()
	obs.StreamOpened()
	if !ci.logStreamEverAnswered.Load() {
		t.Error("StreamAnswered did not record that the daemon answered")
	}
	if got := ci.logParkedAt.Load(); got != logReaderBetweenReads {
		t.Errorf("logParkedAt = %d with a stream open and no read outstanding, want logReaderBetweenReads", got)
	}

	// When: the reader blocks in a Read.
	obs.ReadStarted()
	if got := ci.logParkedAt.Load(); got <= 0 {
		t.Errorf("logParkedAt = %d inside a Read, want the moment it started", got)
	}

	// When: that Read returns nothing.
	obs.ReadReturned(0)
	if got := ci.logParkedAt.Load(); got != logReaderBetweenReads {
		t.Errorf("logParkedAt = %d after a Read returned, want logReaderBetweenReads", got)
	}
	if ci.logReadAt.Load() != 0 {
		t.Error("a Read that delivered no bytes moved logReadAt")
	}

	// When: one returns bytes.
	obs.ReadStarted()
	obs.ReadReturned(64)
	if ci.logReadAt.Load() == 0 {
		t.Error("a Read that delivered bytes did not move logReadAt")
	}

	// When: lines are assembled and then delivered.
	obs.LineScanned()
	obs.LineScanned()
	if got := ci.logInFlight.Load(); got != 2 {
		t.Errorf("logInFlight = %d with two lines assembled, want 2", got)
	}
	obs.LinesRetired(2)
	if got := ci.logInFlight.Load(); got != 0 {
		t.Errorf("logInFlight = %d after both were delivered, want 0", got)
	}

	// When: the stream goes away.
	obs.StreamClosed()
	if got := ci.logParkedAt.Load(); got != logReaderNotReading {
		t.Errorf("logParkedAt = %d with no stream, want logReaderNotReading", got)
	}
}

// START, END and REPORT never come off the container's log stream, so they are
// written on their own — with the same bounded retry, because a CloudWatch
// store that is momentarily busy must not cost an invocation its REPORT line.
func TestWriteEventsWithRetry_transientFailure(t *testing.T) {
	// Given: a log writer that fails once, then succeeds.
	writer := &stubLogWriter{failWrites: 1}
	ci := newLogPipelineInstance(t, writer)

	// When: events are written durably.
	ok := ci.writeEventsWithRetry(context.Background(), []events.LogEntry{{Timestamp: 1, Message: "hello"}})

	// Then: the transient error is retried after ensuring the stream.
	if !ok {
		t.Fatal("writeEventsWithRetry returned false")
	}
	if got := writer.messages(); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("messages = %v, want [hello]", got)
	}
	if writer.ensures() == 0 {
		t.Fatal("EnsureLogStream was not called")
	}
}

func TestWriteEventsWithRetry_withoutACloudWatchWriter(t *testing.T) {
	ci := newLogPipelineInstance(t, nil)
	if !ci.writeEventsWithRetry(context.Background(), []events.LogEntry{{Message: "hello"}}) {
		t.Fatal("writeEventsWithRetry reported a failure with no writer wired")
	}
}

// stubLogWriter is a CloudWatch Logs store that records what it is given.
type stubLogWriter struct {
	mu          sync.Mutex
	failWrites  int
	writes      [][]events.LogEntry
	ensureCalls int
}

func (w *stubLogWriter) EnsureLogGroup(context.Context, string) error { return nil }

func (w *stubLogWriter) EnsureLogStream(context.Context, string, string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ensureCalls++
	return nil
}

func (w *stubLogWriter) WriteLogEvents(_ context.Context, _, _ string, entries []events.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failWrites > 0 {
		w.failWrites--
		return errors.New("transient write failure")
	}
	w.writes = append(w.writes, append([]events.LogEntry(nil), entries...))
	return nil
}

func (w *stubLogWriter) messages() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, batch := range w.writes {
		for _, e := range batch {
			out = append(out, e.Message)
		}
	}
	return out
}

func (w *stubLogWriter) ensures() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ensureCalls
}
