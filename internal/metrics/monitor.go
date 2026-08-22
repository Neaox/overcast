package metrics

// monitor.go is the shared shape and assembly logic behind every service's
// web Monitor tab BFF endpoint (docs/plans/service-metrics-platform.md "Web
// UI plan": "Add a BFF-owned /_metrics read endpoint... that calls the shared
// repository with a fixed allowlist of Lambda metric definitions. It accepts
// resource, relative time range, period, and statistic — not arbitrary
// namespace/metric expressions."). Each service (internal/services/lambda,
// internal/services/sqs, ...) owns its own fixed MonitorCatalogEntry list and
// registers its own GET /_overcast/<service>/.../metrics route, but they all
// build their JSON response through BuildMonitorResponse so the shape, range
// parsing, and disabled/error handling live in exactly one place — the
// backend analog of the plan's "Build one reusable MetricChart/MetricCard
// component" instruction for the frontend.
import (
	"context"
	"net/http"
	"time"
)

// MonitorCatalogEntry is one metric name plus the CloudWatch statistics the
// Monitor tab may request for it and the AWS unit for display — the fixed
// allowlist a service's Monitor endpoint serves. Never extended by a request
// parameter: a caller picks a resource and a range, nothing else.
type MonitorCatalogEntry struct {
	Metric     string
	Statistics []string
	Unit       string

	// ExtraDimensions are appended to the per-request base dimension set
	// (e.g. TableName) for this entry only, before querying. Most services'
	// catalogues leave this nil — Lambda/SQS/SNS record every metric with
	// exactly the same dimension set, so BuildMonitorResponse's single base
	// dims slice already matches every series. DynamoDB is the first
	// exception: ConsumedWriteCapacityUnits always adds a "Source=Customer"
	// dimension on top of TableName (metrics_dynamodb.go's
	// recordConsumedWriteCapacity), so its own series identity has one more
	// dimension than ConsumedReadCapacityUnits' — a fixed per-entry addition
	// here, not a request parameter, keeps that a catalogue fact rather than
	// something a caller could vary.
	ExtraDimensions []Dimension
}

// MonitorSeries is one requested (metric, statistic) series' rendered points.
type MonitorSeries struct {
	Metric    string       `json:"metric"`
	Statistic string       `json:"statistic"`
	Unit      string       `json:"unit"`
	Points    []ChartPoint `json:"points"`
}

// MonitorResponse is the Monitor tab's per-resource BFF response shape.
type MonitorResponse struct {
	// Enabled is false when service-metric collection is off
	// (OVERCAST_SERVICE_METRICS=disabled, or CloudWatch disabled under
	// auto) — the Monitor tab shows a "collection is disabled" state, never
	// an empty chart pretending nothing has ever happened.
	Enabled bool `json:"enabled"`
	// Range echoes the requested range token so the client can confirm what
	// it got back matches what it asked for.
	Range string `json:"range"`
	// PeriodSeconds is the display period the response's points are aligned
	// to — a multiple of ResolutionSeconds.
	PeriodSeconds int `json:"periodSeconds,omitempty"`
	// ResolutionSeconds is the stored tier that actually answered the
	// request (SelectResolution's result) — surfaced so the UI can disclose
	// e.g. "showing hourly data" for an old range.
	ResolutionSeconds int             `json:"resolutionSeconds,omitempty"`
	Series            []MonitorSeries `json:"series"`
	// Error is set only alongside a non-2xx status — an invalid range token.
	Error string `json:"error,omitempty"`
}

// ChartReader is the narrow read surface BuildMonitorResponse needs. A
// service's own metricsRecorder-shaped field (already widened to embed
// ChartQuery) satisfies it structurally; nil (collection disabled, or a unit
// test that never wired a recorder) is handled explicitly below rather than
// panicking.
type ChartReader interface {
	ChartQuery(ctx context.Context, namespace, name, statistic string, dims []Dimension, start, end time.Time, period time.Duration) ([]ChartPoint, int, error)
}

// BuildMonitorResponse answers one resource's Monitor tab request. rangeToken
// defaults to "1h" when empty (the plan's Web UI section: "Use a one-hour
// default"). A nil reader answers {"enabled": false} — never an error — so
// the Monitor tab can render its disabled state instead of a fetch failure.
// One series' ChartQuery failure is silently omitted from Series (the
// concrete *Service never actually returns one — a storage read failure
// degrades to "no data" inside QueryAuto's own callees — but a defensive
// implementation must not let one bad series 500 the whole card grid).
func BuildMonitorResponse(ctx context.Context, reader ChartReader, rangeToken string, now time.Time, namespace string, dims []Dimension, catalog []MonitorCatalogEntry) (MonitorResponse, int) {
	if rangeToken == "" {
		rangeToken = "1h"
	}
	if reader == nil {
		return MonitorResponse{Enabled: false, Range: rangeToken, Series: []MonitorSeries{}}, http.StatusOK
	}

	start, end, period, ok := ParseChartRange(rangeToken, now)
	if !ok {
		return MonitorResponse{
			Enabled: true, Range: rangeToken, Series: []MonitorSeries{},
			Error: "invalid range: must be one of 1h, 6h, 24h, 7d, 30d",
		}, http.StatusBadRequest
	}

	resp := MonitorResponse{Enabled: true, Range: rangeToken, PeriodSeconds: int(period.Seconds()), Series: []MonitorSeries{}}
	for _, entry := range catalog {
		entryDims := dims
		if len(entry.ExtraDimensions) > 0 {
			entryDims = make([]Dimension, 0, len(dims)+len(entry.ExtraDimensions))
			entryDims = append(entryDims, dims...)
			entryDims = append(entryDims, entry.ExtraDimensions...)
		}
		for _, stat := range entry.Statistics {
			points, resSec, err := reader.ChartQuery(ctx, namespace, entry.Metric, stat, entryDims, start, end, period)
			if err != nil {
				continue
			}
			resp.ResolutionSeconds = resSec
			resp.Series = append(resp.Series, MonitorSeries{Metric: entry.Metric, Statistic: stat, Unit: entry.Unit, Points: points})
		}
	}
	return resp, http.StatusOK
}
