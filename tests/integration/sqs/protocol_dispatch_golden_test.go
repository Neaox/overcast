package sqs_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestProtocolDispatch_wireBytes(t *testing.T) {
	// Given: a server with always-on typed protocol dispatch.
	srv := helpers.NewTestServer(t)

	// When + Then: deterministic operations return correct JSON responses.
	verifySQSWire(t, srv, "CreateQueue", map[string]any{
		"QueueName":  "wire-source",
		"Attributes": map[string]string{"VisibilityTimeout": "45"},
		"tags":       map[string]string{"team": "platform"},
	})
	verifySQSWire(t, srv, "GetQueueUrl", map[string]any{
		"QueueName": "wire-source",
	})
	verifySQSWire(t, srv, "ListQueues", map[string]any{
		"QueueNamePrefix": "wire",
	})
	verifySQSWire(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/wire-source",
		"Attributes": map[string]string{
			"ReceiveMessageWaitTimeSeconds": "2",
		},
	})
	verifySQSWire(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       "http://127.0.0.1:0/000000000000/wire-source",
		"AttributeNames": []string{"VisibilityTimeout", "ReceiveMessageWaitTimeSeconds"},
	})
	verifySQSWire(t, srv, "TagQueue", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/wire-source",
		"Tags":     map[string]string{"env": "test"},
	})
	verifySQSWire(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/wire-source",
	})
	verifySQSWire(t, srv, "UntagQueue", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/wire-source",
		"TagKeys":  []string{"env"},
	})

	verifySQSWire(t, srv, "CreateQueue", map[string]any{
		"QueueName": "wire-dlq",
	})
	dlqArn := queueArnFromAttributes(t, srv, "wire-dlq")
	setRedrivePolicy(t, srv, "wire-source", dlqArn)
	verifySQSWire(t, srv, "ListDeadLetterSourceQueues", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/wire-dlq",
	})

	verifySQSWire(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/wire-source",
	})
	verifySQSWire(t, srv, "DeleteQueue", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/wire-source",
	})
}

func verifySQSWire(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) {
	t.Helper()

	resp := sqsCall(t, srv, action, body)
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		helpers.AssertStatus(t, resp, http.StatusOK)
	}
}

func queueArnFromAttributes(t *testing.T, srv *helpers.TestServer, queueName string) string {
	t.Helper()
	resp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       "http://127.0.0.1:0/000000000000/" + queueName,
		"AttributeNames": []string{"QueueArn"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var attrs struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &attrs)
	return attrs.Attributes["QueueArn"]
}

func setRedrivePolicy(t *testing.T, srv *helpers.TestServer, queueName, dlqArn string) {
	t.Helper()
	policy, err := json.Marshal(map[string]any{
		"deadLetterTargetArn": dlqArn,
		"maxReceiveCount":     "3",
	})
	if err != nil {
		t.Fatalf("marshal redrive policy: %v", err)
	}
	resp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl": "http://127.0.0.1:0/000000000000/" + queueName,
		"Attributes": map[string]string{
			"RedrivePolicy": string(policy),
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}
