package dynamodb

// metrics_dynamodb_bench_test.go sizes the cost service-metrics recording
// adds to PutItem — the "xxxTyped" wrapper shape every one of this phase's
// ten instrumented operations shares — with collection disabled vs enabled
// over a real *metrics.Service backed by MemoryStore.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/metrics"
	"github.com/overcast-sh/overcast/internal/state"
)

func newPutItemBenchService(b *testing.B, rec metricsRecorder) *Service {
	b.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New(), events.NewBus())
	svc.handler.metrics = rec
	if _, aerr := svc.handler.createTableTyped(context.Background(), &createTableRequest{
		TableName:            "bench-table",
		KeySchema:            []KeySchemaElement{{AttributeName: "id", KeyType: "HASH"}},
		AttributeDefinitions: []AttributeDef{{AttributeName: "id", AttributeType: "S"}},
		BillingMode:          "PAY_PER_REQUEST",
	}); aerr != nil {
		b.Fatalf("CreateTable: %v", aerr.Message)
	}
	return svc
}

func runPutItemBench(b *testing.B, rec metricsRecorder) {
	svc := newPutItemBenchService(b, rec)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, aerr := svc.handler.putItemTyped(ctx, &putItemRequest{
			TableName: "bench-table",
			Item:      Item{"id": attrValue{"S": "o1"}, "status": attrValue{"S": "placed"}, "amount": attrValue{"N": "9.99"}},
		})
		if aerr != nil {
			b.Fatalf("PutItem: %v", aerr.Message)
		}
	}
}

// BenchmarkPutItem_MetricsDisabled is the baseline: h.metrics is nil, so
// putItemTyped's wrapper takes its early-return branch before putItemTypedCore.
func BenchmarkPutItem_MetricsDisabled(b *testing.B) {
	runPutItemBench(b, nil)
}

// BenchmarkPutItem_MetricsEnabled is the same PutItem with a real
// *metrics.Service recording SuccessfulRequestLatency + ConsumedWriteCapacityUnits.
func BenchmarkPutItem_MetricsEnabled(b *testing.B) {
	svc := metrics.NewRecorder(state.NewMemoryStore(), clock.New(), zap.NewNop())
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	runPutItemBench(b, svc)
}
