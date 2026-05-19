package router

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
