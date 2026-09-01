package lambda

// dead_letter.go — where a failure that has nowhere else to go is parked.
//
// Two paths reach a dead-letter target, and they carry different things:
//
//   - An asynchronous invocation (InvocationType=Event) that fails goes to the
//     function's own DeadLetterConfig.TargetArn. AWS "sends the event to the
//     dead-letter queue as-is, with additional information in attributes", so
//     the body is the original event and the diagnosis rides on three message
//     attributes — RequestID, ErrorCode and ErrorMessage. See
//     https://docs.aws.amazon.com/lambda/latest/dg/invocation-async-retain-records.html
//     § Adding a dead-letter queue.
//   - An event-source mapping that exhausts its retries goes to the ESM's
//     DestinationConfig.OnFailure.Destination, which is a *destination* rather
//     than a dead-letter queue: the body is the invocation record envelope
//     documented on the same page, and there are no attributes.
//
// What the two share is the delivery itself — "post these bytes to this ARN,
// which is either an SQS queue or an SNS topic" — and that lives here once, in
// deliverFailureRecord.

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/overcast-sh/overcast/internal/eventtarget"
)

// deadLetterAttributeErrorMessageLimit is how much of the error message AWS
// puts on the ErrorMessage attribute — "the first 1 KB of the error message".
const deadLetterAttributeErrorMessageLimit = 1024

// errNoTargetRouter reports a dispatcher that was never wired. It is a wiring
// fault rather than a delivery failure, so callers log it as one.
var errNoTargetRouter = errors.New("target router is not configured")

// deliverFailureRecord posts payload to a dead-letter target through the shared
// eventtarget dispatcher, which reaches the sink over the same route an SDK
// client would — so a queue or topic that does not exist produces that
// service's own error rather than a silent no-op.
//
// AWS permits a standard SQS queue or a standard SNS topic for both a
// function's dead-letter queue and an event-source mapping's on-failure
// destination, and nothing else is accepted here either: delivering to a target
// AWS would refuse would be a worse lie than reporting that it cannot be done.
func deliverFailureRecord(
	ctx context.Context,
	targets *eventtarget.Dispatcher,
	arn string,
	payload []byte,
	attributes map[string]eventtarget.MessageAttribute,
) error {
	if arn == "" {
		return errors.New("no dead-letter target is configured")
	}
	if targets == nil {
		return errNoTargetRouter
	}
	kind, err := eventtarget.Classify(arn)
	if err != nil {
		return err
	}
	if kind != eventtarget.KindSQS && kind != eventtarget.KindSNS {
		return fmt.Errorf("dead-letter target %s must be an SQS queue or an SNS topic", arn)
	}
	return targets.Deliver(ctx, eventtarget.Request{
		ARN:               arn,
		Kind:              kind,
		Payload:           payload,
		MessageAttributes: attributes,
	})
}

// deadLetterAttributes builds the three message attributes AWS puts on a
// dead-lettered asynchronous invocation.
//
// ErrorCode is documented as "The HTTP status code" and is a Number. Every
// outcome that can reach a dead-letter queue here is one the Invoke API answers
// 200 for — a handled error, an unhandled crash, a timeout, and (in this
// emulator) a cold start that never produced an execution environment, all of
// which invokeSyncOnce reports as a 200 carrying X-Amz-Function-Error rather
// than as an HTTP failure. The caller passes the code so that stays a decision
// made where the outcome is known, not a constant buried in here.
func deadLetterAttributes(requestID string, errorCode int, errorMessage string) map[string]eventtarget.MessageAttribute {
	return map[string]eventtarget.MessageAttribute{
		"RequestID":    {DataType: "String", StringValue: requestID},
		"ErrorCode":    {DataType: "Number", StringValue: strconv.Itoa(errorCode)},
		"ErrorMessage": {DataType: "String", StringValue: truncateErrorMessage(errorMessage)},
	}
}

// truncateErrorMessage cuts a message to the first kilobyte, as AWS does. The
// cut is by byte, matching the documented limit; a multi-byte rune split across
// the boundary is AWS's behaviour too, and the attribute is diagnostic text
// rather than something a consumer parses.
func truncateErrorMessage(message string) string {
	if len(message) <= deadLetterAttributeErrorMessageLimit {
		return message
	}
	return message[:deadLetterAttributeErrorMessageLimit]
}
