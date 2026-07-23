package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/boottime"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// debugEC2Provider is the subset of the EC2 service needed by the debug
// namespace. Defined here to avoid a circular import between router and
// internal/services/ec2.
type debugEC2Provider interface {
	DebugVPCsHandler() http.HandlerFunc
}

type debugDynamoDBProvider interface {
	DebugStateKeys(ctx context.Context) ([]string, error)
	DebugStateValues(ctx context.Context) (map[string]string, error)
	DebugResetState(ctx context.Context) error
}

// debugHandlers registers the /_debug/* endpoint namespace.
// These are only mounted when cfg.Debug == true.
//
// Equivalent to LocalStack's /_localstack/* endpoints — useful for:
//   - Resetting state between test runs without restarting the container
//   - Inspecting what's stored (useful when debugging test failures)
//   - Verifying configuration is what you expect
//
// A web UI for these endpoints is planned. For now they return JSON.
func debugHandlers(cfg *config.Config, store state.Store, ec2 debugEC2Provider, dynamo debugDynamoDBProvider) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/health", debugHealth(cfg, store))
		r.Get("/config", debugConfig(cfg))
		r.Get("/state", debugState(store, dynamo))
		r.Get("/state/{namespace}", debugStateNamespace(store, dynamo))
		r.Post("/reset", debugReset(store, dynamo))
		r.Post("/reset/{service}", debugResetService(store, dynamo))
		r.Get("/metrics", debugMetrics())

		// ---- Service-specific debug endpoints ---------------------------------
		if ec2 != nil {
			r.Get("/ec2/vpcs", ec2.DebugVPCsHandler())
		}

		// pprof endpoints — goroutine, heap, CPU, etc.
		r.HandleFunc("/pprof/", pprof.Index)
		r.HandleFunc("/pprof/cmdline", pprof.Cmdline)
		r.HandleFunc("/pprof/profile", pprof.Profile)
		r.HandleFunc("/pprof/symbol", pprof.Symbol)
		r.HandleFunc("/pprof/trace", pprof.Trace)
		r.Handle("/pprof/goroutine", pprof.Handler("goroutine"))
		r.Handle("/pprof/heap", pprof.Handler("heap"))
		r.Handle("/pprof/allocs", pprof.Handler("allocs"))
		r.Handle("/pprof/block", pprof.Handler("block"))
		r.Handle("/pprof/mutex", pprof.Handler("mutex"))
		r.Handle("/pprof/threadcreate", pprof.Handler("threadcreate"))
	}
}

// ---- Handler implementations -----------------------------------------------

type debugHealthResponse struct {
	Status        string            `json:"status"`
	Timestamp     string            `json:"timestamp"`
	Uptime        string            `json:"uptime"`
	GoVersion     string            `json:"go_version"`
	Services      map[string]bool   `json:"services"`
	State         string            `json:"state"`
	ServiceStates map[string]string `json:"serviceStates,omitempty"`
	Persistent    *persistentHealth `json:"persistent,omitempty"`
	Debug         bool              `json:"debug"`
}

var (
	startTime   = processStartTime()
	goStartTime = boottime.GoStart
)

func debugHealth(cfg *config.Config, store state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svcStates := make(map[string]string, len(cfg.ServiceStates))
		for svc, mode := range cfg.ServiceStates {
			svcStates[svc] = string(mode)
		}
		persistent := persistentHealthSnapshot(store)
		status := "ok"
		if persistent != nil && !persistent.Healthy {
			status = "degraded"
		}
		resp := &debugHealthResponse{
			Status:        status,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			Uptime:        time.Since(startTime).Round(time.Second).String(),
			GoVersion:     runtime.Version(),
			Services:      cfg.Services,
			State:         string(cfg.State),
			ServiceStates: svcStates,
			Persistent:    persistent,
			Debug:         cfg.Debug,
		}
		writeDebugJSON(w, http.StatusOK, resp)
	}
}

type debugConfigResponse struct {
	Host          string            `json:"host"`
	Port          int               `json:"port"`
	Services      map[string]bool   `json:"services"`
	State         string            `json:"state"`
	ServiceStates map[string]string `json:"serviceStates,omitempty"`
	DataDir       string            `json:"data_dir"`
	Region        string            `json:"region"`
	AccountID     string            `json:"account_id"`
	LogLevel      string            `json:"log_level"`
	Debug         bool              `json:"debug"`
	TLS           bool              `json:"tls_enabled"`
	// TLS cert/key paths are deliberately omitted from the response.
}

func debugConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svcStates := make(map[string]string, len(cfg.ServiceStates))
		for svc, mode := range cfg.ServiceStates {
			svcStates[svc] = string(mode)
		}
		resp := &debugConfigResponse{
			Host:          cfg.Host,
			Port:          cfg.Port,
			Services:      cfg.Services,
			State:         string(cfg.State),
			ServiceStates: svcStates,
			DataDir:       cfg.DataDir,
			Region:        cfg.Region,
			AccountID:     cfg.AccountID,
			LogLevel:      cfg.LogLevel,
			Debug:         cfg.Debug,
			TLS:           cfg.TLSEnabled(),
		}
		writeDebugJSON(w, http.StatusOK, resp)
	}
}

const debugDynamoDBItemsNamespace = "dynamodb:items"

func debugState(store state.Store, dynamo debugDynamoDBProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// List all namespaces currently present and their keys. Store backends keep
		// namespace/key scans indexed, but this debug endpoint still enumerates all
		// keys so the UI can build a complete hierarchy.
		namespaces, err := store.ListNamespaces(r.Context())
		if err != nil {
			writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		result := make(map[string][]string)
		for _, ns := range namespaces {
			keys, err := store.List(r.Context(), ns, "")
			if err != nil || len(keys) == 0 {
				continue
			}
			result[ns] = keys
		}
		if dynamo != nil {
			keys, err := dynamo.DebugStateKeys(r.Context())
			if err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if len(keys) > 0 {
				result[debugDynamoDBItemsNamespace] = keys
			}
		}
		writeDebugJSON(w, http.StatusOK, result)
	}
}

func debugStateNamespace(store state.Store, dynamo debugDynamoDBProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns := chi.URLParam(r, "namespace")
		// Replace URL-encoded colons: "s3:buckets" → "s3:buckets"
		ns = strings.ReplaceAll(ns, "%3A", ":")
		if ns == debugDynamoDBItemsNamespace && dynamo != nil {
			result, err := dynamo.DebugStateValues(r.Context())
			if err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeDebugJSON(w, http.StatusOK, result)
			return
		}

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

func debugReset(store state.Store, dynamo debugDynamoDBProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ms, ok := store.(*state.MemoryStore); ok {
			ms.Reset()
		} else {
			// For SQLite, delete all keys across all namespaces.
			resetAllNamespaces(r.Context(), store)
		}
		if dynamo != nil {
			if err := dynamo.DebugResetState(r.Context()); err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		writeDebugJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	}
}

func debugResetService(store state.Store, dynamo debugDynamoDBProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := chi.URLParam(r, "service")
		namespaces, err := namespacesForService(r.Context(), store, service)
		if err != nil {
			writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
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
		if service == "dynamodb" && dynamo != nil {
			if err := dynamo.DebugResetState(r.Context()); err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
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

func namespacesForService(ctx context.Context, store state.Store, service string) ([]string, error) {
	all, err := store.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	prefix := debugServicePrefix(service)
	if prefix == "" {
		return []string{}, nil
	}
	var namespaces []string
	for _, ns := range all {
		if ns == prefix || strings.HasPrefix(ns, prefix+":") {
			namespaces = append(namespaces, ns)
		}
	}
	return namespaces, nil
}

func debugServicePrefix(service string) string {
	switch service {
	case "cloudformation":
		return "cfn"
	case "apigateway":
		return "apigw"
	case "eventbridge":
		return "eb"
	default:
		return service
	}
}

func resetAllNamespaces(ctx context.Context, store state.Store) {
	namespaces, err := store.ListNamespaces(ctx)
	if err != nil {
		return
	}
	for _, ns := range namespaces {
		keys, _ := store.List(ctx, ns, "")
		for _, k := range keys {
			_ = store.Delete(ctx, ns, k)
		}
	}
}
