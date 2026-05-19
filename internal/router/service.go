// Package router wires together all service handlers into a single HTTP server.
// It owns the middleware chain and the service dispatch logic.
package router

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
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

// QueryDispatcher is implemented by services that use the AWS Query protocol:
// form-encoded POST body with an Action field, XML responses. SNS uses this
// protocol. The router calls r.ParseForm() before invoking DispatchQuery so
// handlers can read fields with r.FormValue().
type QueryDispatcher interface {
	DispatchQuery(w http.ResponseWriter, r *http.Request)
}

// Stopper is optionally implemented by services that hold background resources
// (goroutines, open connections, child processes) that must be released before
// the process exits. The router calls Stop on every Stopper after the HTTP
// server has drained, passing the remaining shutdown context.
type Stopper interface {
	Stop(ctx context.Context)
}

// Readier is optionally implemented by services that perform background
// initialisation (e.g. Docker probing). WaitReady blocks until the service
// is fully ready to handle requests. The router collects all Readier services
// and returns a combined wait function for use in tests.
type Readier interface {
	WaitReady()
}

// PathPrefixService is optionally implemented by services that own specific URL
// path prefixes distinct from S3's /{bucket}/* wildcard. When a service is
// disabled, the router registers a 503 ServiceDisabled catch-all handler at
// each declared prefix so callers receive a clear error instead of a stray 404
// or a spurious S3 response. The format (JSON or XML) is determined by the
// Content-Type of the incoming request at dispatch time.
//
// Prefix examples: "/2015-03-31" for Lambda, "/clusters" for EKS.
// Each prefix must start with "/" and must NOT end with "/".
type PathPrefixService interface {
	// PathPrefixes returns the URL path prefixes this service owns.
	PathPrefixes() []string
}

// QueryVersionOwner is an optional interface for QueryDispatcher services that
// identify themselves by the AWS API Version parameter (e.g. "2010-05-15")
// rather than by individual action names. The router checks OwnsVersion in the
// FIRST pass, before QueryActionOwner, because the version string is a stricter
// discriminator — it prevents action-name collisions between services (e.g.
// both SES and CloudFormation implement "GetTemplate").
type QueryVersionOwner interface {
	OwnsVersion(version string) bool
}

// QueryActionOwner is an optional interface for QueryDispatcher services that
// want to declare which Action values they handle. The router uses OwnsAction
// in the SECOND pass, after all QueryVersionOwner dispatchers have declined.
// Dispatchers that implement neither interface are tried as a final fallback.
type QueryActionOwner interface {
	OwnsAction(action string) bool
}

// AWS Query-protocol API versions are defined in internal/awsapi/versions.go.
// Service packages import that package directly to avoid import cycles.

// ContainerReconciler is optionally implemented by services that manage Docker
// containers. When Docker becomes available at startup, the router calls
// ReconcileContainers with the current state of all managed containers for that
// service. Services should use this to sync their stored state (e.g. mark
// instances as "stopped" if their container exited while overcast was down).
//
// ReconcileContainers must be idempotent — it will only be called once per
// Docker availability event, but it must produce correct results regardless of
// the order or timing of the call relative to CreateDBInstance etc.
type ContainerReconciler interface {
	// ReconcileContainers is called once after Docker becomes available.
	// containers lists every managed container for this service, running or stopped.
	ReconcileContainers(ctx context.Context, containers []docker.ContainerSummary)
}

// NetworkReconciler is optionally implemented by services that manage Docker
// networks. When Docker becomes available at startup, the router calls
// ReconcileNetworks with the current state of all managed networks for that
// service. Services should use this to sync their stored state (e.g. recreate
// missing VPC networks, update Docker network IDs in the store).
//
// ReconcileNetworks must be idempotent.
type NetworkReconciler interface {
	// ReconcileNetworks is called once after Docker becomes available.
	// networks lists every managed network for this service.
	ReconcileNetworks(ctx context.Context, networks []docker.NetworkSummary)
}

// ProtocolService is optionally implemented by services that opt into the
// Smithy-aligned typed dispatcher (see docs/plans/smithy.md §4.4). The
// service supplies a typed operation registry and the set of wire protocols
// it accepts; the protocol-detection middleware (gated by
// OVERCAST_PROTOCOL_DISPATCH) puts the resolved codec and operation name in
// the request context, and the service's Dispatch consults that context to
// route to the typed operation.
//
// Services that implement only Service (not this interface) keep working
// exactly as today — the middleware is a passthrough for them.
type ProtocolService interface {
	Service

	// Operations returns the typed operations this service implements.
	// Operation names are AWS operation names ("SendMessage", "GetItem").
	Operations() []op.Operation

	// SupportedProtocols returns the wire codecs this service accepts.
	// Used by the dispatcher to return 415 if a request arrives in a codec
	// the service doesn't speak.
	SupportedProtocols() []codec.Codec
}

// notFoundHandler returns a clean 404 for requests that don't match any route.
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
