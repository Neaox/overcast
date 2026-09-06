// batch_validation_test.go covers the request-level validation SQS applies to
// every *Batch operation, the MD5OfMessageAttributes digest a client verifies
// its attributes with, and the GetQueueAttributes gaps reported alongside them
// in #1719.
package sqs_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ---- Batch entry limits ----------------------------------------------------

// sendEntries builds n SendMessageBatch entries with distinct ids.
func sendEntries(n int) []map[string]any {
	out := make([]map[string]any, 0, n)
	for i := range n {
		out = append(out, map[string]any{
			"Id":          fmt.Sprintf("id-%d", i),
			"MessageBody": fmt.Sprintf("body-%d", i),
		})
	}
	return out
}

func TestSendMessageBatch_tenEntriesSucceed(t *testing.T) {
	// Given: a queue and a batch of exactly ten entries — AWS's maximum, and
	// the boundary an over-eager limit check would break.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "batch-ten")

	// When: the batch is sent.
	resp := sqsCall(t, srv, "SendMessageBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries":  sendEntries(10),
	})
	defer resp.Body.Close()

	// Then: every entry succeeds and every message is enqueued.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Successful []struct{ Id, MessageId string }
		Failed     []map[string]any
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Successful) != 10 {
		t.Fatalf("Successful = %d entries, want 10", len(result.Successful))
	}
	if len(result.Failed) != 0 {
		t.Errorf("Failed = %v, want none", result.Failed)
	}
	assertApproximateMessages(t, srv, queueURL, 10)
}

// batchLimitCase is one rejected batch shape, shared by all three operations
// because AWS applies the same limits and codes to each of them.
type batchLimitCase struct {
	name       string
	count      int
	duplicate  bool
	wantCode   string
	wantLegacy string
}

func batchLimitCases() []batchLimitCase {
	return []batchLimitCase{
		{
			name:       "no entries",
			count:      0,
			wantCode:   "EmptyBatchRequest",
			wantLegacy: "AWS.SimpleQueueService.EmptyBatchRequest",
		},
		{
			name:       "eleven entries",
			count:      11,
			wantCode:   "TooManyEntriesInBatchRequest",
			wantLegacy: "AWS.SimpleQueueService.TooManyEntriesInBatchRequest",
		},
		{
			name:       "duplicate entry ids",
			count:      2,
			duplicate:  true,
			wantCode:   "BatchEntryIdsNotDistinct",
			wantLegacy: "AWS.SimpleQueueService.BatchEntryIdsNotDistinct",
		},
	}
}

func TestSendMessageBatch_entryLimits(t *testing.T) {
	for _, tc := range batchLimitCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a queue and a batch violating one of AWS's limits.
			srv := helpers.NewTestServer(t)
			queueURL := createQueue(t, srv, "batch-limits")
			entries := sendEntries(tc.count)
			if tc.duplicate {
				entries[1]["Id"] = entries[0]["Id"]
			}

			// When: the batch is sent.
			resp := sqsCall(t, srv, "SendMessageBatch", map[string]any{
				"QueueUrl": queueURL,
				"Entries":  entries,
			})
			defer resp.Body.Close()

			// Then: the request is rejected with the documented code, and the
			// whole batch fails — nothing was enqueued.
			assertBatchRejected(t, resp, tc.wantCode, tc.wantLegacy)
			assertApproximateMessages(t, srv, queueURL, 0)
		})
	}
}

func TestDeleteMessageBatch_entryLimits(t *testing.T) {
	for _, tc := range batchLimitCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a queue holding one received message, and a delete batch
			// violating one of AWS's limits.
			srv := helpers.NewTestServer(t)
			queueURL := createQueue(t, srv, "delete-batch-limits")
			sendMessage(t, srv, queueURL, "keep-me")
			handle := receiveOneHandle(t, srv, queueURL)

			entries := make([]map[string]any, 0, tc.count)
			for i := range tc.count {
				entries = append(entries, map[string]any{
					"Id":            fmt.Sprintf("id-%d", i),
					"ReceiptHandle": handle,
				})
			}
			if tc.duplicate {
				entries[1]["Id"] = entries[0]["Id"]
			}

			// When: the batch is sent.
			resp := sqsCall(t, srv, "DeleteMessageBatch", map[string]any{
				"QueueUrl": queueURL,
				"Entries":  entries,
			})
			defer resp.Body.Close()

			// Then: the request is rejected, and the message is still there —
			// a rejected batch deletes nothing.
			assertBatchRejected(t, resp, tc.wantCode, tc.wantLegacy)
			assertApproximateMessages(t, srv, queueURL, 1)
		})
	}
}

func TestChangeMessageVisibilityBatch_entryLimits(t *testing.T) {
	for _, tc := range batchLimitCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a queue holding one received message, and a visibility
			// batch violating one of AWS's limits.
			srv := helpers.NewTestServer(t)
			queueURL := createQueue(t, srv, "visibility-batch-limits")
			sendMessage(t, srv, queueURL, "in-flight")
			handle := receiveOneHandle(t, srv, queueURL)

			entries := make([]map[string]any, 0, tc.count)
			for i := range tc.count {
				entries = append(entries, map[string]any{
					"Id":                fmt.Sprintf("id-%d", i),
					"ReceiptHandle":     handle,
					"VisibilityTimeout": 0,
				})
			}
			if tc.duplicate {
				entries[1]["Id"] = entries[0]["Id"]
			}

			// When: the batch is sent.
			resp := sqsCall(t, srv, "ChangeMessageVisibilityBatch", map[string]any{
				"QueueUrl": queueURL,
				"Entries":  entries,
			})
			defer resp.Body.Close()

			// Then: the request is rejected outright, so the message stays
			// invisible rather than being released by a partially applied batch.
			assertBatchRejected(t, resp, tc.wantCode, tc.wantLegacy)
			if handles := receiveHandles(t, srv, queueURL); len(handles) != 0 {
				t.Errorf("received %d messages after a rejected visibility batch, want 0", len(handles))
			}
		})
	}
}

// TestQueryProtocol_SendMessageBatch_tooManyEntries pins the other half of the
// error-code spelling: under the legacy Query protocol AWS answers with the
// "AWS.SimpleQueueService."-prefixed code in the XML body itself, where the
// JSON protocol carries the modeled shape name in __type and the legacy code
// only in x-amzn-query-error.
func TestQueryProtocol_SendMessageBatch_tooManyEntries(t *testing.T) {
	// Given: a queue, reached over the Query protocol.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "query-batch-limits")

	form := url.Values{
		"Action":   {"SendMessageBatch"},
		"QueueUrl": {queueURL},
	}
	for i := range 11 {
		n := strconv.Itoa(i + 1)
		form.Set("SendMessageBatchRequestEntry."+n+".Id", "id-"+n)
		form.Set("SendMessageBatchRequestEntry."+n+".MessageBody", "body-"+n)
	}

	// When: an eleven-entry batch is sent.
	resp := sqsQueryCall(t, srv, form)
	defer resp.Body.Close()

	// Then: the XML error carries the legacy code the Query protocol binds.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "AWS.SimpleQueueService.TooManyEntriesInBatchRequest")
}

// ---- MD5OfMessageAttributes ------------------------------------------------

// wantAttrMD5 is the digest of the attribute set both tests below send, per
// AWS's documented message-attribute digest algorithm.
const wantAttrMD5 = "3bc3f392bdd1097ba0b434f65d468d2e"

func messageAttributes() map[string]any {
	return map[string]any{
		"attr1": map[string]any{"DataType": "String", "StringValue": "value1"},
	}
}

func TestSendMessage_md5OfMessageAttributes(t *testing.T) {
	// Given: a queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attr-md5")

	// When: a message with attributes is sent.
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":          queueURL,
		"MessageBody":       "hello",
		"MessageAttributes": messageAttributes(),
	})
	defer resp.Body.Close()

	// Then: the response carries the attribute digest alongside the body one.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		MD5OfMessageBody       string
		MD5OfMessageAttributes string
	}
	helpers.DecodeJSON(t, resp, &result)
	if result.MD5OfMessageAttributes != wantAttrMD5 {
		t.Errorf("MD5OfMessageAttributes = %q, want %q", result.MD5OfMessageAttributes, wantAttrMD5)
	}
	if result.MD5OfMessageBody == "" {
		t.Error("MD5OfMessageBody is empty")
	}
}

func TestSendMessage_md5OfMessageAttributesOmittedWithoutAttributes(t *testing.T) {
	// Given: a queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attr-md5-absent")

	// When: a message with no attributes is sent.
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": "hello",
	})
	defer resp.Body.Close()

	// Then: AWS omits the attribute digest entirely rather than sending an
	// empty or zero-length one.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result map[string]any
	helpers.DecodeJSON(t, resp, &result)
	if _, ok := result["MD5OfMessageAttributes"]; ok {
		t.Errorf("MD5OfMessageAttributes present for a message with no attributes: %v", result)
	}
}

func TestSendMessageBatch_md5OfMessageAttributes(t *testing.T) {
	// Given: a queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "batch-attr-md5")

	// When: a batch entry carries message attributes.
	resp := sqsCall(t, srv, "SendMessageBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries": []map[string]any{{
			"Id":                "e1",
			"MessageBody":       "hello",
			"MessageAttributes": messageAttributes(),
		}},
	})
	defer resp.Body.Close()

	// Then: the successful entry carries the attribute digest.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Successful []struct {
			Id                     string
			MD5OfMessageBody       string
			MD5OfMessageAttributes string
		}
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Successful) != 1 {
		t.Fatalf("Successful = %d entries, want 1", len(result.Successful))
	}
	if got := result.Successful[0].MD5OfMessageAttributes; got != wantAttrMD5 {
		t.Errorf("MD5OfMessageAttributes = %q, want %q", got, wantAttrMD5)
	}

	// And: the attributes really were stored, so the digest describes a message
	// a receiver can actually get back.
	received := sqsCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":              queueURL,
		"MessageAttributeNames": []string{"All"},
	})
	defer received.Body.Close()
	var got struct {
		Messages []map[string]any
	}
	helpers.DecodeJSON(t, received, &got)
	if len(got.Messages) != 1 {
		t.Fatalf("received %d messages, want 1", len(got.Messages))
	}
	attrs, ok := got.Messages[0]["MessageAttributes"].(map[string]any)
	if !ok {
		t.Fatalf("MessageAttributes missing from the received message: %v", got.Messages[0])
	}
	if _, ok := attrs["attr1"]; !ok {
		t.Errorf("MessageAttributes = %v, want it to carry attr1", attrs)
	}
}

func TestSendMessageBatch_md5OfMessageAttributesOmittedWithoutAttributes(t *testing.T) {
	// Given: a queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "batch-attr-md5-absent")

	// When: a batch entry carries no attributes.
	resp := sqsCall(t, srv, "SendMessageBatch", map[string]any{
		"QueueUrl": queueURL,
		"Entries":  []map[string]any{{"Id": "e1", "MessageBody": "hello"}},
	})
	defer resp.Body.Close()

	// Then: the entry omits the attribute digest, as AWS does.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Successful []map[string]any
	}
	helpers.DecodeJSON(t, resp, &result)
	if len(result.Successful) != 1 {
		t.Fatalf("Successful = %d entries, want 1", len(result.Successful))
	}
	if _, ok := result.Successful[0]["MD5OfMessageAttributes"]; ok {
		t.Errorf("MD5OfMessageAttributes present for an entry with no attributes: %v", result.Successful[0])
	}
}

// ---- GetQueueAttributes ----------------------------------------------------

func TestGetQueueAttributes_allIncludesTimestampsAndDelayedCount(t *testing.T) {
	// Given: a queue holding one visible message and one still delayed.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attrs-all")
	sendMessage(t, srv, queueURL, "visible")
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":     queueURL,
		"MessageBody":  "delayed",
		"DelaySeconds": 60,
	})
	resp.Body.Close()

	// When: every attribute is requested.
	attrs := getQueueAttributes(t, srv, queueURL, "All")

	// Then: the three attributes AWS's All includes are present, and the
	// delayed count reflects the message still serving its DelaySeconds.
	for _, name := range []string{"CreatedTimestamp", "LastModifiedTimestamp", "ApproximateNumberOfMessagesDelayed"} {
		if attrs[name] == "" {
			t.Errorf("attribute %s missing from All; got %v", name, attrs)
		}
	}
	if attrs["ApproximateNumberOfMessagesDelayed"] != "1" {
		t.Errorf("ApproximateNumberOfMessagesDelayed = %q, want %q", attrs["ApproximateNumberOfMessagesDelayed"], "1")
	}
	if attrs["ApproximateNumberOfMessages"] != "1" {
		t.Errorf("ApproximateNumberOfMessages = %q, want %q", attrs["ApproximateNumberOfMessages"], "1")
	}
	if _, err := strconv.ParseInt(attrs["CreatedTimestamp"], 10, 64); err != nil {
		t.Errorf("CreatedTimestamp = %q, want a Unix timestamp: %v", attrs["CreatedTimestamp"], err)
	}
}

func TestGetQueueAttributes_lastModifiedTimestampMovesOnSetQueueAttributes(t *testing.T) {
	// Given: a queue created at a known time.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "attrs-modified")
	created := getQueueAttributes(t, srv, queueURL, "All")["LastModifiedTimestamp"]

	// When: an attribute is changed an hour later.
	srv.AdvanceClock(time.Hour)
	resp := sqsCall(t, srv, "SetQueueAttributes", map[string]any{
		"QueueUrl":   queueURL,
		"Attributes": map[string]string{"VisibilityTimeout": "60"},
	})
	resp.Body.Close()

	// Then: LastModifiedTimestamp moves with it while CreatedTimestamp does not.
	after := getQueueAttributes(t, srv, queueURL, "All")
	if after["LastModifiedTimestamp"] == created {
		t.Errorf("LastModifiedTimestamp = %q, unchanged after SetQueueAttributes", created)
	}
	if after["CreatedTimestamp"] != created {
		t.Errorf("CreatedTimestamp = %q, want it to stay %q", after["CreatedTimestamp"], created)
	}
}

func TestGetQueueAttributes_unknownAttributeName(t *testing.T) {
	// Given: a queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attrs-unknown")

	// When: an attribute name AWS does not model is requested.
	resp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"VisibilityTimeout", "NoSuchAttribute"},
	})
	defer resp.Body.Close()

	// Then: the request is rejected rather than answered with whatever matched.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidAttributeName")
	helpers.AssertRequestID(t, resp)
}

func TestGetQueueAttributes_allAnywhereInTheList(t *testing.T) {
	// Given: a queue.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attrs-all-second")

	// When: "All" is requested after a specific name rather than first.
	attrs := getQueueAttributes(t, srv, queueURL, "VisibilityTimeout", "All")

	// Then: every attribute comes back, not just the named one.
	if attrs["QueueArn"] == "" {
		t.Errorf("All requested second returned only the named attributes: %v", attrs)
	}
}

// ---- Test helpers ----------------------------------------------------------

// assertBatchRejected checks a whole-batch rejection: the modeled shape name in
// the JSON body's __type, and the legacy Query-protocol code in the
// x-amzn-query-error header AWS sends alongside it for awsQueryCompatible
// services.
func assertBatchRejected(t *testing.T, resp *http.Response, wantCode, wantLegacy string) {
	t.Helper()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertRequestID(t, resp)
	if got, want := resp.Header.Get("x-amzn-query-error"), wantLegacy+";Sender"; got != want {
		t.Errorf("x-amzn-query-error = %q, want %q", got, want)
	}
	helpers.AssertJSONError(t, resp, wantCode)
}

// assertApproximateMessages checks the queue holds the expected number of
// messages, which is how these tests prove a rejected batch applied nothing.
func assertApproximateMessages(t *testing.T, srv *helpers.TestServer, queueURL string, want int) {
	t.Helper()
	attrs := getQueueAttributes(t, srv, queueURL, "All")
	total := attrs["ApproximateNumberOfMessages"] + "+" + attrs["ApproximateNumberOfMessagesNotVisible"]
	visible, _ := strconv.Atoi(attrs["ApproximateNumberOfMessages"])
	notVisible, _ := strconv.Atoi(attrs["ApproximateNumberOfMessagesNotVisible"])
	if visible+notVisible != want {
		t.Errorf("queue holds %s messages, want %d", total, want)
	}
}

func getQueueAttributes(t *testing.T, srv *helpers.TestServer, queueURL string, names ...string) map[string]string {
	t.Helper()
	resp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": names,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GetQueueAttributes: status %d", resp.StatusCode)
	}
	var result struct {
		Attributes map[string]string
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Attributes
}

// receiveHandles receives up to ten messages and returns their receipt handles.
func receiveHandles(t *testing.T, srv *helpers.TestServer, queueURL string) []string {
	t.Helper()
	var handles []string
	for _, m := range receiveAll2(t, srv, queueURL) {
		handle, _ := m["ReceiptHandle"].(string)
		handles = append(handles, handle)
	}
	return handles
}

func receiveOneHandle(t *testing.T, srv *helpers.TestServer, queueURL string) string {
	t.Helper()
	handles := receiveHandles(t, srv, queueURL)
	if len(handles) != 1 {
		t.Fatalf("received %d messages, want 1", len(handles))
	}
	return handles[0]
}
