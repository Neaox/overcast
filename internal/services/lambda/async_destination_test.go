package lambda

// async_destination_test.go — what the event-invoke configuration actually
// changes about an asynchronous invocation.
//
// The API surface is covered by
// tests/integration/lambda/event_invoke_config_test.go. What is covered here is
// the half that makes storing the configuration honest: the retry count is the
// one that was configured, an aged-out event stops retrying, and the
// destinations receive AWS's invocation record rather than the bare event a
// dead-letter queue gets.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Neaox/overcast/internal/eventtarget"
)

const (
	testDestinationQueue = "arn:aws:sqs:us-east-1:000000000000:async-destination"
	testSuccessQueue     = "arn:aws:sqs:us-east-1:000000000000:async-success"
)

// configureEventInvoke stores an event-invoke configuration for the fixture's
// function, the way PutFunctionEventInvokeConfig would.
func (f *deadLetterFixture) configureEventInvoke(t *testing.T, cfg *EventInvokeConfig) {
	t.Helper()
	cfg.FunctionName = f.fn.Name
	cfg.FunctionArn = f.fn.ARN
	if aerr := f.h.ls.putEventInvokeConfig(context.Background(), cfg); aerr != nil {
		t.Fatalf("put event invoke config: %s", aerr.Message)
	}
}

func TestInvokeAsync_maximumRetryAttemptsReplacesTheDefault(t *testing.T) {
	// Given: a function that always fails, configured for a single retry
	// instead of AWS's default two.
	rt := &failingRuntime{requestID: "configured-retries"}
	f := newDeadLetterFixture(t, rt, "")
	f.configureEventInvoke(t, &EventInvokeConfig{MaximumRetryAttempts: intPtr(1)})

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: it ran twice — the initial attempt plus the one retry asked for.
	if got := rt.invokeCount(); got != 2 {
		t.Errorf("handler ran %d time(s), want 2 (the initial attempt plus one configured retry)", got)
	}
}

func TestInvokeAsync_zeroRetryAttemptsMeansNoRetry(t *testing.T) {
	// Given: a function configured not to retry at all. Zero is a real setting
	// and the reason the stored value is a pointer — treated as "unset" it
	// would silently become AWS's default of two.
	rt := &failingRuntime{requestID: "no-retries"}
	f := newDeadLetterFixture(t, rt, "")
	f.configureEventInvoke(t, &EventInvokeConfig{MaximumRetryAttempts: intPtr(0)})

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: exactly one attempt.
	if got := rt.invokeCount(); got != 1 {
		t.Errorf("handler ran %d time(s), want 1 — MaximumRetryAttempts=0 means do not retry", got)
	}
}

func TestInvokeAsync_ageingOutStopsTheRetries(t *testing.T) {
	// Given: a function that always fails, allowed AWS's two retries and AWS's
	// minimum event age.
	//
	// One attempt is the whole point. AWS waits a minute before the first
	// retry, so an event with a sixty-second maximum age has already expired
	// when that wait ends and is discarded without being sent again — the age
	// decides the outcome, and the two retries it is entitled to never run.
	// This is only reachable because the back-off is AWS's real one; while it
	// was compressed to seconds the event outlived nothing and the setting was
	// stored, honoured and unreachable.
	rt := &failingRuntime{requestID: "aged-out"}
	f := newDeadLetterFixture(t, rt, "")
	f.configureEventInvoke(t, &EventInvokeConfig{
		MaximumRetryAttempts:     intPtr(2),
		MaximumEventAgeInSeconds: intPtr(60),
	})

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: it ran once. Not "fewer than three" — the age is what ended it and
	// the count it ends at is exact.
	if got := rt.invokeCount(); got != 1 {
		t.Errorf("handler ran %d time(s), want 1 — the event ages out during the first retry wait", got)
	}
}

func TestInvokeAsync_onFailureDestinationGetsTheInvocationRecord(t *testing.T) {
	// Given: a function that always fails, with an on-failure destination.
	rt := &failingRuntime{requestID: "record-req"}
	f := newDeadLetterFixture(t, rt, "")
	f.configureEventInvoke(t, &EventInvokeConfig{
		MaximumRetryAttempts: intPtr(0),
		DestinationConfig: &DestinationConfig{
			OnFailure: &OnFailure{Destination: testDestinationQueue},
		},
	})

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the destination receives the invocation record — an envelope
	// describing the invocation — not the bare event a dead-letter queue gets.
	delivery := f.router.awaitDelivery(t)
	if delivery.target != "AmazonSQS.SendMessage" {
		t.Fatalf("X-Amz-Target = %q, want AmazonSQS.SendMessage", delivery.target)
	}
	body, _ := delivery.json["MessageBody"].(string)
	if body == testDLQEvent {
		t.Fatalf("destination got the bare event %q; a destination receives the invocation record", body)
	}
	record := decodeInvocationRecord(t, body)

	if got := record.Version; got != "1.0" {
		t.Errorf("version = %q, want 1.0", got)
	}
	if got := record.RequestContext.Condition; got != "RetriesExhausted" {
		t.Errorf("requestContext.condition = %q, want RetriesExhausted", got)
	}
	// The fake runtime names each attempt, and with MaximumRetryAttempts=0 the
	// only attempt is the first — so this also pins that the record carries the
	// attempt that actually failed rather than a request ID invented later.
	if got := record.RequestContext.RequestID; got != "record-req-attempt-1" {
		t.Errorf("requestContext.requestId = %q, want record-req-attempt-1", got)
	}
	if got := record.RequestContext.ApproximateInvokeCount; got != 1 {
		t.Errorf("requestContext.approximateInvokeCount = %d, want 1", got)
	}
	if got := record.ResponseContext.FunctionError; got != "Unhandled" {
		t.Errorf("responseContext.functionError = %q, want Unhandled", got)
	}
	// The payloads are JSON values, not escaped strings — an SDK consumer reads
	// requestPayload.order rather than parsing a string a second time.
	if got := string(record.RequestPayload); got != testDLQEvent {
		t.Errorf("requestPayload = %s, want %s", got, testDLQEvent)
	}
	if len(record.ResponsePayload) == 0 {
		t.Error("responsePayload is empty, want the handler's error document")
	}
}

func TestInvokeAsync_onSuccessDestinationGetsASuccessRecord(t *testing.T) {
	// Given: a function that succeeds, with an on-success destination.
	f := newDeadLetterFixture(t, &succeedingRuntime{}, "")
	f.configureEventInvoke(t, &EventInvokeConfig{
		DestinationConfig: &DestinationConfig{
			OnSuccess: &OnSuccess{Destination: testSuccessQueue},
		},
	})

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the success destination receives a record whose condition says so,
	// and which carries no functionError.
	delivery := f.router.awaitDelivery(t)
	if got, _ := delivery.json["QueueUrl"].(string); got != "async-success" {
		t.Errorf("QueueUrl = %q, want async-success", got)
	}
	body, _ := delivery.json["MessageBody"].(string)
	record := decodeInvocationRecord(t, body)
	if got := record.RequestContext.Condition; got != "Success" {
		t.Errorf("requestContext.condition = %q, want Success", got)
	}
	if record.ResponseContext.FunctionError != "" {
		t.Errorf("responseContext.functionError = %q, want it absent on success", record.ResponseContext.FunctionError)
	}
}

func TestInvokeAsync_successfulInvocationSkipsTheFailureDestination(t *testing.T) {
	// Given: a function that succeeds, with only an on-failure destination —
	// the configuration most functions have.
	f := newDeadLetterFixture(t, &succeedingRuntime{}, "")
	f.configureEventInvoke(t, &EventInvokeConfig{
		DestinationConfig: &DestinationConfig{
			OnFailure: &OnFailure{Destination: testDestinationQueue},
		},
	})

	// When: it is invoked asynchronously and the async machinery drains.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: nothing is delivered. A failure destination that collects successes
	// is worse than one that collects nothing.
	assertNoDelivery(t, f)
}

func TestInvokeAsync_destinationAndDeadLetterQueueBothReceive(t *testing.T) {
	// Given: a function with both an on-failure destination and a dead-letter
	// queue. AWS documents destinations as usable "in addition to or instead
	// of" a DLQ, so configuring both must deliver to both — and they carry
	// different things.
	rt := &failingRuntime{requestID: "both-req"}
	f := newDeadLetterFixture(t, rt, testDLQARN)
	f.configureEventInvoke(t, &EventInvokeConfig{
		MaximumRetryAttempts: intPtr(0),
		DestinationConfig: &DestinationConfig{
			OnFailure: &OnFailure{Destination: testDestinationQueue},
		},
	})

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: two deliveries — the record to the destination, the event to the
	// dead-letter queue.
	first := f.router.awaitDelivery(t)
	second := f.router.awaitDelivery(t)
	byQueue := map[string]string{}
	for _, d := range []recordedDelivery{first, second} {
		queue, _ := d.json["QueueUrl"].(string)
		body, _ := d.json["MessageBody"].(string)
		byQueue[queue] = body
	}
	if got := byQueue["async-dlq"]; got != testDLQEvent {
		t.Errorf("dead-letter queue got %q, want the bare event %q", got, testDLQEvent)
	}
	if got := byQueue["async-destination"]; got == testDLQEvent || got == "" {
		t.Errorf("destination got %q, want an invocation record", got)
	}
}

// ---- helpers ---------------------------------------------------------------

type asyncRecord struct {
	Version        string `json:"version"`
	Timestamp      string `json:"timestamp"`
	RequestContext struct {
		RequestID              string `json:"requestId"`
		FunctionArn            string `json:"functionArn"`
		Condition              string `json:"condition"`
		ApproximateInvokeCount int    `json:"approximateInvokeCount"`
	} `json:"requestContext"`
	RequestPayload  json.RawMessage `json:"requestPayload"`
	ResponsePayload json.RawMessage `json:"responsePayload"`
	ResponseContext struct {
		StatusCode      int    `json:"statusCode"`
		ExecutedVersion string `json:"executedVersion"`
		FunctionError   string `json:"functionError"`
	} `json:"responseContext"`
}

func decodeInvocationRecord(t *testing.T, body string) asyncRecord {
	t.Helper()
	var record asyncRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		t.Fatalf("decode invocation record %q: %v", body, err)
	}
	return record
}

// eventInvokeDispatcher is here to keep the import of eventtarget honest when
// the fixture is reused: the destinations go out through the same dispatcher
// the dead-letter queue does.
var _ = eventtarget.KindSQS
