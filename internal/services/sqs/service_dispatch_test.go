package sqs

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil/dispatchtest"
)

// TestDispatch_unimplementedVsUnknownOperation pins the house rule that a
// real SQS operation Overcast has not implemented gets an honest 501, in the
// request's own wire format, rather than the 400 InvalidAction that would
// wrongly claim AWS has no such operation, and that a name AWS does not model
// keeps the 400 in each format (#1645). The formats are JSON, RPC v2 CBOR
// (which used to fall through to the X-Amz-Target path and get a JSON 400
// whatever the operation) and Query XML, the queue-URL route's protocol.
func TestDispatch_unimplementedVsUnknownOperation(t *testing.T) {
	// Given: ListMessageMoveTasks is a real, documented SQS operation
	// (internal/awsapi/manifest.gen.go) with no handler here.
	const unimplemented, unknown = "ListMessageMoveTasks", "NotAnSQSOperation"
	svc := newTestSQSService(t)
	if _, ok := svc.handler.ops[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if _, ok := svc.handler.typedOp[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if !awsapi.HasOperation(serviceName, unimplemented) || awsapi.HasOperation(serviceName, unknown) {
		t.Fatalf("test setup: %q must be modeled and %q must not", unimplemented, unknown)
	}

	cases := dispatchtest.UnimplementedVsUnknown(unimplemented, unknown, "InvalidAction")
	cases = append(cases,
		dispatchtest.Refusal{Name: "real unimplemented operation over Query is an XML 501", Operation: unimplemented, Codec: codec.QueryXML, Status: http.StatusNotImplemented, Code: "NotImplemented"},
		dispatchtest.Refusal{Name: "unmodeled name over Query keeps 400 in XML", Operation: unknown, Codec: codec.QueryXML, Status: http.StatusBadRequest, Code: "InvalidAction"},
	)
	dispatchtest.AssertRefusals(t, svc.Dispatch, "AmazonSQS.", cases)
}
