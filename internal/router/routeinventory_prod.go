//go:build !dev

package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// withDispatchMounts returns the mux unchanged in production builds, so a
// released binary serves requests through the bare chi mux exactly as before.
//
// New still records the mounts either way rather than branching on a build
// tag: it is eight appends once at startup, and one code path is worth more
// than that. Only the wrapper is dev-only.
func withDispatchMounts(r *chi.Mux, _ []dispatchMount) http.Handler { return r }
