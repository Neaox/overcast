package router

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/state"
)

// healthResponse is the JSON body returned by GET /_overcast/health.
type healthResponse struct {
	Status    string   `json:"status"`
	Timestamp string   `json:"timestamp"`
	Version   string   `json:"version"`
	Services  []string `json:"services"`
	// ServiceTiers and ServiceGoalTiers are keyed by service name. The value
	// type is the EmulationTier alias rather than bare string so cmd/tsgen
	// renders the web UI's type as the tier union, not `string`.
	ServiceTiers     map[string]EmulationTier `json:"serviceTiers"`
	ServiceGoalTiers map[string]EmulationTier `json:"serviceGoalTiers"`
	Storage          healthStorage            `json:"storage"`
	Docker           *docker.Status           `json:"docker,omitempty"`
}

// healthStorage describes the active storage configuration.
type healthStorage struct {
	Default string `json:"default"`
	// Configured is the raw OVERCAST_STATE value as configured — "auto" when
	// unset or explicitly "auto" (see config.StateSource), otherwise the
	// same value as Default. Distinguishing the two lets a caller tell "the
	// backend in effect" (Default, always concrete) apart from "what was
	// actually configured" (this field) — e.g. Default: "memory",
	// Configured: "auto" means the auto-resolver picked memory because it
	// found no evidence of persistence intent, not that anyone set
	// OVERCAST_STATE=memory. Omitted (not just empty) for callers/tests that
	// build a Config directly rather than via config.Load(), where this
	// field is never populated.
	Configured       string            `json:"configured,omitempty"`
	ServiceOverrides map[string]string `json:"serviceOverrides,omitempty"`
	Persistent       *persistentHealth `json:"persistent,omitempty"`
}

// persistentHealth is the health endpoint's view of state.PersistentHealth:
// the same fields, with the timestamps already formatted as RFC 3339 strings
// and omitted when zero (a zero time.Time would otherwise marshal as
// "0001-01-01T00:00:00Z" — omitempty does not apply to structs).
type persistentHealth struct {
	Mode          string `json:"mode"`
	Healthy       bool   `json:"healthy"`
	PendingWrites int    `json:"pendingWrites"`
	LastError     string `json:"lastError,omitempty"`
	LastErrorAt   string `json:"lastErrorAt,omitempty"`
	LastSuccessAt string `json:"lastSuccessAt,omitempty"`
}

// newHealthHandler returns a handler for GET /_overcast/health.
// Used by Docker HEALTHCHECK, load balancers, and readiness probes.
// Returns 200 OK when the server is ready to accept requests.
// enabledServices is the list of service names that are currently enabled.
// enabledTiers maps each enabled service name to its emulation tier.
// enabledGoalTiers maps each enabled service name to its goal emulation tier.
// dockerStatus, when non-nil, supplies the per-service Docker connectivity snapshot.
func newHealthHandler(cfg *config.Config, store state.Store, enabledServices []string, enabledTiers map[string]string, enabledGoalTiers map[string]string, dockerStatus func() *docker.Status) http.HandlerFunc {
	// Build the storage section once — it's static for the process lifetime.
	storage := healthStorage{Default: string(cfg.State), Configured: cfg.StateConfigured}
	if len(cfg.ServiceStates) > 0 {
		storage.ServiceOverrides = make(map[string]string, len(cfg.ServiceStates))
		for svc, mode := range cfg.ServiceStates {
			storage.ServiceOverrides[svc] = string(mode)
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		currentStorage := storage
		currentStorage.Persistent = persistentHealthSnapshot(store)
		status := "ok"
		if currentStorage.Persistent != nil && !currentStorage.Persistent.Healthy {
			status = "degraded"
		}
		resp := &healthResponse{
			Status:           status,
			Timestamp:        time.Now().UTC().Format(time.RFC3339),
			Version:          cfg.Version,
			Services:         enabledServices,
			ServiceTiers:     enabledTiers,
			ServiceGoalTiers: enabledGoalTiers,
			Storage:          currentStorage,
		}
		if dockerStatus != nil {
			resp.Docker = dockerStatus()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}

// infoResponse is the JSON body returned by GET /_overcast/info.
// Always available (not debug-gated). Used by the web UI to discover the
// server's configured region so it can pre-select the correct region on first
// load, even when the user has never explicitly chosen one.
type infoResponse struct {
	Region    string `json:"region"`
	AccountID string `json:"account_id"`
	Version   string `json:"version"`
	Debug     bool   `json:"debug"`
	// IAMEnforce reports whether opt-in request-time IAM enforcement is on
	// (OVERCAST_ENFORCE_IAM). The web UI shows it beside simulation results so
	// an AccessDenied can be told apart from an application bug.
	IAMEnforce bool `json:"iam_enforce"`
}

// newInfoHandler returns a handler for GET /_overcast/info.
func newInfoHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(&infoResponse{
			Region:     cfg.Region,
			AccountID:  cfg.AccountID,
			Version:    cfg.Version,
			Debug:      cfg.Debug,
			IAMEnforce: cfg.EnforceIAM,
		})
	}
}

func persistentHealthSnapshot(store state.Store) *persistentHealth {
	health, ok := state.PersistentHealthSnapshot(store)
	if !ok {
		return nil
	}
	snapshot := &persistentHealth{
		Mode:          health.Mode,
		Healthy:       health.Healthy,
		PendingWrites: health.PendingWrites,
		LastError:     health.LastError,
	}
	if !health.LastErrorAt.IsZero() {
		snapshot.LastErrorAt = health.LastErrorAt.UTC().Format(time.RFC3339)
	}
	if !health.LastSuccessAt.IsZero() {
		snapshot.LastSuccessAt = health.LastSuccessAt.UTC().Format(time.RFC3339)
	}
	return snapshot
}
