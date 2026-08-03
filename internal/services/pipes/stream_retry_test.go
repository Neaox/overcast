package pipes

// A DynamoDB-stream record has nowhere to be redelivered from: unlike an SQS or
// Kinesis source, there is no cursor to leave alone and no message to let
// reappear. A failed target delivery therefore used to be discarded outright,
// which loses the record silently — the caller has already been answered, and
// the only trace is a log line. These tests pin the retry and dead-letter
// handling AWS gives a stream-sourced pipe through
// SourceParameters.DynamoDBStreamParameters.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// streamPipeFixture builds a handler with a RUNNING DynamoDB-sourced pipe and a
// router stub standing in for every sink the dispatcher can reach.
type streamPipeFixture struct {
	h *Handler
	// calls counts the delivery attempts the target queue saw.
	calls atomic.Int64
	// dlqBodies holds every MessageBody sent to the dead-letter queue.
	dlqBodies []string
}

const (
	streamSourceARN = "arn:aws:dynamodb:us-east-1:000000000000:table/orders/stream/2024-01-01T00:00:00.000"
	streamTargetARN = "arn:aws:sqs:us-east-1:000000000000:dst"
	streamDLQARN    = "arn:aws:sqs:us-east-1:000000000000:pipe-dlq"
)

// newStreamPipeFixture wires a pipe whose target fails its first failUntil
// delivery attempts and succeeds afterwards. failUntil < 0 fails every attempt.
func newStreamPipeFixture(t *testing.T, sp *SourceParameters, failUntil int) *streamPipeFixture {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	f := &streamPipeFixture{
		h: newHandler(cfg, state.NewMemoryStore(), serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clock.NewMock()),
	}

	f.h.targets = eventtarget.NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			QueueURL    string `json:"QueueUrl"`
			MessageBody string `json:"MessageBody"`
		}
		_ = json.Unmarshal(raw, &body)
		if body.QueueURL == "pipe-dlq" {
			f.dlqBodies = append(f.dlqBodies, body.MessageBody)
			w.WriteHeader(http.StatusOK)
			return
		}
		n := f.calls.Add(1)
		if failUntil < 0 || n <= int64(failUntil) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"AWS.SimpleQueueService.NonExistentQueue"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}), cfg.Region)

	pipe := &Pipe{
		Name:             "orders",
		Arn:              "arn:aws:pipes:us-east-1:000000000000:pipe/orders",
		RoleArn:          "arn:aws:iam::000000000000:role/pipes",
		SourceArn:        streamSourceARN,
		SourceParameters: sp,
		TargetArn:        streamTargetARN,
		CurrentState:     PipeStateRunning,
	}
	if aerr := f.h.store.putPipe(context.Background(), pipe); aerr != nil {
		t.Fatalf("putPipe: %s", aerr.Message)
	}
	return f
}

// deliverOne pushes one INSERT record through the bus subscriber.
func (f *streamPipeFixture) deliverOne() {
	f.h.deliverStreamEvent(context.Background(), events.Event{
		Type: events.DynamoDBStreamInsert,
		Payload: events.DynamoDBStreamPayload{
			Table: "orders", EventName: "INSERT", SequenceNumber: 1,
		},
	})
}

func intPtr(v int) *int { return &v }

func TestDeliverStreamEvent_retriesUntilTheTargetAccepts(t *testing.T) {
	// Given: a stream-sourced pipe allowed two retries, and a target that
	// rejects the first two attempts — what a burst of concurrent writes does
	// to a target under contention
	f := newStreamPipeFixture(t, &SourceParameters{
		DynamoDBStreamParameters: &StreamSourceParameters{
			BatchSize:            1,
			StartingPosition:     "TRIM_HORIZON",
			MaximumRetryAttempts: intPtr(2),
		},
	}, 2)

	// When: a stream record arrives
	f.deliverOne()

	// Then: the record is redelivered until it lands, rather than discarded
	if got := f.calls.Load(); got != 3 {
		t.Fatalf("target saw %d attempts, want 3 (initial + MaximumRetryAttempts)", got)
	}
	rec := latestDelivery(t, f.h)
	if rec.Outcome != outcomeDelivered {
		t.Fatalf("outcome = %q (%s), want delivered", rec.Outcome, rec.Error)
	}
	if rec.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", rec.Attempts)
	}
}

func TestDeliverStreamEvent_deadLettersWhenRetriesAreExhausted(t *testing.T) {
	// Given: a stream-sourced pipe with a dead-letter queue and a target that
	// never accepts anything
	f := newStreamPipeFixture(t, &SourceParameters{
		DynamoDBStreamParameters: &StreamSourceParameters{
			MaximumRetryAttempts: intPtr(1),
			DeadLetterConfig:     &DeadLetterConfig{Arn: streamDLQARN},
		},
	}, -1)

	// When: a stream record arrives
	f.deliverOne()

	// Then: it is retried, then parked on the dead-letter queue rather than lost
	if got := f.calls.Load(); got != 2 {
		t.Fatalf("target saw %d attempts, want 2", got)
	}
	if len(f.dlqBodies) != 1 {
		t.Fatalf("dead-letter queue received %d messages, want 1", len(f.dlqBodies))
	}
	if !strings.Contains(f.dlqBodies[0], `"eventSource":"aws:dynamodb"`) {
		t.Fatalf("dead-lettered body is not the source record: %s", f.dlqBodies[0])
	}
	rec := latestDelivery(t, f.h)
	if rec.Outcome != outcomeDLQ {
		t.Fatalf("outcome = %q (%s), want dlq", rec.Outcome, rec.Error)
	}
}

func TestDeliverStreamEvent_reportsTheBatchAsFailedWithoutADeadLetterQueue(t *testing.T) {
	// Given: a stream-sourced pipe with no dead-letter queue
	f := newStreamPipeFixture(t, nil, -1)

	// When: a stream record arrives and every attempt fails
	f.deliverOne()

	// Then: the attempts AWS's default allows are made and the outcome names
	// how many were tried, so the loss is visible in the console feed
	if got := f.calls.Load(); got != int64(maxStreamDeliveryAttempts) {
		t.Fatalf("target saw %d attempts, want %d", got, maxStreamDeliveryAttempts)
	}
	rec := latestDelivery(t, f.h)
	if rec.Outcome != outcomeFailed {
		t.Fatalf("outcome = %q, want failed", rec.Outcome)
	}
	if rec.Attempts != maxStreamDeliveryAttempts {
		t.Fatalf("attempts = %d, want %d", rec.Attempts, maxStreamDeliveryAttempts)
	}
}

func TestDeliverStreamEvent_honoursZeroRetries(t *testing.T) {
	// Given: a pipe that explicitly asks for no retries
	f := newStreamPipeFixture(t, &SourceParameters{
		DynamoDBStreamParameters: &StreamSourceParameters{MaximumRetryAttempts: intPtr(0)},
	}, -1)

	// When: a stream record arrives
	f.deliverOne()

	// Then: exactly one attempt is made
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("target saw %d attempts, want 1", got)
	}
}

// latestDelivery returns the newest record in the handler's delivery feed.
func latestDelivery(t *testing.T, h *Handler) deliveryRecord {
	t.Helper()
	records := h.deliveries.snapshot("", "")
	if len(records) == 0 {
		t.Fatal("no delivery outcome recorded")
	}
	return records[0]
}
