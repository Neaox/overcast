package appsync_test

// dns_test.go — the GraphqlApi `dns` map.
//
// Real AWS returns the hostnames a client resolves:
//
//	dns.GRAPHQL   = {apiId}.appsync-api.{region}.amazonaws.com
//	dns.REALTIME  = {apiId}.appsync-realtime-api.{region}.amazonaws.com
//
// Overcast hardcoded "amazonaws.com" into both, so it advertised names that
// resolve to real AWS and cannot reach the emulator. The `uris` map follows the
// base it is minted on — see TestGraphqlApi_urisFollowTheBase.
//
// See docs/plans/host-routing-precedence.md §8.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
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

// TestGraphqlApi_urisFollowTheBase is the specification for uris.GRAPHQL.
//
// Real AWS returns the host-routed
// https://{apiId}.appsync-api.{region}.amazonaws.com/graphql, and that is what
// every AWS client — and CloudFormation's Fn::GetAtt GraphQLUrl — expects.
// Overcast used to return the path-style {base}/_overcast/appsync/apis/{apiId}/graphql
// unconditionally, because the host-routed form needs *.localhost to resolve
// and it does not on Windows or macOS (see docs/networking.md).
//
// That reasoning holds only for a bare localhost base. On a wildcard-DNS
// hostname every subdomain resolves, so the path-style form buys nothing and
// costs fidelity. The choice therefore follows the base, not the service:
// serviceutil.SupportsHostRouting decides. Both routes stay served either way.
func TestGraphqlApi_urisFollowTheBase(t *testing.T) {
	// Given: a server on a wildcard-DNS hostname
	srv := helpers.NewTestServer(t, helpers.WithHostname("localhost.overcast.sh"))

	// When: a GraphQL API is created
	apiID, uris := createAPIReadingUris(t, srv, "uris-api")

	// Then: uris carries the canonical AWS grammar on that hostname
	want := "http://" + apiID + ".appsync-api.us-east-1." + hostPort(t, srv) + "/graphql"
	if got := uris["GRAPHQL"]; got != want {
		t.Errorf("uris.GRAPHQL = %q, want %q", got, want)
	}
	wantRT := "ws://" + apiID + ".appsync-api.us-east-1." + hostPort(t, srv) + "/realtime"
	if got := uris["REALTIME"]; got != wantRT {
		t.Errorf("uris.REALTIME = %q, want %q", got, wantRT)
	}
}

// TestGraphqlApi_urisStayPathStyleOnABareBase is the other half of the rule
// above: a base that cannot carry a subdomain keeps the URL that resolves
// there rather than advertising one that does not.
func TestGraphqlApi_urisStayPathStyleOnABareBase(t *testing.T) {
	// Given: a server with no configured hostname, reached on its dial address
	srv := helpers.NewTestServer(t)

	// When: a GraphQL API is created
	apiID, uris := createAPIReadingUris(t, srv, "uris-bare-api")

	// Then: uris stays the always-resolvable path-style form
	want := srv.ExternalBase() + "/_overcast/appsync/apis/" + apiID + "/graphql"
	if got := uris["GRAPHQL"]; got != want {
		t.Errorf("uris.GRAPHQL = %q, want %q", got, want)
	}
}

// TestGraphqlApi_urisGraphQLRoutesBackToTheApi round-trips the advertised URI
// through the router. Asserting its shape alone would pass even if minting and
// routing had drifted apart.
func TestGraphqlApi_urisGraphQLRoutesBackToTheApi(t *testing.T) {
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
			Uris map[string]string `json:"uris"`
		} `json:"graphqlApi"`
	}
	helpers.DecodeJSON(t, getResp, &result)

	// When: the advertised URI's host and path are used verbatim
	u, err := url.Parse(result.GraphqlAPI.Uris["GRAPHQL"])
	if err != nil {
		t.Fatalf("parse uris.GRAPHQL %q: %v", result.GraphqlAPI.Uris["GRAPHQL"], err)
	}
	resp := appsyncPostWithHost(t, srv, u.Path,
		map[string]any{"query": `{ hello }`},
		u.Host,
		map[string]string{"x-api-key": keyID},
	)
	defer resp.Body.Close()

	// Then: the query executes — the URL Overcast advertised is one it serves
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

// TestCreateDomainName_appsyncDomainNameUsesConfiguredHostname covers the
// custom-domain target. Real AWS returns d-{hex}.appsync-api.{region}.amazonaws.com
// — the name a CNAME for the custom domain points at. Overcast hardcoded
// amazonaws.com into it, so it handed back a name that resolves to real AWS.
func TestCreateDomainName_appsyncDomainNameUsesConfiguredHostname(t *testing.T) {
	// Given: a server on a wildcard-DNS hostname
	const hostname = "localhost.overcast.sh"
	srv := helpers.NewTestServer(t, helpers.WithHostname(hostname))

	// When: a custom domain name is created
	resp := appsyncPost(t, srv, "/v1/domainnames", map[string]any{
		"domainName":     "api.example.com",
		"certificateArn": "arn:aws:acm:us-east-1:123456789012:certificate/abc-123",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		DomainNameConfig struct {
			AppsyncDomainName string `json:"appsyncDomainName"`
		} `json:"domainNameConfig"`
	}
	helpers.DecodeJSON(t, resp, &created)
	got := created.DomainNameConfig.AppsyncDomainName

	// Then: the target is on the configured hostname, in the AWS grammar
	if strings.Contains(got, "amazonaws.com") {
		t.Errorf("appsyncDomainName = %q points at real AWS", got)
	}
	if !strings.HasSuffix(got, ".appsync-api.us-east-1."+hostname) {
		t.Errorf("appsyncDomainName = %q does not use the d-{hex}.appsync-api.{region}.{host} grammar", got)
	}
	if !strings.HasPrefix(got, "d-") {
		t.Errorf("appsyncDomainName = %q does not start with the d- prefix AWS uses", got)
	}
}

// createAPIReadingUris creates a GraphQL API and returns its id and uris map.
func createAPIReadingUris(t *testing.T, srv *helpers.TestServer, name string) (string, map[string]string) {
	t.Helper()
	resp := appsyncPost(t, srv, "/v1/apis", map[string]any{
		"name":               name,
		"authenticationType": "API_KEY",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		GraphqlAPI struct {
			ApiID string            `json:"apiId"`
			Uris  map[string]string `json:"uris"`
		} `json:"graphqlApi"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.GraphqlAPI.ApiID, result.GraphqlAPI.Uris
}

// hostPort is the server's external "host:port", the tail every host-routed URL
// minted on this server ends with.
func hostPort(t *testing.T, srv *helpers.TestServer) string {
	t.Helper()
	u, err := url.Parse(srv.ExternalBase())
	if err != nil {
		t.Fatalf("parse external base %q: %v", srv.ExternalBase(), err)
	}
	return u.Host
}
