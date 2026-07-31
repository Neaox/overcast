//go:build dev

package appsync

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// TestRESTOperations_MatchRouteInventory asserts that the restOperations
// manifest in rest_operations_dev.go exactly covers the REST routes the
// AppSync service exposes: RegisterRoutes plus the EventsAPIRouter and
// TagsRouter mounts (wired by the main router under /v2/apis and /v1/tags
// respectively). capgen consumes the manifest to validate capability
// declarations, so drift here silently corrupts the capability inventory.
func TestRESTOperations_MatchRouteInventory(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	routeSet := make(map[string]struct{})
	collect := func(prefix string, r chi.Router) {
		err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			route = prefix + route
			if route != "/" {
				route = strings.TrimSuffix(route, "/")
			}
			switch {
			case route == "/v1/apis/*":
				// NotImplemented catch-all for unmatched sub-paths, not an operation.
				return nil
			case method == "GET" && route == "/_appsync/{apiId}/realtime":
				// WebSocket transport endpoint for subscriptions, not an AWS operation.
				return nil
			}
			routeSet[method+" "+route] = struct{}{}
			return nil
		})
		if err != nil {
			t.Fatalf("walk AppSync routes under %q: %v", prefix, err)
		}
	}

	main := chi.NewRouter()
	svc.RegisterRoutes(main)
	collect("", main)
	collect("/v2/apis", svc.EventsAPIRouter())
	collect("/v1/tags", svc.TagsRouter())

	manifestSet := make(map[string]string, len(restOperations))
	for _, op := range restOperations {
		manifestSet[op.Method+" "+op.Path] = op.Operation
	}

	var inRoutesNotManifest []string
	for route := range routeSet {
		if _, ok := manifestSet[route]; !ok {
			inRoutesNotManifest = append(inRoutesNotManifest, route)
		}
	}
	sort.Strings(inRoutesNotManifest)

	var inManifestNotRoutes []string
	for route, op := range manifestSet {
		if _, ok := routeSet[route]; !ok {
			inManifestNotRoutes = append(inManifestNotRoutes, route+" ("+op+")")
		}
	}
	sort.Strings(inManifestNotRoutes)

	if len(inRoutesNotManifest) > 0 {
		t.Errorf("routes registered but missing from rest_operations_dev.go:\n  %v\nAdd a restOperation entry for each.", inRoutesNotManifest)
	}
	if len(inManifestNotRoutes) > 0 {
		t.Errorf("entries in rest_operations_dev.go with no registered route:\n  %v\nRemove the stale entry or register the missing route.", inManifestNotRoutes)
	}
}
