package cloudwatch

import (
	"testing"

	"github.com/overcast-sh/overcast/internal/serviceutil/dispatchtest"
)

// TestDispatch_unimplementedVsUnknownOperation pins the house rule this
// service states in dispatchJSON — a real CloudWatch operation Overcast does
// not emulate gets an honest 501, in the request's own wire format, rather
// than the 400 UnknownOperationException that would wrongly claim AWS has no
// such operation — for an operation the old hand-kept case arm did not list,
// and that a name AWS does not model keeps the 400 (#1645).
func TestDispatch_unimplementedVsUnknownOperation(t *testing.T) {
	// Given: PutDashboard is a real, documented CloudWatch operation
	// (internal/awsapi/manifest.gen.go) with no handler on either protocol.
	const unimplemented, unknown = "PutDashboard", "NotACloudWatchOperation"
	svc := newTestService(t)
	if _, ok := svc.ops[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if _, ok := svc.typedOp[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	dispatchtest.AssertModeled(t, awsapiService, unimplemented, unknown)

	dispatchtest.AssertRefusals(t, svc.Dispatch, cloudwatchJSONTargetPrefix, dispatchtest.UnimplementedVsUnknown(unimplemented, unknown, "UnknownOperationException"))
}
