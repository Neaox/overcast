package pipes

// partial_batch_test.go — a Lambda target reporting batchItemFailures.
//
// EventBridge Pipes has no FunctionResponseTypes to opt in with: AWS makes
// partial-batch reporting always available for a Lambda enrichment or target.
// Before #512 the report was read by nothing, so an SQS-sourced pipe deleted
// every message the target had just said it could not process.

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/eventtarget"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// pipeQueueMessages hands the poller one batch and records what is deleted.
type pipeQueueMessages struct {
	mu      sync.Mutex
	batch   []events.ReceivedMessage
	handed  bool
	deleted []string
}

func (q *pipeQueueMessages) ReceiveMessages(_ context.Context, _ string, _, _ int) ([]events.ReceivedMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.handed {
		return nil, nil
	}
	q.handed = true
	return q.batch, nil
}

func (q *pipeQueueMessages) DeleteMessages(_ context.Context, _ string, handles []string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deleted = append(q.deleted, handles...)
	return nil
}

func (q *pipeQueueMessages) deletedHandles() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.deleted...)
}

// sqsSourcedPipe builds a RUNNING pipe from a queue to a Lambda function whose
// invocation answers with response.
func sqsSourcedPipe(t *testing.T, response string) (*Handler, *Pipe, *pipeQueueMessages) {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	h := newHandler(cfg, state.NewMemoryStore(), serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clock.NewMock())
	h.targets = eventtarget.NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(response))
	}), cfg.Region)

	queue := &pipeQueueMessages{batch: []events.ReceivedMessage{
		{MessageID: "m-1", ReceiptHandle: "rh-1", Body: "one"},
		{MessageID: "m-2", ReceiptHandle: "rh-2", Body: "two"},
		{MessageID: "m-3", ReceiptHandle: "rh-3", Body: "three"},
	}}
	h.messages = queue

	pipe := &Pipe{
		Name:         "orders",
		Arn:          "arn:aws:pipes:us-east-1:000000000000:pipe/orders",
		RoleArn:      "arn:aws:iam::000000000000:role/pipes",
		SourceArn:    "arn:aws:sqs:us-east-1:000000000000:src",
		TargetArn:    "arn:aws:lambda:us-east-1:000000000000:function:handler",
		CurrentState: PipeStateRunning,
	}
	if aerr := h.store.putPipe(context.Background(), pipe); aerr != nil {
		t.Fatalf("putPipe: %s", aerr.Message)
	}
	return h, pipe, queue
}

func assertPipeDeleted(t *testing.T, queue *pipeQueueMessages, want ...string) {
	t.Helper()
	got := queue.deletedHandles()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("deleted receipt handles = %v, want %v", got, want)
	}
}

func TestPollSQS_partialBatchFailureLeavesTheReportedMessage(t *testing.T) {
	// Given: an SQS-sourced pipe whose Lambda target reports one of three
	// records as failed.
	h, pipe, queue := sqsSourcedPipe(t, `{"batchItemFailures":[{"itemIdentifier":"m-2"}]}`)

	// When: the poller runs one batch.
	h.pollSQS(context.Background(), pipe)

	// Then: only the two succeeded messages are deleted; the reported one
	// returns to the queue on its visibility timeout.
	assertPipeDeleted(t, queue, "rh-1", "rh-3")
}

func TestPollSQS_targetResponseWithoutAReportAcknowledgesTheWholeBatch(t *testing.T) {
	// Given: a target that answers with an ordinary payload — the negative
	// control, and by far the common case.
	h, pipe, queue := sqsSourcedPipe(t, `{"statusCode":200}`)

	// When: the poller runs one batch.
	h.pollSQS(context.Background(), pipe)

	// Then: whole-batch semantics are unchanged.
	assertPipeDeleted(t, queue, "rh-1", "rh-2", "rh-3")
}

func TestPollSQS_nonJSONTargetResponseIsNotReadAsAFailureReport(t *testing.T) {
	// Given: a target that answers with something that is not JSON at all. A
	// pipe has no opt-in flag, so AWS's "a response I cannot read is a complete
	// failure" rule must not be applied to a response that never mentions the
	// member — that would retry this batch forever.
	h, pipe, queue := sqsSourcedPipe(t, `plain text`)

	// When: the poller runs one batch.
	h.pollSQS(context.Background(), pipe)

	// Then: the batch is acknowledged, as it was before partial-batch handling
	// existed.
	assertPipeDeleted(t, queue, "rh-1", "rh-2", "rh-3")
}

func TestPollSQS_malformedFailureReportRetriesTheWholeBatch(t *testing.T) {
	cases := map[string]string{
		"empty identifier":   `{"batchItemFailures":[{"itemIdentifier":""}]}`,
		"unknown identifier": `{"batchItemFailures":[{"itemIdentifier":"m-9"}]}`,
		"truncated json":     `{"batchItemFailures":[`,
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			// Given: a target whose report names the member but cannot be
			// honoured.
			h, pipe, queue := sqsSourcedPipe(t, response)

			// When: the poller runs one batch.
			h.pollSQS(context.Background(), pipe)

			// Then: nothing is deleted — the whole batch is redelivered.
			assertPipeDeleted(t, queue)
		})
	}
}
