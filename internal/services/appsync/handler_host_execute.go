package appsync

// handler_host_execute.go — Host-based GraphQL routing.
//
// Real AWS serves AppSync GraphQL at
// {apiId}.appsync-api.{region}.amazonaws.com/graphql. The emulator already
// exposes the same execution logic at the path-style /_appsync/{apiId}/graphql
// (see service.go), so the Host-routed form only needs a path rewrite — no
// store lookups, no ambiguity to resolve (unlike API Gateway's execute-api,
// an AppSync apiId always means exactly one thing: a GraphQL API).
//
// Real AWS also serves realtime WebSocket traffic on a distinct
// appsync-realtime-api host. Overcast colocates both under the same
// /_appsync/{apiId} prefix (see service.go's /realtime route), so this same
// rewrite transparently also makes host-routed realtime connections work —
// a separate "appsync-realtime-api" label/row is not needed.

import (
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/middleware"
)

// HostRouteRewrite adapts a Host-routed AppSync request
// ({apiId}.appsync-api.{region}.{base}/graphql) to the emulator's existing
// /_appsync/{apiId}/graphql path-style route.
func (s *Service) HostRouteRewrite(r *http.Request, m middleware.HostRouteMatch) {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	r.URL.Path = "/_appsync/" + m.ID + path
	if r.URL.RawPath != "" {
		r.URL.RawPath = r.URL.Path
	}
}
