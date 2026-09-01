package cloudwatch

// metrics_bridge.go is the read-through between internal/services/cloudwatch
// and internal/metrics (docs/plans/service-metrics-platform.md). CloudWatch
// keeps its own existing PutMetricData storage (cloudwatchStore.putMetric /
// putMetricDataPoint, unchanged by this file) as the source of user-supplied
// custom metrics; automatically-recorded service metrics (Lambda's
// Invocations/Errors/Duration/Throttles/ConcurrentExecutions, and future
// SQS/SNS/... series) live in internal/metrics instead, written directly by
// each service through its injected metrics.Recorder — never through this
// package.
//
// Every read path that answers an AWS request — ListMetrics,
// GetMetricStatistics, GetMetricData, and the alarm evaluator's own metric
// window read — merges both sources so a caller cannot tell an automatic
// series from a custom one except by its dimensions. This is the "same
// store or a read-through" option the plan calls out explicitly, chosen over
// migrating the already-tested PutMetricData storage engine onto
// internal/metrics wholesale: lower risk, and internal/metrics' own
// dedicated-table storage is unaffected by legacy cloudwatch:metricdata
// compatibility concerns.
//
// A metrics-subsystem read failure never fails an AWS CloudWatch request: it
// degrades to "no automatic series available this call" and still returns
// whatever PutMetricData-backed data exists, because a monitoring subsystem
// hiccup must not turn a healthy CloudWatch response into an error.
import (
	"context"
	"time"

	"github.com/overcast-sh/overcast/internal/metrics"
)

// metricsReader is the narrow read surface CloudWatch needs from
// internal/metrics. Deliberately excludes Observe/Recorder — CloudWatch never
// writes automatic service metrics, only serves them.
type metricsReader interface {
	ListMetrics(ctx context.Context, namespace string) ([]metrics.Metric, error)
	QueryRange(ctx context.Context, namespace, name string, dims []metrics.Dimension, start, end time.Time) ([]metrics.Bucket, error)
}

// InitMetrics wires the shared service-metrics repository so CloudWatch's
// query APIs and the alarm evaluator see automatically-recorded series
// alongside custom PutMetricData data. Called once from router.New, after
// metrics.NewRecorder; a Service without it (unit tests, or
// OVERCAST_SERVICE_METRICS=disabled) simply never merges anything in — every
// existing PutMetricData/GetMetricStatistics/GetMetricData/ListMetrics/alarm
// behavior is unchanged.
func (s *Service) InitMetrics(m metricsReader) { s.store.svcMetrics = m }

func toMetricsDimensions(in []Dimension) []metrics.Dimension {
	out := make([]metrics.Dimension, len(in))
	for i, d := range in {
		out[i] = metrics.Dimension{Name: d.Name, Value: d.Value}
	}
	return out
}

func fromMetricsDimensions(in []metrics.Dimension) []Dimension {
	out := make([]Dimension, len(in))
	for i, d := range in {
		out[i] = Dimension{Name: d.Name, Value: d.Value}
	}
	return out
}

// seriesMergeKey identifies a series for de-duplication when merging the two
// sources — cheap and only used at merge time, never on a hot path.
func seriesMergeKey(namespace, name string, dims []Dimension) string {
	return namespace + "\x00" + name + "\x00" + dimensionsKey(canonicalizeDimensions(dims))
}

// mergedListMetrics returns cloudwatchStore's own custom-metric catalogue
// plus every series internal/metrics knows about for namespace ("" = every
// namespace), deduplicated by (namespace, name, dimension set).
func (s *cloudwatchStore) mergedListMetrics(ctx context.Context, namespace string) ([]*Metric, error) {
	out, err := s.listMetrics(ctx, namespace)
	if err != nil {
		return nil, err
	}
	if s.svcMetrics == nil {
		return out, nil
	}
	seen := make(map[string]bool, len(out))
	for _, m := range out {
		seen[seriesMergeKey(m.Namespace, m.MetricName, m.Dimensions)] = true
	}
	svcMetrics, err := s.svcMetrics.ListMetrics(ctx, namespace)
	if err != nil {
		// Degrade gracefully — see file doc comment.
		return out, nil
	}
	for _, m := range svcMetrics {
		dims := fromMetricsDimensions(m.Dimensions)
		key := seriesMergeKey(m.Namespace, m.Name, dims)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, &Metric{Namespace: m.Namespace, MetricName: m.Name, Dimensions: canonicalizeDimensions(dims)})
	}
	return out, nil
}

// mergedMetricDataPoints returns cloudwatchStore's own custom-metric
// datapoints for one series plus internal/metrics' one-minute buckets for the
// same (namespace, metricName, dimensions), converted to the same
// MetricDataPoint shape aggregateMetricBuckets already knows how to combine
// (each bucket becomes one pre-aggregated point, exactly like a
// StatisticValues-carrying PutMetricData datum).
func (s *cloudwatchStore) mergedMetricDataPoints(ctx context.Context, namespace, metricName string, dimensions []Dimension, startTime, endTime time.Time) ([]*MetricDataPoint, error) {
	out, err := s.listMetricDataPoints(ctx, namespace, metricName, dimensions, startTime, endTime)
	if err != nil {
		return nil, err
	}
	if s.svcMetrics == nil {
		return out, nil
	}
	buckets, err := s.svcMetrics.QueryRange(ctx, namespace, metricName, toMetricsDimensions(dimensions), startTime, endTime)
	if err != nil {
		// Degrade gracefully — see file doc comment.
		return out, nil
	}
	for _, b := range buckets {
		out = append(out, &MetricDataPoint{
			Namespace: namespace, MetricName: metricName, Dimensions: dimensions,
			Timestamp: b.Start, Unit: b.Unit,
			SampleCount: b.Count, Sum: b.Sum, Minimum: b.Min, Maximum: b.Max,
		})
	}
	return out, nil
}
