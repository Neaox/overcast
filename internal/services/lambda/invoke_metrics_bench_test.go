package lambda

// invoke_metrics_bench_test.go sizes the cost service-metrics recording adds
// to a Lambda invocation at the exact seam it happens
// (recordInvocationOutcome, called from invokeSync's defer) —
// docs/plans/benchmark-the-shape-that-exposes-the-cost.md's rule that a
// benchmark must be shaped like the real call it is protecting, not a
// synthetic shortcut. invokeSync is exercised end-to-end (tracker,
// InstancePool admission/release, a scripted zero-delay Invoke) with metrics
// collection disabled vs enabled over a real *metrics.Service backed by
// MemoryStore, so the reported delta is what a real invocation actually pays
// — not just the isolated Observe cost recorder_bench_test.go measures.

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func newInvokeBenchHandler(b *testing.B, rec metricsRecorder) (*Handler, *Function) {
	b.Helper()
	clk := clock.New()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	rt := &scriptedRuntime{next: func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-bench", clk: clk,
			result: &InvokeResult{StatusCode: 200, Payload: []byte(`{}`)}}, nil
	}}
	pool := NewInstancePool(rt, zap.NewNop(), clk, PoolLimits{})
	b.Cleanup(pool.Stop)
	tracker := newInstanceTracker(clk, zap.NewNop())
	b.Cleanup(tracker.Stop)
	pool.observer = tracker

	h := &Handler{
		cfg:      &config.Config{Region: "us-east-1"},
		log:      serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:      clk,
		ls:       ls,
		tracker:  tracker,
		runtimes: newRuntimeRegistry([]Runtime{pool}),
		metrics:  rec,
	}
	if rec != nil {
		pool.concurrencyObserver = h.recordConcurrency
	}
	fn := &Function{
		Name: "bench-fn", ARN: "arn:aws:lambda:us-east-1:000000000000:function:bench-fn",
		Runtime: "nodejs22.x", Handler: "index.handler", Timeout: 3, MemorySize: 128, State: "Active",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		b.Fatalf("seed function: %s", aerr.Message)
	}
	return h, fn
}

func runInvokeBench(b *testing.B, rec metricsRecorder) {
	h, fn := newInvokeBenchHandler(b, rec)
	rt := h.runtimes.runtimeFor(context.Background(), fn.Runtime)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := h.invokeSync(ctx, fn, rt, []byte("{}"), fn.Name, InvokeOptions{})
		if result.FunctionError != "" {
			b.Fatalf("unexpected FunctionError: %s", result.FunctionError)
		}
	}
}

// BenchmarkInvokeSync_MetricsDisabled is the baseline: h.metrics is nil, so
// recordInvocationOutcome's every call is the single predictable
// `if h.metrics == nil { return }` branch the plan's performance model
// requires ("no outcome construction, map lookup, allocation, event,
// goroutine, query, or storage work").
func BenchmarkInvokeSync_MetricsDisabled(b *testing.B) {
	runInvokeBench(b, nil)
}

// BenchmarkInvokeSync_MetricsEnabled is the same invocation with a real
// *metrics.Service recording Invocations/Duration/ConcurrentExecutions (no
// Errors — this scripted invocation always succeeds) on MemoryStore.
func BenchmarkInvokeSync_MetricsEnabled(b *testing.B) {
	svc := metrics.NewRecorder(state.NewMemoryStore(), clock.New(), zap.NewNop())
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	runInvokeBench(b, svc)
}
