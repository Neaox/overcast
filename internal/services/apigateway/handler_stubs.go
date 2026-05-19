package apigateway

// handler_stubs.go — unimplemented API Gateway operations.
//
// Every method here returns 501 Not Implemented. When implementing an
// operation, move its body to handler.go (or the appropriate handler_*.go)
// and delete the stub from this file.

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ---- REST API v1: Account -------------------------------------------------

// GetAccount — GET /account
// TODO(priority:P3): implement GetAccount — return throttle limits and feature flags.
func (h *Handler) GetAccount(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}

// UpdateAccount — PATCH /account
// TODO(priority:P3): implement UpdateAccount — update throttle limits.
func (h *Handler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedJSON(w, r)
}
