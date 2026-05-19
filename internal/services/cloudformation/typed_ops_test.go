package cloudformation

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var cfnOps = []string{
	"CreateStack", "UpdateStack", "DeleteStack",
	"DescribeStacks", "ListStacks",
	"GetTemplate",
	"CreateChangeSet", "DescribeChangeSet", "ExecuteChangeSet", "DeleteChangeSet", "ListChangeSets",
	"DescribeStackResources", "ListStackResources",
	"DescribeStackEvents",
	"GetTemplateSummary", "ValidateTemplate",
	"ListExports", "ListImports",
}

func TestTypedOps_matchLegacyRegistry(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.handler.typedOp) != len(cfnOps) {
		t.Fatalf("typed op count = %d, want %d", len(s.handler.typedOp), len(cfnOps))
	}
	for _, name := range cfnOps {
		operation, ok := s.handler.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
	}
}
