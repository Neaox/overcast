package s3

// notifications.go — S3 event notification dispatcher.
//
// This subscribes to the event bus for S3 object mutations and routes
// matching events to configured SQS queues (and in future, SNS topics
// and Lambda functions) based on per-bucket notification configuration.
//
// Architecture:
//
//	S3 handler → bus.Publish(S3ObjectCreated{...})
//	                  ↓  (goroutine — async)
//	    NotificationDispatcher.handle(ctx, event)
//	      → load bucket's NotificationConfig from store
//	      → match event type + key filter rules
//	      → call MessageEnqueuer for each matching queue config

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/overcast/internal/events"
)

// NotificationDispatcher reads per-bucket notification configs and routes
// matched events to destination sinks (SQS, SNS, Lambda).
type NotificationDispatcher struct {
	store    *s3Store
	enqueuer events.MessageEnqueuer
	logger   *zap.Logger
	region   string
}

// NewNotificationDispatcher creates a dispatcher and subscribes it to the
// given event bus for all S3 event types. The returned cancel function
// removes the subscriptions (useful in tests).
func NewNotificationDispatcher(
	store *s3Store,
	enqueuer events.MessageEnqueuer,
	bus *events.Bus,
	logger *zap.Logger,
	region string,
) (d *NotificationDispatcher, cancel func()) {
	d = &NotificationDispatcher{
		store:    store,
		enqueuer: enqueuer,
		logger:   logger,
		region:   region,
	}

	c1 := bus.Subscribe(events.S3ObjectCreated, d.handle)
	c2 := bus.Subscribe(events.S3ObjectRemoved, d.handle)

	return d, func() { c1(); c2() }
}

// handle is the bus subscriber callback. It runs in its own goroutine.
func (d *NotificationDispatcher) handle(ctx context.Context, e events.Event) {
	p, ok := e.Payload.(events.S3ObjectPayload)
	if !ok {
		return
	}

	cfg, aerr := d.store.getNotificationConfig(ctx, p.Bucket)
	if aerr != nil {
		d.logger.Warn("s3: notification config load failed",
			zap.String("bucket", p.Bucket),
			zap.String("error", aerr.Message),
		)
		return
	}

	eventType := string(e.Type)

	for _, qc := range cfg.QueueConfigurations {
		if !matchesEvent(qc.Events, eventType) {
			continue
		}
		if !matchesFilter(qc.Filter, p.Key) {
			continue
		}

		body := buildNotificationJSON(p, e.Time, qc.ID, d.region)
		queueName := queueNameFromARN(qc.ARN)
		if queueName == "" {
			d.logger.Warn("s3: invalid queue ARN in notification config",
				zap.String("arn", qc.ARN),
			)
			continue
		}

		if err := d.enqueuer.EnqueueRaw(ctx, queueName, body); err != nil {
			d.logger.Warn("s3: notification delivery to SQS failed",
				zap.String("queue", queueName),
				zap.Error(err),
			)
		}
	}

	// TODO(priority:P3): deliver to TopicConfigurations (SNS) when SNS is implemented
	// TODO(priority:P3): deliver to LambdaConfigurations when Lambda is implemented
}

// matchesEvent checks whether eventType matches any of the configured events.
// Supports wildcard matching: "s3:ObjectCreated:*" matches "s3:ObjectCreated:*".
func matchesEvent(configured []string, eventType string) bool {
	for _, e := range configured {
		if e == eventType {
			return true
		}
		// "s3:ObjectCreated:*" should match "s3:ObjectCreated:Put" etc.
		if strings.HasSuffix(e, ":*") {
			prefix := strings.TrimSuffix(e, "*")
			if strings.HasPrefix(eventType, prefix) {
				return true
			}
		}
	}
	return false
}

// matchesFilter checks whether key passes the notification filter rules.
// No filter means all keys match (AWS behaviour).
func matchesFilter(f *NotificationFilter, key string) bool {
	if f == nil {
		return true
	}
	for _, rule := range f.Key.Rules {
		switch strings.ToLower(rule.Name) {
		case "prefix":
			if !strings.HasPrefix(key, rule.Value) {
				return false
			}
		case "suffix":
			if !strings.HasSuffix(key, rule.Value) {
				return false
			}
		}
	}
	return true
}

// queueNameFromARN extracts the queue name from an SQS ARN.
// ARN format: arn:aws:sqs:<region>:<account>:<queue-name>
func queueNameFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return ""
	}
	return parts[5]
}

// s3NotificationRecord is the AWS S3 event notification JSON schema (v2.1).
type s3NotificationRecord struct {
	EventVersion string    `json:"eventVersion"`
	EventSource  string    `json:"eventSource"`
	AWSRegion    string    `json:"awsRegion"`
	EventTime    time.Time `json:"eventTime"`
	EventName    string    `json:"eventName"`
	S3           s3Detail  `json:"s3"`
}

type s3Detail struct {
	SchemaVersion   string       `json:"s3SchemaVersion"`
	ConfigurationID string       `json:"configurationId"`
	Bucket          s3BucketInfo `json:"bucket"`
	Object          s3ObjectInfo `json:"object"`
}

type s3BucketInfo struct {
	Name string `json:"name"`
	ARN  string `json:"arn"`
}

type s3ObjectInfo struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
	ETag string `json:"eTag"`
}

type s3NotificationEnvelope struct {
	Records []s3NotificationRecord `json:"Records"`
}

func buildNotificationJSON(p events.S3ObjectPayload, eventTime time.Time, configID, region string) string {
	env := s3NotificationEnvelope{
		Records: []s3NotificationRecord{
			{
				EventVersion: "2.1",
				EventSource:  "aws:s3",
				AWSRegion:    region,
				EventTime:    eventTime,
				EventName:    p.EventName,
				S3: s3Detail{
					SchemaVersion:   "1.0",
					ConfigurationID: configID,
					Bucket: s3BucketInfo{
						Name: p.Bucket,
						ARN:  "arn:aws:s3:::" + p.Bucket,
					},
					Object: s3ObjectInfo{
						Key:  p.Key,
						Size: p.Size,
						ETag: p.ETag,
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(env)
	return string(raw)
}
