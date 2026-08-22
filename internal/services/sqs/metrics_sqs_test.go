package sqs

// metrics_sqs_test.go proves the phase-2 AWS/SQS metric catalogue
// (metrics_sqs.go, docs/plans/service-metrics-platform.md) is recorded at
// the right outcome boundary with the right dimensions/units, using a real
// internal/metrics.Recorder (not a stub) and reading it back the same way
// CloudWatch's read-through (internal/services/cloudwatch/metrics_bridge.go)
// does — metrics.Service.QueryRange — so this is an integration test of the
// actual recording path, not just a call-count assertion.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// newMetricsTestHandler returns an SQS Handler wired to a real metrics
// recorder over a shared mock clock, plus the recorder itself for QueryRange
// assertions and a Service for gauge-sampler tests.
func newMetricsTestHandler(t *testing.T) (*Handler, *Service, *metrics.Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	st := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	log := serviceutil.NewServiceLogger(zap.NewNop(), serviceName)
	h := newHandler(cfg, st, log, mock)
	h.bus = events.NewBus()
	rec := metrics.NewRecorder(st, mock, zap.NewNop())
	h.metrics = rec
	svc := &Service{cfg: cfg, store: st, log: log, handler: h}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rec.Stop(ctx)
	})
	return h, svc, rec, mock
}

func sumSeries(t *testing.T, rec *metrics.Service, name, queueName string, now time.Time) float64 {
	t.Helper()
	buckets, err := rec.QueryRange(context.Background(), "AWS/SQS", name,
		[]metrics.Dimension{{Name: "QueueName", Value: queueName}}, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange %s: %v", name, err)
	}
	var sum float64
	for _, b := range buckets {
		sum += b.Sum
	}
	return sum
}

func seriesExists(t *testing.T, rec *metrics.Service, name, queueName string, now time.Time) (bool, float64) {
	t.Helper()
	buckets, err := rec.QueryRange(context.Background(), "AWS/SQS", name,
		[]metrics.Dimension{{Name: "QueueName", Value: queueName}}, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange %s: %v", name, err)
	}
	if len(buckets) == 0 {
		return false, 0
	}
	return true, buckets[len(buckets)-1].Sum
}

func TestSendMessage_RecordsNumberOfMessagesSentAndSize(t *testing.T) {
	h, _, rec, mock := newMetricsTestHandler(t)
	ctx := context.Background()
	if _, aerr := h.createQueueTyped(ctx, &createQueueRequest{QueueName: "orders"}); aerr != nil {
		t.Fatalf("CreateQueue: %v", aerr)
	}

	if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "orders", MessageBody: "hello"}); aerr != nil {
		t.Fatalf("SendMessage: %v", aerr)
	}

	now := mock.Now().UTC()
	if got, want := sumSeries(t, rec, "NumberOfMessagesSent", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfMessagesSent Sum = %v, want %v", got, want)
	}
	if got, want := sumSeries(t, rec, "SentMessageSize", "orders", now), float64(len("hello")); got != want {
		t.Fatalf("SentMessageSize Sum = %v, want %v", got, want)
	}
}

func TestSendMessage_FIFODuplicateDoesNotDoubleCount(t *testing.T) {
	h, _, rec, mock := newMetricsTestHandler(t)
	ctx := context.Background()
	if _, aerr := h.createQueueTyped(ctx, &createQueueRequest{
		QueueName: "orders.fifo",
		Attributes: map[string]string{
			"FifoQueue":                 "true",
			"ContentBasedDeduplication": "true",
		},
	}); aerr != nil {
		t.Fatalf("CreateQueue: %v", aerr)
	}

	req := &sendMessageRequest{QueueUrl: "orders.fifo", MessageBody: "hello", MessageGroupId: "g1"}
	if _, aerr := h.sendMessageTyped(ctx, req); aerr != nil {
		t.Fatalf("SendMessage 1: %v", aerr)
	}
	if _, aerr := h.sendMessageTyped(ctx, req); aerr != nil {
		t.Fatalf("SendMessage 2 (dedup): %v", aerr)
	}

	now := mock.Now().UTC()
	if got, want := sumSeries(t, rec, "NumberOfMessagesSent", "orders.fifo", now), 1.0; got != want {
		t.Fatalf("NumberOfMessagesSent Sum after a deduplicated resend = %v, want %v (dedup must not double-count)", got, want)
	}
}

func TestReceiveMessage_RecordsReceivedOrEmptyReceive(t *testing.T) {
	h, _, rec, mock := newMetricsTestHandler(t)
	ctx := context.Background()
	if _, aerr := h.createQueueTyped(ctx, &createQueueRequest{QueueName: "orders"}); aerr != nil {
		t.Fatalf("CreateQueue: %v", aerr)
	}

	// Empty receive first.
	if _, aerr := h.receiveMessageTyped(ctx, &receiveMessageRequest{QueueUrl: "orders"}); aerr != nil {
		t.Fatalf("ReceiveMessage (empty): %v", aerr)
	}
	now := mock.Now().UTC()
	if got, want := sumSeries(t, rec, "NumberOfEmptyReceives", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfEmptyReceives Sum = %v, want %v", got, want)
	}
	if got := sumSeries(t, rec, "NumberOfMessagesReceived", "orders", now); got != 0 {
		t.Fatalf("NumberOfMessagesReceived Sum on an empty receive = %v, want 0", got)
	}

	// Now send + receive.
	if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "orders", MessageBody: "hi"}); aerr != nil {
		t.Fatalf("SendMessage: %v", aerr)
	}
	if _, aerr := h.receiveMessageTyped(ctx, &receiveMessageRequest{QueueUrl: "orders"}); aerr != nil {
		t.Fatalf("ReceiveMessage: %v", aerr)
	}
	if got, want := sumSeries(t, rec, "NumberOfMessagesReceived", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfMessagesReceived Sum = %v, want %v", got, want)
	}
}

func TestDeleteMessage_RecordsNumberOfMessagesDeleted(t *testing.T) {
	h, _, rec, mock := newMetricsTestHandler(t)
	ctx := context.Background()
	if _, aerr := h.createQueueTyped(ctx, &createQueueRequest{QueueName: "orders"}); aerr != nil {
		t.Fatalf("CreateQueue: %v", aerr)
	}
	if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "orders", MessageBody: "hi"}); aerr != nil {
		t.Fatalf("SendMessage: %v", aerr)
	}
	recv, aerr := h.receiveMessageTyped(ctx, &receiveMessageRequest{QueueUrl: "orders"})
	if aerr != nil || len(recv.Messages) != 1 {
		t.Fatalf("ReceiveMessage: %v (%d messages)", aerr, len(recv.Messages))
	}

	if _, aerr := h.deleteMessageTyped(ctx, &deleteMessageRequest{QueueUrl: "orders", ReceiptHandle: recv.Messages[0].ReceiptHandle}); aerr != nil {
		t.Fatalf("DeleteMessage: %v", aerr)
	}

	now := mock.Now().UTC()
	if got, want := sumSeries(t, rec, "NumberOfMessagesDeleted", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfMessagesDeleted Sum = %v, want %v", got, want)
	}
}

// TestGaugeSampler_PublishesQueueDepthEvenWithNoTraffic pins the plan's
// phase-2 rule that AWS samples ApproximateNumberOfMessagesVisible/
// NotVisible/Delayed once a minute for every active queue whether or not it
// has traffic — a genuinely-zero sampled gauge, not a missing-metric
// synthetic zero (see metrics_sqs.go's doc comment).
func TestGaugeSampler_PublishesQueueDepthEvenWithNoTraffic(t *testing.T) {
	h, svc, rec, mock := newMetricsTestHandler(t)
	ctx := context.Background()
	if _, aerr := h.createQueueTyped(ctx, &createQueueRequest{QueueName: "idle-queue"}); aerr != nil {
		t.Fatalf("CreateQueue: %v", aerr)
	}

	svc.sampleQueueGaugesOnce(ctx)

	now := mock.Now().UTC()
	for _, name := range []string{
		"ApproximateNumberOfMessagesVisible",
		"ApproximateNumberOfMessagesNotVisible",
		"ApproximateNumberOfMessagesDelayed",
	} {
		ok, val := seriesExists(t, rec, name, "idle-queue", now)
		if !ok {
			t.Fatalf("%s: expected a real sampled datapoint for an idle queue, found none", name)
		}
		if val != 0 {
			t.Fatalf("%s = %v on an idle queue, want 0", name, val)
		}
	}
	// ApproximateAgeOfOldestMessage must NOT be recorded for an empty queue —
	// there is no oldest message to report an age for (a real absent
	// observation, not a synthetic zero).
	if ok, _ := seriesExists(t, rec, "ApproximateAgeOfOldestMessage", "idle-queue", now); ok {
		t.Fatalf("ApproximateAgeOfOldestMessage recorded for an empty queue, want no observation")
	}
}

// TestGaugeSampler_TracksVisibleNotVisibleAndDelayed exercises all three
// buckets a message can be in when the gauge sampler runs.
func TestGaugeSampler_TracksVisibleNotVisibleAndDelayed(t *testing.T) {
	h, svc, rec, mock := newMetricsTestHandler(t)
	ctx := context.Background()
	if _, aerr := h.createQueueTyped(ctx, &createQueueRequest{QueueName: "mixed"}); aerr != nil {
		t.Fatalf("CreateQueue: %v", aerr)
	}

	// A delayed message (never received, still behind DelaySeconds) — sent
	// first so it is never the (only) visible candidate below.
	if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "mixed", MessageBody: "delayed", DelaySeconds: 60}); aerr != nil {
		t.Fatalf("SendMessage (delayed): %v", aerr)
	}
	// A message that will be received (making it in-flight) before anything
	// else is visible, so the receive below deterministically picks it.
	if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "mixed", MessageBody: "inflight"}); aerr != nil {
		t.Fatalf("SendMessage (to become inflight): %v", aerr)
	}
	one := 1
	recv, aerr := h.receiveMessageTyped(ctx, &receiveMessageRequest{QueueUrl: "mixed", MaxNumberOfMessages: &one})
	if aerr != nil {
		t.Fatalf("ReceiveMessage: %v", aerr)
	}
	if len(recv.Messages) != 1 {
		t.Fatalf("expected exactly 1 receivable message (the delayed one is not yet visible), got %d", len(recv.Messages))
	}
	// A message sent after the receive above, so it stays untouched/visible.
	if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "mixed", MessageBody: "visible"}); aerr != nil {
		t.Fatalf("SendMessage (visible): %v", aerr)
	}

	svc.sampleQueueGaugesOnce(ctx)

	now := mock.Now().UTC()
	if _, v := seriesExists(t, rec, "ApproximateNumberOfMessagesVisible", "mixed", now); v != 1 {
		t.Fatalf("ApproximateNumberOfMessagesVisible = %v, want 1 (the never-received, non-delayed message left over)", v)
	}
	if _, v := seriesExists(t, rec, "ApproximateNumberOfMessagesNotVisible", "mixed", now); v != 1 {
		t.Fatalf("ApproximateNumberOfMessagesNotVisible = %v, want 1 (the received-but-undeleted message)", v)
	}
	if _, v := seriesExists(t, rec, "ApproximateNumberOfMessagesDelayed", "mixed", now); v != 1 {
		t.Fatalf("ApproximateNumberOfMessagesDelayed = %v, want 1", v)
	}
}

// TestMetrics_NilRecorderIsNoOp pins that every recording call site is
// nil-safe when collection is disabled (Service.InitMetrics never called) —
// mirroring Lambda's h.metrics nil-guard contract.
func TestMetrics_NilRecorderIsNoOp(t *testing.T) {
	mock := clock.NewMock()
	st := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	log := serviceutil.NewServiceLogger(zap.NewNop(), serviceName)
	h := newHandler(cfg, st, log, mock)
	h.bus = events.NewBus()
	ctx := context.Background()

	if _, aerr := h.createQueueTyped(ctx, &createQueueRequest{QueueName: "q"}); aerr != nil {
		t.Fatalf("CreateQueue: %v", aerr)
	}
	if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "q", MessageBody: "x"}); aerr != nil {
		t.Fatalf("SendMessage with nil metrics recorder must still succeed: %v", aerr)
	}
}
