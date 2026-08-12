package lambda

// async_destination.go — where a record of an asynchronous invocation's outcome
// is sent.
//
// A destination is not a dead-letter queue, and conflating them is the mistake
// this file exists to avoid. AWS: "For dead-letter queues, Lambda only sends
// the content of the event, without details about the response." A destination
// instead receives an *invocation record* — a JSON envelope naming the request,
// the condition that produced it, how many attempts it took, and the function's
// own response. A function can have both, and then both are delivered, because
// AWS documents destinations as usable "in addition to or instead of a
// dead-letter queue".
//
// https://docs.aws.amazon.com/lambda/latest/dg/invocation-async-retain-records.html
//
// The delivery itself is the shared eventtarget dispatcher, which is why SQS,
// SNS, Lambda and EventBridge all work here for free. S3 is the one destination
// type AWS allows that this does not: it is refused at configuration time (see
// handler_event_invoke.go) rather than accepted and dropped here.

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/eventtarget"
)

// AWS's `requestContext.condition` values for an asynchronous invocation
// record. "RetriesExhausted" is the one a failed event carries after its last
// attempt; a successful one is simply "Success".
const (
	invocationConditionSuccess          = "Success"
	invocationConditionRetriesExhausted = "RetriesExhausted"
)

// deliverAsyncDestination sends the invocation record to the on-success or
// on-failure destination, if the function configures one.
//
// Failure to deliver is logged and dropped. There is no caller left to tell —
// the invocation answered 202 long ago — and on AWS the same situation raises
// the DestinationDeliveryFailures metric rather than surfacing anywhere the
// caller can see.
func (h *Handler) deliverAsyncDestination(
	ctx context.Context,
	fn *Function,
	cfg *EventInvokeConfig,
	payload []byte,
	outcome asyncInvokeOutcome,
	attempts int,
	succeeded bool,
) {
	arn := cfg.onFailureARN()
	side := "OnFailure"
	if succeeded {
		arn = cfg.onSuccessARN()
		side = "OnSuccess"
	}
	if arn == "" {
		return
	}

	record, err := json.Marshal(h.invocationRecord(fn, payload, outcome, attempts, succeeded))
	if err != nil {
		h.log.Error("invokeAsync: could not marshal the invocation record",
			zap.String("function", fn.Name), zap.Error(err))
		return
	}
	if err := deliverInvocationRecord(ctx, h.deadLetterTargets(), arn, record); err != nil {
		h.log.Error("invokeAsync: destination delivery failed — the record is dropped",
			zap.String("function", fn.Name),
			zap.String("destination", arn),
			zap.String("side", side),
			zap.Error(err))
		return
	}
	h.log.Debug("invokeAsync: invocation record sent to destination",
		zap.String("function", fn.Name),
		zap.String("destination", arn),
		zap.String("side", side))
}

// deliverInvocationRecord posts an invocation record to a destination.
//
// It is deliberately not deliverFailureRecord: that one refuses anything but a
// queue or a topic, which is right for a dead-letter target and wrong here.
// AWS's destinations also accept a Lambda function and an EventBridge event
// bus, and the dispatcher already reaches both.
func deliverInvocationRecord(ctx context.Context, targets *eventtarget.Dispatcher, arn string, record []byte) error {
	if targets == nil {
		return errNoTargetRouter
	}
	kind, err := eventtarget.Classify(arn)
	if err != nil {
		return err
	}
	req := eventtarget.Request{ARN: arn, Kind: kind, Payload: record}
	if kind == eventtarget.KindEventBus {
		// AWS puts the record in the PutEvents entry's detail and labels the
		// entry, so a rule on the target bus can match it: "The value for the
		// source event field is lambda", and the detail-type is one of the two
		// invocation-result strings.
		req.Source = "lambda"
		req.DetailType = "Lambda Function Invocation Result - Failure"
		if isSuccessRecord(record) {
			req.DetailType = "Lambda Function Invocation Result - Success"
		}
	}
	return targets.Deliver(ctx, req)
}

// isSuccessRecord reads the condition back out of a marshalled record, so the
// EventBridge detail-type does not need threading through a second parameter.
func isSuccessRecord(record []byte) bool {
	var decoded struct {
		RequestContext struct {
			Condition string `json:"condition"`
		} `json:"requestContext"`
	}
	if err := json.Unmarshal(record, &decoded); err != nil {
		return false
	}
	return decoded.RequestContext.Condition == invocationConditionSuccess
}

// invocationRecord builds AWS's asynchronous invocation record.
//
// The shape is the documented one, field for field:
//
//	{version, timestamp, requestContext{requestId, functionArn, condition,
//	 approximateInvokeCount}, requestPayload, responseContext{statusCode,
//	 executedVersion, functionError}, responsePayload}
//
// responseContext.functionError is omitted on success, as in AWS's own example,
// and statusCode is 200 for every outcome that reaches here for the same reason
// the dead-letter ErrorCode is — the Invoke API answers 200 for a function
// error, a crash and a timeout alike.
func (h *Handler) invocationRecord(
	fn *Function,
	payload []byte,
	outcome asyncInvokeOutcome,
	attempts int,
	succeeded bool,
) map[string]any {
	responseContext := map[string]any{
		"statusCode":      200,
		"executedVersion": "$LATEST",
	}
	condition := invocationConditionRetriesExhausted
	if succeeded {
		condition = invocationConditionSuccess
	} else {
		responseContext["functionError"] = "Unhandled"
	}

	record := map[string]any{
		"version":   "1.0",
		"timestamp": h.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"requestContext": map[string]any{
			"requestId":              outcome.requestID,
			"functionArn":            fn.ARN + ":$LATEST",
			"condition":              condition,
			"approximateInvokeCount": attempts,
		},
		"requestPayload":  rawJSONOrString(payload),
		"responseContext": responseContext,
		"responsePayload": rawJSONOrString(outcome.responsePayload),
	}
	return record
}

// rawJSONOrString renders a payload as the JSON value it is, falling back to a
// string when it is not JSON at all.
//
// AWS's record carries requestPayload and responsePayload as values, not as
// escaped strings — an event of `{"orderId":1}` appears as an object. A handler
// is free to return something that is not JSON, and quoting that is better than
// producing a record that will not parse.
func rawJSONOrString(payload []byte) any {
	if len(payload) == 0 {
		return nil
	}
	if json.Valid(payload) {
		return json.RawMessage(payload)
	}
	return string(payload)
}
