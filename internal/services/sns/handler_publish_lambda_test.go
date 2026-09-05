package sns

// handler_publish_lambda_test.go pins the wire shape of the SNS → Lambda
// event. The payload is the compatibility contract: a handler written against
// real AWS (or against aws-lambda-go's events.SNSEvent) must parse what
// Overcast delivers without modification.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/state"
)

// ---- Fakes -----------------------------------------------------------------

type capturedInvoke struct {
	arn     string
	payload []byte
}

// fakeInvoker records every async invocation and can be told to fail.
type fakeInvoker struct {
	mu    sync.Mutex
	calls []capturedInvoke
	err   error
}

func (f *fakeInvoker) InvokeEvent(_ context.Context, functionARN string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, capturedInvoke{arn: functionARN, payload: append([]byte(nil), payload...)})
	return f.err
}

func (f *fakeInvoker) captured() []capturedInvoke {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capturedInvoke(nil), f.calls...)
}

// fakeEnqueuer records SQS enqueues so dead-letter routing can be asserted.
type fakeEnqueuer struct {
	mu     sync.Mutex
	bodies map[string][]string
}

func newFakeEnqueuer() *fakeEnqueuer {
	return &fakeEnqueuer{bodies: map[string][]string{}}
}

func (f *fakeEnqueuer) EnqueueRaw(_ context.Context, queueName, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[queueName] = append(f.bodies[queueName], body)
	return nil
}

func (f *fakeEnqueuer) enqueued(queueName string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies[queueName]...)
}

// ---- Harness ---------------------------------------------------------------

const testFunctionARN = "arn:aws:lambda:us-east-1:000000000000:function:sns-handler"

// newLambdaFanOutFixture builds an SNS service with a single topic and returns
// it alongside the fakes wired into it.
func newLambdaFanOutFixture(t *testing.T, topicName string) (*Service, *fakeInvoker, *fakeEnqueuer) {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	inv := &fakeInvoker{}
	eq := newFakeEnqueuer()
	svc.InitLambdaDelivery(inv, nil)
	svc.InitSQSDelivery(eq)

	arn := protocol.TopicARN(cfg.Region, cfg.AccountID, topicName)
	if aerr := svc.handler.snsStore.putTopic(context.Background(), &Topic{Name: topicName, ARN: arn}); aerr != nil {
		t.Fatalf("seed topic: %v", aerr.Message)
	}
	return svc, inv, eq
}

// addSubscription stores a subscription on the fixture's topic.
func addSubscription(t *testing.T, svc *Service, topicName, proto, endpoint string, attrs map[string]string) *Subscription {
	t.Helper()
	cfg := svc.cfg
	topicARN := protocol.TopicARN(cfg.Region, cfg.AccountID, topicName)
	sub := &Subscription{
		SubscriptionARN: topicARN + ":00000000-0000-0000-0000-000000000001",
		TopicARN:        topicARN,
		TopicName:       topicName,
		Protocol:        proto,
		Endpoint:        endpoint,
		Owner:           cfg.AccountID,
		Attributes:      attrs,
	}
	if aerr := svc.handler.snsStore.putSubscription(context.Background(), sub); aerr != nil {
		t.Fatalf("seed subscription: %v", aerr.Message)
	}
	return sub
}

// publish drives the real Publish handler and waits for fan-out to drain.
func publish(t *testing.T, svc *Service, form map[string]string) {
	t.Helper()
	values := url.Values{}
	for k, v := range form {
		values.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("parse form: %v", err)
	}
	rec := httptest.NewRecorder()
	svc.handler.Publish(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Publish status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	// Stop drains the fan-out WaitGroup, so delivery is complete on return.
	svc.Stop(context.Background())
}

// ---- Tests -----------------------------------------------------------------

func TestPublish_lambdaSubscription_invokesFunction(t *testing.T) {
	// Given: a topic with a lambda-protocol subscription.
	svc, inv, _ := newLambdaFanOutFixture(t, "orders")
	sub := addSubscription(t, svc, "orders", "lambda", testFunctionARN, nil)

	// When: a message with attributes is published.
	publish(t, svc, map[string]string{
		"TopicArn":                       sub.TopicARN,
		"Message":                        "order placed",
		"Subject":                        "orders",
		"MessageAttributes.entry.1.Name": "eventType",
		"MessageAttributes.entry.1.Value.DataType":    "String",
		"MessageAttributes.entry.1.Value.StringValue": "created",
		"MessageAttributes.entry.2.Name":              "retries",
		"MessageAttributes.entry.2.Value.DataType":    "Number",
		"MessageAttributes.entry.2.Value.StringValue": "3",
	})

	// Then: the function is invoked exactly once.
	calls := inv.captured()
	if len(calls) != 1 {
		t.Fatalf("invocations = %d, want 1", len(calls))
	}
	if calls[0].arn != testFunctionARN {
		t.Errorf("invoked ARN = %q, want %q", calls[0].arn, testFunctionARN)
	}

	// And: the payload is the AWS SNS event shape Lambda handlers parse.
	var event struct {
		Records []struct {
			EventVersion         string `json:"EventVersion"`
			EventSubscriptionArn string `json:"EventSubscriptionArn"`
			EventSource          string `json:"EventSource"`
			SNS                  struct {
				Type              string  `json:"Type"`
				MessageID         string  `json:"MessageId"`
				TopicArn          string  `json:"TopicArn"`
				Subject           *string `json:"Subject"`
				Message           string  `json:"Message"`
				Timestamp         string  `json:"Timestamp"`
				SignatureVersion  string  `json:"SignatureVersion"`
				Signature         string  `json:"Signature"`
				SigningCertURL    string  `json:"SigningCertUrl"`
				UnsubscribeURL    string  `json:"UnsubscribeUrl"`
				MessageAttributes map[string]struct {
					Type  string `json:"Type"`
					Value string `json:"Value"`
				} `json:"MessageAttributes"`
			} `json:"Sns"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(calls[0].payload, &event); err != nil {
		t.Fatalf("decode lambda payload: %v\npayload: %s", err, calls[0].payload)
	}
	if len(event.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(event.Records))
	}
	rec := event.Records[0]
	if rec.EventSource != "aws:sns" {
		t.Errorf("EventSource = %q, want %q", rec.EventSource, "aws:sns")
	}
	if rec.EventVersion != "1.0" {
		t.Errorf("EventVersion = %q, want %q", rec.EventVersion, "1.0")
	}
	if rec.EventSubscriptionArn != sub.SubscriptionARN {
		t.Errorf("EventSubscriptionArn = %q, want %q", rec.EventSubscriptionArn, sub.SubscriptionARN)
	}
	if rec.SNS.Type != "Notification" {
		t.Errorf("Sns.Type = %q, want %q", rec.SNS.Type, "Notification")
	}
	if rec.SNS.Message != "order placed" {
		t.Errorf("Sns.Message = %q, want %q", rec.SNS.Message, "order placed")
	}
	if rec.SNS.Subject == nil || *rec.SNS.Subject != "orders" {
		t.Errorf("Sns.Subject = %v, want %q", rec.SNS.Subject, "orders")
	}
	if rec.SNS.TopicArn != sub.TopicARN {
		t.Errorf("Sns.TopicArn = %q, want %q", rec.SNS.TopicArn, sub.TopicARN)
	}
	if rec.SNS.MessageID == "" {
		t.Error("Sns.MessageId is empty")
	}
	if rec.SNS.SignatureVersion != "1" {
		t.Errorf("Sns.SignatureVersion = %q, want %q", rec.SNS.SignatureVersion, "1")
	}
	if rec.SNS.Signature == "" || rec.SNS.SigningCertURL == "" {
		t.Error("Sns.Signature / Sns.SigningCertUrl must be present")
	}
	if !strings.Contains(rec.SNS.UnsubscribeURL, "Action=Unsubscribe") {
		t.Errorf("Sns.UnsubscribeUrl = %q, want an Unsubscribe URL", rec.SNS.UnsubscribeURL)
	}
	// AWS formats notification timestamps with millisecond precision and a Z suffix.
	if len(rec.SNS.Timestamp) != len("2006-01-02T15:04:05.000Z") || !strings.HasSuffix(rec.SNS.Timestamp, "Z") {
		t.Errorf("Sns.Timestamp = %q, want AWS's 2006-01-02T15:04:05.000Z form", rec.SNS.Timestamp)
	}
	if got := rec.SNS.MessageAttributes["eventType"]; got.Type != "String" || got.Value != "created" {
		t.Errorf("MessageAttributes[eventType] = %+v, want {String created}", got)
	}
	if got := rec.SNS.MessageAttributes["retries"]; got.Type != "Number" || got.Value != "3" {
		t.Errorf("MessageAttributes[retries] = %+v, want {Number 3}", got)
	}
}

func TestPublish_lambdaSubscription_noSubjectIsNull(t *testing.T) {
	// Given: a lambda subscription and a message published without a Subject.
	svc, inv, _ := newLambdaFanOutFixture(t, "plain")
	sub := addSubscription(t, svc, "plain", "lambda", testFunctionARN, nil)

	// When: the message is published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "no subject"})

	// Then: Subject is JSON null and MessageAttributes is an empty object,
	// exactly as real AWS emits them.
	calls := inv.captured()
	if len(calls) != 1 {
		t.Fatalf("invocations = %d, want 1", len(calls))
	}
	body := string(calls[0].payload)
	if !strings.Contains(body, `"Subject":null`) {
		t.Errorf("payload should carry a null Subject, got: %s", body)
	}
	if !strings.Contains(body, `"MessageAttributes":{}`) {
		t.Errorf("payload should carry an empty MessageAttributes object, got: %s", body)
	}
}

func TestPublish_lambdaSubscription_filterPolicyExcludesMessage(t *testing.T) {
	// Given: a lambda subscription with a filter policy the message does not match.
	svc, inv, _ := newLambdaFanOutFixture(t, "filtered")
	sub := addSubscription(t, svc, "filtered", "lambda", testFunctionARN, map[string]string{
		"FilterPolicy": `{"eventType":["deleted"]}`,
	})

	// When: a non-matching message is published.
	publish(t, svc, map[string]string{
		"TopicArn":                       sub.TopicARN,
		"Message":                        "order placed",
		"MessageAttributes.entry.1.Name": "eventType",
		"MessageAttributes.entry.1.Value.DataType":    "String",
		"MessageAttributes.entry.1.Value.StringValue": "created",
	})

	// Then: the function is not invoked.
	if calls := inv.captured(); len(calls) != 0 {
		t.Fatalf("invocations = %d, want 0 (filter policy should exclude it)", len(calls))
	}
}

func TestPublish_lambdaSubscription_deliveryFailureGoesToDLQ(t *testing.T) {
	// Given: a lambda subscription whose invocation fails and a RedrivePolicy DLQ.
	svc, inv, eq := newLambdaFanOutFixture(t, "failing")
	inv.err = context.DeadlineExceeded
	sub := addSubscription(t, svc, "failing", "lambda", testFunctionARN, map[string]string{
		"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:sns-dlq"}`,
	})

	// When: a message is published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "boom"})

	// Then: the notification envelope is redirected to the dead-letter queue.
	bodies := eq.enqueued("sns-dlq")
	if len(bodies) != 1 {
		t.Fatalf("dead-lettered messages = %d, want 1", len(bodies))
	}
	var envelope struct {
		Message  string `json:"Message"`
		TopicArn string `json:"TopicArn"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &envelope); err != nil {
		t.Fatalf("decode DLQ body: %v\nbody: %s", err, bodies[0])
	}
	if envelope.Message != "boom" {
		t.Errorf("DLQ Message = %q, want %q", envelope.Message, "boom")
	}
	if envelope.TopicArn != sub.TopicARN {
		t.Errorf("DLQ TopicArn = %q, want %q", envelope.TopicArn, sub.TopicARN)
	}
}

func TestPublish_unknownProtocol_deadLettersRatherThanDropping(t *testing.T) {
	// Given: a subscription whose protocol has no delivery implementation.
	svc, _, eq := newLambdaFanOutFixture(t, "unknown")
	sub := addSubscription(t, svc, "unknown", "carrier-pigeon", "somewhere", map[string]string{
		"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:unknown-dlq"}`,
	})

	// When: a message is published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "nowhere to go"})

	// Then: it is dead-lettered, not silently discarded.
	if bodies := eq.enqueued("unknown-dlq"); len(bodies) != 1 {
		t.Fatalf("dead-lettered messages = %d, want 1", len(bodies))
	}
}
