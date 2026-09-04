package cloudtrail

import (
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil/dispatchtest"
	"github.com/overcast-sh/overcast/internal/state"
)

// TestDispatch_unimplementedVsUnknownOperation pins the house rule that a
// real CloudTrail operation Overcast has not implemented gets an honest 501,
// in the request's own wire format, rather than the 400 InvalidAction that
// would wrongly claim AWS has no such operation — and that a name AWS does
// not model keeps the 400 (#1645).
func TestDispatch_unimplementedVsUnknownOperation(t *testing.T) {
	// Given: CreateEventDataStore is a real, documented CloudTrail operation
	// (internal/awsapi/manifest.gen.go) with no handler here.
	const unimplemented, unknown = "CreateEventDataStore", "NotACloudTrailOperation"
	svc := New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), nil)
	if _, ok := svc.handler.ops[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if _, ok := svc.handler.typedOp[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if !awsapi.HasOperation(serviceName, unimplemented) || awsapi.HasOperation(serviceName, unknown) {
		t.Fatalf("test setup: %q must be modeled and %q must not", unimplemented, unknown)
	}

	dispatchtest.AssertRefusals(t, svc.Dispatch, svc.TargetPrefix(), dispatchtest.UnimplementedVsUnknown(unimplemented, unknown, "InvalidAction"))
}
