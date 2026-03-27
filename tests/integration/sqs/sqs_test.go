// Package sqs_test contains integration tests for the SQS service emulator.
//
// TDD contract: every handler in internal/services/sqs/ must have a
// corresponding failing test here before implementation begins.
//
// Run: go test ./tests/integration/sqs/...
package sqs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/your-org/overcast/tests/helpers"
)

// ---- CreateQueue -----------------------------------------------------------

func TestCreateQueue_success(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "my-queue",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.QueueUrl == "" {
		t.Error("expected QueueUrl to be set")
	}
	if !containsString(result.QueueUrl, "my-queue") {
		t.Errorf("expected QueueUrl to contain 'my-queue', got %q", result.QueueUrl)
	}
}

func TestCreateQueue_missingName(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "CreateQueue", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateQueue_idempotent(t *testing.T) {
	srv := helpers.NewTestServer(t)
	url1 := createQueue(t, srv, "idempotent-queue")
	url2 := createQueue(t, srv, "idempotent-queue")

	// Creating the same queue twice should return the same URL.
	if url1 != url2 {
		t.Errorf("expected same queue URL on idempotent create, got %q and %q", url1, url2)
	}
}

// ---- GetQueueUrl -----------------------------------------------------------

func TestGetQueueUrl_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	want := createQueue(t, srv, "my-queue")

	resp := sqsCall(t, srv, "GetQueueUrl", map[string]any{
		"QueueName": "my-queue",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.QueueUrl != want {
		t.Errorf("expected QueueUrl %q, got %q", want, result.QueueUrl)
	}
}

func TestGetQueueUrl_notFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "GetQueueUrl", map[string]any{
		"QueueName": "no-such-queue",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "AWS.SimpleQueueService.NonExistentQueue")
}

// ---- SendMessage -----------------------------------------------------------

func TestSendMessage_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "my-queue")

	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "hello world",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		MessageId        string `json:"MessageId"`
		MD5OfMessageBody string `json:"MD5OfMessageBody"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.MessageId == "" {
		t.Error("expected MessageId to be set")
	}
	if result.MD5OfMessageBody == "" {
		t.Error("expected MD5OfMessageBody to be set")
	}
}

func TestSendMessage_md5IsCorrect(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "md5-queue")

	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "test",
	})
	defer resp.Body.Close()

	var result struct {
		MD5OfMessageBody string `json:"MD5OfMessageBody"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// md5("test") = 098f6bcd4621d373cade4e832627b4f6
	expected := "098f6bcd4621d373cade4e832627b4f6"
	if result.MD5OfMessageBody != expected {
		t.Errorf("expected MD5 %q, got %q", expected, result.MD5OfMessageBody)
	}
}

func TestSendMessage_queueNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    "http://localhost:4566/000000000000/no-such-queue",
		"MessageBody": "hello",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- ReceiveMessage --------------------------------------------------------

func TestReceiveMessage_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "recv-queue")
	sendMessage(t, srv, queueURL, "hello!")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []struct {
			MessageId     string `json:"MessageId"`
			Body          string `json:"Body"`
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0].Body != "hello!" {
		t.Errorf("expected body 'hello!', got %q", result.Messages[0].Body)
	}
	if result.Messages[0].ReceiptHandle == "" {
		t.Error("expected ReceiptHandle to be set")
	}
}

func TestReceiveMessage_emptyQueue(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "empty-queue")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl": queueURL,
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if len(result.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result.Messages))
	}
}

func TestReceiveMessage_visibilityTimeout(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "vt-queue")
	sendMessage(t, srv, queueURL, "once")

	// Receive the message once — it becomes invisible.
	resp1 := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 60,
	})
	var r1 struct {
		Messages []struct{ Body string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp1, &r1)
	if len(r1.Messages) != 1 {
		t.Fatalf("first receive: expected 1 message, got %d", len(r1.Messages))
	}

	// Receive again — should get 0 messages because the first is still invisible.
	resp2 := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl": queueURL,
	})
	defer resp2.Body.Close()
	var r2 struct {
		Messages []struct{ Body string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp2, &r2)
	if len(r2.Messages) != 0 {
		t.Errorf("second receive: expected 0 messages (visibility timeout), got %d", len(r2.Messages))
	}
}

// ---- DeleteMessage ---------------------------------------------------------

func TestDeleteMessage_success(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "del-queue")
	sendMessage(t, srv, queueURL, "to be deleted")

	// Receive to get the receipt handle.
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 0, // expire immediately so we can receive again
	})
	var recvResult struct {
		Messages []struct {
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &recvResult)
	if len(recvResult.Messages) == 0 {
		t.Fatal("expected to receive a message")
	}

	// Delete.
	delResp := sqsCall(t, srv, "DeleteMessage", map[string]any{
		"QueueUrl":      queueURL,
		"ReceiptHandle": recvResult.Messages[0].ReceiptHandle,
	})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusOK)

	// Verify queue is now empty.
	// Advance the mock clock so the visibility timeout (0s) has elapsed.
	srv.Clock.Add(1 * time.Second)
	checkResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl": queueURL,
	})
	defer checkResp.Body.Close()
	var checkResult struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, checkResp, &checkResult)
	if len(checkResult.Messages) != 0 {
		t.Errorf("expected queue to be empty after delete, got %d messages", len(checkResult.Messages))
	}
}

// ---- GetQueueAttributes ----------------------------------------------------

func TestGetQueueAttributes_returnsDefaults(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attr-queue")

	resp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"All"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if result.Attributes["VisibilityTimeout"] != "30" {
		t.Errorf("expected VisibilityTimeout 30, got %q", result.Attributes["VisibilityTimeout"])
	}
	if result.Attributes["QueueArn"] == "" {
		t.Error("expected QueueArn to be set")
	}
}

// ---- PurgeQueue ------------------------------------------------------------

func TestPurgeQueue_clearsAllMessages(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "purge-queue")

	for i := 0; i < 5; i++ {
		sendMessage(t, srv, queueURL, "message")
	}

	purgeResp := sqsCall(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": queueURL,
	})
	defer purgeResp.Body.Close()
	helpers.AssertStatus(t, purgeResp, http.StatusOK)

	// Verify queue is empty.
	attrResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"ApproximateNumberOfMessages"},
	})
	defer attrResp.Body.Close()
	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrResp, &result)
	if result.Attributes["ApproximateNumberOfMessages"] != "0" {
		t.Errorf("expected 0 messages after purge, got %s", result.Attributes["ApproximateNumberOfMessages"])
	}
}

// ---- SetQueueAttributes ----------------------------------------------------

func TestSetQueueAttributes_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attr-queue")

	resp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl": queueURL,
		"Attributes": map[string]string{
			"VisibilityTimeout": "60",
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Read it back.
	attrResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"VisibilityTimeout"},
	})
	defer attrResp.Body.Close()
	helpers.AssertStatus(t, attrResp, http.StatusOK)

	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrResp, &result)
	if result.Attributes["VisibilityTimeout"] != "60" {
		t.Errorf("expected VisibilityTimeout=60, got %q", result.Attributes["VisibilityTimeout"])
	}
}

func TestSetQueueAttributes_queueNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl":   "http://sqs.us-east-1.amazonaws.com/000000000000/no-such-queue",
		"Attributes": map[string]string{"VisibilityTimeout": "30"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "AWS.SimpleQueueService.NonExistentQueue")
}

// ---- DeleteQueue -----------------------------------------------------------

func TestDeleteQueue_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "delete-me")

	resp := sqsCall(t, srv, "DeleteQueue", map[string]any{
		"QueueUrl": queueURL,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Queue should no longer be found.
	resp2 := sqsCall(t, srv, "GetQueueUrl", map[string]any{"QueueName": "delete-me"})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp2, "AWS.SimpleQueueService.NonExistentQueue")
}

// ---- ListQueues ------------------------------------------------------------

func TestListQueues_empty(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "ListQueues", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.QueueUrls) != 0 {
		t.Errorf("expected 0 queues, got %d", len(result.QueueUrls))
	}
}

func TestListQueues_returnsAll(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createQueue(t, srv, "queue-alpha")
	createQueue(t, srv, "queue-beta")
	createQueue(t, srv, "queue-gamma")

	resp := sqsCall(t, srv, "ListQueues", map[string]any{})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.QueueUrls) != 3 {
		t.Errorf("expected 3 queues, got %d", len(result.QueueUrls))
	}
}

func TestListQueues_withPrefix(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createQueue(t, srv, "prod-alpha")
	createQueue(t, srv, "prod-beta")
	createQueue(t, srv, "dev-gamma")

	resp := sqsCall(t, srv, "ListQueues", map[string]any{
		"QueueNamePrefix": "prod",
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.QueueUrls) != 2 {
		t.Errorf("expected 2 queues, got %d", len(result.QueueUrls))
	}
}

// ---- SendMessageBatch ------------------------------------------------------

func TestSendMessageBatch_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "batch-queue")

	resp := sqsCall(t, srv, "SendMessageBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{
			{"Id": "1", "MessageBody": "hello"},
			{"Id": "2", "MessageBody": "world"},
		},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Successful []struct {
			Id        string `json:"Id"`
			MessageId string `json:"MessageId"`
		} `json:"Successful"`
		Failed []any `json:"Failed"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Successful) != 2 {
		t.Errorf("expected 2 successful, got %d", len(result.Successful))
	}
	if len(result.Failed) != 0 {
		t.Errorf("expected 0 failed, got %d", len(result.Failed))
	}
	for _, s := range result.Successful {
		if s.MessageId == "" {
			t.Errorf("expected MessageId to be set for id %q", s.Id)
		}
	}
}

func TestSendMessageBatch_queueNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "SendMessageBatch", map[string]any{
		"QueueUrl": "http://sqs.us-east-1.amazonaws.com/000000000000/no-such-queue",
		"Entries":  []map[string]any{{"Id": "1", "MessageBody": "hello"}},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "AWS.SimpleQueueService.NonExistentQueue")
}

// ---- DeleteMessageBatch ----------------------------------------------------

func TestDeleteMessageBatch_success(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "dmbatch-queue")

	// Send two messages.
	msgID1 := sendMessage(t, srv, queueURL, "msg1")
	msgID2 := sendMessage(t, srv, queueURL, "msg2")
	_ = msgID1
	_ = msgID2

	// Receive them to get receipt handles.
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
	})
	defer recvResp.Body.Close()
	helpers.AssertStatus(t, recvResp, http.StatusOK)

	var received struct {
		Messages []struct {
			MessageId     string `json:"MessageId"`
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &received)
	if len(received.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(received.Messages))
	}

	entries := make([]map[string]any, len(received.Messages))
	for i, m := range received.Messages {
		entries[i] = map[string]any{
			"Id":            m.MessageId,
			"ReceiptHandle": m.ReceiptHandle,
		}
	}

	resp := sqsCall(t, srv, "DeleteMessageBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries":  entries,
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Successful []struct{ Id string } `json:"Successful"`
		Failed     []any                 `json:"Failed"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Successful) != 2 {
		t.Errorf("expected 2 successful deletes, got %d", len(result.Successful))
	}
}

// ---- Request ID is present on every response --------------------------------

func TestEveryResponse_hasRequestID(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cases := []struct {
		action string
		body   map[string]any
	}{
		{"CreateQueue", map[string]any{"QueueName": "req-id-test"}},
		{"GetQueueUrl", map[string]any{"QueueName": "no-such-queue"}}, // error response
	}

	for _, tc := range cases {
		resp := sqsCall(t, srv, tc.action, tc.body)
		resp.Body.Close()
		helpers.AssertRequestID(t, resp)
	}
}

// ---- Receipt handle --------------------------------------------------------

func TestReceiptHandle_isBase64Opaque(t *testing.T) {
	// Given a sent message
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "handle-queue")
	sendMessage(t, srv, queueURL, "body")

	// When we receive it
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 60,
	})
	defer resp.Body.Close()
	var result struct {
		Messages []struct {
			MessageId     string `json:"MessageId"`
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}

	// Then the receipt handle is a non-empty base64 string (not just the message ID)
	handle := result.Messages[0].ReceiptHandle
	msgID := result.Messages[0].MessageId
	if handle == "" {
		t.Fatal("expected non-empty receipt handle")
	}
	if handle == msgID {
		t.Error("receipt handle must not equal message ID")
	}
	// base64 characters only
	for _, c := range handle {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			t.Errorf("receipt handle contains non-base64 character %q: %s", c, handle)
			break
		}
	}
}

func TestReceiptHandle_uniquePerReceive(t *testing.T) {
	// Given an in-flight message whose timeout we reset to 0 to allow re-receive
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "unique-handle-queue")
	sendMessage(t, srv, queueURL, "body")

	// First receive
	resp1 := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 0,
	})
	defer resp1.Body.Close()
	var r1 struct {
		Messages []struct{ ReceiptHandle string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp1, &r1)
	if len(r1.Messages) != 1 {
		t.Fatalf("first receive: expected 1 message")
	}
	handle1 := r1.Messages[0].ReceiptHandle

	// Advance past the 0-second visibility timeout
	srv.Clock.Add(1 * time.Second)

	// Second receive — message is visible again, should yield a new handle
	resp2 := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 0,
	})
	defer resp2.Body.Close()
	var r2 struct {
		Messages []struct{ ReceiptHandle string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp2, &r2)
	if len(r2.Messages) != 1 {
		t.Fatalf("second receive: expected 1 message")
	}
	handle2 := r2.Messages[0].ReceiptHandle

	if handle1 == handle2 {
		t.Error("expected different receipt handle on second receive")
	}
}

func TestDeleteMessage_staleHandleRejected(t *testing.T) {
	// Given a message received twice (handle1 is now stale)
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "stale-queue")
	sendMessage(t, srv, queueURL, "body")

	resp1 := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 0,
	})
	defer resp1.Body.Close()
	var r1 struct {
		Messages []struct{ ReceiptHandle string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp1, &r1)
	staleHandle := r1.Messages[0].ReceiptHandle

	srv.Clock.Add(1 * time.Second)

	// Receive again — creates a new current handle
	resp2 := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 60,
	})
	defer resp2.Body.Close()

	// Deleting with the stale handle should fail
	delResp := sqsCall(t, srv, "DeleteMessage", map[string]any{
		"QueueUrl":      queueURL,
		"ReceiptHandle": staleHandle,
	})
	defer delResp.Body.Close()
	helpers.AssertStatus(t, delResp, http.StatusBadRequest)
	helpers.AssertJSONError(t, delResp, "ReceiptHandleIsInvalid")
}

func TestDeleteMessage_invalidHandleRejected(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "invalid-handle-queue")

	resp := sqsCall(t, srv, "DeleteMessage", map[string]any{
		"QueueUrl":      queueURL,
		"ReceiptHandle": "not-a-valid-receipt-handle",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ReceiptHandleIsInvalid")
}

// ---- MessageAttributes -----------------------------------------------------

func TestSendReceive_messageAttributesRoundtrip(t *testing.T) {
	// Given a message with string and number attributes
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attrs-queue")

	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "attributed",
		"MessageAttributes": map[string]any{
			"Color": map[string]any{
				"DataType":    "String",
				"StringValue": "blue",
			},
			"Count": map[string]any{
				"DataType":    "Number",
				"StringValue": "42",
			},
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When we receive the message
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":              queueURL,
		"MessageAttributeNames": []string{"All"},
	})
	defer recvResp.Body.Close()
	helpers.AssertStatus(t, recvResp, http.StatusOK)

	var result struct {
		Messages []struct {
			Body              string `json:"Body"`
			MessageAttributes map[string]struct {
				DataType    string `json:"DataType"`
				StringValue string `json:"StringValue"`
			} `json:"MessageAttributes"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &result)

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	msg := result.Messages[0]
	if msg.Body != "attributed" {
		t.Errorf("expected body 'attributed', got %q", msg.Body)
	}
	if msg.MessageAttributes["Color"].StringValue != "blue" {
		t.Errorf("expected Color=blue, got %q", msg.MessageAttributes["Color"].StringValue)
	}
	if msg.MessageAttributes["Count"].StringValue != "42" {
		t.Errorf("expected Count=42, got %q", msg.MessageAttributes["Count"].StringValue)
	}
}

// ---- ChangeMessageVisibility -----------------------------------------------

func TestChangeMessageVisibility_extendsTimeout(t *testing.T) {
	// Given a received message with a short timeout
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "cmv-queue")
	sendMessage(t, srv, queueURL, "body")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 5,
	})
	defer resp.Body.Close()
	var r struct {
		Messages []struct{ ReceiptHandle string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &r)
	if len(r.Messages) != 1 {
		t.Fatalf("expected 1 message")
	}
	handle := r.Messages[0].ReceiptHandle

	// Advance 3 seconds — still within the 5s timeout, so message is invisible
	srv.Clock.Add(3 * time.Second)

	// Extend the timeout to 60 more seconds from now
	cmvResp := sqsCall(t, srv, "ChangeMessageVisibility", map[string]any{
		"QueueUrl":          queueURL,
		"ReceiptHandle":     handle,
		"VisibilityTimeout": 60,
	})
	defer cmvResp.Body.Close()
	helpers.AssertStatus(t, cmvResp, http.StatusOK)

	// Advance 10 more seconds — message should still be invisible (60s timeout)
	srv.Clock.Add(10 * time.Second)
	checkResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer checkResp.Body.Close()
	var check struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, checkResp, &check)
	if len(check.Messages) != 0 {
		t.Errorf("expected message still invisible after ChangeMessageVisibility, got %d", len(check.Messages))
	}
}

func TestChangeMessageVisibility_zeroMakesVisible(t *testing.T) {
	// Given an in-flight message
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "cmv-zero-queue")
	sendMessage(t, srv, queueURL, "body")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 300,
	})
	defer resp.Body.Close()
	var r struct {
		Messages []struct{ ReceiptHandle string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &r)
	handle := r.Messages[0].ReceiptHandle

	// Set VisibilityTimeout to 0 — message becomes immediately visible
	sqsCall(t, srv, "ChangeMessageVisibility", map[string]any{
		"QueueUrl":          queueURL,
		"ReceiptHandle":     handle,
		"VisibilityTimeout": 0,
	})

	checkResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer checkResp.Body.Close()
	var check struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, checkResp, &check)
	if len(check.Messages) != 1 {
		t.Errorf("expected message visible after ChangeMessageVisibility(0), got %d", len(check.Messages))
	}
}

// ---- Queue default VisibilityTimeout ---------------------------------------

func TestReceiveMessage_usesQueueDefaultVisibilityTimeout(t *testing.T) {
	// Given a queue with VisibilityTimeout=5
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "default-vt-queue")
	sqsCall(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl":   queueURL,
		"Attributes": map[string]string{"VisibilityTimeout": "5"},
	})
	sendMessage(t, srv, queueURL, "body")

	// When we receive without specifying VisibilityTimeout
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer resp.Body.Close()
	var r struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &r)
	if len(r.Messages) != 1 {
		t.Fatalf("expected 1 message on first receive")
	}

	// Then within first 5s the message is invisible
	srv.Clock.Add(3 * time.Second)
	resp2 := sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer resp2.Body.Close()
	var r2 struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp2, &r2)
	if len(r2.Messages) != 0 {
		t.Errorf("expected message invisible within queue default timeout, got %d", len(r2.Messages))
	}

	// And after 5s it reappears
	srv.Clock.Add(3 * time.Second)
	resp3 := sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer resp3.Body.Close()
	var r3 struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp3, &r3)
	if len(r3.Messages) != 1 {
		t.Errorf("expected message visible after queue default timeout expired, got %d", len(r3.Messages))
	}
}

// ---- Test helpers ----------------------------------------------------------

// sqsCall sends a POST request to the SQS dispatcher with the given action
// and JSON body. It's the low-level HTTP helper — prefer the typed helpers
// (createQueue, sendMessage) for test setup.
func sqsCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS."+action)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request %s: %v", action, err)
	}
	return resp
}

func createQueue(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{"QueueName": name})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createQueue %q: status %d", name, resp.StatusCode)
	}
	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.QueueUrl
}

func sendMessage(t *testing.T, srv *helpers.TestServer, queueURL, body string) string {
	t.Helper()
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": body,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sendMessage: status %d", resp.StatusCode)
	}
	var result struct {
		MessageId string `json:"MessageId"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.MessageId
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
