package elasticache

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var ecOps = []string{
	"CreateCacheCluster", "DescribeCacheClusters", "DeleteCacheCluster",
	"CreateServerlessCache", "DescribeServerlessCaches", "DeleteServerlessCache", "ModifyServerlessCache",
	"CreateReplicationGroup", "DescribeReplicationGroups", "DeleteReplicationGroup",
	"CreateCacheSubnetGroup", "DescribeCacheSubnetGroups", "DeleteCacheSubnetGroup",
	"CreateCacheParameterGroup", "DescribeCacheParameterGroups", "DeleteCacheParameterGroup",
	"DescribeCacheParameters", "ModifyCacheCluster", "ModifyReplicationGroup",
}

func TestTypedOps_matchRegistry(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.handler.typedOp) == 0 {
		t.Fatal("typedOp is empty")
	}
	for _, name := range ecOps {
		operation, ok := s.handler.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
	}
}
