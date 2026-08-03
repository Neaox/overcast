package pipes

import (
	"context"
	"net/http"
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

// A DynamoDB-sourced pipe runs on the events bus, which hands subscribers the
// *publishing* request's context on a pool goroutine — so the delivery outlives
// the write that triggered it. Delivering on that context unchanged means a
// write that has already answered its client cancels the pipe mid-delivery.
// SNS detaches for the same reason (internal/services/sns/handler_publish.go).
func TestDeliverStreamEvent_survivesTheOriginatingRequestReturning(t *testing.T) {
	// Given: a RUNNING pipe sourced from the orders table's stream
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	h := newHandler(cfg, state.NewMemoryStore(), serviceutil.NewServiceLogger(zap.NewNop(), serviceName), clock.NewMock())

	// The sink records whether it was handed a live or an already-cancelled
	// context — a real sink does store reads on it.
	var live, cancelled atomic.Int64
	h.targets = eventtarget.NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			cancelled.Add(1)
		} else {
			live.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}), cfg.Region)

	pipe := &Pipe{
		Name:         "orders",
		Arn:          "arn:aws:pipes:us-east-1:000000000000:pipe/orders",
		RoleArn:      "arn:aws:iam::000000000000:role/pipes",
		SourceArn:    "arn:aws:dynamodb:us-east-1:000000000000:table/orders/stream/2024-01-01T00:00:00.000",
		TargetArn:    "arn:aws:sqs:us-east-1:000000000000:dst",
		CurrentState: PipeStateRunning,
	}
	if aerr := h.store.putPipe(context.Background(), pipe); aerr != nil {
		t.Fatalf("putPipe: %s", aerr.Message)
	}

	// When: the bus delivers a stream event on a context that has already been
	// cancelled, exactly as a returned request's context is
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.deliverStreamEvent(ctx, events.Event{
		Type:    events.DynamoDBStreamInsert,
		Payload: events.DynamoDBStreamPayload{Table: "orders", EventName: "INSERT"},
	})

	// Then: the sink ran with a live context, so its own store reads and
	// downstream calls are not cancelled out from under it
	if got := cancelled.Load(); got != 0 {
		t.Fatalf("%d deliveries reached the sink on an already-cancelled context", got)
	}
	if got := live.Load(); got != 1 {
		t.Fatalf("sink received %d live deliveries, want 1", got)
	}
}
