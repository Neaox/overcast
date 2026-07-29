package apigateway_test

// apiendpoint_test.go — HTTP API v2 apiEndpoint minting.
//
// Real AWS returns an executable invoke endpoint on every v2 API response:
//
//	https://{apiId}.execute-api.{region}.amazonaws.com
//
// Overcast never populated the field at all, so CloudFormation's
// Fn::GetAtt ApiEndpoint resolved to an empty string. See
// docs/plans/host-routing-precedence.md §8.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateV2Api_returnsApiEndpoint(t *testing.T) {
	// Given: a server on a wildcard-DNS hostname
	const hostname = "localhost.overcast.sh"
	srv := helpers.NewTestServer(t, helpers.WithHostname(hostname))

	// When: an HTTP API is created
	resp := apiCall(t, srv, http.MethodPost, "/v2/apis", map[string]any{
		"name":         "endpoint-api",
		"protocolType": "HTTP",
	})
	helpers.AssertStatus(t, resp, http.StatusCreated)
	var api struct {
		ApiID       string `json:"apiId"`
		ApiEndpoint string `json:"apiEndpoint"`
	}
	helpers.DecodeJSON(t, resp, &api)

	// Then: apiEndpoint carries the canonical execute-api grammar on the
	// configured hostname, not an empty string and not amazonaws.com
	if api.ApiEndpoint == "" {
		t.Fatal("apiEndpoint is empty; real AWS always returns an invoke endpoint")
	}
	if !strings.Contains(api.ApiEndpoint, api.ApiID+".execute-api.") {
		t.Errorf("apiEndpoint %q does not use the {apiId}.execute-api grammar", api.ApiEndpoint)
	}
	if !strings.Contains(api.ApiEndpoint, hostname) {
		t.Errorf("apiEndpoint %q does not use the configured hostname %q", api.ApiEndpoint, hostname)
	}
	if strings.Contains(api.ApiEndpoint, "amazonaws.com") {
		t.Errorf("apiEndpoint %q points at real AWS", api.ApiEndpoint)
	}
}

func TestGetV2Api_apiEndpointRoutesBackToTheApi(t *testing.T) {
	// Given: a deployed HTTP API on the $default stage
	srv := helpers.NewTestServer(t, helpers.WithHostname("localhost.overcast.sh"))
	apiID := createV2API(t, srv, "endpoint-roundtrip")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	integResp := apiCall(t, srv, http.MethodPost, "/v2/apis/"+apiID+"/integrations", map[string]any{
		"integrationType":   "HTTP_PROXY",
		"integrationUri":    upstream.URL,
		"integrationMethod": "GET",
	})
	helpers.AssertStatus(t, integResp, http.StatusCreated)
	var integ map[string]any
	helpers.DecodeJSON(t, integResp, &integ)
	createV2RouteWithTarget(t, srv, apiID, "GET /ping", "integrations/"+integ["integrationId"].(string))
	createV2Stage(t, srv, apiID, "$default")

	// When: the apiEndpoint the API reports is fed back as a Host header.
	// Asserting the string shape alone would pass even if minting and routing
	// had drifted apart, so this round-trips it through the router.
	getResp := apiCall(t, srv, http.MethodGet, "/v2/apis/"+apiID, nil)
	helpers.AssertStatus(t, getResp, http.StatusOK)
	var api struct {
		ApiEndpoint string `json:"apiEndpoint"`
	}
	helpers.DecodeJSON(t, getResp, &api)

	u, err := url.Parse(api.ApiEndpoint)
	if err != nil {
		t.Fatalf("parse apiEndpoint %q: %v", api.ApiEndpoint, err)
	}
	resp := apiCallWithHost(t, srv, http.MethodGet, "/ping", u.Host, nil)

	// Then: it reaches the integration — the URL Overcast advertised is one
	// Overcast can serve
	helpers.AssertStatus(t, resp, http.StatusOK)
}
