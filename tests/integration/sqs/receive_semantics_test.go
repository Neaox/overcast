// ReceiveMessage semantics that the AWS docs spell out but that the happy-path
// tests in sqs_test.go do not reach.
//
// Sources:
//   - https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html
//   - https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/FIFO-queues-understanding-logic.html
package sqs_test

import (
	"fmt"
	"net/http"
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
