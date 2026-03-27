// Package router wires together all service handlers into a single HTTP server.
// It owns the middleware chain and the service dispatch logic.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Service is the interface every AWS service emulator must implement.
// Adding a new service means:
//  1. Create internal/services/<name>/ package.
//  2. Implement this interface.
//  3. Register it in New() below.
//
// Nothing else needs to change — the router is open for extension,
// closed for modification. (Open/Closed Principle, same as in any language.)
type Service interface {
	// Name returns the lowercase service name, e.g. "s3", "sqs".
	// Used to check against cfg.Services to decide whether to enable it.
	Name() string

	// RegisterRoutes mounts the service's HTTP handlers onto the given router.
	// The router is already chi.Router — use chi's routing DSL.
	RegisterRoutes(r chi.Router)
}

// TargetDispatcher is optionally implemented by services that use the
// X-Amz-Target header for dispatch on POST /. Because SQS, DynamoDB, and SNS
// all share the root POST / endpoint, the router needs to inspect the target
// header to route to the correct service. Services that implement this
// interface do NOT register POST / in RegisterRoutes — the router handles it.
type TargetDispatcher interface {
	// TargetPrefix returns the X-Amz-Target prefix for this service,
	// e.g. "AmazonSQS.", "DynamoDB_20120810.".
	TargetPrefix() string

	// Dispatch handles the request after the router has matched the target prefix.
	Dispatch(w http.ResponseWriter, r *http.Request)
}

// notFoundHandler returns a clean 404 for requests that don't match any route.
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
