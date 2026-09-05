package events

import (
	"context"
	"time"

	"github.com/overcast-sh/overcast/internal/protocol"
)

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

// BusEntry is one event to publish onto an EventBridge event bus. It mirrors
// the PutEvents request entry an AWS service emits on a customer's behalf.
//
// Detail is the already-marshalled JSON detail document, matching PutEvents'
// own string-typed Detail.
type BusEntry struct {
	Source     string
	DetailType string
	Detail     string
	Resources  []string
}

// BusPublisher is the narrow interface a service uses to emit its own events
// onto the default EventBridge bus, the way AWS services publish
// service-originated events. It lives in the events package so both the
// producing service and the EventBridge implementation can reference it
// without an import cycle.
//
// Delivery is best effort from the producer's point of view: an error means
// EventBridge could not accept the entry, not that no rule matched it.
type BusPublisher interface {
	PublishBusEvent(ctx context.Context, entry BusEntry) error
}

// TopicPublisher is the narrow interface used by notification dispatchers to
// publish event payloads to SNS topics (S3 bucket notifications). It lives in
// the events package so both the dispatcher and the SNS implementation can
// reference it without creating import cycles.
//
// topicARN is the full SNS topic ARN. subject and message become the SNS
// notification envelope's Subject and Message fields, so message is the
// already-marshalled JSON document a subscriber should read back — for S3
// notifications, the {"Records":[…]} envelope as a string.
//
// A non-nil error means the publish was refused (no such topic); fan-out to
// the topic's subscribers is asynchronous and its per-subscription failures
// are reported through SNS's own delivery-failure path, exactly as they are
// for a client Publish call.
type TopicPublisher interface {
	PublishToTopic(ctx context.Context, topicARN, subject, message string) error
}

// ReceivedMessage is a single message received from an SQS queue by
// the event source mapping (ESM) SQS poller.
type ReceivedMessage struct {
	MessageID     string
	ReceiptHandle string
	Body          string
	Attributes    map[string]string
	MD5OfBody     string
}

// MessageReceiver is the narrow interface used by the Lambda event source
// mapping (ESM) SQS poller to receive and acknowledge messages from a queue.
// It lives in the events package to avoid import cycles between sqs and lambda.
type MessageReceiver interface {
	// ReceiveMessages fetches up to maxCount visible messages from queueName,
	// making them invisible for visibilitySeconds. Returns an empty slice (not
	// an error) when the queue is empty.
	ReceiveMessages(ctx context.Context, queueName string, maxCount, visibilitySeconds int) ([]ReceivedMessage, error)
	// DeleteMessages permanently removes the given messages (identified by
	// receipt handle) from queueName. Best-effort: individual failures are
	// logged but do not abort the batch.
	DeleteMessages(ctx context.Context, queueName string, receiptHandles []string) error
}

// StreamRecord is a single record read from a Kinesis data stream shard.
type StreamRecord struct {
	ShardID                     string
	SequenceNumber              string
	PartitionKey                string
	Data                        []byte
	ApproximateArrivalTimestamp time.Time
}

// StreamRecordReceiver is the narrow interface used by the EventBridge Pipes
// Kinesis source poller to read a stream without importing the Kinesis service.
// It lives in the events package for the same reason MessageReceiver does:
// avoiding an import cycle between the consumer and the producing service.
//
// It is deliberately cursor-in / cursor-out rather than iterator-based, so the
// caller owns the per-shard position and the Kinesis service keeps no
// per-consumer state.
type StreamRecordReceiver interface {
	// ListStreamShards returns the shard IDs of a stream. A stream that does
	// not exist is an error, not an empty list.
	ListStreamShards(ctx context.Context, streamName string) ([]string, error)
	// ReceiveStreamRecords returns up to limit records written to shardID
	// after afterSeqNo ("" reads from the start of the shard), together with
	// the sequence number to pass on the next call.
	ReceiveStreamRecords(ctx context.Context, streamName, shardID, afterSeqNo string, limit int) (records []StreamRecord, next string, err error)
}

// FunctionInvoker is the narrow interface used by notification dispatchers
// to invoke Lambda functions. It lives in the events package so both the
// S3 notification dispatcher and the Lambda implementation can reference it
// without creating import cycles.
//
// functionARN is the full Lambda function ARN.
// payload is the raw JSON event payload (e.g. an S3 notification envelope).
type FunctionInvoker interface {
	InvokeAsync(ctx context.Context, functionARN string, payload []byte) error
}

// FunctionEventInvoker is FunctionInvoker for callers that must know whether
// delivery succeeded — SNS subscription fan-out, which has to dead-letter a
// failed delivery instead of dropping it.
//
// InvokeEvent accepts an event and returns; it does not run the function. It
// reports the outcome real Lambda's Invoke API reports synchronously for an
// `InvocationType=Event` call: a non-nil error means the event was never
// accepted (no such function, function not in an invokable state) and the
// caller still owns it.
//
// A throttle is not a refusal. AWS has already answered 202 by the time
// concurrency is needed, so it retries internally and the event source never
// hears about it; Overcast does the same. A caller that dead-letters on a
// throttle would lose events AWS would have run.
//
// A function that is accepted and then fails at runtime is likewise a
// *successful* delivery — on AWS that failure is handled by Lambda's own retry
// policy and dead-letter config, not by the event source's.
type FunctionEventInvoker interface {
	InvokeEvent(ctx context.Context, functionARN string, payload []byte) error
}

// FunctionInvokeAuthorizer is the narrow interface a service uses to ask
// Lambda whether its own service principal may invoke a function, the way AWS
// checks the function's resource-based policy before letting S3, SNS, API
// Gateway or EventBridge deliver to it. It lives in the events package for the
// same reason FunctionInvoker does: the callers must not import lambda.
//
// functionRef is a bare function name or a function ARN, qualified or not.
// principal is the AWS service principal the caller invokes as
// ("s3.amazonaws.com", "sns.amazonaws.com", …). sourceARN and sourceAccount are
// the aws:SourceArn and aws:SourceAccount condition-key values the caller
// contributes; either may be empty, and a statement conditioned on one the
// caller does not supply cannot match.
//
// A nil return authorises the invocation, and is what an emulator with
// enforcement switched off always answers — the default, since Overcast does
// not validate credentials and is not a security boundary. A non-nil error is
// Lambda's own AccessDeniedException; each caller renders it as whatever its
// service does when the permission is missing rather than returning it
// verbatim.
type FunctionInvokeAuthorizer interface {
	AuthorizeServiceInvoke(ctx context.Context, functionRef, principal, sourceARN, sourceAccount string) *protocol.AWSError
}

// FunctionSyncInvoker is the narrow interface used by API Gateway (and
// future services) to invoke Lambda functions synchronously and receive
// the response payload. It lives in the events package to avoid import
// cycles between apigateway and lambda.
//
// functionName is the bare function name (not the full ARN).
// payload is the raw JSON event payload (e.g. an API Gateway proxy event).
// Returns the response payload, a function error string (empty on success),
// and any infrastructure-level error.
type FunctionSyncInvoker interface {
	Invoke(ctx context.Context, functionName string, payload []byte) (*InvokeOutcome, error)
}

// InvokeOutcome is the result of a synchronous Lambda invocation.
type InvokeOutcome struct {
	// Payload is the raw bytes returned by the function handler.
	Payload []byte
	// FunctionError is non-empty when the function returned an error
	// (runtime or handled). Corresponds to the X-Amz-Function-Error header.
	FunctionError string
}

// CognitoTokenValidator is the narrow interface used by API Gateway to validate
// Cognito-issued JWTs without creating an import cycle between apigateway and
// cognito. ValidateCognitoToken verifies the RS256 signature and expiry of the
// token against the pool's stored signing key, and returns the parsed claims on
// success.
//
// tokenStr is the raw JWT string (the "Bearer " prefix must be stripped by the caller).
type CognitoTokenValidator interface {
	ValidateCognitoToken(ctx context.Context, tokenStr string) (map[string]any, error)
}

// DynamoDBInvoker is the narrow interface used by AppSync to invoke DynamoDB
// operations (GetItem, PutItem, Query, Scan, DeleteItem, etc.) via the local
// DynamoDB emulator. It lives in the events package to avoid import cycles
// between appsync and dynamodb.
//
// operation is the DynamoDB API action name (e.g. "GetItem", "PutItem").
// input is the raw JSON request body (same format as the DynamoDB JSON wire protocol).
// Returns the raw JSON response body and any infrastructure-level error.
type DynamoDBInvoker interface {
	Invoke(ctx context.Context, operation string, input []byte) ([]byte, error)
}

// LogEntry is a single log event to be written to CloudWatch Logs.
type LogEntry struct {
	// Timestamp is the event time in epoch milliseconds.
	Timestamp int64
	// Message is the log line content.
	Message string
}

// LogWriter is the narrow interface used by Lambda (and other services) to
// write structured log output to CloudWatch Logs without importing the logs
// package directly (which would create an import cycle).
//
// EnsureLogGroup creates the log group if it does not already exist.
// It is safe to call on every function creation — creation is idempotent.
//
// EnsureLogStream creates the log group and stream if they do not already
// exist. It is safe to call on every invocation — creation is idempotent.
//
// WriteLogEvents appends events to the named stream. The group and stream
// must already exist (call EnsureLogStream first).
type LogWriter interface {
	EnsureLogGroup(ctx context.Context, groupName string) error
	EnsureLogStream(ctx context.Context, groupName, streamName string) error
	WriteLogEvents(ctx context.Context, groupName, streamName string, events []LogEntry) error
}
