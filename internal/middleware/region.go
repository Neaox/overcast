package middleware

import (
	"context"
	"net/http"
	"strings"
)

// regionKey is the context key for the per-request AWS region extracted from
// the SigV4 Authorization header's Credential scope.
type regionContextKey struct{}

// Region extracts the AWS region from each request and stores it in the context.
// Resolution order (first non-empty wins):
//  1. X-Overcast-Region header (internal override used by the CloudFormation provisioner)
//  2. SigV4 Authorization header Credential scope: AKID/DATE/REGION/SERVICE/aws4_request
//  3. Host header subdomain: <id>.execute-api.<region>.<base> — the canonical
//     AWS API Gateway invoke URL shape (also supported by LocalStack).
//
// If none yield a region the context is left unchanged and handlers fall back
// to cfg.Region (OVERCAST_DEFAULT_REGION, default "us-east-1"). This mirrors
// how LocalStack resolves region: always from the request, never from a
// server-wide setting.
func Region(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		region := r.Header.Get("X-Overcast-Region")
		if region == "" {
			region = regionFromCredential(r)
		}
		if region == "" {
			region = regionFromHost(r.Host)
		}
		if region != "" {
			// Canonicalise at the one point a region enters the context,
			// whichever source produced it. Everything downstream keys state on
			// this value (serviceutil.RegionKey), so "the region in context is
			// lowercase" has to hold unconditionally rather than depend on each
			// source having remembered. The header path is Overcast's own
			// internal override and already canonical; this costs it nothing,
			// because ToLower returns an already-lowercase string unchanged.
			region = strings.ToLower(region)
			ctx := context.WithValue(r.Context(), regionContextKey{}, region)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// regionFromHost extracts the region from an AWS-style hostname:
//
//	<id>.execute-api.<region>.amazonaws.com
//	<id>.appsync-api.<region>.localhost.localstack.cloud
//	<id>.<service>.<region>.<base>
//
// Returns "" when the host carries no region segment. Strips any port suffix.
//
// This used to carry its own list of service labels, which predated
// hostRouteLabels (hostroute.go) and had already drifted from it: it knew
// "sqs"/"sns"/"dynamodb" but not "appsync-api" or "lambda-url". That gap was a
// live bug rather than an untidiness. A host-routed invoke is precisely the
// case with no SigV4 to read a region from — AppSync authenticates with an
// x-api-key header and a Lambda function URL with nothing at all — so the Host
// is the ONLY region evidence such a request carries. Dropping it sent an
// AppSync API created in eu-west-1 to the default region's partition of a
// region-scoped store (serviceutil.RegionKey), where it does not exist.
//
// So the host-routed half now reads hostRouteLabels through the same parse the
// router dispatches on: a service Overcast host-routes can no longer be one
// this function fails to read a region from. Registering a new label in
// hostroute.go is now sufficient, which is what H5 in
// docs/plans/host-routing-precedence.md asked for.
//
// The host is case-folded first (foldHostname), because a hostname is
// case-insensitive and the region extracted here is not just a routing hint: it
// becomes the store's key prefix via serviceutil.RegionKey. Returning the
// segment verbatim let "…execute-api.US-East-1.…" partition state into a region
// no other request could name.
func regionFromHost(host string) string {
	hostname := foldHostname(hostWithoutPort(host))
	if hostname == "" || isIPLiteral(hostname) {
		return ""
	}

	// Host-routed services, via the router's own label set. A recognised label
	// is answered here even when its Region is "" — "{id}.appsync-api.localhost"
	// genuinely names no region, and falling through to guess one from a
	// later segment would be worse than the default-region fallback.
	if m, ok := parseHostRouteName(hostname); ok {
		return m.Region
	}
	return regionFromEndpointHost(hostname)
}

// regionalEndpointLabels are service labels that carry a region in position 1
// of an endpoint hostname but that Overcast does NOT host-route. They cannot
// live in hostRouteLabels: that set drives dispatch, and registering "s3"
// there would make every virtual-hosted bucket address parse as a host-routed
// service (and, per the guardrail in hostroute.go, a bare label like these
// would wreck the reserved-label veto besides).
//
// This is therefore not a second copy of hostRouteLabels — the two sets are
// disjoint by construction and answer different questions. "s3" is the one
// with a real AWS shape behind it ({bucket}.s3.{region}.amazonaws.com); the
// rest are retained because requests have historically been read this way and
// narrowing the set is a behaviour change no test asks for.
var regionalEndpointLabels = map[string]struct{}{
	"s3":       {},
	"sqs":      {},
	"sns":      {},
	"dynamodb": {},
	"lambda":   {},
}

// regionFromEndpointHost reads the region from "{id}.{service}.{region}.{base}"
// for the non-host-routed services above. hostname must already be port-stripped
// and case-folded.
//
// The segment after the label must actually look like a region. Without that
// check — as this read did before H5a — the base hostname's first segment was
// taken as one, so "mybucket.s3.localhost.overcast.sh" resolved to the region
// "localhost" and partitioned that request's S3 state under a name no other
// request could reach. It is the same failure regionSegment already prevents on
// the host-routed side, applied to the half that had been open to it; a real
// region ("mybucket.s3.us-east-1.amazonaws.com") is unaffected.
func regionFromEndpointHost(hostname string) string {
	parts := strings.Split(hostname, ".")
	if len(parts) < 4 {
		return ""
	}
	if _, ok := regionalEndpointLabels[parts[1]]; !ok {
		return ""
	}
	if !isAWSRegionShaped(parts[2]) {
		return ""
	}
	return parts[2]
}

// RegionFromContext returns the per-request region stored by the Region
// middleware. If absent, returns fallback.
func RegionFromContext(ctx context.Context, fallback string) string {
	if region, ok := ctx.Value(regionContextKey{}).(string); ok && region != "" {
		return region
	}
	return fallback
}

// ContextWithRegion returns a child context carrying region, suitable for
// background goroutines that need to access region-scoped stores outside a
// request context.
func ContextWithRegion(ctx context.Context, region string) context.Context {
	return context.WithValue(ctx, regionContextKey{}, region)
}

// regionFromCredential extracts the region component from the SigV4
// Authorization header. Returns "" if not parseable.
//
// The region is canonicalised to lowercase, as AWS region identifiers are and
// as requestedRegionFromSigV4 (iam_enforce.go) already does with this same
// segment. This is not cosmetic: serviceutil.RegionKey makes the value the
// store's key prefix, so an unfolded region partitions state into a region no
// other request can name — and, before this, a single request could evaluate
// its IAM conditions against "us-east-1" while writing state under "US-East-1".
//
// Folding cannot weaken authentication. Signature verification never reads this
// value: parseSigV4 rebuilds the scope string straight from the Authorization
// header, so a caller that signed an upper-case scope still verifies against
// exactly the bytes it sent.
func regionFromCredential(r *http.Request) string {
	parts := credentialScope(r)
	if len(parts) >= 3 && parts[2] != "" {
		// Deliberately the same expression as requestedRegionFromSigV4, so the
		// two readings of one scope cannot drift.
		return strings.ToLower(strings.TrimSpace(parts[2]))
	}
	return ""
}

// ServiceFromCredential extracts the service name (e.g. "appsync",
// "apigateway") from the SigV4 Authorization header's Credential scope.
// Returns "" if not parseable.
func ServiceFromCredential(r *http.Request) string {
	parts := credentialScope(r)
	if len(parts) >= 4 && parts[3] != "" {
		return parts[3]
	}
	return ""
}
