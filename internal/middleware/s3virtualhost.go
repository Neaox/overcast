package middleware

import (
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// defaultVirtualHostBases are the base hostnames recognised for
// virtual-hosted-style addressing with no configuration. Every subdomain of
// each resolves to 127.0.0.1 — "localhost" natively, and the two wildcard-DNS
// domains via public DNS — so SDK-generated bucket hostnames reach the
// emulator without hosts-file edits. localhost.localstack.cloud is included
// because it is what a user migrating from LocalStack already has in their
// endpoint config; without it their first CDK asset upload fails with a
// baffling "bucket name is not valid" error naming the object key.
var defaultVirtualHostBases = []string{
	"localhost",
	"localhost.overcast.sh",
	"localhost.localstack.cloud",
}

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
	return S3VirtualHostFor("", nil)(next)
}

// S3VirtualHostFor returns a middleware that recognises S3 virtual-hosted-style requests.
//
// When hostname is non-empty, requests whose Host header ends with ".<hostname>" are treated the same as
// ".<localhost>" requests — the leading subdomain is the bucket name.
//
// Example with hostname="localhost.localstack.cloud":
//
//	Host: cdk-hnb659fds-assets-000000000000-ap-southeast-2.localhost.localstack.cloud:4566
//
// is rewritten to path /cdk-hnb659fds-assets-000000000000-ap-southeast-2/… .
func S3VirtualHostFor(hostname string, logger ...*zap.Logger) func(http.Handler) http.Handler {
	var log *zap.Logger
	if len(logger) > 0 {
		log = logger[0]
	}
	var warned sync.Map
	var warnedCount atomic.Int64
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bucket := extractS3BucketFromHost(r.Host, hostname); bucket != "" {
				r.URL.Path = "/" + bucket + r.URL.Path
				if r.URL.RawPath != "" {
					r.URL.RawPath = "/" + bucket + r.URL.RawPath
				}
			} else {
				warnUnrecognisedBase(log, &warned, &warnedCount, r.Host, hostname)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// maxWarnedBases bounds the dedup set so a client sending random subdomains
// cannot grow it without limit.
const maxWarnedBases = 64

// warnUnrecognisedBase logs once per distinct Host when a request looks like
// virtual-hosted addressing against a base domain we do not recognise. Left
// unwarned, that request silently stays path-style and the object key is then
// parsed as the bucket name, producing an error that names the key rather than
// the real problem.
func warnUnrecognisedBase(log *zap.Logger, warned *sync.Map, count *atomic.Int64, host, configured string) {
	if log == nil || !looksVirtualHosted(host, configured) {
		return
	}
	if _, seen := warned.Load(host); seen {
		return
	}
	if count.Load() >= maxWarnedBases {
		return
	}
	if _, loaded := warned.LoadOrStore(host, struct{}{}); loaded {
		return
	}
	count.Add(1)
	shown := configured
	if shown == "" {
		shown = "unset"
	}
	log.Warn("S3 virtual-hosted request with unrecognised host base — bucket not extracted; "+
		"set OVERCAST_HOSTNAME to this domain, or use path-style addressing",
		zap.String("host", host),
		zap.String("overcast_hostname", shown),
		zap.Strings("recognised_bases", defaultVirtualHostBases),
	)
}

// looksVirtualHosted reports whether a Host whose bucket could not be
// extracted nonetheless carries a label in front of an unrecognised base —
// i.e. it plausibly meant to be virtual-hosted. Ordinary path-style traffic
// (an IP, a bare recognised base, a single-label host, or an S3 service
// endpoint) is legitimate and must stay silent.
func looksVirtualHosted(host string, extraBase ...string) bool {
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	if net.ParseIP(hostname) != nil || !strings.Contains(hostname, ".") {
		return false
	}
	if isS3ServiceEndpoint(hostname) {
		return false
	}
	for _, base := range defaultVirtualHostBases {
		if hostname == base {
			return false
		}
	}
	for _, b := range extraBase {
		if b != "" && hostname == b {
			return false
		}
	}
	return true
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
	// canonical AWS virtual-hosted separator. The bucket is everything before
	// the FIRST occurrence, since bucket names may themselves contain dots.
	if idx := strings.Index(hostname, ".s3."); idx > 0 {
		return hostname[:idx]
	}

	// {bucket}.s3-{region}.{base} — legacy dash dialect used by pre-2019 AWS
	// regions (e.g. mybucket.s3-us-west-2.amazonaws.com,
	// mybucket.s3-external-1.amazonaws.com). Same first-match rule as above.
	// This does not misfire on the bare regional endpoint itself
	// ("s3-us-west-2.amazonaws.com") because there is no leading dot there.
	if idx := strings.Index(hostname, ".s3-"); idx > 0 {
		return hostname[:idx]
	}

	// Check recognised non-s3 base hostnames — the built-in defaults plus any
	// configured extra base (OVERCAST_HOSTNAME), which adds to the defaults
	// rather than replacing them. Longest base first, so a configured parent
	// domain never shadows a longer default that also matches: with
	// OVERCAST_HOSTNAME="overcast.sh", "x.localhost.overcast.sh" is bucket
	// "x", not "x.localhost".
	bases := make([]string, 0, len(defaultVirtualHostBases)+len(extraBase))
	bases = append(bases, defaultVirtualHostBases...)
	for _, b := range extraBase {
		if b != "" && !slices.Contains(bases, b) {
			bases = append(bases, b)
		}
	}
	slices.SortStableFunc(bases, func(a, b string) int { return len(b) - len(a) })
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
