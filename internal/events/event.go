// Package events provides the internal event bus used for cross-service
// notifications (e.g. S3 → SQS, SNS → SQS, SQS → Lambda).
//
// Architecture
//
//	Publisher (e.g. S3 handler)
//	    └─ bus.Publish(ctx, Event{...})
//	           │
//	           ▼  (goroutine per subscriber)
//	    Subscriber (e.g. S3 notification dispatcher)
//	           └─ reads per-resource config, routes to a Sink
//
// The bus is the only component shared across service packages.
// Services publish; dispatchers subscribe; sinks deliver.
// No service package imports another service package.
package events

import "time"

// Type identifies the kind of event. Values follow the AWS event name
// convention so they can be stored in notification filter rules verbatim.
type Type string

const (
	// All is a wildcard that receives every event regardless of type.
	// Used by the SSE event-stream endpoint to fan out all events to connected clients.
	All Type = "*"

	// S3ObjectCreated fires after a successful PutObject or CopyObject.
	S3ObjectCreated Type = "s3:ObjectCreated:*"
	// S3ObjectRemoved fires after a successful DeleteObject.
	S3ObjectRemoved Type = "s3:ObjectRemoved:*"
)

// S3ObjectPayload carries the details of an S3 object mutation event.
type S3ObjectPayload struct {
	Bucket    string
	Key       string
	Size      int64
	ETag      string
	EventName string // e.g. "ObjectCreated:Put", "ObjectRemoved:Delete"
}

// Event is the envelope published onto the Bus.
// Payload is a typed struct (e.g. S3ObjectPayload); use a type assertion
// in the subscriber after checking the Type.
type Event struct {
	Type    Type
	Time    time.Time
	Source  string // service name: "s3", "sqs", "sns", …
	Payload any
}
