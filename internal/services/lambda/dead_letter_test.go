package lambda

// dead_letter_test.go — what reaches a function's DeadLetterConfig target.
//
// The API surface (accepting, storing and echoing the member) is covered by
// tests/integration/lambda/dead_letter_config_test.go. What is covered here is
// the behaviour that makes accepting it honest: an asynchronous invocation that
// fails has to end up on the target, shaped the way AWS shapes it.
//
// Delivery goes out through the shared eventtarget dispatcher, which reaches
// the sink over the root router — so the assertion is on the SQS SendMessage or
// SNS Publish request a real SDK client would have produced. The fake router
// below stands in for the emulator's own, which keeps the test to Lambda's half
// of the contract without needing SQS, SNS or Docker.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/eventtarget"
)

const (
	testDLQARN   = "arn:aws:sqs:us-east-1:000000000000:async-dlq"
	testDLTopic  = "arn:aws:sns:us-east-1:000000000000:async-dlq-topic"
	testDLQEvent = `{"order":"9e07af03"}`
)

// ---- Fake root router ------------------------------------------------------

// recordedDelivery is one request the dispatcher made against the root router.
type recordedDelivery struct {
	target string // the X-Amz-Target header, for JSON 1.1 sinks
	json   map[string]any
	form   url.Values
}

// deliveryRecorder is a root http.Handler that answers every delivery 200 and
// keeps what it was sent. Requests arrive on a buffered channel so a test can
// wait for one without polling.
type deliveryRecorder struct {
	deliveries chan recordedDelivery
}

func newDeliveryRecorder() *deliveryRecorder {
	return &deliveryRecorder{deliveries: make(chan recordedDelivery, 4)}
}

func (rec *deliveryRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	delivery := recordedDelivery{target: r.Header.Get("X-Amz-Target")}
	if delivery.target != "" {
		_ = json.Unmarshal(body, &delivery.json)
	} else {
		delivery.form, _ = url.ParseQuery(string(body))
	}
	rec.deliveries <- delivery
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

// awaitDelivery returns the next delivery, failing the test if none arrives.
func (rec *deliveryRecorder) awaitDelivery(t *testing.T) recordedDelivery {
	t.Helper()
	select {
	case delivery := <-rec.deliveries:
		return delivery
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was delivered to the dead-letter target")
		return recordedDelivery{}
	}
}

// ---- Fake runtimes ---------------------------------------------------------

// failingRuntime hands out instances whose handler always raises, the shape of
// a function that fails every asynchronous invocation. failUntil, when set,
// makes only the first n invocations fail so a retry can be seen to succeed.
type failingRuntime struct {
	requestID string
	failUntil int

	mu      sync.Mutex
	invokes int
}

func (r *failingRuntime) CanHandle(string) bool { return true }
func (r *failingRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	return &failingInstance{poolTestInstance: newPoolTestInstance(fn.Name), rt: r}, nil
}
func (r *failingRuntime) Release(context.Context, RuntimeInstance, bool) {}

// nextOutcome records an invocation and reports whether it should fail.
func (r *failingRuntime) nextOutcome() (attempt int, fail bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invokes++
	if r.failUntil == 0 {
		return r.invokes, true
	}
	return r.invokes, r.invokes <= r.failUntil
}

func (r *failingRuntime) invokeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invokes
}

type failingInstance struct {
	*poolTestInstance
	rt *failingRuntime
}

func (i *failingInstance) Invoke(context.Context, []byte, InvokeOptions) (*InvokeResult, error) {
	attempt, fail := i.rt.nextOutcome()
	if !fail {
		return &InvokeResult{StatusCode: 200, RequestID: i.rt.requestID, Payload: []byte(`"ok"`)}, nil
	}
	// Each attempt is its own invocation on AWS, with its own request ID. The
	// configured one is used for the attempt that ends up dead-lettered so the
	// test can name the value it expects on the message.
	requestID := fmt.Sprintf("%s-attempt-%d", i.rt.requestID, attempt)
	if attempt == defaultAsyncInvokeAttempts {
		requestID = i.rt.requestID
	}
	return &InvokeResult{
		StatusCode:    200,
		FunctionError: "Unhandled",
		RequestID:     requestID,
		Payload:       []byte(`{"errorMessage":"boom","errorType":"Error"}`),
	}, nil
}

// unavailableRuntime never produces an execution environment at all, standing
// in for a cold start that cannot be served — a Docker daemon that has gone
// away, or an image that will not pull.
type unavailableRuntime struct{}

func (r *unavailableRuntime) CanHandle(string) bool { return true }
func (r *unavailableRuntime) Acquire(context.Context, *Function) (RuntimeInstance, error) {
	return nil, errors.New("no execution environment could be started")
}
func (r *unavailableRuntime) Release(context.Context, RuntimeInstance, bool) {}

// succeedingRuntime is the same shape with a handler that returns normally.
type succeedingRuntime struct{}

func (r *succeedingRuntime) CanHandle(string) bool { return true }
func (r *succeedingRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	return &succeedingInstance{poolTestInstance: newPoolTestInstance(fn.Name)}, nil
}
func (r *succeedingRuntime) Release(context.Context, RuntimeInstance, bool) {}

type succeedingInstance struct{ *poolTestInstance }

func (i *succeedingInstance) Invoke(context.Context, []byte, InvokeOptions) (*InvokeResult, error) {
	return &InvokeResult{StatusCode: 200, RequestID: "req-ok", Payload: []byte(`"ok"`)}, nil
}

// ---- Fixture ---------------------------------------------------------------

// deadLetterFixture is an asyncFixture with a root router wired and, optionally,
// a dead-letter target on the function.
type deadLetterFixture struct {
	*asyncFixture
	router *deliveryRecorder
}

func newDeadLetterFixture(t *testing.T, rt Runtime, targetARN string) *deadLetterFixture {
	t.Helper()
	f := newAsyncFixture(t, rt)
	router := newDeliveryRecorder()
	f.h.setDeadLetterTargets(eventtarget.NewDispatcher(router, "us-east-1"))
	if targetARN != "" {
		f.fn.DeadLetterTargetArn = targetARN
		if aerr := f.h.ls.putFunction(context.Background(), f.fn); aerr != nil {
			t.Fatalf("put function: %s", aerr.Message)
		}
	}
	return &deadLetterFixture{asyncFixture: f, router: router}
}

// invokeAsyncAndDrain runs one asynchronous invocation and waits for Lambda's
// async machinery to finish with it, so a test can assert on what did *not*
// happen without sleeping.
//
// A failing event is retried twice before it is dead-lettered, and the wait
// between attempts is on the injected clock, so the drain has to drive that
// clock or it never comes due. Advancing in a loop removes any ordering
// assumption: the retry registers its timer only after the attempt before it
// has failed, and an Add with nothing pending simply does nothing.
func (f *deadLetterFixture) invokeAsyncAndDrain(t *testing.T, event string) {
	t.Helper()
	if err := f.inv.InvokeEvent(context.Background(), f.fn.ARN, []byte(event)); err != nil {
		t.Fatalf("InvokeEvent returned %v, want nil — the event must be accepted", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		f.h.StopAsync(ctx)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case <-drained:
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the asynchronous invocation to drain")
		}
		f.clk.Add(asyncRetryBackoff(defaultAsyncInvokeAttempts))
		time.Sleep(time.Millisecond)
	}
}

// ---- Tests -----------------------------------------------------------------

func TestInvokeAsync_functionErrorIsDeadLetteredToTheSQSTarget(t *testing.T) {
	// Given: a function that always fails, with an SQS dead-letter queue.
	f := newDeadLetterFixture(t, &failingRuntime{requestID: "e4b46cbf-b738-8880-a18cdf61200e"}, testDLQARN)

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the event lands on the queue through a real SendMessage.
	delivery := f.router.awaitDelivery(t)
	if delivery.target != "AmazonSQS.SendMessage" {
		t.Fatalf("X-Amz-Target = %q, want AmazonSQS.SendMessage", delivery.target)
	}
	if got, _ := delivery.json["QueueUrl"].(string); got != "async-dlq" {
		t.Errorf("QueueUrl = %q, want async-dlq", got)
	}

	// And: the body is the original event, unwrapped. AWS "sends the event to
	// the dead-letter queue as-is" — a dead-letter queue carries the event, not
	// the invocation record an on-failure destination gets.
	if got, _ := delivery.json["MessageBody"].(string); got != testDLQEvent {
		t.Errorf("MessageBody = %q, want the original event %q", got, testDLQEvent)
	}

	// And: the diagnosis rides on the three documented message attributes.
	attrs := sqsAttributes(t, delivery)
	assertAttribute(t, attrs, "RequestID", "String", "e4b46cbf-b738-8880-a18cdf61200e")
	assertAttribute(t, attrs, "ErrorCode", "Number", "200")
	if got := attrs["ErrorMessage"]["StringValue"]; got == "" {
		t.Error("ErrorMessage attribute is empty, want the function's error")
	}
}

func TestInvokeAsync_functionErrorIsDeadLetteredToTheSNSTarget(t *testing.T) {
	// Given: the same failing function, with an SNS topic instead. AWS accepts
	// a standard topic as a dead-letter target as readily as a standard queue.
	f := newDeadLetterFixture(t, &failingRuntime{requestID: "sns-req-id"}, testDLTopic)

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the event is published to the topic, carrying the same attributes.
	delivery := f.router.awaitDelivery(t)
	if got := delivery.form.Get("Action"); got != "Publish" {
		t.Fatalf("Action = %q, want Publish", got)
	}
	if got := delivery.form.Get("TopicArn"); got != testDLTopic {
		t.Errorf("TopicArn = %q, want %q", got, testDLTopic)
	}
	if got := delivery.form.Get("Message"); got != testDLQEvent {
		t.Errorf("Message = %q, want the original event %q", got, testDLQEvent)
	}
	attrs := snsAttributes(delivery)
	assertAttribute(t, attrs, "RequestID", "String", "sns-req-id")
	assertAttribute(t, attrs, "ErrorCode", "Number", "200")
}

func TestInvokeAsync_failedEventIsRetriedTwiceBeforeItIsDeadLettered(t *testing.T) {
	// Given: a function that always fails, with a dead-letter queue. AWS runs
	// an asynchronous invocation "up to two more times" when the function
	// returns an error, so a DLQ that fills on the first failure is reporting a
	// permanent failure the function has not yet had.
	rt := &failingRuntime{requestID: "retried-req"}
	f := newDeadLetterFixture(t, rt, testDLQARN)

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the handler ran three times — the initial attempt plus two retries.
	// The literal is AWS's documented default rather than asyncInvokeAttempts:
	// asserting against the constant would make the test agree with whatever
	// the code happens to say, which is the one thing it must not do.
	const awsAsyncAttempts = 3
	if got := rt.invokeCount(); got != awsAsyncAttempts {
		t.Errorf("handler ran %d time(s), want %d (the initial attempt plus AWS's two retries)", got, awsAsyncAttempts)
	}

	// And: exactly one event reached the queue, carrying the last attempt's
	// request ID. Three attempts must not become three dead-letter messages.
	delivery := f.router.awaitDelivery(t)
	attrs := sqsAttributes(t, delivery)
	assertAttribute(t, attrs, "RequestID", "String", "retried-req")
	assertNoDelivery(t, f)
}

func TestInvokeAsync_eventThatSucceedsOnARetryIsNotDeadLettered(t *testing.T) {
	// Given: a function that fails once and then succeeds — a cold-start
	// flake, or a dependency that was briefly unavailable.
	rt := &failingRuntime{requestID: "recovered-req", failUntil: 1}
	f := newDeadLetterFixture(t, rt, testDLQARN)

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the retry ran and succeeded, so nothing was dead-lettered. Retrying
	// is only worth doing if a recovery stops the delivery.
	if got := rt.invokeCount(); got != 2 {
		t.Errorf("handler ran %d time(s), want 2 (one failure then a successful retry)", got)
	}
	assertNoDelivery(t, f)
}

func TestInvokeAsync_invocationThatNeverRanIsDeadLetteredToo(t *testing.T) {
	// Given: a function with a dead-letter queue whose cold start cannot be
	// served. The event has still been accepted with a 202 and there is nobody
	// left to hand the failure back to, so dropping it silently is the one
	// outcome the dead-letter queue exists to prevent.
	f := newDeadLetterFixture(t, &unavailableRuntime{}, testDLQARN)

	// When: it is invoked asynchronously.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the event reaches the queue, with a request ID minted for it —
	// nothing ever allocated one, and AWS always reports the attribute.
	delivery := f.router.awaitDelivery(t)
	if got, _ := delivery.json["MessageBody"].(string); got != testDLQEvent {
		t.Errorf("MessageBody = %q, want the original event %q", got, testDLQEvent)
	}
	attrs := sqsAttributes(t, delivery)
	if attrs["RequestID"]["StringValue"] == "" {
		t.Error("RequestID attribute is empty, want a minted request ID")
	}
	assertAttribute(t, attrs, "ErrorCode", "Number", "200")
}

func TestInvokeAsync_withoutADeadLetterTargetNothingIsDelivered(t *testing.T) {
	// Given: a function that always fails and has no dead-letter queue — the
	// overwhelmingly common case, which must stay a no-op rather than an error.
	f := newDeadLetterFixture(t, &failingRuntime{requestID: "no-dlq"}, "")

	// When: it is invoked asynchronously and the async machinery drains.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: nothing was delivered anywhere.
	assertNoDelivery(t, f)
}

func TestInvokeAsync_successfulInvocationIsNotDeadLettered(t *testing.T) {
	// Given: a function with a dead-letter queue whose handler returns normally.
	f := newDeadLetterFixture(t, &succeedingRuntime{}, testDLQARN)

	// When: it is invoked asynchronously and the async machinery drains.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: the queue stays empty. A DLQ that collects successes is worse than
	// one that collects nothing.
	assertNoDelivery(t, f)
}

func TestInvokeAsync_deadLetterTargetThatIsNotAQueueOrTopicIsNotDelivered(t *testing.T) {
	// Given: a failing function whose dead-letter target names a Kinesis
	// stream. AWS allows only a standard SQS queue or a standard SNS topic, so
	// there is nothing correct to do with this — but it must not take the
	// invocation down with it.
	f := newDeadLetterFixture(t, &failingRuntime{requestID: "bad-target"},
		"arn:aws:kinesis:us-east-1:000000000000:stream/not-a-dlq")

	// When: it is invoked asynchronously and the async machinery drains.
	f.invokeAsyncAndDrain(t, testDLQEvent)

	// Then: nothing was delivered.
	assertNoDelivery(t, f)
}

// ---- Assertion helpers -----------------------------------------------------

// sqsAttributes reads the MessageAttributes map out of a SendMessage body.
func sqsAttributes(t *testing.T, delivery recordedDelivery) map[string]map[string]string {
	t.Helper()
	raw, ok := delivery.json["MessageAttributes"].(map[string]any)
	if !ok {
		t.Fatalf("SendMessage carried no MessageAttributes: %v", delivery.json)
	}
	out := make(map[string]map[string]string, len(raw))
	for name, value := range raw {
		fields, _ := value.(map[string]any)
		dataType, _ := fields["DataType"].(string)
		stringValue, _ := fields["StringValue"].(string)
		out[name] = map[string]string{"DataType": dataType, "StringValue": stringValue}
	}
	return out
}

// snsAttributes reassembles the numbered MessageAttributes.entry.N form fields
// of a Publish request into the same shape.
func snsAttributes(delivery recordedDelivery) map[string]map[string]string {
	out := map[string]map[string]string{}
	for i := 1; ; i++ {
		prefix := "MessageAttributes.entry." + strconv.Itoa(i) + "."
		name := delivery.form.Get(prefix + "Name")
		if name == "" {
			return out
		}
		out[name] = map[string]string{
			"DataType":    delivery.form.Get(prefix + "Value.DataType"),
			"StringValue": delivery.form.Get(prefix + "Value.StringValue"),
		}
	}
}

func assertAttribute(t *testing.T, attrs map[string]map[string]string, name, dataType, value string) {
	t.Helper()
	attr, ok := attrs[name]
	if !ok {
		t.Errorf("message attribute %s is absent", name)
		return
	}
	if attr["DataType"] != dataType {
		t.Errorf("%s DataType = %q, want %q", name, attr["DataType"], dataType)
	}
	if attr["StringValue"] != value {
		t.Errorf("%s StringValue = %q, want %q", name, attr["StringValue"], value)
	}
}

// assertNoDelivery fails when anything reached the root router. StopAsync has
// already drained the invocation, so an empty channel is a conclusion rather
// than a race.
func assertNoDelivery(t *testing.T, f *deadLetterFixture) {
	t.Helper()
	select {
	case delivery := <-f.router.deliveries:
		t.Fatalf("delivered %+v, want nothing", delivery)
	default:
	}
}
