// Package dispatchtest drives a service's Dispatch the way middleware.Protocol
// would — codec and operation already in the request context — for the
// refusal cases every X-Amz-Target service shares. It exists so the twelve
// services that pin #1645 assert one contract from one place rather than
// twelve hand-copied tables.
package dispatchtest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// AssertModeled fails unless service is an established Overcast service key
// whose modeled operations include unimplemented and exclude unknown — the
// setup every UnimplementedVsUnknown table rests on.
//
// The key half is the load-bearing one. serviceutil.WriteUnhandledOperation
// asks the model corpus about a (key, operation) pair, so a key the corpus
// does not carry answers "not modeled" for every real operation and silently
// keeps the 400 the rule exists to replace. serviceutil.MustAWSService rules
// that out at package initialisation; this is the same check stated where the
// service's expectations are written down.
func AssertModeled(t *testing.T, service serviceutil.AWSService, unimplemented, unknown string) {
	t.Helper()
	if !awsapi.IsServiceKey(string(service)) {
		t.Fatalf("test setup: %q is not an established Overcast service key", service)
	}
	if !awsapi.KnownOperation(string(service), unimplemented) {
		t.Fatalf("test setup: %q is not a modeled %s operation, so it cannot pin the 501", unimplemented, service)
	}
	if awsapi.KnownOperation(string(service), unknown) {
		t.Fatalf("test setup: %q is a modeled %s operation, so it cannot pin the 400", unknown, service)
	}
}

// Refusal is one operation a service is expected to refuse, and how.
type Refusal struct {
	Name      string
	Operation string
	// Codec is the wire protocol the request is identified as. nil sends a
	// bare AWS JSON request carrying X-Amz-Target and no codec context — the
	// fallback path a caller reaches without middleware.Protocol.
	Codec  codec.Codec
	Status int
	// Code is the error code the body must name.
	Code string
}

// UnimplementedVsUnknown is the contract every JSON-tier service pins for
// #1645: a real, modeled operation Overcast has not implemented is 501 in the
// request's own wire format, over AWS JSON, over RPC v2 CBOR and on the
// no-codec fallback; a name AWS does not model keeps unknownCode, again in
// each format.
func UnimplementedVsUnknown(unimplemented, unknown, unknownCode string) []Refusal {
	return []Refusal{
		{Name: "real unimplemented operation over JSON is 501", Operation: unimplemented, Codec: codec.JSON11, Status: http.StatusNotImplemented, Code: "NotImplemented"},
		{Name: "real unimplemented operation over RPC v2 CBOR is a CBOR 501", Operation: unimplemented, Codec: codec.RPCv2CBOR, Status: http.StatusNotImplemented, Code: "NotImplemented"},
		{Name: "real unimplemented operation without a codec is 501", Operation: unimplemented, Codec: nil, Status: http.StatusNotImplemented, Code: "NotImplemented"},
		{Name: "unmodeled name over JSON keeps 400", Operation: unknown, Codec: codec.JSON11, Status: http.StatusBadRequest, Code: unknownCode},
		{Name: "unmodeled name over RPC v2 CBOR keeps 400 in CBOR", Operation: unknown, Codec: codec.RPCv2CBOR, Status: http.StatusBadRequest, Code: unknownCode},
		{Name: "unmodeled name without a codec keeps 400", Operation: unknown, Codec: nil, Status: http.StatusBadRequest, Code: unknownCode},
	}
}

// AssertRefusals runs each case through dispatch and asserts the status, that
// the error envelope is in the request's wire format, that the
// x-emulator-unsupported marker is present exactly when the status is 501,
// and that the body names the expected code. targetPrefix builds the
// X-Amz-Target for the no-codec cases.
func AssertRefusals(t *testing.T, dispatch http.HandlerFunc, targetPrefix string, cases []Refusal) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			// Given: the operation reaches Dispatch over the given protocol.
			r, wantContentType := Request(t, tc.Codec, targetPrefix, tc.Operation)

			// When: the service dispatches it.
			w := httptest.NewRecorder()
			dispatch(w, r)

			// Then: refused as expected, in the request's own envelope.
			if w.Code != tc.Status {
				t.Fatalf("Dispatch %s: status = %d, want %d; body = %s", tc.Operation, w.Code, tc.Status, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); got != wantContentType {
				t.Fatalf("Dispatch %s: Content-Type = %q, want %q", tc.Operation, got, wantContentType)
			}
			wantMarker := ""
			if tc.Status == http.StatusNotImplemented {
				wantMarker = "true"
			}
			if got := w.Header().Get("x-emulator-unsupported"); got != wantMarker {
				t.Fatalf("Dispatch %s: x-emulator-unsupported = %q, want %q", tc.Operation, got, wantMarker)
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tc.Code)) {
				t.Fatalf("Dispatch %s: expected %q in body, got: %q", tc.Operation, tc.Code, w.Body.String())
			}
		})
	}
}

// Request builds the request AssertRefusals sends for one case: an empty
// document in c's encoding with the codec and operation in context, or, for
// a nil c, a bare AWS JSON 1.0 request naming the operation in X-Amz-Target.
// It also returns the Content-Type an error written in that format carries.
func Request(t *testing.T, c codec.Codec, targetPrefix, operation string) (*http.Request, string) {
	t.Helper()
	if c == nil {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
		r.Header.Set("Content-Type", "application/x-amz-json-1.0")
		r.Header.Set("X-Amz-Target", targetPrefix+operation)
		return r, "application/x-amz-json-1.0"
	}
	ctx := codec.WithDispatch(context.Background(), c, operation)
	switch c.Name() {
	case codec.NameRPCv2CBOR:
		// 0xa0 is an empty CBOR map. The path is what a Smithy RPC v2 client
		// sends; Dispatch takes the operation from the context, not the path.
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/service/Test/operation/"+operation, bytes.NewReader([]byte{0xa0}))
		r.Header.Set("Content-Type", "application/cbor")
		r.Header.Set("Smithy-Protocol", "rpc-v2-cbor")
		return r, "application/cbor"
	case codec.NameAWSQuery:
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader([]byte("Action="+operation)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r, "text/xml"
	default:
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader([]byte(`{}`)))
		r.Header.Set("Content-Type", "application/x-amz-json-1.1")
		// protocol.WriteJSONError writes every JSON error envelope as 1.0.
		return r, "application/x-amz-json-1.0"
	}
}
