package sqs_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestProtocolDispatch_queueLifecycle(t *testing.T) {
	// Given: SQS is running with the typed protocol dispatcher enabled.
	srv := helpers.NewTestServer(t)

	// When: queue lifecycle APIs are called through the JSON protocol.
	createResp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "typed-queue",
	})
	defer createResp.Body.Close()

	// Then: CreateQueue succeeds through the typed path.
	helpers.AssertStatus(t, createResp, http.StatusOK)
	var created struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, createResp, &created)

	getURLResp := sqsCall(t, srv, "GetQueueUrl", map[string]any{
		"QueueName": "typed-queue",
	})
	defer getURLResp.Body.Close()
	helpers.AssertStatus(t, getURLResp, http.StatusOK)

	setResp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl": created.QueueUrl,
		"Attributes": map[string]string{
			"VisibilityTimeout": "45",
		},
	})
	defer setResp.Body.Close()
	helpers.AssertStatus(t, setResp, http.StatusOK)

	attrsResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       created.QueueUrl,
		"AttributeNames": []string{"VisibilityTimeout"},
	})
	defer attrsResp.Body.Close()
	helpers.AssertStatus(t, attrsResp, http.StatusOK)
	var attrs struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrsResp, &attrs)
	if attrs.Attributes["VisibilityTimeout"] != "45" {
		t.Fatalf("expected VisibilityTimeout 45, got %q", attrs.Attributes["VisibilityTimeout"])
	}

	listResp := sqsCall(t, srv, "ListQueues", map[string]any{
		"QueueNamePrefix": "typed",
	})
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)

	sendMessage(t, srv, created.QueueUrl, "delete me")
	purgeResp := sqsCall(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": created.QueueUrl,
	})
	defer purgeResp.Body.Close()
	helpers.AssertStatus(t, purgeResp, http.StatusOK)

	deleteResp := sqsCall(t, srv, "DeleteQueue", map[string]any{
		"QueueUrl": created.QueueUrl,
	})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)
}

func TestProtocolDispatch_messageLifecycle(t *testing.T) {
	// Given: SQS is running with the typed protocol dispatcher enabled.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "typed-message-queue")

	// When: a message is sent and received through JSON protocol dispatch.
	sendResp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "hello",
	})
	defer sendResp.Body.Close()
	helpers.AssertStatus(t, sendResp, http.StatusOK)

	receiveResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
	})
	defer receiveResp.Body.Close()
	helpers.AssertStatus(t, receiveResp, http.StatusOK)

	var received struct {
		Messages []struct {
			ReceiptHandle string `json:"ReceiptHandle"`
			Body          string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, receiveResp, &received)
	if len(received.Messages) != 1 {
		t.Fatalf("expected 1 received message, got %d", len(received.Messages))
	}
	if received.Messages[0].Body != "hello" {
		t.Fatalf("expected body hello, got %q", received.Messages[0].Body)
	}

	visibilityResp := sqsCall(t, srv, "ChangeMessageVisibility", map[string]any{
		"QueueUrl":          queueURL,
		"ReceiptHandle":     received.Messages[0].ReceiptHandle,
		"VisibilityTimeout": 0,
	})
	defer visibilityResp.Body.Close()
	helpers.AssertStatus(t, visibilityResp, http.StatusOK)

	deleteResp := sqsCall(t, srv, "DeleteMessage", map[string]any{
		"QueueUrl":      queueURL,
		"ReceiptHandle": received.Messages[0].ReceiptHandle,
	})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)

	// And: batch send, batch visibility, and batch delete use the typed path too.
	batchSendResp := sqsCall(t, srv, "SendMessageBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{
			{"Id": "a", "MessageBody": "one"},
			{"Id": "b", "MessageBody": "two"},
		},
	})
	defer batchSendResp.Body.Close()
	helpers.AssertStatus(t, batchSendResp, http.StatusOK)

	batchReceiveResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 2,
	})
	defer batchReceiveResp.Body.Close()
	helpers.AssertStatus(t, batchReceiveResp, http.StatusOK)
	var batchReceived struct {
		Messages []struct {
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, batchReceiveResp, &batchReceived)
	if len(batchReceived.Messages) != 2 {
		t.Fatalf("expected 2 batch messages, got %d", len(batchReceived.Messages))
	}

	batchVisibilityResp := sqsCall(t, srv, "ChangeMessageVisibilityBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{
			{"Id": "a", "ReceiptHandle": batchReceived.Messages[0].ReceiptHandle, "VisibilityTimeout": 0},
			{"Id": "b", "ReceiptHandle": batchReceived.Messages[1].ReceiptHandle, "VisibilityTimeout": 0},
		},
	})
	defer batchVisibilityResp.Body.Close()
	helpers.AssertStatus(t, batchVisibilityResp, http.StatusOK)

	batchDeleteResp := sqsCall(t, srv, "DeleteMessageBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{
			{"Id": "a", "ReceiptHandle": batchReceived.Messages[0].ReceiptHandle},
			{"Id": "b", "ReceiptHandle": batchReceived.Messages[1].ReceiptHandle},
		},
	})
	defer batchDeleteResp.Body.Close()
	helpers.AssertStatus(t, batchDeleteResp, http.StatusOK)
}

func TestProtocolDispatch_remainingSQSOperations(t *testing.T) {
	// Given: SQS is running with the typed protocol dispatcher enabled.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "typed-tags-source")
	dlqURL := createQueue(t, srv, "typed-tags-dlq")

	// When: queue tags are written, listed, and removed.
	tagResp := sqsCall(t, srv, "TagQueue", map[string]any{
		"QueueUrl": queueURL,
		"Tags": map[string]string{
			"team": "platform",
		},
	})
	defer tagResp.Body.Close()
	helpers.AssertStatus(t, tagResp, http.StatusOK)

	listTagsResp := sqsCall(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": queueURL,
	})
	defer listTagsResp.Body.Close()
	helpers.AssertStatus(t, listTagsResp, http.StatusOK)
	var tags struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, listTagsResp, &tags)
	if tags.Tags["team"] != "platform" {
		t.Fatalf("expected team tag platform, got %q", tags.Tags["team"])
	}

	untagResp := sqsCall(t, srv, "UntagQueue", map[string]any{
		"QueueUrl": queueURL,
		"TagKeys":  []string{"team"},
	})
	defer untagResp.Body.Close()
	helpers.AssertStatus(t, untagResp, http.StatusOK)

	// And: ListDeadLetterSourceQueues finds queues targeting the DLQ.
	attrsResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       dlqURL,
		"AttributeNames": []string{"QueueArn"},
	})
	defer attrsResp.Body.Close()
	helpers.AssertStatus(t, attrsResp, http.StatusOK)
	var attrs struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrsResp, &attrs)

	policy, err := json.Marshal(map[string]any{
		"deadLetterTargetArn": attrs.Attributes["QueueArn"],
		"maxReceiveCount":     "3",
	})
	if err != nil {
		t.Fatalf("marshal redrive policy: %v", err)
	}

	setResp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl": queueURL,
		"Attributes": map[string]string{
			"RedrivePolicy": string(policy),
		},
	})
	defer setResp.Body.Close()
	helpers.AssertStatus(t, setResp, http.StatusOK)

	listDLQResp := sqsCall(t, srv, "ListDeadLetterSourceQueues", map[string]any{
		"QueueUrl": dlqURL,
	})
	defer listDLQResp.Body.Close()
	helpers.AssertStatus(t, listDLQResp, http.StatusOK)
	var sources struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, listDLQResp, &sources)
	if len(sources.QueueUrls) != 1 || sources.QueueUrls[0] != queueURL {
		t.Fatalf("expected source queue %q, got %#v", queueURL, sources.QueueUrls)
	}

	// And: permission stubs still return the standard not-implemented response.
	addPermResp := sqsCall(t, srv, "AddPermission", map[string]any{})
	defer addPermResp.Body.Close()
	helpers.AssertStatus(t, addPermResp, http.StatusNotImplemented)
	helpers.AssertHeader(t, addPermResp, "x-emulator-unsupported", "true")

	removePermResp := sqsCall(t, srv, "RemovePermission", map[string]any{})
	defer removePermResp.Body.Close()
	helpers.AssertStatus(t, removePermResp, http.StatusNotImplemented)
	helpers.AssertHeader(t, removePermResp, "x-emulator-unsupported", "true")
}

func TestProtocolDispatch_unsupportedProtocol(t *testing.T) {
	// Given: SQS is running with the typed protocol dispatcher enabled.
	srv := helpers.NewTestServer(t)

	// When: a request arrives using AWS JSON 1.1.
	// The emulator treats JSON 1.0 and JSON 1.1 as equivalent for all
	// JSON-tier services (see codec.Supports). SQS accepts either.
	resp := sqsCallWithContentType(t, srv, "CreateQueue", "application/x-amz-json-1.1", map[string]any{
		"QueueName": "json-1-1-queue",
	})
	defer resp.Body.Close()

	// Then: JSON 1.1 is accepted and the queue is created.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var created struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &created)
}
