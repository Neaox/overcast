package apigateway

// metrics_apigateway_bench_test.go sizes the cost service-metrics recording
// adds to ExecuteRestAPI — the REST v1 execution seam — with collection
// disabled vs enabled over a real *metrics.Service backed by MemoryStore.
// Disabled skips the statusCapturingResponseWriter allocation and every
// clock read entirely (see ExecuteRestAPI's h.metrics nil guard); enabled
// pays for that wrapper plus Count/Latency/IntegrationLatency recording.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/metrics"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

func newExecuteRestBenchHandler(b *testing.B, rec metricsRecorder) *Handler {
	b.Helper()
	h := newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		clock.New(),
	)
	h.metrics = rec
	h.invoker = &statusControlledInvoker{status: 200}
	if aerr := h.store.putRestAPI(context.Background(), &RestAPI{ID: "bench-api", Name: "bench-api"}); aerr != nil {
		b.Fatalf("putRestAPI: %v", aerr)
	}
	res := &Resource{
		ID:   "res-pets",
		Path: "/pets",
		ResourceMethods: map[string]*Method{
			http.MethodGet: {
				HTTPMethod:        http.MethodGet,
				AuthorizationType: "NONE",
				MethodIntegration: &Integration{Type: "AWS_PROXY", URI: lambdaIntegrationURI("bench-fn")},
			},
		},
	}
	if aerr := h.store.putResource(context.Background(), "bench-api", res); aerr != nil {
		b.Fatalf("putResource: %v", aerr)
	}
	return h
}

func runExecuteRestBench(b *testing.B, rec metricsRecorder) {
	h := newExecuteRestBenchHandler(b, rec)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		req := execRequest(http.MethodGet, "/restapis/bench-api/dev/_user_request_/pets", map[string]string{
			"restApiId": "bench-api",
			"stageName": "dev",
			"*":         "pets",
		})
		h.ExecuteRestAPI(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("status = %d", rr.Code)
		}
	}
}

// BenchmarkExecuteRestAPI_MetricsDisabled is the baseline: h.metrics is nil.
func BenchmarkExecuteRestAPI_MetricsDisabled(b *testing.B) {
	runExecuteRestBench(b, nil)
}

// BenchmarkExecuteRestAPI_MetricsEnabled is the same request with a real
// *metrics.Service recording Count/Latency/IntegrationLatency.
func BenchmarkExecuteRestAPI_MetricsEnabled(b *testing.B) {
	svc := metrics.NewRecorder(state.NewMemoryStore(), clock.New(), zap.NewNop())
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	runExecuteRestBench(b, svc)
}
