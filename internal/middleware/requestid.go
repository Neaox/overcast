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

	"github.com/Neaox/overcast/internal/protocol"
)

// RequestID attaches a unique request ID to every request context. All
// subsequent middleware and handlers retrieve it via
// protocol.RequestIDFromContext(r.Context()).
//
// It sets no response header. Echoing the ID back is the protocol write
// helpers' job, and they disagree on the name — JSON and Query protocols
// answer with x-amzn-requestid, S3's REST XML with x-amz-request-id, and some
// successful responses carry neither. Do not read a request's ID back off its
// response: that assumption is what silently produced empty IDs, and so broken
// parent/child trace links, for every S3 hop.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Honour a request ID provided by the caller, or generate a new one.
		// Real AWS does not accept caller-supplied IDs. Accepting them here
		// makes integration tests predictable, and lets internal dispatch pin
		// a child call's ID up front so a parent's hop can link to the child
		// trace without depending on which header the response echoes.
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
