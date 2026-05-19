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
			ctx := context.WithValue(r.Context(), regionContextKey{}, region)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// regionFromHost extracts the region from an AWS-style hostname:
//
//	<id>.execute-api.<region>.amazonaws.com
//	<id>.execute-api.<region>.localhost.localstack.cloud
//	<id>.<service>.<region>.<base>
//
// Returns "" if the host does not have at least 4 dot-separated labels with a
// known service segment in position 1. Strips any port suffix.
func regionFromHost(host string) string {
	if host == "" {
		return ""
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	parts := strings.Split(host, ".")
	if len(parts) < 4 {
		return ""
	}
	switch parts[1] {
	case "execute-api", "s3", "sqs", "sns", "dynamodb", "lambda":
		return parts[2]
	}
	return ""
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
func regionFromCredential(r *http.Request) string {
	parts := credentialScope(r)
	if len(parts) >= 3 && parts[2] != "" {
		return parts[2]
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
