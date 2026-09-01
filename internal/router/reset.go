package router

// reset.go implements POST /_overcast/reset and POST /_overcast/reset/{service}.
//
// These handlers used to live under the debug-gated /_overcast/debug/reset
// namespace (internal/router/debug.go), mounted only when cfg.Debug == true.
// They were moved out and made always-on (design decision 2026-09-01):
// OVERCAST_DEBUG exists to gate expensive or leaky instrumentation — request
// tracing, state dumps, pprof — and reset is neither. It also adds no
// destructive power beyond what the unauthenticated AWS API surface already
// exposes (any caller can already delete every resource one at a time
// through ordinary AWS calls). POST /_overcast/tls/setup is the existing
// precedent for an always-on, mutating /_overcast route.
//
// Moved, not rewritten: behavior and response shapes are unchanged from the
// debug-gated handlers they replace. See internal/router/router.go for where
// these are registered, and internal/router/reset_test.go for coverage.

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// resetHandler handles POST /_overcast/reset: wipes every namespace in store,
// plus every registered DebugStateProvider's dedicated storage.
func resetHandler(store state.Store, providers []DebugStateProvider) http.HandlerFunc {
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

// resetServiceHandler handles POST /_overcast/reset/{service}: wipes only the
// namespaces (and matching DebugStateProviders) belonging to one service.
func resetServiceHandler(store state.Store, providers []DebugStateProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := chi.URLParam(r, "service")
		if !slices.Contains(config.AllServices(), service) {
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
		prefix := resetServicePrefix(service)
		matchingProviders := resetProvidersForServicePrefix(providers, prefix)

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

// resetProvidersForServicePrefix returns every provider whose virtual
// namespace's service segment (the part before ":", or the whole namespace
// if there is no ":") equals prefix — e.g. "logs:events" matches prefix
// "logs", "dynamodb:items" matches prefix "dynamodb".
func resetProvidersForServicePrefix(providers []DebugStateProvider, prefix string) []DebugStateProvider {
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

func namespacesForService(ctx context.Context, store state.Store, service string) ([]string, error) {
	all, err := store.ListNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	prefix := resetServicePrefix(service)
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

// resetServicePrefix delegates to config.ServiceNamespacePrefix, the single
// source of truth for the config-service-name → storage-namespace-prefix
// mapping (see its doc comment for why this mapping exists).
func resetServicePrefix(service string) string {
	return config.ServiceNamespacePrefix(service)
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
