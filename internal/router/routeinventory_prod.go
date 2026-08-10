//go:build !dev

package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// withDispatchMounts returns the mux unchanged in production builds. The
// inventory exists for the dev-only model-binding gate; a released binary
// carries neither the wrapper nor a reason to serve requests through one.
func withDispatchMounts(r *chi.Mux, _ []dispatchMount) http.Handler { return r }
