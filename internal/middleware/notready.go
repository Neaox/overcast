package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

// notReadyRetryAfterSeconds is the Retry-After hint sent with a 503 response
// while the storage backend is still migrating. Deliberately short — the
// migration itself is what determines actual wait time, this is just a
// reasonable poll interval for a well-behaved client that honours the
// header rather than relying solely on its SDK's own retry/backoff policy.
const notReadyRetryAfterSeconds = 2

// NotReady rejects a request with a 503 while the storage backend is still
// completing a one-time startup migration (see internal/state/migrate.go),
// instead of letting the request observe whatever the store would otherwise
// do during that window: persistent mode blocks the request indefinitely
// inside ensureReady, and hybrid mode's TierHot reads silently return "not
// found" for data that exists once migration finishes, because the
// post-migration seed hasn't populated memory yet (see
// state.NotReadyReporter and HybridStore.NotReady for the precise window
// this covers).
//
// Internal Overcast endpoints — any path starting with "/_" — are exempt, so
// operators can still check status, inspect debug state, or poll init-hook
// progress while a migration is in flight. No real AWS API request path starts
// with "/_", because an S3 bucket name cannot.
//
// store is checked once per request via a non-blocking type assertion to
// state.NotReadyReporter — stores that don't implement it (MemoryStore,
// WALStore) are always treated as ready, the same convention
// state.ReadyAwaiter already uses.
func NotReady(store state.Store) func(http.Handler) http.Handler {
	reporter, ok := store.(state.NotReadyReporter)
	if !ok {
		// This store type never has a "still starting up" window (e.g.
		// MemoryStore) — skip the per-request type assertion entirely
		// rather than repeating a check that can never succeed.
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isInternalPath(r.URL.Path) || !reporter.NotReady() {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Retry-After", strconv.Itoa(notReadyRetryAfterSeconds))
			if detectService(r) == "s3" {
				protocol.WriteXMLError(w, r, protocol.ErrStorageMigrating)
			} else {
				protocol.WriteJSONError(w, r, protocol.ErrStorageMigrating)
			}
		})
	}
}

// isInternalPath reports whether path is one of Overcast's own operational
// endpoints rather than an AWS API request. Every one is registered under a
// leading underscore, a convention no real AWS service path uses.
//
// It stays the broad "/_" test rather than router.InternalPrefix until
// docs/plans/non-canonical-url-namespace.md finishes: phase 2 moved the router
// roots, but /_debug, /_cognito, /_lambda and the rest are still outside the
// namespace, and exempting only /_overcast/ would start gating them mid-
// migration. The remaining paths are listed in that plan's unmigratedRoutes
// ratchet; when it empties, this becomes the prefix test.
//
// (router.InternalPrefix is not referenced here in any case — router imports
// middleware, so the dependency only runs the other way.)
func isInternalPath(path string) bool {
	return strings.HasPrefix(path, "/_")
}
