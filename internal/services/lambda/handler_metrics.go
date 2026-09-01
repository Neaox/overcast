package lambda

// handler_metrics.go — GET /_overcast/lambda/functions/{name}/metrics
//
// The web Monitor tab's read-through into the shared service-metrics
// repository (docs/plans/service-metrics-platform.md "Web UI plan" / Phase
// 3): "Add a BFF-owned /_metrics read endpoint... that calls the shared
// repository with a fixed allowlist of Lambda metric definitions. It accepts
// resource, relative time range, period, and statistic — not arbitrary
// namespace/metric expressions." metrics.BuildMonitorResponse (internal/metrics/monitor.go)
// is that allowlist assembly, shared with every other service's Monitor
// endpoint; this file only supplies Lambda's own catalogue and dimension.
// The internal/bff proxy in front of this route never speaks CloudWatch
// protocol — it only forwards the "name"/"range" the SPA already has.
import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/metrics"
)

// lambdaMonitorCatalog is the fixed, reviewed set of AWS/Lambda series and
// statistics the Monitor tab may request — the plan's P0 pilot catalogue
// (docs/plans/service-metrics-platform.md "Pilot catalogue and delivery
// order"). Never extended by a request parameter.
var lambdaMonitorCatalog = []metrics.MonitorCatalogEntry{
	{Metric: "Invocations", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "Errors", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "Duration", Statistics: []string{metrics.StatAverage, metrics.StatMaximum}, Unit: "Milliseconds"},
	{Metric: "Throttles", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "ConcurrentExecutions", Statistics: []string{metrics.StatMaximum}, Unit: "None"},
}

// GetFunctionMetrics handles GET /_overcast/lambda/functions/{name}/metrics.
// ?range= is one of "1h" (default), "6h", "24h", "7d", "30d" — see
// metrics.ParseChartRange.
func (h *Handler) GetFunctionMetrics(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	dims := []metrics.Dimension{{Name: "FunctionName", Value: name}}

	resp, status := metrics.BuildMonitorResponse(
		r.Context(), h.metrics, r.URL.Query().Get("range"), h.clk.Now(),
		lambdaMetricsNamespace, dims, lambdaMonitorCatalog,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
