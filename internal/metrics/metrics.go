// Package metrics is the AWS-shaped service-metrics substrate
// (docs/plans/service-metrics-platform.md): a dependency-neutral home for
// metric identity, storage, one-minute aggregation, and retention, shared by
// every emulated service that publishes AWS-native CloudWatch metrics.
//
// CloudWatch (internal/services/cloudwatch) is the AWS protocol adapter over
// this package: it owns request validation, response serialization,
// pagination, and alarm evaluation, and reads through this package's
// Repository for the automatic observations services record here — merged
// alongside its own PutMetricData-supplied points, never replacing them.
// Service packages (Lambda first; SQS/SNS/DynamoDB/API Gateway follow) depend
// only on the narrow Recorder interface — never on
// internal/services/cloudwatch.
//
// The internal/events.Bus is deliberately NOT this package's ledger. See the
// plan's "Architectural decision" section: delivery is asynchronous and
// lossy, which is correct for operator activity/UI but not for an
// AWS-visible accounting metric. Services record an observation directly at
// their authoritative lifecycle boundary; a narrow service-local outcome
// helper (e.g. Lambda's recordInvocationOutcome) is what keeps that DRY
// across a service's several invocation/delivery paths.
package metrics

import (
	"context"
	"time"
)

// Dimension is a name/value pair identifying part of a metric series. Shaped
// identically to cloudwatch.Dimension so callers can convert 1:1 without a
// dependency in either direction.
type Dimension struct {
	Name  string
	Value string
}

// Observation is one measured fact, recorded once at the point a service
// knows its outcome. Metric-definition code in the calling service package —
// not the wire handler — decides how many Observations one execution or
// event produces (docs/plans/service-metrics-platform.md: "Observation
// intentionally represents one measured fact").
type Observation struct {
	// Namespace is the AWS namespace, e.g. "AWS/Lambda". Never an
	// Overcast-invented namespace for an AWS-shaped metric.
	Namespace string
	// Name is the AWS metric name, e.g. "Invocations".
	Name string
	// Dimensions is this observation's complete dimension set. Canonicalized
	// (sorted, deduplicated) by Observe — callers do not need to sort it.
	Dimensions []Dimension
	// Timestamp is when the fact occurred. Zero means "use the recorder's
	// injected clock at Observe time".
	Timestamp time.Time
	// Unit is the AWS unit string, e.g. "Count" or "Milliseconds".
	Unit string
	// Value is this observation's measurement — 1 for a count-style metric
	// event, a duration in the metric's unit for a duration-style metric, or
	// a gauge sample (e.g. ConcurrentExecutions) read at Observe time.
	Value float64
}

// Metric identifies one distinct (namespace, name, dimension-set) series —
// the shape ListMetrics-style callers need.
type Metric struct {
	Namespace  string
	Name       string
	Dimensions []Dimension
}

// Bucket is one aggregated one-minute-resolution datapoint. Count/Sum/Min/Max
// compose exactly, which is what lets a query merge the in-memory active
// overlay with persisted buckets without double counting (see
// (*Service).QueryRange).
type Bucket struct {
	Start time.Time
	Count float64
	Sum   float64
	Min   float64
	Max   float64
	Unit  string
}

// Recorder is the narrow interface service packages depend on to record an
// observation — never internal/services/cloudwatch directly (plan
// acceptance criteria: "No service imports internal/services/cloudwatch").
// Implemented by *Service.
type Recorder interface {
	// Observe records one fact. It updates an in-memory active bucket under a
	// short sharded lock and returns quickly: no state.Store I/O, HTTP, JSON
	// encoding, goroutine creation, or event-bus publish happens on this path.
	// A non-nil error means invalid input (empty Namespace/Name) or a
	// recorder that has already stopped accepting observations — never a
	// storage outage, which is handled by the background flusher instead.
	Observe(ctx context.Context, o Observation) error
}
