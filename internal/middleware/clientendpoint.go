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

// clientAddrContextKey is the context key for the caller's own IP address.
type clientAddrContextKey struct{}

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
//
// The caller's own address is stamped alongside the origin, because a name is
// not the whole answer for services whose resource is a *container* rather than
// Overcast itself: an RDS instance answers on its engine port inside the Docker
// network and on a published port on the host, so which port to hand back
// depends on which side of that boundary the caller is. The origin cannot say —
// a split-horizon hostname is used by both sides — but the source address can.
// See serviceutil.CallerIsSiblingContainer.
func ClientEndpoint(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		changed := false
		if origin := clientOrigin(r); origin != "" {
			ctx = context.WithValue(ctx, clientEndpointContextKey{}, origin)
			changed = true
		}
		if addr := serviceutil.ClientIP(r); addr != "" {
			ctx = context.WithValue(ctx, clientAddrContextKey{}, addr)
			changed = true
		}
		if changed {
			r = r.WithContext(ctx)
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
	if IsRealAWSHost(host) {
		return ""
	}
	return base
}

// IsRealAWSHost reports whether host belongs to real AWS's own domain space
// (awsOwnedSuffixes) rather than to anything Overcast could itself be
// addressed as. A bare IP is never a match — real AWS is never dialed by IP
// literal, and Overcast's own container/host addresses often are.
//
// Exported for WarnRealAWSHost (endpointpreflight.go), the "client's endpoint
// pointed somewhere other than the developer thinks" check named in
// docs/plans/deploy-failure-diagnosis.md's W4 — kept as one predicate here
// rather than duplicated, since clientOrigin's own gate above needs the exact
// same list and two copies of it once drifted (see Hostnames' doc comment in
// internal/containerendpoint for the same lesson learned the hard way).
func IsRealAWSHost(host string) bool {
	if host == "" || net.ParseIP(host) != nil {
		return false
	}
	lower := strings.ToLower(host)
	for _, suffix := range awsOwnedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
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

// ClientAddrFromContext returns the caller's IP address as stamped by the
// ClientEndpoint middleware, or "" when the request had none. It travels with
// the origin through internal dispatch — CloudFormation resolving a
// `Fn::GetAtt` runs on the deploying caller's context — so a resource attribute
// is rendered for whoever asked for the stack, not for the emulator.
func ClientAddrFromContext(ctx context.Context) string {
	if addr, ok := ctx.Value(clientAddrContextKey{}).(string); ok {
		return addr
	}
	return ""
}

// ContextWithClientAddr returns a child context carrying the caller's address,
// the companion of ContextWithClientEndpoint.
func ContextWithClientAddr(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, clientAddrContextKey{}, addr)
}
