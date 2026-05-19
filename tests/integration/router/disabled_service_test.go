package router_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// TestDisabledService_pathPrefix_returnsJSONServiceDisabled verifies that when
// a path-prefix service (Lambda) is disabled, POST requests to its URL paths
// return a JSON 503 ServiceDisabled rather than falling through to S3's wildcard
// and returning an XML error.
func TestDisabledService_pathPrefix_returnsJSONServiceDisabled(t *testing.T) {
	// Given: a server with Lambda disabled (only S3 enabled)
	srv := helpers.NewTestServer(t, helpers.WithServices("s3"))

	body, _ := json.Marshal(map[string]any{
		"FunctionName": "my-fn",
		"Runtime":      "python3.13",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/my-role",
		"Code":         map[string]any{"ZipFile": "aGVsbG8="},
	})

	// When: we POST to /2015-03-31/functions (Lambda CreateFunction)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/2015-03-31/functions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get a JSON 503 ServiceDisabled — not an XML 501 from S3
	helpers.AssertStatus(t, resp, http.StatusServiceUnavailable)

	ct := resp.Header.Get("Content-Type")
	if ct != "application/x-amz-json-1.0" {
		t.Errorf("expected JSON content-type, got %q (wanted no XML)", ct)
	}

	var result struct {
		Type string `json:"__type"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Type != "ServiceDisabled" {
		t.Errorf("expected __type ServiceDisabled, got %q", result.Type)
	}
}

// TestDisabledService_pathPrefix_returnsXMLServiceDisabled verifies that when a
// path-prefix service is disabled, Query-protocol (form-encoded) requests to its
// URL paths return an XML 503 rather than JSON.
func TestDisabledService_pathPrefix_returnsXMLServiceDisabled(t *testing.T) {
	// Given: a server with Lambda disabled
	srv := helpers.NewTestServer(t, helpers.WithServices("s3"))

	// When: we POST to /2015-03-31/functions with a Query-protocol Content-Type
	body := bytes.NewBufferString("Action=CreateFunction&Version=2015-03-31")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/2015-03-31/functions", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get an XML 503 ServiceDisabled
	helpers.AssertStatus(t, resp, http.StatusServiceUnavailable)

	ct := resp.Header.Get("Content-Type")
	if ct != "text/xml" {
		t.Errorf("expected XML content-type for Query-protocol request, got %q", ct)
	}
}

// TestDisabledService_targetDispatch_returnsServiceDisabled verifies that when a
// TargetDispatcher service (e.g. DynamoDB) is disabled, POST requests carrying
// its X-Amz-Target prefix return a JSON 503 ServiceDisabled instead of
// UnknownOperationException.
func TestDisabledService_targetDispatch_returnsServiceDisabled(t *testing.T) {
	// Given: a server with DynamoDB disabled but SQS enabled (so targetDispatch runs)
	srv := helpers.NewTestServer(t, helpers.WithServices("sqs"))

	body, _ := json.Marshal(map[string]any{"TableName": "my-table"})

	// When: we POST with a DynamoDB X-Amz-Target
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.DescribeTable")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: we get a JSON 503 ServiceDisabled — not UnknownOperationException 400
	helpers.AssertStatus(t, resp, http.StatusServiceUnavailable)

	var result struct {
		Type string `json:"__type"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.Type != "ServiceDisabled" {
		t.Errorf("expected __type ServiceDisabled, got %q", result.Type)
	}
}
