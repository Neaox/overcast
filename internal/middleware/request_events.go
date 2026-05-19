package middleware

import (
	"context"
	"net/http"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// RequestEvents publishes a request:Received event onto the bus for every
// incoming HTTP request. The bus is injected via a pointer-to-pointer so it
// can be set after middleware registration (the bus is created late in
// router.New). If the bus is nil at request time, publishing is skipped.
//
// This middleware intentionally mirrors the Logger middleware's
// responseWriter + detectService/detectOperation pattern — both intercept
// the request lifecycle to capture the same metadata for different purposes
// (logging vs event publishing).
//
// Performance: each event is enqueued on the bus's 4096-capacity buffered
// worker pool. Publish returns immediately in the common case; it only waits
// if all 4096 slots are occupied by in-flight work items and 16 workers
// haven't caught up yet. When no SSE client is connected there are no
// wildcard subscribers, so zero work items are enqueued (zero overhead).
func RequestEvents(busPtr **events.Bus, clk clock.Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			bus := *busPtr
			if bus == nil {
				return
			}

			reqID := protocol.RequestIDFromContext(r.Context())
			duration := clk.Since(start)
			svc := detectService(r)
			op := detectOperation(r)

			ev := events.Event{
				Type:   events.RequestReceived,
				Time:   start,
				Source: "request",
				Payload: events.RequestPayload{
					Method:        r.Method,
					Path:          r.URL.Path,
					Query:         r.URL.RawQuery,
					Status:        rw.status,
					DurationUs:    duration.Microseconds(),
					Service:       svc,
					Operation:     op,
					RequestID:     reqID,
					RemoteAddr:    r.RemoteAddr,
					UserAgent:     r.UserAgent(),
					ContentLength: r.ContentLength,
					XAmzTarget:    r.Header.Get("X-Amz-Target"),
				},
			}
			// Publish hands off to the bus worker pool via a buffered
			// channel (4096 cap); returns immediately unless the channel
			// is full (rare — 16 workers drain it).
			bus.Publish(context.Background(), ev)
		})
	}
}
