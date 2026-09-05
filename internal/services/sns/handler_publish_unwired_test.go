package sns

// handler_publish_unwired_test.go is issue #1306's failing-first coverage.
//
// fanOut must treat an unwired delivery dependency (nil enqueuer, mailer,
// smsSender, or outbound) as a delivery FAILURE — routed through the same
// failDelivery funnel every other protocol's failure branch already uses —
// rather than silently `continue`ing past the subscriber. Before this fix
// such a delivery was neither attempted, dead-lettered, nor recorded as
// failed: it simply vanished, exactly the gap fanOut's own doc comment (and
// metrics_sns.go's file doc, wiring #1268's metrics) flagged as a candidate
// follow-up rather than fixed at the time.
//
// See metrics_sns_test.go's TestPublish_UnwiredEmailDependency_RecordsNotificationsFailed
// for the metrics half of this coverage.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/smtp"
	"github.com/overcast-sh/overcast/internal/state"
)

// unwiredWarnMsg is the exact Warn message warnUnwiredOnce logs — pinned once
// here rather than repeated per test.
const unwiredWarnMsg = "SNS fan-out: delivery dependency not wired for this protocol — every delivery to it will fail until this server is configured for it"

// fakeMailer is a smtp.Mailer that never fails — used only where a test needs
// email delivery genuinely wired (the wired-protocols regression), as
// opposed to the unwired-dependency tests, which deliberately never call
// InitEmailDelivery at all.
type fakeMailer struct{}

func (fakeMailer) Send(context.Context, string, []string, string, string, string) error { return nil }
func (fakeMailer) SendRaw(context.Context, string, []string, []byte) error              { return nil }

// newUnwiredFixture builds an SNS service with an observed logger — so the
// once-per-(topic,protocol) Warn can be asserted — and an SQS enqueuer
// already wired: the shared dead-letter path every protocol's failure branch
// uses, so a test can leave only the ONE dependency it is exercising unwired
// and still assert DLQ redirection, exactly like
// TestPublish_lambdaSubscription_deliveryFailureGoesToDLQ does for a runtime
// invocation failure (handler_publish_lambda_test.go).
func newUnwiredFixture(t *testing.T, topicName string) (*Service, *fakeEnqueuer, *observer.ObservedLogs) {
	t.Helper()
	core, observed := observer.New(zapcore.WarnLevel)
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	svc := New(cfg, state.NewMemoryStore(), zap.New(core), clock.New())

	eq := newFakeEnqueuer()
	svc.InitSQSDelivery(eq)

	arn := protocol.TopicARN(cfg.Region, cfg.AccountID, topicName)
	if aerr := svc.handler.snsStore.putTopic(context.Background(), &Topic{Name: topicName, ARN: arn}); aerr != nil {
		t.Fatalf("seed topic: %v", aerr.Message)
	}
	return svc, eq, observed
}

// TestFanOut_unwiredSQSDependency_failsDeliveryAndWarnsOnce covers the one
// protocol newUnwiredFixture can't help with, since its own dependency IS the
// SQS enqueuer: no svc.InitSQSDelivery call at all, so the enqueuer is nil
// exactly as it would be on a server nobody wired SQS delivery into.
func TestFanOut_unwiredSQSDependency_failsDeliveryAndWarnsOnce(t *testing.T) {
	// Given: a topic with an sqs subscription and no SQS delivery wired.
	core, observed := observer.New(zapcore.WarnLevel)
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	svc := New(cfg, state.NewMemoryStore(), zap.New(core), clock.New())
	arn := protocol.TopicARN(cfg.Region, cfg.AccountID, "orders")
	if aerr := svc.handler.snsStore.putTopic(context.Background(), &Topic{Name: "orders", ARN: arn}); aerr != nil {
		t.Fatalf("seed topic: %v", aerr.Message)
	}
	sub := &Subscription{
		SubscriptionARN: arn + ":00000000-0000-0000-0000-000000000001",
		TopicARN:        arn,
		TopicName:       "orders",
		Protocol:        "sqs",
		Endpoint:        "arn:aws:sqs:us-east-1:000000000000:orders-queue",
		QueueName:       "orders-queue",
		Owner:           cfg.AccountID,
	}
	if aerr := svc.handler.snsStore.putSubscription(context.Background(), sub); aerr != nil {
		t.Fatalf("seed subscription: %v", aerr.Message)
	}

	// When: two messages are published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "first"})
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "second"})

	// Then: fanOut's failure log fires for every attempt — the message is
	// never silently dropped...
	if n := observed.FilterMessage("SNS fan-out: delivery failed").Len(); n != 2 {
		t.Fatalf("delivery-failed log entries = %d, want 2 (once per publish)", n)
	}
	// ...but the operator-facing unwired-dependency Warn fires only once.
	entries := observed.FilterMessage(unwiredWarnMsg).All()
	if len(entries) != 1 {
		t.Fatalf("unwired-dependency warn entries = %d, want 1 (once per topic+protocol)", len(entries))
	}
	ctx := entries[0].ContextMap()
	if got, want := ctx["reason"], "SQS enqueuer is not wired on this server"; got != want {
		t.Errorf("warn reason = %v, want %q", got, want)
	}
	if got, want := ctx["protocol"], "sqs"; got != want {
		t.Errorf("warn protocol = %v, want %q", got, want)
	}
	if got, want := ctx["topic"], "orders"; got != want {
		t.Errorf("warn topic = %v, want %q", got, want)
	}
}

// TestFanOut_unwiredEmailDependency_deadLettersAndWarnsOnce covers email and
// email-json's shared nil-mailer branch.
func TestFanOut_unwiredEmailDependency_deadLettersAndWarnsOnce(t *testing.T) {
	// Given: a topic with an email subscription and a RedrivePolicy DLQ, but
	// no svc.InitEmailDelivery call — the mailer stays nil.
	svc, eq, observed := newUnwiredFixture(t, "orders")
	sub := addSubscription(t, svc, "orders", "email", "someone@example.com", map[string]string{
		"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:email-dlq"}`,
	})

	// When: two messages are published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "first"})
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "second"})

	// Then: every attempt is dead-lettered — real AWS would have attempted
	// (and could have failed) delivery too; nothing here is silently dropped.
	if bodies := eq.enqueued("email-dlq"); len(bodies) != 2 {
		t.Fatalf("dead-lettered messages = %d, want 2", len(bodies))
	}
	// And: the operator-facing Warn fires exactly once, naming the gap.
	entries := observed.FilterMessage(unwiredWarnMsg).All()
	if len(entries) != 1 {
		t.Fatalf("unwired-dependency warn entries = %d, want 1", len(entries))
	}
	ctx := entries[0].ContextMap()
	if got, want := ctx["reason"], "SMTP mailer is not configured"; got != want {
		t.Errorf("warn reason = %v, want %q", got, want)
	}
	if got, want := ctx["protocol"], "email"; got != want {
		t.Errorf("warn protocol = %v, want %q", got, want)
	}
}

// TestFanOut_unwiredSMSDependency_deadLettersAndWarnsOnce covers the sms
// nil-smsSender branch.
func TestFanOut_unwiredSMSDependency_deadLettersAndWarnsOnce(t *testing.T) {
	// Given: a topic with an sms subscription and a RedrivePolicy DLQ, but no
	// svc.InitSMSDelivery call — the SMS sender stays nil.
	svc, eq, observed := newUnwiredFixture(t, "orders")
	sub := addSubscription(t, svc, "orders", "sms", "+15555550100", map[string]string{
		"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:sms-dlq"}`,
	})

	// When: a message is published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "hi"})

	// Then: it is dead-lettered, and the Warn names the missing wiring.
	if bodies := eq.enqueued("sms-dlq"); len(bodies) != 1 {
		t.Fatalf("dead-lettered messages = %d, want 1", len(bodies))
	}
	entries := observed.FilterMessage(unwiredWarnMsg).All()
	if len(entries) != 1 {
		t.Fatalf("unwired-dependency warn entries = %d, want 1", len(entries))
	}
	if got, want := entries[0].ContextMap()["reason"], "SMS sender is not configured"; got != want {
		t.Errorf("warn reason = %v, want %q", got, want)
	}
}

// TestFanOut_unwiredHTTPDependency_deadLettersAndWarnsOnce covers http/https's
// shared nil-outbound branch.
func TestFanOut_unwiredHTTPDependency_deadLettersAndWarnsOnce(t *testing.T) {
	// Given: a topic with an https subscription and a RedrivePolicy DLQ, but
	// no svc.InitOutboundCapture call — the outbound handle stays nil.
	svc, eq, observed := newUnwiredFixture(t, "orders")
	sub := addSubscription(t, svc, "orders", "https", "https://example.com/hook", map[string]string{
		"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:webhook-dlq"}`,
	})

	// When: a message is published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "hi"})

	// Then: it is dead-lettered, and the Warn names the missing wiring.
	if bodies := eq.enqueued("webhook-dlq"); len(bodies) != 1 {
		t.Fatalf("dead-lettered messages = %d, want 1", len(bodies))
	}
	entries := observed.FilterMessage(unwiredWarnMsg).All()
	if len(entries) != 1 {
		t.Fatalf("unwired-dependency warn entries = %d, want 1", len(entries))
	}
	if got, want := entries[0].ContextMap()["reason"], "outbound webhook capture is not configured"; got != want {
		t.Errorf("warn reason = %v, want %q", got, want)
	}
}

// TestFanOut_unwiredLambdaDependency_deadLettersAndWarnsOnce covers lambda's
// nil-invoker branch — failDelivery was already called here before #1306
// (deliverToLambda), so this test only pins the newly-added Warn-once.
func TestFanOut_unwiredLambdaDependency_deadLettersAndWarnsOnce(t *testing.T) {
	// Given: a topic with a lambda subscription and a RedrivePolicy DLQ, but
	// no svc.InitLambdaDelivery call — the invoker stays nil.
	svc, eq, observed := newUnwiredFixture(t, "orders")
	sub := addSubscription(t, svc, "orders", "lambda", testFunctionARN, map[string]string{
		"RedrivePolicy": `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:lambda-dlq"}`,
	})

	// When: a message is published.
	publish(t, svc, map[string]string{"TopicArn": sub.TopicARN, "Message": "hi"})

	// Then: it is dead-lettered (pre-existing behaviour), and the Warn fires.
	if bodies := eq.enqueued("lambda-dlq"); len(bodies) != 1 {
		t.Fatalf("dead-lettered messages = %d, want 1", len(bodies))
	}
	entries := observed.FilterMessage(unwiredWarnMsg).All()
	if len(entries) != 1 {
		t.Fatalf("unwired-dependency warn entries = %d, want 1", len(entries))
	}
	if got, want := entries[0].ContextMap()["reason"], "Lambda invoker is not wired on this server"; got != want {
		t.Errorf("warn reason = %v, want %q", got, want)
	}
}

// putUniqueSubscription seeds one subscription with a fresh, unique
// SubscriptionARN — unlike addSubscription (handler_publish_lambda_test.go),
// which mints every call with the same fixed ARN suffix and so silently
// overwrites a prior subscription in the store the moment two calls target
// the same topic. Use this whenever a test puts more than one subscription
// on one topic.
func putUniqueSubscription(t *testing.T, svc *Service, topicName, proto, endpoint, queueName string, attrs map[string]string) *Subscription {
	t.Helper()
	cfg := svc.cfg
	topicARN := protocol.TopicARN(cfg.Region, cfg.AccountID, topicName)
	sub := &Subscription{
		SubscriptionARN: topicARN + ":" + uuid.New().String(),
		TopicARN:        topicARN,
		TopicName:       topicName,
		Protocol:        proto,
		Endpoint:        endpoint,
		QueueName:       queueName,
		Owner:           cfg.AccountID,
		Attributes:      attrs,
	}
	if aerr := svc.handler.snsStore.putSubscription(context.Background(), sub); aerr != nil {
		t.Fatalf("seed subscription: %v", aerr.Message)
	}
	return sub
}

// TestFanOut_wiredProtocolsNeverWarnUnwired is the regression half: every
// protocol with its dependency actually wired (sqs, lambda, email, sms,
// https) must deliver normally and must never trip the unwired-dependency
// Warn — proving this fix only changes behaviour for a genuinely-nil
// dependency, not for a subscription that is delivering successfully. This
// is the same shape PR #1119 (S3→SNS→SQS) and #1136 (Publish) already rely
// on staying green.
func TestFanOut_wiredProtocolsNeverWarnUnwired(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	svc := New(cfg, state.NewMemoryStore(), zap.New(core), clock.New())

	eq := newFakeEnqueuer()
	svc.InitSQSDelivery(eq)
	svc.InitLambdaDelivery(&fakeInvoker{}, nil)
	svc.InitEmailDelivery(fakeMailer{})
	mailStore := smtp.NewMailStore(10)
	svc.InitSMSDelivery(smtp.NewMockSMSSender(mailStore))
	svc.InitOutboundCapture(smtp.NewMockOutboundCapture(mailStore, nil))

	arn := protocol.TopicARN(cfg.Region, cfg.AccountID, "orders")
	if aerr := svc.handler.snsStore.putTopic(context.Background(), &Topic{Name: "orders", ARN: arn}); aerr != nil {
		t.Fatalf("seed topic: %v", aerr.Message)
	}
	// addSubscription (handler_publish_lambda_test.go) mints every
	// subscription with the same fixed ARN suffix, which is fine for the
	// single-subscription fixtures it was written for but collides (same
	// store key, last write wins) the moment more than one subscription
	// lands on the same topic — exactly what this regression test needs.
	// putUniqueSubscription below is this test's own helper for that case.
	putUniqueSubscription(t, svc, "orders", "sqs", "arn:aws:sqs:us-east-1:000000000000:orders-queue", "orders-queue", nil)
	putUniqueSubscription(t, svc, "orders", "lambda", testFunctionARN, "", nil)
	putUniqueSubscription(t, svc, "orders", "email", "someone@example.com", "", nil)
	putUniqueSubscription(t, svc, "orders", "sms", "+15555550100", "", nil)
	putUniqueSubscription(t, svc, "orders", "https", "https://example.com/hook", "", nil)

	publish(t, svc, map[string]string{"TopicArn": arn, "Message": "hi"})

	if bodies := eq.enqueued("orders-queue"); len(bodies) != 1 {
		t.Fatalf("sqs delivery = %d messages, want 1 — a wired protocol must still deliver", len(bodies))
	}
	if n := observed.FilterMessage(unwiredWarnMsg).Len(); n != 0 {
		t.Fatalf("unwired-dependency warn entries = %d, want 0 — every dependency here is wired", n)
	}
	if n := observed.FilterMessage("SNS fan-out: delivery failed").Len(); n != 0 {
		t.Fatalf("delivery-failed log entries = %d, want 0 — every subscription here should deliver successfully", n)
	}
}
