package sns

// metrics_sns_test.go proves the phase-2 AWS/SNS metric catalogue
// (metrics_sns.go, docs/plans/service-metrics-platform.md) is recorded at
// Publish and at each subscription's delivery outcome, using a real
// internal/metrics.Recorder (not a stub) and reading it back the same way
// CloudWatch's read-through does — metrics.Service.QueryRange.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/metrics"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/state"
)

// newMetricsFanOutFixture is newLambdaFanOutFixture's twin, wired to a real
// metrics recorder over a shared mock clock.
func newMetricsFanOutFixture(t *testing.T, topicName string) (*Service, *metrics.Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	st := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	svc := New(cfg, st, zap.NewNop(), mock)
	rec := metrics.NewRecorder(st, mock, zap.NewNop())
	svc.InitMetrics(rec)

	arn := protocol.TopicARN(cfg.Region, cfg.AccountID, topicName)
	if aerr := svc.handler.snsStore.putTopic(context.Background(), &Topic{Name: topicName, ARN: arn}); aerr != nil {
		t.Fatalf("seed topic: %v", aerr.Message)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rec.Stop(ctx)
	})
	return svc, rec, mock
}

func snsPublish(t *testing.T, svc *Service, form map[string]string) {
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
	svc.Stop(context.Background()) // drains fan-out
}

func snsSum(t *testing.T, rec *metrics.Service, name, topicName string, now time.Time) float64 {
	t.Helper()
	buckets, err := rec.QueryRange(context.Background(), "AWS/SNS", name,
		[]metrics.Dimension{{Name: "TopicName", Value: topicName}}, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange %s: %v", name, err)
	}
	var sum float64
	for _, b := range buckets {
		sum += b.Sum
	}
	return sum
}

func TestPublish_RecordsNumberOfMessagesPublishedAndSize(t *testing.T) {
	svc, rec, mock := newMetricsFanOutFixture(t, "orders")
	topicARN := protocol.TopicARN(svc.cfg.Region, svc.cfg.AccountID, "orders")

	snsPublish(t, svc, map[string]string{
		"TopicArn": topicARN,
		"Message":  "order placed",
	})

	now := mock.Now().UTC()
	if got, want := snsSum(t, rec, "NumberOfMessagesPublished", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfMessagesPublished Sum = %v, want %v", got, want)
	}
	if got, want := snsSum(t, rec, "PublishSize", "orders", now), float64(len("order placed")); got != want {
		t.Fatalf("PublishSize Sum = %v, want %v", got, want)
	}
}

func TestPublish_LambdaDeliverySuccess_RecordsNotificationsDelivered(t *testing.T) {
	svc, rec, mock := newMetricsFanOutFixture(t, "orders")
	svc.InitLambdaDelivery(&fakeInvoker{}, nil)
	addSubscription(t, svc, "orders", "lambda", testFunctionARN, nil)
	topicARN := protocol.TopicARN(svc.cfg.Region, svc.cfg.AccountID, "orders")

	snsPublish(t, svc, map[string]string{"TopicArn": topicARN, "Message": "hi"})

	now := mock.Now().UTC()
	if got, want := snsSum(t, rec, "NumberOfNotificationsDelivered", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfNotificationsDelivered Sum = %v, want %v", got, want)
	}
	if got := snsSum(t, rec, "NumberOfNotificationsFailed", "orders", now); got != 0 {
		t.Fatalf("NumberOfNotificationsFailed Sum on a successful delivery = %v, want 0", got)
	}
}

func TestPublish_LambdaDeliveryFailure_RecordsNotificationsFailed(t *testing.T) {
	svc, rec, mock := newMetricsFanOutFixture(t, "orders")
	svc.InitLambdaDelivery(&fakeInvoker{err: errors.New("invoke failed")}, nil)
	addSubscription(t, svc, "orders", "lambda", testFunctionARN, nil)
	topicARN := protocol.TopicARN(svc.cfg.Region, svc.cfg.AccountID, "orders")

	snsPublish(t, svc, map[string]string{"TopicArn": topicARN, "Message": "hi"})

	now := mock.Now().UTC()
	if got, want := snsSum(t, rec, "NumberOfNotificationsFailed", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfNotificationsFailed Sum = %v, want %v", got, want)
	}
	if got := snsSum(t, rec, "NumberOfNotificationsDelivered", "orders", now); got != 0 {
		t.Fatalf("NumberOfNotificationsDelivered Sum on a failed delivery = %v, want 0", got)
	}
}

// TestPublish_UnimplementedProtocol_RecordsNotificationsFailed pins that
// fanOut's default case (a subscribed protocol with no delivery
// implementation) is still counted as a failed delivery, not silently
// dropped — matching failDelivery's own "lost loudly" contract.
func TestPublish_UnimplementedProtocol_RecordsNotificationsFailed(t *testing.T) {
	svc, rec, mock := newMetricsFanOutFixture(t, "orders")
	addSubscription(t, svc, "orders", "application-2-way", "endpoint-arn", nil) // no case in fanOut's switch
	topicARN := protocol.TopicARN(svc.cfg.Region, svc.cfg.AccountID, "orders")

	snsPublish(t, svc, map[string]string{"TopicArn": topicARN, "Message": "hi"})

	now := mock.Now().UTC()
	if got, want := snsSum(t, rec, "NumberOfNotificationsFailed", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfNotificationsFailed Sum = %v, want %v", got, want)
	}
}

// TestPublish_UnwiredEmailDependency_RecordsNotificationsFailed pins #1306's
// fix: a protocol whose delivery dependency was never wired into this
// instance (no svc.InitEmailDelivery call — the mailer stays nil) must still
// record NumberOfNotificationsFailed via failDelivery, not silently continue
// past the subscriber recording neither Delivered nor Failed — the exact gap
// metrics_sns.go's file doc (from #1268) flagged as a candidate follow-up.
// See handler_publish_unwired_test.go for the DLQ/Warn/regression coverage.
func TestPublish_UnwiredEmailDependency_RecordsNotificationsFailed(t *testing.T) {
	svc, rec, mock := newMetricsFanOutFixture(t, "orders")
	// Deliberately no svc.InitEmailDelivery call — the mailer stays nil.
	addSubscription(t, svc, "orders", "email", "someone@example.com", nil)
	topicARN := protocol.TopicARN(svc.cfg.Region, svc.cfg.AccountID, "orders")

	snsPublish(t, svc, map[string]string{"TopicArn": topicARN, "Message": "hi"})

	now := mock.Now().UTC()
	if got, want := snsSum(t, rec, "NumberOfNotificationsFailed", "orders", now), 1.0; got != want {
		t.Fatalf("NumberOfNotificationsFailed Sum = %v, want %v", got, want)
	}
	if got := snsSum(t, rec, "NumberOfNotificationsDelivered", "orders", now); got != 0 {
		t.Fatalf("NumberOfNotificationsDelivered Sum on an unwired dependency = %v, want 0", got)
	}
}

func TestSNSMetrics_NilRecorderIsNoOp(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	arn := protocol.TopicARN(cfg.Region, cfg.AccountID, "orders")
	if aerr := svc.handler.snsStore.putTopic(context.Background(), &Topic{Name: "orders", ARN: arn}); aerr != nil {
		t.Fatalf("seed topic: %v", aerr.Message)
	}
	snsPublish(t, svc, map[string]string{"TopicArn": arn, "Message": "hi"})
}
