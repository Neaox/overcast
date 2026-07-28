package middleware

import (
	"net/http"
	"regexp"
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
// AWS service that owns it. HostAddressing (hostaddressing.go) reads it to
// classify requests, and detectService (logger.go) resolves the log label from
// the claim that classification produced, so a request's log label can never
// drift from what it was actually dispatched to.
//
// ---- Adding a new host-routed service ----
//
//  1. Check the label cannot plausibly end a bucket name. See the guardrail
//     below — this is a correctness requirement, not style.
//  2. Add its "label" -> service-name entry to hostRouteLabels below.
//  3. In router.go, append one middleware.HostRouteRow{Label: "...", Rewrite: ...}
//     to the rows slice passed to middleware.HostAddressing. Keep Rewrite thin:
//     string manipulation of r.URL.Path (and maybe a region context stamp)
//     only, or a call into one small exported method on the owning service
//     (e.g. apigwSvc.HostRouteRewrite) — never protocol/business logic here.
//
// ---- Guardrail: labels must not be plausible bucket-name segments ----
//
// A bucket named "my.execute-api" is not addressable in the bare
// virtual-hosted form, because "my.execute-api.localhost" parses as an API
// Gateway invoke. That collision surface stays negligible only because every
// registered label is a hyphenated, AWS-specific data-plane token that nobody
// ends a bucket name with. Registering a bare common word ("logs", "events",
// "data") would widen it immediately.
//
// Prefer the AWS data-plane hostname verbatim — "appsync-realtime-api", not
// "realtime". TestReservedHostLabels_areNotPlausibleBucketSuffixes enforces
// the hyphenation rule; docs/plans/host-routing-precedence.md §6 tracks making
// this evidence-based against the generated AWS operation manifest, so the set
// can only grow when AWS itself adds a service or hostname.
//
// ---- Deliberately NOT folded into this table yet (follow-up) ----
//
//   - internal/middleware/region.go's regionFromHost: extracts only the
//     region hint from this same grammar, for SigV4-less requests, and
//     predates this file. It carries a third divergent label list that
//     already disagrees with this one (it knows "sqs"/"sns"/"dynamodb" but
//     not "appsync-api"/"lambda-url"). Folding it in is tracked as H5 in
//     docs/plans/host-routing-precedence.md, alongside the manifest work,
//     so both label lists are replaced in one pass rather than two.
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
// It considers the grammar in isolation. Callers that must also account for S3
// virtual-hosted addressing — i.e. anything on the request path — should use
// HostClassifier.Classify instead, which applies the full precedence rule.
func ParseHostRoute(host string) (HostRouteMatch, bool) {
	hostname := hostWithoutPort(host)
	if hostname == "" || isIPLiteral(hostname) {
		return HostRouteMatch{}, false
	}
	return parseHostRouteName(hostname)
}

// HostRouteServiceFor reports the detectService() label owning a parsed
// host-route match — e.g. "apigateway" for an execute-api Host. detectService
// resolves it from the claim HostAddressing stamped on the request, so a
// request's log label is what actually routed it rather than a re-derivation
// that could disagree.
func HostRouteServiceFor(m HostRouteMatch) (service string, ok bool) {
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

// The middleware that applies these rows is HostAddressing in
// hostaddressing.go. It is not in this file because dispatching a host-routed
// request cannot be decided independently of S3 virtual-hosted addressing —
// attempting that is what produced the double-claim bug documented in
// docs/plans/host-routing-precedence.md.
