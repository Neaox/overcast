package protocol_test

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/your-org/overcast/internal/protocol"
)

// TestWriteXMLError_structuredResponse verifies the XML error envelope is correct.
func TestWriteXMLError_structuredResponse(t *testing.T) {
	// Given: a handler that writes an XML error
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "NoSuchBucket",
			Message:    "The bucket does not exist",
			HTTPStatus: http.StatusNotFound,
		})
	})

	// When: we make a request
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := protocol.ContextWithRequestID(req.Context(), "test-request-id")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	resp := w.Result()

	// Then: status, Content-Type, request ID, and body are correct
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: expected 404, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/xml" {
		t.Errorf("Content-Type: expected application/xml, got %q", ct)
	}
	if rid := resp.Header.Get("x-amz-request-id"); rid != "test-request-id" {
		t.Errorf("x-amz-request-id: expected test-request-id, got %q", rid)
	}

	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("failed to parse XML error: %v\nbody: %s", err, body)
	}
	if errResp.Code != "NoSuchBucket" {
		t.Errorf("Code: expected NoSuchBucket, got %q", errResp.Code)
	}
}

// TestWriteJSONError_structuredResponse verifies the JSON error envelope is correct.
func TestWriteJSONError_structuredResponse(t *testing.T) {
	// Given: a handler that writes a JSON error
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "QueueDoesNotExist",
			Message:    "The queue does not exist",
			HTTPStatus: http.StatusBadRequest,
		})
	})

	// When: we make a request
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	ctx := protocol.ContextWithRequestID(req.Context(), "json-request-id")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	resp := w.Result()

	// Then: the JSON body has __type and message fields
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: expected 400, got %d", resp.StatusCode)
	}
	if rid := resp.Header.Get("x-amzn-requestid"); rid != "json-request-id" {
		t.Errorf("x-amzn-requestid: expected json-request-id, got %q", rid)
	}

	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("failed to parse JSON error: %v\nbody: %s", err, body)
	}
	if errResp.Type != "QueueDoesNotExist" {
		t.Errorf("__type: expected QueueDoesNotExist, got %q", errResp.Type)
	}
}

// TestNotImplemented_setsUnsupportedHeader verifies the 501 sentinel header.
func TestNotImplemented_setsUnsupportedHeader(t *testing.T) {
	// Given: a handler that returns NotImplemented
	handler := http.HandlerFunc(protocol.NotImplementedJSON)

	// When: we call it
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(protocol.ContextWithRequestID(req.Context(), "req-1"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	resp := w.Result()

	// Then: status is 501 and x-emulator-unsupported is true
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("x-emulator-unsupported"); got != "true" {
		t.Errorf("expected x-emulator-unsupported: true, got %q", got)
	}
}

// TestRequestID_generatesUniqueIDs verifies NewRequestID produces unique values.
func TestRequestID_generatesUniqueIDs(t *testing.T) {
	// Given / When: we generate multiple request IDs
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := protocol.NewRequestID()
		if id == "" {
			t.Fatal("NewRequestID returned empty string")
		}
		if ids[id] {
			t.Fatalf("NewRequestID generated duplicate ID: %q", id)
		}
		ids[id] = true
	}
	// Then: all 100 IDs are unique (verified by the map above)
}

// TestRequestIDContext_roundtrip verifies storing and retrieving request IDs.
func TestRequestIDContext_roundtrip(t *testing.T) {
	// Given: a context with a request ID
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := protocol.ContextWithRequestID(req.Context(), "my-id-123")

	// When: we retrieve it
	got := protocol.RequestIDFromContext(ctx)

	// Then: we get the same ID back
	if got != "my-id-123" {
		t.Errorf("expected my-id-123, got %q", got)
	}
}

// TestRequestIDContext_missingGeneratesNew verifies fallback behaviour.
func TestRequestIDContext_missingGeneratesNew(t *testing.T) {
	// Given: a context with no request ID
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// When: we retrieve the request ID
	got := protocol.RequestIDFromContext(req.Context())

	// Then: a non-empty ID is generated (not an error, not empty)
	if got == "" {
		t.Error("expected a generated request ID, got empty string")
	}
}

// TestARN_s3OmitsRegionAndAccount verifies S3 ARN format.
func TestARN_s3OmitsRegionAndAccount(t *testing.T) {
	// Given / When: we build an S3 ARN
	arn := protocol.ARN("us-east-1", "000000000000", "s3", "my-bucket")

	// Then: region and account are omitted (AWS spec)
	expected := "arn:aws:s3:::my-bucket"
	if arn != expected {
		t.Errorf("S3 ARN: expected %q, got %q", expected, arn)
	}
}

// TestARN_sqsIncludesRegionAndAccount verifies SQS ARN format.
func TestARN_sqsIncludesRegionAndAccount(t *testing.T) {
	// Given / When: we build an SQS ARN
	arn := protocol.QueueARN("us-east-1", "123456789012", "my-queue")

	// Then: region and account are included
	expected := "arn:aws:sqs:us-east-1:123456789012:my-queue"
	if arn != expected {
		t.Errorf("SQS ARN: expected %q, got %q", expected, arn)
	}
}

// ---- Error wrapping (cause) ------------------------------------------------

// TestWrap_preservesCauseInChain verifies that Wrap() attaches the cause and
// that the standard errors.Is / errors.As functions can inspect it.
func TestWrap_preservesCauseInChain(t *testing.T) {
	// Given: an underlying storage error and a template AWSError
	underlyingErr := errors.New("sqlite: disk I/O error")

	// When: we wrap the storage error with an AWSError
	wrapped := protocol.Wrap(protocol.ErrInternalError, underlyingErr)

	// Then: the AWS error code is preserved
	if wrapped.Code != "InternalError" {
		t.Errorf("expected Code InternalError, got %q", wrapped.Code)
	}

	// And: errors.Is traverses the chain to find the underlying error
	if !errors.Is(wrapped, underlyingErr) {
		t.Error("errors.Is should find the underlying error through the chain")
	}

	// And: errors.As can extract *AWSError from the chain
	var aerr *protocol.AWSError
	if !errors.As(wrapped, &aerr) {
		t.Error("errors.As should find *AWSError in the chain")
	}
	if aerr.Code != "InternalError" {
		t.Errorf("expected Code InternalError via errors.As, got %q", aerr.Code)
	}
}

// TestWrap_doesNotMutateTemplate verifies that Wrap() never modifies the
// sentinel template — this would be a concurrency bug if templates are shared.
func TestWrap_doesNotMutateTemplate(t *testing.T) {
	// Given: the sentinel ErrInternalError template
	originalMessage := protocol.ErrInternalError.Message

	// When: we wrap it with a cause
	cause := errors.New("some cause")
	wrapped := protocol.Wrap(protocol.ErrInternalError, cause)

	// Then: the template is unchanged
	if protocol.ErrInternalError.Message != originalMessage {
		t.Error("Wrap() must not mutate the template AWSError")
	}
	if protocol.Cause(protocol.ErrInternalError) != nil {
		t.Error("Wrap() must not set cause on the template")
	}

	// And: the wrapped copy has the cause
	if protocol.Cause(wrapped) != cause {
		t.Error("wrapped error should have the cause set")
	}
}

// TestWrap_causeNotLeakedToClient verifies that the underlying cause is never
// sent to the HTTP client — it is server-side only.
func TestWrap_causeNotLeakedToClient(t *testing.T) {
	// Given: an AWSError wrapping a sensitive internal error
	sensitiveErr := errors.New("internal db password: hunter2")
	aerr := protocol.Wrap(protocol.ErrInternalError, sensitiveErr)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protocol.WriteJSONError(w, r, aerr)
	})

	// When: the error is written to an HTTP response
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(protocol.ContextWithRequestID(req.Context(), "req-safe"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Then: the response body must not contain the sensitive cause text
	body := w.Body.String()
	if strings.Contains(body, "hunter2") {
		t.Errorf("cause must not be leaked to the client: body = %s", body)
	}
	if strings.Contains(body, "internal db password") {
		t.Errorf("cause must not be leaked to the client: body = %s", body)
	}
}

// TestAsAWSError_extractsFromChain verifies AsAWSError traverses nested wrapping.
func TestAsAWSError_extractsFromChain(t *testing.T) {
	// Given: an AWSError wrapped in a plain fmt.Errorf chain
	aerr := protocol.Wrap(protocol.ErrInternalError, errors.New("root cause"))
	outerErr := fmt.Errorf("service layer: %w", aerr)

	// When: we use AsAWSError
	got := protocol.AsAWSError(outerErr)

	// Then: we get the AWSError back
	if got == nil {
		t.Fatal("AsAWSError should find *AWSError in the chain")
	}
	if got.Code != "InternalError" {
		t.Errorf("expected InternalError, got %q", got.Code)
	}
}

// TestAsAWSError_returnsNilForNonAWSError verifies nil is returned cleanly.
func TestAsAWSError_returnsNilForNonAWSError(t *testing.T) {
	// Given: a plain error with no AWSError in its chain
	plainErr := fmt.Errorf("some plain error: %w", errors.New("root"))

	// When + Then: AsAWSError returns nil
	if got := protocol.AsAWSError(plainErr); got != nil {
		t.Errorf("expected nil for non-AWSError chain, got %v", got)
	}
}
