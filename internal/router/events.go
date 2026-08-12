package router

// events.go — Server-Sent Events endpoint for the internal event bus.
//
// GET /_overcast/events
//
// On connect, replays the bus's rolling History buffer (up to
// events.HistoryCapacity recent events, oldest first) and then streams every
// newly published event live as newline-delimited SSE. Query parameters:
//   source=s3          filter to a single source (may be repeated)
//   last_event_id=…    resume point; see "Resuming" below
//
// The stream stays open until the client disconnects or the server shuts down.
// Each event — replayed or live — is sent in the same shape:
//
//	id: 4f3a1c-128
//	data: {"type":"s3:ObjectCreated:*","time":"...","source":"s3","resourceArn":"arn:aws:s3:::my-bucket","payload":{...}}\n\n
//
// # Resuming
//
// Without an id on each frame, every reconnect replays the whole history
// buffer, and a client that kept what it already had ends up holding several
// copies of it — a laptop waking from sleep was enough to do it. The id is
// "<bus run id>-<resume point>", and a reconnecting client sends the last one
// back: browsers do it automatically via the Last-Event-ID request header,
// while a client that had to construct a fresh EventSource (which starts with
// no memory of the id) passes it as ?last_event_id= instead. The header wins
// when both are present.
//
// Two properties matter more than compactness here:
//
//   - A token from another run is ignored, and so is anything unparseable.
//     Sequence numbers restart with the process, so believing a stale token
//     would silently skip the beginning of the new run. Replaying more than
//     necessary is a nuisance; skipping is data loss.
//   - The resume point is a *contiguous* low-water mark, not the last seq
//     written. The bus dispatches through a worker pool, so events reach one
//     subscriber out of order; treating the newest id as "I have everything
//     up to here" would strand the ones still in flight behind it. Tracking
//     the highest N for which every seq ≤ N has been written means a resume
//     can duplicate a few events that were in flight at the drop, but can
//     never lose one.
//
// resourceArn is omitted whenever the event has no known/cheap-to-construct
// ARN for its primary resource (see events.Event.ResourceARN).
//
// An initial ": connected\n\n" comment is flushed immediately so the client
// can distinguish "connected but no events yet" from "not connected at all".
//
// Replay + live-subscribe happen via Bus.SnapshotAndSubscribeAll, which
// takes the history snapshot and registers the subscription atomically
// under the bus lock — so an event published concurrently with a client
// connecting is delivered exactly once (either in the replay or live,
// never both, never neither).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/logging"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/go-chi/chi/v5"
)

// sseEnvelope is the JSON shape streamed to each SSE client.
type sseEnvelope struct {
	Type   string `json:"type"`
	Time   string `json:"time"`
	Source string `json:"source"`
	// ResourceARN is the ARN of the event's primary resource, when known —
	// see events.Event.ResourceARN. Omitted when empty so older/simpler
	// consumers see no shape change.
	ResourceARN string          `json:"resourceArn,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// eventsHandler returns an http.HandlerFunc that fans out all bus events as SSE.
func eventsHandler(bus *events.Bus, logger *zap.Logger, clk clock.Clock, shutdownCh <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		// Parse optional source filters — multiple values allowed: ?source=s3&source=sqs
		sources := r.URL.Query()["source"]
		sourceSet := make(map[string]struct{}, len(sources))
		for _, s := range sources {
			sourceSet[strings.ToLower(s)] = struct{}{}
		}

		// SSE response headers.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		// Disable buffering in reverse proxies (nginx, Caddy, etc.)
		w.Header().Set("X-Accel-Buffering", "no")

		// Send a comment immediately so the client knows it's live.
		fmt.Fprintf(w, ": connected\n\n")
		flusher.Flush()

		// Where this client got to on a previous connection, if it said.
		resumeFrom := parseResumePoint(r, bus.RunID())

		// Buffer up to 64 events; drop if the client is too slow rather than
		// slowing down the emulator's own goroutines.
		ch := make(chan events.Event, 64)

		// Snapshot the history buffer and subscribe atomically — see the
		// package doc comment above for why this must be one bus call.
		snapshot, cancel := bus.SnapshotAndSubscribeAll(func(_ context.Context, e events.Event) {
			select {
			case ch <- e:
			default:
				// client too slow — drop
			}
		})
		defer cancel()

		// Replay buffered history before going live. Ordering/dedup at this
		// boundary is guaranteed by SnapshotAndSubscribeAll: nothing in
		// snapshot can also arrive on ch, and vice versa.
		progress := &resumeTracker{runID: bus.RunID()}
		for _, e := range snapshot {
			if e.Seq <= resumeFrom {
				continue
			}
			writeSSEEvent(w, flusher, e, sourceSet, logger, progress)
		}
		// Everything up to the newest event in the snapshot has now been
		// accounted for, whether it was written, filtered out by ?source=,
		// or already had by the client. Seeding the low-water mark with it
		// rather than letting it climb frame by frame is what stops an
		// eviction hole — history drops the oldest noise first, so the
		// snapshot's sequence numbers are not contiguous — from pinning the
		// resume point at the hole for the rest of the connection.
		progress.reach(max(resumeFrom, highestSeq(snapshot)))

		// Heartbeat ticker — sends an SSE comment every 15 s so clients can
		// detect a stale connection even when no real events are flowing.
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		ctx := r.Context()
		for {
			select {
			case <-shutdownCh:
				return
			case e := <-ch:
				writeSSEEvent(w, flusher, e, sourceSet, logger, progress)

			case <-heartbeat.C:
				// Lightweight heartbeat — lets clients detect a stale
				// connection even when no real events are flowing.
				// Trace, not debug: fires on a timer even on an idle
				// server (the log-level policy's litmus test).
				logger.Log(logging.TraceLevel, "events: heartbeat sent")
				env := sseEnvelope{
					Type:   "heartbeat",
					Time:   clk.Now().UTC().Format(time.RFC3339Nano),
					Source: "system",
				}
				data, _ := json.Marshal(env)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()

			case <-ctx.Done():
				return
			}
		}
	}
}

// writeSSEEvent marshals e as an SSE data frame and writes it to w, applying
// the source filter first. Shared by both the history-replay loop and the
// live-tail loop so the two paths can never drift in wire format.
//
// progress is advanced for every event that reaches this function, including
// ones the source filter drops: a filtered event will never be sent to this
// client on any future connection either, so leaving it unaccounted-for would
// pin a filtered client's resume point at the first event it did not want.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, e events.Event, sourceSet map[string]struct{}, logger *zap.Logger, progress *resumeTracker) {
	progress.saw(e.Seq)

	if len(sourceSet) > 0 {
		if _, ok := sourceSet[strings.ToLower(e.Source)]; !ok {
			return
		}
	}

	payload, err := json.Marshal(e.Payload)
	if err != nil {
		logger.Error("events: marshal payload", zap.Error(err))
		return
	}

	env := sseEnvelope{
		Type:        string(e.Type),
		Time:        e.Time.UTC().Format(time.RFC3339Nano),
		Source:      e.Source,
		ResourceARN: e.ResourceARN,
		Payload:     payload,
	}

	data, err := json.Marshal(env)
	if err != nil {
		logger.Error("events: marshal envelope", zap.Error(err))
		return
	}

	fmt.Fprintf(w, "id: %s\ndata: %s\n\n", progress.id(), data)
	flusher.Flush()
}

// highestSeq returns the largest sequence number in events, or 0 if empty.
func highestSeq(events []events.Event) uint64 {
	var high uint64
	for _, e := range events {
		if e.Seq > high {
			high = e.Seq
		}
	}
	return high
}

// parseResumePoint reads the resume point a reconnecting client sent, or 0
// to mean "send everything you have".
//
// The Last-Event-ID header is what a browser's EventSource sends by itself;
// the query parameter is for a client that had to build a fresh EventSource
// and therefore lost the id the browser was tracking. The header wins.
//
// Anything that does not parse, or that names a different run, yields 0. See
// the package comment for why that direction is the safe one.
func parseResumePoint(r *http.Request, runID string) uint64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}

	sep := strings.LastIndex(raw, "-")
	if sep < 0 || raw[:sep] != runID {
		return 0
	}
	seq, err := strconv.ParseUint(raw[sep+1:], 10, 64)
	if err != nil {
		return 0
	}
	return seq
}

// maxPendingAhead bounds the out-of-order bookkeeping below. Gaps come from
// the worker pool reordering deliveries and from events this handler drops
// when the client cannot keep up, both of which are bounded by the 64-deep
// channel — so exceeding this by three orders of magnitude means a client
// that has been losing events steadily, not a transient reorder.
const maxPendingAhead = 4096

// resumeTracker follows how far a single client can safely resume from: the
// highest sequence number N for which every event up to N has been dealt
// with, either written to this client or knowably never destined for it.
//
// It is not the last id written. The bus dispatches through a worker pool,
// so seq 7 can be written before seq 6; calling that "resumed from 7" would
// strand 6 permanently. Holding the mark at 5 until 6 arrives means a
// reconnect may re-send 7, which the client can live with — the alternative
// cannot be undone.
//
// Only the handler goroutine touches this, so it needs no lock.
type resumeTracker struct {
	runID string
	point uint64
	ahead map[uint64]struct{}
}

// saw records that seq has been dealt with.
func (t *resumeTracker) saw(seq uint64) {
	if seq == 0 || seq <= t.point {
		return
	}
	if seq != t.point+1 {
		if t.ahead == nil {
			t.ahead = make(map[uint64]struct{})
		}
		t.ahead[seq] = struct{}{}
		if len(t.ahead) > maxPendingAhead {
			// A gap that never closes — the client is being dropped faster
			// than it drains. Give up on precision rather than grow without
			// bound: jump the mark past everything seen, which costs the
			// missing events their place in a future resume. They were
			// already dropped from this connection by the same policy.
			t.reach(highestKey(t.ahead))
		}
		return
	}
	t.advanceTo(seq)
}

// reach moves the mark to at least n, discarding bookkeeping below it.
func (t *resumeTracker) reach(n uint64) {
	if n <= t.point {
		return
	}
	for k := range t.ahead {
		if k <= n {
			delete(t.ahead, k)
		}
	}
	t.advanceTo(n)
}

// advanceTo sets the mark to n and then consumes any contiguous run above it.
func (t *resumeTracker) advanceTo(n uint64) {
	t.point = n
	for {
		if _, ok := t.ahead[t.point+1]; !ok {
			return
		}
		delete(t.ahead, t.point+1)
		t.point++
	}
}

// id renders the current resume point as an SSE id.
func (t *resumeTracker) id() string {
	return t.runID + "-" + strconv.FormatUint(t.point, 10)
}

func highestKey(m map[uint64]struct{}) uint64 {
	var high uint64
	for k := range m {
		if k > high {
			high = k
		}
	}
	return high
}

// eventsByRequestHandler returns all buffered events matching a request ID.
func eventsByRequestHandler(bus *events.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := chi.URLParam(r, "requestId")
		if requestID == "" {
			http.Error(w, "missing requestId", http.StatusBadRequest)
			return
		}
		evs := bus.FindEventsByRequestID(requestID)
		if evs == nil {
			evs = []events.Event{}
		}
		sseEvents := make([]sseEnvelope, len(evs))
		for i, e := range evs {
			payload, _ := json.Marshal(e.Payload)
			sseEvents[i] = sseEnvelope{
				Type:        string(e.Type),
				Time:        e.Time.Format(time.RFC3339Nano),
				Source:      e.Source,
				ResourceARN: e.ResourceARN,
				Payload:     payload,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sseEvents)
	}
}
