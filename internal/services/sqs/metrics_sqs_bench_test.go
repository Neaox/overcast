package sqs

// metrics_sqs_bench_test.go sizes the cost service-metrics recording adds to
// SendMessage — the hot path every metric call site sits on — with
// collection disabled vs enabled over a real *metrics.Service backed by
// MemoryStore, matching send_bench_test.go's own shape (a single-queue,
// zero-delay send) so the reported delta isolates the metrics term rather
// than storage-backend cost.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/metrics"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

func newSendBenchHandler(b *testing.B, rec metricsRecorder) *Handler {
	b.Helper()
	clk := clock.New()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	log := serviceutil.NewServiceLogger(zap.NewNop(), serviceName)
	h := newHandler(cfg, state.NewMemoryStore(), log, clk)
	h.bus = events.NewBus()
	h.metrics = rec
	if _, aerr := h.createQueueTyped(context.Background(), &createQueueRequest{QueueName: "bench-queue"}); aerr != nil {
		b.Fatalf("CreateQueue: %v", aerr)
	}
	return h
}

func runSendBench(b *testing.B, rec metricsRecorder) {
	h := newSendBenchHandler(b, rec)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, aerr := h.sendMessageTyped(ctx, &sendMessageRequest{QueueUrl: "bench-queue", MessageBody: "benchmark message body"}); aerr != nil {
			b.Fatalf("SendMessage: %v", aerr)
		}
	}
}

// BenchmarkSendMessage_MetricsDisabled is the baseline: h.metrics is nil, so
// every observeSQSMetric call is the single predictable
// `if h.metrics == nil { return }` branch.
func BenchmarkSendMessage_MetricsDisabled(b *testing.B) {
	runSendBench(b, nil)
}

// BenchmarkSendMessage_MetricsEnabled is the same send with a real
// *metrics.Service recording NumberOfMessagesSent + SentMessageSize on
// MemoryStore.
func BenchmarkSendMessage_MetricsEnabled(b *testing.B) {
	svc := metrics.NewRecorder(state.NewMemoryStore(), clock.New(), zap.NewNop())
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	runSendBench(b, svc)
}
