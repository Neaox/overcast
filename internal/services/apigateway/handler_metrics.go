package apigateway

// handler_metrics.go —
//   GET /_overcast/apigateway/restapis/{apiId}/metrics (REST v1)
//   GET /_overcast/apigateway/apis/{apiId}/metrics     (HTTP v2)
//
// The web Monitor tab's read-through into the shared service-metrics
// repository (docs/plans/service-metrics-platform.md phase 4, #1307),
// following Lambda/SQS/SNS/DynamoDB's precedent:
// metrics.BuildMonitorResponse (internal/metrics/monitor.go) is the shared
// allowlist assembly; this file only supplies API Gateway's own catalogues
// and dimensions.
//
// Both endpoints resolve the path {apiId} against the store first and
// answer the service's own NotFoundException shape (errRestAPINotFound /
// errV2APINotFound, store.go) on an unknown id, matching every other
// apigateway handler's unknown-resource convention (see GetRestApi/GetV2Api,
// handler_rest.go/handler_http.go) — unlike Lambda/SNS/SQS/DynamoDB's
// Monitor endpoints, which never validate the resource exists and instead
// let an unused name/table simply answer an empty series. API Gateway
// resolves the store lookup anyway (REST needs it to turn {apiId} into the
// ApiName dimension AWS actually meters by), so reusing it for a 404 is
// free and keeps GetRestApiMetrics/GetApiMetrics consistent with every
// other GET on the same resource.
//
// Coarse-series recording (metrics_apigateway.go's recordRestAPIOutcome/
// recordV2APIOutcome) means both endpoints can serve a per-API view
// (dimensioned by ApiName/ApiId alone) and, with ?stage=, a per-stage view
// (+Stage) — the same recorded facts a per-route dashboard would use, just
// queried at a coarser dimension set. Neither endpoint exposes a per-method/
// per-route breakdown: that would need a catalogue keyed by the API's own
// resource/route count, unlike every other service's small fixed catalogue
// (see the plan doc's phase 4 "API Gateway Monitor tab" entry).
import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/protocol"
)

// restAPIMonitorCatalog is the fixed, reviewed set of AWS/ApiGateway REST
// series and statistics the Monitor tab may request — Latency/
// IntegrationLatency use Average+Maximum, matching Lambda's own Duration
// entry (internal/services/lambda/handler_metrics.go). Never extended by a
// request parameter.
var restAPIMonitorCatalog = []metrics.MonitorCatalogEntry{
	{Metric: "Count", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "4XXError", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "5XXError", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "Latency", Statistics: []string{metrics.StatAverage, metrics.StatMaximum}, Unit: "Milliseconds"},
	{Metric: "IntegrationLatency", Statistics: []string{metrics.StatAverage, metrics.StatMaximum}, Unit: "Milliseconds"},
}

// v2APIMonitorCatalog is restAPIMonitorCatalog's HTTP (v2) equivalent —
// identical shape, but AWS names the HTTP API error metrics "4xx"/"5xx"
// (lowercase, no "Error" suffix), which recordV2APIOutcome now records
// (metrics_apigateway.go, #1307).
var v2APIMonitorCatalog = []metrics.MonitorCatalogEntry{
	{Metric: "Count", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "4xx", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "5xx", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "Latency", Statistics: []string{metrics.StatAverage, metrics.StatMaximum}, Unit: "Milliseconds"},
	{Metric: "IntegrationLatency", Statistics: []string{metrics.StatAverage, metrics.StatMaximum}, Unit: "Milliseconds"},
}

// GetRestApiMetrics handles GET /_overcast/apigateway/restapis/{apiId}/metrics.
// ?range= is one of "1h" (default), "6h", "24h", "7d", "30d" — see
// metrics.ParseChartRange. ?stage=<name> narrows the queried series from the
// API-wide {ApiName} aggregate to the {ApiName, Stage} one — both are
// recorded per request (recordRestAPIOutcome), so either dimension set has
// real data to answer with.
func (h *Handler) GetRestApiMetrics(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	api, aerr := h.store.getRestAPI(r.Context(), apiID)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	dims := []metrics.Dimension{{Name: "ApiName", Value: api.Name}}
	if stage := r.URL.Query().Get("stage"); stage != "" {
		dims = append(dims, metrics.Dimension{Name: "Stage", Value: stage})
	}

	resp, status := metrics.BuildMonitorResponse(
		r.Context(), h.metrics, r.URL.Query().Get("range"), h.clk.Now(),
		apiGatewayMetricsNamespace, dims, restAPIMonitorCatalog,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// GetApiMetrics handles GET /_overcast/apigateway/apis/{apiId}/metrics — the
// HTTP (v2) equivalent of GetRestApiMetrics. HTTP APIs are metered by ApiId
// directly (never a separate name dimension), so unlike the REST endpoint
// the store lookup exists only to answer the same 404-on-unknown-id
// convention every other apigateway GET uses; the dimension itself is the
// path parameter.
func (h *Handler) GetApiMetrics(w http.ResponseWriter, r *http.Request) {
	apiID := chi.URLParam(r, "apiId")
	if _, aerr := h.store.getV2API(r.Context(), apiID); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	dims := []metrics.Dimension{{Name: "ApiId", Value: apiID}}
	if stage := r.URL.Query().Get("stage"); stage != "" {
		dims = append(dims, metrics.Dimension{Name: "Stage", Value: stage})
	}

	resp, status := metrics.BuildMonitorResponse(
		r.Context(), h.metrics, r.URL.Query().Get("range"), h.clk.Now(),
		apiGatewayMetricsNamespace, dims, v2APIMonitorCatalog,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
