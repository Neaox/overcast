package lambda

// handler_instances.go — GET /_overcast/lambda/instances
//
// Returns a snapshot of all currently tracked Lambda execution instances so
// that the topology map UI can show sub-nodes for running and idle instances
// without polling a separate endpoint.
//
// This endpoint is emulator-specific (not part of the AWS Lambda API).

import (
	"encoding/json"
	"net/http"
)

// ListInstances handles GET /_overcast/lambda/instances.
// Returns all currently tracked instances (running + idle) across all
// functions, plus a snapshot of the cold-start artifact cache.
func (h *Handler) ListInstances(w http.ResponseWriter, r *http.Request) {
	instances := h.tracker.Instances()
	body := map[string]any{
		"instances": instances,
	}
	if h.tarCacheStats != nil {
		entries, bytes, maxBytes := h.tarCacheStats()
		body["tarCache"] = map[string]any{
			"entries":  entries,
			"bytes":    bytes,
			"maxBytes": maxBytes,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}
