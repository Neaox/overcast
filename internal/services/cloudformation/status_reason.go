package cloudformation

// status_reason.go — the reason a failed resource operation shows the user.
//
// A resource handler provisions by dispatching to the emulated service through
// the router, and a service that refuses answers with an ordinary AWS error
// body. Until this file existed, that body went into ResourceStatusReason
// verbatim, so `cdk deploy` printed raw XML or JSON at the operator:
//
//	s3 CreateBucket: HTTP 400: <?xml version="1.0" encoding="UTF-8"?>
//	<Error><Code>…</Code><Message>…</Message><RequestId>…</RequestId></Error>
//
// Real CloudFormation never shows the wire body. Its resource providers report
// a failure through the CloudFormation Registry's ProgressEvent, and the
// service renders that into one sentence:
//
//	Resource handler returned message: "<the service's message> (Service: S3,
//	Status Code: 400, Error Code: InvalidBucketName, Request ID: …,
//	Extended Request ID: …)" (RequestToken: …, HandlerErrorCode: InvalidRequest)
//
// The inner quoted half is the AWS SDK's own rendering of the service
// exception the provider caught; the outer half is CloudFormation's, naming
// the operation token and the provider's classification of the failure. This
// file reproduces that shape from what an internal dispatch actually knows,
// and states in comments where it cannot.
//
// # What Overcast can and cannot fill in
//
//   - Service, status code, error code and message come from the dispatch
//     itself: internalCall already resolves the service for its trace hop, and
//     the four error envelopes the emulator writes (see internal/protocol) all
//     carry a code and a message.
//   - Request ID is real. Every response the emulator writes carries the
//     minted request ID in a header, so the value here is the one the trace UI
//     files that call under, not a decoration.
//   - Extended Request ID is omitted. It is S3's x-amz-id-2, which Overcast
//     mints for no response, and a fabricated one would be a correlation
//     handle that correlates with nothing. Unverified against real AWS:
//     whether real CloudFormation prints the field with a null value for a
//     service that has no extended request ID, or omits it as this does.
//   - RequestToken is the stack operation's ClientRequestToken, which is the
//     closest thing Overcast has and is stamped on every stack event of the
//     same operation, so a reader can get from the reason back to the event
//     stream and the request behind it. Unverified against real AWS: real
//     CloudFormation's RequestToken here is the per-resource provider
//     invocation token, a different value from the caller's ClientRequestToken
//     — Overcast has no second token to offer and mints none, because a random
//     UUID that appears nowhere else would be worse than an honest reuse.
//   - HandlerErrorCode is derived — see handlerErrorCode.
//
// # Why the reason is built here and not where the status is recorded
//
// Every dispatch funnels through internalCall.do, and every resource handler
// wraps what it gets with its own operation name ("s3 PutBucketVersioning:
// %w"). Rendering here means one place produces the shape and every path that
// reports a failure — create, update, rollback, teardownError — gets it, with
// no per-handler change and no risk of a handler being missed. The cost is
// that the handler's prefix stays in front of the AWS-shaped sentence, which
// real CloudFormation does not print; it is kept because it names the
// operation that failed, which the AWS shape does not carry and which a
// teardown failure has needed since teardownError was written.

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/overcast-sh/overcast/internal/awsapi"
)

// serviceCallError is the error an internal dispatch returns for a response of
// 400 or worse. It is a struct rather than a formatted string so that a caller
// can ask about the failure instead of matching on its text — isIAMNoSuchEntity
// is the first caller to do so, having previously grepped for "HTTP 404" in the
// error message.
type serviceCallError struct {
	// Service is the AWS SDK service id ("S3", "Lambda"), empty when the
	// dispatch could not be classified.
	Service string
	// StatusCode is the HTTP status the emulated service answered with.
	StatusCode int
	// Code and Message are the AWS error code and message parsed out of the
	// response body, empty for a body that is neither XML nor JSON.
	Code    string
	Message string
	// RequestID is the request ID the emulated service answered under.
	RequestID string
	// RequestToken is the stack operation's ClientRequestToken, empty for a
	// dispatch made outside a stack operation.
	RequestToken string
	// Body is the raw response body, kept for the fallback rendering.
	Body string
}

// Error renders the failure the way CloudFormation renders one.
//
// A body that parsed as neither protocol falls back to exactly what this
// package produced before the shape existed, so an unrecognised envelope is
// never rendered as less than it used to be.
func (e *serviceCallError) Error() string {
	if e.Code == "" && e.Message == "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
	}

	// The message is what the SDK exception carried; a code with no message
	// stands in for one rather than leaving empty quotes.
	message := e.Message
	if message == "" {
		message = e.Code
	}

	inner := make([]string, 0, 4)
	if e.Service != "" {
		inner = append(inner, "Service: "+e.Service)
	}
	inner = append(inner, "Status Code: "+strconv.Itoa(e.StatusCode))
	if e.Code != "" {
		inner = append(inner, "Error Code: "+e.Code)
	}
	if e.RequestID != "" {
		inner = append(inner, "Request ID: "+e.RequestID)
	}

	outer := make([]string, 0, 2)
	if e.RequestToken != "" {
		outer = append(outer, "RequestToken: "+e.RequestToken)
	}
	outer = append(outer, "HandlerErrorCode: "+e.handlerErrorCode())

	return `Resource handler returned message: "` + message +
		" (" + strings.Join(inner, ", ") + `)" (` + strings.Join(outer, ", ") + ")"
}

// handlerErrorCode classifies the failure into the enum a CloudFormation
// resource provider returns on its ProgressEvent.
//
// The enum is fixed by the CloudFormation CLI, and only the entries a service
// call can defensibly produce are mapped here: NotUpdatable, NotStabilized,
// InvalidCredentials, NetworkFailure, InvalidTypeConfiguration, NonCompliant
// and the handler-internal ones describe conditions that either belong to the
// provider framework rather than to the service, or that Overcast reaches by
// another path entirely (a stabilizer failure is not a dispatch failure).
// Nothing here invents a mapping for them.
//
// The absence test is absentResourcePhrases — the same list resourceAlreadyGone
// uses to decide a teardown succeeded, because "the resource is not there" is
// one question and AWS spells its answer the same handful of ways for both.
//
// Unverified against real AWS: which HandlerErrorCode real CloudFormation
// picks for any specific service error. The mapping below is derived from what
// the enum members mean, not from observed CloudFormation output; the branch
// most likely to be wrong says so where it is made.
func (e *serviceCallError) handlerErrorCode() string {
	code := lettersLower(e.Code)
	switch {
	case e.StatusCode == http.StatusNotFound || namesAbsence(code):
		return "NotFound"
	case e.StatusCode == http.StatusForbidden ||
		strings.Contains(code, "accessdenied") || strings.Contains(code, "unauthorized"):
		return "AccessDenied"
	case strings.Contains(code, "alreadyexists") || strings.Contains(code, "alreadyowned"):
		return "AlreadyExists"
	case strings.Contains(code, "limitexceeded") || strings.Contains(code, "quotaexceeded"):
		return "ServiceLimitExceeded"
	case e.StatusCode == http.StatusTooManyRequests ||
		strings.Contains(code, "throttl") || strings.Contains(code, "slowdown"):
		return "Throttling"
	// Unverified against real AWS: DynamoDB's ResourceInUseException is how it
	// reports a table that already exists as well as one that is busy, so this
	// branch may be reporting some already-exists failures as ResourceConflict.
	// The code alone cannot separate the two, and guessing from the message
	// would be a heuristic on prose AWS is free to reword.
	case e.StatusCode == http.StatusConflict ||
		strings.Contains(code, "conflict") || strings.Contains(code, "inuse") ||
		strings.Contains(code, "dependencyviolation"):
		return "ResourceConflict"
	// 501 is the emulator saying it does not implement the operation, which is
	// a condition real CloudFormation never meets. GeneralServiceException is
	// the enum's catch-all for "the service failed and none of the specific
	// members fit", which is exactly what this is.
	case e.StatusCode == http.StatusNotImplemented:
		return "GeneralServiceException"
	case e.StatusCode >= 500:
		// ServiceInternalError, not InternalFailure: the downstream service
		// failed, not the resource handler around it.
		return "ServiceInternalError"
	case e.StatusCode >= 400:
		return "InvalidRequest"
	default:
		return "GeneralServiceException"
	}
}

// namesAbsence reports whether a letters-only string names a resource that is
// not there. Shared with resourceAlreadyGone, which asks the same question of a
// whole response body.
func namesAbsence(lettersOnly string) bool {
	for _, phrase := range absentResourcePhrases {
		if strings.Contains(lettersOnly, phrase) {
			return true
		}
	}
	return false
}

// statusError maps a >= 400 internal response to the error every dispatch
// helper here has always returned for one — now carrying the fields the reason
// is rendered from rather than the raw body.
//
// service is the Overcast service key the dispatch reached, "" when it could
// not be classified; ctx supplies the stack operation's token when the call is
// part of one.
func statusError(ctx context.Context, service string, rec *httptest.ResponseRecorder) error {
	if rec == nil || rec.Code < 400 {
		return nil
	}
	body := rec.Body.String()
	code, message := parseServiceError(body)
	return &serviceCallError{
		Service:      sdkServiceName(service),
		StatusCode:   rec.Code,
		Code:         code,
		Message:      message,
		RequestID:    serviceRequestID(rec),
		RequestToken: operationToken(ctx),
		Body:         body,
	}
}

// serviceRequestID reads the request ID off a dispatched response. The two
// header spellings are the two internal/protocol writes: REST XML answers
// x-amz-request-id, and the JSON, Query and EC2 envelopes answer
// x-amzn-requestid.
func serviceRequestID(rec *httptest.ResponseRecorder) string {
	if rec == nil {
		return ""
	}
	if id := rec.Header().Get("x-amzn-requestid"); id != "" {
		return id
	}
	return rec.Header().Get("x-amz-request-id")
}

// parseServiceError reads the AWS error code and message out of a response
// body, across every protocol the provisioner dispatches over. It returns two
// empty strings for anything it cannot read, which is the caller's signal to
// fall back to the raw body.
//
// resourceAlreadyGone and ec2DependencyViolation scan the same bodies without
// parsing them, and deliberately so: their question is whether a phrase appears
// anywhere, and an error body holds nothing but the code and the message, so a
// scan already looks at exactly the two fields a parser would. That reasoning
// does not carry over to reading a field's *value* back out, which is what a
// rendered reason needs — hence a parse here rather than a third scan.
func parseServiceError(body string) (code, message string) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", ""
	}
	switch trimmed[0] {
	case '{':
		return parseJSONServiceError(trimmed)
	case '<':
		return parseXMLServiceError(trimmed)
	default:
		return "", ""
	}
}

// parseJSONServiceError reads the JSON envelope internal/protocol writes for
// the JSON protocols: {"__type": <code>, "message": <message>}.
//
// __type may be namespaced ("com.amazonaws.glue#EntityNotFoundException"), as
// several real services spell it and as teardown's own fixtures already cover;
// AWS SDKs take the part after the '#' and so does this. The alternative key
// spellings are the ones REST-JSON services vary over — a body that carries
// none of them yields an empty code, and the raw-body fallback takes over.
func parseJSONServiceError(body string) (code, message string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		return "", ""
	}
	code = jsonStringField(fields, "__type", "code", "Code")
	if idx := strings.LastIndex(code, "#"); idx >= 0 {
		code = code[idx+1:]
	}
	return code, jsonStringField(fields, "message", "Message")
}

// jsonStringField returns the first of names that is present and holds a
// string.
func jsonStringField(fields map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

// parseXMLServiceError reads the first Code and Message elements out of an XML
// error body, at whatever depth they sit.
//
// The three XML envelopes internal/protocol writes nest them differently — S3's
// bare <Error>, the Query protocol's <ErrorResponse><Error>, and EC2's
// <Response><Errors><Error> — so a struct would need three of them and a way to
// choose. A token walk reads all three, and reads them through encoding/xml
// rather than by substring so that a message containing an escaped character
// (an apostrophe, which AWS messages use freely) comes back as the text the
// service wrote rather than as &#39;.
//
// A body that does not parse all the way to the end yields nothing, even if a
// Code was read before the break. Half a document is not evidence of what the
// service said, and the raw-body fallback shows the operator the whole of it
// rather than a fragment presented as the answer.
func parseXMLServiceError(body string) (code, message string) {
	decoder := xml.NewDecoder(strings.NewReader(body))
	var into *string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", ""
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch {
			case element.Name.Local == "Code" && code == "":
				into = &code
			case element.Name.Local == "Message" && message == "":
				into = &message
			default:
				into = nil
			}
		case xml.CharData:
			if into != nil {
				*into += string(element)
			}
		case xml.EndElement:
			into = nil
		}
	}
	return strings.TrimSpace(code), strings.TrimSpace(message)
}

// ── The operation's token ──────────────────────────────────────────────────

// operationTokenKey is the context key the stack operation's ClientRequestToken
// travels under.
type operationTokenKey struct{}

// withOperationToken carries the token of the stack operation in flight, so a
// dispatch several layers below can name it in a failure reason.
//
// It goes in the context rather than through the dispatch helpers for the same
// reason the limitation collector does (see limitation.go): every resource
// handler already dispatches through one function, and none of them should have
// to know this exists. Threading it as a parameter would have meant touching
// every internalQuery/internalJSON/internalRequest call site in the package —
// several hundred — to carry a value only the failure path reads.
func withOperationToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, operationTokenKey{}, token)
}

// operationToken returns the token of the stack operation this context belongs
// to, or "" outside one.
func operationToken(ctx context.Context) string {
	token, _ := ctx.Value(operationTokenKey{}).(string)
	return token
}

// ── The service's name ─────────────────────────────────────────────────────

// sdkServiceName renders an Overcast service key the way an AWS SDK names the
// service in an exception message: the model's sdkId with spaces removed
// ("api-gateway" → "APIGateway"), which is how the SDKs derive their own
// service-name constants.
//
// A key the pinned models do not carry is printed as it stands. That is
// visible and honest; inventing a display name for it would not be.
//
// Unverified against real AWS: the exact casing CloudFormation prints. The
// Java SDK renders some sdkIds in Pascal case rather than verbatim ("DynamoDB"
// → "DynamoDb"), and which spelling reaches ResourceStatusReason has not been
// checked against a real deploy. The sdkId is used because it is the identity
// the pinned model actually states, rather than a casing rule guessed at here.
func sdkServiceName(key string) string {
	if key == "" {
		return ""
	}
	if name, ok := sdkServiceNames()[key]; ok {
		return name
	}
	return key
}

// sdkServiceNames indexes Overcast service key → SDK service name, built once
// on first use.
//
// awsapi.WalkOperations warns against scanning the corpus on a request path;
// this is not one. It runs at most once per process, and only after a stack
// resource has already failed — the alternative was a hand-written table of
// display names, which is the kind of second copy of the model this repository
// keeps deleting. The first modeled identity for a key wins, which is
// deterministic because the manifest is generated in service order.
var sdkServiceNames = sync.OnceValue(func() map[string]string {
	names := make(map[string]string)
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		key := awsapi.ServiceKey(op.Service)
		if _, seen := names[key]; !seen {
			names[key] = strings.ReplaceAll(op.SDKID, " ", "")
		}
		return true
	})
	return names
})
