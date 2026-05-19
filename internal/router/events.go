package router

// events.go — Server-Sent Events endpoint for the internal event bus.
//
// GET /_events
//
// Streams every event published onto the bus as newline-delimited SSE.
// Query parameters:
//   source=s3          filter to a single source (may be repeated)
//
// The stream stays open until the client disconnects or the server shuts down.
// Each event is sent as:
//
//	data: {"type":"s3:ObjectCreated:*","time":"...","source":"s3","payload":{...}}\n\n
//
// An initial ": connected\n\n" comment is flushed immediately so the client
// can distinguish "connected but no events yet" from "not connected at all".

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

		cancel := bus.SubscribeAll(func(_ context.Context, e events.Event) {
			select {
			case ch <- e:
			default:
				// client too slow — drop
			}
		})
		defer cancel()

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
				// Apply source filter.
				if len(sourceSet) > 0 {
					if _, ok := sourceSet[strings.ToLower(e.Source)]; !ok {
						continue
					}
				}

				payload, err := json.Marshal(e.Payload)
				if err != nil {
					logger.Error("events: marshal payload", zap.Error(err))
					continue
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
					continue
				}

				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()

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
