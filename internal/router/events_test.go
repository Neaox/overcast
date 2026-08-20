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

	"github.com/Neaox/overcast/internal/clock"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
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

// doSSERequest starts a GET /_overcast/events request against handler in a goroutine,
// using a cancelable context so the test can disconnect the client.
// It returns the recorder and the cancel function.
func doSSERequest(handler http.HandlerFunc, query string) (*flushRecorder, context.CancelFunc) {
	rec := newFlushRecorder()
	url := "/_overcast/events"
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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	found := findSSEEnvelope(t, rec.waitFlush(t, 2*time.Second), events.S3ObjectCreated)
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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

	done := make(chan struct{})
	rec := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_overcast/events", nil)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

	done := make(chan struct{})
	rec := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_overcast/events", nil)
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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_overcast/events", nil)
	handler(nonFlusherWriter{rec}, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for non-flusher writer, got %d", rec.Code)
	}
}

func TestEventsHandler_EnvelopeTimeIsRFC3339Nano(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()

	rec.waitFlush(t, time.Second)

	ts := time.Date(2026, 4, 2, 12, 0, 0, 123456789, time.UTC)
	publishAfter(bus, events.Event{
		Type:   events.S3ObjectCreated,
		Source: "s3",
		Time:   ts,
	}, 20*time.Millisecond)

	env := findSSEEnvelope(t, rec.waitFlush(t, 2*time.Second), events.S3ObjectCreated)
	parsed, err := time.Parse(time.RFC3339Nano, env.Time)
	if err != nil {
		t.Fatalf("Time field %q is not RFC3339Nano: %v", env.Time, err)
	}
	if !parsed.UTC().Equal(ts.UTC()) {
		t.Errorf("Time = %v, want %v", parsed.UTC(), ts.UTC())
	}
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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

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

// --- resume (Last-Event-ID) --------------------------------------------------

// publishN publishes n events whose payload carries their index, so a test
// can tell exactly which ones a client was sent.
func publishN(bus *events.Bus, n int) {
	for i := 0; i < n; i++ {
		bus.Publish(context.Background(), events.Event{
			Type:    events.S3ObjectCreated,
			Source:  "s3",
			Payload: map[string]string{"key": fmt.Sprintf("evt-%d.txt", i)},
		})
	}
}

// doSSERequestWithHeaders is doSSERequest with request headers, for the
// Last-Event-ID a browser's EventSource sends on an automatic reconnect.
func doSSERequestWithHeaders(handler http.HandlerFunc, query string, headers map[string]string) (*flushRecorder, context.CancelFunc) {
	rec := newFlushRecorder()
	url := "/_overcast/events"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	go handler(rec, req)
	return rec, cancel
}

// TestEventsHandler_EmitsEventID pins that every data frame carries an SSE
// id. Without it a browser has nothing to send back as Last-Event-ID, and
// every reconnect replays the entire history buffer again.
func TestEventsHandler_EmitsEventID(t *testing.T) {
	bus := newTestBus()
	handler := eventsHandler(bus, nopLogger(), clock.New(), newTestShutdown())

	publishN(bus, 1)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()
	body := rec.waitForFlushes(t, 2, time.Second)

	want := fmt.Sprintf("id: %s-1", bus.RunID())
	if !strings.Contains(body, want) {
		t.Errorf("expected %q in SSE body; got:\n%s", want, body)
	}
}

// TestEventsHandler_ResumeSkipsAlreadyDelivered is the point of the whole
// mechanism: a client that says where it got to is sent only what followed.
func TestEventsHandler_ResumeSkipsAlreadyDelivered(t *testing.T) {
	bus := newTestBus()
	handler := eventsHandler(bus, nopLogger(), clock.New(), newTestShutdown())

	publishN(bus, 3)

	rec, cancel := doSSERequestWithHeaders(handler, "", map[string]string{
		"Last-Event-ID": fmt.Sprintf("%s-2", bus.RunID()),
	})
	defer cancel()

	// ": connected" plus exactly one replayed frame (the third event).
	body := rec.waitForFlushes(t, 2, time.Second)

	if strings.Contains(body, "evt-0.txt") || strings.Contains(body, "evt-1.txt") {
		t.Errorf("resumed stream replayed events the client already had:\n%s", body)
	}
	if !strings.Contains(body, "evt-2.txt") {
		t.Errorf("resumed stream dropped the event after the resume point:\n%s", body)
	}
}

// TestEventsHandler_ResumeAcceptsQueryParam covers the reconnect path the
// browser does not manage for us. When the stream fails hard the client
// constructs a brand-new EventSource, which has no memory of the last id, so
// it passes the resume point explicitly.
func TestEventsHandler_ResumeAcceptsQueryParam(t *testing.T) {
	bus := newTestBus()
	handler := eventsHandler(bus, nopLogger(), clock.New(), newTestShutdown())

	publishN(bus, 3)

	rec, cancel := doSSERequest(handler, "last_event_id="+bus.RunID()+"-2")
	defer cancel()
	body := rec.waitForFlushes(t, 2, time.Second)

	if strings.Contains(body, "evt-1.txt") {
		t.Errorf("query-param resume point ignored:\n%s", body)
	}
	if !strings.Contains(body, "evt-2.txt") {
		t.Errorf("resumed stream dropped the event after the resume point:\n%s", body)
	}
}

// TestEventsHandler_ResumeFromAnotherRunReplaysEverything is the guard that
// makes a restart safe. Sequence numbers restart with the process, so a
// token minted by an earlier run must be discarded rather than believed —
// honouring it would skip the first events of the new run silently.
func TestEventsHandler_ResumeFromAnotherRunReplaysEverything(t *testing.T) {
	bus := newTestBus()
	handler := eventsHandler(bus, nopLogger(), clock.New(), newTestShutdown())

	publishN(bus, 2)

	rec, cancel := doSSERequestWithHeaders(handler, "", map[string]string{
		"Last-Event-ID": "some-other-run-99",
	})
	defer cancel()
	body := rec.waitForFlushes(t, 3, time.Second)

	for i := 0; i < 2; i++ {
		want := fmt.Sprintf("evt-%d.txt", i)
		if !strings.Contains(body, want) {
			t.Errorf("stale-run token suppressed %s; got:\n%s", want, body)
		}
	}
}

// TestEventsHandler_MalformedResumeReplaysEverything pins the safe default:
// an unparseable token replays rather than skips. Sending history twice is
// a nuisance; skipping it silently is data loss.
func TestEventsHandler_MalformedResumeReplaysEverything(t *testing.T) {
	for _, id := range []string{"", "garbage", "-", "abc-notanumber", "12345"} {
		t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
			bus := newTestBus()
			handler := eventsHandler(bus, nopLogger(), clock.New(), newTestShutdown())

			publishN(bus, 1)

			rec, cancel := doSSERequestWithHeaders(handler, "", map[string]string{"Last-Event-ID": id})
			defer cancel()
			body := rec.waitForFlushes(t, 2, time.Second)

			if !strings.Contains(body, "evt-0.txt") {
				t.Errorf("malformed token %q suppressed history; got:\n%s", id, body)
			}
		})
	}
}

// TestEventsHandler_ResumeIDSurvivesAnEvictedGap covers history eviction.
// The buffer drops the oldest request telemetry first, so the replayed
// snapshot can have holes in its sequence numbers. The resume point after a
// replay must still be the newest event replayed — stalling at the hole
// would make the next reconnect re-send everything after it, which is the
// behaviour this whole change exists to remove.
func TestEventsHandler_ResumeIDSurvivesAnEvictedGap(t *testing.T) {
	bus := newTestBus()
	handler := eventsHandler(bus, nopLogger(), clock.New(), newTestShutdown())

	// seq 1 and 3 are noise, seq 2 and 4 are not. A small buffer would evict
	// 1 and 3 first; here nothing is evicted, but the ids must still track
	// the highest seq replayed rather than the count of frames written.
	bus.Publish(context.Background(), events.Event{Type: events.RequestReceived, Source: "request"})
	bus.Publish(context.Background(), events.Event{Type: events.S3ObjectCreated, Source: "s3", Payload: map[string]string{"key": "evt-a.txt"}})
	bus.Publish(context.Background(), events.Event{Type: events.RequestReceived, Source: "request"})
	bus.Publish(context.Background(), events.Event{Type: events.S3ObjectCreated, Source: "s3", Payload: map[string]string{"key": "evt-b.txt"}})

	// A source filter makes the delivered subsequence sparse: only seq 2 and
	// 4 are written, so the last id must be 4, not 2.
	rec, cancel := doSSERequest(handler, "source=s3")
	defer cancel()
	body := rec.waitForFlushes(t, 3, time.Second)

	want := fmt.Sprintf("id: %s-4", bus.RunID())
	if !strings.Contains(body, want) {
		t.Errorf("expected the resume point to reach %q after a filtered replay; got:\n%s", want, body)
	}
}

// TestEventsHandler_CarriesRequestIDOnTheWire verifies the SSE envelope
// carries the request that caused the event, so the Events page can link a
// row to its trace. The id is stamped by Bus.Publish from the publishing
// context, which is what publishAfter passes through.
func TestEventsHandler_CarriesRequestIDOnTheWire(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()
	rec.waitFlush(t, time.Second)

	const wantID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	time.AfterFunc(20*time.Millisecond, func() {
		bus.Publish(protocol.ContextWithRequestID(context.Background(), wantID), events.Event{
			Type:    events.S3ObjectCreated,
			Source:  "s3",
			Time:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Payload: map[string]string{"key": "myfile.txt"},
		})
	})

	env := findSSEEnvelope(t, rec.waitFlush(t, 2*time.Second), events.S3ObjectCreated)
	if env.RequestID != wantID {
		t.Errorf("RequestID = %q, want %q", env.RequestID, wantID)
	}
}

// TestEventsHandler_OmitsRequestIDWhenAbsent verifies an event with no request
// behind it omits the field entirely rather than sending an empty string — a
// client has to be able to tell "no request caused this" from "a request
// caused this and we could not name it".
func TestEventsHandler_OmitsRequestIDWhenAbsent(t *testing.T) {
	bus := newTestBus()
	shutdownCh := newTestShutdown()
	handler := eventsHandler(bus, nopLogger(), clock.New(), shutdownCh)

	rec, cancel := doSSERequest(handler, "")
	defer cancel()
	rec.waitFlush(t, time.Second)

	publishAfter(bus, events.Event{
		Type:   events.DockerContainerDied,
		Source: "docker",
		Time:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, 20*time.Millisecond)

	body := rec.waitFlush(t, 2*time.Second)
	if env := findSSEEnvelope(t, body, events.DockerContainerDied); env.RequestID != "" {
		t.Errorf("RequestID = %q, want empty", env.RequestID)
	}
	if strings.Contains(body, "requestId") {
		t.Errorf("frame carries a requestId key for an event with no request:\n%s", body)
	}
}

// findSSEEnvelope returns the first data frame in body with the given type,
// failing the test if there is none.
func findSSEEnvelope(t *testing.T, body string, want events.Type) sseEnvelope {
	t.Helper()
	for _, line := range readSSELines(body) {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var env sseEnvelope
		if err := json.Unmarshal([]byte(data), &env); err != nil {
			continue
		}
		if env.Type == string(want) {
			return env
		}
	}
	t.Fatalf("no SSE data frame with type %q; body:\n%s", want, body)
	return sseEnvelope{}
}
