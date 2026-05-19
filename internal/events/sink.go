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
