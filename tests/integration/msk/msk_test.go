// Package msk_test contains integration tests for the MSK emulator.
//
// Run: go test ./tests/integration/msk/...
package msk_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Neaox/overcast/tests/helpers"
)

// mskRequest sends a REST JSON request to the MSK emulator.
func mskRequest(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, bodyReader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, dst), "body: %s", b)
}

func assertJSONError(t *testing.T, resp *http.Response, expectedCode string) {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var errResp struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(b, &errResp), "body: %s", b)
	assert.Equal(t, expectedCode, errResp.Type)
}

// ── TestCreateCluster ──────────────────────────────────────────────────────────

func TestCreateCluster_success(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a valid cluster creation request is made
	resp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "test-cluster",
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
	})

	// Then: the response is 200 with CREATING state and a populated ARN
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, "test-cluster", result["clusterName"])
	assert.Equal(t, "CREATING", result["state"])
	arn, _ := result["clusterArn"].(string)
	assert.Contains(t, arn, "arn:aws:kafka:")
	assert.Contains(t, arn, "test-cluster")
}

func TestCreateCluster_missingName(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a cluster creation request is made without a name
	resp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"kafkaVersion": "3.5.1",
	})

	// Then: the response is 400 BadRequestException — the kafka model declares
	// no ValidationException on any operation
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "BadRequestException")
}

// ── TestDescribeCluster ───────────────────────────────────────────────────────

func TestDescribeCluster_success(t *testing.T) {
	// Given: a cluster has been created
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "describe-test",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: the cluster is described
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/"+arn, nil)

	// Then: the response is 200 with the cluster info
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	assert.Equal(t, "describe-test", info["clusterName"])
	assert.Equal(t, arn, info["clusterArn"])
}

// kafkaVersionOf reads the one member of the v1 ClusterInfo shape that carries
// the cluster's Kafka version. AWS binds no top-level kafkaVersion on
// ClusterInfo, so currentBrokerSoftwareInfo.kafkaVersion is the only place an
// SDK client can read back what it provisioned.
func kafkaVersionOf(t *testing.T, info map[string]any) string {
	t.Helper()
	software, ok := info["currentBrokerSoftwareInfo"].(map[string]any)
	require.True(t, ok, "currentBrokerSoftwareInfo missing from clusterInfo: %v", info)
	version, _ := software["kafkaVersion"].(string)
	return version
}

func TestDescribeCluster_reportsTheKafkaVersionItWasCreatedWith(t *testing.T) {
	// Given: a cluster created with an explicit Kafka version
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "version-readback",
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: the cluster is described
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/"+arn, nil)

	// Then: the version comes back where the model puts it. A create the caller
	// cannot verify through the matching describe is the #980 shape — accepted,
	// stored, and unreadable.
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	assert.Equal(t, "3.5.1", kafkaVersionOf(t, info))
	assert.Equal(t, float64(1), info["numberOfBrokerNodes"])
}

func TestDescribeCluster_reportsTheDefaultedKafkaVersion(t *testing.T) {
	// Given: a cluster created without naming a version, which Overcast defaults
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "version-defaulted",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: the cluster is described
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/"+arn, nil)

	// Then: the defaulted version is reported rather than left blank — a caller
	// that omitted the member still has to be able to see what it got
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	assert.NotEmpty(t, kafkaVersionOf(t, info))
}

func TestDescribeCluster_doesNotLeakInternalRecordFields(t *testing.T) {
	// Given: a cluster
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "no-internals",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: the cluster is described
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/"+arn, nil)

	// Then: the response carries the modeled members only. Marshalling the
	// stored record straight onto the wire put the emulator's own Docker
	// bookkeeping in `clusterInfo`.
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	assert.NotContains(t, info, "_dockerContainerID")
	assert.NotContains(t, info, "_hostPort")
}

func TestDescribeCluster_notFound(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a non-existent cluster is described
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nonexistent/abc123", nil)

	// Then: the response is 404 NotFoundException
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertJSONError(t, resp, "NotFoundException")
}

// ── TestListClusters ──────────────────────────────────────────────────────────

func TestListClusters_empty(t *testing.T) {
	// Given: a fresh MSK service with no clusters
	srv := helpers.NewTestServer(t)

	// When: clusters are listed
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters", nil)

	// Then: the response is 200 with an empty list
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	list, _ := result["clusterInfoList"].([]any)
	assert.Empty(t, list)
}

func TestListClusters_multiple(t *testing.T) {
	// Given: two clusters have been created
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"cluster-a", "cluster-b"} {
		resp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
			"clusterName": name,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// When: clusters are listed
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters", nil)

	// Then: both clusters are returned
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	list, _ := result["clusterInfoList"].([]any)
	assert.Len(t, list, 2)
}

func TestListClusters_filter(t *testing.T) {
	// Given: two clusters with different names
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"filter-alpha", "filter-beta"} {
		resp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
			"clusterName": name,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// When: clusters are listed with a name filter
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters?clusterNameFilter=filter-alpha", nil)

	// Then: only the matching cluster is returned
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	list, _ := result["clusterInfoList"].([]any)
	assert.Len(t, list, 1)
	cluster := list[0].(map[string]any)
	assert.Equal(t, "filter-alpha", cluster["clusterName"])
}

func TestListClusters_reportsTheKafkaVersion(t *testing.T) {
	// Given: a cluster created with an explicit Kafka version
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":  "list-version",
		"kafkaVersion": "3.4.0",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	createResp.Body.Close()

	// When: clusters are listed
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters", nil)

	// Then: every element of clusterInfoList is the same modeled ClusterInfo as
	// DescribeCluster's, version included — the web console's cluster table
	// reads its "Kafka version" column from exactly this member
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	list, _ := result["clusterInfoList"].([]any)
	require.Len(t, list, 1)
	assert.Equal(t, "3.4.0", kafkaVersionOf(t, list[0].(map[string]any)))
}

// ── TestDeleteCluster ─────────────────────────────────────────────────────────

func TestDeleteCluster_success(t *testing.T) {
	// Given: a cluster has been created
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "delete-test",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: the cluster is deleted
	resp := mskRequest(t, srv, http.MethodDelete, "/v1/clusters/"+arn, nil)

	// Then: the response is 200 with DELETING state
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, "DELETING", result["state"])
	assert.Equal(t, arn, result["clusterArn"])
}

func TestDeleteCluster_notFound(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a non-existent cluster is deleted
	resp := mskRequest(t, srv, http.MethodDelete, "/v1/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nonexistent/abc123", nil)

	// Then: the response is 404
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ── TestGetBootstrapBrokers ───────────────────────────────────────────────────

func TestGetBootstrapBrokers_success(t *testing.T) {
	// Given: a cluster has been created
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "brokers-test",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: bootstrap brokers are requested
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/"+arn+"/bootstrap-brokers", nil)

	// Then: the response is 200 with bootstrapBrokerString field present
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	_, hasBrokerString := result["bootstrapBrokerString"]
	assert.True(t, hasBrokerString)
}

func TestGetBootstrapBrokers_notFound(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: bootstrap brokers are requested for a non-existent cluster
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nonexistent/abc123/bootstrap-brokers", nil)

	// Then: the response is 404
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ── TestCreateConfiguration ───────────────────────────────────────────────────

func TestCreateConfiguration_success(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a configuration is created
	resp := mskRequest(t, srv, http.MethodPost, "/v1/configurations", map[string]any{
		"name":          "my-config",
		"description":   "test configuration",
		"kafkaVersions": []string{"3.5.1"},
	})

	// Then: the response is 201 with the ARN, state ACTIVE, and latestRevision
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	arn, _ := result["arn"].(string)
	assert.Contains(t, arn, "arn:aws:kafka:")
	assert.Equal(t, "ACTIVE", result["state"])
	assert.Equal(t, "my-config", result["name"])
	assert.NotNil(t, result["latestRevision"])
}

// ── TestListConfigurations ────────────────────────────────────────────────────

func TestListConfigurations_empty(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: configurations are listed
	resp := mskRequest(t, srv, http.MethodGet, "/v1/configurations", nil)

	// Then: the response is 200 with an empty list
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	list, _ := result["configurations"].([]any)
	assert.Empty(t, list)
}

// ── TestDescribeConfiguration ─────────────────────────────────────────────────

func TestDescribeConfiguration_success(t *testing.T) {
	// Given: a configuration has been created
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/configurations", map[string]any{
		"name": "describe-config",
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["arn"].(string)

	// When: the configuration is described
	resp := mskRequest(t, srv, http.MethodGet, "/v1/configurations/"+arn, nil)

	// Then: the response is 200 with the configuration details
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, "describe-config", result["name"])
	assert.Equal(t, arn, result["arn"])
}

// ── TestDeleteConfiguration ───────────────────────────────────────────────────

func TestDeleteConfiguration_success(t *testing.T) {
	// Given: a configuration has been created
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/configurations", map[string]any{
		"name": "delete-config",
	})
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["arn"].(string)

	// When: the configuration is deleted
	resp := mskRequest(t, srv, http.MethodDelete, "/v1/configurations/"+arn, nil)

	// Then: the response is 200
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ── TestListKafkaVersions ─────────────────────────────────────────────────────

func TestListKafkaVersions(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: Kafka versions are listed
	resp := mskRequest(t, srv, http.MethodGet, "/v1/kafka-versions", nil)

	// Then: the response is 200 with the list including 3.5.1
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	versions, _ := result["kafkaVersions"].([]any)
	require.NotEmpty(t, versions)

	var found bool
	for _, v := range versions {
		ver := v.(map[string]any)
		if ver["version"] == "3.5.1" {
			found = true
			assert.Equal(t, "ACTIVE", ver["status"])
		}
	}
	assert.True(t, found, "expected 3.5.1 in kafka versions list")
}

// ── TestTagResource ───────────────────────────────────────────────────────────

func TestTagResource_success(t *testing.T) {
	// Given: a cluster has been created
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "tagged-cluster",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: tags are added to the cluster
	tagResp := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+arn, map[string]any{
		"tags": map[string]string{
			"Environment": "test",
			"Team":        "platform",
		},
	})
	require.Equal(t, http.StatusOK, tagResp.StatusCode)
	tagResp.Body.Close()

	// Then: listing tags returns the added tags
	listResp := mskRequest(t, srv, http.MethodGet, "/v1/tags/"+arn, nil)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var tagResult map[string]any
	decodeJSON(t, listResp, &tagResult)
	tags, _ := tagResult["tags"].(map[string]any)
	assert.Equal(t, "test", tags["Environment"])
	assert.Equal(t, "platform", tags["Team"])
}

// listTags reads a resource's tags back through ListTagsForResource — the one
// operation an SDK client has for the question "what is tagged on this ARN".
func listTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]any {
	t.Helper()
	resp := mskRequest(t, srv, http.MethodGet, "/v1/tags/"+arn, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	tags, _ := result["tags"].(map[string]any)
	return tags
}

func TestCreateCluster_creationTimeTagsReachListTagsForResource(t *testing.T) {
	// Given: a v1 cluster created with tags in the create request
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "v1-create-tags",
		"tags":        map[string]string{"owner": "compat", "env": "test"},
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// Then: they are visible to the tag API, which is where a client looks for
	// them. Accepting them onto the cluster record alone left every tag
	// operation reporting the cluster as untagged.
	tags := listTags(t, srv, arn)
	assert.Equal(t, "compat", tags["owner"])
	assert.Equal(t, "test", tags["env"])
}

func TestCreateClusterV2_creationTimeTagsReachListTagsForResource(t *testing.T) {
	// Given: a v2 cluster created with tags in the create request
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-create-tags",
		"serverless":  map[string]any{},
		"tags":        map[string]string{"owner": "compat"},
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// Then: ListTagsForResource returns them
	tags := listTags(t, srv, arn)
	assert.Equal(t, "compat", tags["owner"])
}

func TestUntagResource_removesACreationTimeTag(t *testing.T) {
	// Given: a cluster created with two tags
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-untag-creation-tag",
		"serverless":  map[string]any{},
		"tags":        map[string]string{"Keep": "yes", "Drop": "me"},
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: one of them is untagged
	untagResp := mskRequest(t, srv, http.MethodDelete, fmt.Sprintf("/v1/tags/%s?tagKeys=Drop", arn), nil)
	require.Equal(t, http.StatusOK, untagResp.StatusCode)
	untagResp.Body.Close()

	// Then: creation-time tags and TagResource tags live in one place, so
	// untagging reaches them both
	tags := listTags(t, srv, arn)
	assert.Equal(t, "yes", tags["Keep"])
	assert.NotContains(t, tags, "Drop")
}

func TestDescribeCluster_reportsCreationTimeAndLaterTags(t *testing.T) {
	// Given: a cluster created with one tag
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "describe-tags",
		"tags":        map[string]string{"atCreate": "yes"},
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: a second tag is added afterwards
	tagResp := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+arn, map[string]any{
		"tags": map[string]string{"afterCreate": "yes"},
	})
	require.Equal(t, http.StatusOK, tagResp.StatusCode)
	tagResp.Body.Close()

	// Then: ClusterInfo.tags shows both — the tag API and the describe response
	// read the same store rather than each keeping half the answer
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/"+arn, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	tags, _ := info["tags"].(map[string]any)
	assert.Equal(t, "yes", tags["atCreate"])
	assert.Equal(t, "yes", tags["afterCreate"])
}

func TestDescribeClusterV2_reportsCreationTimeAndLaterTags(t *testing.T) {
	// Given: a v2 cluster created with one tag
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-describe-tags",
		"serverless":  map[string]any{},
		"tags":        map[string]string{"atCreate": "yes"},
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: a second tag is added afterwards
	tagResp := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+arn, map[string]any{
		"tags": map[string]string{"afterCreate": "yes"},
	})
	require.Equal(t, http.StatusOK, tagResp.StatusCode)
	tagResp.Body.Close()

	// Then: the v2 shape shows both
	resp := mskRequest(t, srv, http.MethodGet, v2ClusterPath(arn), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	tags, _ := info["tags"].(map[string]any)
	assert.Equal(t, "yes", tags["atCreate"])
	assert.Equal(t, "yes", tags["afterCreate"])
}

// ── TestUntagResource ─────────────────────────────────────────────────────────

func TestUntagResource_success(t *testing.T) {
	// Given: a cluster with tags
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "untag-cluster",
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// Add some tags
	tagResp := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+arn, map[string]any{
		"tags": map[string]string{
			"Keep": "yes",
			"Drop": "me",
		},
	})
	require.Equal(t, http.StatusOK, tagResp.StatusCode)
	tagResp.Body.Close()

	// When: one tag is removed
	untagResp := mskRequest(t, srv, http.MethodDelete, fmt.Sprintf("/v1/tags/%s?tagKeys=Drop", arn), nil)
	require.Equal(t, http.StatusOK, untagResp.StatusCode)
	untagResp.Body.Close()

	// Then: only the remaining tag is present
	listResp := mskRequest(t, srv, http.MethodGet, "/v1/tags/"+arn, nil)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var tagResult map[string]any
	decodeJSON(t, listResp, &tagResult)
	tags, _ := tagResult["tags"].(map[string]any)
	assert.Equal(t, "yes", tags["Keep"])
	_, dropped := tags["Drop"]
	assert.False(t, dropped, "tag 'Drop' should have been removed")
}

// ── The v2 cluster API ────────────────────────────────────────────────────────
//
// Every request below goes to /api/v2/clusters, the URI the pinned kafka model
// binds the v2 operations to, and addresses a cluster with the ARN percent-
// encoded into one path segment — the wire form every AWS SDK produces for a
// non-greedy httpLabel. Overcast served these at /v2/clusters until #859, where
// no SDK could reach them.

// clustersV2Path is the modeled base URI of the v2 cluster API.
const clustersV2Path = "/api/v2/clusters"

// v2ClusterPath addresses one cluster the way an SDK does: {ClusterArn} is a
// non-greedy httpLabel, so the whole ARN is percent-encoded into a single path
// segment.
func v2ClusterPath(arn string) string {
	return clustersV2Path + "/" + awsEscapeLabel(arn)
}

// awsEscapeLabel percent-encodes a value the way the AWS SDKs encode a
// non-greedy httpLabel: everything outside RFC 3986's unreserved set, ':' and
// '/' included. smithy-go's httpbinding.EscapePath is the reference
// implementation; botocore's percent_encode and the JS SDK's
// extendedEncodeURIComponent produce the same bytes.
func awsEscapeLabel(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// kafkaScopedRequest is mskRequest carrying a SigV4 credential scope. Overcast
// validates no signature, but the router's 501 fallback needs positive
// evidence that a path it has no route for is not an S3 request, and the scope
// must name the *model's* signing name (kafka) rather than Overcast's service
// key (msk).
func kafkaScopedRequest(t *testing.T, srv *helpers.TestServer, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20240101/us-east-1/kafka/aws4_request, SignedHeaders=host, Signature=stub")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestCreateClusterV2_provisioned_success(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a V2 provisioned cluster creation request is made
	resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-provisioned",
		"provisioned": map[string]any{
			"kafkaVersion":        "3.6.0",
			"numberOfBrokerNodes": 1,
		},
	})

	// Then: the response is 200 with CREATING state, a populated ARN, and clusterType PROVISIONED
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, "v2-provisioned", result["clusterName"])
	assert.Equal(t, "CREATING", result["state"])
	assert.Equal(t, "PROVISIONED", result["clusterType"])
	arn, _ := result["clusterArn"].(string)
	assert.Contains(t, arn, "arn:aws:kafka:")
	assert.Contains(t, arn, "v2-provisioned")
}

func TestCreateClusterV2_serverless_success(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a V2 serverless cluster creation request is made
	resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-serverless",
		"serverless":  map[string]any{},
	})

	// Then: the response is 200 with ACTIVE state (metadata-only) and clusterType SERVERLESS
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, "v2-serverless", result["clusterName"])
	assert.Equal(t, "SERVERLESS", result["clusterType"])
	assert.Equal(t, "ACTIVE", result["state"])
	arn, _ := result["clusterArn"].(string)
	assert.Contains(t, arn, "arn:aws:kafka:")
}

func TestCreateClusterV2_missingName(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a V2 cluster creation request without a name
	resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"provisioned": map[string]any{"kafkaVersion": "3.6.0"},
	})

	// Then: 400 BadRequestException — the kafka model declares no
	// ValidationException on any operation
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "BadRequestException")
}

func TestCreateClusterV2_neitherProvisionedNorServerless(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a V2 cluster creation request names neither cluster shape
	resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-neither",
	})

	// Then: 400 rather than a silently-invented serverless cluster
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "BadRequestException")
}

func TestCreateClusterV2_bothProvisionedAndServerless(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a V2 cluster creation request names both cluster shapes
	resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-both",
		"provisioned": map[string]any{"kafkaVersion": "3.6.0"},
		"serverless":  map[string]any{},
	})

	// Then: 400 — the two members are mutually exclusive
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "BadRequestException")
}

// ── TestDescribeClusterV2 ─────────────────────────────────────────────────────

func TestDescribeClusterV2_provisioned(t *testing.T) {
	// Given: a V2 provisioned cluster
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-describe",
		"provisioned": map[string]any{
			"kafkaVersion":        "3.5.1",
			"numberOfBrokerNodes": 2,
		},
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: described via V2
	resp := mskRequest(t, srv, http.MethodGet, v2ClusterPath(arn), nil)

	// Then: the response includes clusterType and provisioned sub-object
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	assert.Equal(t, "v2-describe", info["clusterName"])
	assert.Equal(t, arn, info["clusterArn"])
	assert.Equal(t, "PROVISIONED", info["clusterType"])
	provisioned, ok := info["provisioned"].(map[string]any)
	require.True(t, ok, "provisioned field should be present")
	assert.Equal(t, float64(2), provisioned["numberOfBrokerNodes"])
	// The v2 shape nests the version one level deeper than v1 does, under
	// `provisioned`. It is still the only member carrying it.
	assert.Equal(t, "3.5.1", kafkaVersionOf(t, provisioned))
}

func TestDescribeClusterV2_describesAClusterCreatedThroughV1(t *testing.T) {
	// Given: a cluster created through the v1 API
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "v1-then-v2",
		"numberOfBrokerNodes": 1,
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: it is described through the v2 API
	resp := mskRequest(t, srv, http.MethodGet, v2ClusterPath(arn), nil)

	// Then: the two APIs agree — v1 creates provisioned clusters, and the v2
	// shape says so even though the v1 record carries no clusterType
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	assert.Equal(t, arn, info["clusterArn"])
	assert.Equal(t, "PROVISIONED", info["clusterType"])
}

func TestDescribeClusterV2_notFound(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: describe a non-existent cluster via V2
	resp := mskRequest(t, srv, http.MethodGet,
		v2ClusterPath("arn:aws:kafka:us-east-1:000000000000:cluster/nope/abc123"), nil)

	// Then: 404 NotFoundException
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertJSONError(t, resp, "NotFoundException")
}

func TestDescribeClusterV2_doesNotAnswerForASubResource(t *testing.T) {
	// Given: a V2 cluster
	srv := helpers.NewTestServer(t)
	createResp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
		"clusterName": "v2-suboperations",
		"serverless":  map[string]any{},
	})
	require.Equal(t, http.StatusOK, createResp.StatusCode)
	var created map[string]any
	decodeJSON(t, createResp, &created)
	arn := created["clusterArn"].(string)

	// When: ListClusterOperationsV2 — modeled beneath the same ARN at
	// …/{ClusterArn}/operations, and not implemented — is called
	resp := kafkaScopedRequest(t, srv, http.MethodGet, v2ClusterPath(arn)+"/operations")
	defer resp.Body.Close() //nolint:errcheck

	// Then: DescribeClusterV2 does not answer it. Binding {clusterArn} as a
	// single label rather than a subtree wildcard is what makes that true: a
	// wildcard would have handed the whole tail to describeClusterV2, which
	// strips nothing and would report the cluster as not found — a plausible
	// answer to a question nobody asked, the #854 shape.
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "clusterInfo")
	assert.NotContains(t, string(body), "NotFoundException")
}

// ── TestListClustersV2 ────────────────────────────────────────────────────────

func TestListClustersV2_returnsBothClusterTypes(t *testing.T) {
	// Given: one provisioned and one serverless cluster
	srv := helpers.NewTestServer(t)
	for _, body := range []map[string]any{
		{"clusterName": "list-v2-prov", "provisioned": map[string]any{"numberOfBrokerNodes": 1}},
		{"clusterName": "list-v2-srvless", "serverless": map[string]any{}},
	} {
		resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, body)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// When: clusters are listed through the v2 API
	resp := mskRequest(t, srv, http.MethodGet, clustersV2Path, nil)

	// Then: both are returned in the v2 shape
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	list, _ := result["clusterInfoList"].([]any)
	require.Len(t, list, 2)
	types := map[string]bool{}
	for _, item := range list {
		types[item.(map[string]any)["clusterType"].(string)] = true
	}
	assert.True(t, types["PROVISIONED"], "provisioned cluster missing from clusterInfoList")
	assert.True(t, types["SERVERLESS"], "serverless cluster missing from clusterInfoList")
}

func TestListClustersV2_filtersByQueryParameters(t *testing.T) {
	// Given: three clusters with two name prefixes and both cluster types
	srv := helpers.NewTestServer(t)
	for _, body := range []map[string]any{
		{"clusterName": "keep-one", "provisioned": map[string]any{"numberOfBrokerNodes": 1}},
		{"clusterName": "keep-two", "serverless": map[string]any{}},
		{"clusterName": "drop-one", "serverless": map[string]any{}},
	} {
		resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, body)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// When: the filters are sent as query parameters, which is where AWS binds
	// them — clusterNameFilter matches on the name prefix
	resp := mskRequest(t, srv, http.MethodGet,
		clustersV2Path+"?clusterNameFilter=keep&clusterTypeFilter=SERVERLESS", nil)

	// Then: only the cluster matching both filters comes back
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	list, _ := result["clusterInfoList"].([]any)
	require.Len(t, list, 1)
	assert.Equal(t, "keep-two", list[0].(map[string]any)["clusterName"])
}

func TestListClustersV2_paginatesWithMaxResultsAndNextToken(t *testing.T) {
	// Given: three clusters
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"page-a", "page-b", "page-c"} {
		resp := mskRequest(t, srv, http.MethodPost, clustersV2Path, map[string]any{
			"clusterName": name,
			"serverless":  map[string]any{},
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// When: the first page is requested with maxResults=2
	resp := mskRequest(t, srv, http.MethodGet, clustersV2Path+"?maxResults=2", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var first map[string]any
	decodeJSON(t, resp, &first)
	firstPage, _ := first["clusterInfoList"].([]any)
	require.Len(t, firstPage, 2)
	token, _ := first["nextToken"].(string)
	require.NotEmpty(t, token, "a truncated page must carry a nextToken")

	// Then: the token returns the remainder and the last page carries no token
	resp = mskRequest(t, srv, http.MethodGet,
		clustersV2Path+"?maxResults=2&nextToken="+url.QueryEscape(token), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var second map[string]any
	decodeJSON(t, resp, &second)
	secondPage, _ := second["clusterInfoList"].([]any)
	assert.Len(t, secondPage, 1)
	assert.NotContains(t, second, "nextToken")
}

func TestListClustersV2_rejectsAnInvalidNextToken(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: a garbled continuation token is sent
	resp := mskRequest(t, srv, http.MethodGet, clustersV2Path+"?nextToken=not-a-token", nil)

	// Then: 400, rather than silently restarting from the first page
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "BadRequestException")
}

// ── TestUpdateClusterConfiguration ───────────────────────────────────────────

func TestUpdateClusterConfiguration_success(t *testing.T) {
	// Given: a cluster and a configuration both exist
	srv := helpers.NewTestServer(t)

	createCluster := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "config-target",
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
	})
	require.Equal(t, http.StatusOK, createCluster.StatusCode)
	var clusterResult map[string]any
	decodeJSON(t, createCluster, &clusterResult)
	clusterArn := clusterResult["clusterArn"].(string)

	// Get the cluster's currentVersion
	descResp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/"+clusterArn, nil)
	require.Equal(t, http.StatusOK, descResp.StatusCode)
	var descResult map[string]any
	decodeJSON(t, descResp, &descResult)
	currentVersion := descResult["clusterInfo"].(map[string]any)["currentVersion"].(string)

	createCfg := mskRequest(t, srv, http.MethodPost, "/v1/configurations", map[string]any{
		"name":          "my-config",
		"kafkaVersions": []string{"3.5.1"},
	})
	require.Equal(t, http.StatusCreated, createCfg.StatusCode)
	var cfgResult map[string]any
	decodeJSON(t, createCfg, &cfgResult)
	configArn := cfgResult["arn"].(string)

	// When: UpdateClusterConfiguration is called
	resp := mskRequest(t, srv, http.MethodPut, "/v1/clusters/"+clusterArn+"/configuration", map[string]any{
		"configurationArn":      configArn,
		"configurationRevision": 1,
		"currentVersion":        currentVersion,
	})

	// Then: 200 with clusterArn and a clusterOperationArn
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, clusterArn, result["clusterArn"])
	opArn, _ := result["clusterOperationArn"].(string)
	assert.Contains(t, opArn, "arn:aws:kafka:")
}

func TestUpdateClusterConfiguration_clusterNotFound(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t)

	// When: update configuration on a non-existent cluster
	resp := mskRequest(t, srv, http.MethodPut, "/v1/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nope/abc/configuration", map[string]any{
		"configurationArn":      "arn:aws:kafka:us-east-1:000000000000:configuration/x/y",
		"configurationRevision": 1,
		"currentVersion":        "any",
	})

	// Then: 404 NotFoundException
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertJSONError(t, resp, "NotFoundException")
}

func TestUpdateClusterConfiguration_staleVersion(t *testing.T) {
	// Given: a cluster exists
	srv := helpers.NewTestServer(t)
	createCluster := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName": "stale-test",
	})
	require.Equal(t, http.StatusOK, createCluster.StatusCode)
	var clusterResult map[string]any
	decodeJSON(t, createCluster, &clusterResult)
	clusterArn := clusterResult["clusterArn"].(string)

	createCfg := mskRequest(t, srv, http.MethodPost, "/v1/configurations", map[string]any{
		"name": "stale-cfg",
	})
	require.Equal(t, http.StatusCreated, createCfg.StatusCode)
	var cfgResult map[string]any
	decodeJSON(t, createCfg, &cfgResult)
	configArn := cfgResult["arn"].(string)

	// When: update with a stale currentVersion
	resp := mskRequest(t, srv, http.MethodPut, "/v1/clusters/"+clusterArn+"/configuration", map[string]any{
		"configurationArn":      configArn,
		"configurationRevision": 1,
		"currentVersion":        "stale-version",
	})

	// Then: 400 BadRequestException
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "BadRequestException")
}
