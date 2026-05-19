package sqs_test

import (
	"bytes"
	"net/http"
	"testing"

	cborlib "github.com/fxamacker/cbor/v2"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestRPCv2CBOR_ListQueues(t *testing.T) {
	// Given: an SQS queue exists.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "cbor-queue")

	// When: ListQueues is called over Smithy RPC v2 CBOR.
	resp := sqsCBORCall(t, srv, "ListQueues", map[string]any{
		"QueueNamePrefix": "cbor",
	})
	defer resp.Body.Close()

	// Then: SQS responds with a CBOR body.
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertHeader(t, resp, "Content-Type", "application/cbor")
	helpers.AssertHeader(t, resp, "Smithy-Protocol", "rpc-v2-cbor")

	var out struct {
		QueueUrls []string `cbor:"QueueUrls"`
	}
	if err := cborlib.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode CBOR response: %v", err)
	}
	if len(out.QueueUrls) != 1 || out.QueueUrls[0] != queueURL {
		t.Fatalf("QueueUrls = %#v, want [%q]", out.QueueUrls, queueURL)
	}
}

func TestRPCv2CBOR_UnsupportedServiceProtocol(t *testing.T) {
	// Given: EC2 has not migrated to rpc-v2-cbor (deferred per §10).
	srv := helpers.NewTestServer(t)

	// When: a Smithy RPC v2 CBOR request targets an unmigrated service.
	resp := smithyCBORCall(t, srv, "ec2", "DescribeInstances", map[string]any{})
	defer resp.Body.Close()

	// Then: the protocol is rejected clearly instead of being coerced.
	helpers.AssertStatus(t, resp, http.StatusUnsupportedMediaType)
	helpers.AssertHeader(t, resp, "x-emulator-unsupported-protocol", "smithy.protocols#rpcv2Cbor")
}

func smithyCBORCall(t *testing.T, srv *helpers.TestServer, service, operation string, body map[string]any) *http.Response {
	t.Helper()
	payload, err := cborlib.Marshal(body)
	if err != nil {
		t.Fatalf("marshal CBOR %s.%s body: %v", service, operation, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/service/"+service+"/operation/"+operation, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build CBOR request: %v", err)
	}
	req.Header.Set("Content-Type", "application/cbor")
	req.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do CBOR request %s.%s: %v", service, operation, err)
	}
	return resp
}

func sqsCBORCall(t *testing.T, srv *helpers.TestServer, operation string, body map[string]any) *http.Response {
	t.Helper()
	return smithyCBORCall(t, srv, "AmazonSQS", operation, body)
}
