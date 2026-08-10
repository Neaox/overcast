//go:build dev

package router

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// inspectableMux is the router plus the sub-routers it dispatches to at
// request time. It embeds *chi.Mux, so it serves and routes exactly as the
// bare mux does and still satisfies chi.Routes for callers that walk it.
type inspectableMux struct {
	*chi.Mux
	mounts []dispatchMount
}

// withDispatchMounts attaches the recorded mounts in dev builds so
// walkRegisteredRoutes can see the whole served surface.
func withDispatchMounts(r *chi.Mux, mounts []dispatchMount) http.Handler {
	return &inspectableMux{Mux: r, mounts: mounts}
}

// registeredRoute is one method/pattern pair the router serves, attributed to
// the sub-router that registered it where the main router owns a shared path
// space on a service's behalf.
type registeredRoute struct {
	Method  string
	Pattern string
	// Owner names the service package for a route registered on a dispatched
	// sub-router, and is empty for a route registered directly on the mux.
	Owner string
	// Fallback is set when the sub-router answers for any service the
	// dispatcher cannot attribute, not only for Owner.
	Fallback bool
}

// walkRegisteredRoutes visits every route the handler serves: the main mux,
// then each dispatched sub-router with its mount prefix applied.
//
// A route reached only through a dispatcher is walked once per owner, because
// two services really do register different operations at the same pattern
// under /v2/apis and /tags — reporting the pattern alone would lose which
// service answers it.
func walkRegisteredRoutes(h http.Handler) ([]registeredRoute, error) {
	mux, ok := h.(*inspectableMux)
	if !ok {
		return nil, errNotInspectable
	}

	var out []registeredRoute
	collect := func(mount dispatchMount) error {
		return chi.Walk(mount.Routes, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			out = append(out, registeredRoute{
				Method:   method,
				Pattern:  joinRoutePattern(mount.Prefix, pattern),
				Owner:    mount.Owner,
				Fallback: mount.Fallback,
			})
			return nil
		})
	}
	if err := collect(dispatchMount{Routes: mux.Mux}); err != nil {
		return nil, err
	}
	for _, mount := range mux.mounts {
		if err := collect(mount); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// joinRoutePattern prefixes a sub-router pattern with its mount path, keeping
// the result in the same shape chi.Walk reports for a directly registered
// route so both can be compared against a modeled URI without a second rule.
func joinRoutePattern(prefix, pattern string) string {
	joined := prefix + pattern
	if joined == "" {
		return "/"
	}
	if joined != "/" {
		joined = strings.TrimSuffix(joined, "/")
	}
	return joined
}

// errNotInspectable reports a handler that did not come from New, so the
// caller says "build it with New" rather than reading an empty inventory as a
// router that serves nothing.
var errNotInspectable = errors.New("router handler was not built by New; the dispatched sub-routers are not recorded")
