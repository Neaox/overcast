// ReceiveMessage semantics that the AWS docs spell out but that the happy-path
// tests in sqs_test.go do not reach: FIFO batch retrieval across and within
// message groups, the five-minute ReceiveRequestAttemptId deduplication window,
// and the in-flight/visibility rules a per-receive VisibilityTimeout implies.
//
// Sources:
//   - https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html
//   - https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/FIFO-queues-understanding-logic.html
package sqs_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// receivedFifoMessage is the projection of a ReceiveMessage result these tests
// assert on: body, handle, and the message group the message belongs to.
type receivedFifoMessage struct {
	Body          string            `json:"Body"`
	MessageId     string            `json:"MessageId"`
	ReceiptHandle string            `json:"ReceiptHandle"`
	Attributes    map[string]string `json:"Attributes"`
}

// receiveFifo issues a ReceiveMessage that asks for MessageGroupId back so the
// caller can assert per-group ordering.
func receiveFifo(t *testing.T, srv *helpers.TestServer, body map[string]any) []receivedFifoMessage {
	t.Helper()
	if _, ok := body["MessageSystemAttributeNames"]; !ok {
		body["MessageSystemAttributeNames"] = []string{"MessageGroupId"}
	}
	resp := sqsCall(t, srv, "ReceiveMessage", body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Messages []receivedFifoMessage `json:"Messages"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.Messages
}

// sendFifoMessage sends one message to a FIFO queue with an explicit
// deduplication ID so repeated bodies are not collapsed.
func sendFifoMessage(t *testing.T, srv *helpers.TestServer, queueURL, group, body string) {
	t.Helper()
	resp := sqsCall(t, srv, "SendMessage", map[string]any{
		"QueueUrl":               queueURL,
		"MessageBody":            body,
		"MessageGroupId":         group,
		"MessageDeduplicationId": group + "/" + body,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sendFifoMessage %s/%s: status %d", group, body, resp.StatusCode)
	}
}

// bodiesForGroup returns, in the order they were received, the bodies of the
// messages belonging to one message group.
func bodiesForGroup(msgs []receivedFifoMessage, group string) []string {
	var out []string
	for _, m := range msgs {
		if m.Attributes["MessageGroupId"] == group {
			out = append(out, m.Body)
		}
	}
	return out
}

func assertBodies(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d messages %v, want %d %v", label, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
}

// ---- FIFO batch retrieval --------------------------------------------------

// AWS: "You may receive multiple messages from the same message group ID in one
// batch (up to 10 messages in a single call using the MaxNumberOfMessages
// parameter)" and "If fewer than 10 messages are available for the same message
// group ID, Amazon SQS may include messages from other message group IDs in the
// same batch, but each group retains FIFO order."
func TestReceiveMessage_fifo_batchReturnsWholeGroupAndFillsFromOtherGroups(t *testing.T) {
	// Given: a FIFO queue holding five messages in group-a and three in group-b.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "fifo-batch.fifo")
	for _, body := range []string{"a-0", "a-1", "a-2", "a-3", "a-4"} {
		sendFifoMessage(t, srv, queueURL, "group-a", body)
	}
	for _, body := range []string{"b-0", "b-1", "b-2"} {
		sendFifoMessage(t, srv, queueURL, "group-b", body)
	}

	// When: one ReceiveMessage asks for ten messages.
	msgs := receiveFifo(t, srv, map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
		"VisibilityTimeout":   60,
	})

	// Then: every message is returned, each group in its own FIFO order.
	if len(msgs) != 8 {
		t.Fatalf("expected 8 messages across both groups, got %d", len(msgs))
	}
	assertBodies(t, "group-a", bodiesForGroup(msgs, "group-a"), []string{"a-0", "a-1", "a-2", "a-3", "a-4"})
	assertBodies(t, "group-b", bodiesForGroup(msgs, "group-b"), []string{"b-0", "b-1", "b-2"})
}

// AWS: a batch never exceeds MaxNumberOfMessages, and once a group has in-flight
// messages "you can't receive additional messages from the same message group ID
// in subsequent requests" until they are deleted or become visible again.
func TestReceiveMessage_fifo_batchCapsAtMaxNumberOfMessagesThenBlocksTheGroup(t *testing.T) {
	// Given: a FIFO queue holding five messages in a single group.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "fifo-batch-cap.fifo")
	for _, body := range []string{"m-0", "m-1", "m-2", "m-3", "m-4"} {
		sendFifoMessage(t, srv, queueURL, "group-a", body)
	}

	// When: the first receive asks for three messages.
	first := receiveFifo(t, srv, map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 3,
		"VisibilityTimeout":   60,
	})

	// Then: exactly the first three, in order.
	assertBodies(t, "first batch", bodiesForGroup(first, "group-a"), []string{"m-0", "m-1", "m-2"})

	// And: while they are in flight the group yields nothing more.
	second := receiveFifo(t, srv, map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
		"VisibilityTimeout":   60,
	})
	if len(second) != 0 {
		t.Fatalf("expected the group to be blocked while messages are in flight, got %d messages", len(second))
	}

	// And: once the visibility timeout expires the remaining messages follow,
	// still in FIFO order behind the redelivered ones.
	srv.Clock.Add(61 * time.Second)
	third := receiveFifo(t, srv, map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
		"VisibilityTimeout":   60,
	})
	assertBodies(t, "after expiry", bodiesForGroup(third, "group-a"),
		[]string{"m-0", "m-1", "m-2", "m-3", "m-4"})
}

// A receive that cannot fill its batch from the first candidate page refetches
// with a larger limit. Messages taken with VisibilityTimeout=0 are visible again
// the instant they are handed out, so they reappear on that refetch — one
// response must still never list the same message twice.
func TestReceiveMessage_fifo_zeroVisibilityTimeoutBatchHasNoDuplicates(t *testing.T) {
	// Given: a FIFO queue where one long-held message blocks a 196-message group,
	// leaving 195 undeliverable candidates ahead of a second, free group. A
	// ten-message receive fetches 200 candidates at a time, so the batch below
	// cannot be filled without a second, larger fetch that sees the free group's
	// messages again.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "fifo-refetch.fifo")
	for i := 0; i < 196; i++ {
		sendFifoMessage(t, srv, queueURL, "blocked", fmt.Sprintf("x-%03d", i))
	}
	if head := receiveFifo(t, srv, map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
		"VisibilityTimeout":   600,
	}); len(head) != 1 {
		t.Fatalf("expected to take the head of the blocked group, got %d", len(head))
	}
	for i := 0; i < 10; i++ {
		sendFifoMessage(t, srv, queueURL, "free", fmt.Sprintf("y-%03d", i))
	}

	// When: ten messages are received with VisibilityTimeout=0.
	msgs := receiveFifo(t, srv, map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 10,
		"VisibilityTimeout":   0,
	})

	// Then: ten distinct messages come back, all from the unblocked group.
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}
	seen := map[string]bool{}
	for _, m := range msgs {
		if seen[m.MessageId] {
			t.Fatalf("message %s (%s) returned twice in one response", m.MessageId, m.Body)
		}
		seen[m.MessageId] = true
		if m.Attributes["MessageGroupId"] != "free" {
			t.Errorf("expected only the unblocked group, got %q from %q", m.Body, m.Attributes["MessageGroupId"])
		}
	}
}

// ---- ReceiveRequestAttemptId deduplication window --------------------------

// AWS: "You can use ReceiveRequestAttemptId only for 5 minutes after a
// ReceiveMessage action."
func TestReceiveMessage_fifo_receiveRequestAttemptIdExpiresAfterFiveMinutes(t *testing.T) {
	// Given: a FIFO queue whose only message was received under an attempt ID
	// and hidden for far longer than the deduplication window.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "attempt-window.fifo")
	sendFifoMessage(t, srv, queueURL, "group-a", "windowed")

	request := map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-window",
		"VisibilityTimeout":       3600,
	}
	first := receiveFifo(t, srv, request)
	if len(first) != 1 {
		t.Fatalf("expected the first receive to return 1 message, got %d", len(first))
	}

	// When: the same attempt ID is retried just inside the five-minute window.
	srv.Clock.Add(4*time.Minute + 59*time.Second)
	inside := receiveFifo(t, srv, request)

	// Then: the retry replays the same message and receipt handle.
	if len(inside) != 1 {
		t.Fatalf("expected a replay inside the window to return 1 message, got %d", len(inside))
	}
	if inside[0].ReceiptHandle != first[0].ReceiptHandle {
		t.Errorf("expected the replayed receipt handle %q, got %q", first[0].ReceiptHandle, inside[0].ReceiptHandle)
	}

	// When: the same attempt ID is retried after the window has closed.
	srv.Clock.Add(2 * time.Second)
	outside := receiveFifo(t, srv, request)

	// Then: it is an ordinary receive again — the message is still in flight, so
	// nothing comes back rather than a replay.
	if len(outside) != 0 {
		t.Fatalf("expected no replay once the 5-minute window closed, got %d messages", len(outside))
	}
}

// AWS: "During a visibility timeout, subsequent calls with the same
// ReceiveRequestAttemptId return the same messages and receipt handles. If a
// retry occurs within the deduplication interval, it resets the visibility
// timeout."
func TestReceiveMessage_fifo_receiveRequestAttemptIdRetryResetsVisibilityTimeout(t *testing.T) {
	// Given: a FIFO message received under an attempt ID with a 30s timeout.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueue(t, srv, "attempt-reset.fifo")
	sendFifoMessage(t, srv, queueURL, "group-a", "reset-me")

	request := map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-reset",
		"VisibilityTimeout":       30,
	}
	if first := receiveFifo(t, srv, request); len(first) != 1 {
		t.Fatalf("expected the first receive to return 1 message, got %d", len(first))
	}

	// When: the attempt is retried 20 seconds in.
	srv.Clock.Add(20 * time.Second)
	if replay := receiveFifo(t, srv, request); len(replay) != 1 {
		t.Fatalf("expected the retry to replay 1 message, got %d", len(replay))
	}

	// Then: 20 more seconds on — past the original 30s deadline — the message is
	// still invisible to an ordinary consumer, because the retry reset it.
	srv.Clock.Add(20 * time.Second)
	during := receiveFifo(t, srv, map[string]any{"QueueUrl": queueURL})
	if len(during) != 0 {
		t.Fatalf("expected the retry to reset the visibility timeout, got %d messages", len(during))
	}

	// And: it returns 30 seconds after the retry, not after the first receive.
	srv.Clock.Add(11 * time.Second)
	after := receiveFifo(t, srv, map[string]any{"QueueUrl": queueURL})
	if len(after) != 1 {
		t.Fatalf("expected the message back once the reset timeout expired, got %d", len(after))
	}
}

// AWS: "This parameter applies only to FIFO (first-in-first-out) queues."
func TestReceiveMessage_receiveRequestAttemptIdIsIgnoredOnStandardQueue(t *testing.T) {
	// Given: a standard queue holding two messages.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "attempt-standard")
	sendMessage(t, srv, queueURL, "first")
	sendMessage(t, srv, queueURL, "second")

	request := map[string]any{
		"QueueUrl":                queueURL,
		"ReceiveRequestAttemptId": "attempt-standard",
		"MaxNumberOfMessages":     1,
		"VisibilityTimeout":       60,
	}

	// When: two receives share one ReceiveRequestAttemptId.
	first := receiveFifo(t, srv, request)
	second := receiveFifo(t, srv, request)

	// Then: no deduplication happens — the second call returns the other message.
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 message per receive, got %d then %d", len(first), len(second))
	}
	if first[0].MessageId == second[0].MessageId {
		t.Error("expected ReceiveRequestAttemptId to be ignored on a standard queue, got a replay")
	}
}

// ---- VisibilityTimeout in-flight semantics ---------------------------------

// AWS applies a request's VisibilityTimeout only "to the messages that Amazon
// SQS returns in the response" — it is not a write to the queue attribute.
func TestReceiveMessage_visibilityTimeoutDoesNotChangeTheQueueDefault(t *testing.T) {
	// Given: a queue whose default visibility timeout is 30 seconds, holding one
	// message.
	srv := helpers.NewTestServer(t, helpers.WithMockClock())
	queueURL := createQueueWithAttrs(t, srv, "vt-no-write", map[string]string{"VisibilityTimeout": "30"})
	sendMessage(t, srv, queueURL, "overridden")

	// When: one receive overrides the timeout for the message it returns.
	overridden := receiveFifo(t, srv, map[string]any{
		"QueueUrl":            queueURL,
		"MaxNumberOfMessages": 1,
		"VisibilityTimeout":   600,
	})
	if len(overridden) != 1 {
		t.Fatalf("expected 1 message, got %d", len(overridden))
	}

	// Then: the queue attribute is untouched.
	attrsResp := sqsCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"VisibilityTimeout"},
	})
	defer attrsResp.Body.Close()
	helpers.AssertStatus(t, attrsResp, http.StatusOK)
	var attrs struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrsResp, &attrs)
	if attrs.Attributes["VisibilityTimeout"] != "30" {
		t.Errorf("expected the queue default to stay 30, got %q", attrs.Attributes["VisibilityTimeout"])
	}

	// And: a later receive that sends no VisibilityTimeout hides its own message
	// for the queue's 30 seconds rather than the previous call's 600 — after 31
	// seconds only that message is back, the overridden one still in flight.
	sendMessage(t, srv, queueURL, "default")
	if got := receiveFifo(t, srv, map[string]any{"QueueUrl": queueURL, "MaxNumberOfMessages": 10}); len(got) != 1 {
		t.Fatalf("expected only the newly sent message, got %d", len(got))
	}
	srv.Clock.Add(31 * time.Second)
	back := receiveFifo(t, srv, map[string]any{"QueueUrl": queueURL, "MaxNumberOfMessages": 10})
	if len(back) != 1 {
		t.Fatalf("expected only the queue-default message back after 31s, got %d", len(back))
	}
	if back[0].Body != "default" {
		t.Errorf("expected the queue-default message back, got %q", back[0].Body)
	}
}

// The Query protocol decodes VisibilityTimeout separately from the JSON one, so
// the 0..43200 range has to be enforced on both wires.
func TestReceiveMessage_queryWireVisibilityTimeoutValidation(t *testing.T) {
	// Given: a queue with a message.
	srv := helpers.NewTestServer(t)
	queueURL := createQueue(t, srv, "vt-query-validation")
	sendMessage(t, srv, queueURL, "message")

	// When + Then: values outside 0..43200 are rejected on the Query wire.
	for _, value := range []string{"-1", "43201"} {
		resp := sqsQueryCall(t, srv, url.Values{
			"Action":            {"ReceiveMessage"},
			"QueueUrl":          {queueURL},
			"VisibilityTimeout": {value},
		})
		helpers.AssertStatus(t, resp, http.StatusBadRequest)
		helpers.AssertQueryXMLError(t, resp, "InvalidParameterValue")
		resp.Body.Close()
	}

	// And: the boundary values are accepted.
	for _, value := range []string{"0", "43200"} {
		resp := sqsQueryCall(t, srv, url.Values{
			"Action":            {"ReceiveMessage"},
			"QueueUrl":          {queueURL},
			"VisibilityTimeout": {value},
		})
		helpers.AssertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
}
