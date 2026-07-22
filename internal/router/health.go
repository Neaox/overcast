package router

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Neaox/overcast/internal/config"
)

// healthResponse is the JSON body returned by GET /_health.
type healthResponse struct {
	Status           string            `json:"status"`
	Timestamp        string            `json:"timestamp"`
	Version          string            `json:"version"`
	Services         []string          `json:"services"`
	ServiceTiers     map[string]string `json:"serviceTiers"`
	ServiceGoalTiers map[string]string `json:"serviceGoalTiers"`
	Storage          healthStorage     `json:"storage"`
}

// healthStorage describes the active storage configuration.
type healthStorage struct {
	Default          string            `json:"default"`
	ServiceOverrides map[string]string `json:"serviceOverrides,omitempty"`
}

// newHealthHandler returns a handler for GET /_health.
// Used by Docker HEALTHCHECK, load balancers, and readiness probes.
// Returns 200 OK when the server is ready to accept requests.
// enabledServices is the list of service names that are currently enabled.
// enabledTiers maps each enabled service name to its emulation tier.
// enabledGoalTiers maps each enabled service name to its goal emulation tier.
func newHealthHandler(cfg *config.Config, enabledServices []string, enabledTiers map[string]string, enabledGoalTiers map[string]string) http.HandlerFunc {
	// Build the storage section once — it's static for the process lifetime.
	storage := healthStorage{Default: string(cfg.State)}
	if len(cfg.ServiceStates) > 0 {
		storage.ServiceOverrides = make(map[string]string, len(cfg.ServiceStates))
		for svc, mode := range cfg.ServiceStates {
			storage.ServiceOverrides[svc] = string(mode)
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		resp := &healthResponse{
			Status:           "ok",
			Timestamp:        time.Now().UTC().Format(time.RFC3339),
			Version:          cfg.Version,
			Services:         enabledServices,
			ServiceTiers:     enabledTiers,
			ServiceGoalTiers: enabledGoalTiers,
			Storage:          storage,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}

// infoResponse is the JSON body returned by GET /_/info.
// Always available (not debug-gated). Used by the web UI to discover the
// server's configured region so it can pre-select the correct region on first
// load, even when the user has never explicitly chosen one.
type infoResponse struct {
	Region    string `json:"region"`
	AccountID string `json:"account_id"`
	Version   string `json:"version"`
	Debug     bool   `json:"debug"`
}

// newInfoHandler returns a handler for GET /_/info.
func newInfoHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(&infoResponse{
			Region:    cfg.Region,
			AccountID: cfg.AccountID,
			Version:   cfg.Version,
			Debug:     cfg.Debug,
		})
	}
}
