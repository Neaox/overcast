package serviceutil_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// dynamoDBForTest is a validated service key, obtained the way every caller
// obtains one.
var dynamoDBForTest = serviceutil.MustAWSService("dynamodb")

var errUnknownForTest = &protocol.AWSError{
	Code:       "UnknownOperationException",
	Message:    "Unknown operation",
	HTTPStatus: http.StatusBadRequest,
}

func TestWriteUnhandledOperation_modeledOperationIs501InRequestFormat(t *testing.T) {
	tests := []struct {
		name            string
		codec           codec.Codec
		wantContentType string
	}{
		{name: "aws json", codec: codec.JSON11, wantContentType: "application/x-amz-json-1.0"},
		{name: "rpc v2 cbor", codec: codec.RPCv2CBOR, wantContentType: "application/cbor"},
		{name: "query xml", codec: codec.QueryXML, wantContentType: "text/xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a real DynamoDB operation Overcast does not implement,
			// reached over the given wire protocol.
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
			w := httptest.NewRecorder()

			// When: the service refuses it.
			serviceutil.WriteUnhandledOperation(w, r, tt.codec, dynamoDBForTest, "CreateBackup", errUnknownForTest)

			// Then: an honest 501 in the request's own envelope, with the
			// unsupported marker the protocol layer adds to every 501.
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body = %s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); got != tt.wantContentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.wantContentType)
			}
			if got := w.Header().Get("x-emulator-unsupported"); got != "true" {
				t.Fatalf("x-emulator-unsupported = %q, want \"true\"", got)
			}
			if bytes.Contains(w.Body.Bytes(), []byte(errUnknownForTest.Code)) {
				t.Fatalf("unknown-operation error leaked into a 501: %s", w.Body.String())
			}
		})
	}
}

func TestWriteUnhandledOperation_unmodeledNameKeepsServiceError(t *testing.T) {
	// Given: a name the AWS models do not carry for the service — including
	// one that is real for a different service.
	for _, operation := range []string{"NotAnOperation", "ListQueues"} {
		t.Run(operation, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
			w := httptest.NewRecorder()

			// When: the service refuses it.
			serviceutil.WriteUnhandledOperation(w, r, codec.JSON10, dynamoDBForTest, operation, errUnknownForTest)

			// Then: the service's own unknown-operation answer, untouched by
			// the not-implemented marker.
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(errUnknownForTest.Code)) {
				t.Fatalf("expected %q in body, got: %s", errUnknownForTest.Code, w.Body.String())
			}
			if got := w.Header().Get("x-emulator-unsupported"); got != "" {
				t.Fatalf("x-emulator-unsupported = %q, want it absent for an unknown operation", got)
			}
		})
	}
}

// TestMustAWSService_rejectsAKeyTheModelsDoNotCarry pins the belt N4 asked
// for: WriteUnhandledOperation's answer is only as good as the service key it
// is handed, and a wrong key fails open — every real operation looks
// unmodeled, so the 400 the house rule replaces comes back. CloudWatch Logs
// hit exactly that with "logs", which is a modeled identity aliasing to
// "cloudwatch-logs" and not itself a key.
func TestMustAWSService_rejectsAKeyTheModelsDoNotCarry(t *testing.T) {
	tests := []struct {
		name, key string
		wantPanic bool
	}{
		{name: "established key", key: "dynamodb"},
		{name: "established key an alias targets", key: "cloudwatch-logs"},
		{name: "modeled identity that aliases to a key", key: "logs", wantPanic: true},
		{name: "modeled identity that aliases to a key, dotted", key: "streams.dynamodb", wantPanic: true},
		{name: "typo", key: "dynamodbb", wantPanic: true},
		{name: "empty", key: "", wantPanic: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// When: a service package declares its corpus key.
			var panicked bool
			func() {
				defer func() { panicked = recover() != nil }()
				_ = serviceutil.MustAWSService(tt.key)
			}()

			// Then: a key the corpus cannot answer for never reaches a request.
			if panicked != tt.wantPanic {
				t.Fatalf("MustAWSService(%q) panicked = %v, want %v", tt.key, panicked, tt.wantPanic)
			}
		})
	}
}
