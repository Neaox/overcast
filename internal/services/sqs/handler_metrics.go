package sqs

// handler_metrics.go — GET /_overcast/sqs/queues/{name}/metrics
//
// The web Monitor tab's read-through into the shared service-metrics
// repository (docs/plans/service-metrics-platform.md phase 3), following
// Lambda's precedent (internal/services/lambda/handler_metrics.go):
// metrics.BuildMonitorResponse (internal/metrics/monitor.go) is the shared
// allowlist assembly; this file only supplies SQS's own catalogue and
// dimension.
import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/metrics"
)

// sqsMonitorCatalog is the fixed, reviewed set of AWS/SQS series and
// statistics the Monitor tab may request, mirroring the AWS SQS console's own
// Monitoring tab groupings: sent/received/deleted/empty-receive counts, and
// the queue-depth gauges (Average is the AWS console's own default statistic
// for these — a queue depth gauge's Maximum would overstate a bursty queue
// that spends most of a period near zero). ApproximateAgeOfOldestMessage
// tracks Maximum, matching AWS's own console default for that metric.
var sqsMonitorCatalog = []metrics.MonitorCatalogEntry{
	{Metric: "NumberOfMessagesSent", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "NumberOfMessagesReceived", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "NumberOfMessagesDeleted", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "NumberOfEmptyReceives", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{Metric: "ApproximateNumberOfMessagesVisible", Statistics: []string{metrics.StatAverage}, Unit: "Count"},
	{Metric: "ApproximateNumberOfMessagesNotVisible", Statistics: []string{metrics.StatAverage}, Unit: "Count"},
	{Metric: "ApproximateAgeOfOldestMessage", Statistics: []string{metrics.StatMaximum}, Unit: "Seconds"},
}

// GetQueueMetrics handles GET /_overcast/sqs/queues/{name}/metrics. ?range=
// is one of "1h" (default), "6h", "24h", "7d", "30d" — see
// metrics.ParseChartRange.
func (h *Handler) GetQueueMetrics(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	dims := []metrics.Dimension{{Name: "QueueName", Value: name}}

	resp, status := metrics.BuildMonitorResponse(
		r.Context(), h.metrics, r.URL.Query().Get("range"), h.clk.Now(),
		sqsMetricsNamespace, dims, sqsMonitorCatalog,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
