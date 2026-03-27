package events

import "context"

// MessageEnqueuer is the narrow interface used by notification dispatchers
// to deliver event payloads to destination queues (SQS). It lives in the
// events package so both the dispatcher and the SQS implementation can
// reference it without creating import cycles.
//
// queueName is the simple queue name (not the full ARN or URL).
// body is the fully-formed JSON message body.
type MessageEnqueuer interface {
	EnqueueRaw(ctx context.Context, queueName string, body string) error
}
