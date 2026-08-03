package eventtarget

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	// Given: ARNs for every deliverable kind, plus a well-formed ARN naming a
	// service with no sink and a value that is not an ARN at all.
	supported := map[string]Kind{
		"arn:aws:lambda:us-east-1:000000000000:function:fn":                   KindLambda,
		"arn:aws:sqs:us-east-1:000000000000:queue":                            KindSQS,
		"arn:aws:sns:us-east-1:000000000000:topic":                            KindSNS,
		"arn:aws:states:us-east-1:000000000000:stateMachine:sm":               KindStepFunctions,
		"arn:aws:kinesis:us-east-1:000000000000:stream/s":                     KindKinesis,
		"arn:aws:firehose:us-east-1:000000000000:deliverystream/d":            KindFirehose,
		"arn:aws:ecs:us-east-1:000000000000:cluster/c":                        KindECS,
		"arn:aws-us-gov:lambda:us-gov-west-1:000000000000:function:partition": KindLambda,
	}
	for arn, want := range supported {
		got, err := Classify(arn)
		if err != nil {
			t.Fatalf("Classify(%q): %v", arn, err)
		}
		if got != want {
			t.Fatalf("Classify(%q) = %q, want %q", arn, got, want)
		}
	}

	// When/Then: an unsupported service is reported as such, not as malformed.
	_, err := Classify("arn:aws:logs:us-east-1:000000000000:log-group:/aws/events/x")
	var unsupported *UnsupportedKindError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Classify(logs ARN) error = %v, want *UnsupportedKindError", err)
	}
	if unsupported.Service != "logs" {
		t.Fatalf("UnsupportedKindError.Service = %q, want logs", unsupported.Service)
	}

	// And: shape violations are malformed, so callers can reject the request.
	for _, bad := range []string{"", "not-an-arn", "arn:aws:sqs:us-east-1:000000000000", "arn::sqs:us-east-1:0:q", "arn:aws:sqs:us-east-1:000000000000:"} {
		if _, err := Classify(bad); !errors.Is(err, ErrMalformedARN) {
			t.Fatalf("Classify(%q) error = %v, want ErrMalformedARN", bad, err)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if got := DisplayName(KindStepFunctions); got != "Step Functions" {
		t.Fatalf("DisplayName(states) = %q", got)
	}
	if got := DisplayName(Kind("logs")); got != "Unknown" {
		t.Fatalf("DisplayName(unsupported) = %q, want Unknown", got)
	}
}

func TestResourceAndFunctionName(t *testing.T) {
	cases := map[string]string{
		"arn:aws:sqs:us-east-1:000000000000:my-queue":               "my-queue",
		"arn:aws:kinesis:us-east-1:000000000000:stream/my-stream":   "my-stream",
		"arn:aws:firehose:us-east-1:000000000000:deliverystream/dd": "dd",
	}
	for arn, want := range cases {
		if got := ResourceName(arn); got != want {
			t.Fatalf("ResourceName(%q) = %q, want %q", arn, got, want)
		}
	}

	// A version or alias qualifier is dropped from a Lambda target ARN.
	if got := FunctionName("arn:aws:lambda:us-east-1:000000000000:function:fn:PROD"); got != "fn" {
		t.Fatalf("FunctionName(qualified) = %q, want fn", got)
	}
	if got := FunctionName("plain-name"); got != "plain-name" {
		t.Fatalf("FunctionName(bare) = %q", got)
	}
}

// capture records the requests a Dispatcher makes against the root router.
type capture struct {
	method string
	path   string
	target string
	ctype  string
	body   string
	status int
}

func newCaptureDispatcher(status int) (*Dispatcher, *capture) {
	c := &capture{status: status}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.method = r.Method
		c.path = r.URL.Path
		c.target = r.Header.Get("X-Amz-Target")
		c.ctype = r.Header.Get("Content-Type")
		c.body = string(body)
		w.WriteHeader(c.status)
	})
	return NewDispatcher(handler, "us-east-1"), c
}

func TestDeliver_perKindWireShape(t *testing.T) {
	event := []byte(`{"detail-type":"T"}`)

	t.Run("lambda invokes asynchronously", func(t *testing.T) {
		d, c := newCaptureDispatcher(http.StatusAccepted)
		if err := d.Deliver(context.Background(), Request{
			ARN:     "arn:aws:lambda:us-east-1:000000000000:function:fn",
			Payload: event,
		}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
		if c.path != "/2015-03-31/functions/fn/invocations" {
			t.Fatalf("path = %q", c.path)
		}
		if c.body != string(event) {
			t.Fatalf("body = %q, want the event verbatim", c.body)
		}
	})

	t.Run("sns publishes over the query protocol", func(t *testing.T) {
		d, c := newCaptureDispatcher(http.StatusOK)
		if err := d.Deliver(context.Background(), Request{
			ARN:     "arn:aws:sns:us-east-1:000000000000:topic",
			Payload: event,
		}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
		if !strings.Contains(c.ctype, "x-www-form-urlencoded") {
			t.Fatalf("Content-Type = %q", c.ctype)
		}
		if !strings.Contains(c.body, "Action=Publish") || !strings.Contains(c.body, "TopicArn=") {
			t.Fatalf("body = %q", c.body)
		}
	})

	t.Run("step functions starts an execution", func(t *testing.T) {
		d, c := newCaptureDispatcher(http.StatusOK)
		if err := d.Deliver(context.Background(), Request{
			ARN:           "arn:aws:states:us-east-1:000000000000:stateMachine:sm",
			Payload:       event,
			ExecutionName: "exec-1",
		}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
		if c.target != "AWSStepFunctions.StartExecution" {
			t.Fatalf("X-Amz-Target = %q", c.target)
		}
		if !strings.Contains(c.body, `"name":"exec-1"`) || !strings.Contains(c.body, `"stateMachineArn"`) {
			t.Fatalf("body = %q", c.body)
		}
	})

	t.Run("kinesis carries the partition key", func(t *testing.T) {
		d, c := newCaptureDispatcher(http.StatusOK)
		if err := d.Deliver(context.Background(), Request{
			ARN:          "arn:aws:kinesis:us-east-1:000000000000:stream/s",
			Payload:      event,
			PartitionKey: "pk-1",
		}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
		if c.target != "Kinesis_20131202.PutRecord" {
			t.Fatalf("X-Amz-Target = %q", c.target)
		}
		if !strings.Contains(c.body, `"PartitionKey":"pk-1"`) || !strings.Contains(c.body, `"StreamName":"s"`) {
			t.Fatalf("body = %q", c.body)
		}
	})

	t.Run("firehose wraps the payload in a record", func(t *testing.T) {
		d, c := newCaptureDispatcher(http.StatusOK)
		if err := d.Deliver(context.Background(), Request{
			ARN:     "arn:aws:firehose:us-east-1:000000000000:deliverystream/dd",
			Payload: event,
		}); err != nil {
			t.Fatalf("Deliver: %v", err)
		}
		if !strings.Contains(c.body, `"Record":{"Data":`) || !strings.Contains(c.body, `"DeliveryStreamName":"dd"`) {
			t.Fatalf("body = %q", c.body)
		}
	})
}

func TestDeliver_surfacesSinkErrors(t *testing.T) {
	// Given: a sink that rejects the delivery
	d, _ := newCaptureDispatcher(http.StatusNotFound)

	// When: a payload is delivered
	err := d.Deliver(context.Background(), Request{
		ARN:     "arn:aws:sqs:us-east-1:000000000000:missing",
		Payload: []byte("{}"),
	})

	// Then: the failure reaches the caller so it can retry or dead-letter
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the sink's status", err)
	}
}

func TestDeliver_unsupportedKindIsRefused(t *testing.T) {
	d, _ := newCaptureDispatcher(http.StatusOK)
	err := d.Deliver(context.Background(), Request{
		ARN:     "arn:aws:logs:us-east-1:000000000000:log-group:/x",
		Payload: []byte("{}"),
	})
	var unsupported *UnsupportedKindError
	if !errors.As(err, &unsupported) {
		t.Fatalf("err = %v, want *UnsupportedKindError", err)
	}
}

func TestDeliver_withoutRouterFails(t *testing.T) {
	var d *Dispatcher
	if err := d.Deliver(context.Background(), Request{ARN: "arn:aws:sqs:us-east-1:0:q"}); err == nil {
		t.Fatal("expected an error from a dispatcher with no router")
	}
}

func TestInvokeJSONTarget_carriesRegion(t *testing.T) {
	var gotRegion string
	d := NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRegion = r.Header.Get("X-Overcast-Region")
		w.WriteHeader(http.StatusOK)
	}), "us-east-1")

	if err := d.InvokeJSONTarget(context.Background(), "AmazonEC2ContainerServiceV20141113.RunTask", map[string]any{"cluster": "c"}); err != nil {
		t.Fatalf("InvokeJSONTarget: %v", err)
	}
	if gotRegion != "us-east-1" {
		t.Fatalf("X-Overcast-Region = %q, want the dispatcher default", gotRegion)
	}
}

// Guard against the recorder being reused across requests, which would make
// per-attempt failures indistinguishable.
func TestDo_usesAFreshRecorderPerAttempt(t *testing.T) {
	calls := 0
	d := NewDispatcher(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}), "us-east-1")

	req := Request{ARN: "arn:aws:sqs:us-east-1:000000000000:q", Payload: []byte("{}")}
	if err := d.Deliver(context.Background(), req); err == nil {
		t.Fatal("expected the first attempt to fail")
	}
	if err := d.Deliver(context.Background(), req); err != nil {
		t.Fatalf("expected the second attempt to succeed, got %v", err)
	}
}
