package lambda_test

// request_id_test.go — the request-ID header on *successful* responses.
//
// Every AWS response carries a request ID, and AGENTS.md says so: "501
// responses get x-emulator-unsupported: true and all responses get a request ID
// — both automatically". Lambda's success surface did not. Its handlers encoded
// JSON straight onto the ResponseWriter instead of going through a
// protocol.Write* helper, and the header is the helper's job (see
// middleware.RequestID, which deliberately sets none itself).
//
// Nothing caught it because every request-ID assertion in this suite was on an
// error response — and errors *do* go through a helper. So the gap was invisible
// exactly where it was widest.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// TestLambdaSuccessResponses_carryARequestID walks one successful response per
// operation family. A request ID is what an SDK surfaces as ResponseMetadata
// and what a support-style trace correlates on; a caller cannot ask for it
// after the fact.
func TestLambdaSuccessResponses_carryARequestID(t *testing.T) {
	// Given: a function, a version, an alias and a layer to read back.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "request-id-fn")

	publish := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/request-id-fn/versions"), map[string]any{})
	helpers.AssertStatus(t, publish, http.StatusCreated)
	publish.Body.Close()

	alias := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/request-id-fn/aliases"), map[string]any{
		"Name": "live", "FunctionVersion": "1",
	})
	helpers.AssertStatus(t, alias, http.StatusCreated)
	alias.Body.Close()

	// When: each family's read operation is called.
	// Paths are absolute, not lambdaURL-relative: Lambda's operations are spread
	// across nine dated bases, and only the /2015-03-31 ones are the function
	// API. Getting one wrong falls through to S3's catch-all rather than 404ing
	// from Lambda, which is its own kind of loud.
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		status int
	}{
		{"CreateFunction", http.MethodPost, "/functions", map[string]any{
			"FunctionName": "request-id-created", "Runtime": "python3.12", "Handler": "index.handler",
			"Role": "arn:aws:iam::000000000000:role/lambda-role",
			"Code": map[string]any{"ZipFile": "UEsFBgAAAAAAAAAAAAAAAAAAAAAAAA=="},
		}, http.StatusCreated},
		{"GetFunction", http.MethodGet, "/functions/request-id-fn", nil, http.StatusOK},
		{"GetFunctionConfiguration", http.MethodGet, "/functions/request-id-fn/configuration", nil, http.StatusOK},
		{"ListFunctions", http.MethodGet, "/functions", nil, http.StatusOK},
		{"UpdateFunctionConfiguration", http.MethodPut, "/functions/request-id-fn/configuration",
			map[string]any{"Description": "updated"}, http.StatusOK},
		{"ListVersionsByFunction", http.MethodGet, "/functions/request-id-fn/versions", nil, http.StatusOK},
		{"GetAlias", http.MethodGet, "/functions/request-id-fn/aliases/live", nil, http.StatusOK},
		{"ListAliases", http.MethodGet, "/functions/request-id-fn/aliases", nil, http.StatusOK},
		{"GetPolicy", http.MethodGet, "/functions/request-id-fn/policy", nil, http.StatusNotFound},
		{"ListEventSourceMappings", http.MethodGet, "/event-source-mappings", nil, http.StatusOK},
		{"ListLayers", http.MethodGet, "/2018-10-31/layers", nil, http.StatusOK},
		{"ListCodeSigningConfigs", http.MethodGet, "/2020-04-22/code-signing-configs", nil, http.StatusOK},
		{"GetFunctionConcurrency", http.MethodGet, "/2019-09-30/functions/request-id-fn/concurrency", nil, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, tc.method, srv.URL+lambdaPath(tc.path), tc.body)
			defer resp.Body.Close()

			// Then: it answers as expected, and reports its request ID.
			helpers.AssertStatus(t, resp, tc.status)
			helpers.AssertRequestID(t, resp)
		})
	}
}

// TestLambdaSuccessResponses_requestIDEchoesTheOneTheCallerPinned proves the
// header carries the invocation's real ID rather than a fresh one minted at
// write time — the property that makes it usable for correlation at all.
// middleware.RequestID honours a caller-supplied x-amzn-requestid so internal
// dispatch can pin a child call's ID; the response has to echo that same value.
func TestLambdaSuccessResponses_requestIDEchoesTheOneTheCallerPinned(t *testing.T) {
	// Given: a function, and a request carrying a known ID.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "request-id-echo-fn")
	const pinned = "11111111-2222-3333-4444-555555555555"

	req, err := http.NewRequest(http.MethodGet, lambdaURL(srv, "/functions/request-id-echo-fn/configuration"), nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("x-amzn-requestid", pinned)

	// When: it is sent.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then: the response reports that ID, not a different one.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("x-amzn-requestid"); got != pinned {
		t.Errorf("x-amzn-requestid = %q, want the pinned %q", got, pinned)
	}
}

// lambdaPath resolves a case's path against the function API's base unless it
// already names one of Lambda's other dated bases.
func lambdaPath(path string) string {
	if strings.HasPrefix(path, "/20") {
		return path
	}
	return "/2015-03-31" + path
}
