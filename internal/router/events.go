package router

// events.go — Server-Sent Events endpoint for the internal event bus.
//
// GET /_events
//
// On connect, replays the bus's rolling History buffer (up to
// events.HistoryCapacity recent events, oldest first) and then streams every
// newly published event live as newline-delimited SSE. Query parameters:
//   source=s3          filter to a single source (may be repeated)
//
// The stream stays open until the client disconnects or the server shuts down.
// Each event — replayed or live — is sent in the same shape:
//
//	data: {"type":"s3:ObjectCreated:*","time":"...","source":"s3","payload":{...}}\n\n
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
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
)

// sseEnvelope is the JSON shape streamed to each SSE client.
type sseEnvelope struct {
	Type    string          `json:"type"`
	Time    string          `json:"time"`
	Source  string          `json:"source"`
	Payload json.RawMessage `json:"payload"`
}

// eventsHandler returns an http.HandlerFunc that fans out all bus events as SSE.
func eventsHandler(bus *events.Bus, logger *zap.Logger, shutdownCh <-chan struct{}) http.HandlerFunc {
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
		for _, e := range snapshot {
			writeSSEEvent(w, flusher, e, sourceSet, logger)
		}

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
				writeSSEEvent(w, flusher, e, sourceSet, logger)

			case <-heartbeat.C:
				// Lightweight heartbeat — lets clients detect a stale
				// connection even when no real events are flowing.
				logger.Debug("events: heartbeat sent")
				env := sseEnvelope{
					Type:   "heartbeat",
					Time:   time.Now().UTC().Format(time.RFC3339Nano),
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
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, e events.Event, sourceSet map[string]struct{}, logger *zap.Logger) {
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
		Type:    string(e.Type),
		Time:    e.Time.UTC().Format(time.RFC3339Nano),
		Source:  e.Source,
		Payload: payload,
	}

	data, err := json.Marshal(env)
	if err != nil {
		logger.Error("events: marshal envelope", zap.Error(err))
		return
	}

	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}
