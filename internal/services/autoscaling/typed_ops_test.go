package autoscaling

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var asgOps = []string{
	"CreateAutoScalingGroup", "UpdateAutoScalingGroup",
	"DescribeAutoScalingGroups", "DeleteAutoScalingGroup",
	"SetDesiredCapacity", "TerminateInstanceInAutoScalingGroup",
	"CreateLaunchConfiguration", "DescribeLaunchConfigurations", "DeleteLaunchConfiguration",
	"PutScalingPolicy", "DescribePolicies", "DeletePolicy",
	"PutLifecycleHook", "DescribeLifecycleHooks", "DeleteLifecycleHook",
	"CreateOrUpdateTags", "DeleteTags", "DescribeTags",
	"DescribeAutoScalingInstances",
}

func TestTypedOps_matchLegacyRegistry(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.handler.typedOp) != len(asgOps) {
		t.Fatalf("typed op count = %d, want legacy count %d", len(s.handler.typedOp), len(asgOps))
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
}
