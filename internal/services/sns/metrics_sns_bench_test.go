package sns

// metrics_sns_bench_test.go sizes the cost service-metrics recording adds to
// Publish's synchronous path (NumberOfMessagesPublished/PublishSize, recorded
// before fan-out is dispatched to its background goroutine — see
// handler_publish.go) with collection disabled vs enabled over a real
// *metrics.Service backed by MemoryStore.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

func newPublishBenchService(b *testing.B, rec metricsRecorder) (*Service, string) {
	b.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000", Port: 4566}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	svc.handler.metrics = rec

	arn := protocol.TopicARN(cfg.Region, cfg.AccountID, "bench-topic")
	if aerr := svc.handler.snsStore.putTopic(context.Background(), &Topic{Name: "bench-topic", ARN: arn}); aerr != nil {
		b.Fatalf("seed topic: %v", aerr.Message)
	}
	return svc, arn
}

func runPublishBench(b *testing.B, rec metricsRecorder) {
	svc, topicARN := newPublishBenchService(b, rec)
	values := url.Values{}
	values.Set("TopicArn", topicARN)
	values.Set("Message", "benchmark message body")
	encoded := values.Encode()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(encoded))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := req.ParseForm(); err != nil {
			b.Fatalf("parse form: %v", err)
		}
		rr := httptest.NewRecorder()
		svc.handler.Publish(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("Publish status = %d", rr.Code)
		}
	}
	svc.Stop(context.Background())
}

// BenchmarkPublish_MetricsDisabled is the baseline: h.metrics is nil.
func BenchmarkPublish_MetricsDisabled(b *testing.B) {
	runPublishBench(b, nil)
}

// BenchmarkPublish_MetricsEnabled is the same Publish call with a real
// *metrics.Service recording NumberOfMessagesPublished + PublishSize.
func BenchmarkPublish_MetricsEnabled(b *testing.B) {
	svc := metrics.NewRecorder(state.NewMemoryStore(), clock.New(), zap.NewNop())
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	runPublishBench(b, svc)
}
