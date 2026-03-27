package router

import (
	"encoding/json"
	"net/http"
	"time"
)

// healthResponse is the JSON body returned by GET /_health.
type healthResponse struct {
	Status    string   `json:"status"`
	Timestamp string   `json:"timestamp"`
	Version   string   `json:"version"`
	Services  []string `json:"services"`
}

// newHealthHandler returns a handler for GET /_health.
// Used by Docker HEALTHCHECK, load balancers, and readiness probes.
// Returns 200 OK when the server is ready to accept requests.
// enabledServices is the list of service names that are currently enabled.
func newHealthHandler(enabledServices []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := &healthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Version:   "dev",
			Services:  enabledServices,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}
