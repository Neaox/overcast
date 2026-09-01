package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/protocol"
)

// collectEvents subscribes to every event on bus and returns a func that
// snapshots what has been published so far.
func collectEvents(t *testing.T, bus *events.Bus) func() []events.Event {
	t.Helper()
	// The bus dispatches through a worker pool, so a live subscriber's
	// deliveries are asynchronous. The history buffer is written under the
	// publish lock instead, which makes it the synchronous view — by the time
	// ServeHTTP returns, everything the request published is in it.
	return func() []events.Event {
		snapshot, cancel := bus.SnapshotAndSubscribeAll(func(context.Context, events.Event) {})
		cancel()
		return snapshot
	}
}

// TestRequestEvents_StampsRequestIDOnTheEnvelope pins the one publish site
// that cannot rely on Bus.Publish's context guard: this middleware publishes
// on context.Background(), so it has to carry the id on the event itself.
func TestRequestEvents_StampsRequestIDOnTheEnvelope(t *testing.T) {
	bus := events.NewBus()
	defer bus.Stop()
	snapshot := collectEvents(t, bus)

	h := RequestID(RequestEvents(&bus, clock.New())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })))

	const wantID = "caller-supplied-request-id"
	req := httptest.NewRequest(http.MethodGet, "/s3/bucket", nil)
	req.Header.Set("x-amzn-requestid", wantID)
	h.ServeHTTP(httptest.NewRecorder(), req)

	got := snapshot()
	if len(got) != 1 {
		t.Fatalf("published %d events, want 1", len(got))
	}
	if got[0].RequestID != wantID {
		t.Errorf("envelope RequestID = %q, want %q", got[0].RequestID, wantID)
	}
	// The payload keeps its own copy — it is what the request:Received detail
	// view renders — and the two must agree.
	p, ok := got[0].Payload.(events.RequestPayload)
	if !ok {
		t.Fatalf("payload type = %T, want events.RequestPayload", got[0].Payload)
	}
	if p.RequestID != wantID {
		t.Errorf("payload RequestID = %q, want %q", p.RequestID, wantID)
	}
}

// TestRequestEvents_HandlerPublishesInheritTheRequestID is the behaviour the
// Events page's trace link depends on end to end: an event a handler publishes
// on the request context is attributed to that request without the handler
// naming the id, so it lands beside the request:Received row under the same
// trace.
func TestRequestEvents_HandlerPublishesInheritTheRequestID(t *testing.T) {
	bus := events.NewBus()
	defer bus.Stop()
	snapshot := collectEvents(t, bus)

	h := RequestID(RequestEvents(&bus, clock.New())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A handler publishing exactly as the service packages do: the
			// request context, and no mention of a request ID.
			bus.Publish(r.Context(), events.Event{
				Type:    events.S3ObjectCreated,
				Source:  "s3",
				Payload: events.S3ObjectPayload{Bucket: "b", Key: "k"},
			})
			w.WriteHeader(http.StatusOK)
		})))

	const wantID = "end-to-end-request-id"
	req := httptest.NewRequest(http.MethodPut, "/b/k", nil)
	req.Header.Set("x-amzn-requestid", wantID)
	h.ServeHTTP(httptest.NewRecorder(), req)

	got := snapshot()
	if len(got) != 2 {
		t.Fatalf("published %d events, want 2 (the object write and the request)", len(got))
	}
	for _, e := range got {
		if e.RequestID != wantID {
			t.Errorf("%s RequestID = %q, want %q", e.Type, e.RequestID, wantID)
		}
	}
	// And the lookup the trace detail page uses finds both.
	if found := bus.FindEventsByRequestID(wantID); len(found) != 2 {
		t.Errorf("FindEventsByRequestID returned %d events, want 2", len(found))
	}
}

// TestRequestEvents_NoBusIsANoOp guards the nil-bus path the middleware
// documents — the bus is wired in after middleware registration.
func TestRequestEvents_NoBusIsANoOp(t *testing.T) {
	var bus *events.Bus
	h := RequestID(RequestEvents(&bus, clock.New())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := protocol.LookupRequestID(r.Context()); !ok {
				t.Error("handler ran without a request ID on its context")
			}
			w.WriteHeader(http.StatusOK)
		})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
