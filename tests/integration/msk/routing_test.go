// Routing tests for the ARN-in-path MSK subtrees.
//
// Every MSK ARN is a non-greedy httpLabel, so an AWS client percent-encodes it
// whole into one path segment and any sub-resource follows it as a further
// segment. Captured from the AWS CLI (aws-cli/2.36.18) against a listener that
// prints the request line:
//
//	GET /v1/clusters/arn%3Aaws%3Akafka%3A…%2Fprobe%2F1111…-5/nodes
//	GET /api/v2/clusters/arn%3Aaws%3Akafka%3A…%2Fprobe%2F1111…-5/operations
//	GET /v1/configurations/arn%3Aaws%3Akafka%3A…%2Fprobe%2F9999/revisions
//
// Overcast implements none of those three, so each has to reach the generated
// 501. Neither did before this file: the v1 subtree wildcards absorbed the
// sub-resource segment into the ARN and answered "cluster not found" for a
// cluster nobody asked about, and the v2 label matched nothing at all, so the
// request fell past MSK into S3's wildcard and came back as <NoSuchKey/>.
//
// Run: go test ./tests/integration/msk/...
package msk_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Neaox/overcast/tests/helpers"
)

// mskSignedRequest sends a REST JSON request the way an SDK sends one: with a
// SigV4 credential scope naming MSK's signing name, "kafka".
//
// The scope is load-bearing for anything that reaches the router's REST
// fallback. addressesNonS3 treats unsigned traffic as S3's, so an unsigned
// request to an unrouted path goes to S3's bucket/object wildcard by design;
// only a request that names a non-S3 service can be answered with the modeled
// 501. mskRequest, which the rest of the suite uses, is unsigned and therefore
// cannot see the difference these tests are about.
func mskSignedRequest(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20260815/us-east-1/kafka/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// labelPath renders the URI an AWS client puts on the wire: the ARN
// percent-encoded whole into one segment, with any sub-resource after it.
func labelPath(collection, arn, subResource string) string {
	return collection + "/" + url.PathEscape(arn) + subResource
}

// assertNotImplemented asserts the protocol-correct restJson1 501 — not a 404
// invented for a resource the caller never named, and not an S3 error body.
func assertNotImplemented(t *testing.T, resp *http.Response, path string) {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotImplemented, resp.StatusCode,
		"%s answered %d, body: %s", path, resp.StatusCode, raw)
	var errResp struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(raw, &errResp), "body: %s", raw)
	assert.Equal(t, "NotImplemented", errResp.Type, "body: %s", raw)
}

// createTestCluster creates a cluster and returns its ARN.
func createTestCluster(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         name,
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created struct {
		ClusterArn string `json:"clusterArn"`
	}
	decodeJSON(t, resp, &created)
	require.NotEmpty(t, created.ClusterArn)
	return created.ClusterArn
}

// createTestConfiguration creates a configuration and returns its ARN.
func createTestConfiguration(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := mskRequest(t, srv, http.MethodPost, "/v1/configurations", map[string]any{
		"name":             name,
		"kafkaVersions":    []string{"3.5.1"},
		"serverProperties": "YXV0by5jcmVhdGUudG9waWNzLmVuYWJsZSA9IHRydWU=",
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		Arn string `json:"arn"`
	}
	decodeJSON(t, resp, &created)
	require.NotEmpty(t, created.Arn)
	return created.Arn
}

// ── The defect: sub-resources absorbed into the ARN ───────────────────────────

// The v1 cluster subtree is served by three wildcard dispatch handlers that
// took the whole tail as the ARN. A sub-resource segment beneath the ARN was
// therefore swallowed into it, and the caller was told the cluster
// "arn:…:cluster/probe/<uuid>/nodes" does not exist — a plausible answer to a
// question nobody asked, which is exactly what the v2 route comment was written
// to avoid. None of ListNodes, ListClusterOperations, GetClusterPolicy or
// ListScramSecrets is emulated, so each has to reach the generated 501.
func TestClusterSubResourcesV1_areNotAbsorbedIntoTheARN(t *testing.T) {
	// Given: a cluster
	srv := helpers.NewTestServer(t)
	arn := createTestCluster(t, srv, "routing-v1")

	// When: a modeled sub-resource beneath the cluster ARN is requested
	for _, sub := range []string{"/nodes", "/operations", "/policy", "/scram-secrets"} {
		t.Run(sub, func(t *testing.T) {
			path := labelPath("/v1/clusters", arn, sub)
			resp := mskSignedRequest(t, srv, http.MethodGet, path, nil)
			defer resp.Body.Close()

			// Then: the protocol-correct not-implemented answer
			assertNotImplemented(t, resp, path)
		})
	}
}

// The same fault on the write methods: DELETE /v1/clusters/{arn}/policy is
// DeleteClusterPolicy and PUT /v1/clusters/{arn}/version is
// UpdateClusterKafkaVersion, neither emulated. Delete answered "cluster not
// found" for an ARN ending in "/policy"; Put already answered 501, and is
// asserted here so the dispatch rewrite keeps it that way.
func TestClusterSubResourcesV1_writeMethods(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createTestCluster(t, srv, "routing-v1-writes")

	for _, tc := range []struct{ method, sub string }{
		{http.MethodDelete, "/policy"},
		{http.MethodPut, "/version"},
		{http.MethodPut, "/nodes/count"},
	} {
		t.Run(tc.method+tc.sub, func(t *testing.T) {
			path := labelPath("/v1/clusters", arn, tc.sub)
			resp := mskSignedRequest(t, srv, tc.method, path, map[string]any{})
			defer resp.Body.Close()

			assertNotImplemented(t, resp, path)
		})
	}
}

// ListClusterOperationsV2 is bound to GET /api/v2/clusters/{clusterArn}/operations.
// The v2 label deliberately matches one segment, so the request matched no MSK
// route and reached the router's REST fallback — which classified it against
// the *decoded* path. Every MSK ARN carries %2F-encoded slashes, so the decoded
// path has four more segments than the binding, the generated trie never
// matched, and S3's wildcard answered an MSK call with
// <Error><Code>NoSuchKey</Code>…</Error> even for a correctly signed client.
func TestClusterSubResourcesV2_doNotFallThroughToS3(t *testing.T) {
	// Given: a cluster
	srv := helpers.NewTestServer(t)
	arn := createTestCluster(t, srv, "routing-v2")

	// When: ListClusterOperationsV2 is called at the binding AWS gives it
	path := labelPath("/api/v2/clusters", arn, "/operations")
	resp := mskSignedRequest(t, srv, http.MethodGet, path, nil)
	defer resp.Body.Close()

	// Then: MSK answers with the modeled 501, not S3 with an object error
	assertNotImplemented(t, resp, path)
}

// The sub-resource paths MSK registers a route for must answer as MSK whether
// or not the caller signed.
//
// This is the half of #963 that matters most and the half a signed probe cannot
// see. The router's REST fallback only classifies traffic that positively
// addresses a non-S3 service — unsigned traffic is S3's by design — so a fix
// that rests on the fallback alone still leaves `aws --no-sign-request` reading
// an S3 <NoSuchKey/> for an MSK call, which is the answer a caller can mistake
// for something other than "not emulated". The `cli` compat suite calls MSK
// unsigned, so this is also the shape its assertions see.
func TestClusterSubResources_answerAsMSKWhenUnsigned(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createTestCluster(t, srv, "routing-unsigned")
	configArn := createTestConfiguration(t, srv, "routing-unsigned-config")

	for _, path := range []string{
		labelPath("/v1/clusters", arn, "/nodes"),
		labelPath("/v1/clusters", arn, "/operations"),
		labelPath("/api/v2/clusters", arn, "/operations"),
		labelPath("/v1/configurations", configArn, "/revisions"),
	} {
		t.Run(path, func(t *testing.T) {
			resp := mskRequest(t, srv, http.MethodGet, path, nil)
			defer resp.Body.Close()

			assertNotImplemented(t, resp, path)
		})
	}
}

// ListConfigurationRevisions and DescribeConfigurationRevision hang off the
// configuration ARN the same way, and the configuration subtree uses the same
// wildcard, so it had the same fault.
func TestConfigurationSubResources_areNotAbsorbedIntoTheARN(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := createTestConfiguration(t, srv, "routing-config")

	for _, sub := range []string{"/revisions", "/revisions/1"} {
		t.Run(sub, func(t *testing.T) {
			path := labelPath("/v1/configurations", arn, sub)
			resp := mskSignedRequest(t, srv, http.MethodGet, path, nil)
			defer resp.Body.Close()

			assertNotImplemented(t, resp, path)
		})
	}
}

// ── Guards ────────────────────────────────────────────────────────────────────

// Narrowing what the wildcards claim must not cost the fallback. MSK emulates
// seventeen of the sixty-odd operations AWS binds to kafka, and the rest have
// to keep reaching a protocol-correct 501 rather than being swallowed by a
// neighbouring route or falling through to S3. These four are the shapes that
// bracket the change: a collection with no ARN at all, a collection under a
// prefix MSK does not register, an ARN-labelled item route on a different
// collection, and a method MSK registers no handler for on a path it owns.
func TestUnimplementedNeighbours_stillReachTheGenerated501(t *testing.T) {
	srv := helpers.NewTestServer(t)
	configArn := createTestConfiguration(t, srv, "neighbour-config")
	opArn := "arn:aws:kafka:us-east-1:000000000000:cluster-operation/probe/1111"

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"GetCompatibleKafkaVersions", http.MethodGet, "/v1/compatible-kafka-versions"},
		{"ListVpcConnections", http.MethodGet, "/v1/vpc-connections"},
		{"ListReplicators", http.MethodGet, "/replication/v1/replicators"},
		{"DescribeClusterOperation", http.MethodGet, labelPath("/v1/operations", opArn, "")},
		{"UpdateConfiguration", http.MethodPut, labelPath("/v1/configurations", configArn, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := mskSignedRequest(t, srv, tc.method, tc.path, nil)
			defer resp.Body.Close()

			assertNotImplemented(t, resp, tc.path)
		})
	}
}

// The operations MSK does emulate on an ARN-in-path route must keep answering,
// at both spellings of the ARN. The percent-encoded single segment is what
// every AWS client sends; the literal-slash form is what CloudFormation's
// provisioner and the rest of this suite have always sent, and the wildcard
// routes exist to keep serving it.
func TestARNInPathRoutes_answerBothSpellings(t *testing.T) {
	srv := helpers.NewTestServer(t)
	configArn := createTestConfiguration(t, srv, "both-spellings-config")

	cases := []struct {
		name   string
		method string
		render func(arn string) string
		body   any
		status int
	}{
		{"DescribeCluster", http.MethodGet, func(a string) string { return "/v1/clusters/" + a }, nil, http.StatusOK},
		{"GetBootstrapBrokers", http.MethodGet, func(a string) string { return "/v1/clusters/" + a + "/bootstrap-brokers" }, nil, http.StatusOK},
		{"UpdateClusterConfiguration", http.MethodPut, func(a string) string { return "/v1/clusters/" + a + "/configuration" }, map[string]any{
			"configurationInfo": map[string]any{"arn": configArn, "revision": 1},
		}, http.StatusOK},
		{"DeleteCluster", http.MethodDelete, func(a string) string { return "/v1/clusters/" + a }, nil, http.StatusOK},
	}

	for _, tc := range cases {
		for _, spelling := range []string{"escaped", "literal"} {
			t.Run(tc.name+"/"+spelling, func(t *testing.T) {
				// Given: a cluster of its own, since one case deletes it
				arn := createTestCluster(t, srv, strings.ToLower(tc.name)+"-"+spelling)
				addressed := arn
				if spelling == "escaped" {
					addressed = url.PathEscape(arn)
				}

				// When: the operation is called at that spelling
				path := tc.render(addressed)
				resp := mskSignedRequest(t, srv, tc.method, path, tc.body)
				defer resp.Body.Close()

				// Then: MSK serves it
				raw, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, tc.status, resp.StatusCode, "%s %s, body: %s", tc.method, path, raw)
			})
		}
	}

	// And: DescribeConfiguration and DeleteConfiguration, on the other subtree
	for _, spelling := range []string{"escaped", "literal"} {
		t.Run("DescribeConfiguration/"+spelling, func(t *testing.T) {
			addressed := configArn
			if spelling == "escaped" {
				addressed = url.PathEscape(configArn)
			}
			path := "/v1/configurations/" + addressed
			resp := mskSignedRequest(t, srv, http.MethodGet, path, nil)
			defer resp.Body.Close()

			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode, "GET %s, body: %s", path, raw)
		})
	}
}
