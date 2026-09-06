package sqs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
)

func TestWithLegacyQueryError_translatesKnownCode(t *testing.T) {
	in := &protocol.AWSError{Code: "QueueNameExists", Message: "boom", HTTPStatus: http.StatusBadRequest}
	out := withLegacyQueryError(in)

	if out.QueryErrorCode != "QueueAlreadyExists" {
		t.Errorf("QueryErrorCode = %q, want %q", out.QueryErrorCode, "QueueAlreadyExists")
	}
	// The original value is untouched — withLegacyQueryError must not mutate
	// its argument, since callers (e.g. errInvalidAction) may hold onto it.
	if in.QueryErrorCode != "" {
		t.Errorf("input mutated: QueryErrorCode = %q, want empty", in.QueryErrorCode)
	}
	// Everything else about the error is preserved.
	if out.Code != in.Code || out.Message != in.Message || out.HTTPStatus != in.HTTPStatus {
		t.Errorf("withLegacyQueryError changed a field other than QueryErrorCode: %+v", out)
	}
}

func TestWithLegacyQueryError_fallsBackToCodeItself(t *testing.T) {
	in := &protocol.AWSError{Code: "InvalidParameterValue", Message: "boom", HTTPStatus: http.StatusBadRequest}
	out := withLegacyQueryError(in)

	if out.QueryErrorCode != "InvalidParameterValue" {
		t.Errorf("QueryErrorCode = %q, want %q", out.QueryErrorCode, "InvalidParameterValue")
	}
}

func TestWithLegacyQueryError_nilPassesThrough(t *testing.T) {
	if got := withLegacyQueryError(nil); got != nil {
		t.Errorf("withLegacyQueryError(nil) = %+v, want nil", got)
	}
}

// TestWriteJSONError_rendersHeader is the sqs-local writeJSONError wrapper's
// unit test: every legacy (non-typed) handler in this package calls this
// instead of protocol.WriteJSONError directly (see queryerror.go), and it
// must apply the same mapping withLegacyQueryError does.
func TestWriteJSONError_rendersHeader(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	writeJSONError(w, req, errQueueNotFound("no-such-queue"))

	const want = "AWS.SimpleQueueService.NonExistentQueue;Sender"
	if got := w.Result().Header.Get("x-amzn-query-error"); got != want {
		t.Errorf("x-amzn-query-error = %q, want %q", got, want)
	}
}

// TestSqsTypedOp_tagsReturnedError is sqsTypedOp's unit test: a typed
// operation function's returned error is tagged the same way, without the
// operation itself knowing anything about query-compatibility.
func TestSqsTypedOp_tagsReturnedError(t *testing.T) {
	type in struct{}
	type out struct{}

	wrapped := sqsTypedOp(func(context.Context, *in) (*out, *protocol.AWSError) {
		return nil, errQueueNameExists("VisibilityTimeout")
	})

	_, aerr := wrapped(context.Background(), &in{})
	if aerr == nil {
		t.Fatal("expected an error")
	}
	if aerr.QueryErrorCode != "QueueAlreadyExists" {
		t.Errorf("QueryErrorCode = %q, want %q", aerr.QueryErrorCode, "QueueAlreadyExists")
	}

	// A successful call passes its output straight through, untouched.
	wrappedOK := sqsTypedOp(func(context.Context, *in) (*out, *protocol.AWSError) {
		return &out{}, nil
	})
	o, aerr := wrappedOK(context.Background(), &in{})
	if aerr != nil {
		t.Errorf("unexpected error: %v", aerr)
	}
	if o == nil {
		t.Error("expected a non-nil output")
	}
}

// TestErrInvalidAction_carriesQueryErrorCode covers the one construction
// site that bypasses both boundary wrappers (service.go's
// serviceutil.WriteUnhandledOperation -> codec.WriteError path), so the
// mapping has to be applied inside errInvalidAction itself.
func TestErrInvalidAction_carriesQueryErrorCode(t *testing.T) {
	aerr := errInvalidAction("NoSuchAction")
	if aerr.QueryErrorCode != "InvalidAction" {
		t.Errorf("QueryErrorCode = %q, want %q", aerr.QueryErrorCode, "InvalidAction")
	}
}
