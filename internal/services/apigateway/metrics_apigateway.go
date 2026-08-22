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
// Both API types record only their most granular documented dimension
// combination — ApiName+Stage+Method+Resource for REST,
// ApiId+Stage+HttpMethod+RouteKey for HTTP — never the coarser
// ApiName+Stage-only (or ApiId+Stage-only) aggregate AWS also documents.
// This mirrors Lambda phase 1's own disclosed narrowing (FunctionName only,
// no Resource/ExecutedVersion): the coarser series are computable by
// aggregating the finer one later if a consumer needs them, whereas the
// reverse is not true, and publishing every documented dimension
// combination per request (as real AWS does internally) is real
// per-request write amplification with no phase-2 consumer.
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
// outcome facts — never internal/services/cloudwatch (plan acceptance
// criteria: "no service imports internal/services/cloudwatch"). Satisfied by
// *metrics.Service.
type metricsRecorder interface {
	Observe(ctx context.Context, o metrics.Observation) error
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

// recordRestAPIOutcome is ExecuteRestAPI's one outcome helper — called
// exactly once per dispatched request, via a defer installed before any
// early return, so every response path (auth rejection, missing
// integration, successful proxy invoke, ...) is counted exactly once.
// apiName/stageName/resourcePath may be "" when the request never resolved
// that far (e.g. an unknown restApiId) — AWS itself never publishes a data
// point it cannot dimension, so an empty apiName skips recording entirely
// rather than inventing a placeholder dimension value.
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
	h.observeAPIGatewayMetric(ctx, "Count", dims, "Count", 1)
	switch {
	case status >= 500:
		h.observeAPIGatewayMetric(ctx, "5XXError", dims, "Count", 1)
	case status >= 400:
		h.observeAPIGatewayMetric(ctx, "4XXError", dims, "Count", 1)
	}
	h.observeAPIGatewayMetric(ctx, "Latency", dims, "Milliseconds", float64(latency.Milliseconds()))
	if integrationLatency > 0 {
		h.observeAPIGatewayMetric(ctx, "IntegrationLatency", dims, "Milliseconds", float64(integrationLatency.Milliseconds()))
	}
}

// recordV2APIOutcome is ExecuteV2API's equivalent outcome helper — same
// shape as recordRestAPIOutcome, but HTTP APIs are dimensioned by ApiId and
// RouteKey/HttpMethod rather than ApiName and Resource (see file doc
// comment / AWS's documented dimension combinations, which genuinely
// differ between REST and HTTP APIs).
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
	h.observeAPIGatewayMetric(ctx, "Count", dims, "Count", 1)
	switch {
	case status >= 500:
		h.observeAPIGatewayMetric(ctx, "5XXError", dims, "Count", 1)
	case status >= 400:
		h.observeAPIGatewayMetric(ctx, "4XXError", dims, "Count", 1)
	}
	h.observeAPIGatewayMetric(ctx, "Latency", dims, "Milliseconds", float64(latency.Milliseconds()))
	if integrationLatency > 0 {
		h.observeAPIGatewayMetric(ctx, "IntegrationLatency", dims, "Milliseconds", float64(integrationLatency.Milliseconds()))
	}
}
