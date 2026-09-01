package autoscaling

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"
)

// asgOps is the operation set Auto Scaling registers. Keeping it in the test
// makes adding an operation to the service without registering it — or the
// other way round — a failing test rather than a silent 501.
var asgOps = []string{
	"CreateAutoScalingGroup", "UpdateAutoScalingGroup",
	"DescribeAutoScalingGroups", "DeleteAutoScalingGroup",
	"SetDesiredCapacity", "TerminateInstanceInAutoScalingGroup",
	"CreateLaunchConfiguration", "DescribeLaunchConfigurations", "DeleteLaunchConfiguration",
	"PutScalingPolicy", "DescribePolicies", "DeletePolicy", "ExecutePolicy",
	"PutLifecycleHook", "DescribeLifecycleHooks", "DeleteLifecycleHook",
	"CompleteLifecycleAction", "RecordLifecycleActionHeartbeat",
	"CreateOrUpdateTags", "DeleteTags", "DescribeTags",
	"DescribeAutoScalingInstances", "DescribeScalingActivities",
	"SetInstanceHealth", "SetInstanceProtection",
}

func TestTypedOps_coverTheRegisteredOperationSet(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	t.Cleanup(func() { s.Stop(context.Background()) })

	if len(s.handler.typedOp) != len(asgOps) {
		t.Fatalf("typed op count = %d, want %d", len(s.handler.typedOp), len(asgOps))
	}
	for _, name := range asgOps {
		operation, ok := s.handler.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
	}
	if got := s.SupportedProtocols(); len(got) != 1 || got[0] != codec.QueryXML {
		t.Errorf("SupportedProtocols = %v, want [QueryXML]", got)
	}
	if len(s.Operations()) != len(asgOps) {
		t.Errorf("Operations() returned %d, want %d", len(s.Operations()), len(asgOps))
	}
}
