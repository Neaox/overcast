package apigateway

// metrics_apigateway.go is API Gateway's half of the service-metrics
// substrate (docs/plans/service-metrics-platform.md, phase 2). REST (v1) and
// HTTP (v2) APIs do NOT share one execution seam — ExecuteRestAPI and
// ExecuteV2API are two independent top-level dispatchers with different
// per-integration-type sub-functions and different CloudWatch dimension
// sets — so this file has two outcome helpers, one per API type, each
// installed once at the top of its own dispatcher (mirroring Lambda's single
// recordInvocationOutcome, just doubled because API Gateway genuinely has
// two execution boundaries instead of one).
//
// AWS reference: https://docs.aws.amazon.com/apigateway/latest/developerguide/apigateway-metrics-and-dimensions.html
//
// Both API types now record every documented dimension combination per
// request (#1307, "API Gateway Monitor tab"): {ApiName}, {ApiName, Stage},
// and {ApiName, Stage, Method, Resource} for REST; {ApiId}, {ApiId, Stage},
// and {ApiId, Stage, HttpMethod, RouteKey} for HTTP. This supersedes phase
// 2's disclosed narrowing to only the most granular combination — the
// Monitor tab's per-API and per-stage views (phase 4) need the coarser
// aggregate series to exist as their own recorded facts, not something the
// query layer derives by summing across the finer series, so every service's
// Monitor endpoint (metrics.BuildMonitorResponse, internal/metrics/monitor.go)
// can keep querying exactly one dimension set per requested series. Real AWS
// gates the detailed per-method/per-route combination behind a stage's
// metricsEnabled (REST)/detailedMetricsEnabled (HTTP) setting; this emulator
// deliberately does not model that toggle and always records the detailed
// combination, since nothing here disables per-request metrics collection
// short of OVERCAST_SERVICE_METRICS.
//
// Neither dispatcher had an existing per-request timer or status-capturing
// response writer before this file (confirmed absent — see the plan's
// research notes); both are added here as the minimum machinery the metric
// needs, not a general request-logging feature.
import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/protocol"
)

// metricsRecorder is the narrow interface API Gateway depends on to record
// outcome facts and to read them back for the web Monitor tab's BFF
// endpoints (handler_metrics.go, #1307) — never internal/services/cloudwatch
// (plan acceptance criteria: "no service imports internal/services/cloudwatch").
// Satisfied by *metrics.Service.
type metricsRecorder interface {
	Observe(ctx context.Context, o metrics.Observation) error
	ChartQuery(ctx context.Context, namespace, name, statistic string, dims []metrics.Dimension, start, end time.Time, period time.Duration) ([]metrics.ChartPoint, int, error)
}

const apiGatewayMetricsNamespace = "AWS/ApiGateway"

// statusCapturingResponseWriter records the first status code written to it,
// defaulting to 200 (http.ResponseWriter's own default when a handler never
// calls WriteHeader before its first Write) so a handler that only calls
// Write still reports the right Count/4XXError/5XXError classification.
type statusCapturingResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusCapturingResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCapturingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets middleware (chi, http.ResponseController) reach the underlying
// writer — same contract as internal/services/dynamodb's crc32ResponseWriter.
func (w *statusCapturingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// RecordAWSError forwards to the wrapped writer, exactly like
// internal/services/dynamodb's crc32ResponseWriter.RecordAWSError. Without
// this, an AWS error written through writeGatewayError/protocol writers
// behind this wrapper (e.g. the 4xx/5xx paths ExecuteRestAPI/ExecuteV2API
// dispatch to) would never reach the trace, the request log, or the
// retention rule that keeps a trace carrying an AWS error — see #964, the
// same bug class dynamodb's own RecordAWSError comment documents.
func (w *statusCapturingResponseWriter) RecordAWSError(aerr *protocol.AWSError) {
	if rec, ok := w.ResponseWriter.(interface {
		RecordAWSError(*protocol.AWSError)
	}); ok {
		rec.RecordAWSError(aerr)
	}
}

func (h *Handler) observeAPIGatewayMetric(ctx context.Context, name string, dims []metrics.Dimension, unit string, value float64) {
	if h.metrics == nil {
		return
	}
	if err := h.metrics.Observe(ctx, metrics.Observation{
		Namespace:  apiGatewayMetricsNamespace,
		Name:       name,
		Dimensions: dims,
		Timestamp:  h.clk.Now(),
		Unit:       unit,
		Value:      value,
	}); err != nil {
		h.log.Debug("apigateway: metrics observe failed", zap.String("metric", name), zap.Error(err))
	}
}

// emitAPIGatewayMetricSet writes the full Count/error/Latency/IntegrationLatency
// set for one already-built dimension set — the part recordRestAPIOutcome and
// recordV2APIOutcome triplicate across their three dimension combinations
// (file doc comment). errorMetric4xx/errorMetric5xx let the two callers use
// their own AWS-documented error metric names (REST: 4XXError/5XXError;
// HTTP: 4xx/5xx — see recordV2APIOutcome's own comment) without duplicating
// the status-to-metric-name branch.
func (h *Handler) emitAPIGatewayMetricSet(ctx context.Context, dims []metrics.Dimension, status int, latency, integrationLatency time.Duration, errorMetric4xx, errorMetric5xx string) {
	h.observeAPIGatewayMetric(ctx, "Count", dims, "Count", 1)
	switch {
	case status >= 500:
		h.observeAPIGatewayMetric(ctx, errorMetric5xx, dims, "Count", 1)
	case status >= 400:
		h.observeAPIGatewayMetric(ctx, errorMetric4xx, dims, "Count", 1)
	}
	h.observeAPIGatewayMetric(ctx, "Latency", dims, "Milliseconds", float64(latency.Milliseconds()))
	if integrationLatency > 0 {
		h.observeAPIGatewayMetric(ctx, "IntegrationLatency", dims, "Milliseconds", float64(integrationLatency.Milliseconds()))
	}
}

// recordRestAPIOutcome is ExecuteRestAPI's one outcome helper — called
// exactly once per dispatched request, via a defer installed before any
// early return, so every response path (auth rejection, missing
// integration, successful proxy invoke, ...) is counted exactly once.
// apiName/stageName/resourcePath may be "" when the request never resolved
// that far (e.g. an unknown restApiId) — AWS itself never publishes a data
// point it cannot dimension, so an empty apiName skips recording entirely
// rather than inventing a placeholder dimension value.
//
// dims is built once, ordered ApiName, Stage, Method, Resource, and then
// sliced by prefix into the three AWS-documented REST combinations
// ({ApiName}, {ApiName, Stage}, {ApiName, Stage, Method, Resource}) — a
// prefix slice shares dims' backing array rather than allocating, and
// metrics.Service.Observe copies into its own canonical slice before
// storing (canonicalizeDimensions, internal/metrics/series.go), so the
// three combos never alias each other's stored identity.
func (h *Handler) recordRestAPIOutcome(ctx context.Context, apiName, stageName, resourcePath, method string, status int, latency, integrationLatency time.Duration) {
	if h.metrics == nil || apiName == "" {
		return
	}
	dims := []metrics.Dimension{
		{Name: "ApiName", Value: apiName},
		{Name: "Stage", Value: stageName},
		{Name: "Method", Value: method},
		{Name: "Resource", Value: resourcePath},
	}
	for _, combo := range [3][]metrics.Dimension{dims[:1], dims[:2], dims} {
		h.emitAPIGatewayMetricSet(ctx, combo, status, latency, integrationLatency, "4XXError", "5XXError")
	}
}

// recordV2APIOutcome is ExecuteV2API's equivalent outcome helper — same
// shape as recordRestAPIOutcome, but HTTP APIs are dimensioned by ApiId and
// RouteKey/HttpMethod rather than ApiName and Resource (see file doc
// comment / AWS's documented dimension combinations, which genuinely
// differ between REST and HTTP APIs), and use AWS's HTTP API error metric
// names — "4xx"/"5xx", lowercase and without the "Error" suffix REST uses.
// The repo previously recorded these as "4XXError"/"5XXError" for HTTP APIs
// too, which was a fidelity bug (#1307): CloudWatch's own AWS/ApiGateway
// HTTP API metrics are genuinely named "4xx"/"5xx".
func (h *Handler) recordV2APIOutcome(ctx context.Context, apiID, stageName, routeKey, httpMethod string, status int, latency, integrationLatency time.Duration) {
	if h.metrics == nil || apiID == "" {
		return
	}
	dims := []metrics.Dimension{
		{Name: "ApiId", Value: apiID},
		{Name: "Stage", Value: stageName},
		{Name: "HttpMethod", Value: httpMethod},
		{Name: "RouteKey", Value: routeKey},
	}
	for _, combo := range [3][]metrics.Dimension{dims[:1], dims[:2], dims} {
		h.emitAPIGatewayMetricSet(ctx, combo, status, latency, integrationLatency, "4xx", "5xx")
	}
}
