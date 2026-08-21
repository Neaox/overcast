// Package hostnamecheck probes, at startup, whether OVERCAST_HOSTNAME's
// subdomains actually resolve on this host.
//
// Overcast advertises client-facing URLs built from OVERCAST_HOSTNAME
// (default "localhost"), and several AWS access patterns address it as a
// *subdomain* — virtual-hosted-style S3 above all ("{bucket}.localhost"),
// which is what CDK's asset publisher builds against an endpoint override
// regardless of forcePathStyle, plus every host-routed invoke URL
// (API Gateway's execute-api, Lambda function URLs, AppSync) that
// internal/middleware/hostaddressing dispatches on a subdomain label — see
// hostRouteLabels there. The Cognito issuer URL is a sibling AWS-hostname
// pattern but is deliberately *not* affected: issuerFor in
// internal/services/cognito/handler_auth.go builds it as a path
// (base + "/" + poolId), not a subdomain, precisely so it survives a caller
// on a remapped port; it is unaffected by anything this package probes.
// Whether subdomain resolution works is a property of the host's resolver,
// not of Overcast, and when it does not work the failure surfaces far from
// the cause: `cdk deploy` fails an asset publish with a bare "getaddrinfo
// ENOTFOUND cdk-hnb659fds-assets-...us-east-1.localhost", which reads like a
// broken emulator rather than a resolver limitation.
//
// Resolving the configured hostname on its own proves nothing — "localhost"
// resolves on every platform, which is exactly why the gap is invisible.
// What actually fails is a subdomain of it (on Windows, *.localhost does not
// resolve the way it does on Linux). So the probe is two lookups: the bare
// hostname, then a synthetic subdomain of it. See Probe.
//
// This package does not detect the operating system — it resolves, and lets
// the answer speak, so it stays correct on any platform (including ones
// where the gap gets fixed underneath it).
package hostnamecheck

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"
)

// probeLabel is the synthetic subdomain used to test whether an arbitrary
// subdomain of the configured hostname resolves — as opposed to the bare
// hostname itself, which "localhost" and every wildcard-DNS base resolve
// even where their subdomains do not.
const probeLabel = "overcast-probe"

// lookupTimeout bounds each of the two lookups Probe performs. Run() invokes
// Probe from a goroutine off the startup critical path (see its doc
// comment), so this only needs to give a slow resolver a fair chance, not to
// keep boot latency down.
const lookupTimeout = 2 * time.Second

// LookupFunc resolves a hostname to its addresses. Matches the signature of
// (*net.Resolver).LookupHost so the real resolver satisfies it directly;
// tests inject a fake to exercise the resolves / NXDOMAIN / timeout branches
// without depending on this host's actual DNS.
type LookupFunc func(ctx context.Context, host string) ([]string, error)

// Verdict classifies a Result into the three outcomes the probe
// distinguishes.
type Verdict uint8

const (
	// VerdictOK means both lookups resolved: nothing to warn about.
	VerdictOK Verdict = iota
	// VerdictHostnameUnresolvable means the bare hostname itself did not
	// resolve — a louder problem than the subdomain case, since nothing
	// built on OVERCAST_HOSTNAME will work at all, not just
	// virtual-hosted-style addressing.
	VerdictHostnameUnresolvable
	// VerdictSubdomainsUnresolvable means the bare hostname resolved but the
	// synthetic subdomain did not: virtual-hosted-style addressing is
	// unavailable on this host, though ordinary path-style traffic is fine.
	VerdictSubdomainsUnresolvable
)

// Result is the outcome of probing a hostname's own resolution and a
// synthetic subdomain's.
type Result struct {
	Hostname    string
	ProbeName   string // probeLabel + "." + Hostname
	BareOK      bool
	BareErr     error
	SubdomainOK bool
	SubErr      error
}

// Verdict classifies the result. See the Verdict* constants.
func (r Result) Verdict() Verdict {
	switch {
	case !r.BareOK:
		return VerdictHostnameUnresolvable
	case !r.SubdomainOK:
		return VerdictSubdomainsUnresolvable
	default:
		return VerdictOK
	}
}

// Probe resolves hostname and a synthetic subdomain of it with lookup, each
// bounded by lookupTimeout so a hung or slow resolver cannot block the
// caller indefinitely.
//
// An IP literal (Overcast accepts one for OVERCAST_HOSTNAME, e.g. a fixed
// Docker Compose service address) is returned as VerdictOK without any
// lookup: virtual-hosted-style addressing over a bare IP is not a thing AWS
// SDKs do, so there is nothing meaningful to probe and no lookup to hang on.
//
// When the bare hostname does not resolve, the subdomain lookup is skipped:
// it would only ever fail too, and skipping it halves the worst-case latency
// of a hung resolver.
func Probe(ctx context.Context, hostname string, lookup LookupFunc) Result {
	r := Result{Hostname: hostname, ProbeName: probeLabel + "." + hostname}
	if hostname == "" || net.ParseIP(hostname) != nil {
		r.BareOK, r.SubdomainOK = true, true
		return r
	}

	bareCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
	_, r.BareErr = lookup(bareCtx, hostname)
	cancel()
	r.BareOK = r.BareErr == nil
	if !r.BareOK {
		return r
	}

	subCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
	_, r.SubErr = lookup(subCtx, r.ProbeName)
	cancel()
	r.SubdomainOK = r.SubErr == nil
	return r
}

// knownWildcardBases are the hostnames whose subdomains are *expected* to
// resolve: the default "localhost" (RFC 6761 reserves *.localhost for
// loopback, and most resolvers honour it, Windows's being the platform this
// whole package exists for) and every base config.WildcardDNSDomains
// advertises a public wildcard A record for. This list is duplicated rather
// than imported from internal/config to avoid a dependency from a
// resolver-probing leaf package back onto config; callers pass any
// additional known-good bases (OVERCAST_SPLIT_HORIZON_HOSTS) via Warn.
var knownWildcardBases = []string{
	"localhost",
	"localhost.overcast.sh",
	"localhost.localstack.cloud",
	"localhost.floci.io",
}

// isKnownWildcardBase reports whether hostname is one Overcast (or public
// DNS) is expected to have wildcard resolution for, as opposed to an
// arbitrary custom hostname a user pointed OVERCAST_HOSTNAME at.
func isKnownWildcardBase(hostname string, extraKnownBases []string) bool {
	if slices.ContainsFunc(knownWildcardBases, func(b string) bool { return strings.EqualFold(b, hostname) }) {
		return true
	}
	return slices.ContainsFunc(extraKnownBases, func(b string) bool { return strings.EqualFold(b, hostname) })
}

// Warn logs a Warn-level, startup-appropriate message for r, or does nothing
// when r.Verdict() is VerdictOK. splitHorizonHosts is
// Config.SplitHorizonHosts — extra hostnames the operator has already told
// Overcast carry a public wildcard A record — passed through so the message
// does not wrongly append the "this may be a false positive" caveat for a
// deliberately configured wildcard base.
//
// The message never suggests Overcast is broken or that the server should
// stop: this is "warn, never fail" — plenty of deployments only ever use
// path-style S3 addressing, for which none of this matters.
func Warn(logger *zap.Logger, r Result, splitHorizonHosts []string) {
	if logger == nil {
		return
	}
	switch r.Verdict() {
	case VerdictOK:
		// Nothing to warn about — resolution works.
	case VerdictHostnameUnresolvable:
		logger.Warn(
			fmt.Sprintf(
				"OVERCAST_HOSTNAME=%s does not resolve on this host at all — every client-facing URL "+
					"Overcast advertises is built from it, so nothing that dials it back (the AWS CLI, "+
					"SDKs, the web console, `cdk deploy`) will connect. This is a property of this "+
					"host's resolver, not of Overcast. Fix: point OVERCAST_HOSTNAME at a name this "+
					"host's resolver can answer (the default \"localhost\" always does), or add it to "+
					"your hosts file.",
				r.Hostname,
			),
			zap.String("hostname", r.Hostname),
			zap.NamedError("lookup_error", r.BareErr),
		)
	case VerdictSubdomainsUnresolvable:
		msg := fmt.Sprintf(
			"virtual-hosted-style addressing will not work with OVERCAST_HOSTNAME=%s: %q resolves, "+
				"but %q does not — this host's resolver does not map subdomains of it. Affects "+
				"virtual-hosted-style S3 (bucket.%s) and every host-routed invoke URL (API Gateway, "+
				"Lambda function URLs, AppSync): `cdk deploy` asset publishing fails with ENOTFOUND on "+
				"a bucket-shaped hostname. This is host-side only — containers Overcast starts still "+
				"resolve these names, through Overcast's own DNS resolver (OVERCAST_DNS); it is "+
				"clients running on this host (your shell, cdk, a browser) that cannot. Path-style S3 "+
				"addressing is unaffected.",
			r.Hostname, r.Hostname, r.ProbeName, r.Hostname,
		)
		if isKnownWildcardBase(r.Hostname, splitHorizonHosts) {
			msg += " Fix: set OVERCAST_HOSTNAME=localhost.overcast.sh (wildcard A record -> 127.0.0.1), " +
				"or add the specific subdomains you use to your hosts file."
		} else {
			msg += fmt.Sprintf(
				" Fix: set OVERCAST_HOSTNAME=localhost.overcast.sh (wildcard A record -> 127.0.0.1), or "+
					"add the specific subdomains you use to your hosts file. If %s is a hostname you "+
					"already resolve via your hosts file, this warning is expected and not a bug: a "+
					"hosts file is an exact-match table and cannot express a wildcard, so this warning "+
					"will keep appearing regardless — ignore it if you only ever use path-style S3 "+
					"addressing.",
				r.Hostname,
			)
		}
		logger.Warn(msg,
			zap.String("hostname", r.Hostname),
			zap.String("probe_subdomain", r.ProbeName),
			zap.NamedError("lookup_error", r.SubErr),
		)
	}
}

// defaultLookup adapts net.DefaultResolver to LookupFunc.
func defaultLookup(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// Run probes hostname with the real system resolver and logs the outcome
// with Warn. It is meant to be launched with `go hostnamecheck.Run(...)`
// from server startup so the DNS lookups — inherently I/O, and occasionally
// slow or hung resolvers on misconfigured hosts — never sit on the startup
// critical path the #252 startup-metrics work measures. lookupTimeout bounds
// each individual lookup even so, and ctx lets the caller bound the whole
// probe (e.g. to the server's lifetime) if it wants to.
func Run(ctx context.Context, hostname string, splitHorizonHosts []string, logger *zap.Logger) {
	Warn(logger, Probe(ctx, hostname, defaultLookup), splitHorizonHosts)
}
