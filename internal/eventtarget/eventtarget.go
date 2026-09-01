// Package eventtarget resolves and delivers event payloads to AWS target ARNs.
//
// It is the shared "fan a payload out to whatever the ARN names" seam used by
// EventBridge rule targets (issue #467) and, by design, by any other service
// that has to deliver to an arbitrary AWS target — EventBridge Pipes (#470) is
// the next intended consumer, which is why the classification and the
// dispatcher live here rather than inside internal/services/eventbridge.
//
// Delivery goes through the emulator's own root router rather than through
// per-service Go interfaces. That keeps one code path per sink (the same one
// an SDK client would take), so a target delivery observes exactly the
// validation, region routing and errors a real call would — in particular a
// missing function/queue/stream produces the service's own error instead of a
// silent no-op, which is what makes "dropped" and "dead-lettered" outcomes
// reportable at all.
//
// Nothing here blocks or does I/O at construction time: NewDispatcher only
// stores handles.
package eventtarget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/middleware"
)

// Kind is the AWS service segment of a target ARN, restricted to the target
// types this package can actually deliver to.
type Kind string

// The delivery-capable target kinds.
const (
	KindLambda        Kind = "lambda"
	KindSQS           Kind = "sqs"
	KindSNS           Kind = "sns"
	KindStepFunctions Kind = "states"
	KindKinesis       Kind = "kinesis"
	KindFirehose      Kind = "firehose"
	KindECS           Kind = "ecs"
	KindEventBus      Kind = "events"
)

// Lambda invocation types, as the Invoke API spells them.
const (
	// InvocationEvent is an asynchronous invoke. It is the zero value, and the
	// invocation type EventBridge rule targets use.
	InvocationEvent = ""
	// InvocationRequestResponse waits for the function's result, so a handled
	// function error becomes a delivery failure. EventBridge Pipes uses it.
	InvocationRequestResponse = "RequestResponse"
)

// maxBusHops caps how many event-bus deliveries one delivery may chain.
//
// A bus target whose own rules target another bus is a cycle. Real EventBridge
// tolerates one because delivery there is asynchronous; here every hop is a
// nested in-process call, so an unbounded cycle would exhaust the stack.
// Beyond the budget the delivery fails and the caller reports it, which is the
// same treatment any other undeliverable target gets.
const maxBusHops = 4

// ErrMalformedARN is returned by Classify for a value that is not an ARN at
// all. AWS performs this shape check synchronously when a target is added.
var ErrMalformedARN = errors.New("target ARN is malformed")

// UnsupportedKindError reports a well-formed ARN naming a service this
// emulator cannot deliver to. Callers must surface it rather than accepting
// the target and dropping it at delivery time — see
// docs/plans/full-emulation-priority.md §2.1.
type UnsupportedKindError struct {
	// Service is the ARN's service segment, e.g. "logs".
	Service string
}

func (e *UnsupportedKindError) Error() string {
	return "target service " + e.Service + " is not supported"
}

// displayNames maps each supported kind to the label AWS uses for it in the
// console and docs. The web UI renders these verbatim.
var displayNames = map[Kind]string{
	KindLambda:        "Lambda",
	KindSQS:           "SQS",
	KindSNS:           "SNS",
	KindStepFunctions: "Step Functions",
	KindKinesis:       "Kinesis",
	KindFirehose:      "Firehose",
	KindECS:           "ECS",
	KindEventBus:      "EventBridge event bus",
}

// DisplayName returns the human label for a kind ("Step Functions" for
// KindStepFunctions), or "Unknown" for a kind this package does not deliver to.
func DisplayName(k Kind) string {
	if name, ok := displayNames[k]; ok {
		return name
	}
	return "Unknown"
}

// Classify resolves a target ARN to the kind that can deliver to it.
//
// It returns ErrMalformedARN when arn is not ARN-shaped, and
// *UnsupportedKindError when the ARN is well formed but names a service this
// package has no sink for.
func Classify(arn string) (Kind, error) {
	svc, ok := arnService(arn)
	if !ok {
		return "", ErrMalformedARN
	}
	switch Kind(svc) {
	case KindLambda, KindSQS, KindSNS, KindStepFunctions, KindKinesis, KindFirehose, KindECS, KindEventBus:
		return Kind(svc), nil
	}
	return "", &UnsupportedKindError{Service: svc}
}

// arnService extracts the service segment of an ARN, validating the overall
// arn:partition:service:region:account:resource shape AWS requires. The region
// and account segments may be empty (S3 and IAM ARNs legitimately omit them),
// but the partition, service and resource segments may not.
func arnService(arn string) (string, bool) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 {
		return "", false
	}
	if parts[0] != "arn" || parts[1] == "" || parts[2] == "" || parts[5] == "" {
		return "", false
	}
	return parts[2], true
}

// ResourceName returns the trailing resource name of an ARN: the part after
// the last ":" or "/", whichever comes later. It covers every shape the
// supported sinks use — sqs:...:queue-name, lambda:...:function:name,
// kinesis:...:stream/name, firehose:...:deliverystream/name.
func ResourceName(arn string) string {
	name := arn
	if i := strings.LastIndexAny(name, ":/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// FunctionName returns the Lambda function name an ARN names, dropping any
// version or alias qualifier. A bare name is returned unchanged, matching the
// FunctionName forms the Lambda API accepts.
func FunctionName(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return arn
	}
	parts := strings.Split(arn, ":")
	// arn:aws:lambda:region:account:function:name[:qualifier]
	for i, p := range parts {
		if p == "function" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return parts[len(parts)-1]
}

// Request is one delivery: a payload bound for a single target ARN.
type Request struct {
	// ARN of the target. Required.
	ARN string
	// Kind the ARN resolved to. Callers that already classified the ARN pass
	// it here; a zero Kind is resolved by Deliver.
	Kind Kind
	// Payload is the fully transformed body to deliver. Sinks that carry
	// binary records (Kinesis, Firehose) receive these bytes verbatim.
	Payload []byte

	// PartitionKey is the Kinesis record partition key. Required by Kinesis;
	// ignored by every other kind.
	PartitionKey string
	// ExecutionName names the Step Functions execution. Optional — Step
	// Functions generates one when empty.
	ExecutionName string
	// MessageGroupID sets the SQS FIFO message group. Optional.
	MessageGroupID string
	// MessageDeduplicationID sets the SQS FIFO deduplication ID. Optional.
	MessageDeduplicationID string
	// Subject sets the SNS message subject. Optional.
	Subject string
	// MessageAttributes are the message attributes to send alongside the
	// payload. Honoured by the SQS and SNS sinks — the two kinds AWS models
	// message attributes on — and ignored by every other kind. Optional.
	MessageAttributes map[string]MessageAttribute

	// InvocationType selects how a Lambda target is invoked:
	// InvocationRequestResponse waits for the result and turns a function error
	// into a delivery failure, and the zero value invokes asynchronously.
	// Ignored by every other kind.
	InvocationType string

	// Source, DetailType and Resources populate the PutEvents entry an
	// EventBridge event-bus target receives. Ignored by every other kind.
	Source     string
	DetailType string
	Resources  []string
}

// MessageAttribute is one SQS or SNS message attribute. Both services model the
// same shape — a data type naming how the value should be read, and the value
// itself — and both spell the string form of it identically, so one type serves
// the two sinks.
//
// Only the string form is carried. AWS's Binary and *.custom data types exist,
// but no caller here produces one: an emulator sender that needs to say "this
// is a number" says so with DataType "Number" and a decimal StringValue,
// exactly as the SDKs do.
type MessageAttribute struct {
	// DataType is AWS's attribute data type — "String" or "Number".
	DataType string
	// StringValue is the attribute value.
	StringValue string
}

// sortedAttributeNames returns the attribute names in a stable order, so a
// delivery built from a map produces the same request every time. SQS's JSON
// body does not care, but SNS's Query form numbers its entries, and an
// unstable order there makes the request untestable and the wire log noise.
func sortedAttributeNames(attrs map[string]MessageAttribute) []string {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// busHopKey counts nested event-bus deliveries on a delivery's context.
type busHopKey struct{}

// busHops reports how many event-bus deliveries ctx is already nested inside.
func busHops(ctx context.Context) int {
	hops, _ := ctx.Value(busHopKey{}).(int)
	return hops
}

// Dispatcher delivers payloads to target ARNs through a root http.Handler.
//
// The zero value is not usable; construct one with NewDispatcher. A Dispatcher
// is safe for concurrent use — it holds no mutable state.
type Dispatcher struct {
	router        http.Handler
	defaultRegion string
}

// NewDispatcher returns a Dispatcher that delivers through router. router is
// the emulator's root handler; defaultRegion is used when neither the request
// context nor the target ARN names one.
func NewDispatcher(router http.Handler, defaultRegion string) *Dispatcher {
	return &Dispatcher{router: router, defaultRegion: defaultRegion}
}

// Deliver dispatches req to the sink its ARN names.
//
// It returns an error when the sink rejects the delivery (a missing function,
// queue or stream produces the service's own AWS error), when the target kind
// is not deliverable, or when the dispatcher has no router. Callers decide
// what a failure means — retry, dead-letter, or drop.
func (d *Dispatcher) Deliver(ctx context.Context, req Request) error {
	_, err := d.DeliverResponse(ctx, req)
	return err
}

// DeliverResponse is Deliver for a caller that needs what the sink answered,
// not only whether it accepted the delivery.
//
// Only a Lambda target invoked with InvocationRequestResponse has an answer to
// return: EventBridge Pipes reads it to honour a partial-batch failure report,
// which is a decision about the source records and so cannot be made inside the
// sink. Every other kind returns a nil payload and behaves exactly as Deliver.
func (d *Dispatcher) DeliverResponse(ctx context.Context, req Request) ([]byte, error) {
	if d == nil || d.router == nil {
		return nil, errors.New("eventtarget: no router configured")
	}
	kind := req.Kind
	if kind == "" {
		resolved, err := Classify(req.ARN)
		if err != nil {
			return nil, err
		}
		kind = resolved
	}
	switch kind {
	case KindLambda:
		return d.deliverLambda(ctx, req)
	case KindSQS:
		return nil, d.deliverSQS(ctx, req)
	case KindSNS:
		return nil, d.deliverSNS(ctx, req)
	case KindStepFunctions:
		return nil, d.deliverStepFunctions(ctx, req)
	case KindKinesis:
		return nil, d.deliverKinesis(ctx, req)
	case KindFirehose:
		return nil, d.deliverFirehose(ctx, req)
	case KindEventBus:
		return nil, d.deliverEventBus(ctx, req)
	case KindECS:
		// ECS RunTask has no generic payload shape: the caller owns the body,
		// built from its own target parameters. EventBridge dispatches it
		// through InvokeJSONTarget instead.
		return nil, errors.New("eventtarget: ECS targets must be delivered with InvokeJSONTarget")
	default:
		return nil, &UnsupportedKindError{Service: string(kind)}
	}
}

// deliverLambda invokes the function. The default is asynchronous, the
// invocation type real EventBridge uses for Lambda targets (the rule does not
// wait for a result); a caller that needs the result — EventBridge Pipes
// invokes its Lambda target with REQUEST_RESPONSE — asks for it on the Request.
//
// The response body is returned for that caller. An asynchronous invoke has
// none, so it comes back empty rather than as the 202's own body.
func (d *Dispatcher) deliverLambda(ctx context.Context, req Request) ([]byte, error) {
	name := FunctionName(req.ARN)
	path := "/2015-03-31/functions/" + url.PathEscape(name) + "/invocations"
	httpReq, err := d.newRequest(ctx, http.MethodPost, path, req.Payload)
	if err != nil {
		return nil, err
	}
	invocation := "Event"
	if req.InvocationType == InvocationRequestResponse {
		invocation = InvocationRequestResponse
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Amz-Invocation-Type", invocation)

	rec, err := d.send(httpReq)
	if err != nil {
		return nil, err
	}
	// A handled function error answers 200 and names itself in a header, so it
	// is invisible to the status check every other sink relies on.
	if fnErr := rec.Header().Get("X-Amz-Function-Error"); fnErr != "" {
		return nil, fmt.Errorf("lambda function error (%s): %s", fnErr, strings.TrimSpace(rec.Body.String()))
	}
	if invocation != InvocationRequestResponse {
		return nil, nil
	}
	return rec.Body.Bytes(), nil
}

func (d *Dispatcher) deliverSQS(ctx context.Context, req Request) error {
	body := map[string]any{
		"QueueUrl":    ResourceName(req.ARN),
		"MessageBody": string(req.Payload),
	}
	if req.MessageGroupID != "" {
		body["MessageGroupId"] = req.MessageGroupID
	}
	if req.MessageDeduplicationID != "" {
		body["MessageDeduplicationId"] = req.MessageDeduplicationID
	}
	if len(req.MessageAttributes) > 0 {
		attrs := make(map[string]any, len(req.MessageAttributes))
		for name, attr := range req.MessageAttributes {
			attrs[name] = map[string]any{"DataType": attr.DataType, "StringValue": attr.StringValue}
		}
		body["MessageAttributes"] = attrs
	}
	return d.InvokeJSONTarget(ctx, "AmazonSQS.SendMessage", body)
}

// deliverEventBus puts the payload on another EventBridge bus as one PutEvents
// entry, carrying the payload verbatim as the entry's Detail.
func (d *Dispatcher) deliverEventBus(ctx context.Context, req Request) error {
	if hops := busHops(ctx); hops >= maxBusHops {
		return fmt.Errorf("eventtarget: event-bus delivery exceeded the %d hop budget — the buses are forwarding in a cycle", maxBusHops)
	}
	entry := map[string]any{
		"EventBusName": ResourceName(req.ARN),
		"Source":       req.Source,
		"DetailType":   req.DetailType,
		"Detail":       string(req.Payload),
	}
	if len(req.Resources) > 0 {
		entry["Resources"] = req.Resources
	}
	ctx = context.WithValue(ctx, busHopKey{}, busHops(ctx)+1)
	return d.InvokeJSONTarget(ctx, "AWSEvents.PutEvents", map[string]any{"Entries": []any{entry}})
}

// deliverSNS publishes through the AWS Query protocol, the only wire format
// the SNS service exposes Publish on.
func (d *Dispatcher) deliverSNS(ctx context.Context, req Request) error {
	form := url.Values{
		"Action":   {"Publish"},
		"Version":  {"2010-03-31"},
		"TopicArn": {req.ARN},
		"Message":  {string(req.Payload)},
	}
	if req.Subject != "" {
		form.Set("Subject", req.Subject)
	}
	// SNS's Query protocol numbers attribute entries from 1.
	for i, name := range sortedAttributeNames(req.MessageAttributes) {
		prefix := fmt.Sprintf("MessageAttributes.entry.%d.", i+1)
		attr := req.MessageAttributes[name]
		form.Set(prefix+"Name", name)
		form.Set(prefix+"Value.DataType", attr.DataType)
		form.Set(prefix+"Value.StringValue", attr.StringValue)
	}
	httpReq, err := d.newRequest(ctx, http.MethodPost, "/", []byte(form.Encode()))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return d.do(httpReq)
}

func (d *Dispatcher) deliverStepFunctions(ctx context.Context, req Request) error {
	body := map[string]any{
		"stateMachineArn": req.ARN,
		"input":           string(req.Payload),
	}
	if req.ExecutionName != "" {
		body["name"] = req.ExecutionName
	}
	return d.InvokeJSONTarget(ctx, "AWSStepFunctions.StartExecution", body)
}

func (d *Dispatcher) deliverKinesis(ctx context.Context, req Request) error {
	return d.InvokeJSONTarget(ctx, "Kinesis_20131202.PutRecord", map[string]any{
		"StreamName":   ResourceName(req.ARN),
		"Data":         req.Payload,
		"PartitionKey": req.PartitionKey,
	})
}

func (d *Dispatcher) deliverFirehose(ctx context.Context, req Request) error {
	return d.InvokeJSONTarget(ctx, "Firehose_20150804.PutRecord", map[string]any{
		"DeliveryStreamName": ResourceName(req.ARN),
		"Record":             map[string]any{"Data": req.Payload},
	})
}

// InvokeJSONTarget performs an AWS JSON 1.1 X-Amz-Target call against the root
// router. It is the low-level escape hatch for target types whose request body
// the caller owns — EventBridge's scheduled ECS RunTask target builds its body
// from the rule's EcsParameters and dispatches it through here.
func (d *Dispatcher) InvokeJSONTarget(ctx context.Context, target string, body any) error {
	if d == nil || d.router == nil {
		return errors.New("eventtarget: no router configured")
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := d.newRequest(ctx, http.MethodPost, "/", data)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	return d.do(req)
}

// newRequest builds an in-process request carrying the delivery's region, so a
// target outside the default region resolves against its own regional state.
// Every delivery is built here, so this is also where the caller's routing
// state is left behind — see detachRouting.
func (d *Dispatcher) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(detachRouting(ctx), method, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Overcast-Region", middleware.RegionFromContext(ctx, d.defaultRegion))
	return req, nil
}

// detachRouting returns ctx with a fresh chi routing context, so re-entering
// the router cannot touch the routing state of the request that is still in
// flight.
//
// No caller owns the context it delivers on: EventBridge fans PutEvents out on
// r.Context(), and the events bus runs a Pipes worker on a pool goroutine
// holding the *publishing* request's context. chi reuses whatever *chi.Context
// it finds under chi.RouteCtxKey instead of allocating one, so a synthetic
// request built on an inherited context would route the delivery on the
// inbound request's own routing state — giving the sink that request's URL
// params, and letting two concurrent deliveries write to one *chi.Context at
// once, which is a data race rather than merely a wrong answer.
//
// Only the routing key is replaced. Everything else the context legitimately
// carries still applies, in particular the region newRequest propagates and
// the event-bus hop budget.
//
// CloudFormation avoids the same trap by provisioning on a fresh
// context.Background() (see internal/services/cloudformation/handler.go); this
// package cannot, because the region it must propagate rides on the caller's
// context.
func detachRouting(ctx context.Context) context.Context {
	return context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
}

// do runs the request against the root router and turns any non-2xx response
// into an error carrying the sink's own message.
func (d *Dispatcher) do(req *http.Request) error {
	_, err := d.send(req)
	return err
}

// send runs the request against the root router and returns the recorded
// response. A non-2xx status is an error carrying the sink's own message; the
// recorder is returned as well for the one sink (Lambda) that reports failure
// in a header rather than the status.
func (d *Dispatcher) send(req *http.Request) (*httptest.ResponseRecorder, error) {
	rec := httptest.NewRecorder()
	d.router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return rec, fmt.Errorf("HTTP %d: %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	return rec, nil
}
