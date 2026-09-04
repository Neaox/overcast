package acm

import (
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil/dispatchtest"
	"github.com/overcast-sh/overcast/internal/state"
)

// newTestService returns an ACM Service wired for tests.
func newTestService(t *testing.T) *Service {
	t.Helper()
	store := state.NewMemoryStore()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	return New(cfg, store, zap.NewNop(), clock.NewMock())
}

// TestDispatch_unimplementedVsUnknownOperation pins the house rule that a
// real ACM operation Overcast has not implemented gets an honest 501, in the
// request's own wire format, rather than the 400 UnknownOperationException
// that would wrongly claim AWS has no such operation — and that a name AWS
// does not model keeps the 400 (#1645, #1656).
//
// ACM made this split inline; it now runs through the same
// serviceutil.WriteUnhandledOperation and the same contract as the other
// eleven JSON-tier services, which is what extends it to RPC v2 CBOR and to
// the no-codec X-Amz-Target fallback.
func TestDispatch_unimplementedVsUnknownOperation(t *testing.T) {
	// Given: ImportCertificate is a real, documented ACM operation
	// (internal/awsapi/manifest.gen.go) with no handler here.
	const unimplemented, unknown = "ImportCertificate", "NotAnACMOperation"
	svc := newTestService(t)
	if _, ok := svc.handler.ops[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if _, ok := svc.handler.typedOp[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	dispatchtest.AssertModeled(t, awsapiService, unimplemented, unknown)

	dispatchtest.AssertRefusals(t, svc.Dispatch, svc.TargetPrefix(), dispatchtest.UnimplementedVsUnknown(unimplemented, unknown, "UnknownOperationException"))
}
