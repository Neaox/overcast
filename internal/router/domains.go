package router

// domains.go — Server-Sent Events endpoint for the custom-domain registry.
//
// GET /_internal/domains/watch
//
// Streams the current set of registered custom domain names and all subsequent
// changes as newline-delimited SSE. Consumed by the `overcast dev` host CLI
// to drive mDNS publishing and local trust-store management.
//
// Each event is sent as:
//
//	data: {"type":"added","name":"api.myapp.local","source":"apigateway.v1"}\n\n
//
// On connect, every currently-active record is replayed as a separate
// "added" event before any live updates arrive. The stream stays open until
// the client disconnects or the server shuts down.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/domainregistry"
)

// domainEnvelope is the JSON shape streamed to each SSE client.
type domainEnvelope struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

func domainsWatchHandler(reg *domainregistry.Registry, logger *zap.Logger, shutdownCh <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")

		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		ch := reg.Watch(r.Context())

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		ctx := r.Context()
		for {
			select {
			case <-shutdownCh:
				return
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				env := domainEnvelope{
					Type:   ev.Type.String(),
					Name:   ev.Record.Name,
					Source: ev.Record.Source,
				}
				data, err := json.Marshal(env)
				if err != nil {
					logger.Error("domains: marshal envelope", zap.Error(err))
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			case <-heartbeat.C:
				fmt.Fprint(w, ": heartbeat\n\n")
				flusher.Flush()
			}
		}
	}
}
