package sqs

import (
	"net/http"
	"testing"

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
	dispatchtest.AssertModeled(t, awsapiService, unimplemented, unknown)

	cases := dispatchtest.UnimplementedVsUnknown(unimplemented, unknown, "InvalidAction")
	cases = append(cases,
		dispatchtest.Refusal{Name: "real unimplemented operation over Query is an XML 501", Operation: unimplemented, Codec: codec.QueryXML, Status: http.StatusNotImplemented, Code: "NotImplemented"},
		dispatchtest.Refusal{Name: "unmodeled name over Query keeps 400 in XML", Operation: unknown, Codec: codec.QueryXML, Status: http.StatusBadRequest, Code: "InvalidAction"},
	)
	dispatchtest.AssertRefusals(t, svc.Dispatch, "AmazonSQS.", cases)
}

// TestDispatchQuery_opsCoversEveryTypedOperation pins the assumption
// DispatchQuery and OwnsAction both rest on: the queue-URL Query route
// resolves an Action through handler.ops alone, so an operation that were
// ever registered typed-only would be refused there — and, since the refusal
// now consults the model corpus, refused with a 501 claiming Overcast does
// not emulate something it does emulate over JSON. That is a worse answer
// than the InvalidAction it replaced, so the two maps have to stay in step.
//
// The fix if this fails is to give the Query route the same typed fallback
// Dispatch has, not to shrink typedOp.
func TestDispatchQuery_opsCoversEveryTypedOperation(t *testing.T) {
	// Given: SQS's two operation registries.
	svc := newTestSQSService(t)

	// Then: every typed operation is also reachable by name through ops,
	// which is the only map the Query route consults.
	for name := range svc.handler.typedOp {
		if _, ok := svc.handler.ops[name]; !ok {
			t.Errorf("%s is registered typed-only; the queue-URL Query route would refuse it with a 501", name)
		}
	}
}
