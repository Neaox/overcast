package lambda

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// fakeMetricsRecorder captures every Observe call for assertion, standing in
// for a real *metrics.Service the same way concurrencyTestHandler's
// countingColdStartRuntime stands in for Docker.
type fakeMetricsRecorder struct {
	mu  sync.Mutex
	obs []metrics.Observation
}

func (f *fakeMetricsRecorder) Observe(_ context.Context, o metrics.Observation) error {
	f.mu.Lock()
	f.obs = append(f.obs, o)
	f.mu.Unlock()
	return nil
}

func (f *fakeMetricsRecorder) byName(name string) []metrics.Observation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]metrics.Observation, 0)
	for _, o := range f.obs {
		if o.Name == name {
			out = append(out, o)
		}
	}
	return out
}

func dimValue(o metrics.Observation, name string) string {
	for _, d := range o.Dimensions {
		if d.Name == name {
			return d.Value
		}
	}
	return ""
}

// scriptedInstance is a RuntimeInstance whose Invoke result and error are
// configured per test. It advances the shared mock clock by invokeDelay
// before returning, so Duration measurement (invokeSyncOnce/invokeAsyncOnce/
// ServiceInvoker.Invoke all wrap h.clk.Now() tightly around inst.Invoke) is
// pinned deterministically rather than racing real wall-clock scheduling.
type scriptedInstance struct {
	functionName string
	instanceID   string
	clk          clock.Clock
	invokeDelay  time.Duration
	result       *InvokeResult
	invokeErr    error
}

func (i *scriptedInstance) Invoke(context.Context, []byte, InvokeOptions) (*InvokeResult, error) {
	if i.invokeDelay > 0 {
		// Only a mock clock can be advanced synchronously; a real clock (used
		// by the benchmarks in invoke_metrics_bench_test.go) just lets actual
		// wall-clock time pass, so invokeDelay is always 0 there.
		if mock, ok := i.clk.(*clock.Mock); ok {
			mock.Add(i.invokeDelay)
		}
	}
	if i.invokeErr != nil {
		return nil, i.invokeErr
	}
	return i.result, nil
}
func (i *scriptedInstance) LogStreamName() string  { return "stream" }
func (i *scriptedInstance) Healthy() bool          { return true }
func (i *scriptedInstance) FunctionName() string   { return i.functionName }
func (i *scriptedInstance) ConfigIdentity() string { return "code" }
func (i *scriptedInstance) ContainerID() string    { return "" }
func (i *scriptedInstance) InstanceID() string     { return i.instanceID }
func (i *scriptedInstance) Close() error           { return nil }

// scriptedRuntime hands out whatever next returns from each Acquire call.
type scriptedRuntime struct {
	next func(fn *Function) (RuntimeInstance, error)
}

func (r *scriptedRuntime) CanHandle(string) bool { return true }
func (r *scriptedRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	return r.next(fn)
}
func (r *scriptedRuntime) Release(context.Context, RuntimeInstance, bool) {}

// metricsTestHandler builds a Handler over a real InstancePool (so
// ConcurrentExecutions sampling exercises the real admit/release admission
// path) backed by a scriptedRuntime, with a fakeMetricsRecorder wired exactly
// the way Service.InitMetrics wires a real one.
func metricsTestHandler(t *testing.T, clk *clock.Mock, mk func(fn *Function) (RuntimeInstance, error)) (*Handler, *fakeMetricsRecorder) {
	t.Helper()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	rt := &scriptedRuntime{next: mk}
	pool := NewInstancePool(rt, zap.NewNop(), clk, PoolLimits{})
	t.Cleanup(pool.Stop)
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)
	pool.observer = tracker

	rec := &fakeMetricsRecorder{}
	h := &Handler{
		cfg:      &config.Config{Region: "us-east-1"},
		log:      serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:      clk,
		ls:       ls,
		tracker:  tracker,
		runtimes: newRuntimeRegistry([]Runtime{pool}),
		metrics:  rec,
	}
	pool.concurrencyObserver = h.recordConcurrency

	fn := &Function{
		Name: "metrics-fn", ARN: "arn:aws:lambda:us-east-1:000000000000:function:metrics-fn",
		Runtime: "nodejs22.x", Handler: "index.handler", Timeout: 3, MemorySize: 128, State: "Active",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function: %s", aerr.Message)
	}
	return h, rec
}

// TestInvokeSync_RecordsInvocationsAndDuration pins the P0 success path:
// Invocations=1 and Duration (matching the scripted delay) are recorded, and
// no Errors/Throttles observation is made.
func TestInvokeSync_RecordsInvocationsAndDuration(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk, invokeDelay: 250 * time.Millisecond,
			result: &InvokeResult{StatusCode: 200, Payload: []byte(`{}`)}}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")

	result := h.invokeSync(context.Background(), fn, h.runtimes.runtimeFor(context.Background(), fn.Runtime), []byte("{}"), fn.Name, InvokeOptions{})
	if result.FunctionError != "" {
		t.Fatalf("unexpected FunctionError: %s", result.FunctionError)
	}

	invocations := rec.byName("Invocations")
	if len(invocations) != 1 {
		t.Fatalf("expected 1 Invocations observation, got %d: %+v", len(invocations), invocations)
	}
	if got := dimValue(invocations[0], "FunctionName"); got != "metrics-fn" {
		t.Errorf("FunctionName dimension = %q, want metrics-fn", got)
	}
	if invocations[0].Value != 1 || invocations[0].Namespace != "AWS/Lambda" || invocations[0].Unit != "Count" {
		t.Errorf("unexpected Invocations observation: %+v", invocations[0])
	}

	if got := rec.byName("Errors"); len(got) != 0 {
		t.Errorf("expected no Errors observation on success, got %+v", got)
	}
	if got := rec.byName("Throttles"); len(got) != 0 {
		t.Errorf("expected no Throttles observation on success, got %+v", got)
	}

	durations := rec.byName("Duration")
	if len(durations) != 1 {
		t.Fatalf("expected 1 Duration observation, got %d", len(durations))
	}
	if durations[0].Unit != "Milliseconds" || durations[0].Value != float64(250*time.Millisecond/time.Millisecond) {
		t.Errorf("Duration observation = %+v, want ~250ms", durations[0])
	}
}

// TestInvokeSync_FunctionErrorRecordsErrors pins that a function error
// records both Invocations and Errors (never Errors alone).
func TestInvokeSync_FunctionErrorRecordsErrors(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk,
			result: &InvokeResult{StatusCode: 200, FunctionError: "Unhandled", Payload: []byte(`{"errorMessage":"boom"}`)}}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")

	result := h.invokeSync(context.Background(), fn, h.runtimes.runtimeFor(context.Background(), fn.Runtime), []byte("{}"), fn.Name, InvokeOptions{})
	if result.FunctionError == "" {
		t.Fatalf("expected a FunctionError")
	}

	if got := rec.byName("Invocations"); len(got) != 1 {
		t.Fatalf("expected 1 Invocations observation even on a function error, got %d", len(got))
	}
	if got := rec.byName("Errors"); len(got) != 1 {
		t.Fatalf("expected 1 Errors observation, got %d", len(got))
	}
}

// TestInvokeSync_ThrottleRecordsThrottlesOnly pins the plan's exclusion rule:
// a rejected (throttled) invocation records Throttles and never Invocations
// or Errors.
func TestInvokeSync_ThrottleRecordsThrottlesOnly(t *testing.T) {
	h, rec := metricsTestHandler(t, clock.NewMock(), func(fn *Function) (RuntimeInstance, error) {
		return nil, errThrottled(reasonReservedConcurrency)
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")
	fn.Timeout = 1 // keep the invokeSync retry loop to a single attempt

	result := h.invokeSync(context.Background(), fn, h.runtimes.runtimeFor(context.Background(), fn.Runtime), []byte("{}"), fn.Name, InvokeOptions{})
	if result.throttle == nil {
		t.Fatalf("expected a throttle result")
	}

	if got := rec.byName("Throttles"); len(got) != 1 {
		t.Fatalf("expected 1 Throttles observation, got %d: %+v", len(got), got)
	}
	if got := rec.byName("Invocations"); len(got) != 0 {
		t.Errorf("expected no Invocations observation for a throttle, got %+v", got)
	}
	if got := rec.byName("Errors"); len(got) != 0 {
		t.Errorf("expected no Errors observation for a throttle, got %+v", got)
	}
}

// TestInvokeSync_ConcurrentExecutionsSampledOnAcquireAndRelease pins that the
// ConcurrentExecutions gauge reflects the pool's real admission count: it
// rises to 1 while an invocation holds its slot and falls back to 0 once
// released — sampled through the exact InstancePool.admit /
// decCheckedOutLocked path a real invocation takes, not a synthetic counter.
func TestInvokeSync_ConcurrentExecutionsSampledOnAcquireAndRelease(t *testing.T) {
	clk := clock.NewMock()
	h, rec := metricsTestHandler(t, clk, func(fn *Function) (RuntimeInstance, error) {
		return &scriptedInstance{functionName: fn.Name, instanceID: "i-1", clk: clk,
			result: &InvokeResult{StatusCode: 200}}, nil
	})
	fn, _ := h.ls.getFunction(context.Background(), "metrics-fn")

	h.invokeSync(context.Background(), fn, h.runtimes.runtimeFor(context.Background(), fn.Runtime), []byte("{}"), fn.Name, InvokeOptions{})

	samples := rec.byName("ConcurrentExecutions")
	if len(samples) < 2 {
		t.Fatalf("expected at least an acquire and a release sample, got %d: %+v", len(samples), samples)
	}
	first, last := samples[0], samples[len(samples)-1]
	if first.Value != 1 {
		t.Errorf("first ConcurrentExecutions sample = %v, want 1 (acquired)", first.Value)
	}
	if last.Value != 0 {
		t.Errorf("last ConcurrentExecutions sample = %v, want 0 (released)", last.Value)
	}
	if first.Unit != "None" {
		t.Errorf("ConcurrentExecutions unit = %q, want None", first.Unit)
	}
}

// TestRecordInvocationOutcome_NilRecorderIsNoOp pins that every call site is
// safe with collection disabled / not yet wired.
func TestRecordInvocationOutcome_NilRecorderIsNoOp(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{Region: "us-east-1"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clock.NewMock(),
	}
	h.recordInvocationOutcome(context.Background(), "fn", h.clk.Now(), 0, false, false)
	h.recordConcurrency("fn", 1)
	// No panic, no observation recorded anywhere — nothing to assert beyond
	// "did not crash", since h.metrics is nil.
}
