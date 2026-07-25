package router

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
)

// flushRecorder is a ResponseRecorder that also implements http.Flusher,
// and signals on every Flush so tests can wait for output reliably.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushSig chan struct{}
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		flushSig:         make(chan struct{}, 64),
	}
}

// Flush implements http.Flusher and signals waiters.
func (f *flushRecorder) Flush() {
	f.ResponseRecorder.Flush()
	select {
	case f.flushSig <- struct{}{}:
	default:
	}
}

// waitFlush blocks until at least one Flush is signalled, or the deadline
// expires. It returns the body written so far.
func (f *flushRecorder) waitFlush(t *testing.T, timeout time.Duration) string {
	t.Helper()
	select {
	case <-f.flushSig:
		return f.ResponseRecorder.Body.String()
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE flush")
		return ""
	}
}

// waitForFlushes blocks until exactly n Flush() calls have been observed,
// then returns the body. Unlike calling waitFlush n times, this only reads
// Body once — after the Nth signal — instead of once per signal.
//
// That distinction matters under -race: the handler writes every SSE frame
// from a single goroutine with no gap between writes when several events
// are ready at once (e.g. replaying a multi-event history snapshot). If a
// caller reads Body after signal k while the writer is already partway
// through producing write k+1 (which is unsynchronized relative to that
// read — only send k has a happens-before edge to it), the race detector
// correctly flags it even though the outcome is harmless in practice.
// Draining every expected signal first and reading Body only after the
// last one ties the single read to the last write we care about, with no
// further writes racing against it.
func (f *flushRecorder) waitForFlushes(t *testing.T, n int, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for i := 0; i < n; i++ {
		select {
		case <-f.flushSig:
		case <-deadline:
			t.Fatalf("timed out waiting for flush %d/%d", i+1, n)
			return ""
		}
	}
	return f.ResponseRecorder.Body.String()
}

// --- helpers ----------------------------------------------------------------

func newTestBus() *events.Bus        { return events.NewBus() }
func newTestShutdown() chan struct{} { return make(chan struct{}) }
func nopLogger() *zap.Logger         { return zap.NewNop() }

// doSSERequest starts a GET /_events request against handler in a goroutine,
// using a cancelable context so the test can disconnect the client.
// It returns the recorder and the cancel function.
func doSSERequest(handler http.HandlerFunc, query string) (*flushRecorder, context.CancelFunc) {
	rec := newFlushRecorder()
	url := "/_events"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	go handler(rec, req)
	return rec, cancel
}

// publishAfter waits briefly then publishes an event so the handler's goroutine
// has time to subscribe before the event arrives.
func publishAfter(bus *events.Bus, e events.Event, d time.Duration) {
	time.AfterFunc(d, func() {
		bus.Publish(context.Background(), e)
	})
}

// readSSELines scans lines from body, returning only non-empty ones.
func readSSELines(body string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		if l := sc.Text(); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// --- tests ------------------------------------------------------------------

func TestEventsHandler_SetsSSEHeaders(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()

	// Wait for the initial flush (": connected").
	rec.waitFlush(t, time.Second)

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestEventsHandler_SendsConnectedComment(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()

	body := rec.waitFlush(t, time.Second)
	if !strings.Contains(body, ": connected") {
		t.Errorf("expected ': connected' comment in initial flush, got: %q", body)
	}
}

func TestEventsHandler_DeliversEventAsSSEData(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()

	// Wait for connect flush.
	rec.waitFlush(t, time.Second)

	// Publish an event a short time after subscription is set up.
	publishAfter(bus, events.Event{
		Type:    events.S3ObjectCreated,
		Source:  "s3",
		Time:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload: map[string]string{"key": "myfile.txt"},
	}, 20*time.Millisecond)

	// Drain the event flush.
	body := rec.waitFlush(t, 2*time.Second)

	var found *sseEnvelope
	for _, line := range readSSELines(body) {
		prefix := "data: "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		var env sseEnvelope
		if err := json.Unmarshal([]byte(line[len(prefix):]), &env); err != nil {
			continue
		}
		if env.Type == string(events.S3ObjectCreated) {
			found = &env
			break
		}
	}

	if found == nil {
		t.Fatalf("expected SSE data line with type %q; body so far:\n%s", events.S3ObjectCreated, body)
	}
	if found.Source != "s3" {
		t.Errorf("Source = %q, want s3", found.Source)
	}
	if found.Time == "" {
		t.Error("Time field is empty")
	}
}

func TestEventsHandler_SourceFilterDeliverMatchingSource(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	// Filter to s3 only.
	rec, cancel := doSSERequest(handler, "source=s3")
	defer cancel()

	rec.waitFlush(t, time.Second)

	publishAfter(bus, events.Event{
		Type:   events.S3ObjectCreated,
		Source: "s3",
		Time:   time.Now(),
	}, 20*time.Millisecond)

	body := rec.waitFlush(t, 2*time.Second)
	if !strings.Contains(body, string(events.S3ObjectCreated)) {
		t.Errorf("expected s3 event in body; got:\n%s", body)
	}
}

func TestEventsHandler_SourceFilterDropsNonMatchingSource(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	// Filter to sqs only — publish an s3 event, expect no data frame.
	rec, cancel := doSSERequest(handler, "source=sqs")
	defer cancel()

	rec.waitFlush(t, time.Second)

	// Publish s3, which should be filtered out.
	bus.Publish(context.Background(), events.Event{
		Type:   events.S3ObjectCreated,
		Source: "s3",
		Time:   time.Now(),
	})

	// Give the bus goroutine time to process and (not) deliver.
	time.Sleep(50 * time.Millisecond)

	body := rec.Body.String()
	for _, line := range readSSELines(body) {
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, string(events.S3ObjectCreated)) {
			t.Errorf("s3 event should have been filtered; got line: %s", line)
		}
	}
}

func TestEventsHandler_MultipleSourceFilters(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	rec, cancel := doSSERequest(handler, "source=s3&source=sqs")
	defer cancel()

	rec.waitFlush(t, time.Second)

	publishAfter(bus, events.Event{
		Type:   events.SQSQueueCreated,
		Source: "sqs",
		Time:   time.Now(),
	}, 20*time.Millisecond)

	body := rec.waitFlush(t, 2*time.Second)
	if !strings.Contains(body, string(events.SQSQueueCreated)) {
		t.Errorf("expected sqs event; got:\n%s", body)
	}
}

func TestEventsHandler_ShutdownClosesStream(t *testing.T) {
	bus := newTestBus()
	shutdownCh := make(chan struct{})
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	done := make(chan struct{})
	rec := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_events", nil)

	go func() {
		defer close(done)
		handler(rec, req)
	}()

	// Wait for the connected flush, then close the shutdown channel.
	rec.waitFlush(t, time.Second)
	close(shutdownCh)

	select {
	case <-done:
		// handler returned promptly — pass
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after shutdownCh was closed")
	}
}

func TestEventsHandler_ClientDisconnectClosesStream(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	done := make(chan struct{})
	rec := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	go func() {
		defer close(done)
		handler(rec, req)
	}()

	rec.waitFlush(t, time.Second)
	cancel() // simulate client disconnect

	select {
	case <-done:
		// handler returned promptly — pass
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}
}

// nonFlusherWriter wraps ResponseRecorder and hides the http.Flusher interface
// so that the handler's flusher-capability check returns false.
type nonFlusherWriter struct {
	http.ResponseWriter
}

func TestEventsHandler_NonFlusherReturns500(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_events", nil)
	handler(nonFlusherWriter{rec}, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for non-flusher writer, got %d", rec.Code)
	}
}

func TestEventsHandler_EnvelopeTimeIsRFC3339Nano(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()

	rec.waitFlush(t, time.Second)

	ts := time.Date(2026, 4, 2, 12, 0, 0, 123456789, time.UTC)
	publishAfter(bus, events.Event{
		Type:   events.S3ObjectCreated,
		Source: "s3",
		Time:   ts,
	}, 20*time.Millisecond)

	body := rec.waitFlush(t, 2*time.Second)

	for _, line := range readSSELines(body) {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var env sseEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &env); err != nil {
			continue
		}
		if env.Type != string(events.S3ObjectCreated) {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, env.Time)
		if err != nil {
			t.Errorf("Time field %q is not RFC3339Nano: %v", env.Time, err)
		}
		if !parsed.UTC().Equal(ts.UTC()) {
			t.Errorf("Time = %v, want %v", parsed.UTC(), ts.UTC())
		}
		return
	}
	t.Fatal("did not find expected data line")
}

// --- history replay ----------------------------------------------------------
//
// A note on synchronisation in these tests: the handler writes every SSE
// frame (replayed or live) from a single goroutine, and flushRecorder
// signals flushSig once per Flush() call. Reading rec.Body while that
// goroutine might still be mid-write on a *later* frame races under -race
// even when the read is "morally" safe (e.g. the outstanding write is one
// we don't care about yet) — see waitForFlushes' doc comment. These tests
// therefore drain the exact, deterministic number of frames a scenario
// produces (1 for ": connected", plus 1 per event that is not filtered
// out) and read Body exactly once, after the last of them.

// TestEventsHandler_ReplaysHistoryOnConnect verifies the core feature: an
// event published before a client connects must still show up when that
// client connects, replayed from the bus's history buffer.
func TestEventsHandler_ReplaysHistoryOnConnect(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	// Publish before any client connects — this lands in history, not on
	// any live subscriber (there isn't one yet).
	bus.Publish(context.Background(), events.Event{
		Type:    events.S3ObjectCreated,
		Source:  "s3",
		Time:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload: map[string]string{"key": "history-event.txt"},
	})

	rec, cancel := doSSERequest(handler, "")
	defer cancel()

	// frame 1: ": connected", frame 2: the replayed event.
	body := rec.waitForFlushes(t, 2, time.Second)

	if !strings.Contains(body, "history-event.txt") {
		t.Errorf("expected replayed history event; got:\n%s", body)
	}
}

// TestEventsHandler_ReplayThenLive_NoGapNoDuplicate publishes a burst of
// events to an already-connected client and verifies every one arrives
// exactly once via the live path. The harder guarantee — that an event
// published concurrently with the connect itself is delivered exactly once
// via replay-or-live — is covered at the Bus level by
// TestBus_SnapshotAndSubscribeAll_NoGapNoDuplicate in the events package,
// where it can use a synchronised counter instead of scraping an
// http.ResponseRecorder body that a live writer goroutine is still
// appending to.
func TestEventsHandler_ReplayThenLive_NoGapNoDuplicate(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()
	rec.waitForFlushes(t, 1, time.Second) // ": connected" — subscription is now live.

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			bus.Publish(context.Background(), events.Event{
				Type:    events.S3ObjectCreated,
				Source:  "s3",
				Payload: map[string]string{"key": fmt.Sprintf("obj-%d.txt", i)},
			})
		}(i)
	}
	wg.Wait()

	// Exactly n more frames are expected (one per event, all delivered
	// live since the subscription was established before any publish).
	body := rec.waitForFlushes(t, n, 2*time.Second)

	for i := 0; i < n; i++ {
		want := fmt.Sprintf(`"key":"obj-%d.txt"`, i)
		if got := strings.Count(body, want); got != 1 {
			t.Errorf("key obj-%d.txt appears %d times in SSE body, want exactly 1", i, got)
		}
	}
}

// TestEventsHandler_HistoryRespectsSourceFilter verifies replayed history
// events are subject to the same ?source= filter as live events: a
// filtered-out event never reaches writeSSEEvent, so it produces no frame
// at all (not an empty one), which is why only one further flush (for the
// sqs event) is expected after ": connected".
func TestEventsHandler_HistoryRespectsSourceFilter(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), shutdownCh)

	bus.Publish(context.Background(), events.Event{
		Type:   events.S3ObjectCreated,
		Source: "s3",
		Time:   time.Now(),
	})
	bus.Publish(context.Background(), events.Event{
		Type:   events.SQSQueueCreated,
		Source: "sqs",
		Time:   time.Now(),
	})

	rec, cancel := doSSERequest(handler, "source=sqs")
	defer cancel()

	// frame 1: ": connected", frame 2: the sqs event (s3 is filtered — no frame for it).
	body := rec.waitForFlushes(t, 2, time.Second)

	if strings.Contains(body, string(events.S3ObjectCreated)) {
		t.Errorf("s3 history event should have been filtered out; got:\n%s", body)
	}
	if !strings.Contains(body, string(events.SQSQueueCreated)) {
		t.Errorf("expected sqs history event to be replayed; got:\n%s", body)
	}
}
