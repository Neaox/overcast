// Package middleware contains HTTP middleware functions for the emulator's
// request pipeline. Each middleware is a standard net/http middleware —
// it takes a handler and returns a handler. This is identical to Express
// middleware in concept: (req, res, next) => void.
//
// In Go:
//
//	func MyMiddleware(next http.Handler) http.Handler {
//	    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	        // do something before
//	        next.ServeHTTP(w, r)
//	        // do something after
//	    })
//	}
package middleware

import (
	"net/http"

	"github.com/your-org/overcast/internal/protocol"
)

// RequestID attaches a unique request ID to every request context and response
// header. All subsequent middleware and handlers retrieve it via
// protocol.RequestIDFromContext(r.Context()).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Honour a request ID provided by the caller (e.g. in tests), or
		// generate a new one. Real AWS does not accept caller-supplied IDs
		// but accepting them here makes integration tests more predictable.
		id := r.Header.Get("x-amzn-requestid")
		if id == "" {
			id = protocol.NewRequestID()
		}

		// Store the ID in the request context so handlers can retrieve it
		// without threading it through every function signature.
		ctx := protocol.ContextWithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
