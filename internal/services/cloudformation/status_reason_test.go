package cloudformation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newErrorRec builds the response a dispatch helper hands back for a refusal,
// carrying the request-id header the emulator's protocol writers set.
func newErrorRec(code int, header, requestID, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rec.Code = code
	if header != "" {
		rec.Header().Set(header, requestID)
	}
	rec.Body.WriteString(body)
	return rec
}

// TestParseServiceError covers every error envelope internal/protocol writes,
// because a reason that cannot read the code and message falls back to pasting
// the wire body — the behaviour this file exists to replace.
//
// The four shapes are S3's bare <Error>, the Query protocol's
// <ErrorResponse><Error>, EC2's <Response><Errors><Error>, and the JSON
// protocols' {"__type", "message"}. The XML three nest Code and Message at
// three different depths, which is why they are parsed by a token walk rather
// than by a struct.
func TestParseServiceError(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantCode    string
		wantMessage string
	}{
		{
			name: "S3 REST XML",
			body: `<?xml version="1.0" encoding="UTF-8"?>` +
				`<Error><Code>MalformedXML</Code><Message>The XML you provided was not well-formed` +
				`</Message><RequestId>req-1</RequestId></Error>`,
			wantCode: "MalformedXML", wantMessage: "The XML you provided was not well-formed",
		},
		{
			name: "Query XML",
			body: `<?xml version="1.0" encoding="UTF-8"?><ErrorResponse><Error><Type>Sender</Type>` +
				`<Code>ValidationError</Code><Message>AutoScalingGroup asg not found</Message>` +
				`</Error><RequestId>req-2</RequestId></ErrorResponse>`,
			wantCode: "ValidationError", wantMessage: "AutoScalingGroup asg not found",
		},
		{
			name: "EC2 Query XML",
			body: `<?xml version="1.0" encoding="UTF-8"?><Response><Errors><Error>` +
				`<Code>DependencyViolation</Code><Message>has dependencies and cannot be deleted` +
				`</Message></Error></Errors><RequestID>req-3</RequestID></Response>`,
			wantCode: "DependencyViolation", wantMessage: "has dependencies and cannot be deleted",
		},
		{
			name:     "JSON",
			body:     `{"__type":"ResourceNotFoundException","message":"Function not found: fn"}`,
			wantCode: "ResourceNotFoundException", wantMessage: "Function not found: fn",
		},
		{
			// AWS namespaces __type on several services and the SDKs take the
			// part after the '#'. teardown_absent_resource_test.go already
			// depends on that spelling reaching the absence classifier.
			name:     "JSON with a namespaced __type",
			body:     `{"__type":"com.amazonaws.glue#EntityNotFoundException","message":"Database not found"}`,
			wantCode: "EntityNotFoundException", wantMessage: "Database not found",
		},
		{
			// XML escapes an apostrophe, and AWS messages use them freely.
			// A substring read of the body would report the escape; a parse
			// reports what the service wrote.
			name: "XML unescapes the message",
			body: `<Error><Code>InvalidParameterValue</Code>` +
				`<Message>The queue &#39;q&#39; already exists</Message></Error>`,
			wantCode: "InvalidParameterValue", wantMessage: "The queue 'q' already exists",
		},
		{name: "a body that is neither protocol", body: "upstream proxy failure"},
		{name: "an empty body", body: ""},
		{name: "truncated XML", body: `<Error><Code>Partial`},
		{name: "malformed JSON", body: `{"__type":`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the body is read for its code and message.
			code, message := parseServiceError(tc.body)

			// Then: both come back as the service wrote them, or both empty for
			// a body that carries neither.
			if code != tc.wantCode || message != tc.wantMessage {
				t.Errorf("parseServiceError(%q) = (%q, %q), want (%q, %q)",
					tc.body, code, message, tc.wantCode, tc.wantMessage)
			}
		})
	}
}

// TestStatusError_reasonShape pins the sentence a failed dispatch becomes.
// Real CloudFormation renders a resource provider's failure as
// `Resource handler returned message: "<the service's message> (Service: …,
// Status Code: …, Error Code: …, Request ID: …)" (RequestToken: …,
// HandlerErrorCode: …)`, and nothing in the emulator emitted that shape before
// status_reason.go.
func TestStatusError_reasonShape(t *testing.T) {
	// Given: DynamoDB refusing a CreateTable, answered under a known request ID
	// inside a stack operation with a known token.
	rec := newErrorRec(http.StatusBadRequest, "x-amzn-requestid", "req-abc",
		`{"__type":"ValidationException","message":"One or more parameter values were invalid"}`)
	ctx := withOperationToken(context.Background(), "token-1")

	// When: the dispatch's failure is turned into an error.
	err := statusError(ctx, "dynamodb", rec)

	// Then: it renders as CloudFormation's own sentence.
	want := `Resource handler returned message: "One or more parameter values were invalid ` +
		`(Service: DynamoDB, Status Code: 400, Error Code: ValidationException, Request ID: req-abc)" ` +
		`(RequestToken: token-1, HandlerErrorCode: InvalidRequest)`
	if err == nil || err.Error() != want {
		t.Fatalf("statusError = %v,\nwant %s", err, want)
	}
}

// TestStatusError_optionalFields covers the parts Overcast cannot always
// supply. A field it does not have is left out rather than filled with a
// placeholder, and a body it cannot parse at all falls back to exactly what
// this package produced before the shape existed — so an unrecognised envelope
// is never rendered as less than it used to be.
func TestStatusError_optionalFields(t *testing.T) {
	cases := []struct {
		name    string
		service string
		token   string
		header  string
		code    int
		body    string
		want    string
	}{
		{
			name: "an unclassifiable dispatch names no service",
			code: http.StatusBadRequest, header: "x-amz-request-id",
			body: `<Error><Code>InvalidRequest</Code><Message>nope</Message></Error>`,
			want: `Resource handler returned message: "nope (Status Code: 400, ` +
				`Error Code: InvalidRequest, Request ID: req-x)" (HandlerErrorCode: InvalidRequest)`,
		},
		{
			name:    "a response with no request-id header omits the field",
			service: "sqs", token: "token-2", code: http.StatusBadRequest,
			body: `{"__type":"InvalidParameterValue","message":"bad"}`,
			want: `Resource handler returned message: "bad (Service: SQS, Status Code: 400, ` +
				`Error Code: InvalidParameterValue)" (RequestToken: token-2, HandlerErrorCode: InvalidRequest)`,
		},
		{
			name:    "a body neither protocol can read falls back to the raw body",
			service: "s3", token: "token-3", code: http.StatusBadGateway,
			body: "upstream proxy failure",
			want: "HTTP 502: upstream proxy failure",
		},
		{
			// A service key the pinned models do not carry is printed as it
			// stands rather than given an invented display name.
			name:    "a service key the models do not carry is printed as it is",
			service: "overcast-only", code: http.StatusNotImplemented,
			body: `{"__type":"NotImplemented","message":"not implemented"}`,
			want: `Resource handler returned message: "not implemented (Service: overcast-only, ` +
				`Status Code: 501, Error Code: NotImplemented)" (HandlerErrorCode: GeneralServiceException)`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a refusal with only some of the fields available.
			rec := newErrorRec(tc.code, tc.header, "req-x", tc.body)
			ctx := withOperationToken(context.Background(), tc.token)

			// When + Then: the reason names what is there and nothing else.
			err := statusError(ctx, tc.service, rec)
			if err == nil || err.Error() != tc.want {
				t.Errorf("statusError = %v,\nwant %s", err, tc.want)
			}
		})
	}
}

// TestStatusError_successIsNotAnError keeps the contract every dispatch helper
// has always had: only a >= 400 response is a failure.
func TestStatusError_successIsNotAnError(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusFound} {
		if err := statusError(context.Background(), "s3", newErrorRec(code, "", "", "")); err != nil {
			t.Errorf("statusError for HTTP %d = %v, want nil", code, err)
		}
	}
	if err := statusError(context.Background(), "s3", nil); err != nil {
		t.Errorf("statusError for a dispatch that never ran = %v, want nil", err)
	}
}

// TestHandlerErrorCode covers the classification CloudFormation's resource
// providers return on their ProgressEvent. Only members a service call can
// defensibly produce are mapped; see handlerErrorCode for what is deliberately
// left unmapped and which two branches are least certain.
func TestHandlerErrorCode(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
		want   string
	}{
		{"a 404", http.StatusNotFound, "NotFoundException", "NotFound"},
		{"absence named under a 400", http.StatusBadRequest, "DBSubnetGroupNotFoundFault", "NotFound"},
		{"SQS's dotted absence code", http.StatusBadRequest, "AWS.SimpleQueueService.NonExistentQueue", "NotFound"},
		{"IAM's absence code", http.StatusNotFound, "NoSuchEntity", "NotFound"},
		{"a 403", http.StatusForbidden, "AccessDenied", "AccessDenied"},
		{"an access denial under a 400", http.StatusBadRequest, "AccessDeniedException", "AccessDenied"},
		{"a name already taken", http.StatusConflict, "BucketAlreadyExists", "AlreadyExists"},
		{"S3's owned-by-you variant", http.StatusConflict, "BucketAlreadyOwnedByYou", "AlreadyExists"},
		{"a quota", http.StatusBadRequest, "LimitExceededException", "ServiceLimitExceeded"},
		{"a throttle", http.StatusTooManyRequests, "ThrottlingException", "Throttling"},
		{"S3's throttle", http.StatusServiceUnavailable, "SlowDown", "Throttling"},
		{"EC2's standing dependency", http.StatusBadRequest, "DependencyViolation", "ResourceConflict"},
		{"IAM's standing dependency", http.StatusConflict, "DeleteConflict", "ResourceConflict"},
		{"a plain validation failure", http.StatusBadRequest, "ValidationException", "InvalidRequest"},
		{"an unimplemented operation", http.StatusNotImplemented, "NotImplemented", "GeneralServiceException"},
		{"a service failure", http.StatusInternalServerError, "InternalError", "ServiceInternalError"},
		{"a refusal with no code at all", http.StatusBadRequest, "", "InvalidRequest"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a failure with a status and an AWS error code.
			err := &serviceCallError{StatusCode: tc.status, Code: tc.code}

			// When + Then: it classifies to the handler error code that describes it.
			if got := err.handlerErrorCode(); got != tc.want {
				t.Errorf("handlerErrorCode for (%d, %q) = %q, want %q", tc.status, tc.code, got, tc.want)
			}
		})
	}
}

// TestServiceRequestID covers both header spellings, because which one a
// response carries depends on which protocol writer answered: REST XML sets
// x-amz-request-id and the JSON, Query and EC2 envelopes set x-amzn-requestid.
func TestServiceRequestID(t *testing.T) {
	for _, header := range []string{"x-amzn-requestid", "x-amz-request-id"} {
		t.Run(header, func(t *testing.T) {
			rec := newErrorRec(http.StatusBadRequest, header, "req-9", "")
			if got := serviceRequestID(rec); got != "req-9" {
				t.Errorf("serviceRequestID = %q, want %q", got, "req-9")
			}
		})
	}
	if got := serviceRequestID(newErrorRec(http.StatusBadRequest, "", "", "")); got != "" {
		t.Errorf("serviceRequestID for a response carrying neither header = %q, want empty", got)
	}
}

// TestSDKServiceName checks the name that reaches the reason comes from the
// pinned AWS models rather than from a table maintained here.
func TestSDKServiceName(t *testing.T) {
	cases := map[string]string{
		"s3":            "S3",
		"dynamodb":      "DynamoDB",
		"sqs":           "SQS",
		"lambda":        "Lambda",
		"iam":           "IAM",
		"ec2":           "EC2",
		"":              "",
		"not-a-service": "not-a-service",
	}
	for key, want := range cases {
		if got := sdkServiceName(key); got != want {
			t.Errorf("sdkServiceName(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestOperationToken pins the plumbing the RequestToken half of the reason
// depends on: a dispatch outside a stack operation names no token rather than
// inventing one.
func TestOperationToken(t *testing.T) {
	if got := operationToken(context.Background()); got != "" {
		t.Errorf("operationToken outside a stack operation = %q, want empty", got)
	}
	ctx := withOperationToken(context.Background(), "token-4")
	if got := operationToken(ctx); got != "token-4" {
		t.Errorf("operationToken = %q, want %q", got, "token-4")
	}
	// An empty token leaves the context alone rather than storing one.
	if got := operationToken(withOperationToken(ctx, "")); got != "token-4" {
		t.Errorf("operationToken after an empty token = %q, want the original %q", got, "token-4")
	}
}

// TestStatusError_isInspectableByCallers covers why the failure is a struct
// rather than a formatted string. isIAMNoSuchEntity used to grep the rendered
// text for "HTTP 404" and "<Code>NoSuchEntity</Code>", a match that would have
// gone quietly false the moment the reason changed shape.
func TestStatusError_isInspectableByCallers(t *testing.T) {
	rec := newErrorRec(http.StatusNotFound, "x-amzn-requestid", "req-5",
		`<ErrorResponse><Error><Code>NoSuchEntity</Code><Message>gone</Message></Error></ErrorResponse>`)
	err := statusError(context.Background(), "iam", rec)
	if !isIAMNoSuchEntity(err) {
		t.Errorf("isIAMNoSuchEntity(%v) = false, want true", err)
	}

	other := statusError(context.Background(), "iam", newErrorRec(http.StatusConflict, "", "",
		`<ErrorResponse><Error><Code>DeleteConflict</Code><Message>still attached</Message></Error></ErrorResponse>`))
	if isIAMNoSuchEntity(other) {
		t.Errorf("isIAMNoSuchEntity(%v) = true, want false — only absence may be treated as gone", other)
	}
	if isIAMNoSuchEntity(nil) {
		t.Error("isIAMNoSuchEntity(nil) = true, want false")
	}
}

// TestTeardownError_wrapsTheShape checks the reason survives the operation name
// every Delete puts in front of it — teardownError is the one place that adds
// to a dispatch failure rather than replacing it.
func TestTeardownError_wrapsTheShape(t *testing.T) {
	rec := newErrorRec(http.StatusBadRequest, "x-amzn-requestid", "req-6",
		`<Response><Errors><Error><Code>DependencyViolation</Code>`+
			`<Message>the internetGateway has dependencies</Message></Error></Errors></Response>`)
	ctx := withOperationToken(context.Background(), "token-7")
	err := teardownError("DeleteInternetGateway", rec, statusError(ctx, "ec2", rec))
	if err == nil {
		t.Fatal("teardownError = nil, want the refusal reported")
	}
	for _, want := range []string{
		"DeleteInternetGateway",
		`Resource handler returned message: "the internetGateway has dependencies (Service: EC2, `,
		"Status Code: 400, Error Code: DependencyViolation, Request ID: req-6)",
		"(RequestToken: token-7, HandlerErrorCode: ResourceConflict)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("teardown error %q does not contain %q", err, want)
		}
	}
}
