package lambda

// metrics_lambda_async_test.go — failing-first coverage for phase 4's Lambda
// async P1 metric tier (metrics_lambda.go's "Async invocation P1 tier"
// section): AsyncEventsReceived, AsyncEventAge, AsyncEventsDropped,
// DeadLetterErrors, and DestinationDeliveryFailures.

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// TestStartAsync_RecordsAsyncEventsReceived pins that accepting an Event
// invocation records exactly one AsyncEventsReceived, dimensioned by
// FunctionName, distinct from (and in addition to) the per-attempt
// Invocations/Duration series recordInvocationOutcome already records.
func TestStartAsync_RecordsAsyncEventsReceived(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk,
			result: &InvokeResult{StatusCode: 200}}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")

	if ok := h.startAsync(fn, h.runtimes.runtimeFor(context.Background(), fn.Runtime), []byte("{}")); !ok {
		t.Fatal("expected startAsync to accept the event")
	}
	h.asyncWg.Wait() // the async goroutine runs invokeAsync to completion

	received := rec.byName("AsyncEventsReceived")
	if len(received) != 1 {
		t.Fatalf("expected 1 AsyncEventsReceived observation, got %d: %+v", len(received), received)
	}
	if got := dimValue(received[0], "FunctionName"); got != "metrics-fn" {
		t.Errorf("FunctionName dimension = %q, want metrics-fn", got)
	}
	if received[0].Value != 1 || received[0].Namespace != "AWS/Lambda" || received[0].Unit != "Count" {
		t.Errorf("unexpected AsyncEventsReceived observation: %+v", received[0])
	}

	ages := rec.byName("AsyncEventAge")
	if len(ages) < 1 {
		t.Fatalf("expected at least 1 AsyncEventAge observation, got %d", len(ages))
	}
	if ages[0].Unit != "Milliseconds" {
		t.Errorf("AsyncEventAge unit = %q, want Milliseconds", ages[0].Unit)
	}
}

// TestStartAsync_RefusedDuringShutdown_DoesNotRecordAsyncEventsReceived pins
// that an event refused because Lambda is shutting down is not counted as
// received — that is this emulator's own process-lifecycle behavior, not the
// AWS queue-acceptance fact AsyncEventsReceived describes.
func TestStartAsync_RefusedDuringShutdown_DoesNotRecordAsyncEventsReceived(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk,
			result: &InvokeResult{StatusCode: 200}}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")

	h.asyncMu.Lock()
	h.asyncClosed = true
	h.asyncMu.Unlock()

	if ok := h.startAsync(fn, h.runtimes.runtimeFor(context.Background(), fn.Runtime), []byte("{}")); ok {
		t.Fatal("expected startAsync to refuse the event once closed")
	}
	if got := rec.byName("AsyncEventsReceived"); len(got) != 0 {
		t.Errorf("expected no AsyncEventsReceived for a shutdown-refused event, got %+v", got)
	}
}

// TestInvokeAsync_AgedOutEventRecordsAsyncEventsDropped pins that an event
// discarded for outliving MaximumEventAgeInSeconds records AsyncEventsDropped
// — the one case Overcast actually discards an event unrun.
func TestInvokeAsync_AgedOutEventRecordsAsyncEventsDropped(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk,
			invokeErr: context.DeadlineExceeded}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")

	// A 1-second MaximumEventAgeInSeconds guarantees the first retry backoff
	// (asyncRetryBackoff(1) == 1 minute on the mock clock) already exceeds it.
	maxAge := 1
	cfg := &EventInvokeConfig{FunctionName: fn.Name, MaximumEventAgeInSeconds: &maxAge}
	if aerr := h.ls.putEventInvokeConfig(context.Background(), cfg); aerr != nil {
		t.Fatalf("seed event invoke config: %s", aerr.Message)
	}

	done := make(chan struct{})
	go func() {
		h.invokeAsync(fn, h.runtimes.runtimeFor(context.Background(), fn.Runtime), []byte("{}"))
		close(done)
	}()
	// Let invokeAsyncOnce's first (failing) attempt run, then advance the
	// clock past the retry backoff so eventAgedOut fires deterministically.
	waitForObservation(t, rec, "AsyncEventAge", 1)
	clk.Add(2 * time.Minute)
	<-done

	if got := rec.byName("AsyncEventsDropped"); len(got) != 1 {
		t.Fatalf("expected 1 AsyncEventsDropped observation, got %d: %+v", len(got), got)
	}
}

// TestDeadLetterAsyncFailure_DeliveryErrorRecordsDeadLetterErrors pins that a
// dead-letter delivery that fails records DeadLetterErrors. The test harness
// never wires a target dispatcher (h.targets stays nil), so
// deliverFailureRecord's own errNoTargetRouter guard supplies the failure
// deterministically, without a fake HTTP router.
func TestDeadLetterAsyncFailure_DeliveryErrorRecordsDeadLetterErrors(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk,
			result: &InvokeResult{StatusCode: 200}}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")
	fn.DeadLetterTargetArn = testDLQARN

	h.deadLetterAsyncFailure(context.Background(), fn, []byte(`{}`), "req-1", "boom", 3)

	if got := rec.byName("DeadLetterErrors"); len(got) != 1 {
		t.Fatalf("expected 1 DeadLetterErrors observation, got %d: %+v", len(got), got)
	}
	if got := dimValue(rec.byName("DeadLetterErrors")[0], "FunctionName"); got != "metrics-fn" {
		t.Errorf("FunctionName dimension = %q, want metrics-fn", got)
	}
}

// TestDeliverAsyncDestination_DeliveryErrorRecordsDestinationDeliveryFailures
// mirrors the dead-letter test above for the destination delivery boundary.
func TestDeliverAsyncDestination_DeliveryErrorRecordsDestinationDeliveryFailures(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk,
			result: &InvokeResult{StatusCode: 200}}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")
	cfg := &EventInvokeConfig{
		FunctionName: fn.Name,
		DestinationConfig: &DestinationConfig{
			OnFailure: &OnFailure{Destination: testDLQARN},
		},
	}
	outcome := asyncInvokeOutcome{requestID: "req-1", errorMessage: "boom"}

	h.deliverAsyncDestination(context.Background(), fn, cfg, []byte(`{}`), outcome, 3, false)

	if got := rec.byName("DestinationDeliveryFailures"); len(got) != 1 {
		t.Fatalf("expected 1 DestinationDeliveryFailures observation, got %d: %+v", len(got), got)
	}
}

// waitForObservation polls (bounded) for at least n observations of name to
// appear, so a test driving a background goroutine over a mock clock does not
// race the goroutine reaching its first Observe call.
func waitForObservation(t *testing.T, rec *fakeMetricsRecorder, name string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.byName(name)) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s observation(s)", n, name)
}
