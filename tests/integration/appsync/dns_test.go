package appsync_test

// dns_test.go — the GraphqlApi `dns` map.
//
// Real AWS returns the hostnames a client resolves:
//
//	dns.GRAPHQL   = {apiId}.appsync-api.{region}.amazonaws.com
//	dns.REALTIME  = {apiId}.appsync-realtime-api.{region}.amazonaws.com
//
// Overcast hardcoded "amazonaws.com" into both, so it advertised names that
// resolve to real AWS and cannot reach the emulator. The `uris` map stays
// path-style deliberately — see the note on TestGraphqlApi_urisStayPathStyle.
//
// See docs/plans/host-routing-precedence.md §8.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateGraphqlApi_dnsUsesConfiguredHostname(t *testing.T) {
	// Given: a server on a wildcard-DNS hostname
	const hostname = "localhost.overcast.sh"
	srv := helpers.NewTestServer(t, helpers.WithHostname(hostname))

	// When: a GraphQL API is created
	resp := appsyncPost(t, srv, "/v1/apis", map[string]any{
		"name":               "dns-api",
		"authenticationType": "API_KEY",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		GraphqlAPI struct {
			ApiID string            `json:"apiId"`
			Dns   map[string]string `json:"dns"`
		} `json:"graphqlApi"`
	}
	helpers.DecodeJSON(t, resp, &result)
	api := result.GraphqlAPI

	// Then: the DNS names are on the configured hostname, not amazonaws.com
	if got := api.Dns["GRAPHQL"]; !strings.HasSuffix(got, hostname) {
		t.Errorf("dns.GRAPHQL = %q, want a name on %q", got, hostname)
	}
	if got := api.Dns["GRAPHQL"]; strings.Contains(got, "amazonaws.com") {
		t.Errorf("dns.GRAPHQL = %q points at real AWS", got)
	}
	if want := api.ApiID + ".appsync-api."; !strings.HasPrefix(api.Dns["GRAPHQL"], want) {
		t.Errorf("dns.GRAPHQL = %q does not use the {apiId}.appsync-api grammar", api.Dns["GRAPHQL"])
	}
	if got := api.Dns["REALTIME"]; strings.Contains(got, "amazonaws.com") {
		t.Errorf("dns.REALTIME = %q points at real AWS", got)
	}
}

func TestGraphqlApi_dnsGraphQLHostRoutesBackToTheApi(t *testing.T) {
	// Given: a GraphQL API with a resolver, on a wildcard-DNS hostname
	srv := helpers.NewTestServer(t, helpers.WithHostname("localhost.overcast.sh"))
	apiID, keyID := setupGraphQLAPI(t, srv, `type Query { hello: String }`)

	appsyncPost(t, srv, "/v1/apis/"+apiID+"/datasources", map[string]any{
		"name": "NoneDS",
		"type": "NONE",
	}).Body.Close()
	appsyncPost(t, srv, "/v1/apis/"+apiID+"/types/Query/resolvers", map[string]any{
		"fieldName":               "hello",
		"dataSourceName":          "NoneDS",
		"kind":                    "UNIT",
		"requestMappingTemplate":  `{"version":"2018-05-29","payload":"world"}`,
		"responseMappingTemplate": `$util.toJson($context.result)`,
	}).Body.Close()

	getResp := appsyncGet(t, srv, "/v1/apis/"+apiID)
	defer getResp.Body.Close()
	var result struct {
		GraphqlAPI struct {
			Dns map[string]string `json:"dns"`
		} `json:"graphqlApi"`
	}
	helpers.DecodeJSON(t, getResp, &result)

	// When: the advertised DNS name is used as the Host. Asserting its shape
	// alone would pass even if minting and routing had drifted apart, so this
	// round-trips it through the router.
	host := result.GraphqlAPI.Dns["GRAPHQL"]
	if host == "" {
		t.Fatal("dns.GRAPHQL is empty")
	}
	resp := appsyncPostWithHost(t, srv, "/graphql",
		map[string]any{"query": `{ hello }`},
		host+":4566",
		map[string]string{"x-api-key": keyID},
	)
	defer resp.Body.Close()

	// Then: the query executes — the name Overcast advertised is one it serves
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Data struct {
			Hello string `json:"hello"`
		} `json:"data"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.Data.Hello != "world" {
		t.Errorf("data.hello = %q, want %q", out.Data.Hello, "world")
	}
}

// TestGraphqlApi_urisStayPathStyle pins a deliberate divergence from AWS.
//
// Real AWS returns uris.GRAPHQL as the host-routed
// https://{apiId}.appsync-api.{region}.amazonaws.com/graphql. Overcast keeps
// the path-style {base}/_appsync/{apiId}/graphql because that URL resolves
// with no DNS setup at all, while the host-routed form needs *.localhost to
// resolve — which it does not on Windows by default (see docs/networking.md).
// Advertising a URL the caller may be unable to resolve would be worse
// fidelity in practice than the shape difference.
//
// The host-routed form is fully supported for callers who want it; `dns`
// carries the name for exactly that purpose, and the tests above prove it
// routes.
func TestGraphqlApi_urisStayPathStyle(t *testing.T) {
	// Given: a server on a wildcard-DNS hostname
	srv := helpers.NewTestServer(t, helpers.WithHostname("localhost.overcast.sh"))

	// When: a GraphQL API is created
	resp := appsyncPost(t, srv, "/v1/apis", map[string]any{
		"name":               "uris-api",
		"authenticationType": "API_KEY",
	})
	defer resp.Body.Close()
	var result struct {
		GraphqlAPI struct {
			ApiID string            `json:"apiId"`
			Uris  map[string]string `json:"uris"`
		} `json:"graphqlApi"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: uris stays the always-resolvable path-style form
	want := srv.ExternalBase() + "/_appsync/" + result.GraphqlAPI.ApiID + "/graphql"
	if got := result.GraphqlAPI.Uris["GRAPHQL"]; got != want {
		t.Errorf("uris.GRAPHQL = %q, want %q", got, want)
	}
}
