package middleware

import (
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
)

// s3virtualhost.go holds the S3-specific parts of host addressing: the
// recognised base hostnames, the service-endpoint guard, and the diagnostic for
// requests that look virtual-hosted against a base we do not know.
//
// The decision of whether a Host is an S3 bucket at all lives in
// hostaddressing.go, because it cannot be made independently of the
// host-routed services — see that file's doc comment and
// docs/plans/host-routing-precedence.md.

// defaultVirtualHostBases are the base hostnames recognised for
// virtual-hosted-style addressing with no configuration: plain "localhost",
// which resolves to 127.0.0.1 natively, plus every public wildcard-DNS domain
// in config.WildcardDNSDomains.
//
// Deriving the wildcard domains rather than restating them is deliberate — the
// two lists were maintained separately and drifted, leaving buckets
// unreachable on localhost.floci.io while containerendpoint advertised it.
// TestVirtualHostBases_coverEveryWildcardDomain now fails if they diverge
// again.
var defaultVirtualHostBases = append(
	[]string{"localhost"},
	config.WildcardDNSDomains...,
)

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

// isS3ServiceEndpoint returns true when the extracted hostname prefix looks like
// an S3 service endpoint (e.g. "s3", "s3.us-east-1") rather than a bucket name.
// s3 and s3.{region} can never be valid bucket names — "s3" is too short (min 3)
// and dots are forbidden in bucket names.
func isS3ServiceEndpoint(candidate string) bool {
	return candidate == "s3" || strings.HasPrefix(candidate, "s3.")
}

// extractS3BucketFromHost returns the bucket name a Host header addresses, or
// "" when it addresses no bucket. It is a thin adapter over the shared
// classifier, retained because the S3 addressing table is specified by its own
// focused unit tests in s3virtualhost_test.go.
func extractS3BucketFromHost(host string, extraBase ...string) string {
	base := ""
	if len(extraBase) > 0 {
		base = extraBase[0]
	}
	if claim := NewHostClassifier(base).Classify(host); claim.Kind == HostClaimS3 {
		return claim.Bucket
	}
	return ""
}
