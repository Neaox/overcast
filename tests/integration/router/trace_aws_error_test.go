package router_test

// trace_aws_error_test.go — the AWS error a request answered with must reach
// its trace.
//
// internal/trace's MatchesSearch reads entry.AWSErrorCode and
// entry.AWSErrorMessage, and internal/trace/retention.go keeps a trace when
// either the status is >= 400 *or* an AWS error code is set. Both were dead
// against the real server: the fields were never populated, so the trace list
// could not be searched by error code and a 2xx carrying an AWS error was not
// recognised as a failure worth keeping.
//
// The cause was not a missing implementation. protocol.recordAWSError does a
// type assertion on the ResponseWriter, middleware.responseWriter implements
// it, and Logger reads the value back off its own writer — all correct in
// isolation. But RequestEvents is registered *after* Logger and builds a
// second responseWriter around the first, so the handler recorded onto the
// inner instance while Logger read the outer one. The assertion succeeded
// every time; the value simply landed on an object nobody read, which is why
// no unit test on either type could see it.
//
// These tests therefore go through the real middleware stack. A test that
// exercises responseWriter directly passes against the bug.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

type traceListForErrors struct {
	Traces []struct {
		RequestID  string `json:"requestId"`
		Service    string `json:"service"`
		Operation  string `json:"operation"`
		StatusCode int    `json:"statusCode"`
	} `json:"traces"`
}

func listTraces(t *testing.T, srv *helpers.TestServer, query string) traceListForErrors {
	t.Helper()
	resp, err := http.Get(srv.URL + "/_overcast/debug/traces?" + query)
	if err != nil {
		t.Fatalf("list traces: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read traces: %v", err)
	}
	var out traceListForErrors
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode traces: %v\nbody: %s", err, raw)
	}
	return out
}

// seedLambdaNotFound issues a signed GetFunctionConfiguration for a function
// that does not exist. Signed, because the credential scope is what names the
// service — an unsigned request would be classified as s3 and prove less.
func seedLambdaNotFound(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/2015-03-31/functions/"+name+"/configuration", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=test/20260813/us-east-1/lambda/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seed request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("seed request status = %d, want 404", resp.StatusCode)
	}
	return resp.Header.Get("x-amzn-requestid")
}

// The list summary deliberately carries neither field — it is polled once a
// second and stays small on purpose — so the detail view is where a reader
// sees what the request actually answered with.
func TestTrace_recordsTheAWSErrorCodeARequestAnsweredWith(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// Given: a request that failed with a modeled AWS error.
	reqID := seedLambdaNotFound(t, srv, "no-such-function-anywhere")

	// When: its trace is opened.
	resp, err := http.Get(srv.URL + "/_overcast/debug/trace/" + reqID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var detail struct {
		StatusCode      int    `json:"statusCode"`
		AWSErrorCode    string `json:"awsErrorCode"`
		AWSErrorMessage string `json:"awsErrorMessage"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode trace: %v\nbody: %s", err, raw)
	}

	// Then: it carries the code and the message, not just the status.
	if detail.AWSErrorCode != "ResourceNotFoundException" {
		t.Errorf("awsErrorCode = %q, want ResourceNotFoundException", detail.AWSErrorCode)
	}
	if detail.AWSErrorMessage == "" {
		t.Error("awsErrorMessage is empty; the trace records the code but not what it said")
	}
}

func TestTrace_listSearchMatchesTheAWSErrorCode(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// Given: a failed request whose error code appears nowhere in its path,
	// service or request ID — so only the recorded error can match it.
	reqID := seedLambdaNotFound(t, srv, "no-such-function-anywhere")

	// When: the list is searched by that code, which is what someone pastes
	// in from a failing SDK call.
	got := listTraces(t, srv, "search=ResourceNotFoundException&limit=50")

	// Then: the trace comes back.
	for _, tr := range got.Traces {
		if tr.RequestID == reqID {
			return
		}
	}
	t.Errorf("search by error code returned %d traces, none of them %s", len(got.Traces), reqID)
}

// The operation half of the same search already worked, and must keep working:
// it is the half that made the feature useful for RPC-protocol services, where
// every request is POST / and the operation is the only distinguishing field.
func TestTrace_listSearchStillMatchesTheOperation(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))
	reqID := seedLambdaNotFound(t, srv, "no-such-function-anywhere")

	for _, tr := range listTraces(t, srv, "search=GetFunctionConfiguration&limit=50").Traces {
		if tr.RequestID == reqID {
			return
		}
	}
	t.Errorf("search by operation did not return %s", reqID)
}

// DynamoDB wraps the writer again, in its own crc32ResponseWriter, to add the
// X-Amz-Crc32 header its clients check. CloudFront does the same for access
// logging. Those wrappers sit *closer to the handler* than Logger's, so they
// are what protocol.recordAWSError asserts on — and a wrapper that does not
// forward swallows the error exactly as the nested middleware writer did.
//
// This is the same defect one layer down, and the reason the fix is a
// forwarding contract rather than a one-off: the number of wrappers between
// Logger and a handler is a property of the service, not of the middleware
// chain.
func TestTrace_recordsTheAWSErrorThroughAServiceOwnedWriter(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// Given: a DynamoDB request that failed — DynamoDB's handlers write through
	// protocol.WriteJSONError like everyone else, but behind crc32ResponseWriter.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(`{"TableName":"no-such-table-anywhere"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.DescribeTable")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seed request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("seed status = %d, want 400", resp.StatusCode)
	}
	reqID := resp.Header.Get("x-amzn-requestid")

	// Then: the error reached the trace despite the extra wrapper.
	detail, err := http.Get(srv.URL + "/_overcast/debug/trace/" + reqID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	defer detail.Body.Close()
	raw, err := io.ReadAll(detail.Body)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	var got struct {
		AWSErrorCode string `json:"awsErrorCode"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode trace: %v\nbody: %s", err, raw)
	}
	if got.AWSErrorCode != "ResourceNotFoundException" {
		t.Errorf("awsErrorCode = %q, want ResourceNotFoundException — the service's own writer swallowed it", got.AWSErrorCode)
	}
}
