package sns

// metrics_sns.go is SNS's half of the service-metrics substrate
// (docs/plans/service-metrics-platform.md, phase 2): the AWS/SNS metric
// catalogue, recorded at the two authoritative outcome boundaries every
// notification already funnels through — Publish/PublishBatch (once per
// successfully-accepted message) and fanOut's per-subscription delivery
// attempt (once per subscriber, via the same success/failDelivery branches
// that already publish an events.Bus notification).
//
// AWS reference: https://docs.aws.amazon.com/sns/latest/dg/sns-monitoring-using-cloudwatch.html
//
// All four AWS/SNS metrics are count-or-size, never gauges — recorded once
// per real outcome, never sampled:
//
//   - NumberOfMessagesPublished, PublishSize — recorded once per successful
//     Publish/PublishBatch-entry call, before fan-out is dispatched.
//   - NumberOfNotificationsDelivered, NumberOfNotificationsFailed — recorded
//     once per subscription delivery attempt, from fanOut's per-protocol
//     success branches and failDelivery's single failure funnel
//     respectively. A protocol whose delivery dependency is un-wired (nil
//     enqueuer/mailer/smsSender/outbound) currently `continue`s silently
//     without calling failDelivery — see fanOut's doc comment — so that path
//     records neither Delivered nor Failed, matching the plan's "only where
//     the emulator can observe the underlying fact" rule: there is no
//     delivery outcome to observe when the dependency was never wired.
import (
	"context"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/metrics"
)

// metricsRecorder is the narrow interface SNS depends on to record outcome
// facts — never internal/services/cloudwatch (plan acceptance criteria: "no
// service imports internal/services/cloudwatch"). Satisfied by *metrics.Service.
type metricsRecorder interface {
	Observe(ctx context.Context, o metrics.Observation) error
}

const snsMetricsNamespace = "AWS/SNS"

// observeSNSMetric records one AWS/SNS observation, logging (never failing
// the caller's request) on error. A nil h.metrics (collection disabled, or a
// unit test that never called Service.InitMetrics) makes this a no-op.
func (h *Handler) observeSNSMetric(ctx context.Context, name, topicName, unit string, value float64) {
	if h.metrics == nil {
		return
	}
	if err := h.metrics.Observe(ctx, metrics.Observation{
		Namespace:  snsMetricsNamespace,
		Name:       name,
		Dimensions: []metrics.Dimension{{Name: "TopicName", Value: topicName}},
		Timestamp:  h.clk.Now(),
		Unit:       unit,
		Value:      value,
	}); err != nil {
		h.log.WithRecorder(ctx).Debug("sns: metrics observe failed", zap.String("metric", name), zap.Error(err))
	}
}

// recordMessagePublished is SNS's Publish/PublishBatch outcome helper —
// called once per successfully-accepted message, before fan-out begins.
func (h *Handler) recordMessagePublished(ctx context.Context, topicName string, messageBytes int) {
	h.observeSNSMetric(ctx, "NumberOfMessagesPublished", topicName, "Count", 1)
	h.observeSNSMetric(ctx, "PublishSize", topicName, "Bytes", float64(messageBytes))
}

// recordNotificationDelivered is fanOut's per-subscription success helper —
// called once per protocol's successful delivery branch.
func (h *Handler) recordNotificationDelivered(ctx context.Context, topicName string) {
	h.observeSNSMetric(ctx, "NumberOfNotificationsDelivered", topicName, "Count", 1)
}

// recordNotificationFailed is failDelivery's single failure-funnel helper —
// called for every protocol's failure branch, including the
// no-delivery-implementation default case.
func (h *Handler) recordNotificationFailed(ctx context.Context, topicName string) {
	h.observeSNSMetric(ctx, "NumberOfNotificationsFailed", topicName, "Count", 1)
}
