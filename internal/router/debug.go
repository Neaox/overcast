package router

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/your-org/overcast/internal/config"
	"github.com/your-org/overcast/internal/state"
)

// debugHandlers registers the /_debug/* endpoint namespace.
// These are only mounted when cfg.Debug == true.
//
// Equivalent to LocalStack's /_localstack/* endpoints — useful for:
//   - Resetting state between test runs without restarting the container
//   - Inspecting what's stored (useful when debugging test failures)
//   - Verifying configuration is what you expect
//
// A web UI for these endpoints is planned. For now they return JSON.
func debugHandlers(cfg *config.Config, store state.Store) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/health", debugHealth(cfg))
		r.Get("/config", debugConfig(cfg))
		r.Get("/state", debugState(store))
		r.Get("/state/{namespace}", debugStateNamespace(store))
		r.Post("/reset", debugReset(store))
		r.Post("/reset/{service}", debugResetService(store))
		r.Get("/metrics", debugMetrics())
	}
}

// ---- Handler implementations -----------------------------------------------

type debugHealthResponse struct {
	Status    string          `json:"status"`
	Timestamp string          `json:"timestamp"`
	Uptime    string          `json:"uptime"`
	GoVersion string          `json:"go_version"`
	Services  map[string]bool `json:"services"`
	State     string          `json:"state"`
	Debug     bool            `json:"debug"`
}

var startTime = time.Now()

func debugHealth(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := &debugHealthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Uptime:    time.Since(startTime).Round(time.Second).String(),
			GoVersion: runtime.Version(),
			Services:  cfg.Services,
			State:     string(cfg.State),
			Debug:     cfg.Debug,
		}
		writeDebugJSON(w, http.StatusOK, resp)
	}
}

type debugConfigResponse struct {
	Host      string          `json:"host"`
	Port      int             `json:"port"`
	Services  map[string]bool `json:"services"`
	State     string          `json:"state"`
	DataDir   string          `json:"data_dir"`
	Region    string          `json:"region"`
	AccountID string          `json:"account_id"`
	LogLevel  string          `json:"log_level"`
	Debug     bool            `json:"debug"`
	TLS       bool            `json:"tls_enabled"`
	// TLS cert/key paths are deliberately omitted from the response.
}

func debugConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := &debugConfigResponse{
			Host:      cfg.Host,
			Port:      cfg.Port,
			Services:  cfg.Services,
			State:     string(cfg.State),
			DataDir:   cfg.DataDir,
			Region:    cfg.Region,
			AccountID: cfg.AccountID,
			LogLevel:  cfg.LogLevel,
			Debug:     cfg.Debug,
			TLS:       cfg.TLSEnabled(),
		}
		writeDebugJSON(w, http.StatusOK, resp)
	}
}

func debugState(store state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// List all known namespaces and their keys.
		// CAVEAT: This does a full scan — expensive on large datasets.
		namespaces := []string{
			"s3:buckets", "s3:objects",
			"sqs:queues", "sqs:messages",
			"sns:topics", "sns:subscriptions",
			"dynamodb:tables", "dynamodb:items",
			"lambda:functions",
		}

		result := make(map[string][]string)
		for _, ns := range namespaces {
			keys, err := store.List(r.Context(), ns, "")
			if err != nil || len(keys) == 0 {
				continue
			}
			result[ns] = keys
		}
		writeDebugJSON(w, http.StatusOK, result)
	}
}

func debugStateNamespace(store state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns := chi.URLParam(r, "namespace")
		// Replace URL-encoded colons: "s3:buckets" → "s3:buckets"
		ns = strings.ReplaceAll(ns, "%3A", ":")

		keys, err := store.List(r.Context(), ns, "")
		if err != nil {
			writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		// Fetch values too — useful for inspecting stored objects.
		result := make(map[string]string, len(keys))
		for _, k := range keys {
			v, _, _ := store.Get(r.Context(), ns, k)
			result[k] = v
		}
		writeDebugJSON(w, http.StatusOK, result)
	}
}

func debugReset(store state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ms, ok := store.(*state.MemoryStore); ok {
			ms.Reset()
		} else {
			// For SQLite, delete all keys across all namespaces.
			resetAllNamespaces(r.Context(), store)
		}
		writeDebugJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	}
}

func debugResetService(store state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := chi.URLParam(r, "service")
		namespaces := namespacesForService(service)
		if len(namespaces) == 0 {
			writeDebugJSON(w, http.StatusBadRequest, map[string]string{
				"error": "unknown service: " + service,
			})
			return
		}

		for _, ns := range namespaces {
			keys, _ := store.List(r.Context(), ns, "")
			for _, k := range keys {
				_ = store.Delete(r.Context(), ns, k)
			}
		}
		writeDebugJSON(w, http.StatusOK, map[string]string{
			"status":  "reset",
			"service": service,
		})
	}
}

func debugMetrics() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: implement request counters and latency histograms.
		// Wire into the Logger middleware to collect per-operation metrics.
		writeDebugJSON(w, http.StatusOK, map[string]string{
			"status": "metrics not yet implemented",
		})
	}
}

// ---- Helpers ---------------------------------------------------------------

func writeDebugJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func namespacesForService(service string) []string {
	m := map[string][]string{
		"s3":       {"s3:buckets", "s3:objects", "s3:meta"},
		"sqs":      {"sqs:queues", "sqs:messages"},
		"sns":      {"sns:topics", "sns:subscriptions"},
		"dynamodb": {"dynamodb:tables", "dynamodb:items"},
		"lambda":   {"lambda:functions"},
	}
	return m[service]
}

func resetAllNamespaces(ctx context.Context, store state.Store) {
	all := []string{
		"s3:buckets", "s3:objects", "s3:meta",
		"sqs:queues", "sqs:messages",
		"sns:topics", "sns:subscriptions",
		"dynamodb:tables", "dynamodb:items",
		"lambda:functions",
	}
	for _, ns := range all {
		keys, _ := store.List(ctx, ns, "")
		for _, k := range keys {
			_ = store.Delete(ctx, ns, k)
		}
	}
}
