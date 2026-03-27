// Package protocol provides shared AWS protocol helpers used by all service
// handlers: error serialisation, request IDs, ARN construction, and response
// writing. Nothing in this package is service-specific.
package protocol

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
)

// ---- Error types -----------------------------------------------------------

// AWSError represents a structured AWS API error that maps to an HTTP response.
//
// It implements the standard error interface and supports Go's error wrapping
// convention, equivalent to JavaScript's `new Error("msg", { cause: err })`.
//
// Wrapping pattern — use Wrap() to attach an underlying cause while presenting
// a clean AWS error to callers:
//
//	// Service code:
//	raw, found, err := s.store.Get(ctx, ns, key)
//	if err != nil {
//	    return protocol.Wrap(protocol.ErrInternalError, err)
//	}
//
//	// The HTTP layer sees ErrInternalError (clean AWS error code + message).
//	// The original err is preserved in the chain for logging and debugging:
//	logger.Error("state read failed", zap.Error(aerr))
//	// → logs both "InternalError" and the underlying storage error
//
// Inspection pattern — anywhere in the call chain:
//
//	var aerr *protocol.AWSError
//	if errors.As(err, &aerr) {
//	    // aerr.Code, aerr.HTTPStatus available
//	}
type AWSError struct {
	// Code is the AWS error code string, e.g. "NoSuchBucket", "QueueDoesNotExist".
	Code string
	// Message is the human-readable description sent to the client.
	Message string
	// HTTPStatus is the HTTP status code to send, e.g. 404, 400, 500.
	HTTPStatus int
	// cause is the underlying error that triggered this AWSError.
	// It is not sent to clients — it is for internal logging and error chain
	// inspection only. Equivalent to JavaScript's Error.cause.
	cause error
}

// Error implements the error interface.
// Returns the AWS error code and message; the cause is accessible via Unwrap.
func (e *AWSError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s (cause: %v)", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause, enabling errors.Is / errors.As to
// traverse the full error chain. This is the Go equivalent of error.cause
// in JavaScript.
//
//	// Check if a specific underlying error occurred:
//	if errors.Is(aerr, sql.ErrNoRows) { ... }
//
//	// Extract a specific error type from anywhere in the chain:
//	var pgErr *pgconn.PgError
//	if errors.As(aerr, &pgErr) { ... }
func (e *AWSError) Unwrap() error {
	return e.cause
}

// Wrap returns a new *AWSError with the same Code, Message, and HTTPStatus as
// template, but with cause attached as the underlying error.
//
// This is the primary way to preserve error context in service code:
//
//	func (s *s3Store) getBucket(ctx context.Context, name string) (*Bucket, *AWSError) {
//	    raw, found, err := s.store.Get(ctx, nsBuckets, name)
//	    if err != nil {
//	        // ErrInternalError is shown to the client.
//	        // err (e.g. "sqlite: no such table") is preserved for logging.
//	        return nil, protocol.Wrap(ErrInternalError, err)
//	    }
//	    ...
//	}
//
// Wrap never modifies the template — it always returns a new AWSError value.
func Wrap(template *AWSError, cause error) *AWSError {
	return &AWSError{
		Code:       template.Code,
		Message:    template.Message,
		HTTPStatus: template.HTTPStatus,
		cause:      cause,
	}
}

// Cause returns the underlying cause of aerr, or nil if none was set.
// Prefer errors.Is / errors.As over Cause for most use cases — they traverse
// arbitrarily deep chains. Use Cause only when you need the immediate cause.
func Cause(aerr *AWSError) error {
	return aerr.cause
}

// ---- Sentinel errors -------------------------------------------------------
// These are templates — never mutate them. Use Wrap() to attach a cause.

var (
	// ErrNotImplemented is returned for endpoints that exist in the routing
	// table but have not yet been implemented. The x-emulator-unsupported
	// header is added automatically by WriteXMLError / WriteJSONError.
	ErrNotImplemented = &AWSError{
		Code:       "NotImplemented",
		Message:    "This operation is not yet emulated. Check docs/services/ for the support matrix.",
		HTTPStatus: http.StatusNotImplemented,
	}

	// ErrInternalError is returned when the emulator itself encounters an
	// unexpected failure (state backend error, serialisation failure, etc.).
	// Always Wrap() this with the underlying error for log context.
	ErrInternalError = &AWSError{
		Code:       "InternalError",
		Message:    "An internal error occurred.",
		HTTPStatus: http.StatusInternalServerError,
	}
)

// ErrInvalidArgument returns a 400 error for malformed input.
func ErrInvalidArgument(msg string) *AWSError {
	return &AWSError{Code: "InvalidArgument", Message: msg, HTTPStatus: http.StatusBadRequest}
}

// ErrMissingParameter returns a 400 error for a missing required parameter.
func ErrMissingParameter(param string) *AWSError {
	return &AWSError{
		Code:       "MissingParameter",
		Message:    fmt.Sprintf("The request must contain the parameter %s.", param),
		HTTPStatus: http.StatusBadRequest,
	}
}

// AsAWSError extracts the first *AWSError from the error chain.
// Returns nil if no *AWSError is found.
//
// This is a convenience wrapper around errors.As for the common case of
// checking whether an error from a helper function is an AWSError:
//
//	aerr := protocol.AsAWSError(err)
//	if aerr != nil {
//	    protocol.WriteJSONError(w, r, aerr)
//	    return
//	}
func AsAWSError(err error) *AWSError {
	var aerr *AWSError
	if errors.As(err, &aerr) {
		return aerr
	}
	return nil
}

// ---- XML error format (S3 and other REST-XML services) ---------------------

// xmlErrorResponse is the wire envelope S3 uses for errors.
type xmlErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	RequestID string   `xml:"RequestId"`
}

// WriteXMLError writes an AWS REST-XML error response (S3 format).
// The cause, if any, is NOT included in the response — it stays server-side
// for logging. The x-emulator-unsupported header is set automatically for 501.
func WriteXMLError(w http.ResponseWriter, r *http.Request, aerr *AWSError) {
	reqID := RequestIDFromContext(r.Context())

	body, _ := xml.Marshal(&xmlErrorResponse{
		Code:      aerr.Code,
		Message:   aerr.Message,
		RequestID: reqID,
	})

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-request-id", reqID)
	if aerr.HTTPStatus == http.StatusNotImplemented {
		w.Header().Set("x-emulator-unsupported", "true")
	}
	w.WriteHeader(aerr.HTTPStatus)
	w.Write([]byte(xml.Header))
	w.Write(body)
}

// ---- JSON error format (SQS, SNS, DynamoDB, Lambda) -----------------------

// jsonErrorResponse is the wire envelope used by JSON-protocol services.
type jsonErrorResponse struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// WriteJSONError writes an AWS JSON-protocol error response.
// The cause, if any, is NOT included in the response body.
func WriteJSONError(w http.ResponseWriter, r *http.Request, aerr *AWSError) {
	reqID := RequestIDFromContext(r.Context())

	body, _ := json.Marshal(&jsonErrorResponse{
		Type:    aerr.Code,
		Message: aerr.Message,
	})

	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("x-amzn-requestid", reqID)
	if aerr.HTTPStatus == http.StatusNotImplemented {
		w.Header().Set("x-emulator-unsupported", "true")
	}
	w.WriteHeader(aerr.HTTPStatus)
	w.Write(body)
}

// NotImplementedXML is a convenience handler for unimplemented S3 endpoints.
func NotImplementedXML(w http.ResponseWriter, r *http.Request) {
	WriteXMLError(w, r, ErrNotImplemented)
}

// NotImplementedJSON is a convenience handler for unimplemented JSON endpoints.
func NotImplementedJSON(w http.ResponseWriter, r *http.Request) {
	WriteJSONError(w, r, ErrNotImplemented)
}
