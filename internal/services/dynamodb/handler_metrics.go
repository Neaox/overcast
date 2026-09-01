package dynamodb

// handler_metrics.go — GET /_overcast/dynamodb/tables/{name}/metrics
//
// The web Monitor tab's read-through into the shared service-metrics
// repository (docs/plans/service-metrics-platform.md phase 4), following
// Lambda/SQS/SNS's precedent. metrics.BuildMonitorResponse
// (internal/metrics/monitor.go) is the shared allowlist assembly; this file
// only supplies DynamoDB's own catalogue and dimensions.
//
// Deliberately table-scoped, capacity-only. AWS's own SuccessfulRequestLatency,
// UserErrors, and SystemErrors are dimensioned by Operation (SuccessfulRequestLatency/
// SystemErrors) or published with no dimensions at all (UserErrors — see
// metrics_dynamodb.go's file doc comment) — neither fits a single per-table
// series the way ConsumedReadCapacityUnits/ConsumedWriteCapacityUnits do.
// Charting those would mean either one line per operation (a catalogue that
// grows with every operation this emulator implements, unlike every other
// service's fixed small catalogue) or a coarser TableName-only aggregate that
// was never recorded in the first place — phase 2 explicitly records only
// the fully-dimensioned series. Tracked as a follow-up rather than built here
// (see the plan doc's phase 4 disposition); the two capacity metrics below
// are real, correctly-dimensioned per-table facts today.
import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/metrics"
)

// ddbMonitorCatalog is the fixed, reviewed set of AWS/DynamoDB series and
// statistics the Monitor tab may request for one table.
// ConsumedWriteCapacityUnits' ExtraDimensions matches
// recordConsumedWriteCapacity's always-added "Source=Customer" dimension
// exactly — global tables are not modeled, so every write this emulator
// records is a customer write (see metrics_dynamodb.go).
var ddbMonitorCatalog = []metrics.MonitorCatalogEntry{
	{Metric: "ConsumedReadCapacityUnits", Statistics: []string{metrics.StatSum}, Unit: "Count"},
	{
		Metric: "ConsumedWriteCapacityUnits", Statistics: []string{metrics.StatSum}, Unit: "Count",
		ExtraDimensions: []metrics.Dimension{{Name: "Source", Value: "Customer"}},
	},
}

// GetTableMetrics handles GET /_overcast/dynamodb/tables/{name}/metrics.
// ?range= is one of "1h" (default), "6h", "24h", "7d", "30d" — see
// metrics.ParseChartRange.
func (h *Handler) GetTableMetrics(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	dims := []metrics.Dimension{{Name: "TableName", Value: name}}

	resp, status := metrics.BuildMonitorResponse(
		r.Context(), h.metrics, r.URL.Query().Get("range"), h.clk.Now(),
		dynamoDBMetricsNamespace, dims, ddbMonitorCatalog,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
