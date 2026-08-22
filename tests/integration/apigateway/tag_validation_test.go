// tag_validation_test.go — API Gateway tag validation (#1052).
//
// TagResource, TagV2Resource, and every Create* operation carrying inline
// tags used to store whatever a caller sent without checking AWS's own tag
// constraints. A reserved `aws:` key prefix had to be rejected the way real
// AWS rejects it, and none of them did.
package apigateway_test

import (
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func apigwTagMap(t *testing.T, srv *helpers.TestServer, arn string) map[string]any {
	t.Helper()
	resp := apiCall(t, srv, http.MethodGet, "/tags/"+arn, nil)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	helpers.DecodeJSON(t, resp, &result)
	tags, _ := result["tags"].(map[string]any)
	return tags
}

func TestTagResource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:apigateway:us-east-1::/restapis/reserved-tag-test"

	resp := apiCall(t, srv, http.MethodPut, "/tags/"+arn, map[string]any{
		"tags": map[string]string{"aws:reserved": "x"},
	})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")

	if got := apigwTagMap(t, srv, arn); len(got) != 0 {
		t.Fatalf("tags = %#v after a rejected TagResource, want none stored", got)
	}
}

func TestTagV2Resource_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:apigateway:us-east-1::/apis/reserved-tag-test-v2"

	resp := apiCall(t, srv, http.MethodPost, "/v2/tags/"+arn, map[string]any{
		"tags": map[string]string{"aws:reserved": "x"},
	})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestCreateRestApi_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := apiCall(t, srv, http.MethodPost, "/restapis", map[string]any{
		"name": "invalid-tag-create",
		"tags": map[string]string{"aws:reserved": "x"},
	})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestCreateDomainName_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := apiCall(t, srv, http.MethodPost, "/domainnames", map[string]any{
		"domainName": "invalid-tag.example.com",
		"tags":       map[string]string{"aws:reserved": "x"},
	})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestCreateApiKey_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := apiCall(t, srv, http.MethodPost, "/apikeys", map[string]any{
		"name": "invalid-tag-key",
		"tags": map[string]string{"aws:reserved": "x"},
	})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestCreateVpcLinkV2_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := apiCall(t, srv, http.MethodPost, "/v2/vpclinks", map[string]any{
		"name":      "invalid-tag-vpclink",
		"subnetIds": []string{"subnet-1"},
		"tags":      map[string]string{"aws:reserved": "x"},
	})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestCreateApi_reservedTagPrefixRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := apiCall(t, srv, http.MethodPost, "/v2/apis", map[string]any{
		"name":         "invalid-tag-api-v2",
		"protocolType": "HTTP",
		"tags":         map[string]string{"aws:reserved": "x"},
	})
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "BadRequestException")
}

func TestTagResource_validTagsStillWork(t *testing.T) {
	srv := helpers.NewTestServer(t)
	arn := "arn:aws:apigateway:us-east-1::/restapis/valid-tag-test"

	resp := apiCall(t, srv, http.MethodPut, "/tags/"+arn, map[string]any{
		"tags": map[string]string{"env": "prod"},
	})
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	if got := apigwTagMap(t, srv, arn); got["env"] != "prod" {
		t.Fatalf("tags = %#v, want env=prod", got)
	}
}
