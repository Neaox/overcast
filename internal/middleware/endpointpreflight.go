package middleware

// endpointpreflight.go — "you dialed a real AWS hostname".
//
// # The failure this exists for
//
// docs/plans/deploy-failure-diagnosis.md's W4 names "the client's endpoint is
// pointed somewhere other than the developer thinks" as a known-costly trap
// distinct from the region check (internal/router/preflight_region.go): the
// region check answers "why is this list empty" once the console asks; this
// answers a question the developer never gets to ask at all, because from
// their side nothing looks wrong — their SDK is pointed at *some* endpoint,
// it answers, and they have no reason to suspect it isn't the one they meant.
//
// The one shape of that mistake Overcast can actually see is a request whose
// Host targets real AWS's own domain space (*.amazonaws.com and friends —
// see awsOwnedSuffixes in clientendpoint.go) arriving at Overcast at all. In
// the ordinary case such a request would go to real AWS and Overcast would
// never lay eyes on it; the only way it lands here is a hosts-file entry, a
// DNS override, or a proxy silently redirecting that traffic — which may be
// exactly what the developer set up on purpose (LocalStack's own
// documented technique), or may be a leftover from a previous project doing
// the exact same thing. Either way it is worth saying out loud once: this is
// the client-side mistake the region check cannot cover, because it has
// nothing to do with which region a request names.
//
// # Why this is "can only hint at it"
//
// Overcast cannot know which of the two the developer intended — that a real
// AWS hostname reached it at all is the entire observable fact. So the
// message states what's true (a real-AWS-shaped Host answered by Overcast)
// and names both fixes (point AWS_ENDPOINT_URL at Overcast explicitly, or
// remove the redirect) rather than picking one, matching the region check's
// own discipline of stating the fact and letting the developer draw the
// conclusion.
//
// # Why it only ever fires once
//
// A hosts-file or DNS redirect, once present, matches on every request for
// the rest of the process's life — logging it per-request would be exactly
// the "wall of output nobody reads" this whole workstream exists to avoid.
// One Warn the first time is the same discipline hostnamecheck.Warn and the
// region check both already follow: state the fact once, clearly, and get
// out of the way.
//
// # Why it never false-positives on the default path
//
// IsRealAWSHost only matches awsOwnedSuffixes, and neither "localhost" nor
// any OVERCAST_HOSTNAME/OVERCAST_SPLIT_HORIZON_HOSTS value Overcast itself
// advertises can end in one — the default compose/docker-run path never
// dials Overcast on a real-AWS-shaped hostname, so this never fires there.
import (
	"fmt"
	"net/http"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
)

// WarnRealAWSHost returns middleware that logs one actionable Warn the first
// time a request's Host targets real AWS's own domain space (see
// IsRealAWSHost). Never rejects or alters the request — this is a hint, not
// an enforcement point, matching every other check in this family.
//
// cfg is used only to name Overcast's own endpoint in the remediation text
// (the port a client should point AWS_ENDPOINT_URL at instead); logger nil is
// a no-op, matching WithPublishedPort and friends' "nil is safe" convention.
func WarnRealAWSHost(cfg *config.Config, logger *zap.Logger) func(http.Handler) http.Handler {
	var warned atomic.Bool
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if logger != nil && !warned.Load() {
				if host := hostWithoutPort(r.Host); host != "" && IsRealAWSHost(host) && warned.CompareAndSwap(false, true) {
					logger.Warn(realAWSHostWarning(host, cfg), zap.String("host", host))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// realAWSHostWarning builds the message body for WarnRealAWSHost.
func realAWSHostWarning(host string, cfg *config.Config) string {
	port := 0
	if cfg != nil {
		port = cfg.Port
	}
	return fmt.Sprintf(
		"a request arrived addressed to %q — a real AWS hostname — and Overcast answered it. That "+
			"only happens when something on this host (a /etc/hosts entry, a DNS override, or a proxy) "+
			"is redirecting *.amazonaws.com traffic here; Overcast cannot tell whether that's deliberate. "+
			"If it is, ignore this. If it isn't, the client's endpoint is not what you think it is: point "+
			"AWS_ENDPOINT_URL at Overcast explicitly (e.g. http://localhost:%d) instead of relying on the "+
			"redirect, or remove the redirect if you meant to reach real AWS.",
		host, port,
	)
}
