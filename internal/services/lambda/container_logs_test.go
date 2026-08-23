package lambda

// container_logs_test.go — the host end of the in-container init's log channel.
//
// These replace the mock-clock wait tests this cut-over deleted
// (log_tail_wait_test.go, log_drain_wait_test.go). The subject changed: there
// is no longer any inference to test, so nothing here asserts a duration. What
// is asserted instead are the protocol's facts — a frame goes where its Req
// says, a replayed frame is dropped, a wait ends on the event rather than on a
// clock.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

// newTestLogSink builds a sink with no Runtime API behind it: the Telemetry
// publish is skipped while the container has no address, which is exactly what
// a sink built before its container looks like.
func newTestLogSink(t *testing.T, writer events.LogWriter) *logSink {
	t.Helper()
	s := newLogSink(logSinkConfig{
		logger:    zap.NewNop(),
		clk:       clock.New(),
		logWriter: writer,
		group:     "/aws/lambda/fn",
		stream:    "stream",
		region:    "us-east-1",
	})
	t.Cleanup(s.close)
	return s
}

// frameOf is one ordinary stdout line for a request.
func frameOf(seq uint64, req, msg string) initproto.Frame {
	return initproto.Frame{Seq: seq, Req: req, Src: initproto.SrcStdout, T: 1700000000000, Msg: msg}
}

// A line the init read during an invocation belongs to that invocation's tail,
// and to CloudWatch.
func TestLogSink_frameReachesTheRequestTailAndCloudWatch(t *testing.T) {
	writer := &stubLogWriter{}
	s := newTestLogSink(t, writer)

	if !s.frame(frameOf(1, "req-1", "hello")) {
		t.Fatal("the sink did not batch a live frame")
	}
	s.flush()

	if got := string(s.snapshot("req-1")); got != "hello\n" {
		t.Errorf("req-1 tail = %q, want %q", got, "hello\n")
	}
	if got := writer.messages(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("CloudWatch messages = %v, want [hello]", got)
	}
}

// The init replays its backlog from the oldest frame it still holds after a
// reconnect, because it cannot know which of the frames it wrote to a
// connection that then failed arrived. The sequence is what tells the
// duplicates apart, and a duplicate must reach neither the tail nor CloudWatch.
func TestLogSink_replayedFramesAreDroppedBySeq(t *testing.T) {
	writer := &stubLogWriter{}
	s := newTestLogSink(t, writer)

	s.frame(frameOf(1, "req-1", "one"))
	s.frame(frameOf(2, "req-1", "two"))
	// The connection broke here; the init reconnects and replays from seq 1.
	s.frame(frameOf(1, "req-1", "one"))
	s.frame(frameOf(2, "req-1", "two"))
	s.frame(frameOf(3, "req-1", "three"))
	s.flush()

	if got := string(s.snapshot("req-1")); got != "one\ntwo\nthree\n" {
		t.Errorf("req-1 tail = %q, want each line exactly once in order", got)
	}
	if got := writer.messages(); len(got) != 3 {
		t.Errorf("CloudWatch messages = %v, want three", got)
	}
}

// A gap frame says the init's replay backlog overflowed: those lines are never
// coming. It carries the sequence of the last frame that was lost, so the host
// still sees an unbroken sequence — and it is not a line, so nothing about it
// reaches the tail or CloudWatch.
func TestLogSink_gapFrameAdvancesTheSequenceWithoutALine(t *testing.T) {
	writer := &stubLogWriter{}
	s := newTestLogSink(t, writer)

	s.frame(frameOf(1, "req-1", "one"))
	if s.frame(initproto.Frame{Seq: 40, T: 1700000000000, Gap: 39}) {
		t.Fatal("a gap frame was batched as a line")
	}
	s.frame(frameOf(41, "req-1", "after the gap"))
	s.flush()

	if got := string(s.snapshot("req-1")); got != "one\nafter the gap\n" {
		t.Errorf("req-1 tail = %q, want only the two real lines", got)
	}
	if got := writer.messages(); len(got) != 2 {
		t.Errorf("CloudWatch messages = %v, want two", got)
	}
	// The gap advanced the watermark, so an invocation waiting on a sequence
	// inside the lost run is released rather than held to its bound.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !s.awaitLogSeq(ctx, 20) {
		t.Error("a wait for a sequence the gap covered was not satisfied")
	}
}

// The whole point of the protocol: two invocations on one warm container, each
// tail holding exactly its own lines.
func TestLogSink_perRequestBuffersAreIsolated(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})

	s.synth("req-1", "START RequestId: req-1")
	s.frame(frameOf(1, "req-1", "first invocation"))
	s.synth("req-1", "END RequestId: req-1")
	s.synth("req-2", "START RequestId: req-2")
	s.frame(frameOf(2, "req-2", "second invocation"))
	s.synth("req-2", "END RequestId: req-2")

	one := string(s.snapshot("req-1"))
	two := string(s.snapshot("req-2"))
	if one != "START RequestId: req-1\nfirst invocation\nEND RequestId: req-1\n" {
		t.Errorf("req-1 tail = %q", one)
	}
	if two != "START RequestId: req-2\nsecond invocation\nEND RequestId: req-2\n" {
		t.Errorf("req-2 tail = %q", two)
	}
	if strings.Contains(two, "first invocation") {
		t.Error("req-2's tail leaked req-1's output")
	}
}

// A line the init read outside any invocation carries no request — the INIT
// phase, and the space between invocations. It is what the INIT-timeout
// diagnostic quotes, and it must not open the next invocation's tail.
func TestLogSink_unattributedFramesGoToTheInitBuffer(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})

	s.frame(frameOf(1, "", "loading handler"))
	s.frame(frameOf(2, "req-1", "in the handler"))

	if got := s.initOutput(); got != "loading handler\n" {
		t.Errorf("init buffer = %q, want the unattributed line", got)
	}
	if got := string(s.snapshot("req-1")); got != "in the handler\n" {
		t.Errorf("req-1 tail = %q, want only its own line", got)
	}
}

// X-Amz-Log-Result is "the last 4 KB of the execution log". A handler that
// prints more than that gets the most recent 4 KB — the same promise as before,
// now per request.
func TestLogSink_tailIsBoundedAtFourKiB(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})

	line := strings.Repeat("x", 200)
	for seq := uint64(1); seq <= 100; seq++ {
		s.frame(frameOf(seq, "req-1", line))
	}
	s.frame(frameOf(101, "req-1", "the last line"))

	snap := s.snapshot("req-1")
	if len(snap) > tailMaxBytes {
		t.Errorf("tail is %d bytes, want at most %d", len(snap), tailMaxBytes)
	}
	if !strings.HasSuffix(string(snap), "the last line\n") {
		t.Error("the tail does not end with the most recent line")
	}
}

// An extension's output reaches the Telemetry/Logs API as an extension record.
// The init tags the frame with the extension it started it from, so there is no
// prefix convention to parse back out.
func TestLogSink_extensionFramePublishesAnExtensionRecord(t *testing.T) {
	api, addr := newRuntimeAPITestServer(t)
	const containerIP = "127.0.0.1"
	api.RegisterContainerConfig(containerIP, runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:fn",
		FunctionName: "fn",
	})
	extID := registerExtension(t, http.DefaultClient, addr, "logs-extension")
	received := make(chan []map[string]any, 4)
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var batch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- batch
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()
	subscribeToLogs(t, addr, extID, dest.URL, "function", "extension")

	s := newLogSink(logSinkConfig{
		logger:     zap.NewNop(),
		clk:        clock.New(),
		group:      "/aws/lambda/fn",
		stream:     "stream",
		region:     "us-east-1",
		runtimeAPI: api,
	})
	t.Cleanup(s.close)
	s.attach(containerIP, "container123456")

	s.frame(initproto.Frame{Seq: 1, Req: "req-1", Src: initproto.ExtensionSrc("my-ext"), Msg: "extension says hello"})
	s.frame(frameOf(2, "req-1", "handler says hello"))

	got := map[string]string{}
	for len(got) < 2 {
		select {
		case batch := <-received:
			for _, event := range batch {
				record, _ := event["record"].(string)
				typ, _ := event["type"].(string)
				got[record] = typ
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the Logs API destination received %v, want both records", got)
		}
	}
	if got["extension says hello"] != "extension" {
		t.Errorf("the extension's line was published as type %q, want extension", got["extension says hello"])
	}
	if got["handler says hello"] != "function" {
		t.Errorf("the handler's line was published as type %q, want function", got["handler says hello"])
	}
	// Both are the container's output, so both are in the tail — without the
	// [overcast-extension:…] prefix the shell bootstrap used to add.
	if tail := string(s.snapshot("req-1")); tail != "extension says hello\nhandler says hello\n" {
		t.Errorf("req-1 tail = %q", tail)
	}
}

// subscribeToLogs registers a Logs API subscription for an extension.
func subscribeToLogs(t *testing.T, addr, extID, destURL string, types ...string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"types": types,
		"buffering": map[string]any{
			"timeoutMs": 1000,
			"maxBytes":  262144,
			"maxItems":  1000,
		},
		"destination": map[string]string{"protocol": "HTTP", "URI": destURL},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, "http://"+addr+"/2020-08-15/logs", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Identifier", extID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Logs API subscribe status = %d, want 200", resp.StatusCode)
	}
}

// ApplicationLogLevel filters what reaches CloudWatch and the tail.
func TestLogSink_filteredFrameReachesNeitherTailNorCloudWatch(t *testing.T) {
	writer := &stubLogWriter{}
	s := newLogSink(logSinkConfig{
		logger:      zap.NewNop(),
		clk:         clock.New(),
		logWriter:   writer,
		group:       "/aws/lambda/fn",
		stream:      "stream",
		region:      "us-east-1",
		logFormat:   logFormatJSON,
		appLogLevel: logLevelWarn,
	})
	t.Cleanup(s.close)

	if s.frame(frameOf(1, "req-1", `{"level":"DEBUG","msg":"noisy"}`)) {
		t.Fatal("the sink batched a frame the log level filters out")
	}
	if n := s.flush(); n != 0 {
		t.Fatalf("flush = %d after a filtered frame, want 0", n)
	}
	if got := string(s.snapshot("req-1")); got != "" {
		t.Errorf("req-1 tail = %q after a filtered frame, want empty", got)
	}
	if got := writer.messages(); len(got) != 0 {
		t.Errorf("CloudWatch messages = %v, want none", got)
	}
}

// The wait the invoke path runs, satisfied by the frame arriving. It is
// event-driven: the frame lands on another goroutine and the waiter wakes on
// it, with no clock involved.
func TestLogSink_awaitLogSeqIsSatisfiedByTheFrame(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.streamOpened()

	go func() {
		s.frame(frameOf(1, "req-1", "one"))
		s.frame(frameOf(2, "req-1", "two"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !s.awaitLogSeq(ctx, 2) {
		t.Fatal("awaitLogSeq was not satisfied by the frames arriving")
	}
	if got := string(s.snapshot("req-1")); got != "one\ntwo\n" {
		t.Errorf("req-1 tail = %q once the wait returned", got)
	}
}

// The invariant the whole cut-over rests on: when awaitLogSeq reports that a
// sequence has arrived, that sequence's line is already in the tail and already
// in the CloudWatch batch. The invoke path snapshots the tail on the very next
// statement, so a waiter woken even a few instructions early gets a tail
// missing the line it waited for — the class of race the init exists to remove,
// reintroduced on the host side.
//
// It is a concurrency invariant, so it is exercised rather than reasoned about:
// several waiters park on the sequence before the frame exists, and each
// records what the tail held at the instant its wait returned. Publishing the
// sequence before appending the line — the ordering this replaced — fails this
// well inside the iteration count under `-race`, whose instrumentation is what
// makes a window of a few instructions reliably observable.
func TestLogSink_awaitLogSeqNeverReturnsBeforeTheLineIsInTheTail(t *testing.T) {
	const (
		iterations = 200
		waiters    = 8
	)
	for i := 0; i < iterations; i++ {
		s := newTestLogSink(t, &stubLogWriter{})
		s.streamOpened()

		const req = "req-1"
		line := fmt.Sprintf("line for iteration %d", i)

		// The waiters park on the sequence before the frame exists, which is
		// the state the invoke path is in: the response has named a sequence
		// the ingest has not reached yet.
		type observation struct {
			arrived bool
			tail    string
		}
		parked := sync.WaitGroup{}
		parked.Add(waiters)
		seen := make(chan observation, waiters)
		for w := 0; w < waiters; w++ {
			go func() {
				parked.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				arrived := s.awaitLogSeq(ctx, 1)
				// Snapshot immediately, exactly as Invoke does.
				seen <- observation{arrived: arrived, tail: string(s.snapshot(req))}
			}()
		}
		parked.Wait()

		s.frame(frameOf(1, req, line))

		for w := 0; w < waiters; w++ {
			select {
			case got := <-seen:
				if !got.arrived {
					t.Fatalf("iteration %d: awaitLogSeq reported the frame never arrived", i)
				}
				if got.tail != line+"\n" {
					t.Fatalf("iteration %d: awaitLogSeq returned with the tail at %q, want %q — the sequence was published before the line was appended",
						i, got.tail, line+"\n")
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("iteration %d: awaitLogSeq never returned", i)
			}
		}
	}
}

// The same invariant for the CloudWatch side: a flush issued the moment the
// wait returns must already carry the line, because that is what puts END and
// REPORT after it in the log stream rather than in front of it.
func TestLogSink_awaitLogSeqNeverReturnsBeforeTheLineIsInTheBatch(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		writer := &stubLogWriter{}
		s := newTestLogSink(t, writer)
		s.streamOpened()

		line := fmt.Sprintf("batched line %d", i)
		parked := make(chan struct{})
		flushed := make(chan int, 1)
		go func() {
			close(parked)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.awaitLogSeq(ctx, 1)
			flushed <- s.flush()
		}()
		<-parked

		s.frame(frameOf(1, "req-1", line))

		select {
		case n := <-flushed:
			if n != 1 {
				t.Fatalf("iteration %d: the flush that followed awaitLogSeq carried %d lines, want 1 — the sequence was published before the line was batched", i, n)
			}
			if got := writer.messages(); len(got) != 1 || got[0] != line {
				t.Fatalf("iteration %d: CloudWatch got %v, want [%s]", i, got, line)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("iteration %d: awaitLogSeq never returned", i)
		}
	}
}

// A sequence that will never arrive because the init's stream has ended is not
// worth waiting for: the crash path depends on this returning rather than
// holding the teardown to its bound.
func TestLogSink_awaitLogSeqEndsWhenTheStreamDoes(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.streamOpened()

	go func() {
		s.frame(frameOf(1, "req-1", "one"))
		s.streamClosed()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.awaitLogSeq(ctx, 9) {
		t.Fatal("awaitLogSeq reported the frames arrived, but the stream ended first")
	}
}

// And when the channel is merely broken — the stream open and nothing coming —
// the caller's bound is what ends the wait. The tail is then what arrived,
// which is what LogResult promises.
func TestLogSink_awaitLogSeqGivesUpAtTheCallersBound(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.streamOpened()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if s.awaitLogSeq(ctx, 5) {
		t.Fatal("awaitLogSeq reported frames that never arrived")
	}
}

// Nothing to wait for is not the same as waiting: a response with no log-seq
// header comes from a Runtime API client that is not behind our init, and must
// not cost the invoke anything at all.
func TestLogSink_awaitLogSeqZeroReturnsImmediately(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.streamOpened()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already dead: a wait that consulted it would report failure
	if !s.awaitLogSeq(ctx, 0) {
		t.Fatal("awaitLogSeq(0) waited for something")
	}
}

// The crash and timeout paths wait for the init's stream to end. A sink no
// connection ever reached has nothing to drain, and must not spend the bound.
func TestLogSink_awaitStreamEndWithoutAConnection(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !s.awaitStreamEnd(ctx) {
		t.Fatal("awaitStreamEnd waited on a sink nothing ever connected to")
	}
}

func TestLogSink_awaitStreamEndWaitsForTheClose(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.streamOpened()

	go func() {
		s.frame(frameOf(1, "req-1", "dying words"))
		s.streamClosed()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !s.awaitStreamEnd(ctx) {
		t.Fatal("awaitStreamEnd did not see the stream close")
	}
	if got := string(s.snapshot("req-1")); got != "dying words\n" {
		t.Errorf("req-1 tail = %q, want the line published before the close", got)
	}
}

// A stream still open and an init still running is the abandoned-invoke case:
// the wait ends on the caller's bound.
func TestLogSink_awaitStreamEndGivesUpAtTheCallersBound(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.streamOpened()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if s.awaitStreamEnd(ctx) {
		t.Fatal("awaitStreamEnd reported an end on a stream that is still open")
	}
}

// The INIT phase, and the space between invocations, must reach CloudWatch in
// front of the START of the invocation that follows them — which is where AWS
// puts them, and where Overcast put them before the init existed.
//
// The init stamps what it had published when the runtime went idle onto its
// GET /next; the host records it here, and the invoke path waits for it before
// writing START. This asserts the ordering the wait exists to produce: a START
// written while the awaited frame is still in flight must land after it.
func TestLogSink_startWaitsForTheOutputThatPrecedesIt(t *testing.T) {
	writer := &stubLogWriter{}
	s := newTestLogSink(t, writer)
	s.streamOpened()

	// The runtime printed one line during INIT and then asked for work, so the
	// init told the host "everything up to seq 1 came before this poll".
	s.noteIdleSeq(1)
	if got := s.idleSequence(); got != 1 {
		t.Fatalf("idleSequence = %d, want 1", got)
	}
	// A later poll reporting an older watermark must not move it backwards.
	s.noteIdleSeq(0)
	if got := s.idleSequence(); got != 1 {
		t.Fatalf("idleSequence = %d after an older poll, want 1", got)
	}

	// The frame is still in flight when the invocation begins.
	started := make(chan struct{})
	go func() {
		close(started)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if !s.awaitLogSeq(ctx, s.idleSequence()) {
			t.Errorf("the wait for the INIT output was not satisfied")
		}
		s.synth("req-1", "START RequestId: req-1")
	}()
	<-started

	s.frame(frameOf(1, "", "INIT phase output"))

	// Whatever order the goroutines ran in, CloudWatch must show the INIT line
	// first: the START could not be written until the frame had landed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := writer.messages(); len(got) == 2 {
			if got[0] != "INIT phase output" || got[1] != "START RequestId: req-1" {
				t.Fatalf("CloudWatch order = %v, want the INIT line before START", got)
			}
			// And it belongs to no invocation's tail.
			if tail := string(s.snapshot("req-1")); strings.Contains(tail, "INIT phase output") {
				t.Errorf("the INIT line leaked into the invocation's tail: %q", tail)
			}
			if got := s.initOutput(); got != "INIT phase output\n" {
				t.Errorf("init buffer = %q, want the unattributed line", got)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("CloudWatch never received both lines; got %v", writer.messages())
}

// A container whose init never reported an idle watermark — a Runtime API
// client that is not behind our init — must not cost the invoke its bound.
func TestLogSink_startDoesNotWaitWhenNothingPrecededIt(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.streamOpened()
	if got := s.idleSequence(); got != 0 {
		t.Fatalf("idleSequence = %d before any /next, want 0", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !s.awaitLogSeq(ctx, s.idleSequence()) {
		t.Fatal("the invoke waited on an idle watermark that was never reported")
	}
}

// A request's buffer goes when its invoke has answered, so a warm environment
// serving thousands of invocations holds one at a time rather than all of them.
func TestLogSink_releaseDropsTheRequestBuffer(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	s.frame(frameOf(1, "req-1", "hello"))
	s.release("req-1")
	if got := s.snapshot("req-1"); got != nil {
		t.Errorf("req-1 tail = %q after release, want nothing", got)
	}
}

// An invocation that never reaches its release — an abandoned caller, a
// container torn down mid-flight — must not leak a buffer either.
func TestLogSink_retainedRequestBuffersAreBounded(t *testing.T) {
	s := newTestLogSink(t, &stubLogWriter{})
	for i := 0; i < retainedRequestBuffers*3; i++ {
		s.frame(frameOf(uint64(i+1), fmt.Sprintf("req-%d", i), "line"))
	}
	s.mu.Lock()
	held := len(s.buffers)
	s.mu.Unlock()
	if held > retainedRequestBuffers {
		t.Errorf("the sink holds %d request buffers, want at most %d", held, retainedRequestBuffers)
	}
}

// START, END and REPORT never come off the container, so they are written on
// their own — with the same bounded retry, because a CloudWatch store that is
// momentarily busy must not cost an invocation its REPORT line.
func TestLogSink_synthLineRetriesATransientWriteFailure(t *testing.T) {
	writer := &stubLogWriter{failWrites: 1}
	s := newTestLogSink(t, writer)

	s.synth("req-1", "REPORT RequestId: req-1")

	if got := writer.messages(); len(got) != 1 || got[0] != "REPORT RequestId: req-1" {
		t.Fatalf("messages = %v, want the REPORT line", got)
	}
	if writer.ensures() == 0 {
		t.Fatal("EnsureLogStream was not called")
	}
}

func TestLogSink_synthLineWithoutACloudWatchWriter(t *testing.T) {
	s := newTestLogSink(t, nil)
	s.synth("req-1", "REPORT RequestId: req-1")
	if got := string(s.snapshot("req-1")); got != "REPORT RequestId: req-1\n" {
		t.Errorf("req-1 tail = %q with no writer wired, want the REPORT line", got)
	}
}

// CloudWatch ordering is exact because a synthesised line flushes the ingest's
// pending batch before it is written: END must never land in front of the
// output it closes.
func TestLogSink_synthFlushesTheIngestBatchFirst(t *testing.T) {
	writer := &stubLogWriter{}
	s := newTestLogSink(t, writer)

	s.synth("req-1", "START RequestId: req-1")
	s.frame(frameOf(1, "req-1", "handler line"))
	s.synth("req-1", "END RequestId: req-1")
	s.synth("req-1", "REPORT RequestId: req-1")

	want := []string{
		"START RequestId: req-1",
		"handler line",
		"END RequestId: req-1",
		"REPORT RequestId: req-1",
	}
	got := writer.messages()
	if len(got) != len(want) {
		t.Fatalf("CloudWatch messages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("CloudWatch messages = %v, want %v", got, want)
		}
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
