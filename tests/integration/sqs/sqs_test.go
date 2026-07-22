// Package sqs_test contains integration tests for the SQS service emulator.
//
// TDD contract: every handler in internal/services/sqs/ must have a
// corresponding failing test here before implementation begins.
//
// Run: go test ./tests/integration/sqs/...
package sqs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
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

func TestCreateQueue_receiveMessageWaitTimeSecondsValidation(t *testing.T) {
	// Given: requests with invalid queue-level long-poll defaults.
	srv := helpers.NewTestServer(t)
	cases := []struct {
		name  string
		value string
	}{
		{name: "negative", value: "-1"},
		{name: "above maximum", value: "21"},
		{name: "not an integer", value: "soon"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: CreateQueue includes the invalid ReceiveMessageWaitTimeSeconds attribute.
			resp := sqsCall(t, srv, "CreateQueue", map[string]any{
				"QueueName":  "invalid-wait-" + strings.ReplaceAll(tc.name, " ", "-"),
				"Attributes": map[string]string{"ReceiveMessageWaitTimeSeconds": tc.value},
			})
			defer resp.Body.Close()

			// Then: SQS rejects the invalid attribute value.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidAttributeValue")
		})
	}
}

func TestCreateQueue_invalidQueueAttributes(t *testing.T) {
	// Given: queue attributes outside the ranges and enums documented by AWS.
	cases := []struct {
		name      string
		attribute string
		value     string
		errorCode string
		queueName string
	}{
		{name: "unknown attribute", attribute: "BogusAttribute", value: "1", errorCode: "InvalidAttributeName"},
		{name: "delay below minimum", attribute: "DelaySeconds", value: "-1", errorCode: "InvalidAttributeValue"},
		{name: "delay above maximum", attribute: "DelaySeconds", value: "901", errorCode: "InvalidAttributeValue"},
		{name: "maximum message size below minimum", attribute: "MaximumMessageSize", value: "1023", errorCode: "InvalidAttributeValue"},
		{name: "maximum message size above maximum", attribute: "MaximumMessageSize", value: "1048577", errorCode: "InvalidAttributeValue"},
		{name: "message retention below minimum", attribute: "MessageRetentionPeriod", value: "0", errorCode: "InvalidAttributeValue"},
		{name: "message retention above maximum", attribute: "MessageRetentionPeriod", value: "1209601", errorCode: "InvalidAttributeValue"},
		{name: "kms data key reuse below minimum", attribute: "KmsDataKeyReusePeriodSeconds", value: "59", errorCode: "InvalidAttributeValue"},
		{name: "kms data key reuse above maximum", attribute: "KmsDataKeyReusePeriodSeconds", value: "86401", errorCode: "InvalidAttributeValue"},
		{name: "fifo queue not boolean", attribute: "FifoQueue", value: "yes", errorCode: "InvalidAttributeValue"},
		{name: "content based deduplication not boolean", attribute: "ContentBasedDeduplication", value: "yes", errorCode: "InvalidAttributeValue", queueName: "content-dedup-invalid.fifo"},
		{name: "sqs managed sse not boolean", attribute: "SqsManagedSseEnabled", value: "yes", errorCode: "InvalidAttributeValue"},
		{name: "deduplication scope invalid", attribute: "DeduplicationScope", value: "message", errorCode: "InvalidAttributeValue", queueName: "dedup-scope-invalid.fifo"},
		{name: "fifo throughput limit invalid", attribute: "FifoThroughputLimit", value: "perGroup", errorCode: "InvalidAttributeValue", queueName: "throughput-limit-invalid.fifo"},
		{name: "policy not json", attribute: "Policy", value: "not-json", errorCode: "InvalidAttributeValue"},
		{name: "redrive allow policy not json", attribute: "RedriveAllowPolicy", value: "not-json", errorCode: "InvalidAttributeValue"},
		{name: "redrive allow policy invalid permission", attribute: "RedriveAllowPolicy", value: `{"redrivePermission":"maybe"}`, errorCode: "InvalidAttributeValue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a fresh SQS server and an invalid CreateQueue attribute.
			srv := helpers.NewTestServer(t)
			queueName := tc.queueName
			if queueName == "" {
				queueName = "invalid-attr-" + strings.ReplaceAll(tc.name, " ", "-")
			}

			// When: CreateQueue includes the invalid attribute.
			resp := sqsCall(t, srv, "CreateQueue", map[string]any{
				"QueueName":  queueName,
				"Attributes": map[string]string{tc.attribute: tc.value},
			})
			defer resp.Body.Close()

			// Then: SQS rejects the invalid request attribute.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, tc.errorCode)
		})
	}
}

func TestCreateQueue_incompatibleEncryptionAttributes(t *testing.T) {
	// Given: a request enables both SQS-managed SSE and an explicit KMS key.
	srv := helpers.NewTestServer(t)

	// When: CreateQueue includes mutually exclusive server-side encryption attributes.
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "invalid-encryption-queue",
		"Attributes": map[string]string{
			"SqsManagedSseEnabled": "true",
			"KmsMasterKeyId":       "alias/aws/sqs",
		},
	})
	defer resp.Body.Close()

	// Then: SQS rejects the incompatible queue attributes.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidAttributeValue")
}

func TestCreateQueue_invalidName(t *testing.T) {
	// Given: invalid SQS queue names from the documented name constraints.
	cases := []struct {
		name      string
		queueName string
	}{
		{name: "invalid character", queueName: "bad!queue"},
		{name: "too long", queueName: strings.Repeat("a", 81)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := helpers.NewTestServer(t)

			// When: CreateQueue is called with an invalid QueueName.
			resp := sqsCall(t, srv, "CreateQueue", map[string]any{"QueueName": tc.queueName})
			defer resp.Body.Close()

			// Then: SQS rejects the request with an AWS-modeled validation error.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidParameterValue")
		})
	}
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

// AWS returns system Attributes only when the caller requests them via
// AttributeNames (deprecated) or MessageSystemAttributeNames. With no request,
// the Attributes member must be absent.
func TestReceiveMessage_noAttributesRequested_omitsAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attr-omit-queue")
	sendMessage(t, srv, queueURL, "hello!")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []struct {
			Attributes map[string]string `json:"Attributes"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if len(result.Messages[0].Attributes) != 0 {
		t.Errorf("expected no Attributes when none requested, got %v", result.Messages[0].Attributes)
	}
}

// AWS returns system Attributes when AttributeNames=["All"] is requested.
func TestReceiveMessage_allAttributesRequested_includesSystemAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attr-all-queue")
	sendMessage(t, srv, queueURL, "hello!")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
		"AttributeNames":      []string{"All"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []struct {
			Attributes map[string]string `json:"Attributes"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0].Attributes["ApproximateReceiveCount"] != "1" {
		t.Errorf("expected ApproximateReceiveCount=1, got %q", result.Messages[0].Attributes["ApproximateReceiveCount"])
	}
	if result.Messages[0].Attributes["SenderId"] == "" {
		t.Error("expected SenderId to be present when All requested")
	}
}

// AWS returns only the specifically named system attributes.
func TestReceiveMessage_specificAttributeRequested_returnsOnlyThatAttribute(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attr-specific-queue")
	sendMessage(t, srv, queueURL, "hello!")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
		"AttributeNames":      []string{"ApproximateReceiveCount"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []struct {
			Attributes map[string]string `json:"Attributes"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	attrs := result.Messages[0].Attributes
	if attrs["ApproximateReceiveCount"] != "1" {
		t.Errorf("expected ApproximateReceiveCount=1, got %q", attrs["ApproximateReceiveCount"])
	}
	if _, ok := attrs["SentTimestamp"]; ok {
		t.Errorf("expected SentTimestamp to be absent when only ApproximateReceiveCount requested, got %v", attrs)
	}
}

// AWS returns MessageAttributes only when the caller requests them via
// MessageAttributeNames.
func TestReceiveMessage_messageAttributesFilteredByRequest(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "msg-attr-queue")

	// Send a message with a custom message attribute.
	sendResp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "hello!",
		"MessageAttributes": map[string]any{
			"trace": map[string]any{"DataType": "String", "StringValue": "abc-123"},
		},
	})
	sendResp.Body.Close()
	if sendResp.StatusCode != http.StatusOK {
		t.Fatalf("SendMessage: status %d", sendResp.StatusCode)
	}

	// When no MessageAttributeNames requested → MessageAttributes omitted.
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []struct {
			MessageAttributes map[string]any `json:"MessageAttributes"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
	if len(result.Messages[0].MessageAttributes) != 0 {
		t.Errorf("expected no MessageAttributes when none requested, got %v", result.Messages[0].MessageAttributes)
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

func TestReceiveMessage_emptyQueueJSONWire(t *testing.T) {
	// Given: an empty queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "empty-json-wire-queue")

	// When: ReceiveMessage finds no messages using the AWS JSON protocol.
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl": queueURL,
	})
	defer resp.Body.Close()

	// Then: AWS returns a 200 response with no Messages member on the wire.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]json.RawMessage
	helpers.DecodeJSON(t, resp, &result)
	if _, ok := result["Messages"]; ok {
		t.Fatalf("empty ReceiveMessage JSON response included Messages member: %#v", result)
	}
}

func TestReceiveMessage_malformedPersistedMessage(t *testing.T) {
	// Given: an empty queue plus one malformed persisted message record.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "malformed-message-queue")
	if err := srv.Store.Set(context.Background(), "sqs:messages", "us-east-1/malformed-message-queue/bad", "{"); err != nil {
		t.Fatalf("seed malformed message: %v", err)
	}

	// When: ReceiveMessage scans the queue.
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl": queueURL,
	})
	defer resp.Body.Close()

	// Then: the malformed persisted record is isolated and the queue remains usable.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]json.RawMessage
	helpers.DecodeJSON(t, resp, &result)
	if _, ok := result["Messages"]; ok {
		t.Fatalf("expected empty ReceiveMessage response to omit Messages, got %#v", result)
	}
}

func TestReceiveMessage_malformedPersistedQueue(t *testing.T) {
	// Given: the targeted queue record exists but cannot be decoded.
	srv := helpers.NewTestServer(t)
	if err := srv.Store.Set(context.Background(), "sqs:queues", "us-east-1/bad-queue", "{"); err != nil {
		t.Fatalf("seed malformed queue: %v", err)
	}

	// When: ReceiveMessage targets that queue URL.
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl": "http://localhost:4566/000000000000/bad-queue",
	})
	defer resp.Body.Close()

	// Then: the corrupt emulator record is treated as unavailable, not as an
	// InternalError that destabilizes SQS clients.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "AWS.SimpleQueueService.NonExistentQueue")
}

func TestReceiveMessage_emptyQueueQueryWire(t *testing.T) {
	// Given: an empty queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "empty-query-wire-queue")

	// When: ReceiveMessage finds no messages using the AWS Query protocol.
	resp := sqsQueryCall(t, srv, url.Values{
		"Action":   {"ReceiveMessage"},
		"QueueUrl": {queueURL},
	})
	defer resp.Body.Close()

	// Then: the XML result has no Message elements.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if strings.Contains(body, "<Message>") {
		t.Fatalf("empty ReceiveMessage Query response included Message element: %s", body)
	}
}

func TestReceiveMessage_emptyQueueQueryWireViaQueueURL(t *testing.T) {
	// Given: an empty queue whose client-facing URL uses LocalStack's hostname.
	srv := helpers.NewTestServer(t, helpers.WithHostname("localhost.localstack.cloud"))
	queueURL := createQueue(t, srv, "empty-query-wire-qurl-queue")
	u, err := url.Parse(queueURL)
	if err != nil {
		t.Fatalf("parse queue URL: %v", err)
	}

	// When: ReceiveMessage is sent form-encoded to the concrete queue URL path.
	form := url.Values{
		"Action":          {"ReceiveMessage"},
		"QueueUrl":        {queueURL},
		"WaitTimeSeconds": {"1"},
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+u.Path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then: the queue is found and an empty receive succeeds rather than 500ing.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if strings.Contains(body, "<Message>") {
		t.Fatalf("empty ReceiveMessage Query response included Message element: %s", body)
	}
}

func TestReceiveMessage_emptyQueueJSONWireViaQueueURL(t *testing.T) {
	// Given: an empty queue whose client-facing URL uses LocalStack's hostname.
	srv := helpers.NewTestServer(t, helpers.WithHostname("localhost.localstack.cloud"))
	queueURL := createQueue(t, srv, "empty-json-wire-qurl-queue")
	u, err := url.Parse(queueURL)
	if err != nil {
		t.Fatalf("parse queue URL: %v", err)
	}
	body, err := json.Marshal(map[string]any{"QueueUrl": queueURL, "WaitTimeSeconds": 1})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	// When: ReceiveMessage is sent as AWS JSON to the concrete queue URL path.
	req, err := http.NewRequest(http.MethodPost, srv.URL+u.Path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.ReceiveMessage")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then: the queue is found and an empty receive succeeds rather than 500ing.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]json.RawMessage
	helpers.DecodeJSON(t, resp, &result)
	if _, ok := result["Messages"]; ok {
		t.Fatalf("empty ReceiveMessage JSON response included Messages member: %#v", result)
	}
}

func TestReceiveMessage_maxNumberOfMessagesValidation(t *testing.T) {
	// Given: a queue with messages available.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "max-validation-queue")
	for i := 0; i < 2; i++ {
		sendMessage(t, srv, queueURL, fmt.Sprintf("message-%d", i))
	}

	// When: MaxNumberOfMessages is omitted.
	defaultResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 0,
	})
	defer defaultResp.Body.Close()

	// Then: AWS's default of 1 message is used.
	helpers.AssertStatus(t, defaultResp, http.StatusOK)
	var result struct {
		Messages []map[string]any `json:"Messages"`
	}
	helpers.DecodeJSON(t, defaultResp, &result)
	if len(result.Messages) != 1 {
		t.Fatalf("default MaxNumberOfMessages returned %d messages, want 1", len(result.Messages))
	}

	// And: explicit values outside AWS's documented 1..10 range are rejected.
	for _, value := range []int{0, 11} {
		resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            queueURL,
			"MaxNumberOfMessages": value,
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "InvalidParameterValue")
	}

	queryResp := sqsQueryCall(t, srv, url.Values{
		"Action":              {"ReceiveMessage"},
		"QueueUrl":            {queueURL},
		"MaxNumberOfMessages": {"11"},
	})
	defer queryResp.Body.Close()
	helpers.AssertStatus(t, queryResp, http.StatusBadRequest)
	var queryErr struct {
		Error struct {
			Code string `xml:"Code"`
		} `xml:"Error"`
	}
	if err := xml.NewDecoder(queryResp.Body).Decode(&queryErr); err != nil {
		t.Fatalf("decode query error XML: %v", err)
	}
	if queryErr.Error.Code != "InvalidParameterValue" {
		t.Fatalf("query error code = %q, want InvalidParameterValue", queryErr.Error.Code)
	}
}

func TestReceiveMessage_waitTimeSecondsValidation(t *testing.T) {
	// Given: a queue with a message available so invalid waits do not delay the test.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "wait-validation-queue")
	sendMessage(t, srv, queueURL, "message")

	// When + Then: values outside AWS's documented 0..20 second range are rejected.
	for _, value := range []int{-1, 21} {
		resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":        queueURL,
			"WaitTimeSeconds": value,
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "InvalidParameterValue")
	}
}

func TestReceiveMessage_visibilityTimeoutValidation(t *testing.T) {
	// Given: a queue with a message.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "vt-val-queue")
	sendMessage(t, srv, queueURL, "message")

	// When + Then: VisibilityTimeout outside 0..43200 is rejected.
	for _, value := range []int{-1, 43201} {
		resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":          queueURL,
			"VisibilityTimeout": value,
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "InvalidParameterValue")
	}

	// VT=0 and VT=43200 are valid boundary values.
	for _, value := range []int{0, 43200} {
		resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":          queueURL,
			"VisibilityTimeout": value,
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
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

func TestReceiveMessage_visibilityTimeoutZeroMakesMessageVisible(t *testing.T) {
	// Given: a queue with one message.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "vt-zero-queue")
	sendMessage(t, srv, queueURL, "visible-again")

	receive := func() []map[string]any {
		t.Helper()
		resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":          queueURL,
			"VisibilityTimeout": 0,
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, resp, &result)
		return result.Messages
	}

	// When: ReceiveMessage returns the message with VisibilityTimeout=0 twice.
	first := receive()
	second := receive()

	// Then: the first receive does not hide the message from the second receive.
	if len(first) != 1 {
		t.Fatalf("first receive: expected 1 message, got %d", len(first))
	}
	if len(second) != 1 {
		t.Fatalf("second receive: expected same message to be immediately visible, got %d", len(second))
	}
	if second[0]["MessageId"] != first[0]["MessageId"] {
		t.Errorf("expected same MessageId after VisibilityTimeout=0, got %v then %v", first[0]["MessageId"], second[0]["MessageId"])
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

func TestPurgeQueue_messagesWithUnreadablePayloads(t *testing.T) {
	// Given: a queue contains message rows that cannot be decoded.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "purge-unreadable-queue")
	ctx := t.Context()
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("us-east-1/purge-unreadable-queue/msg-%d", i)
		if err := srv.Store.Set(ctx, "sqs:messages", key, "{"); err != nil {
			t.Fatalf("seed unreadable message %d: %v", i, err)
		}
	}

	// When: the queue is purged.
	purgeResp := sqsCall(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": queueURL,
	})
	defer purgeResp.Body.Close()

	// Then: purge succeeds because it only needs message keys, not payloads.
	helpers.AssertStatus(t, purgeResp, http.StatusOK)
	pairs, err := srv.Store.Scan(ctx, "sqs:messages", "us-east-1/purge-unreadable-queue/")
	if err != nil {
		t.Fatalf("scan purged messages: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("expected all unreadable messages to be purged, got %d", len(pairs))
	}
}

func TestPurgeQueue_secondRequestWithinWindow(t *testing.T) {
	// Given: a queue was just purged.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "purge-in-progress-queue")
	purgeResp := sqsCall(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": queueURL,
	})
	defer purgeResp.Body.Close()
	helpers.AssertStatus(t, purgeResp, http.StatusOK)

	// When: PurgeQueue is called again within AWS's 60-second purge window.
	secondResp := sqsCall(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": queueURL,
	})
	defer secondResp.Body.Close()

	// Then: AWS rejects the second request with PurgeQueueInProgress.
	helpers.AssertStatus(t, secondResp, http.StatusBadRequest)
	helpers.AssertJSONError(t, secondResp, "PurgeQueueInProgress")

	// And: the same queue can be purged again after the 60-second window expires.
	srv.Clock.Add(61 * time.Second)
	thirdResp := sqsCall(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": queueURL,
	})
	defer thirdResp.Body.Close()
	helpers.AssertStatus(t, thirdResp, http.StatusOK)
}

func TestPurgeQueue_sendDuringPurgeWindow(t *testing.T) {
	// Given: a queue is in AWS's 60-second purge window.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "purge-send-window-queue")
	purgeResp := sqsCall(t, srv, "PurgeQueue", map[string]any{
		"QueueUrl": queueURL,
	})
	defer purgeResp.Body.Close()
	helpers.AssertStatus(t, purgeResp, http.StatusOK)

	// When: a new message is sent while the purge is still in progress.
	sendMessage(t, srv, queueURL, "message during purge")

	// Then: the send is accepted, but the message is deleted by the purge.
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
		t.Errorf("expected 0 messages during purge, got %s", result.Attributes["ApproximateNumberOfMessages"])
	}

	// And: sends after the purge window are retained normally.
	srv.Clock.Add(61 * time.Second)
	sendMessage(t, srv, queueURL, "message after purge")
	attrResp = sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"ApproximateNumberOfMessages"},
	})
	defer attrResp.Body.Close()
	result = struct {
		Attributes map[string]string `json:"Attributes"`
	}{}
	helpers.DecodeJSON(t, attrResp, &result)
	if result.Attributes["ApproximateNumberOfMessages"] != "1" {
		t.Errorf("expected 1 message after purge window, got %s", result.Attributes["ApproximateNumberOfMessages"])
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

func TestSetQueueAttributes_receiveMessageWaitTimeSecondsValidation(t *testing.T) {
	// Given: a queue exists.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "set-wait-validation-queue")
	cases := []struct {
		name  string
		value string
	}{
		{name: "negative", value: "-1"},
		{name: "above maximum", value: "21"},
		{name: "not an integer", value: "soon"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: SetQueueAttributes sets an invalid queue-level long-poll default.
			resp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
				"QueueUrl": queueURL,
				"Attributes": map[string]string{
					"ReceiveMessageWaitTimeSeconds": tc.value,
				},
			})
			defer resp.Body.Close()

			// Then: SQS rejects the invalid attribute value.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidAttributeValue")
		})
	}
}

func TestSetQueueAttributes_invalidQueueAttributes(t *testing.T) {
	// Given: a queue exists.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "set-attr-validation-queue.fifo")
	cases := []struct {
		name      string
		attribute string
		value     string
		errorCode string
	}{
		{name: "unknown attribute", attribute: "BogusAttribute", value: "1", errorCode: "InvalidAttributeName"},
		{name: "delay above maximum", attribute: "DelaySeconds", value: "901", errorCode: "InvalidAttributeValue"},
		{name: "maximum message size below minimum", attribute: "MaximumMessageSize", value: "1023", errorCode: "InvalidAttributeValue"},
		{name: "message retention below minimum", attribute: "MessageRetentionPeriod", value: "0", errorCode: "InvalidAttributeValue"},
		{name: "kms data key reuse above maximum", attribute: "KmsDataKeyReusePeriodSeconds", value: "86401", errorCode: "InvalidAttributeValue"},
		{name: "content based deduplication not boolean", attribute: "ContentBasedDeduplication", value: "yes", errorCode: "InvalidAttributeValue"},
		{name: "sqs managed sse not boolean", attribute: "SqsManagedSseEnabled", value: "yes", errorCode: "InvalidAttributeValue"},
		{name: "deduplication scope invalid", attribute: "DeduplicationScope", value: "message", errorCode: "InvalidAttributeValue"},
		{name: "fifo throughput limit invalid", attribute: "FifoThroughputLimit", value: "perGroup", errorCode: "InvalidAttributeValue"},
		{name: "policy not json", attribute: "Policy", value: "not-json", errorCode: "InvalidAttributeValue"},
		{name: "redrive allow policy invalid permission", attribute: "RedriveAllowPolicy", value: `{"redrivePermission":"maybe"}`, errorCode: "InvalidAttributeValue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: SetQueueAttributes includes an invalid request attribute.
			resp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
				"QueueUrl": queueURL,
				"Attributes": map[string]string{
					tc.attribute: tc.value,
				},
			})
			defer resp.Body.Close()

			// Then: SQS rejects the invalid request attribute.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, tc.errorCode)
		})
	}
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

func TestListQueues_malformedPersistedQueue(t *testing.T) {
	// Given: one valid queue and one malformed persisted queue record.
	srv := helpers.NewTestServer(t)
	goodURL := createQueue(t, srv, "good-queue")
	if err := srv.Store.Set(context.Background(), "sqs:queues", "us-east-1/bad-queue", "{"); err != nil {
		t.Fatalf("seed malformed queue: %v", err)
	}

	// When: ListQueues scans queue metadata.
	resp := sqsCall(t, srv, "ListQueues", map[string]any{})
	defer resp.Body.Close()

	// Then: the malformed record is skipped and valid queues remain visible.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if !containsStringInSlice(result.QueueUrls, goodURL) {
		t.Fatalf("expected valid queue URL %q, got %#v", goodURL, result.QueueUrls)
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

func TestChangeMessageVisibility_validation(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "cmv-val-queue")
	sendMessage(t, srv, queueURL, "message")

	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer recvResp.Body.Close()
	var r struct {
		Messages []struct{ ReceiptHandle string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &r)
	if len(r.Messages) != 1 {
		t.Fatal("expected 1 message")
	}
	handle := r.Messages[0].ReceiptHandle

	// Valid boundary values: 0 and 43200.
	for _, vt := range []int{0, 43200} {
		resp := sqsCall(t, srv, "ChangeMessageVisibility", map[string]any{
			"QueueUrl":          queueURL,
			"ReceiptHandle":     handle,
			"VisibilityTimeout": vt,
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
	}

	// Out of range: -1 and 43201 are rejected.
	for _, vt := range []int{-1, 43201} {
		resp := sqsCall(t, srv, "ChangeMessageVisibility", map[string]any{
			"QueueUrl":          queueURL,
			"ReceiptHandle":     handle,
			"VisibilityTimeout": vt,
		})
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertJSONError(t, resp, "InvalidParameterValue")
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

// ---- PeekMessages (non-AWS extension) --------------------------------------

func TestPeekMessages_returnsAllIncludingInflight(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "peek-queue")
	sendMessage(t, srv, queueURL, "msg-a")
	sendMessage(t, srv, queueURL, "msg-b")

	// Receive one message with a 60s visibility timeout — it becomes in-flight.
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"VisibilityTimeout":   60,
		"MaxNumberOfMessages": 1,
	})
	defer recvResp.Body.Close()
	helpers.AssertStatus(t, recvResp, http.StatusOK)

	// Peek should return BOTH messages (visible + in-flight).
	peekResp, err := http.Get(srv.URL + queuePath(t, queueURL))
	if err != nil {
		t.Fatalf("peek request: %v", err)
	}
	defer peekResp.Body.Close()
	helpers.AssertStatus(t, peekResp, http.StatusOK)

	var result struct {
		Messages []struct {
			MessageID    string `json:"MessageId"`
			Inflight     bool   `json:"Inflight"`
			VisibleAfter int64  `json:"VisibleAfter"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, peekResp, &result)

	if len(result.Messages) != 2 {
		t.Fatalf("peek: expected 2 messages, got %d", len(result.Messages))
	}

	inflightCount := 0
	for _, m := range result.Messages {
		if m.Inflight {
			inflightCount++
			if m.VisibleAfter == 0 {
				t.Error("in-flight message must have VisibleAfter set")
			}
		}
	}
	if inflightCount != 1 {
		t.Errorf("expected 1 in-flight message, got %d", inflightCount)
	}
}

func TestPeekMessages_doesNotIncrementReceiveCount(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "peek-count-queue")
	sendMessage(t, srv, queueURL, "hello")

	path := queuePath(t, queueURL)

	// Peek three times — must not affect receive count.
	for i := 0; i < 3; i++ {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("peek %d: %v", i, err)
		}
		resp.Body.Close()
	}

	// First real ReceiveMessage should show receive count = 1, not 4.
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"ApproximateReceiveCount"},
	})
	defer recvResp.Body.Close()
	var r struct {
		Messages []struct {
			Attributes map[string]string `json:"Attributes"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &r)
	if len(r.Messages) == 0 {
		t.Fatal("expected 1 message from ReceiveMessage after peek")
	}
	if got := r.Messages[0].Attributes["ApproximateReceiveCount"]; got != "1" {
		t.Errorf("expected ApproximateReceiveCount=1 after first real receive, got %q", got)
	}
}

func TestPeekMessages_queueNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.Get(srv.URL + "/000000000000/no-such-queue")
	if err != nil {
		t.Fatalf("peek request: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// ---- FIFO queues -----------------------------------------------------------

func TestCreateQueue_fifo_success(t *testing.T) {
	// Given: a FIFO queue name (ends in .fifo)
	srv := helpers.NewTestServer(t)

	// When: CreateQueue is called with a FIFO queue name
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "my-queue.fifo",
	})
	defer resp.Body.Close()

	// Then: queue is created successfully
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if !containsString(result.QueueUrl, "my-queue.fifo") {
		t.Errorf("expected QueueUrl to contain 'my-queue.fifo', got %q", result.QueueUrl)
	}

	// Verify FifoQueue attribute is set
	attrResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       result.QueueUrl,
		"AttributeNames": []string{"All"},
	})
	defer attrResp.Body.Close()
	helpers.AssertStatus(t, attrResp, http.StatusOK)
	var attrs struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrResp, &attrs)
	if attrs.Attributes["FifoQueue"] != "true" {
		t.Errorf("expected FifoQueue=true, got %q", attrs.Attributes["FifoQueue"])
	}
}

func TestCreateQueue_fifo_attribute_success(t *testing.T) {
	// Given: a queue name ending in .fifo with FifoQueue attribute
	srv := helpers.NewTestServer(t)

	// When: CreateQueue is called with FifoQueue=true attribute
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName":  "orders.fifo",
		"Attributes": map[string]string{"FifoQueue": "true"},
	})
	defer resp.Body.Close()

	// Then: queue is created
	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestCreateQueue_fifo_missingFifoSuffix(t *testing.T) {
	// Given: a non-.fifo queue name with FifoQueue=true attribute
	srv := helpers.NewTestServer(t)

	// When: CreateQueue is called with FifoQueue=true but no .fifo suffix
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName":  "my-queue",
		"Attributes": map[string]string{"FifoQueue": "true"},
	})
	defer resp.Body.Close()

	// Then: error — FIFO queues must have .fifo suffix
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestSendMessage_fifo_requiresMessageGroupId(t *testing.T) {
	// Given: a FIFO queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "group-test.fifo")

	// When: SendMessage is called without MessageGroupId
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "hello",
	})
	defer resp.Body.Close()

	// Then: error — MessageGroupId is required for FIFO queues
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestSendMessage_fifo_messageOrdering(t *testing.T) {
	// Given: a FIFO queue with several messages in the same group
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "order-test.fifo")

	for i := 0; i < 5; i++ {
		resp := sqsCall(t, srv, "SendMessage", map[string]any{
			"QueueUrl":               queueURL,
			"MessageBody":            fmt.Sprintf("msg-%d", i),
			"MessageGroupId":         "group-a",
			"MessageDeduplicationId": fmt.Sprintf("dedup-%d", i),
		})
		resp.Body.Close()
	}

	// When: messages are received
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 5,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)

	// Then: messages are returned in FIFO order
	for i, m := range result.Messages {
		expected := fmt.Sprintf("msg-%d", i)
		if m.Body != expected {
			t.Errorf("message %d: expected %q, got %q", i, expected, m.Body)
		}
	}
}

func TestSendMessage_fifo_deduplication(t *testing.T) {
	// Given: a FIFO queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "dedup-test.fifo")

	// When: two messages are sent with the same deduplication ID
	for i := 0; i < 2; i++ {
		resp := sqsCall(t, srv, "SendMessage", map[string]any{
			"QueueUrl":               queueURL,
			"MessageBody":            fmt.Sprintf("msg-%d", i),
			"MessageGroupId":         "group-a",
			"MessageDeduplicationId": "same-dedup-id",
		})
		resp.Body.Close()
	}

	// Then: only the first message is stored (deduplication)
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message (dedup), got %d", len(result.Messages))
	}
	if result.Messages[0].Body != "msg-0" {
		t.Errorf("expected first message body, got %q", result.Messages[0].Body)
	}
}

func TestSendMessage_fifo_contentBasedDeduplication(t *testing.T) {
	// Given: a FIFO queue with ContentBasedDeduplication enabled
	srv := helpers.NewTestServer(t)
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName":  "cbd-test.fifo",
		"Attributes": map[string]string{"ContentBasedDeduplication": "true"},
	})
	resp.Body.Close()
	queueURL := createQueue(t, srv, "cbd-test.fifo") // idempotent — returns existing

	// When: two messages are sent with the same body (no explicit dedup ID)
	for i := 0; i < 2; i++ {
		r := sqsCall(t, srv, "SendMessage", map[string]any{
			"QueueUrl":       queueURL,
			"MessageBody":    "same-body",
			"MessageGroupId": "group-a",
		})
		r.Body.Close()
	}

	// Then: only the first message is stored
	rcv := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
	})
	defer rcv.Body.Close()
	helpers.AssertStatus(t, rcv, http.StatusOK)

	var result struct {
		Messages []struct{ Body string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, rcv, &result)
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message (content-based dedup), got %d", len(result.Messages))
	}
}

func TestReceiveMessage_fifo_messageGroupBlocking(t *testing.T) {
	// Given: a FIFO queue with messages in two groups
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "group-block.fifo")

	// Send 2 messages in group-a, then 1 in group-b
	for i := 0; i < 2; i++ {
		resp := sqsCall(t, srv, "SendMessage", map[string]any{
			"QueueUrl":               queueURL,
			"MessageBody":            fmt.Sprintf("a-%d", i),
			"MessageGroupId":         "group-a",
			"MessageDeduplicationId": fmt.Sprintf("a-%d", i),
		})
		resp.Body.Close()
	}
	resp2 := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            "b-0",
		"MessageGroupId":         "group-b",
		"MessageDeduplicationId": "b-0",
	})
	resp2.Body.Close()

	// When: first receive — should get a-0 (first from group-a) and b-0 (first from group-b)
	rcv1 := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
	})
	defer rcv1.Body.Close()
	helpers.AssertStatus(t, rcv1, http.StatusOK)

	var result1 struct {
		Messages []struct {
			Body string `json:"Body"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, rcv1, &result1)

	// Then: should get 2 messages — one from each group (group-a is blocked after first)
	if len(result1.Messages) != 2 {
		t.Fatalf("expected 2 messages (one per group), got %d", len(result1.Messages))
	}

	// The two messages should be a-0 and b-0 (group-a[1] is blocked)
	bodies := map[string]bool{}
	for _, m := range result1.Messages {
		bodies[m.Body] = true
	}
	if !bodies["a-0"] {
		t.Error("expected message a-0 from group-a")
	}
	if !bodies["b-0"] {
		t.Error("expected message b-0 from group-b")
	}
}

func TestReceiveMessage_fifo_receiveRequestAttemptIdRetry(t *testing.T) {
	// Given: a FIFO queue with one visible message.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attempt-retry.fifo")
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            "retry-body",
		"MessageGroupId":         "group-a",
		"MessageDeduplicationId": "attempt-retry-1",
	})
	resp.Body.Close()

	receive := func(body map[string]any) []map[string]any {
		t.Helper()
		resp := sqsCall(t, srv, "ReceiveMessage", body)
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, resp, &result)
		return result.Messages
	}

	// When: the first receive uses a ReceiveRequestAttemptId.
	first := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-1",
		"VisibilityTimeout":       60,
	})

	// Then: it returns the message.
	if len(first) != 1 {
		t.Fatalf("expected first receive to return 1 message, got %d", len(first))
	}

	// When: the same receive is retried with the same attempt ID while the message is invisible.
	second := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-1",
		"VisibilityTimeout":       60,
	})

	// Then: AWS returns the same message and receipt handle for idempotent retry.
	if len(second) != 1 {
		t.Fatalf("expected retry receive to return 1 message, got %d", len(second))
	}
	if second[0]["MessageId"] != first[0]["MessageId"] {
		t.Errorf("expected same MessageId on retry, got %v then %v", first[0]["MessageId"], second[0]["MessageId"])
	}
	if second[0]["ReceiptHandle"] != first[0]["ReceiptHandle"] {
		t.Errorf("expected same ReceiptHandle on retry, got %v then %v", first[0]["ReceiptHandle"], second[0]["ReceiptHandle"])
	}

	// And: a different receive without the attempt ID still sees the message as in-flight.
	third := receive(map[string]any{"QueueUrl": queueURL})
	if len(third) != 0 {
		t.Fatalf("expected receive without attempt ID to return no messages, got %d", len(third))
	}
}

func TestReceiveMessage_fifo_receiveRequestAttemptIdValidation(t *testing.T) {
	// Given: a FIFO queue exists.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attempt-validation.fifo")
	cases := []struct {
		name      string
		attemptID string
	}{
		{name: "too long", attemptID: strings.Repeat("a", 129)},
		{name: "space", attemptID: "bad token"},
		{name: "control character", attemptID: "bad\ntoken"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: ReceiveMessage includes an invalid ReceiveRequestAttemptId.
			resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
				"QueueUrl":                queueURL,
				"ReceiveRequestAttemptId": tc.attemptID,
			})
			defer resp.Body.Close()

			// Then: SQS rejects the invalid request parameter.
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidParameterValue")
		})
	}
}

func TestReceiveMessage_fifo_receiveRequestAttemptId_invalidatedByVisibilityChange(t *testing.T) {
	// Given: a FIFO queue with one visible message.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attempt-inval-vis.fifo")
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            "inval-vis-body",
		"MessageGroupId":         "group-a",
		"MessageDeduplicationId": "inval-vis-1",
	})
	resp.Body.Close()

	receive := func(body map[string]any) []map[string]any {
		t.Helper()
		resp := sqsCall(t, srv, "ReceiveMessage", body)
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, resp, &result)
		return result.Messages
	}

	// When: the first receive uses a ReceiveRequestAttemptId.
	first := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-vis-1",
		"VisibilityTimeout":       60,
	})
	if len(first) != 1 {
		t.Fatalf("expected first receive to return 1 message, got %d", len(first))
	}

	// When: ChangeMessageVisibility is called on the message.
	resp2 := sqsCall(t, srv, "ChangeMessageVisibility", map[string]any{
		"QueueUrl":          queueURL,
		"ReceiptHandle":     first[0]["ReceiptHandle"],
		"VisibilityTimeout": 0,
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// Then: a retry with the same ReceiveRequestAttemptId returns no messages
	// because the message's visibility was modified.
	second := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-vis-1",
	})
	if len(second) != 0 {
		t.Fatalf("expected replay after visibility change to return 0 messages, got %d", len(second))
	}
}

func TestReceiveMessage_fifo_receiveRequestAttemptId_invalidatedByDeleteMessage(t *testing.T) {
	// Given: a FIFO queue with one visible message.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attempt-inval-del.fifo")
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            "inval-del-body",
		"MessageGroupId":         "group-a",
		"MessageDeduplicationId": "inval-del-1",
	})
	resp.Body.Close()

	receive := func(body map[string]any) []map[string]any {
		t.Helper()
		resp := sqsCall(t, srv, "ReceiveMessage", body)
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, resp, &result)
		return result.Messages
	}

	// When: the first receive uses a ReceiveRequestAttemptId.
	first := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-del-1",
		"VisibilityTimeout":       60,
	})
	if len(first) != 1 {
		t.Fatalf("expected first receive to return 1 message, got %d", len(first))
	}

	// When: the message is deleted.
	resp2 := sqsCall(t, srv, "DeleteMessage", map[string]any{
		"QueueUrl":      queueURL,
		"ReceiptHandle": first[0]["ReceiptHandle"],
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// Then: a retry with the same ReceiveRequestAttemptId returns no messages
	// because the message was deleted.
	second := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-del-1",
	})
	if len(second) != 0 {
		t.Fatalf("expected replay after delete to return 0 messages, got %d", len(second))
	}
}

func TestReceiveMessage_fifo_receiveRequestAttemptId_invalidatedByBatchVisibilityChange(t *testing.T) {
	// Given: a FIFO queue with one visible message.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attempt-inval-batch.fifo")
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            "inval-batch-body",
		"MessageGroupId":         "group-a",
		"MessageDeduplicationId": "inval-batch-1",
	})
	resp.Body.Close()

	receive := func(body map[string]any) []map[string]any {
		t.Helper()
		resp := sqsCall(t, srv, "ReceiveMessage", body)
		defer resp.Body.Close()
		helpers.AssertStatus(t, resp, http.StatusOK)
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, resp, &result)
		return result.Messages
	}

	// When: the first receive uses a ReceiveRequestAttemptId.
	first := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-batch-1",
		"VisibilityTimeout":       60,
	})
	if len(first) != 1 {
		t.Fatalf("expected first receive to return 1 message, got %d", len(first))
	}

	// When: ChangeMessageVisibilityBatch is called on the message.
	resp2 := sqsCall(t, srv, "ChangeMessageVisibilityBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{
			{
				"Id":                "1",
				"ReceiptHandle":     first[0]["ReceiptHandle"],
				"VisibilityTimeout": 0,
			},
		},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// Then: a retry with the same ReceiveRequestAttemptId returns no messages
	// because the message's visibility was modified via batch.
	second := receive(map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-batch-1",
	})
	if len(second) != 0 {
		t.Fatalf("expected replay after batch visibility change to return 0 messages, got %d", len(second))
	}
}

func TestSendMessage_fifo_sequenceNumber(t *testing.T) {
	// Given: a FIFO queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "seq-test.fifo")

	// When: messages are sent
	resp1 := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            "first",
		"MessageGroupId":         "g1",
		"MessageDeduplicationId": "d1",
	})
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)

	var r1 struct {
		SequenceNumber string `json:"SequenceNumber"`
	}
	helpers.DecodeJSON(t, resp1, &r1)

	resp2 := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            "second",
		"MessageGroupId":         "g1",
		"MessageDeduplicationId": "d2",
	})
	defer resp2.Body.Close()
	var r2 struct {
		SequenceNumber string `json:"SequenceNumber"`
	}
	helpers.DecodeJSON(t, resp2, &r2)

	// Then: both have sequence numbers and they are increasing
	if r1.SequenceNumber == "" {
		t.Error("expected SequenceNumber on first message")
	}
	if r2.SequenceNumber == "" {
		t.Error("expected SequenceNumber on second message")
	}
	if r1.SequenceNumber >= r2.SequenceNumber {
		t.Errorf("expected increasing sequence numbers, got %s >= %s", r1.SequenceNumber, r2.SequenceNumber)
	}
}

// ---- Queue tags ------------------------------------------------------------

func TestTagQueue_success(t *testing.T) {
	// Given: a queue exists
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "tag-test-queue")

	// When: TagQueue is called with two tags
	resp := sqsCall(t, srv, "TagQueue", map[string]any{
		"QueueUrl": queueURL,
		"Tags":     map[string]string{"project": "overcast", "env": "test"},
	})
	defer resp.Body.Close()

	// Then: 200 OK
	helpers.AssertStatus(t, resp, http.StatusOK)

	// And: ListQueueTags returns the tags
	listResp := sqsCall(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": queueURL,
	})
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)

	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, listResp, &result)
	if result.Tags["project"] != "overcast" {
		t.Errorf("expected tag project=overcast, got %q", result.Tags["project"])
	}
	if result.Tags["env"] != "test" {
		t.Errorf("expected tag env=test, got %q", result.Tags["env"])
	}
}

func TestTagQueue_merge(t *testing.T) {
	// Given: a queue with an existing tag
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "tag-merge-queue")
	resp1 := sqsCall(t, srv, "TagQueue", map[string]any{
		"QueueUrl": queueURL,
		"Tags":     map[string]string{"a": "1"},
	})
	resp1.Body.Close()

	// When: TagQueue is called with additional tags
	resp2 := sqsCall(t, srv, "TagQueue", map[string]any{
		"QueueUrl": queueURL,
		"Tags":     map[string]string{"b": "2"},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	// Then: both tags exist
	listResp := sqsCall(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": queueURL,
	})
	defer listResp.Body.Close()
	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, listResp, &result)
	if len(result.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d: %v", len(result.Tags), result.Tags)
	}
}

func TestTagQueue_queueNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := sqsCall(t, srv, "TagQueue", map[string]any{
		"QueueUrl": srv.URL + "/000000000000/nope",
		"Tags":     map[string]string{"a": "1"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestUntagQueue_success(t *testing.T) {
	// Given: a queue with two tags
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "untag-queue")
	tagResp := sqsCall(t, srv, "TagQueue", map[string]any{
		"QueueUrl": queueURL,
		"Tags":     map[string]string{"project": "overcast", "env": "test"},
	})
	tagResp.Body.Close()

	// When: UntagQueue removes one tag
	resp := sqsCall(t, srv, "UntagQueue", map[string]any{
		"QueueUrl": queueURL,
		"TagKeys":  []string{"env"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: only the remaining tag is present
	listResp := sqsCall(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": queueURL,
	})
	defer listResp.Body.Close()
	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, listResp, &result)
	if _, ok := result.Tags["env"]; ok {
		t.Error("expected 'env' tag to be removed")
	}
	if result.Tags["project"] != "overcast" {
		t.Errorf("expected tag project=overcast, got %q", result.Tags["project"])
	}
}

func TestUntagQueue_queueNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := sqsCall(t, srv, "UntagQueue", map[string]any{
		"QueueUrl": srv.URL + "/000000000000/nope",
		"TagKeys":  []string{"a"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestListQueueTags_empty(t *testing.T) {
	// Given: a queue with no tags
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "no-tags-queue")

	// When: ListQueueTags is called
	resp := sqsCall(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": queueURL,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: empty tags map
	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Tags) != 0 {
		t.Errorf("expected empty tags, got %v", result.Tags)
	}
}

func TestListQueueTags_queueNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := sqsCall(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": srv.URL + "/000000000000/nope",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestCreateQueue_withTags(t *testing.T) {
	// Given/When: a queue is created with tags
	srv := helpers.NewTestServer(t)
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "tagged-at-create",
		"Tags":      map[string]string{"created": "yes"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var createResult struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &createResult)

	// Then: ListQueueTags returns the tags
	listResp := sqsCall(t, srv, "ListQueueTags", map[string]any{
		"QueueUrl": createResult.QueueUrl,
	})
	defer listResp.Body.Close()
	var result struct {
		Tags map[string]string `json:"Tags"`
	}
	helpers.DecodeJSON(t, listResp, &result)
	if result.Tags["created"] != "yes" {
		t.Errorf("expected tag created=yes, got %q", result.Tags["created"])
	}
}

// ---- ChangeMessageVisibilityBatch -----------------------------------------

func TestChangeMessageVisibilityBatch_basic(t *testing.T) {
	// Given two received messages with 300s timeout
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "cmvb-queue")
	sendMessage(t, srv, queueURL, "msg1")
	sendMessage(t, srv, queueURL, "msg2")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 2,
		"VisibilityTimeout":   300,
	})
	defer resp.Body.Close()
	var r struct {
		Messages []struct {
			MessageId     string
			ReceiptHandle string
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &r)
	if len(r.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(r.Messages))
	}

	// When we batch-change both to VisibilityTimeout=0 (make visible immediately)
	batchResp := sqsCall(t, srv, "ChangeMessageVisibilityBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{
			{"Id": "1", "ReceiptHandle": r.Messages[0].ReceiptHandle, "VisibilityTimeout": 0},
			{"Id": "2", "ReceiptHandle": r.Messages[1].ReceiptHandle, "VisibilityTimeout": 0},
		},
	})
	defer batchResp.Body.Close()

	// Then it should succeed
	helpers.AssertStatus(t, batchResp, http.StatusOK)
	var batchResult struct {
		Successful []struct{ Id string } `json:"Successful"`
		Failed     []any                 `json:"Failed"`
	}
	helpers.DecodeJSON(t, batchResp, &batchResult)
	if len(batchResult.Successful) != 2 {
		t.Errorf("expected 2 successful, got %d", len(batchResult.Successful))
	}
	if len(batchResult.Failed) != 0 {
		t.Errorf("expected 0 failed, got %d", len(batchResult.Failed))
	}

	// And both messages are now visible
	checkResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
	})
	defer checkResp.Body.Close()
	var check struct {
		Messages []any `json:"Messages"`
	}
	helpers.DecodeJSON(t, checkResp, &check)
	if len(check.Messages) != 2 {
		t.Errorf("expected 2 messages visible after batch change, got %d", len(check.Messages))
	}
}

func TestChangeMessageVisibilityBatch_partialFailure(t *testing.T) {
	// Given a received message
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "cmvb-fail-queue")
	sendMessage(t, srv, queueURL, "msg1")

	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":          queueURL,
		"VisibilityTimeout": 300,
	})
	defer resp.Body.Close()
	var r struct {
		Messages []struct{ ReceiptHandle string } `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &r)
	if len(r.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(r.Messages))
	}

	// When we batch with one valid and one invalid receipt handle
	batchResp := sqsCall(t, srv, "ChangeMessageVisibilityBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{
			{"Id": "good", "ReceiptHandle": r.Messages[0].ReceiptHandle, "VisibilityTimeout": 0},
			{"Id": "bad", "ReceiptHandle": "invalid-handle", "VisibilityTimeout": 0},
		},
	})
	defer batchResp.Body.Close()

	// Then the valid one succeeds and the invalid one fails
	helpers.AssertStatus(t, batchResp, http.StatusOK)
	var result struct {
		Successful []struct{ Id string } `json:"Successful"`
		Failed     []any                 `json:"Failed"`
	}
	helpers.DecodeJSON(t, batchResp, &result)
	if len(result.Successful) != 1 {
		t.Errorf("expected 1 successful, got %d", len(result.Successful))
	}
	if len(result.Failed) != 1 {
		t.Errorf("expected 1 failed, got %d", len(result.Failed))
	}
}

// ---- Long polling (WaitTimeSeconds) ----------------------------------------

func TestReceiveMessage_longPoll_returnsMessageWhenAvailable(t *testing.T) {
	// Given an empty queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "lp-queue")

	// When we send a message in a background goroutine after a short delay
	go func() {
		time.Sleep(80 * time.Millisecond)
		sendMessage(t, srv, queueURL, "delayed-body")
	}()

	start := time.Now()

	// And we do a long-poll receive with WaitTimeSeconds=2
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":        queueURL,
		"WaitTimeSeconds": 2,
	})
	defer resp.Body.Close()

	elapsed := time.Since(start)

	// Then it should return the message (not wait the full 2 seconds)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []map[string]any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(result.Messages))
	}
	if result.Messages[0]["Body"] != "delayed-body" {
		t.Errorf("expected body %q, got %v", "delayed-body", result.Messages[0]["Body"])
	}
	// Should have returned well before the 2 second deadline
	if elapsed > 1200*time.Millisecond {
		t.Errorf("long poll took too long: %v (expected < 1.2s)", elapsed)
	}
}

func TestReceiveMessage_longPoll_returnsEmptyOnTimeout(t *testing.T) {
	// Given an empty queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "lp-empty-queue")

	start := time.Now()

	// When we do a long-poll receive with WaitTimeSeconds=1 and no messages arrive
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":        queueURL,
		"WaitTimeSeconds": 1,
	})
	defer resp.Body.Close()

	elapsed := time.Since(start)

	// Then it should return empty after waiting ~1 second
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []map[string]any `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Messages) != 0 {
		t.Errorf("expected empty response, got %d messages", len(result.Messages))
	}
	// Should have waited at least 900ms
	if elapsed < 900*time.Millisecond {
		t.Errorf("long poll returned too quickly: %v (expected >= 900ms)", elapsed)
	}
}

func TestReceiveMessage_usesQueueDefaultReceiveMessageWaitTimeSeconds(t *testing.T) {
	// Given: an empty queue with ReceiveMessageWaitTimeSeconds=1.
	srv := helpers.NewTestServer(t)
	queueURL := createQueueWithAttrs(t, srv, "lp-default-queue", map[string]string{
		"ReceiveMessageWaitTimeSeconds": "1",
	})

	// When: ReceiveMessage omits WaitTimeSeconds.
	start := time.Now()
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer resp.Body.Close()
	elapsed := time.Since(start)

	// Then: the queue-level wait default makes it long poll before returning empty.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if elapsed < 900*time.Millisecond {
		t.Errorf("queue-default long poll returned too quickly: %v (expected >= 900ms)", elapsed)
	}

	// When: ReceiveMessage explicitly sets WaitTimeSeconds=0.
	start = time.Now()
	shortResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":        queueURL,
		"WaitTimeSeconds": 0,
	})
	defer shortResp.Body.Close()
	elapsed = time.Since(start)

	// Then: the explicit request value overrides the queue default and short polls.
	helpers.AssertStatus(t, shortResp, http.StatusOK)
	if elapsed > 200*time.Millisecond {
		t.Errorf("explicit short poll took too long: %v (expected < 200ms)", elapsed)
	}
}

func TestReceiveMessage_queryWireUsesQueueDefaultReceiveMessageWaitTimeSeconds(t *testing.T) {
	// Given: an empty queue with ReceiveMessageWaitTimeSeconds=1.
	srv := helpers.NewTestServer(t)
	queueURL := createQueueWithAttrs(t, srv, "lp-query-default-queue", map[string]string{
		"ReceiveMessageWaitTimeSeconds": "1",
	})

	// When: Query ReceiveMessage omits WaitTimeSeconds.
	start := time.Now()
	resp := sqsQueryCall(t, srv, url.Values{
		"Action":   {"ReceiveMessage"},
		"QueueUrl": {queueURL},
	})
	defer resp.Body.Close()
	elapsed := time.Since(start)

	// Then: the queue-level wait default makes it long poll before returning empty.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if elapsed < 900*time.Millisecond {
		t.Errorf("query queue-default long poll returned too quickly: %v (expected >= 900ms)", elapsed)
	}

	// When: Query ReceiveMessage explicitly sets WaitTimeSeconds=0.
	start = time.Now()
	shortResp := sqsQueryCall(t, srv, url.Values{
		"Action":          {"ReceiveMessage"},
		"QueueUrl":        {queueURL},
		"WaitTimeSeconds": {"0"},
	})
	defer shortResp.Body.Close()
	elapsed = time.Since(start)

	// Then: the explicit request value overrides the queue default and short polls.
	helpers.AssertStatus(t, shortResp, http.StatusOK)
	if elapsed > 200*time.Millisecond {
		t.Errorf("query explicit short poll took too long: %v (expected < 200ms)", elapsed)
	}
}

func TestReceiveMessage_noWait_returnsImmediately(t *testing.T) {
	// Given an empty queue
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "nowait-queue")

	start := time.Now()

	// When we receive with WaitTimeSeconds=0 (default)
	resp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":        queueURL,
		"WaitTimeSeconds": 0,
	})
	defer resp.Body.Close()

	elapsed := time.Since(start)

	// Then it should return immediately with no messages
	helpers.AssertStatus(t, resp, http.StatusOK)
	if elapsed > 200*time.Millisecond {
		t.Errorf("short poll took too long: %v (expected < 200ms)", elapsed)
	}
}

// ---- Test helpers ----------------------------------------------------------

// queuePath extracts the URL path from a full SQS queue URL.
// e.g. "http://localhost:0/000000000000/my-queue" → "/000000000000/my-queue"
func queuePath(t *testing.T, queueURL string) string {
	t.Helper()
	u, err := url.Parse(queueURL)
	if err != nil {
		t.Fatalf("parse queue URL %q: %v", queueURL, err)
	}
	return u.Path
}

// sqsCall sends a POST request to the SQS dispatcher with the given action
// and JSON body. It's the low-level HTTP helper — prefer the typed helpers
// (createQueue, sendMessage) for test setup.
func sqsCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return sqsCallWithContentType(t, srv, action, "application/x-amz-json-1.0", body)
}

func sqsCallWithContentType(t *testing.T, srv *helpers.TestServer, action, contentType string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
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

func containsStringInSlice(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ─── Dead Letter Queue ──────────────────────────────────────────────────────

// TestDLQ_attributeStoredAndReturned verifies that a RedrivePolicy set on
// CreateQueue round-trips through GetQueueAttributes unchanged.
func TestDLQ_attributeStoredAndReturned(t *testing.T) {
	// Given: a DLQ and a source queue with a RedrivePolicy pointing at it.
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "my-dlq")

	dlqARN := getQueueARN(t, srv, dlqURL)

	redrivePolicy := `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":3}`
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "my-source",
		"Attributes": map[string]string{
			"RedrivePolicy": redrivePolicy,
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: GetQueueAttributes is called on the source queue.
	srcURL := createQueue(t, srv, "my-source") // idempotent — returns existing URL
	resp2 := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       srcURL,
		"AttributeNames": []string{"All"},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)

	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp2, &result)

	// Then: RedrivePolicy is present and matches what was set.
	if result.Attributes["RedrivePolicy"] != redrivePolicy {
		t.Errorf("expected RedrivePolicy %q, got %q", redrivePolicy, result.Attributes["RedrivePolicy"])
	}
}

// TestDLQ_invalidRedrivePolicy_unknownDLQ verifies that setting a RedrivePolicy
// pointing at a non-existent queue is rejected.
func TestDLQ_invalidRedrivePolicy_unknownDLQ(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "bad-source",
		"Attributes": map[string]string{
			"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:nonexistent","maxReceiveCount":3}`,
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestDLQ_crossRegionRedrivePolicy(t *testing.T) {
	// Given: a DLQ queue that EXISTS locally (name: "same-name-dlq")
	// but the RedrivePolicy ARN references it in a DIFFERENT region.
	// The queue name match would silently succeed without an explicit region check.
	srv := helpers.NewTestServer(t)
	createQueue(t, srv, "same-name-dlq") // exists in us-east-1

	// When: CreateQueue sets a RedrivePolicy pointing to eu-west-1 (wrong region)
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "cross-region-src",
		"Attributes": map[string]string{
			"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:eu-west-1:000000000000:same-name-dlq","maxReceiveCount":3}`,
		},
	})
	defer resp.Body.Close()

	// Then: real AWS rejects cross-region DLQs even when the queue name exists locally
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// TestDLQ_messageMovedAfterMaxReceives verifies that after a message's receive
// count exceeds maxReceiveCount without being deleted, it is moved to the DLQ
// and no longer visible in the source queue.
func TestDLQ_messageMovedAfterMaxReceives(t *testing.T) {
	// Given: a DLQ, a source queue with maxReceiveCount=3, and one message.
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "dlq")
	dlqARN := getQueueARN(t, srv, dlqURL)

	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "source",
		"Attributes": map[string]string{
			"VisibilityTimeout": "0", // so the message becomes visible again immediately
			"RedrivePolicy":     `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":3}`,
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var srcResp struct{ QueueUrl string }
	helpers.DecodeJSON(t, resp, &srcResp)
	srcURL := srcResp.QueueUrl

	sendMessage(t, srv, srcURL, "hello dlq")

	// When: the message is received more than maxReceiveCount times without deleting.
	receiveAll := func() []map[string]any {
		r := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            srcURL,
			"MaxNumberOfMessages": 10,
			"VisibilityTimeout":   0,
		})
		defer r.Body.Close()
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, r, &result)
		return result.Messages
	}

	// Receives 1 through 3 should return the message.
	for i := 1; i <= 3; i++ {
		msgs := receiveAll()
		if len(msgs) == 0 {
			t.Fatalf("receive %d: expected message, got none", i)
		}
	}

	// Receive 4 triggers the move — the source queue returns nothing.
	msgs := receiveAll()
	if len(msgs) != 0 {
		t.Fatalf("receive 4 (last): expected source queue empty after DLQ move, got %d messages", len(msgs))
	}

	// Then: the message must appear in the DLQ.
	dlqMsgs := receiveAll2(t, srv, dlqURL)
	if len(dlqMsgs) == 0 {
		t.Fatal("expected message in DLQ, got none")
	}
}

func TestDLQ_messageMovedAfterReceiveCountExceedsMaxReceiveCount(t *testing.T) {
	// Given: a source queue with maxReceiveCount=2 and a zero visibility timeout.
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "dlq-threshold")
	dlqARN := getQueueARN(t, srv, dlqURL)
	srcURL := createQueueWithAttrs(t, srv, "source-threshold", map[string]string{
		"VisibilityTimeout": "0",
		"RedrivePolicy":     `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":2}`,
	})
	sendMessage(t, srv, srcURL, "threshold body")

	receiveAll := func(queueURL string) []map[string]any {
		t.Helper()
		r := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            queueURL,
			"MaxNumberOfMessages": 10,
			"VisibilityTimeout":   0,
		})
		defer r.Body.Close()
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, r, &result)
		return result.Messages
	}

	// When: the message is received up to maxReceiveCount times.
	first := receiveAll(srcURL)
	second := receiveAll(srcURL)

	// Then: it is still returned on those receives; it has not exceeded the limit yet.
	if len(first) != 1 {
		t.Fatalf("receive 1: expected message, got %d", len(first))
	}
	if len(second) != 1 {
		t.Fatalf("receive 2: expected message before count exceeds maxReceiveCount, got %d", len(second))
	}

	// When: the next receive would exceed maxReceiveCount.
	third := receiveAll(srcURL)

	// Then: the source queue returns no message and the message appears in the DLQ.
	if len(third) != 0 {
		t.Fatalf("receive 3: expected source queue empty after DLQ move, got %d messages", len(third))
	}
	if dlqMsgs := receiveAll(dlqURL); len(dlqMsgs) != 1 {
		t.Fatalf("expected 1 message in DLQ after receive count exceeded maxReceiveCount, got %d", len(dlqMsgs))
	}
}

// TestDLQ_messageMovedAfterMaxReceives_stringMaxReceiveCount is the same as
// TestDLQ_messageMovedAfterMaxReceives but uses maxReceiveCount as a JSON
// string ("3") rather than a number (3).  The AWS CLI serialises it this way;
// the emulator must handle both to avoid silently ignoring the redrive policy.
func TestDLQ_messageMovedAfterMaxReceives_stringMaxReceiveCount(t *testing.T) {
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "dlq-str")
	dlqARN := getQueueARN(t, srv, dlqURL)

	// Note: maxReceiveCount is a quoted string, as the AWS CLI sends it.
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "source-str",
		"Attributes": map[string]string{
			"VisibilityTimeout": "0",
			"RedrivePolicy":     `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":"3"}`,
		},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var srcResp struct{ QueueUrl string }
	helpers.DecodeJSON(t, resp, &srcResp)
	srcURL := srcResp.QueueUrl

	sendMessage(t, srv, srcURL, "hello dlq string")

	receiveAll := func() []map[string]any {
		r := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            srcURL,
			"MaxNumberOfMessages": 10,
			"VisibilityTimeout":   0,
		})
		defer r.Body.Close()
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, r, &result)
		return result.Messages
	}

	for i := 1; i <= 3; i++ {
		msgs := receiveAll()
		if len(msgs) == 0 {
			t.Fatalf("receive %d: expected message, got none", i)
		}
	}

	msgs := receiveAll()
	if len(msgs) != 0 {
		t.Fatalf("receive 4: expected source queue empty after DLQ move, got %d messages", len(msgs))
	}

	dlqMsgs := receiveAll2(t, srv, dlqURL)
	if len(dlqMsgs) == 0 {
		t.Fatal("expected message in DLQ after string-format maxReceiveCount, got none")
	}
}

// TestDLQ_dlqMessageHasDeadLetterAttribute verifies that a message moved to
// the DLQ carries a DeadLetterSourceQueueArn system attribute.
func TestDLQ_dlqMessageHasDeadLetterAttribute(t *testing.T) {
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "dlq2")
	dlqARN := getQueueARN(t, srv, dlqURL)

	createResp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "source2",
		"Attributes": map[string]string{
			"VisibilityTimeout": "0",
			"RedrivePolicy":     `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":2}`,
		},
	})
	defer createResp.Body.Close()
	helpers.AssertStatus(t, createResp, http.StatusOK)
	var srcResp struct{ QueueUrl string }
	helpers.DecodeJSON(t, createResp, &srcResp)
	srcURL := srcResp.QueueUrl

	srcARN := getQueueARN(t, srv, srcURL)
	sendMessage(t, srv, srcURL, "move me")

	// Receive three times (maxReceiveCount=2); third receive triggers the move.
	receiveOnce := func(qURL string) []map[string]any {
		r := sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            qURL,
			"MaxNumberOfMessages": 10,
			"VisibilityTimeout":   0,
			"AttributeNames":      []string{"All"},
		})
		defer r.Body.Close()
		var result struct {
			Messages []map[string]any `json:"Messages"`
		}
		helpers.DecodeJSON(t, r, &result)
		return result.Messages
	}

	receiveOnce(srcURL) // count = 1
	receiveOnce(srcURL) // count = 2
	receiveOnce(srcURL) // count = 3 -> triggers move, returns []

	// Message should be in DLQ with the attribute set.
	dlqMsgs := receiveOnce(dlqURL)
	if len(dlqMsgs) == 0 {
		t.Fatal("expected message in DLQ")
	}
	attrs, ok := dlqMsgs[0]["Attributes"].(map[string]any)
	if !ok {
		t.Fatal("DLQ message has no Attributes")
	}
	got, ok := attrs["DeadLetterSourceQueueArn"].(string)
	if !ok || got != srcARN {
		t.Errorf("expected DeadLetterSourceQueueArn=%q, got %q", srcARN, got)
	}
}

// TestListDeadLetterSourceQueues verifies that ListDeadLetterSourceQueues
// returns all queues whose RedrivePolicy targets the given DLQ.
func TestListDeadLetterSourceQueues(t *testing.T) {
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "shared-dlq")
	dlqARN := getQueueARN(t, srv, dlqURL)

	policy := `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":5}`
	src1 := createQueueWithAttrs(t, srv, "src-a", map[string]string{"RedrivePolicy": policy})
	src2 := createQueueWithAttrs(t, srv, "src-b", map[string]string{"RedrivePolicy": policy})
	_ = createQueue(t, srv, "unrelated") // must NOT appear in the response

	resp := sqsCall(t, srv, "ListDeadLetterSourceQueues", map[string]any{
		"QueueUrl": dlqURL,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, resp, &result)

	found := map[string]bool{}
	for _, u := range result.QueueUrls {
		found[u] = true
	}
	if !found[src1] {
		t.Errorf("expected src-a (%s) in result", src1)
	}
	if !found[src2] {
		t.Errorf("expected src-b (%s) in result", src2)
	}
	if len(result.QueueUrls) != 2 {
		t.Errorf("expected 2 source queues, got %d: %v", len(result.QueueUrls), result.QueueUrls)
	}
}

// TestStartMessageMoveTask_redrivesMessagesBackToSource verifies that calling
// StartMessageMoveTask on a DLQ moves all its messages back to the source queue.
func TestStartMessageMoveTask_redrivesMessagesBackToSource(t *testing.T) {
	// Given: a DLQ with messages that were moved from a source queue.
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "redrive-dlq")
	dlqARN := getQueueARN(t, srv, dlqURL)

	srcURL := createQueueWithAttrs(t, srv, "redrive-source", map[string]string{
		"VisibilityTimeout": "0",
		"RedrivePolicy":     `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":2}`,
	})

	// Send two messages to the source queue.
	sendMessage(t, srv, srcURL, "msg-1")
	sendMessage(t, srv, srcURL, "msg-2")

	// Receive enough times to move both messages to DLQ (maxReceiveCount=2:
	// each message is returned on receives 1 and 2, moved on receive 3).
	for i := 0; i < 10; i++ {
		sqsCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":          srcURL,
			"VisibilityTimeout": 0,
		}).Body.Close()
	}

	// Verify messages are in the DLQ.
	dlqMsgs := receiveAll2(t, srv, dlqURL)
	if len(dlqMsgs) != 2 {
		t.Fatalf("expected 2 messages in DLQ, got %d", len(dlqMsgs))
	}

	// When: StartMessageMoveTask is called on the DLQ.
	resp := sqsCall(t, srv, "StartMessageMoveTask", map[string]any{
		"SourceArn": dlqARN,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		TaskHandle string `json:"TaskHandle"`
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.TaskHandle == "" {
		t.Error("expected TaskHandle to be set")
	}

	// Then: source queue has the messages back (receive count reset to 0,
	// so this receive won't trigger another DLQ move since 1 < maxReceiveCount=2).
	srcMsgs := receiveAll2(t, srv, srcURL)
	if len(srcMsgs) != 2 {
		t.Fatalf("expected 2 messages back in source queue, got %d", len(srcMsgs))
	}

	// And: DLQ is now empty.
	dlqMsgsAfter := receiveAll2(t, srv, dlqURL)
	if len(dlqMsgsAfter) != 0 {
		t.Errorf("expected DLQ to be empty after redrive, got %d messages", len(dlqMsgsAfter))
	}
}

// TestStartMessageMoveTask_missingSourceArn verifies that the operation
// returns an error when SourceArn is not provided.
func TestStartMessageMoveTask_missingSourceArn(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsCall(t, srv, "StartMessageMoveTask", map[string]any{})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

// TestStartMessageMoveTask_multipleSourceQueues verifies that messages from
// different source queues are routed back to their respective origins using
// the per-message DeadLetterSourceQueueArn attribute.
func TestStartMessageMoveTask_multipleSourceQueues(t *testing.T) {
	srv := helpers.NewTestServer(t)
	dlqURL := createQueue(t, srv, "shared-dlq")
	dlqARN := getQueueARN(t, srv, dlqURL)

	// Two source queues pointing at the same DLQ.
	srcAURL := createQueueWithAttrs(t, srv, "source-a", map[string]string{
		"VisibilityTimeout": "0",
		"RedrivePolicy":     `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":2}`,
	})
	srcBURL := createQueueWithAttrs(t, srv, "source-b", map[string]string{
		"VisibilityTimeout": "0",
		"RedrivePolicy":     `{"deadLetterTargetArn":"` + dlqARN + `","maxReceiveCount":2}`,
	})

	// Send one message to each source queue.
	sendMessage(t, srv, srcAURL, "from-a")
	sendMessage(t, srv, srcBURL, "from-b")

	// Receive enough times to move both messages to DLQ.
	for i := 0; i < 10; i++ {
		sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": srcAURL, "VisibilityTimeout": 0}).Body.Close()
		sqsCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": srcBURL, "VisibilityTimeout": 0}).Body.Close()
	}

	// Verify both messages are in the shared DLQ.
	dlqMsgs := receiveAll2(t, srv, dlqURL)
	if len(dlqMsgs) != 2 {
		t.Fatalf("expected 2 messages in DLQ, got %d", len(dlqMsgs))
	}

	// When: StartMessageMoveTask without DestinationArn — should route per-message.
	resp := sqsCall(t, srv, "StartMessageMoveTask", map[string]any{"SourceArn": dlqARN})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: each source queue has exactly its message back.
	msgsA := receiveAll2(t, srv, srcAURL)
	msgsB := receiveAll2(t, srv, srcBURL)

	if len(msgsA) != 1 {
		t.Errorf("expected 1 message in source-a, got %d", len(msgsA))
	} else if body, _ := msgsA[0]["Body"].(string); body != "from-a" {
		t.Errorf("expected source-a message body 'from-a', got %q", body)
	}

	if len(msgsB) != 1 {
		t.Errorf("expected 1 message in source-b, got %d", len(msgsB))
	} else if body, _ := msgsB[0]["Body"].(string); body != "from-b" {
		t.Errorf("expected source-b message body 'from-b', got %q", body)
	}
}

// ─── DLQ test helpers ──────────────────────────────────────────────────────

func getQueueARN(t *testing.T, srv *helpers.TestServer, queueURL string) string {
	t.Helper()
	resp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"QueueArn"},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getQueueARN: status %d", resp.StatusCode)
	}
	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, resp, &result)
	arn := result.Attributes["QueueArn"]
	if arn == "" {
		t.Fatal("getQueueARN: empty QueueArn")
	}
	return arn
}

func createQueueWithAttrs(t *testing.T, srv *helpers.TestServer, name string, attrs map[string]string) string {
	t.Helper()
	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName":  name,
		"Attributes": attrs,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("createQueueWithAttrs %q: status %d", name, resp.StatusCode)
	}
	var result struct{ QueueUrl string }
	helpers.DecodeJSON(t, resp, &result)
	return result.QueueUrl
}

func receiveAll2(t *testing.T, srv *helpers.TestServer, queueURL string) []map[string]any {
	t.Helper()
	r := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
	})
	defer r.Body.Close()
	var result struct {
		Messages []map[string]any `json:"Messages"`
	}
	helpers.DecodeJSON(t, r, &result)
	return result.Messages
}

// ---- Hostname override -----------------------------------------------------

func TestCreateQueue_honorsHostname(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithHostname("overcast.local"))

	resp := sqsCall(t, srv, "CreateQueue", map[string]any{
		"QueueName": "hostname-q",
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		QueueUrl string `json:"QueueUrl"`
	}
	helpers.DecodeJSON(t, resp, &result)

	if !containsString(result.QueueUrl, "overcast.local") {
		t.Errorf("expected QueueUrl to contain 'overcast.local', got %q", result.QueueUrl)
	}
	if containsString(result.QueueUrl, "localhost") {
		t.Errorf("expected QueueUrl NOT to contain 'localhost', got %q", result.QueueUrl)
	}
}

// ---- SQS Query protocol (form-encoded Action=) -----------------------------
// These tests verify the legacy AWS Query protocol works alongside the JSON protocol.

// sqsQueryCall sends a form-encoded SQS query request (Action=...) and returns the raw response.
func sqsQueryCall(t *testing.T, srv *helpers.TestServer, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do query request: %v", err)
	}
	return resp
}

// queryXMLResult is a generic wrapper for SQS Query XML responses.
type queryXMLResult struct {
	XMLName xml.Name
	Inner   []byte `xml:",innerxml"`
}

func TestQueryProtocol_CreateQueue(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":    {"CreateQueue"},
		"QueueName": {"query-test-queue"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	if ct := resp.Header.Get("Content-Type"); ct != "text/xml" {
		t.Errorf("expected Content-Type text/xml, got %q", ct)
	}

	// Parse XML to verify structure.
	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	if raw.XMLName.Local != "CreateQueueResponse" {
		t.Errorf("expected root element CreateQueueResponse, got %s", raw.XMLName.Local)
	}
	// Body should contain QueueUrl and RequestId.
	body := string(raw.Inner)
	if !strings.Contains(body, "QueueUrl") {
		t.Errorf("expected QueueUrl in response, got: %s", body)
	}
	if !strings.Contains(body, "RequestId") {
		t.Errorf("expected RequestId in response, got: %s", body)
	}
}

func TestQueryProtocol_GetQueueUrl(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create queue via JSON protocol first.
	queueURL := createQueue(t, srv, "query-geturl-test")

	// Retrieve via Query protocol.
	resp := sqsQueryCall(t, srv, url.Values{
		"Action":    {"GetQueueUrl"},
		"QueueName": {"query-geturl-test"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if !strings.Contains(body, queueURL) {
		t.Errorf("expected body to contain queue URL %q, got: %s", queueURL, body)
	}
}

func TestQueryProtocol_SendMessage(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-send-test")

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":      {"SendMessage"},
		"QueueUrl":    {queueURL},
		"MessageBody": {"hello from query protocol"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if !strings.Contains(body, "MessageId") {
		t.Errorf("expected MessageId in response, got: %s", body)
	}
	if !strings.Contains(body, "MD5OfMessageBody") {
		t.Errorf("expected MD5OfMessageBody in response, got: %s", body)
	}
}

func TestQueryProtocol_ReceiveMessage(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-recv-test")

	// Send a message via JSON.
	sendMessage(t, srv, queueURL, "query-recv-body")

	// Receive via Query protocol.
	resp := sqsQueryCall(t, srv, url.Values{
		"Action":              {"ReceiveMessage"},
		"QueueUrl":            {queueURL},
		"MaxNumberOfMessages": {"10"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if !strings.Contains(body, "query-recv-body") {
		t.Errorf("expected message body in response, got: %s", body)
	}
	if !strings.Contains(body, "ReceiptHandle") {
		t.Errorf("expected ReceiptHandle in response, got: %s", body)
	}
}

func TestQueryProtocol_ListQueues(t *testing.T) {
	srv := helpers.NewTestServer(t)
	createQueue(t, srv, "query-list-alpha")
	createQueue(t, srv, "query-list-beta")

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":          {"ListQueues"},
		"QueueNamePrefix": {"query-list-"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if !strings.Contains(body, "query-list-alpha") {
		t.Errorf("expected alpha queue in response, got: %s", body)
	}
	if !strings.Contains(body, "query-list-beta") {
		t.Errorf("expected beta queue in response, got: %s", body)
	}
}

func TestQueryProtocol_DeleteMessage(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-delmsg-test")

	// Send a message, receive it to get a receipt handle.
	sendMessage(t, srv, queueURL, "to-delete")
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
	})
	defer recvResp.Body.Close()
	var recvResult struct {
		Messages []struct {
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &recvResult)
	if len(recvResult.Messages) == 0 {
		t.Fatal("expected at least one message")
	}

	// Delete via Query protocol.
	resp := sqsQueryCall(t, srv, url.Values{
		"Action":        {"DeleteMessage"},
		"QueueUrl":      {queueURL},
		"ReceiptHandle": {recvResult.Messages[0].ReceiptHandle},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)
}

func TestQueryProtocol_DeleteQueue(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-delqueue-test")

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":   {"DeleteQueue"},
		"QueueUrl": {queueURL},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify queue no longer exists.
	getResp := sqsQueryCall(t, srv, url.Values{
		"Action":    {"GetQueueUrl"},
		"QueueName": {"query-delqueue-test"},
	})
	defer getResp.Body.Close()

	if getResp.StatusCode == http.StatusOK {
		t.Error("expected queue to be deleted")
	}
}

func TestQueryProtocol_GetQueueAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-attrs-test")

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":          {"GetQueueAttributes"},
		"QueueUrl":        {queueURL},
		"AttributeName.1": {"All"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if !strings.Contains(body, "VisibilityTimeout") {
		t.Errorf("expected VisibilityTimeout attribute, got: %s", body)
	}
}

// TestQueryProtocol_GetQueueAttributes_viaQueueURL verifies that a
// form-encoded (Query protocol) request sent directly to the queue URL path
// (/{accountID}/{queueName}) is handled correctly — the same as AWS SDK v1
// behaviour.
func TestQueryProtocol_GetQueueAttributes_viaQueueURL(t *testing.T) {
	// Given: a queue created via JSON protocol.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-attrs-qurl-test")

	// When: GetQueueAttributes is sent form-encoded to the queue URL path
	// (not the root "/"). AWS SDK v1 sends to the queue URL.
	u, err := url.Parse(queueURL)
	if err != nil {
		t.Fatalf("parse queue URL: %v", err)
	}
	form := url.Values{
		"Action":          {"GetQueueAttributes"},
		"QueueUrl":        {queueURL},
		"AttributeName.1": {"All"},
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+u.Path,
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// Then: 200 OK with XML response containing the queue attributes.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "text/xml" {
		t.Errorf("expected Content-Type text/xml, got %q", ct)
	}
	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if !strings.Contains(body, "VisibilityTimeout") {
		t.Errorf("expected VisibilityTimeout attribute in XML, got: %s", body)
	}
	if !strings.Contains(body, "QueueArn") {
		t.Errorf("expected QueueArn attribute in XML, got: %s", body)
	}
}

func TestQueryProtocol_ErrorResponse(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":    {"GetQueueUrl"},
		"QueueName": {"nonexistent-queue-xyz"},
	})
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected error for nonexistent queue")
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/xml" {
		t.Errorf("expected Content-Type text/xml for error, got %q", ct)
	}

	// Parse error XML.
	type errorResp struct {
		XMLName xml.Name
		Error   struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	var errResult errorResp
	if err := xml.NewDecoder(resp.Body).Decode(&errResult); err != nil {
		t.Fatalf("decode error XML: %v", err)
	}
	if errResult.XMLName.Local != "ErrorResponse" {
		t.Errorf("expected ErrorResponse root, got %s", errResult.XMLName.Local)
	}
	if errResult.Error.Code == "" {
		t.Error("expected error code in response")
	}
}

func TestQueryProtocol_InvalidAction(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsQueryCall(t, srv, url.Values{
		"Action": {"BogusAction"},
	})
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected error for bogus action")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/xml" {
		t.Errorf("expected text/xml Content-Type for error, got %q", ct)
	}
}

func TestQueryProtocol_CreateQueueWithAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":            {"CreateQueue"},
		"QueueName":         {"query-attrs-queue"},
		"Attribute.1.Name":  {"VisibilityTimeout"},
		"Attribute.1.Value": {"60"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	// Verify the attribute was set.
	var raw queryXMLResult
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode XML: %v", err)
	}
	body := string(raw.Inner)
	if !strings.Contains(body, "QueueUrl") {
		t.Errorf("expected QueueUrl, got: %s", body)
	}

	// Check the attribute via JSON protocol.
	attrResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       strings.TrimSpace(extractXMLElement(body, "QueueUrl")),
		"AttributeNames": []string{"VisibilityTimeout"},
	})
	defer attrResp.Body.Close()
	helpers.AssertStatus(t, attrResp, http.StatusOK)

	var attrs struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrResp, &attrs)
	if attrs.Attributes["VisibilityTimeout"] != "60" {
		t.Errorf("expected VisibilityTimeout=60, got %q", attrs.Attributes["VisibilityTimeout"])
	}
}

func TestQueryProtocol_SendMessageWithAttributes(t *testing.T) {
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-msgattr-test")

	resp := sqsQueryCall(t, srv, url.Values{
		"Action":                               {"SendMessage"},
		"QueueUrl":                             {queueURL},
		"MessageBody":                          {"attr test"},
		"MessageAttribute.1.Name":              {"mykey"},
		"MessageAttribute.1.Value.DataType":    {"String"},
		"MessageAttribute.1.Value.StringValue": {"myval"},
	})
	defer resp.Body.Close()

	helpers.AssertStatus(t, resp, http.StatusOK)

	// Receive and verify the attribute.
	recvResp := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":              queueURL,
		"MaxNumberOfMessages":   1,
		"MessageAttributeNames": []string{"All"},
	})
	defer recvResp.Body.Close()
	helpers.AssertStatus(t, recvResp, http.StatusOK)

	var recvResult struct {
		Messages []struct {
			MessageAttributes map[string]struct {
				DataType    string `json:"DataType"`
				StringValue string `json:"StringValue"`
			} `json:"MessageAttributes"`
		} `json:"Messages"`
	}
	helpers.DecodeJSON(t, recvResp, &recvResult)
	if len(recvResult.Messages) == 0 {
		t.Fatal("expected at least one message")
	}
	attr, ok := recvResult.Messages[0].MessageAttributes["mykey"]
	if !ok {
		t.Fatal("expected message attribute 'mykey'")
	}
	if attr.StringValue != "myval" {
		t.Errorf("expected StringValue=myval, got %q", attr.StringValue)
	}
}

// extractXMLElement is a test helper that extracts the text content of a simple XML element.
func extractXMLElement(xmlBody, element string) string {
	start := strings.Index(xmlBody, "<"+element+">")
	if start == -1 {
		return ""
	}
	start += len("<" + element + ">")
	end := strings.Index(xmlBody[start:], "</"+element+">")
	if end == -1 {
		return ""
	}
	return xmlBody[start : start+end]
}
