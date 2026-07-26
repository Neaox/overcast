package middleware

import (
	"net"
	"net/http"
	"regexp"
	"strings"
)

// hostroute.go implements ONE general grammar + dispatch table for AWS
// services whose real invoke/control endpoints encode a resource ID (and
// usually a region) as a Host subdomain rather than in the request path:
//
//	{id}.{label}[.{region}].{base}[:port]
//
// Real examples:
//
//	myapi123.execute-api.us-east-1.amazonaws.com   (API Gateway invoke, v1+v2)
//	{url-id}.lambda-url.us-east-1.on.aws           (Lambda function URLs)
//	myapi456.appsync-api.us-east-1.amazonaws.com   (AppSync GraphQL)
//
// ParseHostRoute recognises this grammar against the fixed set of known
// `label` tokens in hostRouteLabels and returns the {id} (everything before
// the first recognised label — dot-joined, so IDs that themselves contain
// dots are supported) and {region} (the segment immediately after the label,
// only when it looks like an AWS region — otherwise region is "" and that
// segment is just the start of {base}).
//
// hostRouteLabels is the single source of truth mapping a label token to the
// AWS service that owns it. Both HostDispatch (the rewrite middleware,
// instantiated per-router in internal/router/router.go because its rows
// close over service instances) and HostRouteService (the pure lookup used
// by detectService in logger.go) read this same map, so a request's log
// label can never drift from what it was actually dispatched to.
//
// ---- Adding a new host-routed service ----
//
//  1. Add its "label" -> service-name entry to hostRouteLabels below.
//  2. In router.go, append one middleware.HostRouteRow{Label: "...", Rewrite: ...}
//     to the rows slice passed to middleware.HostDispatch. Keep Rewrite thin:
//     string manipulation of r.URL.Path (and maybe a region context stamp)
//     only, or a call into one small exported method on the owning service
//     (e.g. apigwSvc.HostRouteRewrite) — never protocol/business logic here.
//
// That's the whole cost of a new row: one label + one table entry.
//
// ---- Deliberately NOT folded into this table yet (follow-ups) ----
//
//   - S3 virtual-hosted addressing (internal/middleware/s3virtualhost.go):
//     `{bucket}.s3[.{region}].{base}` and the bucket-with-no-label form
//     (`{bucket}.{OVERCAST_HOSTNAME}`). It predates this table and is under
//     active development on a parallel branch
//     (feat/s3-virtual-hosted-addressing); folding it in here now would
//     collide with that work. It already fits the grammar (S3 is a fourth
//     row: label "s3", region optional) — migrating it in is a follow-up
//     once both branches have landed.
//   - internal/middleware/region.go's regionFromHost: extracts only the
//     region hint from this same grammar, for SigV4-less requests, and
//     predates this file. It could delegate to ParseHostRoute, but region.go
//     is also touched by the S3 branch (it lists "s3" as a recognised
//     label); merging the two is deferred to the same follow-up pass so
//     this branch's diff stays out of that file.
var hostRouteLabels = map[string]string{
	"execute-api": "apigateway",
	"lambda-url":  "lambda",
	"appsync-api": "appsync",
}

// awsRegionPattern matches AWS region shapes: us-east-1, ap-southeast-2,
// us-gov-west-1, us-iso-east-1, etc. Used to decide whether the host segment
// right after the label is a region or already the start of {base}.
var awsRegionPattern = regexp.MustCompile(`^[a-z]{2}(-gov|-iso[a-z]*)?-[a-z]+-\d$`)

// HostRouteMatch is a successfully parsed Host-based AWS endpoint address.
type HostRouteMatch struct {
	// Label is the recognised host segment, e.g. "execute-api". Always a key
	// of hostRouteLabels.
	Label string
	// ID is the subdomain segment(s) before Label, dot-joined.
	ID string
	// Region is the AWS region parsed from the segment after Label, or ""
	// if that segment doesn't look like a region (e.g. the base hostname
	// starts right after the label, as with a bare "localhost" base).
	Region string
}

// ParseHostRoute parses host (which may include a port) against the AWS
// `{id}.{label}[.{region}].{base}` grammar using the labels registered in
// hostRouteLabels. Returns ok=false for path-style requests, IP literals, or
// hosts that don't contain a registered label.
func ParseHostRoute(host string) (HostRouteMatch, bool) {
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	if hostname == "" {
		return HostRouteMatch{}, false
	}
	// IP literals never carry a host-routed subdomain.
	if net.ParseIP(hostname) != nil {
		return HostRouteMatch{}, false
	}

	parts := strings.Split(hostname, ".")
	for i, p := range parts {
		// The label can't be the very first segment — {id} must be
		// non-empty, matching every real AWS host-routed shape.
		if i == 0 {
			continue
		}
		if _, ok := hostRouteLabels[p]; !ok {
			continue
		}
		region := ""
		if i+1 < len(parts) && awsRegionPattern.MatchString(parts[i+1]) {
			region = parts[i+1]
		}
		return HostRouteMatch{Label: p, ID: strings.Join(parts[:i], "."), Region: region}, true
	}
	return HostRouteMatch{}, false
}

// HostRouteService reports the detectService() label for a Host header that
// matches the host-route grammar (see ParseHostRoute) — e.g. "apigateway"
// for an execute-api Host. detectService (logger.go) calls this so a
// request's log label always matches what HostDispatch actually routed it
// to; there is no separate/parallel suffix list to keep in sync.
func HostRouteService(host string) (service string, ok bool) {
	m, matched := ParseHostRoute(host)
	if !matched {
		return "", false
	}
	service, ok = hostRouteLabels[m.Label]
	return service, ok
}

// HostRouteRow binds one recognised label to the rewrite that adapts a
// Host-routed request into the owning service's existing path-style route.
// See the package doc above for the recipe to add a new row.
type HostRouteRow struct {
	// Label must be a key of hostRouteLabels.
	Label string
	// Rewrite mutates r (typically r.URL.Path/RawPath, and optionally the
	// request context, e.g. to stamp a region hint) in place so the request
	// matches a route already registered for the owning service. Called
	// once, synchronously, before chi's router dispatches on the (possibly
	// now-different) path. Rewrite should always mutate on a recognised ID
	// — even one that turns out not to exist — and let the owning service's
	// own handler produce the AWS-shaped not-found/forbidden error; only a
	// Host that doesn't match the grammar at all should fall through
	// untouched (see AGENTS.md "Routing fallthrough is S3").
	Rewrite func(r *http.Request, m HostRouteMatch)
}

// HostDispatch returns middleware that recognises Host-routed AWS-style
// addresses (see ParseHostRoute) and applies the matching row's Rewrite,
// before chi's router runs. rows is read on every request via the pointer,
// so callers can declare it early in the middleware chain (chi requires all
// r.Use calls before any route registration) and populate it later once the
// owning services exist — the same pattern already used for this router's
// query-dispatcher and event-bus wiring.
//
// Hosts that don't match any row — including S3's own virtual-hosted forms,
// handled separately by S3VirtualHostFor — pass through unchanged, which is
// what lets unknown/foreign hosts fall through to the S3 catch-all per
// AGENTS.md "Routing fallthrough is S3" instead of 404ing here.
func HostDispatch(rows *[]HostRouteRow) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if m, ok := ParseHostRoute(r.Host); ok {
				for _, row := range *rows {
					if row.Label == m.Label {
						row.Rewrite(r, m)
						break
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
