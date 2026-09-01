// tag_validation_test.go — MSK CreateCluster/TagResource tag validation
// (#1052).
//
// CreateCluster's inline tags and TagResource used to store whatever a
// caller sent without checking AWS's own tag constraints — a reserved
// `aws:` key prefix, or more than 50 tags on one cluster, had to be rejected
// the way real AWS rejects them, and neither was.
package msk_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func mskTagMap(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := mskRequest(t, srv, http.MethodGet, "/v1/tags/"+arn, nil)
	defer resp.Body.Close()
	require := func(cond bool, msg string) {
		if !cond {
			t.Fatal(msg)
		}
	}
	require(resp.StatusCode == http.StatusOK, fmt.Sprintf("ListTagsForResource: HTTP %d", resp.StatusCode))
	var out struct {
		Tags map[string]string `json:"tags"`
	}
	decodeJSON(t, resp, &out)
	return out.Tags
}

func TestCreateCluster_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "invalid-tag-create",
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
		"tags":                map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertJSONError(t, resp, "BadRequestException")

	// And: no cluster was left behind for the rejected tags to strand.
	list := mskRequest(t, srv, http.MethodGet, "/v1/clusters?clusterNameFilter=invalid-tag-create", nil)
	defer list.Body.Close()
	var out struct {
		ClusterInfoList []map[string]any `json:"clusterInfoList"`
	}
	decodeJSON(t, list, &out)
	if len(out.ClusterInfoList) != 0 {
		t.Fatalf("a cluster named invalid-tag-create exists despite the rejected create")
	}
}

func TestCreateClusterV2_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := mskRequest(t, srv, http.MethodPost, "/api/v2/clusters", map[string]any{
		"clusterName": "invalid-tag-create-v2",
		"tags":        map[string]string{"aws:reserved": "x"},
		"serverless":  map[string]any{"vpcConfigs": []any{map[string]any{"subnetIds": []string{"subnet-1"}}}},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertJSONError(t, resp, "BadRequestException")
}

func TestTagResource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "invalid-tag-resource",
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
	})
	var created struct {
		ClusterArn string `json:"clusterArn"`
	}
	decodeJSON(t, create, &created)
	create.Body.Close()

	resp := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+created.ClusterArn, map[string]any{
		"tags": map[string]string{"aws:reserved": "x"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertJSONError(t, resp, "BadRequestException")

	if got := mskTagMap(t, srv, created.ClusterArn); len(got) != 0 {
		t.Fatalf("tags = %#v after a rejected TagResource, want none stored", got)
	}
}

func TestTagResource_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "valid-tag-resource",
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
	})
	var created struct {
		ClusterArn string `json:"clusterArn"`
	}
	decodeJSON(t, create, &created)
	create.Body.Close()

	resp := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+created.ClusterArn, map[string]any{
		"tags": map[string]string{"env": "prod"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if got := mskTagMap(t, srv, created.ClusterArn); got["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", got)
	}
}

// The 50-tag limit is checked against the existing plus incoming set, not
// just what one TagResource call adds.
func TestTagResource_tagLimitEnforcedOnMergedSet(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := mskRequest(t, srv, http.MethodPost, "/v1/clusters", map[string]any{
		"clusterName":         "tag-limit",
		"kafkaVersion":        "3.5.1",
		"numberOfBrokerNodes": 1,
	})
	var created struct {
		ClusterArn string `json:"clusterArn"`
	}
	decodeJSON(t, create, &created)
	create.Body.Close()

	seedTags := make(map[string]string, 50)
	for i := 0; i < 50; i++ {
		seedTags[fmt.Sprintf("k%d", i)] = "v"
	}
	seed := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+created.ClusterArn, map[string]any{"tags": seedTags})
	if seed.StatusCode != http.StatusOK {
		t.Fatalf("seed TagResource: HTTP %d", seed.StatusCode)
	}
	drain(t, seed)

	resp := mskRequest(t, srv, http.MethodPost, "/v1/tags/"+created.ClusterArn, map[string]any{
		"tags": map[string]string{"one-too-many": "x"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	assertJSONError(t, resp, "BadRequestException")
}
