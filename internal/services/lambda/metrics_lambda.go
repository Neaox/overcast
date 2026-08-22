package lambda

// metrics_lambda.go is Lambda's half of the service-metrics substrate
// (docs/plans/service-metrics-platform.md): the AWS/Lambda P0 metric
// catalogue (Invocations, Errors, Duration, Throttles, ConcurrentExecutions),
// recorded at the one authoritative outcome boundary every invocation
// mechanism already funnels through.
//
// recordInvocationOutcome is that boundary's shared helper — called from
// invokeSync (sync HTTP, function URLs, InvokeWithResponseStream, and the SSE
// invoke path all share invokeSync), invokeAsyncOnce (Event/async
// invocations, including ones triggered in-process via
// ServiceInvoker.InvokeEvent → startAsync → invokeAsync), and
// ServiceInvoker.Invoke (the synchronous in-process path event-source
// mappings use). No invocation path calls metrics.Recorder.Observe directly;
// they all go through this one function, which is what keeps the AWS metric
// definitions (namespace, dimensions, units, the throttle-exclusion rule)
// defined in exactly one place.
//
// DryRun never reaches any of the three call sites above (InvokeFunction
// returns before either is called for InvocationType=DryRun), so it correctly
// never emits an invocation metric.
import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/metrics"
)

// metricsRecorder is the narrow interface Lambda depends on to record
// invocation-outcome facts and to read them back for the web Monitor tab's
// BFF endpoint (handler_metrics.go) — never internal/services/cloudwatch
// (plan acceptance criteria). Satisfied by *metrics.Service.
type metricsRecorder interface {
	Observe(ctx context.Context, o metrics.Observation) error
	ChartQuery(ctx context.Context, namespace, name, statistic string, dims []metrics.Dimension, start, end time.Time, period time.Duration) ([]metrics.ChartPoint, int, error)
}

const lambdaMetricsNamespace = "AWS/Lambda"

// recordInvocationOutcome is Lambda's one recordInvocationOutcome-style
// helper (service-metrics-platform.md's "Architectural decision"). It builds
// the AWS/Lambda P0 series from a single InvocationOutcome-shaped call:
//
//   - throttled: records Throttles=1 only. AWS never counts a throttled
//     invocation as an Invocation or an Error, and Duration has no meaning
//     for an invocation that never reached function code.
//   - otherwise: records Invocations=1, plus Errors=1 when hadError, plus
//     Duration (milliseconds spent inside the runtime's Invoke round trip,
//     which starts after cold-start/init is complete — see invokeSyncOnce's
//     and invokeAsyncOnce's duration measurement, wrapped tightly around
//     inst.Invoke).
//
// Region/qualifier-level dimensions (Resource, ExecutedVersion) are
// deliberately not recorded yet — the plan calls for adding them "only after
// documentation/tests establish the exact combinations"; phase 1 records only
// the base FunctionName-dimensioned series real CloudWatch always publishes.
//
// A nil recorder (collection disabled, or a unit test that never called
// InitMetrics) makes this a no-op — every call site can invoke it
// unconditionally.
func (h *Handler) recordInvocationOutcome(ctx context.Context, functionName string, startedAt time.Time, duration time.Duration, throttled, hadError bool) {
	if h.metrics == nil {
		return
	}
	dims := []metrics.Dimension{{Name: "FunctionName", Value: functionName}}

	if throttled {
		h.observeLambdaMetric(ctx, "Throttles", dims, startedAt, "Count", 1)
		return
	}
	h.observeLambdaMetric(ctx, "Invocations", dims, startedAt, "Count", 1)
	if hadError {
		h.observeLambdaMetric(ctx, "Errors", dims, startedAt, "Count", 1)
	}
	h.observeLambdaMetric(ctx, "Duration", dims, startedAt, "Milliseconds", float64(duration.Milliseconds()))
}

// observeLambdaMetric records one AWS/Lambda observation, logging (never
// failing the invocation) on error — a metrics-subsystem hiccup must not
// turn a successful Lambda invocation into a caller-visible failure.
func (h *Handler) observeLambdaMetric(ctx context.Context, name string, dims []metrics.Dimension, ts time.Time, unit string, value float64) {
	if err := h.metrics.Observe(ctx, metrics.Observation{
		Namespace: lambdaMetricsNamespace, Name: name, Dimensions: dims,
		Timestamp: ts, Unit: unit, Value: value,
	}); err != nil {
		h.log.Debug("lambda: metrics observe failed", zap.String("metric", name), zap.Error(err))
	}
}

// recordConcurrency observes a ConcurrentExecutions sample for functionName
// at the moment InstancePool's real-invocation admission count changed —
// wired as InstancePool.concurrencyObserver (see runtime_pool.go /
// runtime_pool_admission.go). Deliberately not called for provisioned
// concurrency prewarm or ProactiveInit's own bookkeeping — see
// concurrencyObserver's doc comment on InstancePool.
func (h *Handler) recordConcurrency(functionName string, current int) {
	if h.metrics == nil {
		return
	}
	h.observeLambdaMetric(context.Background(), "ConcurrentExecutions",
		[]metrics.Dimension{{Name: "FunctionName", Value: functionName}},
		h.clk.Now(), "None", float64(current))
}
