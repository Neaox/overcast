package sns

// handler_metrics.go — GET /_overcast/sns/topics/{topicName}/metrics
//
// The web Monitor tab's read-through into the shared service-metrics
// repository (docs/plans/service-metrics-platform.md phase 4), following
// Lambda/SQS's precedent (internal/services/lambda/handler_metrics.go,
// internal/services/sqs/handler_metrics.go): metrics.BuildMonitorResponse
// (internal/metrics/monitor.go) is the shared allowlist assembly; this file
// only supplies SNS's own catalogue and dimension.
import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/metrics"
)

// snsMonitorCatalog is the fixed, reviewed set of AWS/SNS series and
// statistics the Monitor tab may request — the phase 2 catalogue
// (metrics_sns.go): publish volume/size and delivery outcome counts. Never
// extended by a request parameter.
var snsMonitorCatalog = []metrics.MonitorCatalogEntry{
	{Metric: "NumberOfMessagesPublished", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "PublishSize", Statistics: []string{metrics.StatAverage, metrics.StatMaximum}, Unit: "Bytes"},
	{Metric: "NumberOfNotificationsDelivered", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "NumberOfNotificationsFailed", Statistics: []string{metrics.StatSum}, Unit: "Count"},
}

// GetTopicMetrics handles GET /_overcast/sns/topics/{topicName}/metrics.
// ?range= is one of "1h" (default), "6h", "24h", "7d", "30d" — see
// metrics.ParseChartRange.
func (h *Handler) GetTopicMetrics(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "topicName")
	dims := []metrics.Dimension{{Name: "TopicName", Value: name}}

	resp, status := metrics.BuildMonitorResponse(
		r.Context(), h.metrics, r.URL.Query().Get("range"), h.clk.Now(),
		snsMetricsNamespace, dims, snsMonitorCatalog,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
