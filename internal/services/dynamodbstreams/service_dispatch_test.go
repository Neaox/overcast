package dynamodbstreams

import (
	"net/http"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil/dispatchtest"
)

// TestDispatch_unknownOperation pins that a name AWS does not model keeps 400
// UnknownOperationException in the request's own wire format, JSON or RPC v2
// CBOR (#1645). The 501 half of that rule, a real operation Overcast has not
// implemented, has nothing to exercise here; see
// TestDispatch_everyModeledOperationImplemented for why.
func TestDispatch_unknownOperation(t *testing.T) {
	// Given: a name no AWS model carries for DynamoDB Streams.
	const unknown = "NotAStreamsOperation"
	svc := New(nil, zap.NewNop())

	dispatchtest.AssertRefusals(t, svc.Dispatch, svc.TargetPrefix(), []dispatchtest.Refusal{
		{Name: "over JSON", Operation: unknown, Codec: codec.JSON10, Status: http.StatusBadRequest, Code: "UnknownOperationException"},
		{Name: "over RPC v2 CBOR", Operation: unknown, Codec: codec.RPCv2CBOR, Status: http.StatusBadRequest, Code: "UnknownOperationException"},
		{Name: "without a codec", Operation: unknown, Codec: nil, Status: http.StatusBadRequest, Code: "UnknownOperationException"},
	})
}

// TestDispatch_everyModeledOperationImplemented records why the 501 arm of
// the refusal in Dispatch has no case to pin: every DynamoDB Streams
// operation in the pinned models has a typed handler. When a model refresh
// adds one, this fails, and the new operation is either implemented or gets
// its 501 case in TestDispatch_unknownOperation.
func TestDispatch_everyModeledOperationImplemented(t *testing.T) {
	svc := New(nil, zap.NewNop())
	awsapi.WalkOperations(func(op awsapi.Operation) bool {
		if awsapi.ServiceKey(op.Service) != serviceName {
			return true
		}
		if _, ok := svc.handler.typedOp[op.Name]; !ok {
			t.Errorf("%s is modeled but has no typed handler; implement it or pin its 501", op.Name)
		}
		return true
	})
}
