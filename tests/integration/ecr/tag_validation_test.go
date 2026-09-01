// tag_validation_test.go — ECR CreateRepository/TagResource tag validation
// (#1052).
//
// CreateRepository's inline tags and TagResource used to store whatever a
// caller sent without checking AWS's own tag constraints — a reserved
// `aws:` key prefix, or more than 50 tags on one repository, had to be
// rejected the way real AWS rejects them, and neither was.
package ecr_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func ecrTagMap(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := ecrCall(t, srv, "ListTagsForResource", map[string]any{"resourceArn": arn})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"tags"`
	}
	decodeJSON(t, resp, &out)
	got := make(map[string]string, len(out.Tags))
	for _, tag := range out.Tags {
		got[tag.Key] = tag.Value
	}
	return got
}

func TestCreateRepository_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := ecrCall(t, srv, "CreateRepository", map[string]any{
		"repositoryName": "invalid-tag-create",
		"tags":           []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")

	// And: no repository was left behind for the rejected tags to strand.
	describe := ecrCall(t, srv, "DescribeRepositories", map[string]any{
		"repositoryNames": []string{"invalid-tag-create"},
	})
	defer describe.Body.Close()
	if describe.StatusCode == http.StatusOK {
		t.Fatalf("a repository named invalid-tag-create exists despite the rejected create")
	}
}

func TestTagResource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "invalid-tag-resource"})
	var created struct {
		Repository struct {
			RepositoryArn string `json:"repositoryArn"`
		} `json:"repository"`
	}
	decodeJSON(t, create, &created)

	resp := ecrCall(t, srv, "TagResource", map[string]any{
		"resourceArn": created.Repository.RepositoryArn,
		"tags":        []map[string]string{{"Key": "aws:reserved", "Value": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")

	if got := ecrTagMap(t, srv, created.Repository.RepositoryArn); len(got) != 0 {
		t.Fatalf("tags = %#v after a rejected TagResource, want none stored", got)
	}
}

func TestTagResource_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "valid-tag-resource"})
	var created struct {
		Repository struct {
			RepositoryArn string `json:"repositoryArn"`
		} `json:"repository"`
	}
	decodeJSON(t, create, &created)

	resp := ecrCall(t, srv, "TagResource", map[string]any{
		"resourceArn": created.Repository.RepositoryArn,
		"tags":        []map[string]string{{"Key": "env", "Value": "prod"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	if got := ecrTagMap(t, srv, created.Repository.RepositoryArn); got["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", got)
	}
}

// The 50-tag limit is checked against the existing plus incoming set, not
// just what one TagResource call adds.
func TestTagResource_tagLimitEnforcedOnMergedSet(t *testing.T) {
	srv := helpers.NewTestServer(t)
	create := ecrCall(t, srv, "CreateRepository", map[string]any{"repositoryName": "tag-limit"})
	var created struct {
		Repository struct {
			RepositoryArn string `json:"repositoryArn"`
		} `json:"repository"`
	}
	decodeJSON(t, create, &created)

	seedTags := make([]map[string]string, 0, 50)
	for i := 0; i < 50; i++ {
		seedTags = append(seedTags, map[string]string{"Key": fmt.Sprintf("k%d", i), "Value": "v"})
	}
	seed := ecrCall(t, srv, "TagResource", map[string]any{"resourceArn": created.Repository.RepositoryArn, "tags": seedTags})
	defer seed.Body.Close()
	helpers.AssertStatus(t, seed, http.StatusOK)

	resp := ecrCall(t, srv, "TagResource", map[string]any{
		"resourceArn": created.Repository.RepositoryArn,
		"tags":        []map[string]string{{"Key": "one-too-many", "Value": "x"}},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}
