package dynamodb

import (
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/serviceutil/dispatchtest"
	"github.com/overcast-sh/overcast/internal/state"
)

// TestDispatch_unimplementedVsUnknownOperation pins the house rule that a
// real DynamoDB operation Overcast has not implemented gets an honest 501, in
// the request's own wire format. The RPC v2 CBOR case is the one that used
// to fall through to the X-Amz-Target path, which has no header to read for
// a CBOR request and answered a JSON 400 whatever the operation. A name AWS
// does not model keeps 400 UnknownOperationException (#1645).
func TestDispatch_unimplementedVsUnknownOperation(t *testing.T) {
	// Given: CreateBackup is a real, documented DynamoDB operation
	// (internal/awsapi/manifest.gen.go) with no handler here.
	const unimplemented, unknown = "CreateBackup", "NotADynamoDBOperation"
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.NewMock(), events.NewBus())
	if _, ok := svc.handler.rawOp[unimplemented]; ok {
		t.Fatalf("test setup: %q is implemented, pick an unimplemented operation", unimplemented)
	}
	if !awsapi.HasOperation(serviceName, unimplemented) || awsapi.HasOperation(serviceName, unknown) {
		t.Fatalf("test setup: %q must be modeled and %q must not", unimplemented, unknown)
	}

	dispatchtest.AssertRefusals(t, svc.Dispatch, svc.TargetPrefix(), dispatchtest.UnimplementedVsUnknown(unimplemented, unknown, "UnknownOperationException"))
}
