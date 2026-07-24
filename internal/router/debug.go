package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"runtime"
	"sort"
	"strconv"
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

// DebugStateProvider is implemented by services with data outside the
// generic kv store (a dedicated SQL table, e.g. DynamoDB items or CloudWatch
// Logs events) that still needs to appear as a virtual namespace in
// /_debug/state and be clearable via /_debug/reset. See storage-plan.md
// item 2.3 — the raw state debugger only enumerates the generic kv store by
// default, so a dedicated table is invisible (and immune to /_debug/reset)
// without one of these.
//
// The router holds a slice of these (one per opted-in service) instead of
// hardcoding a single provider — see debugHandlers.
type DebugStateProvider interface {
	// DebugNamespace is the virtual namespace name shown in /_debug/state's
	// top-level listing (e.g. "dynamodb:items", "logs:events").
	DebugNamespace() string
	// DebugStateKeys returns every key in this virtual namespace (used by
	// the top-level /_debug/state listing's key list).
	DebugStateKeys(ctx context.Context) ([]string, error)
	// DebugStateValues returns key->raw-value for /_debug/state/<namespace>.
	DebugStateValues(ctx context.Context) (map[string]string, error)
	// DebugResetState clears all data in this provider's dedicated storage.
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
func debugHandlers(cfg *config.Config, store state.Store, ec2 debugEC2Provider, providers []DebugStateProvider) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/health", debugHealth(cfg, store))
		r.Get("/config", debugConfig(cfg))
		r.Get("/state", debugState(store, providers))
		r.Get("/state/{namespace}", debugStateNamespace(store, providers))
		r.Post("/reset", debugReset(store, providers))
		r.Post("/reset/{service}", debugResetService(store, providers, cfg.Services))
		r.Get("/metrics", debugMetrics(store))

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

const (
	debugStateValuePreviewBytes = 100 * 1024
	debugStateTruncatedSuffix   = "...(truncated)"
)

// debugState (the top-level /_debug/state namespace listing) intentionally
// stays unpaginated (storage-plan.md item 3.13). It returns namespace -> key
// list only, never values — a bare key list for even a very large namespace
// (sqs:messages, logs:events) is orders of magnitude smaller than the
// per-namespace *values* response debugStateNamespace used to return
// unbounded (the actual multi-MB risk item 3.13 called out). If a namespace
// with an extreme key count ever makes this response itself a problem,
// paginating this endpoint too is a natural follow-up, but there is no
// evidence of that today, so it's left as-is rather than paginated
// speculatively.
func debugState(store state.Store, providers []DebugStateProvider) http.HandlerFunc {
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
		for _, p := range providers {
			if p == nil {
				continue
			}
			keys, err := p.DebugStateKeys(r.Context())
			if err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if len(keys) > 0 {
				result[p.DebugNamespace()] = keys
			}
		}
		writeDebugJSON(w, http.StatusOK, result)
	}
}

// debugStateNamespacePage is the paginated response shape for
// GET /_debug/state/{namespace} (storage-plan.md item 3.13, backend half).
//
// Contract for consumers (e.g. the web debug UI):
//   - Query params: ?after=<key> (exclusive cursor, omit/empty for the first
//     page) and ?limit=<n> (defaults to debugStateDefaultPageLimit, capped at
//     debugStateMaxPageLimit). Neither collides with the pre-existing ?key=
//     single-value param, which still short-circuits to
//     writeDebugStateRawValue and ignores after/limit entirely.
//   - Values is the page's key -> value map, values truncated per the
//     existing truncateDebugStateValue 100KB-preview logic.
//   - NextKey, when non-empty, is the cursor to pass as ?after= to fetch the
//     next page. An empty/absent NextKey means this was the last page.
type debugStateNamespacePage struct {
	Values  map[string]string `json:"values"`
	NextKey string            `json:"nextKey,omitempty"`
}

const (
	// debugStateDefaultPageLimit is used when ?limit= is absent or invalid.
	debugStateDefaultPageLimit = 500
	// debugStateMaxPageLimit caps ?limit= so a caller can't force an
	// arbitrarily large single response even by asking for one.
	debugStateMaxPageLimit = 5000
)

func debugStateNamespace(store state.Store, providers []DebugStateProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ns := chi.URLParam(r, "namespace")
		// Replace URL-encoded colons: "s3:buckets" → "s3:buckets"
		ns = strings.ReplaceAll(ns, "%3A", ":")
		if key := r.URL.Query().Get("key"); key != "" {
			writeDebugStateRawValue(w, r, store, providers, ns, key)
			return
		}
		after := r.URL.Query().Get("after")
		limit := parseDebugStateLimit(r.URL.Query().Get("limit"))

		var page map[string]string
		var nextKey string
		if p := debugProviderForNamespace(providers, ns); p != nil {
			// Virtual (DebugStateProvider-backed) namespaces don't expose a
			// paginated read of their own — DebugStateProvider is also
			// implemented outside this package (e.g. DynamoDB's item
			// backend), so extending that interface is a larger, separate
			// change. Fetch the full map (existing behavior; CloudWatch
			// Logs's provider already caps this internally — see
			// debugScan) and apply the same after/limit windowing in Go, so
			// callers see one consistent paginated contract regardless of
			// which kind of namespace they're reading.
			values, err := p.DebugStateValues(r.Context())
			if err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			page, nextKey = paginateDebugStateValues(values, after, limit)
		} else {
			// A single Scan(...Page) round trip instead of List+per-key Get
			// (storage-plan.md item 3.1 — this endpoint was itself a named
			// example of the N+1 pattern) — and paginated, so a namespace
			// with millions of keys (sqs:messages, logs:events-shaped
			// tables) never has to return them all in one response
			// (storage-plan.md item 3.13).
			pairs, nk, err := store.ScanPage(r.Context(), ns, "", after, limit)
			if err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			page = make(map[string]string, len(pairs))
			for _, kv := range pairs {
				page[kv.Key] = kv.Value
			}
			nextKey = nk
		}
		truncateDebugStateValues(page)
		writeDebugJSON(w, http.StatusOK, debugStateNamespacePage{Values: page, NextKey: nextKey})
	}
}

// parseDebugStateLimit parses the ?limit= query parameter, falling back to
// debugStateDefaultPageLimit for an absent, non-numeric, or non-positive
// value, and capping at debugStateMaxPageLimit.
func parseDebugStateLimit(raw string) int {
	if raw == "" {
		return debugStateDefaultPageLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return debugStateDefaultPageLimit
	}
	if n > debugStateMaxPageLimit {
		return debugStateMaxPageLimit
	}
	return n
}

// paginateDebugStateValues applies the same after/limit windowing ScanPage
// uses to an already-fetched, non-paginated map — the fallback path for
// DebugStateProvider-backed virtual namespaces (see debugStateNamespace).
// after is an exclusive cursor (matching ScanPage's startAfter semantics);
// limit <= 0 means "no limit", also matching ScanPage.
func paginateDebugStateValues(values map[string]string, after string, limit int) (map[string]string, string) {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	start := 0
	if after != "" {
		start = sort.SearchStrings(keys, after)
		if start < len(keys) && keys[start] == after {
			start++
		}
	}
	end := len(keys)
	if limit > 0 && start+limit < end {
		end = start + limit
	}

	page := make(map[string]string, end-start)
	for _, k := range keys[start:end] {
		page[k] = values[k]
	}
	nextKey := ""
	if end < len(keys) {
		nextKey = keys[end-1]
	}
	return page, nextKey
}

func writeDebugStateRawValue(w http.ResponseWriter, r *http.Request, store state.Store, providers []DebugStateProvider, namespace, key string) {
	var value string
	var found bool
	if p := debugProviderForNamespace(providers, namespace); p != nil {
		values, err := p.DebugStateValues(r.Context())
		if err != nil {
			writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		value, found = values[key]
	} else {
		var err error
		value, found, err = store.Get(r.Context(), namespace, key)
		if err != nil {
			writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	if !found {
		writeDebugJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("state key %q not found", key)})
		return
	}
	writeDebugRawValue(w, value)
}

func writeDebugRawValue(w http.ResponseWriter, value string) {
	if json.Valid([]byte(value)) {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(value))
}

func truncateDebugStateValues(values map[string]string) {
	for key, value := range values {
		values[key] = truncateDebugStateValue(value)
	}
}

func truncateDebugStateValue(value string) string {
	if len(value) <= debugStateValuePreviewBytes {
		return value
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		truncated := truncateDebugJSONStrings(decoded)
		if raw, err := json.Marshal(truncated); err == nil {
			return string(raw)
		}
	}
	return value[:debugStateValuePreviewBytes] + debugStateTruncatedSuffix
}

func truncateDebugJSONStrings(value any) any {
	switch v := value.(type) {
	case string:
		if len(v) <= debugStateValuePreviewBytes {
			return v
		}
		return v[:debugStateValuePreviewBytes] + debugStateTruncatedSuffix
	case []any:
		for i, item := range v {
			v[i] = truncateDebugJSONStrings(item)
		}
		return v
	case map[string]any:
		for key, item := range v {
			v[key] = truncateDebugJSONStrings(item)
		}
		return v
	default:
		return value
	}
}

func debugReset(store state.Store, providers []DebugStateProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resetStore(r.Context(), store)
		for _, p := range providers {
			if p == nil {
				continue
			}
			if err := p.DebugResetState(r.Context()); err != nil {
				writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		writeDebugJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	}
}

func debugResetService(store state.Store, providers []DebugStateProvider, services map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := chi.URLParam(r, "service")
		if !services[service] {
			writeDebugJSON(w, http.StatusBadRequest, map[string]string{
				"error": "unknown service: " + service,
			})
			return
		}
		namespaces, err := namespacesForService(r.Context(), store, service)
		if err != nil {
			writeDebugJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		prefix := debugServicePrefix(service)
		matchingProviders := debugProvidersForServicePrefix(providers, prefix)

		for _, ns := range namespaces {
			keys, _ := store.List(r.Context(), ns, "")
			for _, k := range keys {
				_ = store.Delete(r.Context(), ns, k)
			}
		}
		for _, p := range matchingProviders {
			if err := p.DebugResetState(r.Context()); err != nil {
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

// debugProviderForNamespace returns the provider whose virtual namespace
// matches ns, or nil if none of providers own it.
func debugProviderForNamespace(providers []DebugStateProvider, ns string) DebugStateProvider {
	for _, p := range providers {
		if p != nil && p.DebugNamespace() == ns {
			return p
		}
	}
	return nil
}

// debugProvidersForServicePrefix returns every provider whose virtual
// namespace's service segment (the part before ":", or the whole namespace
// if there is no ":") equals prefix — e.g. "logs:events" matches prefix
// "logs", "dynamodb:items" matches prefix "dynamodb".
func debugProvidersForServicePrefix(providers []DebugStateProvider, prefix string) []DebugStateProvider {
	if prefix == "" {
		return nil
	}
	var matched []DebugStateProvider
	for _, p := range providers {
		if p == nil {
			continue
		}
		ns := p.DebugNamespace()
		if svc, _, found := strings.Cut(ns, ":"); (found && svc == prefix) || (!found && ns == prefix) {
			matched = append(matched, p)
		}
	}
	return matched
}

// debugMetricsResponse is the JSON body for GET /_debug/metrics
// (storage-plan.md item 3.6). Stores is one entry per distinct underlying
// store that implements state.DebugMetricsReporter — see
// state.DebugMetricsSnapshot's doc comment for why a *state.NamespacedStore
// yields a list here instead of one merged entry. Empty (never null) for
// backends that don't implement it at all (MemoryStore, WALStore).
//
// Query params: ?includeRowCounts=true additionally computes
// NamespaceRowCounts per store — for TierCached namespaces this issues one
// SQL COUNT(*) per namespace, so it is opt-in rather than always computed.
type debugMetricsResponse struct {
	// state.DebugMetrics carries the JSON tags for this wire shape;
	// DebugFlushRecord timestamps are stored UTC and marshal as RFC 3339.
	Stores []state.DebugMetrics `json:"stores"`
}

func debugMetrics(store state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := state.DebugMetricsOptions{
			IncludeNamespaceRowCounts: r.URL.Query().Get("includeRowCounts") == "true",
		}
		snapshots, _ := state.DebugMetricsSnapshot(r.Context(), store, opts)
		if snapshots == nil {
			snapshots = []state.DebugMetrics{}
		}
		writeDebugJSON(w, http.StatusOK, debugMetricsResponse{Stores: snapshots})
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

// resetStore clears all data in store. It fast-paths *state.MemoryStore via
// its Reset() method and falls back to a generic list-and-delete sweep
// (resetAllNamespaces) for anything else. When store is a
// *state.NamespacedStore, the same logic is applied recursively to every
// distinct underlying store instead of asserting the concrete type of the
// wrapper itself — a bare `store.(*state.MemoryStore)` check silently missed
// wrapped stores regardless of what backend they actually wrap, doing a
// (still-correct-but-unintended) generic sweep even when a fast reset was
// available on every underlying store.
func resetStore(ctx context.Context, store state.Store) {
	if ns, ok := store.(*state.NamespacedStore); ok {
		for _, underlying := range ns.UnderlyingStores() {
			resetStore(ctx, underlying)
		}
		return
	}
	if ms, ok := store.(*state.MemoryStore); ok {
		ms.Reset()
		return
	}
	resetAllNamespaces(ctx, store)
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
