package metrics

// chart.go is the narrow read shape the web Monitor tab's BFF endpoint
// consumes (docs/plans/service-metrics-platform.md "Web UI plan": "a fixed
// allowlist of Lambda metric definitions... accepts resource, relative time
// range, period, and statistic"). It sits directly on top of QueryAuto/the
// planner — no second query engine, no new persistence — and only adds the
// CloudWatch statistic-to-value derivation every per-service BFF handler
// would otherwise duplicate.
import (
	"context"
	"time"
)

// CloudWatch statistic names this package can answer from a Bucket's
// aggregate fields (count/sum/min/max) — see Bucket.Value.
const (
	StatSum         = "Sum"
	StatAverage     = "Average"
	StatMinimum     = "Minimum"
	StatMaximum     = "Maximum"
	StatSampleCount = "SampleCount"
)

// Value derives one CloudWatch statistic from this aggregate bucket. An
// unrecognized statistic falls back to Sum — count-style metrics (the common
// case) are so a caller that forgets to special-case a new statistic degrades
// to "the count", not a zero/NaN.
func (b Bucket) Value(statistic string) float64 {
	switch statistic {
	case StatAverage:
		if b.Count == 0 {
			return 0
		}
		return b.Sum / b.Count
	case StatMinimum:
		return b.Min
	case StatMaximum:
		return b.Max
	case StatSampleCount:
		return b.Count
	case StatSum:
		return b.Sum
	default:
		return b.Sum
	}
}

// ChartPoint is one rendered (timestamp, value) pair — the JSON shape the
// web Monitor tab's BFF read endpoint returns for one series.
type ChartPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// ChartQuery answers one chart-ready (metric, statistic) series over
// [start, end] at the given display period: QueryAuto picks the stored
// resolution and shapes the range, and this converts each returned bucket to
// the requested statistic. resolutionUsedSec is QueryAuto's own return value,
// passed through so a caller can disclose which stored resolution actually
// answered the request.
func (s *Service) ChartQuery(ctx context.Context, namespace, name, statistic string, dims []Dimension, start, end time.Time, period time.Duration) (points []ChartPoint, resolutionUsedSec int, err error) {
	buckets, resSec, err := s.QueryAuto(ctx, namespace, name, dims, start, end, period)
	if err != nil {
		return nil, 0, err
	}
	points = make([]ChartPoint, 0, len(buckets))
	for _, b := range buckets {
		points = append(points, ChartPoint{Timestamp: b.Start, Value: b.Value(statistic)})
	}
	return points, resSec, nil
}
