package router

import "github.com/go-chi/chi/v5"

// dispatchMount records a sub-router the main router reaches through a runtime
// dispatcher rather than through a chi Mount.
//
// Four path spaces are shared by more than one service and are therefore owned
// by the main router: S3's deliberately broad bucket/object routes, /v2/apis
// (API Gateway v2 and AppSync Events), /v1/tags (AppSync and MSK) and /tags
// (Pipes, EKS, Scheduler and API Gateway). Each is registered as a plain
// HandleFunc that picks a sub-router at request time from the SigV4 credential
// scope or the resource ARN, so chi.Walk stops at the dispatcher and every
// route beneath it is invisible to an inventory taken from the returned
// handler.
//
// That invisibility is not academic: the operations #858 and #860 filed sit
// under exactly these prefixes, and an audit that walked only the top-level
// mux would have reported them as unrouted rather than mis-routed. Recording
// the mounts here is what lets the model-binding gate in
// modelbinding_dev_test.go see the whole served surface.
type dispatchMount struct {
	// Prefix is the path the main router mounts Routes under, without a
	// trailing slash. It is empty for S3, whose routes are absolute.
	Prefix string
	// Owner names the service the sub-router belongs to, so a binding
	// mismatch names a package rather than a path.
	Owner string
	// Fallback marks the sub-router a dispatcher falls back to when no owner
	// claims the request. Only API Gateway's ARN-keyed tag store is one: it
	// answers /tags for every ARN no other service claims, which is how
	// AppRegistry's tag operations are served at all.
	Fallback bool
	Routes   chi.Routes
}

// recordDispatchMount appends a mount when the sub-router exists. Services
// absent from a test subset contribute nothing, which keeps the inventory a
// description of what this router actually serves.
func recordDispatchMount(mounts []dispatchMount, prefix, owner string, routes chi.Routes) []dispatchMount {
	if routes == nil {
		return mounts
	}
	return append(mounts, dispatchMount{Prefix: prefix, Owner: owner, Routes: routes})
}

// recordDispatchFallback is recordDispatchMount for the sub-router a
// dispatcher uses when no owner matches.
func recordDispatchFallback(mounts []dispatchMount, prefix, owner string, routes chi.Routes) []dispatchMount {
	mounts = recordDispatchMount(mounts, prefix, owner, routes)
	if routes != nil {
		mounts[len(mounts)-1].Fallback = true
	}
	return mounts
}
