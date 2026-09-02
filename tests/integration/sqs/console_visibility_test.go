package sqs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// Credentials are not a partition key — SQS. The DynamoDB copy of this test
// (tests/integration/dynamodb/console_visibility_test.go) says why it exists.

func sqsSignedAs(accessKey, region string) string {
	return "AWS4-HMAC-SHA256 Credential=" + accessKey + "/20250101/" + region +
		"/sqs/aws4_request, SignedHeaders=host;x-amz-date, Signature=fake"
}

func sqsCallWithHeaders(t *testing.T, srv *helpers.TestServer, action string, body map[string]any, headers map[string]string) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return resp
}

// Given: a queue created by a signed CLI-style client in the default region.
// When: the console lists queues in each shape its requests take.
// Then: every shape sees the queue.
func TestListQueues_consoleSeesQueuesCreatedBySignedClients(t *testing.T) {
	srv := helpers.NewTestServer(t)
	region := srv.Config.Region
	resp := sqsCallWithHeaders(t, srv, "CreateQueue", map[string]any{"QueueName": "from-cli"}, map[string]string{
		"Authorization": sqsSignedAs("AKIAIOSFODNN7EXAMPLE", region),
		"X-Amz-Date":    "20250101T000000Z",
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	consoleShapes := map[string]map[string]string{
		"browser SDK, signed as overcast": {
			"Authorization": sqsSignedAs("overcast", region),
			"X-Amz-Date":    "20250101T000000Z",
		},
		"BFF, unsigned with X-Overcast-Region": {"X-Overcast-Region": region},
		"unsigned, no region evidence at all":  {},
	}
	for name, headers := range consoleShapes {
		t.Run(name, func(t *testing.T) {
			resp := sqsCallWithHeaders(t, srv, "ListQueues", map[string]any{}, headers)
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)
			var out struct {
				QueueUrls []string `json:"QueueUrls"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode ListQueues: %v", err)
			}
			if len(out.QueueUrls) != 1 || !strings.HasSuffix(out.QueueUrls[0], "/from-cli") {
				t.Fatalf("expected the CLI's queue, got %v", out.QueueUrls)
			}
		})
	}
}
