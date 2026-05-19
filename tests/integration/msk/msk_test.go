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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: a cluster creation request is made without a name
	resp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"kafkaVersion": "3.5.1",
	})

	// Then: the response is 400 ValidationException
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "ValidationException")
}

// ── TestDescribeCluster ───────────────────────────────────────────────────────

func TestDescribeCluster_success(t *testing.T) {
	// Given: a cluster has been created
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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

func TestDescribeCluster_notFound(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: a non-existent cluster is described
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nonexistent/abc123", nil)

	// Then: the response is 404 NotFoundException
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertJSONError(t, resp, "NotFoundException")
}

// ── TestListClusters ──────────────────────────────────────────────────────────

func TestListClusters_empty(t *testing.T) {
	// Given: a fresh MSK service with no clusters
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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

// ── TestDeleteCluster ─────────────────────────────────────────────────────────

func TestDeleteCluster_success(t *testing.T) {
	// Given: a cluster has been created
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: a non-existent cluster is deleted
	resp := mskRequest(t, srv, http.MethodDelete, "/v1/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nonexistent/abc123", nil)

	// Then: the response is 404
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ── TestGetBootstrapBrokers ───────────────────────────────────────────────────

func TestGetBootstrapBrokers_success(t *testing.T) {
	// Given: a cluster has been created
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: bootstrap brokers are requested for a non-existent cluster
	resp := mskRequest(t, srv, http.MethodGet, "/v1/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nonexistent/abc123/bootstrap-brokers", nil)

	// Then: the response is 404
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ── TestCreateConfiguration ───────────────────────────────────────────────────

func TestCreateConfiguration_success(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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

// ── TestUntagResource ─────────────────────────────────────────────────────────

func TestUntagResource_success(t *testing.T) {
	// Given: a cluster with tags
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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

// ── TestCreateClusterV2 ───────────────────────────────────────────────────────

func TestCreateClusterV2_provisioned_success(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: a V2 provisioned cluster creation request is made
	resp := mskRequest(t, srv, http.MethodPost, "/v2/clusters", map[string]any{
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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: a V2 serverless cluster creation request is made
	resp := mskRequest(t, srv, http.MethodPost, "/v2/clusters", map[string]any{
		"clusterName": "v2-serverless",
		"serverless":  map[string]any{},
	})

	// Then: the response is 200 with ACTIVE state (metadata-only) and clusterType SERVERLESS
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	assert.Equal(t, "v2-serverless", result["clusterName"])
	assert.Equal(t, "SERVERLESS", result["clusterType"])
	arn, _ := result["clusterArn"].(string)
	assert.Contains(t, arn, "arn:aws:kafka:")
}

func TestCreateClusterV2_missingName(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: a V2 cluster creation request without a name
	resp := mskRequest(t, srv, http.MethodPost, "/v2/clusters", map[string]any{
		"provisioned": map[string]any{"kafkaVersion": "3.6.0"},
	})

	// Then: 400 ValidationException
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assertJSONError(t, resp, "ValidationException")
}

// ── TestDescribeClusterV2 ─────────────────────────────────────────────────────

func TestDescribeClusterV2_provisioned(t *testing.T) {
	// Given: a V2 provisioned cluster
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
	createResp := mskRequest(t, srv, http.MethodPost, "/v2/clusters", map[string]any{
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
	resp := mskRequest(t, srv, http.MethodGet, "/v2/clusters/"+arn, nil)

	// Then: the response includes clusterType and provisioned sub-object
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]any
	decodeJSON(t, resp, &result)
	info := result["clusterInfo"].(map[string]any)
	assert.Equal(t, "v2-describe", info["clusterName"])
	assert.Equal(t, "PROVISIONED", info["clusterType"])
	provisioned, ok := info["provisioned"].(map[string]any)
	require.True(t, ok, "provisioned field should be present")
	assert.Equal(t, float64(2), provisioned["numberOfBrokerNodes"])
}

func TestDescribeClusterV2_notFound(t *testing.T) {
	// Given: a fresh MSK service
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

	// When: describe a non-existent cluster via V2
	resp := mskRequest(t, srv, http.MethodGet, "/v2/clusters/arn:aws:kafka:us-east-1:000000000000:cluster/nope/abc123", nil)

	// Then: 404 NotFoundException
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assertJSONError(t, resp, "NotFoundException")
}

// ── TestUpdateClusterConfiguration ───────────────────────────────────────────

func TestUpdateClusterConfiguration_success(t *testing.T) {
	// Given: a cluster and a configuration both exist
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))

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
	srv := helpers.NewTestServer(t, helpers.WithServices("msk"))
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
