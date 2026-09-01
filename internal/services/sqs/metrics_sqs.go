package sqs

// metrics_sqs.go is SQS's half of the service-metrics substrate
// (docs/plans/service-metrics-platform.md, phase 2): the AWS/SQS metric
// catalogue, recorded at each operation's authoritative outcome boundary —
// the same point that already publishes an events.Bus notification (or, for
// SendMessage, right after the message is durably enqueued) — and, for the
// four gauge-shaped queue-depth metrics AWS samples once a minute regardless
// of traffic, a small periodic sampler mirroring Service.watchVisibility's
// existing scanAllQueues/scanAllMessagesForQueue pattern.
//
// AWS reference: https://docs.aws.amazon.com/AmazonSQS/latest/dg/sqs-available-cloudwatch-metrics.html
//
// Count-vs-gauge disposition (see recorder.go's Bucket doc comment — count
// and sum compose exactly, so a count-style metric is queried with Sum and a
// gauge is queried with Average/Maximum against the same storage):
//
//   - NumberOfMessagesSent, NumberOfMessagesReceived, NumberOfMessagesDeleted,
//     NumberOfEmptyReceives, SentMessageSize — count-style: recorded once per
//     API-level outcome, Value=1 (or the batch count) for count metrics,
//     Value=size for SentMessageSize.
//   - ApproximateNumberOfMessagesVisible/NotVisible/Delayed,
//     ApproximateAgeOfOldestMessage — gauge-style: AWS publishes these every
//     minute for every active queue whether or not it has traffic (they are
//     a sampled queue-depth fact, not a per-request event), so phase 2 adds a
//     periodic sampler rather than only recording on a message operation.
//     ApproximateAgeOfOldestMessage is the one exception recorded only when
//     the queue is non-empty: there is no "age of oldest message" fact for an
//     empty queue, so recording 0 there would be exactly the plan's
//     forbidden synthetic zero rather than a real observation.
import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/metrics"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// metricsRecorder is the narrow interface SQS depends on to record outcome
// facts and to read them back for the web Monitor tab's BFF endpoint
// (handler_metrics.go) — never internal/services/cloudwatch (plan acceptance
// criteria: "no service imports internal/services/cloudwatch"). Satisfied by
// *metrics.Service.
type metricsRecorder interface {
	Observe(ctx context.Context, o metrics.Observation) error
	ChartQuery(ctx context.Context, namespace, name, statistic string, dims []metrics.Dimension, start, end time.Time, period time.Duration) ([]metrics.ChartPoint, int, error)
}

const sqsMetricsNamespace = "AWS/SQS"

// sqsGaugeSampleInterval matches AWS's one-minute queue-attribute sampling
// model (docs/plans/service-metrics-platform.md's "AWS's one-minute
// service-metric model") and internal/metrics' own resolutionSeconds.
const sqsGaugeSampleInterval = 60 * time.Second

// observeSQSMetric records one AWS/SQS observation, logging (never failing
// the caller's request) on error — a metrics-subsystem hiccup must not turn
// a successful SQS operation into a caller-visible failure. A nil h.metrics
// (collection disabled, or a unit test that never called Service.InitMetrics)
// makes this a no-op.
func (h *Handler) observeSQSMetric(ctx context.Context, name, queueName, unit string, value float64) {
	if h.metrics == nil {
		return
	}
	if err := h.metrics.Observe(ctx, metrics.Observation{
		Namespace:  sqsMetricsNamespace,
		Name:       name,
		Dimensions: []metrics.Dimension{{Name: "QueueName", Value: queueName}},
		Timestamp:  h.clk.Now(),
		Unit:       unit,
		Value:      value,
	}); err != nil {
		h.log.Debug("sqs: metrics observe failed", zap.String("metric", name), zap.Error(err))
	}
}

// recordMessageSent is SQS's SendMessage/SendMessageBatch outcome helper —
// called once per message actually enqueued (never for the FIFO
// content-based-deduplication early return, which enqueues nothing new).
func (h *Handler) recordMessageSent(ctx context.Context, queueName string, bodyBytes int) {
	h.observeSQSMetric(ctx, "NumberOfMessagesSent", queueName, "Count", 1)
	h.observeSQSMetric(ctx, "SentMessageSize", queueName, "Bytes", float64(bodyBytes))
}

// recordMessagesReceived is SQS's ReceiveMessage outcome helper — called
// once per API call with the final returned message count (after any
// long-poll retry loop has settled), never once per internal poll attempt.
// AWS's own ReceiveMessage/EmptyReceive distinction: a call that returns zero
// messages counts as one EmptyReceive, never zero Invocations of
// NumberOfMessagesReceived plus zero of anything else.
func (h *Handler) recordMessagesReceived(ctx context.Context, queueName string, count int) {
	if count == 0 {
		h.observeSQSMetric(ctx, "NumberOfEmptyReceives", queueName, "Count", 1)
		return
	}
	h.observeSQSMetric(ctx, "NumberOfMessagesReceived", queueName, "Count", float64(count))
}

// recordMessageDeleted is SQS's DeleteMessage/DeleteMessageBatch outcome
// helper — called once per message actually removed from the store.
func (h *Handler) recordMessageDeleted(ctx context.Context, queueName string) {
	h.observeSQSMetric(ctx, "NumberOfMessagesDeleted", queueName, "Count", 1)
}

// ---- gauge sampler ---------------------------------------------------------

// sampleQueueGauges runs until ctx is cancelled, sampling every existing
// queue's four gauge-shaped AWS/SQS metrics once per sqsGaugeSampleInterval —
// mirroring watchVisibility's own scanAllQueues/scanAllMessagesForQueue
// enumeration (service.go), just at a coarser interval and computing
// aggregates instead of visibility transitions.
func (s *Service) sampleQueueGauges(ctx context.Context) {
	ticker := s.handler.clk.Ticker(sqsGaugeSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleQueueGaugesOnce(ctx)
		}
	}
}

// sampleQueueGaugesOnce samples every queue exactly once — extracted so
// tests can trigger a deterministic pass without waiting on a real ticker.
func (s *Service) sampleQueueGaugesOnce(ctx context.Context) {
	h := s.handler
	if h.metrics == nil {
		return
	}
	pairs, err := h.store.scanAllQueues(ctx)
	if err != nil {
		h.log.Debug("sqs: gauge sample scanAllQueues failed", zap.Error(err))
		return
	}
	now := h.clk.Now()
	for _, p := range pairs {
		var q Queue
		if err := json.Unmarshal([]byte(p.Value), &q); err != nil {
			continue
		}
		region, queueName := serviceutil.SplitRegionKey(p.Key)
		messages, err := h.store.scanAllMessagesForQueue(ctx, region, queueName)
		if err != nil {
			continue
		}

		var visible, notVisible, delayed int
		var oldestSentMs int64
		for _, msg := range messages {
			if oldestSentMs == 0 || msg.SentTimestamp < oldestSentMs {
				oldestSentMs = msg.SentTimestamp
			}
			switch {
			case msg.IsVisible(h.clk):
				visible++
			case msg.ApproximateReceiveCount == 0:
				// Never received yet: still behind its initial DelaySeconds,
				// not an in-flight message — same distinction PeekMessages
				// already draws (handler_message.go).
				delayed++
			default:
				notVisible++
			}
		}

		// AWS publishes these three every minute for every active queue
		// regardless of traffic (docs/plans/service-metrics-platform.md
		// phase 2 scope note) — always recorded, including zero, which is a
		// real sampled fact here, not a synthetic zero standing in for a
		// missing observation.
		h.observeSQSMetric(ctx, "ApproximateNumberOfMessagesVisible", q.Name, "Count", float64(visible))
		h.observeSQSMetric(ctx, "ApproximateNumberOfMessagesNotVisible", q.Name, "Count", float64(notVisible))
		h.observeSQSMetric(ctx, "ApproximateNumberOfMessagesDelayed", q.Name, "Count", float64(delayed))

		if len(messages) > 0 {
			age := now.Sub(time.UnixMilli(oldestSentMs)).Seconds()
			if age < 0 {
				age = 0
			}
			h.observeSQSMetric(ctx, "ApproximateAgeOfOldestMessage", q.Name, "Seconds", age)
		}
	}
}
