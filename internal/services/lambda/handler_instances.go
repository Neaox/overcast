package lambda

// handler_instances.go — GET /_lambda/instances
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

// ListInstances handles GET /_lambda/instances.
// Returns all currently tracked instances (running + idle) across all functions.
func (h *Handler) ListInstances(w http.ResponseWriter, r *http.Request) {
	instances := h.tracker.Instances()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"instances": instances,
	})
}
