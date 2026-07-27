package middleware

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Neaox/overcast/internal/serviceutil"
)

// clientEndpointContextKey is the context key for the origin the caller used to
// reach Overcast.
type clientEndpointContextKey struct{}

// awsOwnedSuffixes are hostname suffixes belonging to real AWS endpoints.
// A request can arrive on one of these via host-based addressing (a hosts-file
// alias, a DNS override, or a proxy), but the name is not something an
// arbitrary client on the machine can dial, so it is never echoed back into a
// resource URL.
var awsOwnedSuffixes = []string{".amazonaws.com", ".on.aws", ".aws"}

// ClientEndpoint records the origin — scheme://host[:port] — that the caller
// used to reach Overcast, so handlers can mint resource URLs the caller can
// actually dial back.
//
// This matters because several AWS SDKs resolve a service endpoint from a
// resource URL rather than from client configuration. The clearest case is SQS:
// @aws-sdk/middleware-sdk-sqs replaces the resolved endpoint with the QueueUrl's
// origin whenever the two differ and no explicit `endpoint` was passed to the
// client — and AWS_ENDPOINT_URL does not count, because it is resolved through
// the endpoint ruleset's Endpoint parameter and never lands on config.endpoint.
// .NET and Java v1 use the queue URL as the request URI for the same historical
// reason (the query protocol addressed queues by URL).
//
// A single server-wide origin therefore cannot serve every caller: "localhost"
// is right for a host CLI and wrong inside a sibling Lambda container, and a
// compose service name is the reverse. Minting per request keeps every URL
// dialable by whoever asked for it.
//
// Requests arriving on a real AWS hostname fall through: the context is left
// unset and handlers use the configured external origin (OVERCAST_HOSTNAME).
func ClientEndpoint(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := clientOrigin(r); origin != "" {
			r = r.WithContext(context.WithValue(r.Context(), clientEndpointContextKey{}, origin))
		}
		next.ServeHTTP(w, r)
	})
}

// clientOrigin returns the dialable origin for r, or "" when the request
// carries no usable host or arrived on a real AWS hostname.
func clientOrigin(r *http.Request) string {
	base := serviceutil.RequestBaseURL(r)
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	if net.ParseIP(host) == nil {
		lower := strings.ToLower(host)
		for _, suffix := range awsOwnedSuffixes {
			if strings.HasSuffix(lower, suffix) {
				return ""
			}
		}
	}
	return base
}

// ClientEndpointFromContext returns the origin stored by the ClientEndpoint
// middleware, or "" when the request had none (background work, internal
// callers, or a request on a real AWS hostname). Callers fall back to the
// configured external origin.
func ClientEndpointFromContext(ctx context.Context) string {
	if origin, ok := ctx.Value(clientEndpointContextKey{}).(string); ok {
		return origin
	}
	return ""
}

// ContextWithClientEndpoint returns a child context carrying origin, for
// background goroutines that mint resource URLs outside a request context.
func ContextWithClientEndpoint(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, clientEndpointContextKey{}, origin)
}
