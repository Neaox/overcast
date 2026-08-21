package lambda

// esm_partial_batch_test.go — FunctionResponseTypes: ["ReportBatchItemFailures"].
//
// A function that reports a subset of a batch as failed must have exactly that
// subset retried: the succeeded messages are deleted from the queue and the
// failed ones are left to become visible again. Before #512 the flag was
// refused by the API and never read by the poller, so a handler written against
// it either had its whole batch acknowledged or its whole batch retried.
//
// These tests drive the real poller through its two injected seams — an SQS
// receiver and a Lambda invoker — because a container-backed invoke is not what
// is under test here; the acknowledgement decision is.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// batchReceiver hands the poller one batch and then nothing, and records every
// receipt handle deleted from the queue.
type batchReceiver struct {
	mu      sync.Mutex
	batch   []events.ReceivedMessage
	handed  bool
	deleted []string
}

func newBatchReceiver(msgs ...events.ReceivedMessage) *batchReceiver {
	return &batchReceiver{batch: msgs}
}

func (r *batchReceiver) ReceiveMessages(_ context.Context, _ string, _, _ int) ([]events.ReceivedMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handed {
		return nil, nil
	}
	r.handed = true
	return r.batch, nil
}

func (r *batchReceiver) DeleteMessages(_ context.Context, _ string, handles []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, handles...)
	return nil
}

func (r *batchReceiver) deletedHandles() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.deleted...)
}

// respondingInvoker returns a fixed response payload and reports how many times
// it was invoked.
type respondingInvoker struct {
	payload string

	mu    sync.Mutex
	calls int
}

func (i *respondingInvoker) Invoke(_ context.Context, _ string, _ []byte) (*events.InvokeOutcome, error) {
	i.mu.Lock()
	i.calls++
	i.mu.Unlock()
	return &events.InvokeOutcome{Payload: []byte(i.payload)}, nil
}

func (i *respondingInvoker) invocations() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.calls
}

// threeMessages is the batch every case below delivers.
func threeMessages() []events.ReceivedMessage {
	return []events.ReceivedMessage{
		{MessageID: "m-1", ReceiptHandle: "rh-1", Body: "one"},
		{MessageID: "m-2", ReceiptHandle: "rh-2", Body: "two"},
		{MessageID: "m-3", ReceiptHandle: "rh-3", Body: "three"},
	}
}

func sqsPartialBatchESM(uuid string, responseTypes []string) *EventSourceMapping {
	return &EventSourceMapping{
		UUID:                  uuid,
		FunctionArn:           "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn:        "arn:aws:sqs:us-east-1:000000000000:q",
		State:                 esmStateEnabled,
		BatchSize:             10,
		FunctionResponseTypes: responseTypes,
	}
}

// awaitInvocation waits for the poller to have invoked the function at least
// once, then gives the acknowledgement that follows it time to land.
func awaitInvocation(t *testing.T, inv *respondingInvoker) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if inv.invocations() > 0 {
			// The delete happens on the same goroutine immediately after the
			// invoke returns; a short settle is enough and keeps the test from
			// depending on scheduler ordering.
			time.Sleep(200 * time.Millisecond)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the poller never invoked the function")
}

func assertDeleted(t *testing.T, recv *batchReceiver, want ...string) {
	t.Helper()
	got := recv.deletedHandles()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("deleted receipt handles = %v, want %v", got, want)
	}
}

func TestPollSQS_reportBatchItemFailuresDeletesOnlyTheSucceededMessages(t *testing.T) {
	// Given: an SQS mapping that asked for partial-batch reporting, and a
	// function that reports the second of three records as failed.
	inv := &respondingInvoker{payload: `{"batchItemFailures":[{"itemIdentifier":"m-2"}]}`}
	recv := newBatchReceiver(threeMessages()...)
	startSQSPoller(t, sqsPartialBatchESM("partial-uuid", []string{functionResponseTypeReportBatchItemFailures}), inv, recv)

	// When: the batch is delivered.
	awaitInvocation(t, inv)

	// Then: exactly the two succeeded messages are deleted. The reported one is
	// left in flight and returns to the queue on its visibility timeout, which
	// is how AWS redelivers it.
	assertDeleted(t, recv, "rh-1", "rh-3")
}

func TestPollSQS_withoutReportBatchItemFailuresTheWholeBatchIsAcknowledged(t *testing.T) {
	// Given: the same function response, on a mapping that did not ask for
	// partial-batch reporting — the negative control.
	inv := &respondingInvoker{payload: `{"batchItemFailures":[{"itemIdentifier":"m-2"}]}`}
	recv := newBatchReceiver(threeMessages()...)
	startSQSPoller(t, sqsPartialBatchESM("whole-batch-uuid", nil), inv, recv)

	// When: the batch is delivered.
	awaitInvocation(t, inv)

	// Then: whole-batch semantics are unchanged — a response with no function
	// error acknowledges every message, because the mapping never opted in.
	assertDeleted(t, recv, "rh-1", "rh-2", "rh-3")
}

func TestPollSQS_emptyBatchItemFailuresAcknowledgesTheWholeBatch(t *testing.T) {
	// Given: a function that opted in and reported nothing.
	inv := &respondingInvoker{payload: `{"batchItemFailures":[]}`}
	recv := newBatchReceiver(threeMessages()...)
	startSQSPoller(t, sqsPartialBatchESM("empty-report-uuid", []string{functionResponseTypeReportBatchItemFailures}), inv, recv)

	// When: the batch is delivered.
	awaitInvocation(t, inv)

	// Then: an empty list means the whole batch succeeded.
	assertDeleted(t, recv, "rh-1", "rh-2", "rh-3")
}

func TestPollSQS_malformedPartialBatchResponseFailsTheWholeBatch(t *testing.T) {
	cases := map[string]string{
		"empty identifier":   `{"batchItemFailures":[{"itemIdentifier":""}]}`,
		"null identifier":    `{"batchItemFailures":[{"itemIdentifier":null}]}`,
		"bad key name":       `{"batchItemFailures":[{"id":"m-2"}]}`,
		"not a list":         `{"batchItemFailures":{"itemIdentifier":"m-2"}}`,
		"invalid json":       `{"batchItemFailures":[`,
		"unknown identifier": `{"batchItemFailures":[{"itemIdentifier":"m-99"}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			// Given: a mapping that opted in, and a function whose response
			// AWS documents as a complete batch failure.
			inv := &respondingInvoker{payload: payload}
			recv := newBatchReceiver(threeMessages()...)
			startSQSPoller(t, sqsPartialBatchESM("malformed-uuid", []string{functionResponseTypeReportBatchItemFailures}), inv, recv)

			// When: the batch is delivered.
			awaitInvocation(t, inv)

			// Then: nothing is acknowledged. A response the poller cannot read
			// is never permission to delete a message.
			assertDeleted(t, recv)
		})
	}
}

// scriptedInvoker returns a different response for each invocation and keeps
// every payload it was given.
type scriptedInvoker struct {
	responses []string

	mu       sync.Mutex
	payloads [][]byte
}

func (i *scriptedInvoker) Invoke(_ context.Context, _ string, payload []byte) (*events.InvokeOutcome, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	response := ""
	if len(i.payloads) < len(i.responses) {
		response = i.responses[len(i.payloads)]
	}
	i.payloads = append(i.payloads, append([]byte(nil), payload...))
	return &events.InvokeOutcome{Payload: []byte(response)}, nil
}

func (i *scriptedInvoker) delivered() [][]byte {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([][]byte(nil), i.payloads...)
}

func TestDeliverStreamBatch_reportBatchItemFailuresRetriesFromTheLowestReportedRecord(t *testing.T) {
	// Given: a DynamoDB Streams mapping that asked for partial-batch reporting,
	// and a function that reports the second of three records as failed and
	// then succeeds.
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clock.New())
	es := newESMStore(ls)
	esm := &EventSourceMapping{
		UUID:                  "stream-partial-uuid",
		FunctionArn:           "arn:aws:lambda:us-east-1:000000000000:function:fn",
		EventSourceArn:        "arn:aws:dynamodb:us-east-1:000000000000:table/T/stream/2026",
		State:                 esmStateEnabled,
		BatchSize:             10,
		MaximumRetryAttempts:  intPtr(2),
		FunctionResponseTypes: []string{functionResponseTypeReportBatchItemFailures},
	}
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	if aerr := es.putESM(ctx, esm); aerr != nil {
		t.Fatalf("putESM: %v", aerr)
	}
	inv := &scriptedInvoker{responses: []string{
		`{"batchItemFailures":[{"itemIdentifier":"000000000000000000002"}]}`,
	}}
	mgr := newESMDeliveryManager(
		es, inv, nil, nil, nil,
		serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clock.New(), &config.Config{Region: "us-east-1"},
		context.Background(),
	)

	// When: a batch of three records is delivered.
	batch := make([]streamDeliveryEvent, 0, 3)
	for seq := int64(1); seq <= 3; seq++ {
		batch = append(batch, streamDeliveryEvent{
			payload: events.DynamoDBStreamPayload{Table: "T", EventName: "INSERT", SequenceNumber: seq},
		})
	}
	mgr.deliverStreamBatch(ctx, esm, batch)

	// Then: the retry carries the reported record and everything after it, and
	// nothing before it — AWS checkpoints a stream at the lowest reported
	// sequence number rather than skipping the records in between.
	payloads := inv.delivered()
	if len(payloads) != 2 {
		t.Fatalf("invocations = %d, want 2", len(payloads))
	}
	retried := string(payloads[1])
	for _, seq := range []string{"000000000000000000002", "000000000000000000003"} {
		if !strings.Contains(retried, seq) {
			t.Fatalf("retry payload does not carry record %s: %s", seq, retried)
		}
	}
	if strings.Contains(retried, "000000000000000000001") {
		t.Fatalf("retry payload replays a record reported as succeeded: %s", retried)
	}
}

func TestPollSQS_emptyResponseWithReportingEnabledAcknowledgesTheWholeBatch(t *testing.T) {
	// Given: a mapping that opted in, and a handler that returns nothing —
	// the common case of a template that sets the flag before the handler is
	// written to use it.
	inv := &respondingInvoker{payload: ""}
	recv := newBatchReceiver(threeMessages()...)
	startSQSPoller(t, sqsPartialBatchESM("no-response-uuid", []string{functionResponseTypeReportBatchItemFailures}), inv, recv)

	// When: the batch is delivered.
	awaitInvocation(t, inv)

	// Then: an empty response is a complete success, so opting in cannot break
	// a handler that does not report anything.
	assertDeleted(t, recv, "rh-1", "rh-2", "rh-3")
}
