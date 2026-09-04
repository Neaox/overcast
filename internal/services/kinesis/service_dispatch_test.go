package kinesis

import (
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil/dispatchtest"
	"github.com/overcast-sh/overcast/internal/state"
)

// TestDispatch_unimplementedVsUnknownOperation pins the house rule that a
// real Kinesis operation Overcast has not implemented gets an honest 501, in
// the request's own wire format, rather than the 400 UnknownOperationException
// that would wrongly claim AWS has no such operation, and that a name AWS
// does not model keeps the 400 (#1645).
func TestDispatch_unimplementedVsUnknownOperation(t *testing.T) {
	// Given: DescribeLimits is a real, documented Kinesis operation
	// (internal/awsapi/manifest.gen.go) with no handler here.
	const unimplemented, unknown = "DescribeLimits", "NotAKinesisOperation"
	svc := New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
	if _, ok := svc.handler.ops[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if _, ok := svc.handler.typedOp[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if !awsapi.HasOperation(serviceName, unimplemented) || awsapi.HasOperation(serviceName, unknown) {
		t.Fatalf("test setup: %q must be modeled and %q must not", unimplemented, unknown)
	}

	dispatchtest.AssertRefusals(t, svc.Dispatch, targetPrefix, dispatchtest.UnimplementedVsUnknown(unimplemented, unknown, "UnknownOperationException"))
}
