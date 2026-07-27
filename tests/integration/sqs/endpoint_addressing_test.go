// endpoint_addressing_test.go covers queue-URL minting across caller origins.
//
// AWS SDKs resolve the SQS endpoint from the QueueUrl, not from the client's
// configured endpoint: @aws-sdk/middleware-sdk-sqs's queueUrlMiddleware swaps
// context.endpointV2 for the QueueUrl origin whenever the two differ and no
// explicit `endpoint` was passed to the client (AWS_ENDPOINT_URL does not
// count — it is resolved via the ruleset's Endpoint parameter, never
// config.endpoint). .NET and Java v1 use the queue URL as the request URI for
// the same historical reason.
//
// The consequence for an emulator: a queue URL is only usable by a caller that
// can dial its origin. A single server-wide origin cannot serve both a host
// CLI (localhost:4566) and a sibling Lambda container, so queue URLs are minted
// from the origin each caller actually reached Overcast on.
package sqs_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestQueueURL_usesCallerOrigin(t *testing.T) {
	// Given: an emulator reachable under several origins (host CLI, sibling
	// container, compose service name).
	srv := helpers.NewTestServer(t)

	for _, host := range []string{"overcast:4566", "172.18.0.1:4566", "localhost.overcast.sh:4566"} {
		t.Run(host, func(t *testing.T) {
			// When: a queue is created by a caller reaching Overcast on that origin.
			url := createQueueFromHost(t, srv, host, "origin-"+strings.NewReplacer(":", "-", ".", "-").Replace(host))

			// Then: the queue URL carries that origin, so the SDK's endpoint
			// override resolves back to Overcast rather than somewhere unreachable.
			want := "http://" + host + "/000000000000/"
			if !strings.HasPrefix(url, want) {
				t.Fatalf("QueueUrl = %q, want prefix %q", url, want)
			}
		})
	}
}

func TestQueueURL_getQueueUrlAndListQueuesFollowCaller(t *testing.T) {
	// Given: a queue created by a host-side caller.
	srv := helpers.NewTestServer(t)
	createQueueFromHost(t, srv, "localhost:4566", "cross-origin-queue")

	// When: a sibling container looks the same queue up on its own origin.
	got := sqsCallFromHost(t, srv, "172.18.0.1:4566", "GetQueueUrl", map[string]any{
		"QueueName": "cross-origin-queue",
	})

	// Then: it gets a URL it can actually dial, not the host-side one.
	want := "http://172.18.0.1:4566/000000000000/cross-origin-queue"
	if got.QueueUrl != want {
		t.Fatalf("GetQueueUrl = %q, want %q", got.QueueUrl, want)
	}

	// And: ListQueues re-renders stored URLs the same way.
	resp := sqsCallWithHost(t, srv, "172.18.0.1:4566", "ListQueues", map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var list struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, resp, &list)
	if len(list.QueueUrls) != 1 || list.QueueUrls[0] != want {
		t.Fatalf("ListQueues = %v, want [%q]", list.QueueUrls, want)
	}
}

func TestQueueURL_awsHostFallsBackToConfiguredOrigin(t *testing.T) {
	// Given: a caller using real AWS host-based addressing (#313). Echoing that
	// host back would mint a queue URL nobody on the machine can dial.
	srv := helpers.NewTestServer(t)

	// When: a queue is created over the AWS hostname.
	url := createQueueFromHost(t, srv, "sqs.us-east-1.amazonaws.com", "aws-host-queue")

	// Then: the URL falls back to the configured external origin.
	want := srv.Config.ExternalBaseURL() + "/000000000000/aws-host-queue"
	if url != want {
		t.Fatalf("QueueUrl = %q, want %q", url, want)
	}
}

// ---- helpers ---------------------------------------------------------------

func createQueueFromHost(t *testing.T, srv *helpers.TestServer, host, name string) string {
	t.Helper()
	return sqsCallFromHost(t, srv, host, "CreateQueue", map[string]any{"QueueName": name}).QueueUrl
}

type queueURLResult struct {
	QueueUrl string `json:"QueueUrl"`
}

func sqsCallFromHost(t *testing.T, srv *helpers.TestServer, host, action string, body map[string]any) queueURLResult {
	t.Helper()
	resp := sqsCallWithHost(t, srv, host, action, body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result queueURLResult
	helpers.DecodeJSON(t, resp, &result)
	return result
}

// sqsCallWithHost issues an SQS call whose Host header is host, emulating a
// caller that reached Overcast on that origin.
func sqsCallWithHost(t *testing.T, srv *helpers.TestServer, host, action string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s: %v", action, err)
	}
	return resp
}
