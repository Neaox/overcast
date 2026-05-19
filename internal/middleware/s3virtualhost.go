package middleware

import (
	"net"
	"net/http"
	"strings"
)

// S3VirtualHost detects S3 virtual-hosted-style requests and rewrites the URL
// path to path-style so chi's /{bucket}/* routes match correctly.
//
// Virtual-hosted-style sends the bucket name in the Host header:
//
//	Host: mybucket.localhost:4566        → /key rewritten to /mybucket/key
//	Host: mybucket.s3.localhost:4566     → /key rewritten to /mybucket/key
//	Host: mybucket.s3.us-east-1.localhost → /key rewritten to /mybucket/key
//
// Path-style requests (bucket already in the URL path) pass through unchanged.
// Use S3VirtualHostFor when OVERCAST_HOSTNAME is a wildcard-DNS name (e.g.
// "localhost.localstack.cloud") so CDK asset-publisher URLs also resolve.
func S3VirtualHost(next http.Handler) http.Handler {
	return S3VirtualHostFor("")(next)
}

// S3VirtualHostFor returns a middleware that recognises an additional hostname
// base for S3 virtual-hosted-style requests. When hostname is non-empty,
// requests whose Host header ends with ".<hostname>" are treated the same as
// ".<localhost>" requests — the leading subdomain is the bucket name.
//
// Example: with hostname="localhost.localstack.cloud" a request to
//
//	Host: cdk-hnb659fds-assets-000000000000-ap-southeast-2.localhost.localstack.cloud:4566
//
// is rewritten to path /cdk-hnb659fds-assets-000000000000-ap-southeast-2/…
func S3VirtualHostFor(hostname string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bucket := extractS3BucketFromHost(r.Host, hostname); bucket != "" {
				r.URL.Path = "/" + bucket + r.URL.Path
				if r.URL.RawPath != "" {
					r.URL.RawPath = "/" + bucket + r.URL.RawPath
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractS3BucketFromHost returns the S3 bucket name from a virtual-hosted-style
// Host header, or "" if the request is path-style (no subdomain detected).
// extraBase is an optional additional hostname to recognise (e.g.
// "localhost.localstack.cloud" when OVERCAST_HOSTNAME is configured).
func extractS3BucketFromHost(host string, extraBase ...string) string {
	// Separate hostname from port.
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}

	// IP addresses never carry virtual-hosted subdomains.
	if net.ParseIP(hostname) != nil {
		return ""
	}

	// {bucket}.s3.{region}.{base} or {bucket}.s3.{base} — matches for any base
	// (localhost, amazonaws.com, or any custom hostname) because .s3. is the
	// canonical AWS virtual-hosted separator.
	if idx := strings.Index(hostname, ".s3."); idx > 0 {
		return hostname[:idx]
	}

	// Check recognised non-s3 base hostnames in order: localhost first, then
	// any configured extra base (e.g. "localhost.localstack.cloud").
	bases := make([]string, 0, 1+len(extraBase))
	bases = append(bases, "localhost")
	for _, b := range extraBase {
		if b != "" && b != "localhost" {
			bases = append(bases, b)
		}
	}
	for _, base := range bases {
		suffix := "." + base
		if strings.HasSuffix(hostname, suffix) {
			candidate := strings.TrimSuffix(hostname, suffix)
			if isS3ServiceEndpoint(candidate) {
				return ""
			}
			return candidate
		}
	}

	return ""
}

// isS3ServiceEndpoint returns true when the extracted hostname prefix looks like
// an S3 service endpoint (e.g. "s3", "s3.us-east-1") rather than a bucket name.
// s3 and s3.{region} can never be valid bucket names — "s3" is too short (min 3)
// and dots are forbidden in bucket names.
func isS3ServiceEndpoint(candidate string) bool {
	return candidate == "s3" || strings.HasPrefix(candidate, "s3.")
}
